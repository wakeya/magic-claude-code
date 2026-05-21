package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type sessionLine struct {
	Type        string          `json:"type"`
	IsMeta      bool            `json:"isMeta"`
	SessionID   string          `json:"sessionId"`
	CWD         string          `json:"cwd"`
	Timestamp   string          `json:"timestamp"`
	CustomTitle string          `json:"customTitle"`
	Message     sessionMessage  `json:"message"`
	Raw         json.RawMessage `json:"-"`
}

type sessionMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content"`
	ToolUseID string          `json:"tool_use_id"`
}

func ParseMessages(filePath string) ([]Message, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var messages []Message
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line, ok := parseJSONLine(scanner.Text())
		if !ok || line.IsMeta || len(line.Message.Content) == 0 {
			continue
		}
		role := line.Message.Role
		if role == "" {
			role = line.Type
		}
		if role == "" {
			continue
		}
		content, onlyToolResults := renderContent(line.Message.Content)
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		if role == "user" && onlyToolResults {
			role = "tool"
		}
		messages = append(messages, Message{
			Role:      role,
			Content:   content,
			Timestamp: parseTimestampUnix(line.Timestamp),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func ExtractTitle(lines []string) string {
	customTitle := ""
	for _, raw := range lines {
		line, ok := parseJSONLine(raw)
		if !ok {
			continue
		}
		if strings.TrimSpace(line.CustomTitle) != "" {
			customTitle = strings.TrimSpace(line.CustomTitle)
		}
	}
	if customTitle != "" {
		return customTitle
	}
	for _, raw := range lines {
		line, ok := parseJSONLine(raw)
		if !ok || line.IsMeta || line.Message.Role != "user" || len(line.Message.Content) == 0 {
			continue
		}
		content, onlyToolResults := renderContent(line.Message.Content)
		content = strings.TrimSpace(content)
		if content == "" || onlyToolResults || isTitleNoise(content) {
			continue
		}
		return trimTitle(content)
	}
	return ""
}

func parseJSONLine(raw string) (sessionLine, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return sessionLine{}, false
	}
	var line sessionLine
	if err := json.Unmarshal([]byte(raw), &line); err != nil {
		return sessionLine{}, false
	}
	line.Raw = json.RawMessage(raw)
	return line, true
}

func renderContent(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, false
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		onlyToolResults := len(blocks) > 0
		for _, block := range blocks {
			if block.Type != "tool_result" {
				onlyToolResults = false
			}
			rendered := renderBlock(block)
			if strings.TrimSpace(rendered) != "" {
				parts = append(parts, rendered)
			}
		}
		return strings.Join(parts, "\n\n"), onlyToolResults
	}
	return string(raw), false
}

func renderBlock(block contentBlock) string {
	switch block.Type {
	case "text":
		return block.Text
	case "tool_use":
		input := compactJSON(block.Input)
		if input == "" {
			return fmt.Sprintf("[Tool: %s]", block.Name)
		}
		return fmt.Sprintf("[Tool: %s]\n%s", block.Name, input)
	case "tool_result":
		content := renderToolResultContent(block.Content)
		if block.ToolUseID == "" {
			return content
		}
		if content == "" {
			return fmt.Sprintf("[Tool Result: %s]", block.ToolUseID)
		}
		return fmt.Sprintf("[Tool Result: %s]\n%s", block.ToolUseID, content)
	case "image":
		return "[Image]"
	default:
		if block.Text != "" {
			return block.Text
		}
		return fmt.Sprintf("[%s]", block.Type)
	}
}

func renderToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	rendered, _ := renderContent(raw)
	if rendered != "" {
		return rendered
	}
	return compactJSON(raw)
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

func parseTimestamp(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func parseTimestampUnix(value string) int64 {
	t, ok := parseTimestamp(value)
	if !ok {
		return 0
	}
	return t.Unix()
}

func isTitleNoise(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.Contains(trimmed, "<local-command-caveat>") ||
		strings.HasPrefix(trimmed, "<command-name>") ||
		strings.HasPrefix(trimmed, "<local-command-stdout>") ||
		strings.HasPrefix(trimmed, "<local-command-stderr>")
}

func trimTitle(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	const maxTitleRunes = 120
	runes := []rune(content)
	if len(runes) <= maxTitleRunes {
		return content
	}
	return string(runes[:maxTitleRunes])
}
