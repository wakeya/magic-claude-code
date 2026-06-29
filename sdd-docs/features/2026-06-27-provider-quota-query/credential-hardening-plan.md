# Provider Quota Credential Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复原生 Token Plan 重定向泄漏、JSON Store 旧凭据未迁移，以及密钥替换与清除冲突三个已复现问题。

**Architecture:** 在 `TokenPlanAdapter` 的统一请求边界复制并加固 HTTP Client；在 `Config.NormalizeDefaults` 统一执行幂等凭据迁移；在前端 payload 构建和后端请求入口双重保证 secret replace/clear 互斥。每个问题独立完成 RED、GREEN 和提交。

**Tech Stack:** Go 1.26、`net/http`、`httptest`、Vue 3、TypeScript、Node test runner。

---

## 文件职责

- `internal/providerquota/token_plan.go`：原生 Token Plan HTTP 请求和安全重定向策略。
- `internal/providerquota/token_plan_test.go`：认证请求重定向回归测试。
- `internal/config/config.go`：所有 ConfigStore 共享的默认值归一化与旧凭据迁移入口。
- `internal/config/store_test.go`：JSON Store 加载旧配置的迁移测试。
- `internal/frontend/src/utils/quotaForm.ts`：保存请求 payload 的 secret replace/clear 互斥规则。
- `internal/frontend/src/utils/quotaForm.test.ts`：前端 payload 行为测试。
- `internal/admin/provider_quota_handler.go`：PUT 和 test 请求的 secret patch 冲突校验。
- `internal/admin/provider_quota_handler_test.go`：API 边界拒绝含糊请求的测试。

### Task 1: 加固 Token Plan 认证请求重定向

**Files:**
- Modify: `internal/providerquota/token_plan_test.go`
- Modify: `internal/providerquota/token_plan.go:24-28,896-910`

- [ ] **Step 1: 写 HTTPS 降级和跨源重定向失败测试**

在 `token_plan_test.go` 增加表驱动测试。HTTPS source 返回 `302`，目标 handler 记录 `Authorization`；调用真实 `TokenPlanAdapter.Query(..., "zenmux", ..., "card-secret")` 后断言目标没有收到凭据，结果为失败：

```go
func TestTokenPlanAdapterRejectsUnsafeAuthenticatedRedirects(t *testing.T) {
	tests := []struct {
		name      string
		newTarget func(http.Handler) *httptest.Server
	}{
		{"https to http", httptest.NewServer},
		{"cross origin https", httptest.NewTLSServer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth string
			target := tt.newTarget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusOK)
			}))
			defer target.Close()

			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, http.StatusFound)
			}))
			defer source.Close()

			adapter := &TokenPlanAdapter{HTTPClient: source.Client()}
			result := adapter.Query(context.Background(), "zenmux", &ProviderQuotaConfig{}, source.URL, "card-secret")
			if gotAuth != "" {
				t.Fatalf("unsafe redirect target received Authorization: %q", gotAuth)
			}
			if result.Success {
				t.Fatal("unsafe redirect unexpectedly succeeded")
			}
		})
	}
}
```

- [ ] **Step 2: 运行测试并确认 RED**

Run:

```bash
go test ./internal/providerquota -run TestTokenPlanAdapterRejectsUnsafeAuthenticatedRedirects -count=1 -v
```

Expected: FAIL，至少 HTTPS→HTTP 子用例显示目标收到 `Bearer card-secret`。

- [ ] **Step 3: 写同源 HTTPS 重定向正向测试**

使用单个 `httptest.NewTLSServer`，`/start` 相对跳转到 `/final`，`/final` 返回合法 ZenMux JSON并记录 Authorization。断言查询成功且最终请求仍携带 `Bearer card-secret`：

