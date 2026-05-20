package session

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMessagesExtractsUserAndAssistant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeJSONL(t, path,
		`{"type":"user","message":{"role":"user","content":"hello"},"timestamp":"2026-05-18T10:00:00Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"hi there"},"timestamp":"2026-05-18T10:01:00Z"}`,
	)

	messages, err := ParseMessages(path)
	if err != nil {
		t.Fatalf("ParseMessages() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "hello" {
		t.Fatalf("first message = %#v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Content != "hi there" {
		t.Fatalf("second message = %#v", messages[1])
	}
}

func TestParseMessagesReclassifiesToolResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeJSONL(t, path,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file contents"}]},"timestamp":"2026-05-18T10:00:00Z"}`,
	)

	messages, err := ParseMessages(path)
	if err != nil {
		t.Fatalf("ParseMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].Role != "tool" {
		t.Fatalf("messages = %#v, want one tool message", messages)
	}
	if !strings.Contains(messages[0].Content, "file contents") {
		t.Fatalf("tool content = %q", messages[0].Content)
	}
}

func TestParseMessagesSkipsMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeJSONL(t, path,
		`{"isMeta":true,"type":"user","message":{"role":"user","content":"skip me"}}`,
		`{"type":"user","message":{"role":"user","content":"keep me"}}`,
	)

	messages, err := ParseMessages(path)
	if err != nil {
		t.Fatalf("ParseMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "keep me" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestParseMessagesHandlesContentArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeJSONL(t, path,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"looking"},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"main.go"}}]},"timestamp":"2026-05-18T10:00:00Z"}`,
	)

	messages, err := ParseMessages(path)
	if err != nil {
		t.Fatalf("ParseMessages() error = %v", err)
	}
	content := messages[0].Content
	if !strings.Contains(content, "looking") || !strings.Contains(content, "[Tool: Read]") || !strings.Contains(content, "main.go") {
		t.Fatalf("content = %q", content)
	}
}

func TestExtractTitleFromCustomTitle(t *testing.T) {
	got := ExtractTitle([]string{`{"type":"custom-title","customTitle":"fix-login-bug"}`})
	if got != "fix-login-bug" {
		t.Fatalf("ExtractTitle() = %q", got)
	}
}

func TestExtractTitleFromFirstUserMessage(t *testing.T) {
	got := ExtractTitle([]string{
		`{"type":"assistant","message":{"role":"assistant","content":"skip"}}`,
		`{"type":"user","message":{"role":"user","content":"Please fix the login bug"}}`,
	})
	if got != "Please fix the login bug" {
		t.Fatalf("ExtractTitle() = %q", got)
	}
}

func TestExtractTitleSkipsCaveatAndCommands(t *testing.T) {
	got := ExtractTitle([]string{
		`{"type":"user","message":{"role":"user","content":"<local-command-caveat>Do not use this as title</local-command-caveat>"}}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"user","message":{"role":"user","content":"Real user task"}}`,
	})
	if got != "Real user task" {
		t.Fatalf("ExtractTitle() = %q", got)
	}
}
