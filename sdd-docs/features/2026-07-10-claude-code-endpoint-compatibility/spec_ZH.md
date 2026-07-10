# Claude Code 端点兼容规格

本地页面：无  
代理入口：`internal/proxy/handler.go`、`internal/proxy/hardcoded.go`  
参考来源：Claude Code `2.1.196` 与 `2.1.206` 提取 JS、Docker `mcc` 日志、本地 Claude 配置/插件/skills  
技术栈：Go 1.26 标准库代理、现有 MCC provider/config 包  
最后更新：2026-07-10  
进度：0 / 7 已规划

## 整体分析（源站分析）

### 当前项目状态

MCC 当前为 `api.anthropic.com` 终结 TLS，对一组固定的 Claude Code 客户端端点本地返回响应，其余请求全部转发到配置的上游模型供应商。这个策略在本地硬编码端点列表跟随 Claude Code `2.1.88` 时尚可接受，但面对较新的客户端版本已经过于宽松。

当前关键流程是：

```text
internal/proxy/handler.go ServeHTTP
  1. GET / -> OK
  2. handleHardcodedEndpoint(w, r) -> 如果匹配则本地响应
  3. 加载配置
  4. 读取请求体
  5. 解析 provider/model
  6. 转换请求
  7. 构造上游 URL
  8. 转发给供应商
```

风险在第 3 步之后：未识别的非模型端点，例如 `/v1/logs`、`/api/frame/contract/latest`、`/api/ws/speech_to_text/voice_stream`，甚至 `/favicon.ico`，都会被发送给 GLM、MiniMax、DeepSeek、Kimi、Qwen 或其他配置的模型供应商。这些供应商并不实现 Claude Code 控制面、遥测、artifact、插件搜索或 design 服务端点。继续转发会浪费请求、产生噪声错误，也可能把客户端元数据泄露给模型供应商。

新架构必须默认关闭：

```text
已知本地端点       -> MCC 本地响应
已知模型端点       -> 转发到配置的模型供应商
未知非模型端点     -> 本地拦截，并只记录 method/path/query 是否存在等安全日志
```

### 本地 Docker 日志证据

`docker logs --since 24h mcc` 暂未看到新的 `2.1.206` 端点被调用，但日志中已有：

```text
3 x GET /api/claude_cli/bootstrap     本地处理
4 x GET localhost/favicon.ico         转发到上游
```

这说明两点：

1. 新端点可能不会在普通启动流程中触发，日志没有出现不代表转发是安全的。
2. 当前默认转发行为已经能从无害的浏览器探测路径 `/favicon.ico` 观察到，证明未知路径确实会逃逸到模型供应商。

### Claude Code 2.1.196 与 2.1.206 端点差异

提取源码路径：

```text
/home/www/workspace/open-software/claude_code/073_claude_spy/claude_code_src.js
/home/www/workspace/open-software/claude_code/073_claude_spy/claude_code_src_2.1.206.js
```

`2.1.206` 相比 `2.1.196` 新增的双引号端点字面量：

| 端点 | 分类 | 当前 MCC 状态 | 必须处理方式 |
| --- | --- | --- | --- |
| `/api/frame/contract/latest` | Frame artifact contract | 会转发 | 本地不可用响应 |
| `/api/frame/frames?limit=200` | Frame artifact 列表 | 会转发 | 本地空列表 |
| `/api/oauth/organizations/:orgUUID/mcp/connectors/list` | MCP connector 发现 | 被宽泛前缀匹配，但 `{}` 响应偏弱 | 本地 `{"results":[]}` |
| `/api/oauth/organizations/:orgUUID/mcp/connectors/search` | MCP connector 搜索 | 被宽泛前缀匹配，但 `{}` 响应偏弱 | 本地 `{"results":[]}` |
| `/api/oauth/organizations/:orgUUID/mcp/connectors/suggest` | MCP connector 建议 | 被宽泛前缀匹配，但 `{}` 响应偏弱 | 本地 `{"results":[]}` |
| `/api/oauth/organizations/:orgUUID/plugins/search` | 插件搜索 | 被宽泛前缀匹配，但 `{}` 响应偏弱 | 本地配置搜索结果，失败时空数组 |
| `/api/oauth/organizations/:orgUUID/skills/search` | Skill 搜索 | 被宽泛前缀匹配，但 `{}` 响应偏弱 | 本地配置搜索结果，失败时空数组 |
| `/v1/design/consent` | Claude Design consent | 会转发 | 本地 consent 状态 |
| `/v1/design/mcp` | Claude Design MCP bridge | 会转发 | 本地不支持响应 |

`2.1.206` 中还存在且当前未被硬编码处理的端点包括：

```text
/api/claude_code/discovery/team_usage
/api/claude_code/notification/preferences
/api/claude_code/skills
/api/frame/deploy/complete
/api/frame/deploy/direct
/api/frame/deploy/init
/api/frame/track
/api/organizations/:orgUUID/claude_code/onboarding
/api/ws/speech_to_text/voice_stream
/v1/agents?beta=true
/v1/code/
/v1/code/agent-proxy
/v1/code/github/import-token
/v1/code/sessions
/v1/code/triggers
/v1/complete
/v1/design/
/v1/environments?beta=true
/v1/files?beta=true
/v1/filestore/fs/readFile
/v1/logs
/v1/mcp/{server_id}
/v1/memory_stores?beta=true
/v1/messages/batches
/v1/metrics
/v1/models
/v1/oauth/token
/v1/organizations/spend_limits
/v1/sessions
/v1/skills?beta=true
/v1/token
/v1/toolbox/shttp/mcp/{server_id}
/v1/traces
/v1/ultrareview/preflight
/v1/user_profiles?beta=true
/v1/vaults?beta=true
```

