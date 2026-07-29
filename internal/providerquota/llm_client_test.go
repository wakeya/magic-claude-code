package providerquota

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testPublicLLMAPIURL = "http://93.184.216.34"

func TestLLMClientAnthropic(t *testing.T) {
	t.Run("posts messages request and extracts text", func(t *testing.T) {
		provider := LLMProvider{APIFormat: "anthropic", APIToken: "sk-test"}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/messages" {
				t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
			}
			if got := r.Header.Get("x-api-key"); got != provider.APIToken {
				t.Fatalf("x-api-key = %q, want token", got)
			}
			if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
				t.Fatalf("anthropic-version = %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["model"] != "claude-test" || body["system"] != "system prompt" {
				t.Fatalf("body = %#v", body)
			}
			messages := body["messages"].([]any)
			message := messages[0].(map[string]any)
			if message["role"] != "user" || message["content"] != "user prompt" {
				t.Fatalf("message = %#v", message)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"generated script"}]}`))
		}))
		defer server.Close()
		provider.APIURL = testPublicLLMAPIURL

		result := newLLMTestClient(server).Call(context.Background(), provider, "claude-test", "system prompt", "user prompt")
		if result.ErrorCode != "" {
			t.Fatalf("Call() error = %s: %s", result.ErrorCode, result.ErrorMessage)
		}
		if result.Text != "generated script" {
			t.Fatalf("Text = %q", result.Text)
		}
	})

	t.Run("does not duplicate v1 prefix", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/messages" {
				t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
		}))
		defer server.Close()

		provider := LLMProvider{APIFormat: "anthropic", APIURL: testPublicLLMAPIURL + "/v1", APIToken: "sk-test"}
		result := newLLMTestClient(server).Call(context.Background(), provider, "m", "s", "u")
		if result.ErrorCode != "" || result.Text != "ok" {
			t.Fatalf("Call() = %#v", result)
		}
	})
}

func TestLLMClientOpenAIChat(t *testing.T) {
	t.Run("posts chat request and extracts message content", func(t *testing.T) {
		provider := LLMProvider{APIFormat: "openai_chat", APIToken: "sk-test"}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/chat/completions" {
				t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer "+provider.APIToken {
				t.Fatalf("Authorization = %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			messages := body["messages"].([]any)
			first := messages[0].(map[string]any)
			second := messages[1].(map[string]any)
			if body["model"] != "gpt-test" || first["role"] != "system" || first["content"] != "system prompt" || second["role"] != "user" || second["content"] != "user prompt" {
				t.Fatalf("body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"chat text"}}]}`))
		}))
		defer server.Close()
		provider.APIURL = testPublicLLMAPIURL

		result := newLLMTestClient(server).Call(context.Background(), provider, "gpt-test", "system prompt", "user prompt")
		if result.ErrorCode != "" || result.Text != "chat text" {
			t.Fatalf("Call() = %#v", result)
		}
	})

	t.Run("does not duplicate v1 prefix", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/chat/completions" {
				t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		}))
		defer server.Close()

		provider := LLMProvider{APIFormat: "openai_chat", APIURL: testPublicLLMAPIURL + "/v1", APIToken: "sk-test"}
		result := newLLMTestClient(server).Call(context.Background(), provider, "m", "s", "u")
		if result.ErrorCode != "" || result.Text != "ok" {
			t.Fatalf("Call() = %#v", result)
		}
	})
}

