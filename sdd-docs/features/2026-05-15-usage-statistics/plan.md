# 使用统计实施计划

> **给自动化执行代理：** 必须使用子技能：使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans` 按任务逐项实施本计划。步骤使用复选框（`- [ ]`）语法追踪进度。

**目标：** 为 Claude Code 代理请求构建使用统计，包括 provider 返回的真实 token 使用量、Claude Session Log 独立补账、默认有效统计去重口径、请求质量指标、usage 覆盖率、管理 API、状态摘要，以及 Vue/ECharts 统计页面。

**架构：** 新增聚焦的 `internal/usage` 包，负责 schema 迁移、记录、解析、聚合、脱敏、Session Log 同步和 HTTP 处理器。将 proxy 处理器接入为每个 provider 请求创建一条 usage 记录，同时不改变请求/响应转发行为；Session Log 作为独立来源导入，不覆盖 provider 记录；聚合查询默认使用有效统计口径排除重复 Session Log。随后在现有管理服务中暴露只读 usage API，并在仪表盘中渲染。

**技术栈：** Go `database/sql`、现有 `modernc.org/sqlite`、Go `net/http` 测试、Vue 3、TypeScript、Tailwind、ECharts。

**规格：** [2026-05-15-usage-statistics-design.md](../specs/2026-05-15-usage-statistics-design.md)

---

## 文件映射

| 文件 | 动作 | 责任 |
|------|------|------|
| `internal/usage/store.go` | 新建 | Schema 迁移、存储层写入、聚合查询 |
| `internal/usage/types.go` | 新建 | 请求、token、过滤器、结果结构体和枚举 |
| `internal/usage/parse.go` | 新建 | 请求元数据解析、来源入口解析、usage 提取 |
| `internal/usage/sse.go` | 新建 | SSE usage 观察器和 usage 字段合并逻辑 |
| `internal/usage/redact.go` | 新建 | URL、User-Agent、错误、解析错误的截断和敏感值脱敏 |
| `internal/usage/handler.go` | 新建 | summary、trends、requests、providers、models、coverage 的管理 API 处理器 |
| `internal/usage/session_sync.go` | 新建 | Claude session 日志扫描和 usage 补账 |
| `internal/usage/*_test.go` | 新建 | 存储层、解析器、SSE 观察器、脱敏、API 查询、session 补账的单元测试 |
| `internal/config/sqlite_store.go` | 修改 | 增加 usage schema 迁移 hook，并向内部包暴露 DB 句柄 |
| `internal/proxy/handler.go` | 修改 | 记录 provider 请求生命周期、非流式 usage、SSE usage，并保持完整响应转发 |
| `internal/proxy/heartbeat.go` | 修改 | 允许 `copyWithHeartbeat` 投递观察器，同时不观察本地注入的 ping |
| `internal/proxy/server.go` | 修改 | 接收 usage 记录器，并保持服务请求统计与 provider 请求统计分离 |
| `internal/admin/server.go` | 修改 | 挂载 `/api/usage/*` 处理器，并持有 usage 服务依赖 |
| `internal/admin/handler.go` | 修改 | 在 status 响应中包含 usage 摘要字段，或委托 usage summary |
| `cmd/server/main.go` | 修改 | 构造 usage store/handler，启动 session 补账，并把记录器传给 proxy/admin server |
| `docker-compose.yml` | 修改 | 挂载宿主机 Claude projects 目录（只读），用于 session 补账 |
| `internal/frontend/package.json` | 修改 | 增加 `echarts` 依赖 |
| `internal/frontend/src/composables/useApi.ts` | 修改 | 增加 usage API 的 TypeScript 类型和函数 |
| `internal/frontend/src/composables/useI18n.ts` | 修改 | 增加 usage/status 的中英文标签 |
| `internal/frontend/src/views/DashboardView.vue` | 修改 | 增加状态摘要卡片、usage 页签、过滤器、表格、ECharts 趋势面板、请求日志分页 |
| `internal/frontend/src/components/UsageCoverageHelp.vue` | 新建 | Usage 覆盖率帮助提示弹窗组件 |
| `internal/frontend/src/utils/formatters.ts` | 新建 | 格式化工具函数（formatPercent 等） |

---

## 任务 1：增加 Usage 类型、脱敏和解析器测试

**文件：**
- 新建：`internal/usage/types.go`
- 新建：`internal/usage/redact.go`
- 新建：`internal/usage/parse.go`
- 测试：`internal/usage/parse_test.go`
- 测试：`internal/usage/redact_test.go`

- [ ] **步骤 1：创建解析器和脱敏测试**

创建 `internal/usage/parse_test.go`，包含以下测试：

```go
func TestParseRequestMetadataExtractsModelsAndStream(t *testing.T)
func TestParseSourceEntryPointFromBillingHeader(t *testing.T)
func TestParseSourceEntryPointFallsBackToUserAgent(t *testing.T)
func TestExtractUsageFromNonStreamingResponse(t *testing.T)
func TestMissingUsageReturnsMissingStatus(t *testing.T)
```

使用包含 `model`、`stream`、`system` 和 `messages` 的请求体。期望元数据必须包含 `original_model`、`stream`、`source_app=claude_code`，以及 `source_entrypoint=cli` 或 `claude-vscode`。

创建 `internal/usage/redact_test.go`，包含以下测试：

```go
func TestRedactURLRemovesSensitiveQueryValues(t *testing.T)
func TestTruncateUserAgentLimitsTo512Bytes(t *testing.T)
func TestSanitizeErrorMessageRedactsTokensAndLimitsTo1024Bytes(t *testing.T)
func TestSanitizeParseErrorLimitsTo512Bytes(t *testing.T)
```

- [ ] **步骤 2：运行测试并确认失败**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/usage -run 'TestParse|TestRedact|TestTruncate|TestSanitize' -count=1
```

预期：失败，因为 `internal/usage` 尚不存在。

- [ ] **步骤 3：增加类型**

创建 `internal/usage/types.go`，定义：

```go
package usage

import "time"

const (
	UsageSourceProvider = "provider"
	UsageSourceSessionLog = "session_log"
	UsageSourceNone     = "none"

	ParseStatusOK                = "ok"
	ParseStatusMissing           = "missing"
	ParseStatusUnsupportedFormat = "unsupported_format"
	ParseStatusParseError        = "parse_error"
	ParseStatusSkippedNon2xx     = "skipped_non_2xx"
	ParseStatusNetworkError      = "network_error"

	ErrorHTTP            = "http_error"
	ErrorNetwork         = "network_error"
	ErrorUpstreamTimeout = "upstream_timeout"
	ErrorClientAborted   = "client_aborted"
)

type RequestRecord struct {
	ID                       string
	StartedAt                time.Time
	EndedAt                  *time.Time
	DurationMS               *int64
	UpstreamResponseHeaderMS *int64
	TimeToFirstByteMS        *int64
	StatusCode               *int
	ErrorType                string
	ErrorMessage             string
	Method                   string
	RequestPath              string
	BackendURL               string
	ProviderID               string
	ProviderName             string
	ProviderAPIURL           string
	SourceApp                string
	SourceEntrypoint         string
	UserAgent                string
	OriginalModel            string
	MappedModel              string
	Stream                   bool
	RequestBytes             int64
	ResponseBytes            int64
}

type TokenRecord struct {
	RequestID                string
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	UsageSource              string
	UsageParseStatus         string
	UsageParseError          string
}

type UsageValues struct {
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	HasAny                   bool
}

type RequestMetadata struct {
	OriginalModel    string
	Stream           bool
	SourceApp        string
	SourceEntrypoint string
	UserAgent         string
}
```

同时为后续查询定义统计口径常量：

```go
const (
	StatsScopeEffective  = "effective"
	StatsScopeProvider   = "provider"
	StatsScopeSessionLog = "session_log"
	StatsScopeRaw        = "raw"
)
```

- [ ] **步骤 4：实现脱敏辅助函数**

创建 `internal/usage/redact.go`，包含：

```go
package usage

func RedactURL(raw string) string
func TruncateUserAgent(ua string) string
func SanitizeErrorMessage(msg string) string
func SanitizeParseError(msg string) string
```

规则：
- 移除或替换 key 中包含 `key`、`token`、`secret`、`auth`、`password`、`cookie` 的 query 值。
- 将类似 bearer token 的值替换为 `[REDACTED]`。
- 将 User-Agent 限制为 512 字节。
- 将错误信息限制为 1024 字节。
- 将解析错误限制为 512 字节。

- [ ] **步骤 5：实现解析器**

创建 `internal/usage/parse.go`，包含：

```go
package usage

import "net/http"

func ParseRequestMetadata(body []byte, headers http.Header) RequestMetadata
func ExtractUsageFromJSON(body []byte) (UsageValues, string, string)
```

当顶层 `usage` 至少包含一个已知 token 字段时，`ExtractUsageFromJSON` 返回 `(usage, UsageSourceProvider, ParseStatusOK)`。当 JSON 合法但没有 usage 时，返回 `(UsageValues{}, UsageSourceNone, ParseStatusMissing)`。当 JSON 格式错误时，返回 `(UsageValues{}, UsageSourceNone, ParseStatusParseError)`。

- [ ] **步骤 6：运行解析器测试**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/usage -run 'TestParse|TestRedact|TestTruncate|TestSanitize' -count=1
```

预期：通过。

- [ ] **步骤 7：提交解析器基础能力**

```bash
git add internal/usage/types.go internal/usage/redact.go internal/usage/parse.go internal/usage/parse_test.go internal/usage/redact_test.go
git commit -m "feat: 增加 usage 解析基础能力"
```

---

## 任务 2：增加 Usage 存储和 Schema 迁移

**文件：**
- 新建：`internal/usage/store.go`
- 测试：`internal/usage/store_test.go`
- 修改：`internal/config/sqlite_store.go`

- [ ] **步骤 1：编写存储层测试**

创建 `internal/usage/store_test.go`，包含以下测试：

```go
func TestStoreMigratesUsageSchema(t *testing.T)
func TestRecordRequestAlwaysWritesTokenRow(t *testing.T)
func TestSummaryAggregatesEffectiveUsage(t *testing.T)
func TestEffectiveScopeExcludesDuplicateSessionLogUsage(t *testing.T)
func TestRawScopeIncludesProviderAndSessionLogUsage(t *testing.T)
func TestCoverageGroupsByProviderURLModelAndEntrypoint(t *testing.T)
func TestRequestsFilterBySourceEntrypointUsageStatusAndSearch(t *testing.T)
func TestRequestsCanFilterStatsScope(t *testing.T)
func TestTodaySummaryUsesTimezone(t *testing.T)
```

写入混合种子记录：
- `usage_source=provider`
- `usage_source=session_log`
- `usage_source=none`
- `source_entrypoint=cli`
- `source_entrypoint=claude-vscode`
- `source_entrypoint=session_log`
- `status_code=200`
- `status_code=500`
- `error_type=network_error`
- 一组模型相同、四项 token 相同、时间戳在 `±10 分钟` 内的 provider/session_log 重复记录
- 一条没有匹配 provider 的非重复 session_log 记录

- [ ] **步骤 2：运行存储层测试并确认失败**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/usage -run 'TestStore|TestRecord|TestSummary|TestCoverage|TestRequests|TestToday' -count=1
```

预期：失败，因为 `Store` 尚不存在。

- [ ] **步骤 3：实现 usage store**

创建 `internal/usage/store.go`，包含：

```go
package usage

import "database/sql"

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store
func (s *Store) Migrate() error
func (s *Store) Record(req RequestRecord, tok TokenRecord) error
func (s *Store) Summary(filter Filter) (Summary, error)
func (s *Store) Trends(filter Filter) ([]TrendPoint, error)
func (s *Store) Requests(filter Filter) (RequestPage, error)
func (s *Store) Providers(filter Filter) ([]AggregateRow, error)
func (s *Store) Models(filter Filter) ([]AggregateRow, error)
func (s *Store) Coverage(filter Filter) ([]CoverageRow, error)
```

在 `types.go` 中定义 `Filter`、`Summary`、`TrendPoint`、`RequestPage`、`AggregateRow` 和 `CoverageRow`。

`Filter` 必须包含：

```go
StatsScope string
```

默认 `StatsScopeEffective`。各查询必须支持：
- `effective`：provider 记录 + 非重复 session_log 记录。
- `provider`：仅 provider 实时记录。
- `session_log`：仅 Session Log 导入记录，包含重复记录。
- `raw`：全部原始记录，不排重。

`RequestRow` 需要提供重复标记字段：

```go
DedupeStatus    string // "" 或 "duplicate"
DedupeRequestID string // 可选，匹配到的 provider request ID
```

`Migrate()` 必须创建：
- `usage_requests`
- `usage_tokens`
- 规格中的全部索引
- 使用 `INSERT OR IGNORE` 写入 `settings.usage_retention_days = 90`

`Record()` 必须使用一个事务，并始终为每条 `usage_requests` 记录向 `usage_tokens` 插入一行。

- [ ] **步骤 4：从 SQLite store 暴露 DB handle**

修改 `internal/config/sqlite_store.go`：

```go
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}
```

保持 `db.SetMaxOpenConns(1)` 不变。

- [ ] **步骤 5：运行存储层测试**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/usage -count=1
```

预期：通过。

- [ ] **步骤 6：运行 config 回归测试**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/config -count=1
```

预期：通过。

- [ ] **步骤 7：提交 store 工作**

```bash
git add internal/usage/store.go internal/usage/store_test.go internal/config/sqlite_store.go
git commit -m "feat: 增加 usage 统计存储"
```

---

## 任务 3：增加 SSE Usage 观察器

**文件：**
- 新建：`internal/usage/sse.go`
- 测试：`internal/usage/sse_test.go`
- 修改：`internal/proxy/heartbeat.go`
- 测试：`internal/proxy/heartbeat_test.go`

- [ ] **步骤 1：编写 SSE 观察器测试**

创建 `internal/usage/sse_test.go`，包含以下测试：

```go
func TestSSEObserverExtractsUsageFromMessageStart(t *testing.T)
func TestSSEObserverMergesPartialUsageFields(t *testing.T)
func TestSSEObserverIgnoresPingEvents(t *testing.T)
func TestSSEObserverMarksParseErrorWithoutPanic(t *testing.T)
func TestSSEObserverTracksFirstDataChunk(t *testing.T)
```

使用包含 `event: message_start`、`event: message_delta`、`event: ping` 和格式错误 JSON 的 SSE chunk。

- [ ] **步骤 2：运行测试并确认失败**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/usage -run 'TestSSEObserver' -count=1
```

预期：失败，因为 `SSEObserver` 尚不存在。

- [ ] **步骤 3：实现 SSE 观察器**

创建 `internal/usage/sse.go`，包含：

```go
package usage

import "time"

type SSEObserver struct {
	startedAt time.Time
}

func NewSSEObserver(startedAt time.Time) *SSEObserver
func (o *SSEObserver) Observe(chunk []byte)
func (o *SSEObserver) Result() (UsageValues, string, string, *int64)
```

`Result()` 返回 usage 值、`usage_source`、`usage_parse_status`，以及首个数据 chunk 延迟毫秒数。合并 usage 字段时，如果后续 usage 对象缺少某字段，则保留已有值。

- [ ] **步骤 4：更新 heartbeat copy 以观察上游 chunk**

修改 `internal/proxy/heartbeat.go`，增加：

```go
type ChunkObserver interface {
	Observe(chunk []byte)
}

func copyWithHeartbeatAndObserver(dst *heartbeatWriter, src io.Reader, observer ChunkObserver) error
```

保留 `copyWithHeartbeat(dst, src)` 作为调用 `copyWithHeartbeatAndObserver(dst, src, nil)` 的包装函数，确保现有测试和调用方保持兼容。

只对从 `src` 读取到的上游 chunk 调用 `observer.Observe(buf[:n])`。不要观察 `anthropicPingEvent`。

- [ ] **步骤 5：运行 SSE 和 heartbeat 测试**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/usage ./internal/proxy -run 'TestSSEObserver|TestCopyWithHeartbeat|TestHeartbeat' -count=1
```

预期：通过。

- [ ] **步骤 6：提交 SSE 观察器**

```bash
git add internal/usage/sse.go internal/usage/sse_test.go internal/proxy/heartbeat.go internal/proxy/heartbeat_test.go
git commit -m "feat: 观察流式 usage"
```

---

## 任务 4：将 Usage 记录接入 Proxy

**文件：**
- 修改：`internal/proxy/handler.go`
- 修改：`internal/proxy/server.go`
- 测试：`internal/proxy/server_test.go`
- 测试：`internal/proxy/heartbeat_test.go`

- [ ] **步骤 1：编写 proxy 集成测试**

向 `internal/proxy/server_test.go` 增加以下测试：

```go
func TestProxyRecordsNonStreamingProviderUsage(t *testing.T)
func TestProxyRecordsUsageNoneWhenUsageMissing(t *testing.T)
func TestProxyRecordsHTTPErrorAndForwardsFullBody(t *testing.T)
func TestProxyRecordsNetworkError(t *testing.T)
func TestProxyRecordsStreamingUsage(t *testing.T)
func TestProxyDoesNotRecordHardcodedEndpointUsage(t *testing.T)
```

使用实现以下接口的假记录器：

```go
type fakeUsageRecorder struct {
	records []usage.RequestRecord
	tokens  []usage.TokenRecord
}

func (f *fakeUsageRecorder) Record(req usage.RequestRecord, tok usage.TokenRecord) error
```

- [ ] **步骤 2：运行 proxy 测试并确认失败**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/proxy -run 'TestProxyRecords|TestProxyDoesNotRecord' -count=1
```

预期：失败，因为 proxy 尚未接入 usage 记录器。

- [ ] **步骤 3：向 proxy 增加 recorder 接口**

修改 `internal/proxy/handler.go`：

```go
type UsageRecorder interface {
	Record(req usage.RequestRecord, tok usage.TokenRecord) error
}
```

更新 `Handler` 和 `NewHandler`，让它们接收 `UsageRecorder`。nil recorder 必须是合法输入，并跳过记录。

- [ ] **步骤 4：记录生命周期元数据**

在 `ServeHTTP` 中：
- 转发前生成请求 ID。
- 记录 `started_at`、`method`、`request_path`、`request_bytes`、来源元数据、原始/映射后模型、provider 快照、脱敏后的 backend URL。
- 在 `client.Do` 后立即测量 `upstream_response_header_ms`。
- 在请求完成时测量 `duration_ms`。
- 当 `client.Do` 失败时记录 `network_error` 和 `upstream_timeout`。
- 不要把本地 `config_error`、`request_too_large` 或硬编码端点记录到 usage 表。

- [ ] **步骤 5：保留非流式和错误响应的完整转发**

将当前读取 4KB 错误响应体的路径替换为观察器：它把完整 provider 响应体复制给客户端，同时最多保留 4MB 用于 usage 解析、最多保留 1024 字节用于 `error_message`。客户端必须收到 4xx/5xx 响应的完整 provider 响应体。

- [ ] **步骤 6：接入流式观察器**

对于 SSE 响应：
- 创建 `usage.NewSSEObserver(startedAt)`。
- 将它传给 `copyWithHeartbeatAndObserver`。
- 使用观察器结果创建 `usage.TokenRecord`。
- `response_bytes` 只统计上游 chunk，不统计注入的 heartbeat ping。

- [ ] **步骤 7：运行 proxy 测试**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/proxy -count=1
```

预期：通过。

- [ ] **步骤 8：提交 proxy 集成**

```bash
git add internal/proxy/handler.go internal/proxy/server.go internal/proxy/server_test.go internal/proxy/heartbeat.go internal/proxy/heartbeat_test.go
git commit -m "feat: 在 proxy 中记录 provider usage"
```

---

## 任务 5：增加 Usage 管理 API

**文件：**
- 新建：`internal/usage/handler.go`
- 测试：`internal/usage/handler_test.go`
- 修改：`internal/admin/server.go`
- 修改：`internal/admin/handler.go`
- 测试：`internal/admin/auth_test.go`

- [ ] **步骤 1：编写 usage API 测试**

创建 `internal/usage/handler_test.go`，包含以下测试：

```go
func TestUsageSummaryHandler(t *testing.T)
func TestUsageRequestsHandlerFiltersAndSearches(t *testing.T)
func TestUsageCoverageHandler(t *testing.T)
func TestUsageHandlersRejectInvalidTimezone(t *testing.T)
```

通过 `usage.Store` 向内存 SQLite DB 写入种子数据，并通过 `httptest` 调用处理器。

- [ ] **步骤 2：运行测试并确认失败**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/usage -run 'TestUsage.*Handler' -count=1
```

预期：失败，因为 HTTP 处理器尚不存在。

- [ ] **步骤 3：实现 usage HTTP 处理器**

创建 `internal/usage/handler.go`，包含：

```go
package usage

import "net/http"

type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.HandlerFunc) http.HandlerFunc)
```

注册：
- `/api/usage/summary`
- `/api/usage/trends`
- `/api/usage/requests`
- `/api/usage/providers`
- `/api/usage/models`
- `/api/usage/coverage`

解析规格中的 query 参数，包括 `source_entrypoint`、`request_path`、`q` 和 `tz`。
同时解析 `stats_scope`，允许值为 `effective`、`provider`、`session_log`、`raw`，默认 `effective`。无效值返回明确 400 错误。

Coverage 响应需要同时保留：
- Provider Usage 覆盖率：只衡量 provider 响应是否返回 usage。
- 有效 Usage 覆盖率：按 `effective` 口径计入非重复 Session Log 补账。

- [ ] **步骤 4：接入 admin server**

修改 `internal/admin/server.go`，接收可选 usage 处理器，并在 `Start` 内调用 `usageHandler.Register(mux, s.authMiddlewareFunc)`。

修改 `internal/admin/handler.go`，让 `/api/status` 可以包含：
- `service_requests_total`
- `provider_requests_total`
- `today_provider_requests`
- `today_token_consumption`
- `usage_coverage`
- `last_provider_request`

如果 usage store 为 nil，返回现有 status 字段和零值 usage 摘要字段。

- [ ] **步骤 5：运行 admin 和 usage 测试**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/usage ./internal/admin -count=1
```

预期：通过。

- [ ] **步骤 6：提交 admin API**

```bash
git add internal/usage/handler.go internal/usage/handler_test.go internal/admin/server.go internal/admin/handler.go internal/admin/auth_test.go
git commit -m "feat: 暴露 usage 统计 API"
```

---

## 任务 6：启动时接入 Usage 存储和 Session 补账

**文件：**
- 修改：`cmd/server/main.go`
- 修改：`internal/proxy/server.go`
- 修改：`internal/admin/server.go`
- 修改：`docker-compose.yml`

- [ ] **步骤 1：更新 server 构造**

在 `cmd/server/main.go` 的 `config.NewSQLiteStore` 之后创建：

```go
usageStore := usage.NewStore(configStore.DB())
if err := usageStore.Migrate(); err != nil {
	log.Fatalf("Failed to initialize usage store: %v", err)
}
usageHandler := usage.NewHandler(usageStore)
```

将 `usageStore` 传给 `proxy.NewServer`，将 `usageHandler` 传给 `admin.NewServer`。

启动 session 补账 goroutine：

```go
usageSyncCtx, usageSyncCancel := context.WithCancel(context.Background())
defer usageSyncCancel()
usage.StartClaudeSessionSync(usageSyncCtx, usageStore, usage.DefaultClaudeProjectsDir(), time.Minute)
```

- [ ] **步骤 1B：更新 docker-compose.yml**

增加宿主机 Claude projects 目录挂载：

```yaml
volumes:
  - ${CLAUDE_PROJECTS_DIR:-${HOME}/.claude/projects}:/claude-projects:ro
environment:
  - CLAUDE_PROJECTS_DIR=/claude-projects
```

挂载为只读，补账只写入 SQLite，不修改宿主机文件。

Windows 宿主机必须在部署文档中要求显式设置 `CLAUDE_PROJECTS_DIR`，例如 `C:\Users\<username>\.claude\projects`。容器内部仍统一使用 `/claude-projects`。

- [ ] **步骤 2：更新构造函数**

修改 proxy/admin 构造函数，确保调用点可以编译：

```go
func NewServer(store config.ConfigStore, usageRecorder UsageRecorder) *Server
```

and:

```go
func NewServer(cfg *AdminConfig, configStore config.ConfigStore, statsProvider StatsProvider, usageHandler *usage.Handler) *Server
```

在不测试 usage 的测试中传入 nil，以保持兼容。

- [ ] **步骤 3：运行完整 Go 测试**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./... -count=1
```

预期：通过。

- [ ] **步骤 4：提交启动接线**

```bash
git add cmd/server/main.go internal/proxy/server.go internal/admin/server.go
git commit -m "feat: 初始化 usage 统计"
```

---

## 任务 7：增加 Claude Session Usage 补账

**文件：**
- 新建：`internal/usage/session_sync.go`
- 测试：`internal/usage/session_sync_test.go`

- [ ] **步骤 1：编写 session 补账测试**

创建 `internal/usage/session_sync_test.go`，包含以下测试：

```go
func TestSessionSyncImportsUsageFromLog(t *testing.T)
func TestSessionSyncSkipsAlreadySynced(t *testing.T)
func TestSessionSyncImportsSessionUsageAsSeparateSource(t *testing.T)
func TestSessionSyncDoesNotOverwriteOrDeleteProviderUsage(t *testing.T)
func TestSessionSyncKeepsDuplicateSessionLogVisibleInRawScope(t *testing.T)
func TestEffectiveScopeCountsProviderInsteadOfDuplicateSessionLog(t *testing.T)
func TestSessionSyncHandlesInvalidJSONLWithoutPanic(t *testing.T)
func TestSessionSyncTracksFileOffset(t *testing.T)
```

使用包含 `type`、`timestamp`、`message.id`、`message.model`、`message.usage` 的 JSONL 文件，以及已有 provider 实时统计种子记录。

- [ ] **步骤 2：运行测试并确认失败**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/usage -run 'TestSessionSync' -count=1
```

预期：失败，因为 session sync 尚不存在。

- [ ] **步骤 3：实现 session sync**

创建 `internal/usage/session_sync.go`，包含：

```go
package usage

type SessionSyncResult struct { ... }

func StartClaudeSessionSync(ctx context.Context, store *Store, projectsDir string, interval time.Duration)
func DefaultClaudeProjectsDir() string
func (s *Store) SyncClaudeSessions(projectsDir string) SessionSyncResult
```

扫描 `$CLAUDE_PROJECTS_DIR/**/*.jsonl`，通过 `session_log_sync` 表跟踪已同步文件状态。每条完整 assistant final usage 以独立请求记录写入：

```text
id=session:<message.id>
provider_id=_session
provider_name=Session Log
method=SESSION
request_path=session_log
source_app=claude_code
source_entrypoint=session_log
usage_source=session_log
usage_parse_status=ok
```

Session sync 不覆盖、不删除 provider 实时记录。重复识别只在查询/聚合层用于 `effective` 口径。

- [ ] **步骤 4：运行测试**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./internal/usage -run 'TestSessionSync' -count=1
```