只有 `POST /v1/messages` 和 `POST /anthropic/v1/messages` 是应该继续转发的模型推理端点。`POST /v1/messages/count_tokens` 已经本地处理，必须保持本地。`/v1/models` 应改为使用 MCC 配置做本地模型发现，不应转发到上游。

### 客户端解析与兼容性说明

`2.1.206` 客户端源码显示以下响应预期：

| 客户端区域 | 请求 | 客户端行为 | MCC 兼容响应 |
| --- | --- | --- | --- |
| MCP connectors | `POST /api/oauth/organizations/:orgUUID/mcp/connectors/search`、`suggest`、`list` | 解析 `{results: array, opt_in_required?: bool, message?: string}`；非 2xx 或 schema 不匹配会抛错 | `200 {"results":[]}` |
| 插件搜索 | `POST /api/oauth/organizations/:orgUUID/plugins/search` | 解析 `{results: array}`，item 通常包含 `id`、`name`、`description`、`enabled` | 从本地 marketplace cache 返回 `200 {"results":[...]}`，失败时 `[]` |
| Skill 搜索 | `POST /api/oauth/organizations/:orgUUID/skills/search` | 与插件搜索使用同类 parser | 从本地 skills/plugin manifests 返回 `200 {"results":[...]}`，失败时 `[]` |
| 已安装 skill 健康 | `GET /api/claude_code/skills` | 非 2xx 时跳过；2xx 时读取 `data.skills`，每项需要 `skill_name` 与 `good|warn|poor` 健康值 | `200 {"skills":[...]}` 或 `{"skills":[]}` |
| Frame 列表 | `GET /api/frame/frames?limit=200` | 解析 `{frames: array|null}`；空数组可接受 | `200 {"frames":[]}` |
| Frame track | `POST /api/frame/track` | 期望 `204`；否则只记录日志 | `204` 空 body |
| Frame deploy complete | `POST /api/frame/deploy/complete` | 期望 `204`；否则只记录日志 | `204` 空 body |
| Frame deploy init/direct | `POST /api/frame/deploy/init`、`direct` | 发布路径能处理 403 reason，包括 `write_gate_disabled` | `403 {"error":"Frame publishing is unavailable in MCC local mode","reason":"write_gate_disabled"}` |
| Frame contract | `GET /api/frame/contract/latest` 及 contract 资源 | 成功时要求精确 schema/version；伪造数据有 parser 风险 | `404 {"error":"Frame contract service is unavailable in MCC local mode","reason":"local_unavailable"}` |
| Design consent | `GET /v1/design/consent` | 读取 `agent_design_projects` 等 boolean；非 200 当作空状态 | `200 {"agent_design_projects":false}` |
| Design consent 修改 | `POST`/`DELETE /v1/design/consent` | 期望成功状态 | `204` 空 body |
| Design MCP | `POST /v1/design/mcp` | JSON-RPC bridge；非 2xx 变为明确功能错误 | `403 {"error":{"type":"unsupported_local_endpoint","message":"Claude Design is unavailable in MCC local mode"}}` |
| OTLP 遥测 | `POST /v1/metrics`、`/v1/logs`、`/v1/traces`、`/api/event_logging/*` | 客户端不需要返回数据 | `/v1/*` 返回 `204`；`/api/event_logging/*` 保持现有 `{}` 也可 |
| 语音流 | `/api/ws/speech_to_text/voice_stream` | WebSocket/audio 路径，不是模型请求 | `501 {"error":{"type":"unsupported_local_endpoint","message":"Speech-to-text streaming is unavailable in MCC local mode"}}` |

### 本地配置、插件与 Skills

本地检查确认以下数据源有助于提升兼容响应：

```text
/home/www/.claude/settings.json
/home/www/.claude.json
/home/www/.claude/plugins/marketplaces/*/.claude-plugin/marketplace.json
/home/www/.claude/plugins/marketplaces/*/.claude-plugin/plugin.json
/home/www/.claude/skills/*/SKILL.md
```

`~/.claude/settings.json` 包含 `enabledPlugins`、`extraKnownMarketplaces`、`env` 等 key。`~/.claude.json` 包含 `additionalModelOptionsCache`、`pluginUsage`、`skillUsage` 等 cache。

