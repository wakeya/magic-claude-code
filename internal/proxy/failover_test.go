package proxy

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/failover"
	"magic-claude-code/internal/usage"

	_ "modernc.org/sqlite"
)

// failoverRecorder 收集 usage 记录，用于断言只记录了最终供应商。
type failoverRecorder struct {
	mu   sync.Mutex
	recs []usage.RequestRecord
}

func (r *failoverRecorder) Record(req usage.RequestRecord, tok usage.TokenRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, req)
	return nil
}

func (r *failoverRecorder) providerIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, rec := range r.recs {
		out = append(out, rec.ProviderID)
	}
	return out
}

// newFailoverTestHandler 构造一个接入故障切换管理器的 Handler。
func newFailoverTestHandler(t *testing.T, cfg *config.Config, rec UsageRecorder) (*Handler, *failover.Manager, *config.MockStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	fstore := failover.NewStore(db)
	if err := fstore.Migrate(); err != nil {
		t.Fatalf("migrate failover: %v", err)
	}
	cfgStore := config.NewMockStore(cfg)
	mgr := failover.NewManager(fstore, cfgStore)
	h := NewHandler(cfgStore, http.DefaultTransport.(*http.Transport), rec)
	h.SetFailoverManager(mgr)
	return h, mgr, cfgStore
}

func anthropicProvider(id, name, apiURL string, mapping map[string]string) config.Provider {
	p := config.NewProvider(name, apiURL, id+"-token")
	p.ID = id
	p.APIFormat = config.APIFormatAnthropic
	if mapping != nil {
		p.ModelMappings = mapping
	} else {
		p.ModelMappings = map[string]string{}
	}
	return *p
}

func openAIChatProvider(id, name, apiURL string, mapping map[string]string) config.Provider {
	p := config.NewProvider(name, apiURL, id+"-token")
	p.ID = id
	p.APIFormat = config.APIFormatOpenAIChat
	p.ModelMappings = mapping
	return *p
}

const failoverRequestBody = `{"model":"claude-opus-4-8","stream":false,"messages":[{"role":"user","content":"hi"}]}`

func newFailoverRequest() *http.Request {
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(failoverRequestBody))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestFailoverDisabledPasses1308Through(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":1308,"message":"five hour quota exhausted"}}`))
	}))
	defer backend.Close()

	cfg := config.DefaultConfig()
	cfg.AutoFailoverEnabled = false
	cfg.Providers = []config.Provider{anthropicProvider("a", "A", backend.URL, nil)}
	cfg.ActiveProviderID = "a"

	h, _, cfgStore := newFailoverTestHandler(t, cfg, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newFailoverRequest())

	if rec.Code != 429 {
		t.Fatalf("status = %d, want 429 (disabled must pass through)", rec.Code)
	}
	loaded, _ := cfgStore.Load()
	if loaded.ActiveProviderID != "a" {
		t.Fatalf("active = %s, want a (no switch when disabled)", loaded.ActiveProviderID)
	}
}

func TestFailoverSwitchesSameMappedModelFirst(t *testing.T) {
	var bSeen bool
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":1308,"message":"five hour quota exhausted"}}`))
	}))
	defer backendA.Close()
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bSeen = true
		// 验证候选请求用的是 B 的 token + 映射模型。
		if r.Header.Get("Authorization") != "Bearer b-token" {
			t.Errorf("candidate auth = %q, want B's token", r.Header.Get("Authorization"))
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"glm-5.2","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer backendB.Close()

	cfg := config.DefaultConfig()
	cfg.AutoFailoverEnabled = true
	cfg.Providers = []config.Provider{
		anthropicProvider("a", "A", backendA.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
		anthropicProvider("b", "B", backendB.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
	}
	cfg.ActiveProviderID = "a"

	h, mgr, cfgStore := newFailoverTestHandler(t, cfg, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newFailoverRequest())

	if !bSeen {
		t.Fatal("expected candidate B to receive the replayed request")
	}
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 after failover to B", rec.Code)
	}
	loaded, _ := cfgStore.Load()
	if loaded.ActiveProviderID != "b" {
		t.Fatalf("active = %s, want b", loaded.ActiveProviderID)
	}
	var switched int
	for _, e := range mgr.Events(10, nil) {
		if e.Outcome == failover.OutcomeSwitched {
			switched++
		}
	}
	if switched != 1 {
		t.Fatalf("switched events = %d, want 1", switched)
	}
}

func TestFailoverFallsBackInProviderOrder(t *testing.T) {
	// A 失败；B 不同映射模型；C 可用且同映射模型在前？这里 A 是唯一同模型，
	// B 不同模型作 fallback。让 A(同模型)失败，候选只剩 B（fallback）。
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":1308,"message":"quota exhausted"}}`))
	}))
	defer backendA.Close()
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"kimi","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer backendB.Close()

	cfg := config.DefaultConfig()
	cfg.AutoFailoverEnabled = true
	cfg.Providers = []config.Provider{
		anthropicProvider("a", "A", backendA.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
		anthropicProvider("b", "B", backendB.URL, map[string]string{"claude-opus-4-8": "kimi"}),
	}
	cfg.ActiveProviderID = "a"

	h, _, cfgStore := newFailoverTestHandler(t, cfg, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newFailoverRequest())

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 via fallback B", rec.Code)
	}
	if loaded, _ := cfgStore.Load(); loaded.ActiveProviderID != "b" {
		t.Fatalf("active = %s, want b (fallback in order)", loaded.ActiveProviderID)
	}
}