预期：通过。

- [ ] **步骤 5：提交 session 补账**

```bash
git add internal/usage/session_sync.go internal/usage/session_sync_test.go
git commit -m "feat: 同步 Claude session usage"
```

---

## 任务 8：增加前端 Usage API 客户端和 ECharts 依赖

**文件：**
- 修改：`internal/frontend/package.json`
- 修改：`internal/frontend/package-lock.json`
- 修改：`internal/frontend/src/composables/useApi.ts`

- [ ] **步骤 1：增加 ECharts**

运行：

```bash
cd internal/frontend
npm install echarts
```

预期：`package.json` 和 `package-lock.json` 包含 `echarts`。

- [ ] **步骤 2：增加 usage API 类型**

修改 `internal/frontend/src/composables/useApi.ts`，导出：

```ts
export interface UsageSummary {
  service_requests_total: number
  provider_requests_total: number
  today_provider_requests: number
  token_consumption_total: number
  today_token_consumption: number
  failed_requests: number
  usage_coverage: number
  last_provider_request: string | null
}

export interface UsageTrendPoint {
  bucket: string
  input_tokens: number
  output_tokens: number
  cache_creation_input_tokens: number
  cache_read_input_tokens: number
  token_consumption_total: number
  provider_requests_total: number
  failed_requests: number
  usage_coverage: number
}

export interface UsageRequestRow {
  id: string
  started_at: string
  duration_ms: number | null
  upstream_response_header_ms: number | null
  time_to_first_byte_ms: number | null
  status_code: number | null
  error_type: string
  provider_name: string
  provider_api_url: string
  source_entrypoint: string
  original_model: string
  mapped_model: string
  stream: boolean
  input_tokens: number
  output_tokens: number
  cache_creation_input_tokens: number
  cache_read_input_tokens: number
  usage_source: 'provider' | 'session_log' | 'none'
  usage_parse_status: string
  dedupe_status?: 'duplicate' | ''
  dedupe_request_id?: string
}
```

