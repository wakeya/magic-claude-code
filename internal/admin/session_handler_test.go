package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionProjectsReturnsProjects(t *testing.T) {
	server, mux, _ := newSessionTestServer(t)

	req := authenticatedSessionRequest(server, http.MethodGet, "/api/sessions/projects")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var projects []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &projects); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	if len(projects) != 1 || projects[0]["path"] != "/work/project-a" {
		t.Fatalf("projects = %#v", projects)
	}
}

func TestSessionListFilterByProject(t *testing.T) {
	server, mux, _ := newSessionTestServer(t)

	req := authenticatedSessionRequest(server, http.MethodGet, "/api/sessions?project="+url.QueryEscape("/work/project-a"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		Sessions []map[string]any `json:"sessions"`
		Total    int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if page.Total != 1 || len(page.Sessions) != 1 || page.Sessions[0]["id"] != "sess-1" {
		t.Fatalf("page = %#v", page)
	}
}

func TestSessionDetailReturnsMessages(t *testing.T) {
	server, mux, source := newSessionTestServer(t)

	req := authenticatedSessionRequest(server, http.MethodGet, "/api/sessions/sess-1?source="+url.QueryEscape(source))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Session  map[string]any   `json:"session"`
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Session["id"] != "sess-1" || len(detail.Messages) != 2 {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestSessionDetailAcceptsRootRelativeSource(t *testing.T) {
	server, mux, _ := newSessionTestServer(t)

	req := authenticatedSessionRequest(server, http.MethodGet, "/api/sessions/sess-1?source="+url.QueryEscape(filepath.Join("project-a", "sess-1.jsonl")))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSessionExportReturnsHTML(t *testing.T) {
	server, mux, source := newSessionTestServer(t)

	req := authenticatedSessionRequest(server, http.MethodGet, "/api/sessions/sess-1/export?source="+url.QueryEscape(source))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "Test session") {
		t.Fatalf("html = %s", rec.Body.String())
	}
}

func TestSessionCleanupHintReturnsCommands(t *testing.T) {
	server, mux, source := newSessionTestServer(t)

	req := authenticatedSessionRequest(server, http.MethodGet, "/api/sessions/sess-1/cleanup-hint?source="+url.QueryEscape(source))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var hint map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &hint); err != nil {
		t.Fatalf("decode hint: %v", err)
	}
	if !strings.Contains(hint["preview_command"], "claude project purge --dry-run") ||
		!strings.Contains(hint["interactive_command"], "claude project purge -i") {
		t.Fatalf("hint = %#v", hint)
	}
	if !strings.Contains(hint["windows_preview_command"], `claude project purge --dry-run "C:\Users\用户名代理\work\project-a"`) ||
		!strings.Contains(hint["windows_interactive_command"], `claude project purge -i "C:\Users\用户名代理\work\project-a"`) {
		t.Fatalf("windows hint = %#v", hint)
	}
	if _, ok := hint["note"]; ok {
		t.Fatalf("cleanup hint note should be localized by frontend, got %#v", hint)
	}
}

func TestWindowsCleanupPathFromLinuxHome(t *testing.T) {
	got := windowsCleanupPath(`/home/www/workspace/MyProjects/2026/pm0511-lvshixiehui`)
	want := `C:\Users\用户名代理\workspace\MyProjects\2026\pm0511-lvshixiehui`
	if got != want {
		t.Fatalf("windowsCleanupPath = %q, want %q", got, want)
	}
}

func TestWindowsCleanupPathPreservesNativeDriveAndMasksUser(t *testing.T) {
	got := windowsCleanupPath(`C:\Users\Alice\workspace\MyProjects\2026\pm0511-lvshixiehui`)
	want := `C:\Users\用户名代理\workspace\MyProjects\2026\pm0511-lvshixiehui`
	if got != want {
		t.Fatalf("windowsCleanupPath = %q, want %q", got, want)
	}
}

func TestWindowsCleanupPathSanitizesUnsafeCommandCharacters(t *testing.T) {
	got := windowsCleanupPath("/home/www/workspace/bad\"name\nnext")
	want := `C:\Users\用户名代理\workspace\bad_name_next`
	if got != want {
		t.Fatalf("windowsCleanupPath = %q, want %q", got, want)
	}
	quoted := windowsShellQuote(got)
	if !strings.HasPrefix(quoted, `"`) || !strings.HasSuffix(quoted, `"`) {
		t.Fatalf("windowsShellQuote should wrap path in quotes: %q", quoted)
	}
	if strings.ContainsAny(strings.Trim(quoted, `"`), "\"\r\n") {
		t.Fatalf("windowsShellQuote contains unsafe embedded quote/control characters: %q", quoted)
	}
}

func TestSessionRoutesDoNotDeleteFiles(t *testing.T) {
	server, mux, source := newSessionTestServer(t)

	req := authenticatedSessionRequest(server, http.MethodDelete, "/api/sessions/sess-1?source="+url.QueryEscape(source))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source should not be deleted: %v", err)
	}
}

func TestSessionRoutesRejectSymlinkSource(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	writeAdminSessionJSONL(t, outside,
		`{"sessionId":"sess-outside","cwd":"/work/outside","timestamp":"2026-05-18T10:00:00Z"}`,
		`{"type":"user","message":{"role":"user","content":"outside"}}`,
	)
	link := filepath.Join(root, "project-a", "sess-outside.jsonl")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := NewServer(&AdminConfig{Password: "secret", ClaudeProjectsDir: root}, nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions/", server.authMiddlewareFunc(server.handleSessionRoutes))

	req := authenticatedSessionRequest(server, http.MethodGet, "/api/sessions/sess-outside?source="+url.QueryEscape(link))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSessionRoutesRequireAuth(t *testing.T) {
	_, mux, _ := newSessionTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/projects", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func newSessionTestServer(t *testing.T) (*Server, *http.ServeMux, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "project-a", "sess-1.jsonl")
	writeAdminSessionJSONL(t, source,
		`{"sessionId":"sess-1","cwd":"/work/project-a","timestamp":"2026-05-18T10:00:00Z"}`,
		`{"type":"custom-title","customTitle":"Test session"}`,
		`{"type":"user","message":{"role":"user","content":"hello"},"timestamp":"2026-05-18T10:01:00Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"done"},"timestamp":"2026-05-18T10:02:00Z"}`,
	)
	server := NewServer(&AdminConfig{Password: "secret", ClaudeProjectsDir: root}, nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", server.authMiddlewareFunc(server.handleSessions))
	mux.HandleFunc("/api/sessions/projects", server.authMiddlewareFunc(server.handleSessionProjects))
	mux.HandleFunc("/api/sessions/", server.authMiddlewareFunc(server.handleSessionRoutes))
	return server, mux, source
}

func authenticatedSessionRequest(server *Server, method string, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: server.auth.GenerateToken()})
	return req
}

func writeAdminSessionJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