func TestFailoverSkipsQuarantinedCandidate(t *testing.T) {
	// B 预先摘除 → 应跳过 B 切到 C。
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":1308,"message":"quota exhausted"}}`))
	}))
	defer backendA.Close()
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("quarantined B must not be tried")
	}))
	defer backendB.Close()
	backendC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"glm-5.2","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer backendC.Close()

	cfg := config.DefaultConfig()
	cfg.AutoFailoverEnabled = true
	cfg.Providers = []config.Provider{
		anthropicProvider("a", "A", backendA.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
		anthropicProvider("b", "B", backendB.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
		anthropicProvider("c", "C", backendC.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
	}
	cfg.ActiveProviderID = "a"

	h, mgr, _ := newFailoverTestHandler(t, cfg, nil)
	// 预先摘除 B。
	mgr.QuarantineFailed("b", failover.Classification{Eligible: true, Kind: failover.StateKindAvailability, DisabledUntil: time.Now().Add(time.Minute)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newFailoverRequest())
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 via C (B skipped)", rec.Code)
	}
}

func TestFailoverNeverChangesExposedModelRoute(t *testing.T) {
	var hits int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":1308,"message":"quota exhausted"}}`))
	}))
	defer backend.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("exposed-model route must never replay to another provider")
	}))
	defer other.Close()

	cfg := config.DefaultConfig()
	cfg.AutoFailoverEnabled = true
	a := anthropicProvider("a", "A", backend.URL, nil)
	a.ExposedModels = []config.ExposedModel{{ID: "glm-4.6", Label: "GLM", BackendModel: "glm-4.6"}}
	cfg.Providers = []config.Provider{
		a,
		anthropicProvider("b", "B", other.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
	}
	cfg.ActiveProviderID = "a"

	h, _, cfgStore := newFailoverTestHandler(t, cfg, nil)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"glm-4.6","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if hits != 1 {
		t.Fatalf("exposed-model request hits = %d, want exactly 1 (no replay)", hits)
	}
	if rec.Code != 429 {
		t.Fatalf("status = %d, want original 429 (no failover for exposed route)", rec.Code)
	}
	if loaded, _ := cfgStore.Load(); loaded.ActiveProviderID != "a" {
		t.Fatalf("active = %s, want a (exposed route must not change active)", loaded.ActiveProviderID)
	}
}