增加 `getUsageSummary`、`getUsageTrends`、`getUsageRequests`、`getUsageProviders`、`getUsageModels` 和 `getUsageCoverage`。每个函数接收普通 params 对象并构建 `URLSearchParams`，支持传入 `stats_scope`。

- [ ] **步骤 3：运行前端类型检查**

运行：

```bash
cd internal/frontend
npm run build
```

预期：通过。

- [ ] **步骤 4：提交前端 API 基础能力**

```bash
git add internal/frontend/package.json internal/frontend/package-lock.json internal/frontend/src/composables/useApi.ts
git commit -m "feat: 增加 usage 前端 API 客户端"
```

---

## 任务 9：构建 Usage 界面和状态摘要

**文件：**
- 修改：`internal/frontend/src/views/DashboardView.vue`
- 修改：`internal/frontend/src/composables/useI18n.ts`
- 修改：`internal/frontend/src/styles/main.css`

- [ ] **步骤 1：增加 i18n 标签**

增加以下 zh/en key：
- `tab.usage`
- `usage.overview`
- `usage.requests`
- `usage.providers`
- `usage.models`
- `usage.coverage`
- `usage.provider_requests_total`
- `usage.token_consumption_total`
- `usage.usage_coverage`
- `usage.stats_scope`
- `usage.stats_scope_effective`
- `usage.stats_scope_provider`
- `usage.stats_scope_session_log`
- `usage.stats_scope_raw`
- `usage.dedupe_duplicate`
- `usage.failed_requests`
- `usage.source_entrypoint`
- `usage.usage_status`
- `status.provider_requests_total`
- `status.today_provider_requests`
- `status.today_token_consumption`
- `status.usage_coverage`
- `status.last_provider_request`

