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

// AuditWarning is a structured script audit warning. Code is stable for i18n.
type AuditWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// GenerateScriptResult is the generated script or a structured error.
type GenerateScriptResult struct {
	Script       string
	Warnings     []AuditWarning
	Iterations   int
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

	model := strings.TrimSpace(req.Model)
	executor := &ScriptExecutor{}
	call := llm.Call(callCtx, provider, model, systemPromptForScript(), buildUserMessage(req))
	if call.ErrorCode != "" {
		return GenerateScriptResult{ErrorCode: call.ErrorCode, ErrorMessage: call.ErrorMessage}
	}

	script, err := extractScript(call.Text)
	if err != nil {
		return GenerateScriptResult{ErrorCode: "invalid_response", ErrorMessage: err.Error()}
	}

	if _, err := executor.parseRequest(script); err != nil {
		return GenerateScriptResult{
			ErrorCode:    "script_error",
			ErrorMessage: fmt.Sprintf("generated script failed to parse request: %v; llm_output=%s", err, summarizeLLMText(call.Text)),
		}
	}
	warnings := auditScript(req.RequestInfo, script)
	iterations := 1
	const maxIterations = 10
	for len(warnings) > 0 && iterations < maxIterations {
		fixCall := llm.Call(callCtx, provider, model, systemPromptForFix(), buildFixMessage(script, warnings, req))
		if fixCall.ErrorCode != "" {
			break
		}
		newScript, err := extractScript(fixCall.Text)
		if err != nil {
			break
		}
		if _, err := executor.parseRequest(newScript); err != nil {
			break
		}
		script = newScript
		warnings = auditScript(req.RequestInfo, script)
		iterations++
	}
	return GenerateScriptResult{Script: script, Warnings: warnings, Iterations: iterations}
}

// auditScript scans the user's request info and the generated script for
// common AI mistakes and returns human-readable warnings (empty if clean).
// Warnings are advisory; the script is still returned for the user to use or fix.
func auditScript(requestInfo, script string) []AuditWarning {
	warnings := make([]AuditWarning, 0)
	ri := strings.ToLower(requestInfo)
	sc := script // case-sensitive for placeholders

	// 1. Credential in request info but missing from script
	if (strings.Contains(ri, "cookie:") || strings.Contains(ri, "cookie =")) &&
		!strings.Contains(strings.ToLower(sc), "cookie") {
		warnings = append(warnings, AuditWarning{Code: "missing_cookie", Message: "request info contains a Cookie header but the script does not set Cookie - authentication will likely fail"})
	}
	if (strings.Contains(ri, "authorization:") || strings.Contains(ri, "bearer ")) &&
		!strings.Contains(strings.ToLower(sc), "authorization") {
		warnings = append(warnings, AuditWarning{Code: "missing_authorization", Message: "request info contains Authorization/Bearer but the script does not set the Authorization header"})
	}
	if strings.Contains(ri, "sec_token") && !strings.Contains(sc, "sec_token") {
		warnings = append(warnings, AuditWarning{Code: "missing_sec_token", Message: "request info contains sec_token but the script does not include a sec_token field in body/url"})
	}

	// 2. response fetch-API misuse (response is already a parsed JSON object)
	if strings.Contains(sc, "response.body") || strings.Contains(sc, "JSON.parse(response") {
		warnings = append(warnings, AuditWarning{Code: "response_body_misuse", Message: "script uses response.body or JSON.parse(response) - the extractor receives an already-parsed JSON object; use response.xxx directly"})
	}

	// 3. POST with empty body
	if strings.Contains(strings.ToLower(sc), "\"post\"") || strings.Contains(strings.ToLower(sc), "'post'") {
		if strings.Contains(sc, "body: {}") || strings.Contains(sc, "body:{}") {
			warnings = append(warnings, AuditWarning{Code: "empty_post_body", Message: "POST request with empty body {} - required fields are likely missing"})
		}
	}

	// 4. No credential placeholder (configured secrets won't be injected)
	if !strings.Contains(sc, "{{apiKey}}") && !strings.Contains(sc, "{{apiKey2}}") &&
		!strings.Contains(sc, "{{accessToken}}") && !strings.Contains(sc, "{{userId}}") {
		warnings = append(warnings, AuditWarning{Code: "no_credential_placeholder", Message: "script uses no credential placeholder ({{apiKey}}/{{apiKey2}}/{{accessToken}}/{{userId}}); configured secrets will not be injected"})
	}

	// 5. Hardcoded URL (no {{baseUrl}}) — same-origin check may reject
	if (strings.Contains(sc, "url:") || strings.Contains(sc, "url :")) && !strings.Contains(sc, "{{baseUrl}}") {
		warnings = append(warnings, AuditWarning{Code: "hardcoded_url", Message: "script URL does not use the {{baseUrl}} placeholder; the same-origin check may reject it"})
	}

	return warnings
}

func systemPromptForFix() string {
	return systemPromptForScript() + "\n\n" + "You are now in FIX mode. The user previously generated a script, but an automated audit found the issues below. Return a COMPLETE corrected script (same `({request, extractor})` format) that fixes every listed issue. Preserve the working parts of the previous script. Do not regress fields that were correct."
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
- utilization is ALWAYS "used percent" in 0-100. If the source field is a 0-1 ratio, multiply by 100. If the source is "remaining percent", compute 100 - remaining. When unsure whether a "Percentage"/"Usage"/"Rate" field means used vs remaining, DEFAULT to "used" (utilization = value * 100); only use (100 - value) if the field name explicitly contains "remaining"/"left" or the response sample clearly proves remaining semantics. Most quota APIs report "used", not "remaining".

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

func buildFixMessage(script string, warnings []AuditWarning, req GenerateScriptRequest) string {
	var b strings.Builder
	b.WriteString("Previous script:\n")
	b.WriteString(strings.TrimSpace(script))
	b.WriteString("\n\nAudit warnings (fix ALL of them):\n")
	for _, warning := range warnings {
		b.WriteString("- [")
		b.WriteString(warning.Code)
		b.WriteString("] ")
		b.WriteString(warning.Message)
		b.WriteByte('\n')
	}
	info := strings.TrimSpace(req.RequestInfo)
	if info == "" {
		info = "(not provided)"
	}
	b.WriteString("\nOriginal need: ")
	b.WriteString(strings.TrimSpace(req.Prompt))
	b.WriteString("\nOriginal response sample (authoritative for extractor field paths):\n")
	b.WriteString(strings.TrimSpace(req.ResponseSample))
	b.WriteString("\nRequest info (where the credentials live):\n")
	b.WriteString(info)
	b.WriteString("\n\nReturn the corrected full script.")
	return b.String()
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