func TestLLMClientOpenAIResponses(t *testing.T) {
	t.Run("posts responses request and extracts output_text", func(t *testing.T) {
		provider := LLMProvider{APIFormat: "openai_responses", APIToken: "sk-test"}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/responses" {
				t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer "+provider.APIToken {
				t.Fatalf("Authorization = %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["model"] != "resp-test" || body["instructions"] != "system prompt" || body["input"] != "user prompt" {
				t.Fatalf("body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"output_text":"responses text"}`))
		}))
		defer server.Close()
		provider.APIURL = testPublicLLMAPIURL

		result := newLLMTestClient(server).Call(context.Background(), provider, "resp-test", "system prompt", "user prompt")
		if result.ErrorCode != "" || result.Text != "responses text" {
			t.Fatalf("Call() = %#v", result)
		}
	})

	t.Run("extracts nested output content and chat fallback", func(t *testing.T) {
		tests := []struct {
			name string
			body string
			want string
		}{
			{name: "nested output_text", body: `{"output":[{"content":[{"type":"output_text","text":"nested text"}]}]}`, want: "nested text"},
			{name: "message content", body: `{"output":[{"content":[{"type":"message","text":"message text"}]}]}`, want: "message text"},
			{name: "chat fallback", body: `{"choices":[{"message":{"content":"chat fallback"}}]}`, want: "chat fallback"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(tt.body))
				}))
				defer server.Close()
				provider := LLMProvider{APIFormat: "openai_responses", APIURL: testPublicLLMAPIURL, APIToken: "sk-test"}
				result := newLLMTestClient(server).Call(context.Background(), provider, "m", "s", "u")
				if result.ErrorCode != "" || result.Text != tt.want {
					t.Fatalf("Call() = %#v, want text %q", result, tt.want)
				}
			})
		}
	})
}

func TestLLMClientErrors(t *testing.T) {
	t.Run("401 maps to invalid_credentials and redacts token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "bad token sk-secret", http.StatusUnauthorized)
		}))
		defer server.Close()

		provider := LLMProvider{APIFormat: "openai_chat", APIURL: testPublicLLMAPIURL, APIToken: "sk-secret"}
		result := newLLMTestClient(server).Call(context.Background(), provider, "m", "s", "u")
		if result.ErrorCode != "invalid_credentials" {
			t.Fatalf("ErrorCode = %q, want invalid_credentials", result.ErrorCode)
		}
		if strings.Contains(result.ErrorMessage, provider.APIToken) {
			t.Fatalf("ErrorMessage leaked token: %q", result.ErrorMessage)
		}
	})

	t.Run("timeout maps to request_timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"late"}}]}`))
		}))
		defer server.Close()

		provider := LLMProvider{APIFormat: "openai_chat", APIURL: testPublicLLMAPIURL, APIToken: "sk-test"}
		client := newLLMTestClient(server)
		client.HTTPClient.Timeout = 10 * time.Millisecond
		result := client.Call(context.Background(), provider, "m", "s", "u")
		if result.ErrorCode != "request_timeout" {
			t.Fatalf("ErrorCode = %q, want request_timeout; message=%q", result.ErrorCode, result.ErrorMessage)
		}
	})

	t.Run("network error maps to network_error", func(t *testing.T) {
		provider := LLMProvider{APIFormat: "openai_chat", APIURL: testPublicLLMAPIURL, APIToken: "sk-test"}
		client := &LLMClient{HTTPClient: &http.Client{
			Timeout: time.Second,
			Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, net.ErrClosed
			}),
		}}
		result := client.Call(context.Background(), provider, "m", "s", "u")
		if result.ErrorCode != "network_error" {
			t.Fatalf("ErrorCode = %q, want network_error; message=%q", result.ErrorCode, result.ErrorMessage)
		}
	})

	t.Run("missing token maps to missing_credentials", func(t *testing.T) {
		provider := LLMProvider{APIFormat: "anthropic", APIURL: "https://api.example.com"}
		result := NewLLMClient(time.Second).Call(context.Background(), provider, "m", "s", "u")
		if result.ErrorCode != "missing_credentials" {
			t.Fatalf("ErrorCode = %q, want missing_credentials", result.ErrorCode)
		}
	})

	t.Run("bad format maps to invalid_config", func(t *testing.T) {
		provider := LLMProvider{APIFormat: "unknown", APIURL: "https://api.example.com", APIToken: "sk-test"}
		result := NewLLMClient(time.Second).Call(context.Background(), provider, "m", "s", "u")
		if result.ErrorCode != "invalid_config" {
			t.Fatalf("ErrorCode = %q, want invalid_config", result.ErrorCode)
		}
	})

	t.Run("invalid json maps to invalid_response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`not-json`))
		}))
		defer server.Close()

		provider := LLMProvider{APIFormat: "openai_chat", APIURL: testPublicLLMAPIURL, APIToken: "sk-test"}
		result := newLLMTestClient(server).Call(context.Background(), provider, "m", "s", "u")
		if result.ErrorCode != "invalid_response" {
			t.Fatalf("ErrorCode = %q, want invalid_response", result.ErrorCode)
		}
	})
}