实现必须把这些文件作为可选的尽力而为输入。很多用户通过 Docker 运行 MCC，容器内未必挂载宿主机 `~/.claude`。文件缺失或不可读时必须返回合法空响应，不能导致启动失败或接口 500。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已规划 | 端点策略与默认拦截 guard | `endpoint_policy.go`、handler guard、策略测试 | 未知端点被拦截；只有消息端点转发 |
| 2 | 已规划 | 遥测、探测、模型与低风险 Claude Code API 本地响应 | hardcoded handlers 与测试 | 本地 2xx/204 响应；不上游 |
| 3 | 已规划 | 插件、skill、MCP connector 兼容 | 本地 catalog loader/search handlers | Search schema 匹配客户端 parser |
| 4 | 已规划 | Frame artifact 兼容 | frame handlers 与测试 | 空列表、204 track、受控发布拒绝 |
| 5 | 已规划 | Design 与不支持的 streaming 兼容 | design/ws handlers 与测试 | Consent 可用；design MCP/语音流被本地拦截 |
| 6 | 已规划 | 日志与诊断 | 不含敏感信息的拦截日志 | 未知路径产生一条有用日志 |
| 7 | 已规划 | 回归验证与端点矩阵 | 测试、可选脚本/文档更新 | `go test ./...`；端点矩阵复核 |

## 需求

### 交付物

1. 在 provider 选择与请求转换之前增加默认拦截的端点策略。
2. 明确模型转发白名单：
   - `POST /v1/messages`
   - `POST /anthropic/v1/messages`
3. 保持现有 `POST /v1/messages/count_tokens` 本地行为。
4. 所有已知 Claude Code 控制面、遥测、Frame、Design、插件、skill、MCP connector、浏览器探测与语音流端点都本地处理。
5. 未知非模型端点本地拦截，返回稳定 JSON 错误，并打印安全日志。
6. `/v1/models` 使用 MCC 配置本地返回模型列表。
7. 插件与 skill 搜索从 `CLAUDE_CONFIG_DIR` 或 `~/.claude` 做本地尽力而为搜索；没有数据时返回空兼容响应。
8. 单元测试证明未知端点不会进入 provider 转发流程。
9. 实现完成后回写本规格的检查清单、进度和实际验证证据。

### 端点策略

代理必须只使用标准化后的 `r.URL.Path` 对请求分类。query string 不能决定路径是否可转发；本地 handler 匹配后可以按需读取 query 参数。

| 决策 | 匹配 | 动作 |
| --- | --- | --- |
| 根探测 | `GET /` | 保持现有 `OK\n` |
| 静态/浏览器探测 | `/favicon.ico`、`/robots.txt`、`/apple-touch-icon.png`、`/apple-touch-icon-precomposed.png` | 本地 `404` 空 body |
| 本地硬编码 | 本地端点注册表中的精确或前缀匹配 | 执行本地 handler |
| 可转发模型端点 | `POST /v1/messages`、`POST /anthropic/v1/messages` | 转发到配置的 provider |
| 已知但方法错误 | 与模型端点同路径但非 POST | 本地 `405` |
| 未知 | 其他任何路径 | 本地拦截，不调用上游 |

禁止转发 `GET /v1/models`、`POST /v1/messages/batches`、`/v1/complete`、`/v1/logs`、`/v1/traces` 或 `/api/*` catch-all，除非后续规格明确补充了具体本地 handler 或模型转发理由。

### 本地响应契约

| 端点模式 | 方法 | 状态 | Body |
| --- | --- | --- | --- |
| `/v1/messages/count_tokens` | `POST` | `200` | 保持现有 token 估算响应 |
| `/v1/models` | `GET` | `200` | `{"data":[{"id":"...","type":"model","display_name":"..."}],"has_more":false}` |
| `/v1/metrics`、`/v1/logs`、`/v1/traces` | `POST` | `204` | 空 |
| `/api/event_logging/batch`、`/api/event_logging/v2/batch` | 现有支持方法 | `200` | `{}` |
| `/api/claude_code/skills` | `GET` | `200` | `{"skills":[{"skill_name":"...","health":"good","source":"local"}]}` 或 `{"skills":[]}` |
| `/api/claude_code/discovery/team_usage` | `GET` | `200` | `{"teams":[],"usage":[],"data":[]}` |
| `/api/claude_code/notification/preferences` | `GET` | `200` | `{"preferences":{},"notifications_enabled":false}` |
| `/api/organizations/{orgUUID}/claude_code/onboarding` | `GET`、`POST`、`PUT`、`PATCH` | `200` | `{}` |
| `/api/oauth/organizations/{orgUUID}/mcp/connectors/list` | `POST` | `200` | `{"results":[]}` |
| `/api/oauth/organizations/{orgUUID}/mcp/connectors/search` | `POST` | `200` | `{"results":[]}` |
| `/api/oauth/organizations/{orgUUID}/mcp/connectors/suggest` | `POST` | `200` | `{"results":[]}` |
| `/api/oauth/organizations/{orgUUID}/plugins/search` | `POST` | `200` | `{"results":[{"id":"...","name":"...","description":"...","enabled":false}]}` |
| `/api/oauth/organizations/{orgUUID}/skills/search` | `POST` | `200` | 与插件搜索相同 shape |
| `/api/frame/frames` | `GET` | `200` | `{"frames":[]}` |
| `/api/frame/track` | `POST` | `204` | 空 |
| `/api/frame/deploy/complete` | `POST` | `204` | 空 |
| `/api/frame/deploy/init`、`/api/frame/deploy/direct` | `POST` | `403` | `{"error":"Frame publishing is unavailable in MCC local mode","reason":"write_gate_disabled"}` |
| `/api/frame/contract/*` | `GET` | `404` | `{"error":"Frame contract service is unavailable in MCC local mode","reason":"local_unavailable"}` |
| `/api/frame/{slug}` | `GET`、`DELETE` | `404` | `{"error":"Artifact not found in MCC local mode","reason":"not_found"}` |
| `/v1/design/consent` | `GET` | `200` | `{"agent_design_projects":false}` |
| `/v1/design/consent` | `POST`、`DELETE` | `204` | 空 |
| `/v1/design/mcp` | `POST` | `403` | `{"error":{"type":"unsupported_local_endpoint","message":"Claude Design is unavailable in MCC local mode"}}` |
| `/api/ws/*` | 任意 | `501` | `{"error":{"type":"unsupported_local_endpoint","message":"Streaming endpoint is unavailable in MCC local mode"}}` |
| 未知端点 | 任意 | `404` | `{"error":{"type":"mcc_blocked_unknown_endpoint","message":"MCC blocked an unrecognized non-model endpoint"},"path":"/..."}` |

