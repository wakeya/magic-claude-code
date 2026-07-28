package providerquota

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GenerateScriptRequest is the user input for AI script generation.
type GenerateScriptRequest struct {
	Model          string
	Prompt         string
	ResponseSample string
	RequestInfo    string
}

// GenerateScriptResult is the generated script or a structured error.
type GenerateScriptResult struct {
	Script       string
	ErrorCode    string
	ErrorMessage string
}

// GenerateScript builds prompts, calls the LLM, extracts the script, and
// verifies that the request phase can be parsed without performing HTTP.
func GenerateScript(ctx context.Context, llm *LLMClient, provider LLMProvider, req GenerateScriptRequest, timeout time.Duration) GenerateScriptResult {
	if strings.TrimSpace(req.Prompt) == "" || strings.TrimSpace(req.ResponseSample) == "" {
		return GenerateScriptResult{ErrorCode: "invalid_config", ErrorMessage: "prompt and response_sample are required"}
	}
	if llm == nil {
		return GenerateScriptResult{ErrorCode: "internal_error", ErrorMessage: "llm client is required"}
	}

	callCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	call := llm.Call(callCtx, provider, strings.TrimSpace(req.Model), systemPromptForScript(), buildUserMessage(req))
	if call.ErrorCode != "" {
		return GenerateScriptResult{ErrorCode: call.ErrorCode, ErrorMessage: call.ErrorMessage}
	}

	script, err := extractScript(call.Text)
	if err != nil {
		return GenerateScriptResult{ErrorCode: "invalid_response", ErrorMessage: err.Error()}
	}

	if _, err := (&ScriptExecutor{}).parseRequest(script); err != nil {
		return GenerateScriptResult{
			ErrorCode:    "script_error",
			ErrorMessage: fmt.Sprintf("generated script failed to parse request: %v; llm_output=%s", err, summarizeLLMText(call.Text)),
		}
	}
	return GenerateScriptResult{Script: script}
}

func systemPromptForScript() string {
	return `You generate a JavaScript quota-query script for MCC (Magic Claude Code), a Claude Code proxy.

OUTPUT FORMAT - return ONLY a single JavaScript expression: an object literal ` + "`({ request, extractor })`" + `. No markdown fences, no explanation, no leading/trailing text.

REQUEST CONTRACT:
- request.url: string, may use placeholders {{baseUrl}} (replaced by the script's Base URL at runtime).
- request.method: "GET" or "POST" only.
- request.headers: object of string values; may use placeholders {{apiKey}}, {{apiKey2}}, {{accessToken}}, {{userId}} (replaced by Go, never appear in JS runtime). Do NOT set Host/Content-Length/Transfer-Encoding/Connection/Proxy-Authorization.
- request.body: for POST, a JSON object (default), OR set request.bodyType: "form" and make body a flat object whose values may be strings/numbers/booleans/nested objects (nested values are JSON-marshaled then form-encoded).
- The request URL must share scheme+host+port with {{baseUrl}} (same-origin enforced).

EXTRACTOR CONTRACT - extractor(response) where response is the upstream response ALREADY PARSED as a JSON object (object/array/string/number) by the runtime. Do NOT call JSON.parse on it and do NOT access response.body or response.json() (those are browser fetch APIs that do not exist here) — use its fields directly (e.g. response.data.DataV2.data.data). extractor returns one object or an array of objects. Each item:
- Time-window tier (preferred when the response has a time-bounded quota): { window: "five_hour"|"seven_day"|"monthly"|"weekly", utilization: <0-100 USED percent>, resetsAt: <RFC3339|string>|<unix seconds>|<unix ms number>, used?: number, total?: number, remaining?: number, unit?: string }
- Balance (no time window): { planName?: string, remaining?: number, used?: number, total?: number, unit?: string, isValid?: boolean, invalidMessage?: string }
- If the upstream returned a business error, return { __error_code: "upstream_business_error"|"invalid_credentials"|..., __error_message: "..." }.
- utilization is ALWAYS "used percent" in 0-100. If the source field is a 0-1 ratio, multiply by 100. If the source is "remaining percent", compute 100 - remaining.

SECURITY - the script runs in a sandboxed goja runtime WITHOUT fetch/require/file/env/process. Do not call any global API; only manipulate the response argument and return literals.

PLACEHOLDERS - {{apiKey}} / {{apiKey2}} are the two configured secrets, {{baseUrl}} is the Base URL, {{accessToken}}/{{userId}} for newapi. Use them in headers/body; never hardcode secrets.

Given the user's description and a real response sample, produce the script. The response sample is authoritative for field paths.`
}

func buildUserMessage(req GenerateScriptRequest) string {
	info := strings.TrimSpace(req.RequestInfo)
	if info == "" {
		info = "(not provided - infer from need)"
	}
	return fmt.Sprintf(
		"Need: %s\n\nRequest info (best-effort, may be partial):\n%s\n\nReal response sample (JSON):\n%s",
		strings.TrimSpace(req.Prompt),
		info,
		strings.TrimSpace(req.ResponseSample),
	)
}

func extractScript(text string) (string, error) {
	s := strings.TrimSpace(text)

	// Strip a single surrounding markdown fence (```js / ```javascript / ```).
	if strings.HasPrefix(s, "```") {
		if idx := strings.IndexByte(s, '\n'); idx >= 0 {
			s = strings.TrimSpace(s[idx+1:])
		} else {
			s = strings.TrimPrefix(s, "```")
		}
		if strings.HasSuffix(s, "```") {
			s = strings.TrimSpace(strings.TrimSuffix(s, "```"))
		}
	}

	// Fast path: the remainder is already a bare object literal.
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		return s, nil
	}

	// Fallback: LLMs often wrap the script in explanation despite instructions.
	// Locate the outermost ({ ... }) anywhere in the text.
	if extracted, ok := locateObjectLiteral(s); ok {
		return extracted, nil
	}

	return "", fmt.Errorf("LLM output does not contain a recognizable ({...}) object literal; first %d bytes: %s", maxErrorBodyBytes, summarizeLLMText(text))
}

// locateObjectLiteral finds the first "({" (or "(") and the last "})" (or ")")
// in s and returns the inclusive slice. Returns false when no span is found.
// This lets extractScript tolerate leading/trailing explanation text that
// LLMs commonly emit around the script.
func locateObjectLiteral(s string) (string, bool) {
	start := strings.Index(s, "({")
	if start < 0 {
		start = strings.Index(s, "(")
	}
	if start < 0 {
		return "", false
	}
	if end := strings.LastIndex(s, "})"); end > start {
		return s[start : end+2], true
	}
	if end := strings.LastIndex(s, ")"); end > start {
		return s[start : end+1], true
	}
	return "", false
}

func summarizeLLMText(text string) string {
	s := strings.TrimSpace(text)
	if len(s) > maxErrorBodyBytes {
		return s[:maxErrorBodyBytes]
	}
	return s
}
