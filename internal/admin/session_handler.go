package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"magic-claude-code/internal/session"
)

func (s *Server) handleSessionProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeSessionError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	projects, err := session.ScanProjects(s.claudeProjectsDir())
	writeSessionJSON(w, projects, err)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeSessionError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	q := r.URL.Query()
	page := positiveInt(q.Get("page"), 1)
	pageSize := positiveInt(q.Get("page_size"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	sessions, err := session.ScanSessions(s.claudeProjectsDir(), q.Get("project"))
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "failed to scan sessions")
		return
	}
	total := len(sessions)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	writeSessionJSON(w, map[string]interface{}{
		"sessions":  sessions[start:end],
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}, nil)
}

func (s *Server) handleSessionRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if path == "" {
		writeSessionError(w, http.StatusBadRequest, "session id is required")
		return
	}
	switch {
	case strings.HasSuffix(path, "/export"):
		if r.Method != http.MethodGet {
			writeSessionError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		id := strings.TrimSuffix(path, "/export")
		s.handleSessionExport(w, r, id)
	case strings.HasSuffix(path, "/cleanup-hint"):
		if r.Method != http.MethodGet {
			writeSessionError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		id := strings.TrimSuffix(path, "/cleanup-hint")
		s.handleSessionCleanupHint(w, r, id)
	default:
		if r.Method != http.MethodGet {
			writeSessionError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleSessionDetail(w, r, path)
	}
}

func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request, id string) {
	sess, err := s.findSessionBySource(id, r.URL.Query().Get("source"))
	if err != nil {
		writeSessionError(w, http.StatusBadRequest, err.Error())
		return
	}
	messages, err := session.ParseMessages(sess.SourcePath)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "failed to parse session")
		return
	}
	if messages == nil {
		messages = []session.Message{}
	}
	writeSessionJSON(w, session.SessionDetail{Session: sess, Messages: messages, MessageCount: len(messages)}, nil)
}

func (s *Server) handleSessionExport(w http.ResponseWriter, r *http.Request, id string) {
	sess, err := s.findSessionBySource(id, r.URL.Query().Get("source"))
	if err != nil {
		writeSessionError(w, http.StatusBadRequest, err.Error())
		return
	}
	messages, err := session.ParseMessages(sess.SourcePath)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "failed to parse session")
		return
	}
	if messages == nil {
		messages = []session.Message{}
	}
	theme := r.URL.Query().Get("theme")
	locale := r.URL.Query().Get("locale")
	html, err := session.ExportHTML(&session.SessionDetail{Session: sess, Messages: messages, MessageCount: len(messages)}, theme, locale)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "failed to export session")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.html"`, safeDownloadName(sess.Title)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html)
}

func (s *Server) handleSessionCleanupHint(w http.ResponseWriter, r *http.Request, id string) {
	sess, err := s.findSessionBySource(id, r.URL.Query().Get("source"))
	if err != nil {
		writeSessionError(w, http.StatusBadRequest, err.Error())
		return
	}
	quotedPath := shellQuote(sess.ProjectPath)
	windowsQuotedPath := windowsShellQuote(windowsCleanupPath(sess.ProjectPath))
	writeSessionJSON(w, session.CleanupHint{
		ProjectPath:               sess.ProjectPath,
		PreviewCommand:            "claude project purge --dry-run " + quotedPath,
		InteractiveCommand:        "claude project purge -i " + quotedPath,
		WindowsPreviewCommand:     "claude project purge --dry-run " + windowsQuotedPath,
		WindowsInteractiveCommand: "claude project purge -i " + windowsQuotedPath,
	}, nil)
}

