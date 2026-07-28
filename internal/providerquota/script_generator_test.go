package providerquota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSystemPromptContainsContract(t *testing.T) {
	prompt := systemPromptForScript()
	for _, want := range []string{"extractor", "utilization", "window", "{{apiKey}}", "bodyType", "sandbox", "ALREADY PARSED", "JSON.parse"} {
		t.Run(want, func(t *testing.T) {
			if !strings.Contains(prompt, want) {
				t.Fatalf("system prompt does not contain %q", want)
			}
		})
	}
}

func TestBuildUserMessage(t *testing.T) {
	t.Run("includes all user fields", func(t *testing.T) {
		msg := buildUserMessage(GenerateScriptRequest{
			Prompt:         "query quota",
			ResponseSample: `{"balance": 1}`,
			RequestInfo:    "GET with auth",
		})
		for _, want := range []string{"Need: query quota", "GET with auth", `{"balance": 1}`} {
			if !strings.Contains(msg, want) {
				t.Fatalf("user message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("uses fallback for empty request info", func(t *testing.T) {
		msg := buildUserMessage(GenerateScriptRequest{
			Prompt:         "query quota",
			ResponseSample: `{"balance": 1}`,
		})
		if !strings.Contains(msg, "(not provided") {
			t.Fatalf("user message missing fallback:\n%s", msg)
		}
	})
}

func TestExtractScript(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "plain expression",
			input: `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance};}})`,
			want:  `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance};}})`,
		},
		{
			name:  "js fence",
			input: "```js\n({request:{url:\"{{baseUrl}}/balance\",method:\"GET\"},extractor:function(r){return {remaining:r.balance};}})\n```",
			want:  `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance};}})`,
		},
		{
			name:  "javascript fence",
			input: "```javascript\n({request:{url:\"{{baseUrl}}/balance\",method:\"GET\"},extractor:function(r){return {remaining:r.balance};}})\n```",
			want:  `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance};}})`,
		},
		{
			name:  "bare fence",
			input: "```\n({request:{url:\"{{baseUrl}}/balance\",method:\"GET\"},extractor:function(r){return {remaining:r.balance};}})\n```",
			want:  `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance};}})`,
		},
		{
			name:  "leading explanation tolerated",
			input: "Here is the script:\n({request:{url:\"{{baseUrl}}/balance\",method:\"GET\"},extractor:function(r){return {remaining:r.balance};}})",
			want:  `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance};}})`,
		},
		{
			name:  "trailing explanation tolerated",
			input: "({request:{url:\"{{baseUrl}}/balance\",method:\"GET\"},extractor:function(r){return {remaining:r.balance};}})\n\nHope this helps.",
			want:  `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance};}})`,
		},
		{
			name:  "explanation both sides with fence (zh LLM style)",
			input: "好的，这是脚本：\n```javascript\n({request:{url:\"{{baseUrl}}/balance\",method:\"GET\"},extractor:function(r){return {remaining:r.balance};}})\n```\n以上脚本会查询余额。",
			want:  `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance};}})`,
		},
		{name: "empty rejected", input: "", wantErr: true},
		{name: "no object literal rejected", input: "I cannot generate that script.", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractScript(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("extractScript() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("extractScript() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("script = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuditScript(t *testing.T) {
	cleanQwenScript := `({
  request: {
    url: "{{baseUrl}}/data/api.json",
    method: "POST",
    bodyType: "form",
    headers: {
      "Cookie": "{{apiKey}}",
      "Content-Type": "application/x-www-form-urlencoded"
    },
    body: {
      sec_token: "{{apiKey2}}",
      params: { Api: "usage" }
    }
  },
  extractor: function(response) {
    return { window: "seven_day", utilization: response.used_pct };
  }
})`

	tests := []struct {
		name        string
		requestInfo string
		script      string
		wantSubstrs []string
	}{
		{
			name:        "clean script has no warnings",
			requestInfo: "POST /data/api.json\nCookie: sid=abc\nsec_token=xyz",
			script:      cleanQwenScript,
		},
		{
			name:        "cookie in request info missing from script",
			requestInfo: "GET /usage\ncookie: sid=abc",
			script:      `({request:{url:"{{baseUrl}}/usage",method:"GET",headers:{"Authorization":"Bearer {{apiKey}}"}},extractor:function(response){return response;}})`,
			wantSubstrs: []string{"Cookie header"},
		},
		{
			name:        "authorization in request info missing from script",
			requestInfo: "GET /usage\nauthorization: bearer token",
			script:      `({request:{url:"{{baseUrl}}/usage",method:"GET",headers:{"Cookie":"{{apiKey}}"}},extractor:function(response){return response;}})`,
			wantSubstrs: []string{"Authorization/Bearer"},
		},
		{
			name:        "sec token in request info missing from script",
			requestInfo: "POST /usage\nsec_token=xyz",
			script:      `({request:{url:"{{baseUrl}}/usage",method:"POST",headers:{"Cookie":"{{apiKey}}"},body:{page:1}},extractor:function(response){return response;}})`,
			wantSubstrs: []string{"sec_token"},
		},
		{
			name:        "response body misuse",
			requestInfo: "",
			script:      `({request:{url:"{{baseUrl}}/usage",method:"GET",headers:{"Authorization":"Bearer {{apiKey}}"}},extractor:function(response){return response.body.data;}})`,
			wantSubstrs: []string{"response.body"},
		},
		{
			name:        "json parse response misuse",
			requestInfo: "",
			script:      `({request:{url:"{{baseUrl}}/usage",method:"GET",headers:{"Authorization":"Bearer {{apiKey}}"}},extractor:function(response){var data=JSON.parse(response);return data;}})`,
			wantSubstrs: []string{"JSON.parse(response)"},
		},
		{
			name:        "post empty body",
			requestInfo: "",
			script:      `({request:{url:"{{baseUrl}}/usage",method:"POST",headers:{"Authorization":"Bearer {{apiKey}}"},body: {}},extractor:function(response){return response;}})`,
			wantSubstrs: []string{"empty body"},
		},
		{
			name:        "no credential placeholder",
			requestInfo: "",
			script:      `({request:{url:"{{baseUrl}}/usage",method:"GET"},extractor:function(response){return response;}})`,
			wantSubstrs: []string{"no credential placeholder"},
		},
		{
			name:        "hardcoded url",
			requestInfo: "",
			script:      `({request:{url:"https://api.example.com/usage",method:"GET",headers:{"Authorization":"Bearer {{apiKey}}"}},extractor:function(response){return response;}})`,
			wantSubstrs: []string{"{{baseUrl}}"},
		},
		{
			name:        "multiple warnings",
			requestInfo: "POST /usage\nCookie: sid=abc\nAuthorization: Bearer abc",
			script:      `({request:{url:"https://api.example.com/usage",method:"POST",body:{}},extractor:function(response){return response.body;}})`,
			wantSubstrs: []string{"Cookie header", "Authorization/Bearer", "response.body", "empty body", "no credential placeholder", "{{baseUrl}}"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := auditScript(tt.requestInfo, tt.script)
			if len(warnings) != len(tt.wantSubstrs) {
				t.Fatalf("warnings len = %d, want %d; warnings=%v", len(warnings), len(tt.wantSubstrs), warnings)
			}
			joined := strings.Join(warnings, "\n")
			for _, substr := range tt.wantSubstrs {
				if !strings.Contains(joined, substr) {
					t.Fatalf("warnings = %v, want substring %q", warnings, substr)
				}
			}
		})
	}
}

func TestScriptGenerator(t *testing.T) {
	validScript := `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance,unit:"USD"};}})`

	t.Run("calls llm, extracts fenced script, and prevalidates request", func(t *testing.T) {
		var seenSystem, seenUser string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				System   string `json:"system"`
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			seenSystem = body.System
			if len(body.Messages) > 0 {
				seenUser = body.Messages[0].Content
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]string{{
					"type": "text",
					"text": "```js\n" + validScript + "\n```",
				}},
			})
		}))
		defer server.Close()

		result := GenerateScript(context.Background(), NewLLMClient(time.Second), LLMProvider{
			APIFormat: "anthropic",
			APIURL:    server.URL,
			APIToken:  "sk-test",
		}, GenerateScriptRequest{
			Model:          "claude-test",
			Prompt:         "query balance",
			ResponseSample: `{"balance":42}`,
			RequestInfo:    "GET /balance",
		}, time.Second)
		if result.ErrorCode != "" {
			t.Fatalf("GenerateScript() error = %s: %s", result.ErrorCode, result.ErrorMessage)
		}
		if result.Script != validScript {
			t.Fatalf("Script = %q, want %q", result.Script, validScript)
		}
		if _, err := (&ScriptExecutor{}).parseRequest(result.Script); err != nil {
			t.Fatalf("generated script did not parse: %v", err)
		}
		if !strings.Contains(seenSystem, "OUTPUT FORMAT") || !strings.Contains(seenUser, "query balance") || !strings.Contains(seenUser, `{"balance":42}`) {
			t.Fatalf("prompts not propagated; system=%q user=%q", seenSystem, seenUser)
		}
	})

	t.Run("passes llm errors through", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}))
		defer server.Close()

		result := GenerateScript(context.Background(), NewLLMClient(time.Second), LLMProvider{
			APIFormat: "openai_chat",
			APIURL:    server.URL,
			APIToken:  "sk-test",
		}, GenerateScriptRequest{
			Model:          "m",
			Prompt:         "p",
			ResponseSample: "{}",
		}, time.Second)
		if result.ErrorCode != "invalid_credentials" {
			t.Fatalf("ErrorCode = %q, want invalid_credentials", result.ErrorCode)
		}
	})

	t.Run("rejects non script llm output as invalid_response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not a script"}}]}`))
		}))
		defer server.Close()

		result := GenerateScript(context.Background(), NewLLMClient(time.Second), LLMProvider{
			APIFormat: "openai_chat",
			APIURL:    server.URL,
			APIToken:  "sk-test",
		}, GenerateScriptRequest{
			Model:          "m",
			Prompt:         "p",
			ResponseSample: "{}",
		}, time.Second)
		if result.ErrorCode != "invalid_response" {
			t.Fatalf("ErrorCode = %q, want invalid_response", result.ErrorCode)
		}
	})

	t.Run("rejects script that cannot parse request as script_error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"({extractor:function(r){return r;}})"}}]}`))
		}))
		defer server.Close()

		result := GenerateScript(context.Background(), NewLLMClient(time.Second), LLMProvider{
			APIFormat: "openai_chat",
			APIURL:    server.URL,
			APIToken:  "sk-test",
		}, GenerateScriptRequest{
			Model:          "m",
			Prompt:         "p",
			ResponseSample: "{}",
		}, time.Second)
		if result.ErrorCode != "script_error" {
			t.Fatalf("ErrorCode = %q, want script_error; message=%q", result.ErrorCode, result.ErrorMessage)
		}
	})

	t.Run("rejects missing user input", func(t *testing.T) {
		tests := []GenerateScriptRequest{
			{Model: "m", ResponseSample: "{}"},
			{Model: "m", Prompt: "p"},
		}
		for _, req := range tests {
			result := GenerateScript(context.Background(), NewLLMClient(time.Second), LLMProvider{
				APIFormat: "openai_chat",
				APIURL:    "https://api.example.com",
				APIToken:  "sk-test",
			}, req, time.Second)
			if result.ErrorCode != "invalid_config" {
				t.Fatalf("GenerateScript(%#v) ErrorCode = %q, want invalid_config", req, result.ErrorCode)
			}
		}
	})

	t.Run("populates script audit warnings", func(t *testing.T) {
		script := `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance,unit:"USD"};}})`
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + strconv.Quote(script) + `}}]}`))
		}))
		defer server.Close()

		result := GenerateScript(context.Background(), NewLLMClient(time.Second), LLMProvider{
			APIFormat: "openai_chat",
			APIURL:    server.URL,
			APIToken:  "sk-test",
		}, GenerateScriptRequest{
			Model:          "m",
			Prompt:         "p",
			ResponseSample: `{"balance":42}`,
			RequestInfo:    "GET /balance\nCookie: sid=abc",
		}, time.Second)
		if result.ErrorCode != "" {
			t.Fatalf("GenerateScript() error = %s: %s", result.ErrorCode, result.ErrorMessage)
		}
		if len(result.Warnings) == 0 {
			t.Fatalf("Warnings = nil, want cookie warning")
		}
		if !strings.Contains(strings.Join(result.Warnings, "\n"), "Cookie") {
			t.Fatalf("Warnings = %v, want Cookie warning", result.Warnings)
		}
	})
}
