package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanProjectsGroupsByCwd(t *testing.T) {
	root := t.TempDir()
	writeJSONL(t, filepath.Join(root, "project-a", "session-1.jsonl"),
		`{"sessionId":"sess-1","cwd":"/work/project-a","timestamp":"2026-05-18T10:00:00Z"}`,
		`{"type":"user","message":{"role":"user","content":"first"},"timestamp":"2026-05-18T10:01:00Z"}`,
	)
	writeJSONL(t, filepath.Join(root, "project-a", "session-2.jsonl"),
		`{"sessionId":"sess-2","cwd":"/work/project-a","timestamp":"2026-05-18T11:00:00Z"}`,
		`{"type":"user","message":{"role":"user","content":"second"},"timestamp":"2026-05-18T11:01:00Z"}`,
	)
	writeJSONL(t, filepath.Join(root, "project-b", "session-3.jsonl"),
		`{"sessionId":"sess-3","cwd":"/work/project-b","timestamp":"2026-05-18T12:00:00Z"}`,
	)

	projects, err := ScanProjects(root)
	if err != nil {
		t.Fatalf("ScanProjects() error = %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("len(projects) = %d, want 2: %#v", len(projects), projects)
	}
	if projects[0].Path != "/work/project-b" || projects[0].SessionCount != 1 {
		t.Fatalf("first project = %#v", projects[0])
	}
	if projects[1].Path != "/work/project-a" || projects[1].SessionCount != 2 {
		t.Fatalf("second project = %#v", projects[1])
	}
}

func TestScanSessionsExtractsMetadata(t *testing.T) {
	root := t.TempDir()
	writeJSONL(t, filepath.Join(root, "project-a", "session-1.jsonl"),
		`{"sessionId":"sess-1","cwd":"/work/project-a","timestamp":"2026-05-18T10:00:00Z"}`,
		`{"type":"custom-title","customTitle":"Fix login bug"}`,
		`{"type":"user","message":{"role":"user","content":"hello"},"timestamp":"2026-05-18T10:01:00Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"done"},"timestamp":"2026-05-18T10:02:00Z"}`,
	)

	sessions, err := ScanSessions(root, "/work/project-a")
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	got := sessions[0]
	if got.ID != "sess-1" || got.Title != "Fix login bug" || got.ProjectPath != "/work/project-a" {
		t.Fatalf("unexpected session metadata: %#v", got)
	}
	if got.MessageCount != 2 {
		t.Fatalf("MessageCount = %d, want 2", got.MessageCount)
	}
	if got.SourcePath == "" || filepath.Base(got.SourcePath) != "session-1.jsonl" {
		t.Fatalf("SourcePath = %q", got.SourcePath)
	}
	if !got.CreatedAt.Equal(time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("CreatedAt = %s", got.CreatedAt)
	}
	if !got.LastActiveAt.Equal(time.Date(2026, 5, 18, 10, 2, 0, 0, time.UTC)) {
		t.Fatalf("LastActiveAt = %s", got.LastActiveAt)
	}
}

func TestScanSessionsUsesHeadWindowForTitle(t *testing.T) {
	root := t.TempDir()
	lines := []string{
		`{"sessionId":"sess-1","cwd":"/work/project-a","timestamp":"2026-05-18T10:00:00Z"}`,
		`{"type":"user","message":{"role":"user","content":"Head title"},"timestamp":"2026-05-18T10:01:00Z"}`,
	}
	for i := 0; i < 12; i++ {
		lines = append(lines, `{"type":"assistant","message":{"role":"assistant","content":"filler"},"timestamp":"2026-05-18T10:02:00Z"}`)
	}
	lines = append(lines, `{"type":"custom-title","customTitle":"Late custom title"}`)
	writeJSONL(t, filepath.Join(root, "project-a", "session-1.jsonl"), lines...)

	sessions, err := ScanSessions(root, "/work/project-a")
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	if sessions[0].Title != "Head title" {
		t.Fatalf("Title = %q, want head-window title", sessions[0].Title)
	}
}

func TestScanSkipsAgentFiles(t *testing.T) {
	root := t.TempDir()
	writeJSONL(t, filepath.Join(root, "project-a", "agent-1.jsonl"),
		`{"sessionId":"agent-1","cwd":"/work/project-a","timestamp":"2026-05-18T10:00:00Z"}`,
	)
	writeJSONL(t, filepath.Join(root, "project-a", "session-1.jsonl"),
		`{"sessionId":"sess-1","cwd":"/work/project-a","timestamp":"2026-05-18T10:00:00Z"}`,
	)

	sessions, err := ScanSessions(root, "")
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "sess-1" {
		t.Fatalf("sessions = %#v, want only sess-1", sessions)
	}
}

func TestScanHandlesEmptyDirectory(t *testing.T) {
	projects, err := ScanProjects(t.TempDir())
	if err != nil {
		t.Fatalf("ScanProjects() error = %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("len(projects) = %d, want 0", len(projects))
	}
}

func writeJSONL(t *testing.T, path string, lines ...string) {
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