- [ ] **步骤 2：增加 usage 页签状态和 API 加载**

修改 `DashboardView.vue`：
- 增加顶级 `usage` 页签。
- 所有页签统一使用 `max-w-[1440px]` 容器宽度。
- 增加 usage 过滤器：日期范围、provider、模型、状态、usage 来源、来源入口、搜索。
- 增加统计口径切换：有效统计、实时请求、Session Log、全部原始。
- 从 `useApi` 加载 summary/trends/requests/providers/models/coverage。
- 请求日志默认 10 条/页，可选 20、50、100 条/页。

- [ ] **步骤 3：增加状态摘要卡片**

扩展状态页签卡片，展示：
- 服务状态
- 运行时间
- 服务总请求数
- provider 请求总数
- 今日 provider 请求数
- 今日 token 消耗
- usage 覆盖率
- 当前 provider
- 最近一次 provider 请求

保持状态页签紧凑，不展示完整请求日志。

- [ ] **步骤 4：增加 ECharts 趋势面板**

在 `DashboardView.vue` 中导入 ECharts：

```ts
import * as echarts from 'echarts'
```

渲染响应式折线图，包含以下序列：
- 输入 token
- 输出 token
- 缓存创建 token
- 缓存命中 token
- provider 请求数
- 失败请求数
- usage 覆盖率