所有本地 handler 返回前必须 drain 或关闭请求体；已经读取请求体做本地计算的 handler 例外。

### 本地 Catalog 规则

插件/skill 搜索必须在 `internal/proxy` 下实现小型内部 helper 或包，规则如下：

1. 配置目录解析：
   - 优先使用 `CLAUDE_CONFIG_DIR`。
   - 否则使用 `os.UserHomeDir()` + `.claude`。
   - 如果 home directory 不可用，则返回空 catalog。
2. 候选文件：
   - `plugins/marketplaces/*/.claude-plugin/marketplace.json`
   - `plugins/marketplaces/*/.claude-plugin/plugin.json`
   - `skills/*/SKILL.md`
   - 可选只读读取 `.claude.json` 与 `settings.json` 元数据。
3. 搜索请求解析：
   - 支持 `{"keywords":["foo","bar"]}`。
   - 兼容 `{"keywords":"foo bar"}`。
   - body 缺失或格式错误时返回未过滤第一页或空列表。
4. 匹配：
   - 对 `id`、`name`、`description`、`tags`、`keywords`、来源 marketplace 名称做大小写不敏感 substring 匹配。
   - 不引入模糊搜索依赖或外部库。
5. 结果限制：
   - 最多返回 50 条。
   - 稳定排序：enabled 优先，然后 lowercase name，然后 id。
6. enabled 判断：
   - 读取 `settings.json.enabledPlugins`。
   - map 中 result id 对应 boolean `true` 时返回 `enabled:true`，否则 false。
7. 失败行为：
   - JSON 格式错误、目录不可读、文件缺失都不能返回 HTTP 500。
   - 可以记录简短 debug/warn 日志，但 HTTP 响应仍然是兼容的空结果。

### 约束

1. 未知端点不得转发给配置的模型供应商。
2. 不得记录请求体、Authorization header、API key、Cookie 或 bearer token。
3. 本地兼容 handler 不得发起网络请求。
4. 不得要求 Docker 环境存在宿主机 `~/.claude`。
5. 保持 `/api/claude_cli/bootstrap`、`/api/feature/*`、`/v1/me`、`/api/oauth/profile` 等现有硬编码端点行为，除非测试证明必须增强响应。
6. 端点分类必须确定、易审计；避免用一个巨大正则隐藏意图。
7. 本功能不为 Frame、Design、遥测状态增加持久化。
8. 保持已转发 `/v1/messages` 请求的现有 usage accounting 行为。

### 实现审查重点

以下 4 点是 GLM-5.2 实现时的强制审查门槛：

1. `/v1/models` 必须在阅读当前代码后，从 MCC 现有 provider/model 配置结构派生。实现者不得凭空猜字段，也不得新增一套平行 model registry。
2. `/api/oauth/organizations/{orgUUID}/plugins/search`、`/skills/search`、`/mcp/connectors/*` 等具体组织端点必须先于现有宽泛 `/api/oauth/organizations/` fallback 匹配；否则客户端仍会拿到偏弱的 `{}` 响应。
3. 默认拦截 guard 必须放在 `ServeHTTP` 中 `handleHardcodedEndpoint(w, r)` 之后、加载配置/provider 解析/请求转换之前；否则未知端点仍可能进入转发路径。
4. 测试必须证明 blocked endpoint 没有命中 fake upstream provider。只测试返回状态码不足以验收本功能。

### 边界情况

1. 请求是 `GET /v1/messages` 而不是 `POST`：本地返回 `405`，不得转发。
2. 请求是 `POST /v1/messages?beta=true`：作为模型请求转发；现有 provider URL 逻辑可继续为非 Anthropic 格式移除 `beta=true`。
3. 请求是 `/v1/models?beta=true`：按 path `/v1/models` 本地处理。
4. 遥测 body 很大：不解析，drain/discard 后返回 `204`。
5. 插件搜索 body 格式错误：返回 `{"results":[]}` 或未过滤本地结果，不能 `500`。
6. Frame 路由包含 `/api/frame/{slug}`：除明确 Frame 控制路由外，返回本地 not found。
7. WebSocket upgrade 请求命中 `/api/ws/*`：返回 `501`，不要 hijack connection。
8. Docker 没有挂载 Claude config：插件/skill 搜索返回空数组。
9. Provider 配置为空：`/v1/models` 返回 `{"data":[],"has_more":false}`。
10. 未知路径是 `/anthropic/v1/anything-but-messages`：本地拦截。

### 非目标

