package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const sessionRequestPrefix = "session:"

type SessionSyncResult struct {
	FilesScanned int
	Imported     int
	Skipped      int
	Errors       []string
}

type claudeSessionLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	Message   struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func DefaultClaudeProjectsDir() string {
	if dir := os.Getenv("CLAUDE_PROJECTS_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

func StartClaudeSessionSync(ctx context.Context, store *Store, projectsDir string, interval time.Duration) {
	if projectsDir == "" {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		syncAndLog(store, projectsDir)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				syncAndLog(store, projectsDir)
			}
		}
	}()
}

func SyncClaudeSessionLogs(store *Store, projectsDir string) (SessionSyncResult, error) {
	var result SessionSyncResult
	if store == nil || projectsDir == "" {
		return result, nil
	}
	if _, err := os.Stat(projectsDir); err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}

	err := filepath.WalkDir(projectsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Errors = append(result.Errors, walkErr.Error())
			return nil
		}
		if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		fileResult, err := syncClaudeSessionFile(store, path)
		result.FilesScanned++
		result.Imported += fileResult.Imported
		result.Skipped += fileResult.Skipped
		result.Errors = append(result.Errors, fileResult.Errors...)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func syncAndLog(store *Store, projectsDir string) {
	result, err := SyncClaudeSessionLogs(store, projectsDir)
	if err != nil {
		log.Printf("同步 Claude session usage 失败: %v", err)
		return
	}
	if len(result.Errors) > 0 {
		log.Printf("同步 Claude session usage 部分失败: files=%d imported=%d skipped=%d errors=%d", result.FilesScanned, result.Imported, result.Skipped, len(result.Errors))
	}
}

func syncClaudeSessionFile(store *Store, path string) (SessionSyncResult, error) {
	var result SessionSyncResult
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()

	info, _ := file.Stat()
	candidates := make(map[string]claudeSessionLine)
	incomplete := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var lineOffset int64
	for scanner.Scan() {
		lineOffset++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var line claudeSessionLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s:%d: %v", path, lineOffset, err))
			continue
		}
		if line.Type != "assistant" || line.Message.ID == "" {
			continue
		}
		if !line.hasFinalUsage() {
			if _, ok := candidates[line.Message.ID]; !ok {
				incomplete[line.Message.ID] = true
			}
			continue
		}
		existing, ok := candidates[line.Message.ID]
		if !ok || line.Message.Usage.OutputTokens >= existing.Message.Usage.OutputTokens {
			candidates[line.Message.ID] = line
		}
		delete(incomplete, line.Message.ID)
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}

	for _, line := range candidates {
		inserted, err := store.recordIfAbsent(sessionRequest(line), sessionToken(line))
		if err != nil {
			return result, err
		}
		if inserted {
			result.Imported++
		} else {
			result.Skipped++
		}
	}
	result.Skipped += len(incomplete)
	if err := store.recordSessionSyncFile(path, infoModTimeNanos(info), lineOffset); err != nil {
		return result, err
	}
	return result, nil
}

func (line claudeSessionLine) hasFinalUsage() bool {
	return line.Message.StopReason != "" && line.Message.Usage.OutputTokens > 0
}

func sessionRequest(line claudeSessionLine) RequestRecord {
	startedAt := parseSessionTimestamp(line.Timestamp)
	status := 200
	endedAt := startedAt
	return RequestRecord{
		ID:               sessionRequestPrefix + line.Message.ID,
		StartedAt:        startedAt,
		EndedAt:          &endedAt,
		StatusCode:       &status,
		Method:           "SESSION",
		RequestPath:      "session_log",
		ProviderID:       "_session",
		ProviderName:     "Session Log",
		SourceApp:        "claude_code",
		SourceEntrypoint: "session_log",
		OriginalModel:    line.Message.Model,
		MappedModel:      line.Message.Model,
		Stream:           true,
	}
}

func sessionToken(line claudeSessionLine) TokenRecord {
	return TokenRecord{
		RequestID:                sessionRequestPrefix + line.Message.ID,
		InputTokens:              line.Message.Usage.InputTokens,
		OutputTokens:             line.Message.Usage.OutputTokens,
		CacheCreationInputTokens: line.Message.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     line.Message.Usage.CacheReadInputTokens,
		UsageSource:              UsageSourceSessionLog,
		UsageParseStatus:         ParseStatusOK,
	}
}

func parseSessionTimestamp(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Now().UTC()
	}
	return t.UTC()
}

func infoModTimeNanos(info os.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

func (s *Store) recordSessionSyncFile(path string, modifiedNanos, lineOffset int64) error {
	_, err := s.db.Exec(
		`INSERT INTO session_log_sync(file_path, last_modified, last_line_offset, last_synced_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(file_path) DO UPDATE SET
			last_modified = excluded.last_modified,
			last_line_offset = excluded.last_line_offset,
			last_synced_at = excluded.last_synced_at`,
		path,
		modifiedNanos,
		lineOffset,
		formatTime(time.Now()),
	)
	return err
}
