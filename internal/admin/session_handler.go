package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"claude_code_proxy_dns/internal/session"
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
	theme := r.URL.Query().Get("theme")
	html, err := session.ExportHTML(&session.SessionDetail{Session: sess, Messages: messages, MessageCount: len(messages)}, theme)
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
	writeSessionJSON(w, session.CleanupHint{
		ProjectPath:        sess.ProjectPath,
		PreviewCommand:     "claude project purge --dry-run " + quotedPath,
		InteractiveCommand: "claude project purge -i " + quotedPath,
		Note:               "管理面板不会删除 JSONL 文件。请在终端中运行 Claude Code CLI 命令，并先使用 --dry-run 预览。",
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