1. 不实现真实 Frame artifact 托管或发布。
2. 不实现真实 Claude Design MCP 服务。
3. 不实现远程插件市场联邦搜索。
4. 不模拟 Anthropic 账号账单、组织 spend limits 或 OAuth token 签发。
5. 不把遥测转发到任何地方，包括用户配置的 provider。
6. 本功能不修改管理 UI。

## 任务详情

### 任务 1：端点策略与默认拦截 Guard

#### 需求

**Objective（目标）** — 在 provider 选择之前拒绝未知非模型端点。

**Outcomes（成果）** — `ServeHTTP` 只转发明确模型请求；其他未识别路径全部本地响应。

**Evidence（证据）** — 使用 `httptest.Server` 上游的测试证明 `GET /favicon.ico`、`POST /v1/logs`、`GET /v1/complete` 不会命中上游，而 `POST /v1/messages` 仍会进入转发路径。

**Constraints（约束）** — 改动保持小而易审计；保留现有 hardcoded endpoint 行为。

**Edge Cases（边界）** — `/v1/messages` 方法错误；模型路径带 query；未知 `/anthropic/v1/*`。

**Verification（验证）** — `go test ./internal/proxy -run 'TestEndpointPolicy|TestServeHTTPFailClosed'`。

#### 计划

1. 创建 `internal/proxy/endpoint_policy.go`。
2. 定义：
   ```go
   type endpointAction int

   const (
       endpointActionLocal endpointAction = iota
       endpointActionForwardModel
       endpointActionBlock
       endpointActionMethodNotAllowed
   )

   type endpointDecision struct {
       action endpointAction
       reason string
   }
   ```
3. 增加 `classifyForwardingEndpoint(method, path string) endpointDecision`，规则必须精确如下：
   - `POST /v1/messages` -> `endpointActionForwardModel`
   - `POST /anthropic/v1/messages` -> `endpointActionForwardModel`
   - 非 POST 的 `/v1/messages` 或 `/anthropic/v1/messages` -> `endpointActionMethodNotAllowed`
   - 其他全部 -> `endpointActionBlock`
4. 先在 `internal/proxy/endpoint_policy_test.go` 写失败测试：
   - `TestClassifyForwardingEndpointAllowsOnlyMessagePosts`
   - `TestClassifyForwardingEndpointRejectsWrongMethod`
   - `TestClassifyForwardingEndpointIgnoresQueryBecausePathIsNormalized`
5. 运行 `go test ./internal/proxy -run TestClassifyForwardingEndpoint`，确认实现前失败。
6. 实现 classifier。
7. 修改 `internal/proxy/handler.go`，位置必须在 `handleHardcodedEndpoint` 之后、加载配置之前：
   ```go
   decision := classifyForwardingEndpoint(r.Method, r.URL.Path)
   switch decision.action {
   case endpointActionForwardModel:
       // 继续现有转发流程
   case endpointActionMethodNotAllowed:
       h.handleBlockedEndpoint(w, r, http.StatusMethodNotAllowed, "method_not_allowed")
       return
   default:
       h.handleBlockedEndpoint(w, r, http.StatusNotFound, decision.reason)
       return
   }
   ```
8. 在 `internal/proxy/hardcoded.go` 或新建 `blocked.go` 中增加 `handleBlockedEndpoint`。它必须 drain body，设置 `Content-Type: application/json`，打印安全日志，并返回稳定 JSON 错误。
9. 增加 handler 集成测试：配置 fake provider，断言 blocked path 对应的原子计数器保持 0。
10. 运行 `go test ./internal/proxy -run 'TestClassifyForwardingEndpoint|TestServeHTTPFailClosed'`。
11. 完成后回写本规格检查清单与进度。

#### 验证

- [ ] Classifier 单元测试通过。
- [ ] blocked endpoint 集成测试证明不会调用上游。
- [ ] `POST /v1/messages` 在测试中仍进入现有转发路径。

### 任务 2：遥测、探测、模型与低风险本地响应

#### 需求

**Objective（目标）** — 扩展本地硬编码处理，覆盖绝不应到达模型供应商的非模型端点。

**Outcomes（成果）** — 遥测、浏览器探测、模型发现、team usage、notification preferences、onboarding 与现有 count-token 行为都本地完成。

**Evidence（证据）** — 测试断言本任务每个端点的 status/body，并验证不调用上游。

**Constraints（约束）** — 不发起外部网络请求。`/v1/models` 必须使用现有 MCC 配置，而不是 Anthropic API。

**Edge Cases（边界）** — 空 provider 配置；重复模型名；大体积遥测 body。

**Verification（验证）** — `go test ./internal/proxy -run 'TestHardcodedTelemetry|TestHardcodedModels|TestHardcodedLowRiskClaudeCode|TestStaticProbeEndpoints'`。

#### 计划

1. 先在 `internal/proxy/hardcoded_test.go` 增加失败测试：
   - `TestStaticProbeEndpointsAreLocal`
   - `TestHardcodedTelemetryOTLPEndpoints`
   - `TestHardcodedModelsUsesConfiguredProviders`
   - `TestHardcodedLowRiskClaudeCodeEndpoints`
2. 静态探测：
   - 增加精确本地匹配 `/favicon.ico`、`/robots.txt`、`/apple-touch-icon.png`、`/apple-touch-icon-precomposed.png`。
   - 响应：`404`，空 body。