在页签切换或组件卸载时释放图表实例。

- [ ] **步骤 5：增加表格**

增加以下表格区块：
- 请求日志
- provider 聚合
- 模型聚合
- usage 覆盖率

将 `usage_source=none`、`usage_source=session_log`、`usage_parse_status` 和重复 Session Log 标记显示为可见标记。小屏下使用横向滚动。

- [ ] **步骤 6：运行前端构建**

运行：

```bash
cd internal/frontend
npm run build
```

预期：通过，并更新 `internal/frontend/dist`。

- [ ] **步骤 7：提交 usage UI**

```bash
git add internal/frontend/src/views/DashboardView.vue internal/frontend/src/composables/useI18n.ts internal/frontend/src/styles/main.css internal/frontend/dist
git commit -m "feat: 增加 usage 统计 dashboard"
```

---

## 任务 10：最终集成验证

**文件：**
- 仅验证；除非需要修复，否则不修改文件。

- [ ] **步骤 1：运行 Go 测试**

运行：

```bash
env GOCACHE=/tmp/go-build go test ./... -count=1
```

预期：通过。

- [ ] **步骤 2：运行前端构建**

运行：

```bash
cd internal/frontend
npm run build
```

预期：通过。

- [ ] **步骤 3：运行容器构建**

运行：

