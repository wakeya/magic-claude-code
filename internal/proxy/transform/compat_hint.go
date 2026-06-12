package transform

import "strings"

type Options struct {
	ClaudeCodeCompatHint bool
}

const claudeCodeToolUseCompatibilityHint = `Claude Code tool-use compatibility:
- Before editing a file, use Read to inspect the exact current lines around the intended change.
- For Edit.old_string, copy the exact current text from the latest Read result and include enough surrounding context so it matches exactly once.
- If Edit reports multiple matches, retry with more surrounding context instead of replace_all unless every occurrence must change.
- If Edit reports "String to replace not found", read the file again and rebuild old_string from the latest content.
- Do not claim a file was updated after an Edit failure; verify the successful tool result first.`

func appendClaudeCodeToolUseCompatibilityHint(system string, enabled bool) string {
	system = strings.TrimSpace(system)
	if !enabled || strings.Contains(system, "Claude Code tool-use compatibility") {
		return system
	}
	if system == "" {
		return claudeCodeToolUseCompatibilityHint
	}
	return system + "\n\n" + claudeCodeToolUseCompatibilityHint
}