3. 遥测：
   - 增加精确本地匹配 `/v1/metrics`、`/v1/logs`、`/v1/traces`。
   - `POST` 响应：`204`，空 body。
   - 非 POST 响应：`405`，JSON error。
4. 模型：
   - 增加精确本地匹配 `/v1/models`。
   - 实现 `handleModels(w, r)`，只允许 `GET`。
   - 通过 `h.configManager.Load()` 加载当前配置。
   - 使用 `handleBootstrap` 已经依赖的 provider/model 结构收集模型 id。
   - 编写该 handler 前必须先阅读现有 config/provider struct 与测试；不得猜测新字段名。
   - 按 `id` 去重。
   - 按 `id` 排序。
   - 响应：
     ```json
     {"data":[{"id":"model-id","type":"model","display_name":"model-id"}],"has_more":false}
     ```
   - 配置加载失败或没有模型时，返回 `{"data":[],"has_more":false}`。
5. 低风险 Claude Code API：
   - `/api/claude_code/discovery/team_usage` -> `200 {"teams":[],"usage":[],"data":[]}`
   - `/api/claude_code/notification/preferences` -> `200 {"preferences":{},"notifications_enabled":false}`
   - `/api/organizations/{orgUUID}/claude_code/onboarding` -> `200 {}`
6. 保留现有 `/v1/messages/count_tokens` 测试，并增加一条回归断言：加入默认拦截 guard 后它仍然本地处理。
7. 运行目标测试命令，确认实现前失败。
8. 实现 handler helper：
   - `writeJSON(w, status, value)`
   - `writeNoContent(w)`
   - `methodAllowed(w, r, allowed ...string) bool`
9. 运行目标测试，再运行 `go test ./internal/proxy`。
10. 回写本规格检查清单与进度。

#### 验证

- [ ] 遥测端点返回 `204`，且不解析 payload。
- [ ] `/v1/models` 返回配置派生数据或空列表。
- [ ] 浏览器探测路径不再转发上游。
- [ ] 现有 hardcoded endpoints 仍然通过测试。

### 任务 3：插件、Skill 与 MCP Connector 兼容

#### 需求

**Objective（目标）** — 为 Claude Code 插件、skill、MCP connector 发现端点返回 schema 兼容的本地响应。

**Outcomes（成果）** — Connector 端点返回空 result 数组；本地 Claude config 存在时，插件和 skill 搜索返回本地尽力而为结果。

**Evidence（证据）** — 临时目录测试创建假的 Claude config/plugin/skill 文件，验证搜索响应匹配客户端 parser schema。

**Constraints（约束）** — 只做尽力而为；缺失 config 不能失败；不请求远程 marketplace。

**Edge Cases（边界）** — JSON 格式错误；缺少 `enabledPlugins`；`keywords` 是 string 或 array；重复 plugin id。

**Verification（验证）** — `CLAUDE_CONFIG_DIR=$(mktemp -d) go test ./internal/proxy -run 'TestLocalCatalog|TestPluginSkillSearch|TestMCPConnectorEndpoints'`。

#### 计划

1. 创建 `internal/proxy/local_catalog.go` 与 `internal/proxy/local_catalog_test.go`。
2. 定义：
   ```go
   type localCatalogItem struct {
       ID          string `json:"id"`
       Name        string `json:"name"`
       Description string `json:"description,omitempty"`
       Enabled     bool   `json:"enabled"`
       Source      string `json:"source,omitempty"`
       Kind        string `json:"kind,omitempty"`
   }
   ```
3. 先写失败测试：
   - `TestLocalCatalogDirUsesEnvOverride`
   - `TestLocalCatalogLoadsMarketplacePluginJSON`
   - `TestLocalCatalogLoadsSkillsDirectory`
   - `TestSearchLocalCatalogHandlesArrayAndStringKeywords`
   - `TestPluginSkillSearchReturnsEmptyOnMalformedConfig`
4. 实现配置目录解析：
   - 优先 `CLAUDE_CONFIG_DIR`。
   - fallback 到 `os.UserHomeDir()+"/.claude"`。
   - 都不可用则返回空字符串。
5. 实现 `loadLocalCatalog(kind string) []localCatalogItem`：
   - plugin 扫描 `plugins/marketplaces/*/.claude-plugin/plugin.json` 与 `marketplace.json`。
   - skill 扫描 `skills/*/SKILL.md` 和暴露 skills 的 plugin manifests。
   - 使用宽松 JSON struct，忽略未知字段。
   - 使用目录 basename 作为 fallback id/name。
6. 实现 `readEnabledPlugins(configDir string) map[string]bool`。
7. 实现 `parseSearchKeywords(r *http.Request) []string`：
   - Body `{"keywords":["a","b"]}` -> `[]string{"a","b"}`
   - Body `{"keywords":"a b"}` -> `[]string{"a","b"}`
   - body 缺失或格式错误 -> 空 keywords
8. 实现 `filterCatalog(items, keywords)`：
   - 无 keywords 时返回排序后的前 50 条。
   - 所有 keywords 都应在可搜索文本中命中。
   - 排序：enabled 优先、lowercase name、id。