```bash
docker compose up -d --build
```

预期：容器启动，日志中没有 usage 迁移错误。

- [ ] **步骤 4：验证真实请求路径**

通过 proxy 发送一个 Claude Code CLI 请求和一个 Claude Code VS Code 扩展请求。

预期：
- `/api/usage/summary` 显示 `provider_requests_total >= 2`。
- `/api/usage/requests` 包含两个请求。
- 当请求元数据包含对应值时，`source_entrypoint` 能区分 `cli` 和 `claude-vscode`。
- 每条 `usage_requests` 都有一条对应的 `usage_tokens`。
- `stats_scope=effective` 不重复计入同一条 provider/session_log usage。
- `stats_scope=session_log` 可以单独查看 Session Log 导入记录。
- `stats_scope=raw` 可以查看全部原始记录。

- [ ] **步骤 5：如有需要，提交最终修复**

如果验证过程中需要修复：

```bash
git add cmd/server/main.go internal/usage internal/proxy/handler.go internal/proxy/server.go internal/proxy/heartbeat.go internal/proxy/server_test.go internal/proxy/heartbeat_test.go internal/admin/server.go internal/admin/handler.go internal/admin/auth_test.go internal/config/sqlite_store.go internal/frontend/package.json internal/frontend/package-lock.json internal/frontend/src/composables/useApi.ts internal/frontend/src/composables/useI18n.ts internal/frontend/src/views/DashboardView.vue internal/frontend/src/styles/main.css internal/frontend/dist
git commit -m "fix: 完成 usage 统计验证"
```

