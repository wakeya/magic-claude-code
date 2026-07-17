# Claude Code 2.1.211 端点兼容规格

本地页面：无（纯代理后端逻辑）
代理入口：`internal/proxy/handler.go` → `handleHardcodedEndpoint`（`internal/proxy/hardcoded.go:107`）
参考源站：`claude_code_src_2.1.211.js`（对照 `2.1.206`，位于 `073_claude_spy/`）
技术栈：Go 1.26 标准库（`net/http`）
最后更新：2026-07-15
进度：4 / 4
关联 issue：[#17](https://github.com/wakeya/magic-claude-code/issues/17)

## 整体分析（源站分析）

### 分析方法

1. 用 `comm` 双向 diff `2.1.206` 与 `2.1.211` 的 API 路径字面量与模板字符串（`${...}` 占位符剥离），定位版本间新增路径。
2. 用 `await client.get/post(...)` 模式筛选"活的"运行时请求，排除 Anthropic SDK 类常量（`/v1/agents`、`/v1/vaults` 等）与 cURL 文档示例（`# Claude API — cURL / Raw HTTP` 段）。
3. 逐个查阅客户端响应处理逻辑（`validateStatus`、失败分支、是否重试、是否抛异常），判断 404 对客户端的影响。

### 安全前提

`classifyForwardingEndpoint`（`internal/proxy/endpoint_policy.go:31`）使用 **map 精确匹配**，转发白名单仅：

```go
var modelForwardPaths = map[string]struct{}{
    "/v1/messages":           {},
    "/anthropic/v1/messages": {},
}
```

下列 8 个端点均不命中转发白名单，当前全部走 `handleBlockedEndpoint` 返回本地 `404 mcc_blocked_unknown_endpoint`，**不会转发到第三方 provider，无数据泄露风险**。本规格仅解决功能兼容性（避免客户端抛异常 / 遥测噪音），不涉及转发或泄露。

### A 组 — 当前 404 已是最优兼容，保持不动（5 个）

| 端点 | 客户端响应处理（源码证据） | 结论 |
|------|------|------|
| `GET /v1/code/local/memory/mounts` | `validateStatus:(r)=>r===200\|\|r===404`；`status===404 → {kind:"off"}`，缓存 24h（`aLg=86400000`） | 404 = 客户端定义的"memory 未启用"正常分支，作为 discovery 门控短路整个 memory 链路 |
| `POST /v1/code/local/memory/credential` | `validateStatus:()=>!0`；仅在 discovery(mounts) 返回 `{kind:"stores"}` 后调用 | mounts=404→off 时不触发 |
| `/v1/code/memory/*`（读写） | 依赖 discovery 拿到 stores | 当前不被触发 |
| `POST /v1/code/github/import-token` | 上传 `token.reveal()`（GitHub token）；失败→`{ok:false}` | 安全敏感，404 让登录失败、token 不外泄 |
| `POST /v1/filestore/fs/readFile` | `if(!i.ok) return {ok:false}` + warn `stage_file_read_gated` | 优雅降级 |

### B 组 — 需补 hardcoded 兼容响应（3 个路径）

| 端点 | 客户端响应处理 | 本地响应 | 理由 |
|------|------|------|------|
| `GET /v1/design/grants` | `status!==200→null`；404 触发遥测 `probe_404_old_server` | `200 {grants:[]}` | 空授权列表禁用 Design 授权，避免 404 遥测噪音 |
| `POST /v1/design/grants` | `validateStatus:(n)=>n<300\|\|n===404`；`!ok→throw Error` | `403 {error,reason:"write_gate_disabled"}` | 与 frame deploy 写入门一致，明确"写关闭"而非 404 |
| `GET /v1/ultrareview/preflight` | `if(!t.ok) switch(t.reason){...}` | `200 {}` | 与同族 `/v1/ultrareview/quota` 一致（`handleEmptyResponse`） |
| `GET /v1/code/triggers` | `if(!e.ok) throw Error("triggers unavailable")`（容错最差） | `200 {data:[]}` | 空列表避免抛异常；POST 写入返回 403 |

> `/v1/design/grants` 单路径区分 GET/POST 两种响应；`/v1/code/triggers` 用前缀覆盖 list 与 `/triggers/{id}` 子路径。

### 风险总结

1. **不过度激活 memory 链路**：A 组保持 404 是有意为之。若为 mounts 返回 200+假数据，会激活 credential / memory 读写，引入更复杂的行为面，反而增加风险。
2. **triggers 语义瑕疵**：GET `/v1/code/triggers/{id}`（单个）也会返回 `{data:[]}`（列表形态），语义不完美。但第三方场景用户不使用 CCR 触发器，目标只是"不抛异常"，可接受。
3. **grants POST 的 403 必须带 `reason:"write_gate_disabled"`**：客户端据此识别写入门关闭（参考 `frame.go:50` 的 deploy init/direct）。
4. **不改 `modelForwardPaths`**：这 8 个均非模型推理端点，转发白名单保持不变。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
|------|------|------|------|------|
| 1 | 已完成 | design grants GET/POST 分流 | `hardcoded.go` + `hardcoded_test.go` | 单测：GET `{grants:[]}`、POST 403、PUT 405 |
| 2 | 已完成 | ultrareview preflight 并入 quota 组 | `hardcoded.go` + `hardcoded_test.go` | 单测：GET `200 {}` |
| 3 | 已完成 | triggers 前缀 + 单独 handler | `hardcoded.go` + `triggers.go` + `triggers_test.go` | 单测：GET `{data:[]}`、POST 403、子路径覆盖 |
| 4 | 已完成 | 基线文档更新 52→55 | `2026-07-15-intercepted-endpoints.md` | 数量校验脚本输出 55 |

## 需求

### 交付物

1. `isHardcodedEndpoint`（`internal/proxy/hardcoded.go:19`）新增：
   - exactMatches：`/v1/design/grants`、`/v1/ultrareview/preflight`
   - prefixMatches：`/v1/code/triggers`
2. `handleHardcodedEndpoint`（`hardcoded.go:107`）switch 新增分支（详见各任务 Plan 的精确插入点）。
3. 新增 `internal/proxy/triggers.go`，实现 `handleTriggersEndpoint`。
4. 单元测试覆盖所有新分支（代码见各任务 Plan）。
5. 更新 `sdd-docs/research/2026-07-15-intercepted-endpoints.md`（逐条修改见任务 4）。

### 目录结构

```text
internal/proxy/
  hardcoded.go          （修改：isHardcodedEndpoint + handleHardcodedEndpoint switch）
  hardcoded_test.go     （修改：3 个表格加路径 + 2 个新测试函数/子测试）
  triggers.go           （新增：handleTriggersEndpoint）
  triggers_test.go      （新增）
sdd-docs/research/
  2026-07-15-intercepted-endpoints.md  （修改：清单 52→55）
sdd-docs/features/2026-07-15-cc-2.1.211-endpoint-compat/
  spec.md / spec_ZH.md  （新增）
```

### 约束

1. 不修改 `modelForwardPaths`（`endpoint_policy.go`）。
2. `/v1/design/grants` 按 `r.Method` 分流：GET→200、POST→403、其他→405。
3. POST grants 与 POST triggers 的 403 响应体必须含 `"reason":"write_gate_disabled"`。
4. `/v1/code/triggers` 用前缀匹配（覆盖 `/triggers` 与 `/triggers/{id}`）。
5. A 组 5 个端点保持现状（fail-closed 404）。
6. 新 handler 函数需有文档注释；测试风格与现有 `hardcoded_test.go` 一致。

### 边界情况

1. grants/preflight 带 query（如 `?beta=true`）——`r.URL.Path` 已剥离 query。
2. `/v1/code/triggers/{id}`——前缀命中，GET 返回 `{data:[]}`。
3. PUT/DELETE 等方法——返回 405 + `Allow` 头。
4. POST grants 带 `project_id` body——`drainRequestBodyLimited` 已 drain，不解析。

### 非目标

1. 不为 A 组 5 个端点新增本地 mock。
2. 不实现 memory / design / triggers 的真实业务逻辑。
3. 不将任何非模型端点加入转发白名单。
4. 不处理 Anthropic SDK 类常量路径。

## 任务详情

### 任务 1：design grants GET/POST 分流

#### 需求

**Objective（目标）** — 为 `/v1/design/grants` 提供本地兼容响应：GET 返回空授权禁用 Design，POST 返回写入门关闭。

**Outcomes（成果）** — `isHardcodedEndpoint` 精确匹配新增 `/v1/design/grants`；`handleHardcodedEndpoint` switch 新增 case 按 `r.Method` 分流；`hardcoded_test.go` 新增 `TestHardcodedDesignGrants`。

**Evidence（证据）** — `go test ./internal/proxy/ -run TestHardcodedDesignGrants -v` 三个子测试全绿。

**Constraints（约束）** — POST 403 body 结构与 `frame.go:50` write_gate 一致；不改转发白名单。

**Edge Cases（边界）** — `?beta=true`（path 已剥离）；非 GET/POST 方法；POST 带 project_id body。

**Verification（验证）** — `go test ./internal/proxy/ -run TestHardcodedDesignGrants -v` 通过。

#### 计划

**步骤 1.1** — `isHardcodedEndpoint` exactMatches（`hardcoded.go:61-64`）在 `"/v1/design/mcp"` 后新增一行：

```go
		// Claude Design consent / MCP bridge
		"/v1/design/consent",
		"/v1/design/mcp",
		"/v1/design/grants", // GET 空授权 / POST 写入门关闭（CC 2.1.211）
```

**步骤 1.2** — `handleHardcodedEndpoint` switch 在 `/v1/design/mcp` case（`hardcoded.go:302-304`）之后插入新 case：

```go
	// Claude Design MCP bridge - POST 返回受控 unsupported
	case path == "/v1/design/mcp":
		h.handleDesignMCP(w, r)
		return true

	// Claude Design grants - GET 返回空授权（禁用 Design 授权）；POST 写入门关闭
	case path == "/v1/design/grants":
		switch r.Method {
		case http.MethodGet:
			writeJSONResponse(w, http.StatusOK, map[string]any{"grants": []any{}})
		case http.MethodPost:
			writeJSONResponse(w, http.StatusForbidden, map[string]any{
				"error":  "Design grants write is unavailable in MCC local mode",
				"reason": "write_gate_disabled",
			})
		default:
			w.Header().Set("Allow", "GET, POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return true
```

**步骤 1.3** — `hardcoded_test.go` `TestIsHardcodedEndpoint` 表格（`hardcoded_test.go:50-53` 附近的精确匹配区）新增：

```go
		// CC 2.1.211 新增
		{"/v1/design/grants", true},
		{"/v1/ultrareview/preflight", true},
		{"/v1/code/triggers", true},
		{"/v1/code/triggers/t1", true},
```

**步骤 1.4** — `hardcoded_test.go` `TestHandleHardcodedEndpoint` 表格（`hardcoded_test.go:487-520`）新增 4 行：

```go
		{"/v1/design/grants"},
		{"/v1/ultrareview/preflight"},
		{"/v1/code/triggers"},
		{"/v1/code/triggers/t1"},
```

**步骤 1.5** — `hardcoded_test.go` 末尾新增测试函数 `TestHardcodedDesignGrants`：

```go
func TestHardcodedDesignGrants(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	t.Run("GET returns empty grants", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/design/grants", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle design grants")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Grants []any `json:"grants"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Grants) != 0 {
			t.Errorf("grants = %v, want empty", resp.Grants)
		}
	})

	t.Run("POST returns 403 write_gate_disabled", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/design/grants", strings.NewReader(`{"project_id":"p1"}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		var resp struct {
			Reason string `json:"reason"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Reason != "write_gate_disabled" {
			t.Errorf("reason = %q, want write_gate_disabled", resp.Reason)
		}
	})

	t.Run("PUT returns 405 with Allow", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/v1/design/grants", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, POST" {
			t.Errorf("Allow = %q, want GET, POST", got)
		}
	})
}
```

#### 验证

- [x] `GET /v1/design/grants` 返回 `200 {"grants":[]}`。
- [x] `POST /v1/design/grants` 返回 `403`，body 含 `"reason":"write_gate_disabled"`。
- [x] `PUT /v1/design/grants` 返回 `405` + `Allow: GET, POST`。

### 任务 2：ultrareview preflight 并入 quota 组

#### 需求

**Objective（目标）** — `/v1/ultrareview/preflight` 返回 `200 {}`，与同族 quota 一致。

**Outcomes（成果）** — `isHardcodedEndpoint` 精确匹配新增；`handleHardcodedEndpoint` 低优先级 case 新增路径走 `handleEmptyResponse`；`TestHardcodedLowRiskClaudeCodeEndpoints` 新增子测试。

**Evidence（证据）** — 子测试 `ultrareview preflight returns empty object` 通过。

**Constraints（约束）** — 与 quota 处置完全一致（`handleEmptyResponse` 返回 `200 {}`）。

**Edge Cases（边界）** — 非 GET 方法——`handleEmptyResponse` 不校验方法，preflight 客户端只用 GET。

**Verification（验证）** — `go test ./internal/proxy/ -run TestHardcodedLowRiskClaudeCodeEndpoints -v` 通过。

#### 计划

**步骤 2.1** — `isHardcodedEndpoint` exactMatches（`hardcoded.go:41`）在 `"/v1/ultrareview/quota"` 后新增：

```go
		"/v1/ultrareview/quota",
		"/v1/ultrareview/preflight", // CC 2.1.211：与 quota 同走 handleEmptyResponse
```

**步骤 2.2** — `handleHardcodedEndpoint` 低优先级 case（`hardcoded.go:312-327`）在 `path == "/v1/ultrareview/quota"`（`hardcoded.go:319`）后新增一行：

```go
		path == "/v1/ultrareview/quota",
		path == "/v1/ultrareview/preflight",
```

**步骤 2.3** — `TestIsHardcodedEndpoint` 与 `TestHandleHardcodedEndpoint` 表格新增条目（步骤 1.3、1.4 已包含 preflight）。

**步骤 2.4** — `TestHardcodedLowRiskClaudeCodeEndpoints`（`hardcoded_test.go:959`）在 onboarding 子测试之后、count_tokens 子测试之前新增：

```go
	t.Run("ultrareview preflight returns empty object", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/ultrareview/preflight", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle ultrareview preflight")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if strings.TrimSpace(rec.Body.String()) != "{}" {
			t.Errorf("body = %q, want {}", rec.Body.String())
		}
	})
```

#### 验证

- [x] `GET /v1/ultrareview/preflight` 返回 `200 {}`。
- [x] `GET /v1/ultrareview/quota` 行为不变（回归）。

### 任务 3：triggers 前缀 + 单独 handler

#### 需求

**Objective（目标）** — `/v1/code/triggers`（含子路径）返回空列表，避免客户端抛 `Error("triggers unavailable")`。

**Outcomes（成果）** — `isHardcodedEndpoint` prefixMatches 新增 `/v1/code/triggers`；新增 `internal/proxy/triggers.go` 与 `triggers_test.go`；`handleHardcodedEndpoint` switch 新增前缀 case。

**Evidence（证据）** — `go test ./internal/proxy/ -run TestHardcodedTriggers -v` 全绿（GET list/子路径、POST、DELETE）。

**Constraints（约束）** — 前缀匹配覆盖 list 与子路径；POST 403 含 `write_gate_disabled`；handler 有文档注释。

**Edge Cases（边界）** — get 单个返回列表形态（可接受）；深层子路径 `/triggers/t1/run`。

**Verification（验证）** — `go test ./internal/proxy/ -run TestHardcodedTriggers -v` 通过。

#### 计划

**步骤 3.1** — `isHardcodedEndpoint` prefixMatches（`hardcoded.go:73-85`）在 `"/v1/code/sessions/"`（`hardcoded.go:82`）后新增：

```go
		"/v1/code/sessions/",
		"/v1/code/triggers", // CC 2.1.211：CCR 触发器，本地空响应
```

**步骤 3.2** — 新建 `internal/proxy/triggers.go`：

```go
package proxy

import "net/http"

// handleTriggersEndpoint 处理 CCR 触发器端点，全部本地响应，不转发上游。
//
// 路由契约：
//   - GET /v1/code/triggers 或 /v1/code/triggers/{id}[/run] -> 200 {"data":[]}
//   - POST（create）-> 403 write_gate_disabled
//   - 其它方法 -> 405
//
// 请求体已在 handleHardcodedEndpoint 中 drain。
// 第三方 provider 场景不使用 CCR 触发器，目标仅是避免客户端抛
// Error("triggers unavailable")，不实现真实触发器语义。
func (h *Handler) handleTriggersEndpoint(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSONResponse(w, http.StatusOK, map[string]any{"data": []any{}})
	case http.MethodPost:
		writeJSONResponse(w, http.StatusForbidden, map[string]any{
			"error":  "Triggers write is unavailable in MCC local mode",
			"reason": "write_gate_disabled",
		})
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
```

**步骤 3.3** — `handleHardcodedEndpoint` switch 在 `/api/ws/` case（`hardcoded.go:307-309`）之前插入（紧跟 frame 区块之后）：

```go
	// Frame artifact 兼容 - 列表/track/deploy/contract/slug 全部本地处理
	case strings.HasPrefix(path, "/api/frame/"):
		h.handleFrameEndpoint(w, r)
		return true

	// CCR 触发器 - 前缀覆盖 list 与子路径，本地空响应（CC 2.1.211）
	case strings.HasPrefix(path, "/v1/code/triggers"):
		h.handleTriggersEndpoint(w, r)
		return true

	// Claude Design consent - GET/POST/DELETE 本地状态
	case path == "/v1/design/consent":
```

**步骤 3.4** — `TestIsHardcodedEndpoint` 与 `TestHandleHardcodedEndpoint` 表格新增条目（步骤 1.3、1.4 已包含 triggers）。

**步骤 3.5** — 新建 `internal/proxy/triggers_test.go`：

```go
package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"magic-claude-code/internal/config"
)

func TestHardcodedTriggers(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	for _, path := range []string{
		"/v1/code/triggers",
		"/v1/code/triggers/t1",
		"/v1/code/triggers/t1/run",
	} {
		t.Run("GET "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			if !handler.handleHardcodedEndpoint(rec, req) {
				t.Fatalf("should handle %s", path)
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var resp struct {
				Data []any `json:"data"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(resp.Data) != 0 {
				t.Errorf("data = %v, want empty", resp.Data)
			}
		})
	}

	t.Run("POST returns 403 write_gate_disabled", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/code/triggers", strings.NewReader(`{"name":"t"}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		var resp struct {
			Reason string `json:"reason"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Reason != "write_gate_disabled" {
			t.Errorf("reason = %q, want write_gate_disabled", resp.Reason)
		}
	})

	t.Run("DELETE returns 405 with Allow", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/code/triggers/t1", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, POST" {
			t.Errorf("Allow = %q, want GET, POST", got)
		}
	})
}
```

#### 验证

- [x] `GET /v1/code/triggers` 返回 `200 {"data":[]}`。
- [x] `GET /v1/code/triggers/t1`（子路径）返回 `200 {"data":[]}`。
- [x] `POST /v1/code/triggers` 返回 `403`，`reason=="write_gate_disabled"`。
- [x] `DELETE /v1/code/triggers/t1` 返回 `405` + `Allow: GET, POST`。

### 任务 4：基线文档逐条更新 52→55

#### 需求

**Objective（目标）** — `2026-07-15-intercepted-endpoints.md` 同步反映 3 个新端点，端点总数 52→55。

**Outcomes（成果）** — 数量总览、精确匹配清单、前缀清单、版本标识、附录校验脚本五处同步更新。

**Evidence（证据）** — 附录校验脚本输出：精确 39、前缀 12、合计 55。

**Constraints（约束）** — 仅更新数字与清单，不改"兜底规则""日志安全红线"等无关段落。

**Edge Cases（边界）** — 无；纯文档变更。

**Verification（验证）** — 附录脚本输出与文档数字一致。

#### 计划

文档路径 `sdd-docs/research/2026-07-15-intercepted-endpoints.md`。按以下逐条修改（行号基于当前文档）：

**步骤 4.1** — 数量总览表（`第 9-14 行`）：

| 字段 | 旧 | 新 |
|------|-----|-----|
| 本地硬编码拦截 | 50 | **53** |
| 精确匹配端点 | 37 | **39** |
| 前缀匹配端点 | 11 | **12** |
| 合计顶层端点 | 52 | **55** |

> 模式匹配端点（2）不变；模型推理转发（2）不变。

**步骤 4.2** — 精确匹配清单新增 2 行（A3 策略区与 A6 Design 区）：

- A3「策略 / 限制 / 合规」表格（`第 83-88 行`）在 `/v1/ultrareview/quota` 行后新增：

```
| 20b | `GET` | `/v1/ultrareview/preflight` | ultrareview 预检（与 quota 同走 200 {}） |
```

- A6「Claude Design」表格（`第 112-116 行`）在 `/v1/design/mcp` 行后新增：

```
| 33b | `GET`/`POST` | `/v1/design/grants` | GET 空授权禁用 Design；POST 403 write_gate_disabled |
```

（编号用 `b` 后缀避免重排既有 1–37；或实现时统一重排，二选一即可。）

**步骤 4.3** — B 前缀匹配清单（`第 131-145 行`）在 `/v1/code/sessions/*`（第 9 行）后新增：

```
| 12 | `GET`/`POST` | `/v1/code/triggers*` | CCR 触发器：GET `{data:[]}`、POST 403 write_gate_disabled |
```

**步骤 4.4** — API 版本标识表（`第 204 行` v1 行）在 `/v1/ultrareview/quota` 后补 `/v1/ultrareview/preflight`、在 `/v1/design/mcp` 后补 `/v1/design/grants`、在 `/v1/code/sessions/*` 后补 `/v1/code/triggers*`。

**步骤 4.5** — 附录校验脚本注释（`第 213-230 行`）：

```
# 精确匹配端点数 → 39   （原 37）
# 前缀匹配端点数 → 12   （原 11）
# 合计：39 + 12 + 2（本地拦截）+ 2（模型转发）= 55
```

#### 验证

- [x] 附录脚本实际输出精确 39、前缀 12（`awk ... | grep -cE` 验证）。
- [x] 数量总览表"合计顶层端点"= 55。
- [x] B 前缀清单含 `/v1/code/triggers*`，精确清单含 grants 与 preflight。

---

## 实现后回写

实现完成后，回写本规格头部"进度：0 / 4 → 4 / 4"，各任务"状态：已规划 → 已完成"，并在任务 1-3 的 `#### 验证` 复选框打勾，任务 4 贴入实际脚本输出。

---

## 实现记录（2026-07-15）

4 个任务全部完成，`go test ./...` 全包通过（17 包）。

- 任务 1-3 单测全绿：`TestHardcodedDesignGrants`、`TestHardcodedLowRiskClaudeCodeEndpoints`（preflight 子测试）、`TestHardcodedTriggers`。
- 任务 4 附录脚本实测：精确匹配 `39`、前缀匹配 `12`、模型转发 `2`、模式匹配 `2` → 合计 `55`。

实现期自审查证源码（`claude_code_src_2.1.211.js`）确认的关键点：

- POST `/v1/design/grants` 用 403 `write_gate_disabled`（与 404 功能等价，择 403 求一致性）。源码验证：客户端 `W8u` 对 403（`if(!r.ok) throw sn`，catch re-throw）与 404（`if(t===404) throw sn`）均抛 `sn`（grant 失败异常，extends Error）；调用者 `if(!(m instanceof yco)) throw m` 只放行 `yco`（consent required 异常，与 `sn` 同为 Error 子类、无继承），故 `sn` 被 re-throw 传播至工具框架顶层，Design 授权最终失败。保持 403 因与 `frame.go` 写入门一致、语义为"写关闭"；返回 200 会伪造授权成功（错）。
- `/v1/ultrareview/quota` 在 2.1.211 出现 0 次，已被 `preflight` 取代；本地拦截 quota 保留以兼容 ≤2.1.206 旧版本客户端（旧版本仍可能请求）。
- triggers 属 CCR 功能（`auth:"teleport-org"`，`anthropic-beta: ccr-triggers-2026-01-30`），第三方 provider 场景基本不触发；GET list 返回 `{data:[]}` 避免客户端 `Was` throw "triggers unavailable" 中断加载流程。