9. 在现有宽泛 `/api/oauth/organizations/` fallback 之前增加更具体的本地端点匹配：
   - `/api/oauth/organizations/{orgUUID}/mcp/connectors/list`
   - `/api/oauth/organizations/{orgUUID}/mcp/connectors/search`
   - `/api/oauth/organizations/{orgUUID}/mcp/connectors/suggest`
   - `/api/oauth/organizations/{orgUUID}/plugins/search`
   - `/api/oauth/organizations/{orgUUID}/skills/search`
   - 增加一条回归断言，证明这些具体 handler 会先于宽泛 organization fallback 命中。
10. Connector handlers 返回 `200 {"results":[]}`。
11. Plugin/skill handlers 返回 `200 {"results":[...]}`。
12. 增加 `GET /api/claude_code/skills` handler：
   - 本地无 skills 时返回 `{"skills":[]}`。
   - 有本地 skills 时返回 `{"skills":[{"skill_name":"name","health":"good","source":"local"}]}`。
13. 运行目标测试，再运行 `go test ./internal/proxy`。
14. 回写本规格检查清单与进度。

#### 验证

- [ ] 搜索端点返回客户端兼容的 `results`。
- [ ] 缺失 `~/.claude` 返回空结果，不返回 `500`。
- [ ] 现有宽泛 `/api/oauth/organizations/` fallback 不会遮蔽新增具体 handler。

### 任务 4：Frame Artifact 兼容

#### 需求

**Objective（目标）** — 防止 Frame artifact 端点到达模型供应商，同时保持 Claude Code 客户端行为受控。

**Outcomes（成果）** — 列表与 track 是无害 no-op；发布返回客户端可识别的 write-gate denial；contract 与 artifact slug 返回本地 not-found/unavailable。

**Evidence（证据）** — 测试覆盖每条 Frame 路由的 status/body 契约。

**Constraints（约束）** — 不实现 artifact 持久化或远程发布。不要伪造 contract 数据，因为客户端会校验 contract version。

**Edge Cases（边界）** — `/api/frame/frames?limit=200`；未知 slug；方法不匹配；contract 子路径。

**Verification（验证）** — `go test ./internal/proxy -run TestFrameEndpointCompatibility`。

#### 计划

1. 在 `internal/proxy/hardcoded_frame_test.go` 或 `hardcoded_test.go` 先写失败测试：
   - `TestFrameFramesReturnsEmptyList`
   - `TestFrameTrackAndDeployCompleteReturnNoContent`
   - `TestFrameDeployInitDirectReturnWriteGateDenied`
   - `TestFrameContractReturnsUnavailable`
   - `TestFrameSlugReturnsNotFound`
2. 增加 `/api/frame/` 前缀本地匹配。
3. 实现 `handleFrameEndpoint(w, r)`，路由顺序必须是：
   - `GET /api/frame/frames` -> `200 {"frames":[]}`
   - `POST /api/frame/track` -> `204`
   - `POST /api/frame/deploy/complete` -> `204`
   - `POST /api/frame/deploy/init` -> `403 {"error":"Frame publishing is unavailable in MCC local mode","reason":"write_gate_disabled"}`
   - `POST /api/frame/deploy/direct` -> 同样 403
   - `GET /api/frame/contract/*` -> `404 {"error":"Frame contract service is unavailable in MCC local mode","reason":"local_unavailable"}`
   - `GET /api/frame/{slug}` -> `404 {"error":"Artifact not found in MCC local mode","reason":"not_found"}`
   - `DELETE /api/frame/{slug}` -> 同样 404
   - 未匹配方法 -> `405`
4. 匹配时忽略 query string。`GET /api/frame/frames?limit=200` 必须匹配 `/api/frame/frames`。
5. POST 路由必须 drain request body。
6. 运行目标测试与 `go test ./internal/proxy`。
7. 回写本规格检查清单与进度。

#### 验证

- [ ] Frame list 是空数组。
- [ ] Tracking 与 deploy completion 是 no-op `204`。
- [ ] 发布尝试返回客户端可识别的 `reason:"write_gate_disabled"`。
- [ ] 所有 Frame 路由均不上游。

### 任务 5：Design 与不支持的 Streaming 兼容

#### 需求

**Objective（目标）** — 本地处理 Claude Design 与语音流端点，返回可预测的不支持行为。

**Outcomes（成果）** — Consent 读取/修改在本地可成功；Design MCP 与语音流用明确 unsupported error 拦截。

**Evidence（证据）** — 测试验证 `GET`、`POST`、`DELETE /v1/design/consent`、`POST /v1/design/mcp` 与 `/api/ws/*` 的 status/body。

**Constraints（约束）** — 不实现 JSON-RPC MCP bridge 或 WebSocket streaming；不发起外部请求。

**Edge Cases（边界）** — `/v1/design/mcp` 上的 JSON-RPC body；WebSocket upgrade headers；DELETE consent。

**Verification（验证）** — `go test ./internal/proxy -run 'TestDesignEndpointCompatibility|TestUnsupportedStreamingEndpoints'`。

#### 计划

1. 先写失败测试：
   - `TestDesignConsentCompatibility`
   - `TestDesignMCPReturnsUnsupported`
   - `TestUnsupportedStreamingEndpoints`
2. 增加精确本地匹配：
   - `/v1/design/consent`
   - `/v1/design/mcp`