func (s *Server) findSessionBySource(id string, sourcePath string) (session.Session, error) {
	if id == "" {
		return session.Session{}, fmt.Errorf("session id is required")
	}
	if sourcePath == "" {
		return session.Session{}, fmt.Errorf("source is required")
	}
	root, err := filepath.Abs(s.claudeProjectsDir())
	if err != nil {
		return session.Session{}, fmt.Errorf("invalid projects dir")
	}
	sourceAbs := filepath.Clean(sourcePath)
	if !filepath.IsAbs(sourceAbs) {
		sourceAbs = filepath.Clean(filepath.Join(root, sourceAbs))
	}
	if filepath.Ext(sourceAbs) != ".jsonl" || strings.HasPrefix(filepath.Base(sourceAbs), "agent-") {
		return session.Session{}, fmt.Errorf("invalid source")
	}
	info, err := os.Lstat(sourceAbs)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return session.Session{}, fmt.Errorf("invalid source")
	}
	rel, err := filepath.Rel(root, sourceAbs)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return session.Session{}, fmt.Errorf("source is outside projects dir")
	}
	sessions, err := session.ScanSessions(root, "")
	if err != nil {
		return session.Session{}, fmt.Errorf("failed to scan sessions")
	}
	for _, sess := range sessions {
		candidate, _ := filepath.Abs(sess.SourcePath)
		if sess.ID == id && candidate == sourceAbs {
			return sess, nil
		}
	}
	return session.Session{}, fmt.Errorf("session not found")
}

func (s *Server) claudeProjectsDir() string {
	if s.config != nil && s.config.ClaudeProjectsDir != "" {
		return s.config.ClaudeProjectsDir
	}
	return session.DefaultProjectsDir()
}

func positiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func writeSessionJSON(w http.ResponseWriter, value interface{}, err error) {
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "request failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeSessionError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func safeDownloadName(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "claude-session"
	}
	var builder strings.Builder
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		case r == ' ':
			builder.WriteRune('-')
		}
	}
	if builder.Len() == 0 {
		return "claude-session"
	}
	return builder.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func windowsCleanupPath(path string) string {
	normalized := strings.ReplaceAll(path, "\\", "/")
	if len(normalized) >= 3 && isASCIIAlpha(normalized[0]) && normalized[1] == ':' && normalized[2] == '/' {
		drive := strings.ToUpper(normalized[:1])
		parts := windowsPathParts(normalized[3:])
		if len(parts) >= 2 && strings.EqualFold(parts[0], "Users") {
			parts[1] = "用户名代理"
		}
		if len(parts) == 0 {
			return drive + `:\`
		}
		return drive + `:\` + strings.Join(parts, `\`)
	}
	parts := windowsPathParts(normalized)
	if len(parts) >= 3 && strings.EqualFold(parts[0], "mnt") && len(parts[1]) == 1 {
		drive := strings.ToUpper(parts[1])
		return drive + `:\` + strings.Join(parts[2:], `\`)
	}
	if len(parts) >= 2 && (parts[0] == "home" || parts[0] == "Users") {
		return `C:\Users\用户名代理\` + strings.Join(parts[2:], `\`)
	}
	if len(parts) == 0 {
		return `C:\Users\用户名代理`
	}
	return `C:\Users\用户名代理\` + strings.Join(parts, `\`)
}

func windowsShellQuote(value string) string {
	return `"` + value + `"`
}

func windowsPathParts(path string) []string {
	rawParts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		cleaned := sanitizeWindowsPathPart(part)
		if cleaned != "" {
			parts = append(parts, cleaned)
		}
	}
	return parts
}

func sanitizeWindowsPathPart(part string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range part {
		if isUnsafeWindowsPathRune(r) {
			if !lastUnderscore {
				builder.WriteByte('_')
				lastUnderscore = true
			}
			continue
		}
		builder.WriteRune(r)
		lastUnderscore = false
	}
	return strings.Trim(builder.String(), " ._")
}

func isUnsafeWindowsPathRune(r rune) bool {
	if r < 0x20 || r == 0x7f {
		return true
	}
	switch r {
	case '<', '>', ':', '"', '|', '?', '*':
		return true
	default:
		return false
	}
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
