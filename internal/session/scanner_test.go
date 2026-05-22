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

func TestScanProjectsFoldsSubdirectoryCwdIntoProjectRoot(t *testing.T) {
	root := t.TempDir()
	writeJSONL(t, filepath.Join(root, "encoded-project-a", "session-root.jsonl"),
		`{"sessionId":"sess-root","cwd":"/work/project-a","timestamp":"2026-05-18T10:00:00Z"}`,
		`{"type":"user","message":{"role":"user","content":"root"},"timestamp":"2026-05-18T10:01:00Z"}`,
	)
	writeJSONL(t, filepath.Join(root, "encoded-project-a", "session-frontend.jsonl"),
		`{"sessionId":"sess-frontend","cwd":"/work/project-a/internal/frontend","timestamp":"2026-05-18T11:00:00Z"}`,
		`{"type":"custom-title","customTitle":"Frontend work"}`,
		`{"type":"user","message":{"role":"user","content":"frontend"},"timestamp":"2026-05-18T11:01:00Z"}`,
	)

	projects, err := ScanProjects(root)
	if err != nil {
		t.Fatalf("ScanProjects() error = %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("len(projects) = %d, want 1: %#v", len(projects), projects)
	}
	if projects[0].Path != "/work/project-a" || projects[0].Name != "project-a" || projects[0].SessionCount != 2 {
		t.Fatalf("project = %#v, want folded root project", projects[0])
	}

	sessions, err := ScanSessions(root, "/work/project-a")
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2: %#v", len(sessions), sessions)
	}
	for _, session := range sessions {
		if session.ProjectPath != "/work/project-a" {
			t.Fatalf("session %s ProjectPath = %q, want root", session.ID, session.ProjectPath)
		}
	}
}

func TestScanSessionsKeepsRootCwdWhenSessionTailContainsSubdirectoryCwd(t *testing.T) {
	root := t.TempDir()
	lines := []string{
		`{"sessionId":"sess-frontend","cwd":"/work/project-a","timestamp":"2026-05-18T10:00:00Z"}`,
		`{"type":"custom-title","customTitle":"Frontend work"}`,
		`{"type":"user","message":{"role":"user","content":"start at root"},"timestamp":"2026-05-18T10:01:00Z"}`,
	}
	for i := 0; i < 45; i++ {
		lines = append(lines, `{"type":"assistant","message":{"role":"assistant","content":"filler"},"timestamp":"2026-05-18T10:02:00Z"}`)
	}
	lines = append(lines,
		`{"sessionId":"sess-frontend","cwd":"/work/project-a/internal/frontend","timestamp":"2026-05-18T11:00:00Z"}`,
		`{"type":"user","message":{"role":"user","content":"command from frontend"},"timestamp":"2026-05-18T11:01:00Z"}`,
	)
	writeJSONL(t, filepath.Join(root, "encoded-project-a", "session-frontend.jsonl"), lines...)

	projects, err := ScanProjects(root)
	if err != nil {
		t.Fatalf("ScanProjects() error = %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("len(projects) = %d, want 1: %#v", len(projects), projects)
	}
	if projects[0].Path != "/work/project-a" || projects[0].Name != "project-a" {
		t.Fatalf("project = %#v, want root project", projects[0])
	}

	sessions, err := ScanSessions(root, "/work/project-a")
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1: %#v", len(sessions), sessions)
	}
	if sessions[0].ProjectPath != "/work/project-a" {
		t.Fatalf("ProjectPath = %q, want root", sessions[0].ProjectPath)
	}
}

func TestIsSameOrChildPathHandlesWindowsStylePaths(t *testing.T) {
	if !isSameOrChildPath(`C:\Work\Project-A`, `c:\work\project-a\internal\frontend`) {
		t.Fatal("expected Windows-style child path to match parent case-insensitively")
	}
	if isSameOrChildPath(`C:\Work\Project-A`, `C:\Work\Project-API`) {
		t.Fatal("expected sibling with shared prefix not to match")
	}
}

func TestIsSameOrChildPathUsesPathSegments(t *testing.T) {
	if !isSameOrChildPath("/work/project-a", "/work/project-a/internal/frontend") {
		t.Fatal("expected child path to match parent")
	}
	if isSameOrChildPath("/work/project-a", "/work/project-api") {
		t.Fatal("expected sibling with shared prefix not to match")
	}
}

func TestScanSessionsUsesScanWindowCustomTitleForTitle(t *testing.T) {
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
	if sessions[0].Title != "Late custom title" {
		t.Fatalf("Title = %q, want scan-window custom title", sessions[0].Title)
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

func TestProjectNameFromDir(t *testing.T) {
	tests := []struct {
		dir  string
		want string
	}{
		// 不含 "-" 的项目名可完整还原
		{"-home-www-workspace-2026-claude_code_proxy_dns", "claude_code_proxy_dns"},
		// 含 "-" 的项目名有损，只能拿到最后一段
		{"-home-www-workspace-MyProjects-2026-pm0511-lvshixiehui", "lvshixiehui"},
		{"-home-www-workspace-claude-workspace", "workspace"},
		{"foo", "foo"},
		{"", "Unknown Project"},
		{"-", "Unknown Project"},
	}
	for _, tt := range tests {
		got := projectNameFromDir(tt.dir)
		if got != tt.want {
			t.Errorf("projectNameFromDir(%q) = %q, want %q", tt.dir, got, tt.want)
		}
	}
}

func TestFoldSourceProjectSessionsUsesValidCwdToResolveUnknown(t *testing.T) {
	// 同目录下：session-A 缺少 cwd → "Unknown Project"
	//           session-B 有 cwd → "/work/project-a"
	// 期望：fold 后 session-A 也被修正为 "/work/project-a"
	root := t.TempDir()
	writeJSONL(t, filepath.Join(root, "-work-project-a", "session-A.jsonl"),
		`{"sessionId":"sess-A","timestamp":"2026-05-18T10:00:00Z"}`,
	)
	writeJSONL(t, filepath.Join(root, "-work-project-a", "session-B.jsonl"),
		`{"sessionId":"sess-B","cwd":"/work/project-a","timestamp":"2026-05-18T11:00:00Z"}`,
	)

	sessions, err := ScanSessions(root, "")
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}
	for _, sess := range sessions {
		if sess.ProjectPath != "/work/project-a" {
			t.Errorf("session %s ProjectPath = %q, want /work/project-a", sess.ID, sess.ProjectPath)
		}
	}
}

func TestFoldSourceProjectSessionsAllUnknownFallback(t *testing.T) {
	// 同目录下所有 session 都缺少 cwd，使用目录名最后一段作为兜底
	root := t.TempDir()
	writeJSONL(t, filepath.Join(root, "-work-foo_bar", "session-A.jsonl"),
		`{"sessionId":"sess-A","timestamp":"2026-05-18T10:00:00Z"}`,
	)
	writeJSONL(t, filepath.Join(root, "-work-foo_bar", "session-B.jsonl"),
		`{"sessionId":"sess-B","timestamp":"2026-05-18T11:00:00Z"}`,
	)

	sessions, err := ScanSessions(root, "")
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}
	for _, sess := range sessions {
		if sess.ProjectPath != "foo_bar" {
			t.Errorf("session %s ProjectPath = %q, want foo_bar", sess.ID, sess.ProjectPath)
		}
	}
}
