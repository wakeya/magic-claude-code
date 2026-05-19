package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSyncClaudeSessionLogsImportsFinalAssistantUsage(t *testing.T) {
	store := newTestStore(t)
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "project-a")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeSessionLog(t, filepath.Join(projectDir, "session-1.jsonl"),
		`{"type":"user","message":{"id":"user-1"}}`,
		`{"type":"assistant","timestamp":"2026-05-18T15:19:00Z","sessionId":"sess-1","message":{"id":"msg_1","model":"MiniMax-M2.7-highspeed","usage":{"input_tokens":12,"output_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}`,
		`{"type":"assistant","timestamp":"2026-05-18T15:20:00Z","sessionId":"sess-1","message":{"id":"msg_1","model":"MiniMax-M2.7-highspeed","stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":7,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}`,
	)

	result, err := SyncClaudeSessionLogs(store, projectsDir)
	if err != nil {
		t.Fatalf("SyncClaudeSessionLogs() error = %v", err)
	}
	if result.FilesScanned != 1 || result.Imported != 1 || result.Skipped != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}

	rows, err := store.Requests(Filter{UsageSource: UsageSourceSessionLog, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}
	if rows.Total != 1 || len(rows.Rows) != 1 {
		t.Fatalf("unexpected rows: %#v", rows)
	}
	row := rows.Rows[0]
	if row.ID != "session:msg_1" {
		t.Fatalf("ID = %q", row.ID)
	}
	if !row.StartedAt.Equal(time.Date(2026, 5, 18, 15, 20, 0, 0, time.UTC)) {
		t.Fatalf("StartedAt = %s", row.StartedAt.Format(time.RFC3339Nano))
	}
	if row.SourceApp != "claude_code" || row.SourceEntrypoint != "session_log" {
		t.Fatalf("source = %q/%q", row.SourceApp, row.SourceEntrypoint)
	}
	if row.ProviderID != "_session" || row.ProviderName != "Session Log" {
		t.Fatalf("provider = %q/%q", row.ProviderID, row.ProviderName)
	}
	if row.RequestPath != "session_log" || row.Method != "SESSION" || row.MappedModel != "MiniMax-M2.7-highspeed" || !row.Stream {
		t.Fatalf("unexpected request metadata: %#v", row.RequestRecord)
	}
	if row.InputTokens != 12 || row.OutputTokens != 7 || row.CacheCreationInputTokens != 2 || row.CacheReadInputTokens != 3 {
		t.Fatalf("unexpected token values: %#v", row.TokenRecord)
	}
	if row.UsageSource != UsageSourceSessionLog || row.UsageParseStatus != ParseStatusOK {
		t.Fatalf("usage state = %q/%q", row.UsageSource, row.UsageParseStatus)
	}

	summary, err := store.Summary(Filter{From: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC), TZ: "UTC"})
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}
	if summary.TokenConsumptionTotal != 24 || summary.UsageCoverage != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}

func TestSyncClaudeSessionLogsIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	projectsDir := t.TempDir()
	writeSessionLog(t, filepath.Join(projectsDir, "session-1.jsonl"),
		`{"type":"assistant","timestamp":"2026-05-18T15:20:00Z","message":{"id":"msg_1","model":"qwen3-coder-plus","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":6}}}`,
	)

	first, err := SyncClaudeSessionLogs(store, projectsDir)
	if err != nil {
		t.Fatalf("first SyncClaudeSessionLogs() error = %v", err)
	}
	second, err := SyncClaudeSessionLogs(store, projectsDir)
	if err != nil {
		t.Fatalf("second SyncClaudeSessionLogs() error = %v", err)
	}
	if first.Imported != 1 || second.Imported != 0 || second.Skipped != 1 {
		t.Fatalf("unexpected results: first=%#v second=%#v", first, second)
	}

	page, err := store.Requests(Filter{UsageSource: UsageSourceSessionLog, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("Total = %d", page.Total)
	}
}

func TestSyncClaudeSessionLogsSkipsIncompleteAssistantUsage(t *testing.T) {
	store := newTestStore(t)
	projectsDir := t.TempDir()
	writeSessionLog(t, filepath.Join(projectsDir, "session-1.jsonl"),
		`{"type":"assistant","timestamp":"2026-05-18T15:20:00Z","message":{"id":"msg_no_stop","model":"kimi-k2","usage":{"input_tokens":5,"output_tokens":6}}}`,
		`{"type":"assistant","timestamp":"2026-05-18T15:21:00Z","message":{"id":"msg_no_output","model":"deepseek-chat","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":0}}}`,
	)

	result, err := SyncClaudeSessionLogs(store, projectsDir)
	if err != nil {
		t.Fatalf("SyncClaudeSessionLogs() error = %v", err)
	}
	if result.Imported != 0 || result.Skipped != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestDefaultClaudeProjectsDirUsesEnvironmentOverride(t *testing.T) {
	t.Setenv("CLAUDE_PROJECTS_DIR", "/mounted-claude-projects")

	if got := DefaultClaudeProjectsDir(); got != "/mounted-claude-projects" {
		t.Fatalf("DefaultClaudeProjectsDir() = %q", got)
	}
}

func writeSessionLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
