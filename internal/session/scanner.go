package session

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	listHeadLines = 10
	listTailLines = 30
	scanCacheTTL  = 30 * time.Second
)

var (
	scanCacheMu sync.Mutex
	scanCache   = map[string]scanCacheEntry{}
)

type scanCacheEntry struct {
	at       time.Time
	sessions []Session
	err      error
}

func DefaultProjectsDir() string {
	if dir := os.Getenv("CLAUDE_PROJECTS_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

func ScanProjects(root string) ([]Project, error) {
	sessions, err := ScanSessions(root, "")
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]*Project)
	for _, sess := range sessions {
		key := projectKey(sess.ProjectPath)
		project := byPath[key]
		if project == nil {
			project = &Project{
				Path: sess.ProjectPath,
				Name: projectName(sess.ProjectPath),
			}
			byPath[key] = project
		}
		project.SessionCount++
		if sess.LastActiveAt.After(project.LastActiveAt) {
			project.LastActiveAt = sess.LastActiveAt
		}
	}
	projects := make([]Project, 0, len(byPath))
	for _, project := range byPath {
		projects = append(projects, *project)
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].LastActiveAt.After(projects[j].LastActiveAt)
	})
	return projects, nil
}

func ScanSessions(root string, projectPath string) ([]Session, error) {
	sessions, err := scanSessionsCached(root)
	if err != nil {
		return nil, err
	}
	filterKey := ""
	if projectPath != "" {
		filterKey = projectKey(normalizeProjectPath(projectPath))
	}
	filtered := make([]Session, 0, len(sessions))
	for _, sess := range sessions {
		if filterKey != "" && projectKey(sess.ProjectPath) != filterKey {
			continue
		}
		filtered = append(filtered, sess)
	}
	return filtered, nil
}

func scanSessionsCached(root string) ([]Session, error) {
	if root == "" {
		return nil, nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	now := time.Now()
	scanCacheMu.Lock()
	if cached, ok := scanCache[absRoot]; ok && now.Sub(cached.at) < scanCacheTTL {
		sessions := append([]Session(nil), cached.sessions...)
		err := cached.err
		scanCacheMu.Unlock()
		return sessions, err
	}
	scanCacheMu.Unlock()

	var sessions []Session
	err = filepath.WalkDir(absRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(path) != ".jsonl" || strings.HasPrefix(entry.Name(), "agent-") {
			return nil
		}
		sess, ok := scanSessionFile(path)
		if !ok {
			return nil
		}
		sessions = append(sessions, sess)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastActiveAt.After(sessions[j].LastActiveAt)
	})
	scanCacheMu.Lock()
	scanCache[absRoot] = scanCacheEntry{at: now, sessions: append([]Session(nil), sessions...), err: err}
	scanCacheMu.Unlock()
	return append([]Session(nil), sessions...), nil
}

func scanSessionFile(path string) (Session, bool) {
	info, _ := os.Stat(path)
	lines := readListWindow(path)
	if len(lines) == 0 {
		return Session{}, false
	}
	meta := sessionFileMeta{
		ID:        strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		CWD:       "Unknown Project",
		CreatedAt: fallbackModTime(info),
		LastAt:    fallbackModTime(info),
	}
	for _, raw := range lines {
		line, ok := parseJSONLine(raw)
		if !ok || line.IsMeta {
			continue
		}
		if line.SessionID != "" {
			meta.ID = line.SessionID
		}
		if line.CWD != "" {
			meta.CWD = normalizeProjectPath(line.CWD)
		}
		if ts, ok := parseTimestamp(line.Timestamp); ok {
			if meta.CreatedAt.IsZero() || ts.Before(meta.CreatedAt) {
				meta.CreatedAt = ts
			}
			meta.LastAt = ts
		}
		if line.Type != "summary" && line.Type != "custom-title" && line.Message.Role != "" {
			meta.MessageCount++
		}
	}
	title := ExtractTitle(firstN(lines, listHeadLines))
	if title == "" {
		title = projectName(meta.CWD)
	}
	if title == "" || title == "." || title == string(filepath.Separator) {
		title = shortID(meta.ID)
	}
	return Session{
		ID:           meta.ID,
		Title:        title,
		ProjectPath:  meta.CWD,
		SourcePath:   path,
		CreatedAt:    meta.CreatedAt,
		LastActiveAt: meta.LastAt,
		MessageCount: meta.MessageCount,
	}, true
}

func readListWindow(path string) []string {
	head := readHeadLines(path, listHeadLines)
	tail := readTailLines(path, listTailLines)
	if len(head) == 0 {
		return tail
	}
	lines := append([]string(nil), head...)
	for _, line := range tail {
		if !containsString(lines, line) {
			lines = append(lines, line)
		}
	}
	return lines
}

func readHeadLines(path string, limit int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= limit {
			break
		}
	}
	return lines
}

func readTailLines(path string, limit int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return nil
	}
	const chunkSize int64 = 64 * 1024
	var data []byte
	var offset = info.Size()
	for offset > 0 && countNewlines(data) <= limit {
		readSize := chunkSize
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize
		chunk := make([]byte, readSize)
		if _, err := file.ReadAt(chunk, offset); err != nil && err != io.EOF {
			return nil
		}
		data = append(chunk, data...)
	}
	lines := splitLines(string(data))
	if len(lines) <= limit {
		return lines
	}
	return lines[len(lines)-limit:]
}

func countNewlines(data []byte) int {
	count := 0
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	return count
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func firstN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}

type sessionFileMeta struct {
	ID           string
	CWD          string
	CreatedAt    time.Time
	LastAt       time.Time
	MessageCount int
}

func splitLines(content string) []string {
	rawLines := strings.Split(content, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func normalizeProjectPath(path string) string {
	if path == "" {
		return "Unknown Project"
	}
	return filepath.Clean(path)
}

func projectKey(path string) string {
	cleaned := normalizeProjectPath(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

func projectName(path string) string {
	if path == "" || path == "Unknown Project" {
		return "Unknown Project"
	}
	normalized := strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(strings.TrimRight(normalized, "/"), "/")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func fallbackModTime(info os.FileInfo) time.Time {
	if info == nil {
		return time.Time{}
	}
	return info.ModTime().UTC()
}