func TestFailoverDoesNotSwitchRequestOrModelErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"bare429", 429, `{"error":{"message":"rate limited"}}`},
		{"code1210", 400, `{"error":{"code":1210,"message":"invalid request"}}`},
		{"modelNotFound", 404, `{"error":{"type":"model_not_found","message":"no such model"}}`},
		{"toolError", 400, `{"error":{"message":"unknown tool_reference block"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				w.Write([]byte(c.body))
			}))
			defer backend.Close()
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("non-eligible error must not trigger failover replay")
			}))
			defer other.Close()

			cfg := config.DefaultConfig()
			cfg.AutoFailoverEnabled = true
			cfg.Providers = []config.Provider{
				anthropicProvider("a", "A", backend.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
				anthropicProvider("b", "B", other.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
			}
			cfg.ActiveProviderID = "a"

			h, _, cfgStore := newFailoverTestHandler(t, cfg, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, newFailoverRequest())
			if rec.Code != c.status {
				t.Fatalf("status = %d, want original %d (no switch)", rec.Code, c.status)
			}
			if loaded, _ := cfgStore.Load(); loaded.ActiveProviderID != "a" {
				t.Fatalf("active = %s, want a (no switch for %s)", loaded.ActiveProviderID, c.name)
			}
		})
	}
}

func TestFailoverSwitchesAvailabilityFailure(t *testing.T) {
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte(`{"error":"bad gateway"}`))
	}))
	defer backendA.Close()
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"glm-5.2","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer backendB.Close()

	cfg := config.DefaultConfig()
	cfg.AutoFailoverEnabled = true
	cfg.Providers = []config.Provider{
		anthropicProvider("a", "A", backendA.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
		anthropicProvider("b", "B", backendB.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
	}
	cfg.ActiveProviderID = "a"

	h, _, cfgStore := newFailoverTestHandler(t, cfg, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newFailoverRequest())
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 after 502 failover", rec.Code)
	}
	if loaded, _ := cfgStore.Load(); loaded.ActiveProviderID != "b" {
		t.Fatalf("active = %s, want b", loaded.ActiveProviderID)
	}
}

func TestFailoverAllCandidatesExhausted(t *testing.T) {
	// 所有候选都失败 → 客户端收到原始错误，不双写。
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":1308,"message":"quota exhausted"}}`))
	}))
	defer backendA.Close()
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer backendB.Close()

	cfg := config.DefaultConfig()
	cfg.AutoFailoverEnabled = true
	cfg.Providers = []config.Provider{
		anthropicProvider("a", "A", backendA.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
		anthropicProvider("b", "B", backendB.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
	}
	cfg.ActiveProviderID = "a"

	h, _, cfgStore := newFailoverTestHandler(t, cfg, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newFailoverRequest())
	// 全部耗尽：返回原始失败响应（A 的 429），active 不变。
	if rec.Code != 429 {
		t.Fatalf("status = %d, want original 429 (exhausted)", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "1308") {
		t.Fatalf("response body should be the original A error, got %s", body)
	}
	if loaded, _ := cfgStore.Load(); loaded.ActiveProviderID != "a" {
		t.Fatalf("active = %s, want a (no switch when exhausted)", loaded.ActiveProviderID)
	}
}

func TestFailoverRecordsOnlyFinalUsage(t *testing.T) {
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":1308,"message":"quota exhausted"}}`))
	}))
	defer backendA.Close()
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"glm-5.2","usage":{"input_tokens":3,"output_tokens":5}}`))
	}))
	defer backendB.Close()

	cfg := config.DefaultConfig()
	cfg.AutoFailoverEnabled = true
	cfg.Providers = []config.Provider{
		anthropicProvider("a", "A", backendA.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
		anthropicProvider("b", "B", backendB.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
	}
	cfg.ActiveProviderID = "a"

	rec0 := &failoverRecorder{}
	h, _, _ := newFailoverTestHandler(t, cfg, rec0)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newFailoverRequest())

	ids := rec0.providerIDs()
	if len(ids) != 1 {
		t.Fatalf("usage records = %v, want exactly 1 (final provider only)", ids)
	}
	if ids[0] != "b" {
		t.Fatalf("usage provider = %s, want b (final)", ids[0])
	}
}

func TestFailoverRebuildsOpenAIRequestFromCandidate(t *testing.T) {
	// 候选 B 是 OpenAI chat 格式：重放的 URL/认证/模型应来自 B。
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":1308,"message":"quota exhausted"}}`))
	}))
	defer backendA.Close()
	var gotPath, gotAuth, gotModel string
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if m, ok := body["model"].(string); ok {
			gotModel = m
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"chatcmpl-x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"},"index":0,"finish_reason":"stop"}],"model":"glm-5.2","usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer backendB.Close()

	cfg := config.DefaultConfig()
	cfg.AutoFailoverEnabled = true
	cfg.Providers = []config.Provider{
		anthropicProvider("a", "A", backendA.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
		openAIChatProvider("b", "B", backendB.URL, map[string]string{"claude-opus-4-8": "glm-5.2"}),
	}
	cfg.ActiveProviderID = "a"

	h, _, _ := newFailoverTestHandler(t, cfg, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newFailoverRequest())

	if gotAuth != "Bearer b-token" {
		t.Errorf("candidate auth = %q, want B's token", gotAuth)
	}
	if !strings.HasSuffix(gotPath, "/chat/completions") {
		t.Errorf("candidate path = %q, want OpenAI chat completions", gotPath)
	}
	if gotModel != "glm-5.2" {
		t.Errorf("candidate model = %q, want B's mapped glm-5.2", gotModel)
	}
}
