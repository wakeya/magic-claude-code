package session

import (
	"strings"
	"testing"
	"time"
)

func TestExportHTMLContainsSessionTitle(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "Fix login bug") {
		t.Fatalf("export does not contain title: %s", html)
	}
}

func TestExportHTMLContainsMessages(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "hello") || !strings.Contains(html, "done") {
		t.Fatalf("export does not contain messages: %s", html)
	}
}

func TestExportHTMLIsSelfContained(t *testing.T) {
	html := exportTestHTML(t)
	for _, forbidden := range []string{"<link ", " src=\"http", " href=\"http"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("export contains external dependency marker %q: %s", forbidden, html)
		}
	}
	if !strings.Contains(html, "<style>") {
		t.Fatalf("export should contain inline style: %s", html)
	}
}

func TestExportHTMLCollapsesSystemMessages(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "<details") || !strings.Contains(html, "system prompt") {
		t.Fatalf("system message should be inside details: %s", html)
	}
}

func TestExportHTMLHighlightsUserMessagesAsGreenBlocks(t *testing.T) {
	html := exportTestHTML(t)
	for _, want := range []string{".message.user{", "var(--user-bg)", "var(--user-border)", "var(--user-label)"} {
		if !strings.Contains(html, want) {
			t.Fatalf("exported user message CSS missing %q: %s", want, html)
		}
	}
}

func exportTestHTML(t *testing.T) string {
	t.Helper()
	out, err := ExportHTML(&SessionDetail{
		Session: Session{
			ID:           "sess-1",
			Title:        "Fix login bug",
			ProjectPath:  "/work/project-a",
			CreatedAt:    time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC),
			LastActiveAt: time.Date(2026, 5, 18, 10, 2, 0, 0, time.UTC),
		},
		Messages: []Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "done"},
		},
	})
	if err != nil {
		t.Fatalf("ExportHTML() error = %v", err)
	}
	return string(out)
}