预期：如果前面所有任务都干净通过，则不需要提交。

---

## 任务 11：增加统计数据清除能力

**状态：** 已实现（2026-06-11）

**文件规划：**

后端：

1. `internal/usage/store.go`
2. `internal/usage/store_test.go`
3. `internal/usage/handler.go`
4. `internal/usage/handler_test.go`

前端：

1. `internal/frontend/src/composables/useApi.ts`
2. `internal/frontend/src/composables/useI18n.ts`
3. `internal/frontend/src/views/DashboardView.vue`
4. `internal/frontend/src/views/DashboardUsageRequests.test.ts`
5. `internal/frontend/dist/*`

### 任务 11.1：存储层清除接口

- [x] 在 `Store` 中增加清除统计数据方法。
- [x] 使用事务删除 `usage_tokens` 和 `usage_requests`。
- [x] 默认保留 `session_log_sync`。
- [x] 当 `resetSessionSync=true` 时删除 `session_log_sync`。
- [x] 返回删除的 request/token 数量，便于 API 响应和测试断言。
- [x] 增加测试：默认清除不删除 `session_log_sync`。
- [x] 增加测试：重置同步状态时删除 `session_log_sync`。
- [x] 增加测试：清除后新的 usage 记录仍可写入。

### 任务 11.2：管理 API

