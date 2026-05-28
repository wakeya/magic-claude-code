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

func TestExportHTMLContainsOutline(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "Outline") {
		t.Fatalf("export should contain Outline panel: %s", html)
	}
}

func TestExportHTMLOutlineItems(t *testing.T) {
	html := exportTestHTML(t)
	// The test data has 1 user message + 1 assistant message (2 total)
	// System messages are collapsed and not included in outline
	count := strings.Count(html, "class=\"outline-item\"")
	if count != 2 {
		t.Fatalf("expected 2 outline items (1 user + 1 assistant), got %d: %s", count, html)
	}
}

func TestExportHTMLOutlineHasPreviewText(t *testing.T) {
	html := exportTestHTML(t)
	// The user message is "hello" (5 chars, less than 50)
	if !strings.Contains(html, "hello") {
		t.Fatalf("outline preview should contain 'hello': %s", html)
	}
}

func TestExportHTMLMessageHasAnchorID(t *testing.T) {
	html := exportTestHTML(t)
	// The user message is at index 1
	if !strings.Contains(html, `id="msg-1"`) {
		t.Fatalf("user message should have id=\"msg-1\": %s", html)
	}
}

func TestExportHTMLOutlineHasBackToTop(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "back-to-top") {
		t.Fatalf("export should contain back-to-top: %s", html)
	}
}

func TestExportHTMLOutlineScript(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "jumpToMsg") {
		t.Fatalf("export should contain jumpToMsg JS: %s", html)
	}
	if !strings.Contains(html, "IntersectionObserver") {
		t.Fatalf("export should contain IntersectionObserver: %s", html)
	}
	if !strings.Contains(html, "backToTop") {
		t.Fatalf("export should contain backToTop JS: %s", html)
	}
}

func TestExportHTMLOutlineModal(t *testing.T) {
	html := exportTestHTML(t)
	if !strings.Contains(html, "outline-modal") {
		t.Fatalf("export should contain outline-modal: %s", html)
	}
	if !strings.Contains(html, "outline-toggle") {
		t.Fatalf("export should contain outline-toggle: %s", html)
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
	}, "dark")
	if err != nil {
		t.Fatalf("ExportHTML() error = %v", err)
	}
	return string(out)
}