```go
func TestTokenPlanAdapterAllowsSameOriginHTTPSRedirect(t *testing.T) {
	var gotAuth string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota_5_hour":{"usage_percentage":0.1},"quota_7_day":{"usage_percentage":0.2}}}`))
	}))
	defer server.Close()

	adapter := &TokenPlanAdapter{HTTPClient: server.Client()}
	result := adapter.Query(context.Background(), "zenmux", &ProviderQuotaConfig{}, server.URL+"/start", "card-secret")
	if !result.Success || gotAuth != "Bearer card-secret" {
		t.Fatalf("result=%+v Authorization=%q", result, gotAuth)
	}
}
```

- [ ] **Step 4: 实现最小安全 Client 副本**

在 `token_plan.go` 增加：

```go
func authenticatedHTTPClient(base *http.Client, original *url.URL) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	previous := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !sameOrigin(req.URL, original) {
			return fmt.Errorf("authenticated redirect rejected: %s://%s -> %s://%s", original.Scheme, original.Host, req.URL.Scheme, req.URL.Host)
		}
		if previous != nil {
			return previous(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &client
}
```

`doRequest` 使用 `authenticatedHTTPClient(a.HTTPClient, req.URL).Do(req)`。补充 `errors` import。不要修改注入的原始 Client。

- [ ] **Step 5: 运行定向测试并确认 GREEN**

```bash
go test ./internal/providerquota -run 'TestTokenPlanAdapter(RejectsUnsafeAuthenticatedRedirects|AllowsSameOriginHTTPSRedirect)' -count=1 -v
go test ./internal/providerquota -count=1
```

Expected: PASS。

- [ ] **Step 6: 提交 Task 1**

```bash
git add internal/providerquota/token_plan.go internal/providerquota/token_plan_test.go
git commit -m "fix(providerquota): reject unsafe authenticated redirects"
```

### Task 2: 统一 JSON Store 旧凭据迁移

**Files:**
- Modify: `internal/config/store_test.go`
- Modify: `internal/config/config.go:161-179`

- [ ] **Step 1: 写 JSON Store 迁移表驱动测试**

在 `store_test.go` 添加 General 和旧 ZenMux 两个 JSON fixture，使用 `NewStore(path).Load()`，分别断言：

```go
func TestJSONStoreMigratesLegacyQuotaCredentials(t *testing.T) {
	tests := []struct {
		name      string
		quotaJSON string
		assert    func(*testing.T, *providerquota.ProviderQuotaConfig)
	}{
		{
			name: "general api_key becomes script key",
			quotaJSON: `{"enabled":true,"template_type":"general","timeout_seconds":10,"api_key":"legacy-script"}`,
			assert: func(t *testing.T, q *providerquota.ProviderQuotaConfig) {
				if q.ScriptAPIKey != "legacy-script" || q.LegacyAPIKey != "" { t.Fatalf("quota=%+v", q) }
			},
		},
		{
			name: "legacy zenmux pair becomes atomic override",
			quotaJSON: `{"enabled":true,"template_type":"token_plan","timeout_seconds":10,"base_url":"https://quota.zenmux.example/usage","api_key":"legacy-zenmux"}`,
			assert: func(t *testing.T, q *providerquota.ProviderQuotaConfig) {
				if q.ZenMuxBaseURL != "https://quota.zenmux.example/usage" || q.ZenMuxAPIKey != "legacy-zenmux" || q.BaseURL != "" || q.LegacyAPIKey != "" { t.Fatalf("quota=%+v", q) }
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			data := []byte(`{"providers":[{"id":"p1","name":"p1","api_url":"https://gateway.example/v1","api_token":"card-token","enabled":true,"quota_query":` + tt.quotaJSON + `}]}`)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := NewStore(path).Load()
			if err != nil {
				t.Fatal(err)
			}
			tt.assert(t, cfg.Providers[0].QuotaQuery)
		})
	}
}
```

- [ ] **Step 2: 运行测试并确认 RED**

```bash
go test ./internal/config -run TestJSONStoreMigratesLegacyQuotaCredentials -count=1 -v
```

Expected: FAIL，旧字段仍在 `LegacyAPIKey`。

- [ ] **Step 3: 在公共归一化入口迁移**

修改 `Config.NormalizeDefaults` 的 provider 循环：

```go
for i := range c.Providers {
	c.Providers[i].normalizeDefaults()
	providerquota.MigrateLegacyCredentials(c.Providers[i].QuotaQuery, c.Providers[i].APIURL)
}
```

在 `config.go` import `magic-claude-code/internal/providerquota`。保留 SQLite 显式迁移，依靠幂等契约。

- [ ] **Step 4: 运行定向和 config 全包测试并确认 GREEN**

```bash
go test ./internal/config -run 'TestJSONStoreMigratesLegacyQuotaCredentials|TestSQLiteStore.*Quota' -count=1 -v
go test ./internal/config -count=1
```

Expected: PASS。

- [ ] **Step 5: 提交 Task 2**

```bash
git add internal/config/config.go internal/config/store_test.go
git commit -m "fix(config): migrate legacy quota credentials on JSON load"
```

### Task 3: 前端 secret replace/clear 互斥

**Files:**
- Modify: `internal/frontend/src/utils/quotaForm.test.ts`
- Modify: `internal/frontend/src/utils/quotaForm.ts:132-152`

- [ ] **Step 1: 写四类替换优先测试**

在 `quotaForm.test.ts` 添加：

```ts
test('buildSavePayload does not emit clear flags with replacement secrets', () => {
  const script = buildSavePayload({...baseForm, script_api_key: 'replacement', clear_script_api_key: true}, '', null)
  assert.equal(script.script_api_key, 'replacement')
  assert.equal('clear_script_api_key' in script, false)

  const zenmux = buildSavePayload({...baseForm, template_type: 'token_plan', coding_plan_provider: 'zenmux', zenmux_base_url: 'https://quota.zenmux.example/usage', zenmux_api_key: 'replacement', clear_zenmux_api_key: true}, '', null)
  assert.equal(zenmux.zenmux_api_key, 'replacement')
  assert.equal('clear_zenmux_api_key' in zenmux, false)

  const newapi = buildSavePayload({...baseForm, template_type: 'newapi', access_token: 'replacement', clear_access_token: true}, '', null)
  assert.equal(newapi.access_token, 'replacement')
  assert.equal('clear_access_token' in newapi, false)

  const volcengine = buildSavePayload({...baseForm, template_type: 'token_plan', coding_plan_provider: 'volcengine', secret_access_key: 'replacement', clear_secret_access_key: true}, '', null)
  assert.equal(volcengine.secret_access_key, 'replacement')
  assert.equal('clear_secret_access_key' in volcengine, false)
})
```

- [ ] **Step 2: 运行前端测试并确认 RED**

```bash
npm --prefix internal/frontend test -- --test-name-pattern='does not emit clear flags with replacement secrets'
```

Expected: FAIL，payload 同时包含 replacement 和 clear flag。

- [ ] **Step 3: 修改 payload 构建条件**

只在当前模板实际发送的对应替换值为空时发送 clear：

```ts
const replacesScriptAPIKey = usesScriptAPIKey && !!form.script_api_key
const replacesZenMuxAPIKey = zenmux && !!form.zenmux_api_key
const replacesAccessToken = usesAccessToken && !!form.access_token
const replacesSecretAccessKey = usesVolcSK && !!form.secret_access_key
if (form.clear_script_api_key && !replacesScriptAPIKey) data.clear_script_api_key = true
if (form.clear_zenmux_api_key && !replacesZenMuxAPIKey) data.clear_zenmux_api_key = true
if (form.clear_access_token && !replacesAccessToken) data.clear_access_token = true
if (form.clear_secret_access_key && !replacesSecretAccessKey) data.clear_secret_access_key = true
```

NewAPI 和 Volcengine 离开适用模板时的自动清理条件保持不变；此时替换字段不会被发送，因此不属于 replace/clear 冲突。

- [ ] **Step 4: 运行前端测试并确认 GREEN**

```bash
npm --prefix internal/frontend test
```

Expected: 所有测试 PASS。

- [ ] **Step 5: 提交 Task 3**

```bash
git add internal/frontend/src/utils/quotaForm.ts internal/frontend/src/utils/quotaForm.test.ts
git commit -m "fix(frontend): make quota secret replace and clear exclusive"
```

### Task 4: 后端拒绝含糊 secret patch

**Files:**
- Modify: `internal/admin/provider_quota_handler_test.go`
- Modify: `internal/admin/provider_quota_handler.go:122-205,283-304`

- [ ] **Step 1: 写 PUT 和 test 的冲突请求测试**

建立包含一个 provider 的 MockStore，对以下四组字段分别请求 PUT `/usage` 和 POST `/usage/test`：

```go
tests := []struct {
	valueField string
	clearField string
}{
	{"script_api_key", "clear_script_api_key"},
	{"zenmux_api_key", "clear_zenmux_api_key"},
	{"access_token", "clear_access_token"},
	{"secret_access_key", "clear_secret_access_key"},
}
```

请求 body 同时包含 `valueField: "replacement"` 和 `clearField: true`。断言两个 endpoint 均返回 400，响应包含字段名，Store 中原配置未改变；test endpoint 在无 Manager 时也必须先返回 400 而不是 500。

- [ ] **Step 2: 运行测试并确认 RED**

```bash
go test ./internal/admin -run TestProviderUsageRejectsConflictingSecretPatches -count=1 -v
```

Expected: FAIL，PUT 接受请求或 test 进入后续路径。

- [ ] **Step 3: 增加请求边界校验**

实现：

```go
func validateProviderQuotaSecretPatches(req providerQuotaUpdateRequest) error {
	tests := []struct {
		name  string
		value *string
		clear bool
	}{
		{"script_api_key", req.ScriptAPIKey, req.ClearScriptAPIKey},
		{"zenmux_api_key", req.ZenMuxAPIKey, req.ClearZenMuxAPIKey},
		{"access_token", req.AccessToken, req.ClearAccessToken},
		{"secret_access_key", req.SecretAccessKey, req.ClearSecretAccessKey},
	}
	for _, item := range tests {
		if item.clear && item.value != nil && *item.value != "" {
			return fmt.Errorf("%s cannot be replaced and cleared in the same request", item.name)
		}
	}
	return nil
}
```

在 `updateProviderUsage` 和 `handleProviderUsageTest` JSON 解码成功后、加载配置前调用。错误编码为 `{"error": err.Error()}` 并返回 400。不要改变 `applySecretPatch` 的 clear-first 契约。

- [ ] **Step 4: 运行定向和 admin 全包测试并确认 GREEN**

```bash
go test ./internal/admin -run 'TestProviderUsageRejectsConflictingSecretPatches|TestApplyQuotaUpdateSecretPatch' -count=1 -v
go test ./internal/admin -count=1
```

Expected: PASS。

- [ ] **Step 5: 提交 Task 4**

```bash
git add internal/admin/provider_quota_handler.go internal/admin/provider_quota_handler_test.go
git commit -m "fix(admin): reject conflicting quota secret patches"
```

### Task 5: 全量验证和交付检查

**Files:**
- Verify only; no production changes expected.

- [ ] **Step 1: 运行 Go 全量测试**

```bash
go test ./...
```

Expected: PASS。

- [ ] **Step 2: 运行关键包 race 测试**

```bash
go test -race ./internal/providerquota ./internal/admin ./internal/config
```

Expected: PASS，无 data race。

- [ ] **Step 3: 运行静态检查和构建**

```bash
go vet ./...
go build ./...
```

Expected: exit 0。

- [ ] **Step 4: 运行前端测试和构建**

```bash
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

Expected: 全部测试 PASS，Vite build 成功；更新后的 `internal/frontend/dist` 属于本次改动。

- [ ] **Step 5: 检查 diff 和工作树**

```bash
git diff --check
git status --short
git log --oneline -6
```

Expected: 无 whitespace 错误；只包含本轮相关源码、测试和前端构建产物；不包含 `review-notes.md` 或 `review-notes_ZH.md`。

- [ ] **Step 6: 提交前端构建产物（如有）**

```bash
git add internal/frontend/dist
git commit -m "build(frontend): refresh quota credential assets"
```

仅当 `git status --short internal/frontend/dist` 有输出时执行。