func TestLLMClientRejectsLoopback(t *testing.T) {
	provider := LLMProvider{APIFormat: "openai_chat", APIURL: "http://127.0.0.1:65535", APIToken: "sk-test"}
	client := &LLMClient{HTTPClient: &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("LLMClient attempted request to loopback endpoint")
			return nil, nil
		}),
	}}

	result := client.Call(context.Background(), provider, "m", "s", "u")
	if result.ErrorCode != "invalid_config" {
		t.Fatalf("ErrorCode = %q, want invalid_config; message=%q", result.ErrorCode, result.ErrorMessage)
	}
	if strings.Contains(result.ErrorMessage, "127.0.0.1") {
		t.Fatalf("ErrorMessage leaked endpoint URL: %q", result.ErrorMessage)
	}
}

func TestLLMClientRejectsMetadata(t *testing.T) {
	provider := LLMProvider{APIFormat: "openai_chat", APIURL: "http://169.254.169.254", APIToken: "sk-test"}
	client := &LLMClient{HTTPClient: &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("LLMClient attempted request to metadata endpoint")
			return nil, nil
		}),
	}}

	result := client.Call(context.Background(), provider, "m", "s", "u")
	if result.ErrorCode != "invalid_config" {
		t.Fatalf("ErrorCode = %q, want invalid_config; message=%q", result.ErrorCode, result.ErrorMessage)
	}
}

func TestLLMClientRejectsRedirect(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"followed redirect"}}]}`))
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, httptest.NewRequest(http.MethodPost, "/unused", nil), target.URL+"/v1/chat/completions", http.StatusFound)
	}))
	defer source.Close()

	provider := LLMProvider{APIFormat: "openai_chat", APIURL: testPublicLLMAPIURL, APIToken: "sk-test"}
	result := newLLMTestClient(source).Call(context.Background(), provider, "m", "s", "u")
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target hits = %d, want 0", targetHits.Load())
	}
	if result.ErrorCode == "" {
		t.Fatalf("Call() unexpectedly succeeded after redirect: %#v", result)
	}
}

func TestLLMClient404NoBodyLeak(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "sensitive upstream body", http.StatusNotFound)
	}))
	defer server.Close()

	provider := LLMProvider{APIFormat: "openai_chat", APIURL: testPublicLLMAPIURL, APIToken: "sk-test"}
	result := newLLMTestClient(server).Call(context.Background(), provider, "m", "s", "u")
	if result.ErrorCode != "upstream_http_error" {
		t.Fatalf("ErrorCode = %q, want upstream_http_error; message=%q", result.ErrorCode, result.ErrorMessage)
	}
	if result.ErrorMessage != "HTTP 404" {
		t.Fatalf("ErrorMessage = %q, want HTTP 404", result.ErrorMessage)
	}
	if strings.Contains(result.ErrorMessage, "sensitive") {
		t.Fatalf("ErrorMessage leaked upstream body: %q", result.ErrorMessage)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newLLMTestClient(server *httptest.Server) *LLMClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, server.Listener.Addr().String())
		},
	}
	return &LLMClient{HTTPClient: &http.Client{
		Timeout:   time.Second,
		Transport: transport,
	}}
}