3. 增加前缀本地匹配：
   - `/api/ws/`
4. 实现 `handleDesignConsent`：
   - `GET` -> `200 {"agent_design_projects":false}`
   - `POST` -> `204`
   - `DELETE` -> `204`
   - 其他方法 -> `405`
5. 实现 `handleDesignMCP`：
   - `POST` -> `403 {"error":{"type":"unsupported_local_endpoint","message":"Claude Design is unavailable in MCC local mode"}}`
   - 其他方法 -> `405`
6. 实现 `handleUnsupportedStreamingEndpoint`：
   - 任意方法 -> `501 {"error":{"type":"unsupported_local_endpoint","message":"Streaming endpoint is unavailable in MCC local mode"}}`
   - 不 upgrade、不 hijack connection。
7. 返回前 drain request body。
8. 运行目标测试与 `go test ./internal/proxy`。
9. 回写本规格检查清单与进度。

#### 验证

- [ ] Design consent 不再转发。
- [ ] Design MCP 返回受控 unsupported error。
- [ ] WebSocket/audio 路径本地拦截。

### 任务 6：日志与诊断

#### 需求

**Objective（目标）** — 让被拦截的端点在日志中可见，同时不泄露敏感数据。

**Outcomes（成果）** — 已知本地端点保留现有 hardcoded 日志；未知拦截端点产生一条结构化日志，包含 method、host、path、query 是否存在、user agent、status、reason。

**Evidence（证据）** — 测试捕获 logger 输出或注入 logger hook，验证 blocked endpoint 日志不包含 body/auth 内容。

**Constraints（约束）** — 不记录 request body 或敏感 headers。日志量有界：每个 blocked request 一行。

**Edge Cases（边界）** — query string 含 token-like 值；body 含 API key；存在 Authorization header。

**Verification（验证）** — `go test ./internal/proxy -run TestBlockedEndpointLogging`。

#### 计划

1. 先写失败测试 `TestBlockedEndpointLogging`：
   - 发送带 `Authorization: Bearer secret`、`Cookie: a=b`、body `api_key=secret`、query `token=secret` 的 blocked endpoint。
   - 断言日志包含 method/path/status/reason。
   - 断言日志不包含 body、authorization、cookie header 值。
2. 在 `handleBlockedEndpoint` 中实现日志：
   ```text
   [Hardcoded] Blocking unknown endpoint method=GET host=api.anthropic.com path=/v1/complete query_present=true status=404 reason=unknown_non_model_endpoint ua="..."
   ```
3. 只记录 query 是否存在，不记录原始 query string，除非项目现有日志规范已经记录 query。这里优先使用 `query_present=true`。
4. 已知本地 handler 保持现有日志：
   ```text
   [Hardcoded] Handling METHOD /path
   ```
5. 运行目标测试与 `go test ./internal/proxy`。
6. 回写本规格检查清单与进度。

#### 验证

- [ ] 拦截日志能定位 endpoint 与 reason。
- [ ] 日志不包含 request body、authorization、cookies 或原始 query token。
- [ ] 单个请求不会重复打印拦截日志。

### 任务 7：回归验证与端点矩阵

#### 需求

**Objective（目标）** — 证明功能对现有代理行为安全，并为未来 Claude Code 更新记录端点矩阵方法。

**Outcomes（成果）** — Go 全量测试通过；可选端点提取脚本或命令被记录；规格中记录实际验证证据。

**Evidence（证据）** — `go test ./...` 通过。如果未触碰前端，前端测试/构建可跳过并注明原因；如果触碰 admin/frontend 文件，必须运行前端验证。

**Constraints（约束）** — 除非前端源码改变，否则不要修改生成的 frontend `dist`。除非用户要求，否则不要 commit。

**Edge Cases（边界）** — worktree 中有与本功能无关的 dirty change；Docker daemon 不可用；本地 Claude config 缺失。

**Verification（验证）** — `go test ./...`。

#### 计划

1. 运行：
   ```bash
   go test ./internal/proxy
   go test ./...
   ```
2. 如果任何 frontend/admin UI 源码被修改，还要运行：
   ```bash
   npm --prefix internal/frontend test
   npm --prefix internal/frontend run build
   ```
3. 在本规格中保留未来更新时的端点提取命令：
   ```bash
   rg -o '"/(api|v1|mcp-registry|anthropic)[^"]*"' /path/to/claude_code_src_2.1.206.js | sort -u
   ```
4. 检查 `git diff --stat`，确认改动范围限制在：
   - `internal/proxy/*`
   - 本功能规格的进度更新
   - `internal/proxy` 下可选 tests/helpers
5. 如果 Docker runtime 可用，在一次正常 Claude Code 启动后手动查看日志：
   ```bash
   docker logs --since 10m mcc | rg 'Hardcoded|Blocking unknown endpoint|Forwarding request'
   ```
6. 把实际命令输出记录到本规格的任务验证 checkbox。
7. 只有测试通过且 blocked-endpoint 行为被观察到或由单元测试证明后，才把功能状态标记为 `validated`。

#### 验证

- [ ] `go test ./internal/proxy` 通过。
- [ ] `go test ./...` 通过。
- [ ] 本规格中的端点矩阵与实现 handler 一致。
- [ ] 测试证明未知非模型端点无法进入 provider 转发。