- [x] 增加 `POST /api/usage/clear`。
- [x] 请求体支持 `reset_session_sync`，默认 false。
- [x] 接口复用管理鉴权。
- [x] 成功返回 `success`、`cleared_requests`、`cleared_tokens`、`reset_session_sync`。
- [x] 错误时返回非 2xx，且不留下部分删除状态。
- [x] 增加 handler 测试覆盖默认清除、重置同步状态、鉴权。

### 任务 11.3：前端清除数据交互

- [x] 在使用统计页顶部 `刷新` 按钮左侧增加 `清除数据` 按钮。
- [x] 点击后展示确认弹窗。
- [x] 弹窗说明会清除全部统计数据，不删除本地 JSONL，不影响会话记录，清除后会继续记录新请求。
- [x] 弹窗包含默认不勾选的 `同时重置 Session Log 同步状态` checkbox。
- [x] checkbox 说明迁移 data 目录、更换系统或更换 session 日志目录时使用，勾选后可能重新导入当前机器 JSONL 历史 usage。
- [x] 确认后调用 `POST /api/usage/clear`。
- [x] 成功后刷新当前统计页面。
- [x] 失败时展示错误并保留当前页面状态。

### 任务 11.4：验证

- [x] `go test ./internal/usage ./internal/admin`
- [x] `go test ./...`
- [x] `npm --prefix internal/frontend test`
- [x] `npm --prefix internal/frontend run build`
- [ ] 手动验证默认清除后历史 Session Log 不会立刻补回。
- [ ] 手动验证勾选重置同步状态后，当前机器 JSONL 可重新补账。

### 实现记录（2026-06-11）

- `internal/usage/store.go` 新增 `ClearUsageData(resetSessionSync bool)`，事务内删除 `usage_tokens`、`usage_requests`，并按需删除 `session_log_sync`。
- `internal/usage/handler.go` 新增 `POST /api/usage/clear`，请求体使用 `reset_session_sync`。
- `internal/frontend/src/views/DashboardView.vue` 新增清除数据按钮、确认弹窗、Session Log 同步状态重置 checkbox、成功后刷新当前 Usage 数据。
- 自动化验证已通过：`go test ./... -count=1`、`npm --prefix internal/frontend test`、`npm --prefix internal/frontend run build`。
