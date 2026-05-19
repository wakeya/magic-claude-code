# 使用统计规格

**版本**: 0.3
**日期**: 2026-05-18
**状态**: 待审核

---

## 1. 背景

当前项目已经能代理 Claude Code 请求到不同 provider，并通过前端切换当前 provider。现有状态页只展示服务运行状态、uptime 和总请求数，无法回答以下问题：

1. 每个 provider、模型、Claude Code 入口实际返回了多少 input/output/cache token。
2. 哪些请求失败、耗时较高、触发限额或参数错误。
3. 哪些 provider/API 地址/模型没有返回可解析的 usage。
4. Claude Code 内部不同入口是否能分开统计，例如 CLI、VS Code 扩展、其他未知入口。

首版目标是做“真实 usage 统计 + 请求质量统计 + usage 覆盖率分析”。不做本地 token 估算，不做成本估算，不做模型价格配置。

管理页需要新增“使用统计”顶级页签，并让状态页展示轻量统计摘要。当前管理页 `max-w-[900px]` 对统计页面偏窄，使用统计页需要单独放宽展示宽度。

---

## 2. 设计决策

采用“只记录 provider 返回的 usage”的方案。

原因：

1. Anthropic-compatible 响应通常会返回 `usage`，流式响应也会在 SSE 事件中携带 usage，能直接反映 provider 实际计数。
2. 不同 provider 和模型的 tokenizer 差异较大，本地估算容易被误认为账单级数据。
3. 首版更需要可靠性和可解释性：有 usage 就统计，没 usage 就明确显示为未返回或格式不支持。
4. usage 覆盖率本身是重要诊断信号，可以帮助发现 provider 兼容性问题。

本功能与 SQLite 配置存储改造共享同一个数据库：

```text
<dataDir>/proxy.db
```

---

## 3. 非目标

1. 不拦截或修改 provider 响应内容，统计逻辑必须是旁路观察。
2. 不实现本地 tokenizer，也不写入估算 token。
3. 不做成本估算、账单对账、价格配置或倍率配置。
4. 不做云端同步或多实例汇总。
5. 不实现复杂报表导出；数据保留只预留配置，不在首版执行自动清理。

---

## 4. 采集范围

首版只统计代理到 AI provider 的主请求：

1. `/v1/messages`
2. `/anthropic/v1/messages`
3. provider 基础 URL 拼接后实际转发的 messages 请求

不统计本地拦截的硬编码端点，例如 OAuth、settings、quota mock 响应。这些请求不是 provider 真实模型调用，混入统计会污染 usage 覆盖率和请求质量数据。

---

## 5. 请求元数据

每次 provider 请求记录以下元数据：

| 字段 | 说明 |
|------|------|
| `id` | 请求日志 ID |
| `started_at` | 请求开始时间 |
| `ended_at` | 请求结束时间 |
| `duration_ms` | 总耗时 |
| `upstream_response_header_ms` | provider 响应头到达耗时，适用于流式和非流式 |
| `time_to_first_byte_ms` | provider 响应体首个字节或首个 SSE 数据 chunk 到达耗时；无响应体时为空 |
| `status_code` | provider HTTP 状态码；网络错误为空 |
| `error_type` | 成功为空；失败时记录 `http_error`、`network_error`、`upstream_timeout` 等 |
| `method` | 原始请求方法 |
| `request_path` | 原始请求路径，例如 `/v1/messages` |
| `backend_url` | 实际转发到 provider 的 URL，去除 query 中的敏感参数 |
| `provider_id` | 请求开始时的 active provider ID |
| `provider_name` | 请求开始时的 provider 名称快照 |
| `provider_api_url` | 请求开始时的 provider APIURL 快照 |
| `source_app` | 首版为 `claude_code` 或 `unknown` |
| `source_entrypoint` | 从 billing header 或 User-Agent 解析出的 Claude Code 入口 |
| `user_agent` | 原始 User-Agent 摘要，便于后续修正来源识别 |
| `original_model` | 请求体原始 `model` |
| `mapped_model` | 经过 provider model mapping 后的实际模型 |
| `stream` | 请求是否流式 |
| `request_bytes` | 转发请求体大小 |
| `response_bytes` | provider 响应体字节数，流式按已转发字节累计 |

来源解析优先级：

1. 请求体 `system` 文本中的 `x-anthropic-billing-header`，例如 `cc_entrypoint=cli`、`cc_entrypoint=claude-vscode`。
2. 请求头 `User-Agent`。
3. 能识别为 Claude Code 时，`source_app=claude_code`，`source_entrypoint` 记录 `cli`、`claude-vscode` 等入口。
4. 无法识别时记为 `source_app=unknown`，但保留 `user_agent` 摘要。

`user_agent` 最多保存 512 字节，超出后截断。`backend_url` 和 `error_message` 写入前必须执行敏感信息脱敏，移除 Authorization、API key、Cookie、token-like query 参数等内容。

`error_type` 固定枚举：

| 状态 | 说明 | 是否计入 provider 请求 |
|------|------|------------------------|
| `''` | 请求成功 | 是 |
| `http_error` | provider 返回 4xx/5xx | 是 |
| `network_error` | 连接失败、DNS、TLS、连接重置等 | 是 |
| `upstream_timeout` | provider 请求超时 | 是 |
| `client_aborted` | 客户端断开导致转发中断 | 是，若已开始 provider 请求 |
| `request_too_large` | 请求体超过代理限制 | 否，未转发到 provider |
| `config_error` | 无 active provider 或配置加载失败 | 否，未转发到 provider |
| `transform_error` | 请求体转换失败并且无法转发 | 否，未转发到 provider |

Usage 统计只聚合已经尝试转发到 provider 的请求；未转发到 provider 的代理本地错误可以继续进入服务健康日志，但不进入 `usage_requests` 主统计。

---

## 6. Usage 数据

每次请求记录以下 usage 字段：

| 字段 | 说明 |
|------|------|
| `input_tokens` | provider 返回的输入 token |
| `output_tokens` | provider 返回的输出 token |
| `cache_creation_input_tokens` | provider 返回的缓存创建 token |
| `cache_read_input_tokens` | provider 返回的缓存命中 token |
| `usage_source` | `provider` 或 `none` |
| `usage_parse_status` | usage 解析状态，见下方枚举 |
| `usage_parse_error` | 可读错误摘要；成功或正常缺失时为空 |

`usage_source` 语义：

```text
provider     = provider 响应中成功提取 usage
session_log  = 从 Claude Code 本地 session 日志中提取 usage（补账，见第 9A 节）
none         = provider 未返回 usage，或响应格式不支持，或请求没有拿到可解析响应
```

`session_log` 是通过后台定时扫描宿主机 Claude Code session 日志文件来补充 usage 数据。它不改变原始请求记录，只更新已有请求的 token 字段。详见第 9A 节。

`usage_parse_status` 枚举：

| 状态 | 说明 |
|------|------|
| `ok` | 成功提取 usage |
| `missing` | 2xx 响应成功，但未发现 usage 字段 |
| `unsupported_format` | 响应格式不是当前支持的 Anthropic-style usage |
| `parse_error` | 响应像是目标格式，但 JSON 或字段解析失败 |
| `skipped_non_2xx` | provider 返回 4xx/5xx，不按成功响应解析 usage |
| `network_error` | 网络错误或超时，没有拿到 provider 响应 |

请求成功与 usage 解析是两条独立维度。HTTP 2xx 但 `usage_source=none` 不应计入请求失败；它只表示 usage 覆盖率不足。

`usage_parse_error` 最多保存 512 字节，超出后截断，并执行敏感信息脱敏。

---

## 7. SSE 解析

当前代理对 SSE 流使用 `copyWithHeartbeat()` 注入心跳。统计功能需要在不改变转发行为的前提下观察 SSE 数据。

设计：

1. 新增 `usageObserver`，实现 `io.Writer` 或接收复制过程中的 chunk。
2. SSE chunk 原样写给客户端，同时把相同 chunk 传给观察器。
3. 观察器按行解析 `event:` 和 `data:`。
4. 遇到 JSON `data:` 时提取 `usage` 字段。
5. 记录首个 provider 数据 chunk 到达时间作为 `time_to_first_byte_ms`。
6. 心跳注入产生的本地 `ping` 不参与 provider 响应字节数和 usage 解析。

需要支持的 Anthropic-style 事件：

```text
message_start
message_delta
message_stop
error
```

兼容策略：

1. 如果 provider 的 SSE 事件格式不是 Anthropic-style，只记录请求元数据，`usage_source=none`、`usage_parse_status=unsupported_format`。
2. 如果 usage 在多次事件中出现，按字段合并 usage；后出现的非空字段覆盖已有值，缺失字段保留已有值，避免 `message_delta` 只返回 output 时丢失 input/cache。
3. 解析失败不影响响应转发，也不污染请求失败率；只记录 `usage_source=none`、`usage_parse_status=parse_error`。
4. provider SSE `error` 事件需要记录请求错误摘要，但仍必须按原样转发给客户端。

---

## 8. 非流式解析

非流式响应在复制前通过 `io.TeeReader` 或受限缓冲观察响应体。

设计：

1. 对 2xx JSON 响应解析顶层 `usage`。
2. 对 2xx 但无 usage 的响应记录 `usage_parse_status=missing`。
3. 对 2xx 但非支持格式的响应记录 `usage_parse_status=unsupported_format`。
4. 对 4xx/5xx 响应记录状态码和错误响应摘要，`usage_parse_status=skipped_non_2xx`。
5. 响应体观察上限建议 4MB，避免大响应导致内存压力。
6. 错误响应摘要最多保存 1024 字节，并执行敏感信息脱敏。
7. 无论解析是否成功，都必须把 provider 原始响应完整转发给客户端。实现时不能为了记录错误摘要而只读前 4KB 后转发；应使用 `TeeReader`、多 writer 或受限观察器，只限制观察内容，不限制转发内容。

---

## 9. Usage 覆盖率和无 usage 分析

首版需要把 `usage_source=none` 作为可见统计对象，而不是只在请求日志里显示。

覆盖率定义：

```text
usage_coverage = requests_with_provider_usage / total_provider_requests
```

需要支持以下聚合维度：

1. 全局覆盖率。
2. 按 provider 聚合。
3. 按 provider API URL 聚合。
4. 按 mapped model 聚合。
5. 按 Claude Code 入口 `source_entrypoint` 聚合。
6. 按 `usage_parse_status` 聚合。

覆盖率表格建议字段：

| 字段 | 说明 |
|------|------|
| `provider_name` | provider 名称 |
| `provider_api_url` | API 地址快照 |
| `mapped_model` | 实际请求模型 |
| `source_entrypoint` | Claude Code 入口 |
| `total_requests` | 总请求数 |
| `success_requests` | HTTP 2xx 请求数 |
| `error_requests` | HTTP 非 2xx 或网络错误请求数 |
| `with_usage_requests` | 成功提取 usage 的请求数 |
| `without_usage_requests` | 未提取 usage 的请求数 |
| `usage_coverage` | 覆盖率 |
| `top_usage_parse_status` | 最常见的无 usage 原因 |
| `last_seen_at` | 最近出现时间 |

这个视图用于回答：

1. 哪个 provider 不返回 usage。
2. 是所有模型都不返回，还是某个模型不返回。
3. 是请求失败导致没有 usage，还是 2xx 成功但响应没有 usage。
4. 哪个 API 地址兼容性最好。
5. 后续应该优先适配哪个 provider 的 usage 格式。

---

## 9A. Claude Session Usage 补账

部分第三方 provider 不在响应中返回 `usage` 字段，但 Claude Code 本地 session 日志文件中包含完整的 token 使用记录。为提高 usage 覆盖率，增加后台补账机制。

### 数据来源

Claude Code 在 `~/.claude/projects/` 目录下的 JSONL 文件中记录每次请求的 session 信息。每行包含 `type`、`timestamp`、`sessionId`、`message` 等字段。其中 `message.usage` 包含 `input_tokens`、`output_tokens`、`cache_creation_input_tokens`、`cache_read_input_tokens`。

### 同步机制

1. 后台 goroutine 每分钟扫描一次 `$CLAUDE_PROJECTS_DIR` 下的 `.jsonl` 文件。
2. 按 session 文件的修改时间判断是否有新内容。
3. 通过 `session_log_sync` 表记录已同步的文件位置，避免重复处理。
4. 匹配规则：按 `timestamp` + `model` + `message ID` 与已有 `usage_requests` 记录关联。
5. 只更新 `usage_source=none` 的记录，不覆盖 `usage_source=provider` 的数据。
6. 补账成功后设置 `usage_source=session_log`、`usage_parse_status=ok`。

### `session_log_sync` 表

```sql
CREATE TABLE IF NOT EXISTS session_log_sync (
  file_path TEXT PRIMARY KEY,
  last_offset INTEGER NOT NULL DEFAULT 0,
  last_synced_at TEXT NOT NULL
);
```

### Docker 挂载

容器需要挂载宿主机的 Claude projects 目录：

```yaml
volumes:
  - ${CLAUDE_PROJECTS_DIR:-${HOME}/.claude/projects}:/claude-projects:ro
environment:
  - CLAUDE_PROJECTS_DIR=/claude-projects
```

挂载为只读（`:ro`），补账只更新 SQLite 中的 usage 字段，不修改宿主机文件。

---

## 10. SQLite Schema 设计

### 10.1 `usage_requests`

```sql
-- usage_requests: 每次代理到 provider 的请求日志主表，用于查询请求状态、来源、provider、模型和耗时
CREATE TABLE IF NOT EXISTS usage_requests (
  id TEXT PRIMARY KEY,                         -- 请求日志唯一 ID
  started_at TEXT NOT NULL,                    -- 请求开始时间，RFC3339Nano 字符串
  ended_at TEXT,                               -- 请求结束时间，RFC3339Nano 字符串
  duration_ms INTEGER,                         -- 请求总耗时，单位毫秒
  upstream_response_header_ms INTEGER,          -- provider 响应头到达耗时，单位毫秒
  time_to_first_byte_ms INTEGER,               -- 响应体首字节或首个 SSE 数据 chunk 到达耗时，单位毫秒
  status_code INTEGER,                         -- provider HTTP 状态码，网络错误时为空
  error_type TEXT NOT NULL DEFAULT '',         -- 请求错误类型，成功时为空字符串
  error_message TEXT NOT NULL DEFAULT '',      -- 请求错误摘要，最多 1024 字节，避免保存完整敏感响应
  method TEXT NOT NULL DEFAULT '',             -- 原始请求方法
  request_path TEXT NOT NULL DEFAULT '',       -- 原始请求路径
  backend_url TEXT NOT NULL DEFAULT '',        -- 实际转发 URL，已脱敏
  provider_id TEXT NOT NULL DEFAULT '',        -- 请求开始时的 provider ID 快照
  provider_name TEXT NOT NULL DEFAULT '',      -- 请求开始时的 provider 名称快照
  provider_api_url TEXT NOT NULL DEFAULT '',   -- 请求开始时的 provider APIURL 快照
  source_app TEXT NOT NULL DEFAULT 'unknown',  -- 请求来源应用，首版为 claude_code 或 unknown
  source_entrypoint TEXT NOT NULL DEFAULT '',  -- 来源入口，例如 cli、claude-vscode
  user_agent TEXT NOT NULL DEFAULT '',         -- User-Agent 摘要
  original_model TEXT NOT NULL DEFAULT '',     -- 请求体原始模型名
  mapped_model TEXT NOT NULL DEFAULT '',       -- 转发给 provider 的实际模型名
  stream INTEGER NOT NULL DEFAULT 0,           -- 是否流式请求，0=false，1=true
  request_bytes INTEGER NOT NULL DEFAULT 0,    -- 转发请求体字节数
  response_bytes INTEGER NOT NULL DEFAULT 0    -- provider 响应体字节数
);
```

索引：

```sql
-- idx_usage_requests_started_at: 按时间倒序查询请求日志和趋势数据
CREATE INDEX IF NOT EXISTS idx_usage_requests_started_at ON usage_requests(started_at);

-- idx_usage_requests_provider: 按 provider 聚合统计
CREATE INDEX IF NOT EXISTS idx_usage_requests_provider ON usage_requests(provider_id, started_at);

-- idx_usage_requests_provider_url: 按 API 地址聚合 usage 覆盖率
CREATE INDEX IF NOT EXISTS idx_usage_requests_provider_url ON usage_requests(provider_api_url, started_at);

-- idx_usage_requests_entrypoint: 按 Claude Code 入口过滤统计
CREATE INDEX IF NOT EXISTS idx_usage_requests_entrypoint ON usage_requests(source_entrypoint, started_at);

-- idx_usage_requests_path: 按请求路径过滤统计
CREATE INDEX IF NOT EXISTS idx_usage_requests_path ON usage_requests(request_path, started_at);

-- idx_usage_requests_model: 按模型聚合统计
CREATE INDEX IF NOT EXISTS idx_usage_requests_model ON usage_requests(mapped_model, started_at);

-- idx_usage_requests_source: 按来源应用过滤统计，首版主要区分 claude_code 和 unknown
CREATE INDEX IF NOT EXISTS idx_usage_requests_source ON usage_requests(source_app, started_at);

-- idx_usage_requests_status: 按请求成功/失败过滤
CREATE INDEX IF NOT EXISTS idx_usage_requests_status ON usage_requests(status_code, error_type, started_at);
```

### 10.2 `usage_tokens`

```sql
-- usage_tokens: 请求 usage 明细表，只保存 provider 返回的真实 usage 和解析状态
CREATE TABLE IF NOT EXISTS usage_tokens (
  request_id TEXT PRIMARY KEY,                       -- 对应 usage_requests.id
  input_tokens INTEGER NOT NULL DEFAULT 0,           -- provider 返回的输入 token
  output_tokens INTEGER NOT NULL DEFAULT 0,          -- provider 返回的输出 token
  cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0, -- provider 返回的缓存创建 token
  cache_read_input_tokens INTEGER NOT NULL DEFAULT 0, -- provider 返回的缓存命中 token
  usage_source TEXT NOT NULL DEFAULT 'none',         -- usage 来源：provider、none
  usage_parse_status TEXT NOT NULL DEFAULT 'missing',-- usage 解析状态
  usage_parse_error TEXT NOT NULL DEFAULT '',        -- usage 解析错误摘要，最多 512 字节
  FOREIGN KEY (request_id) REFERENCES usage_requests(id) ON DELETE CASCADE
);
```

索引：

```sql
-- idx_usage_tokens_source: 按 usage_source 查询覆盖率
CREATE INDEX IF NOT EXISTS idx_usage_tokens_source ON usage_tokens(usage_source);

-- idx_usage_tokens_parse_status: 按 usage_parse_status 聚合无 usage 原因
CREATE INDEX IF NOT EXISTS idx_usage_tokens_parse_status ON usage_tokens(usage_parse_status);
```

每条 `usage_requests` 必须对应写入一条 `usage_tokens`。即使 provider 没返回 usage，也要写入 `usage_source=none` 和对应 `usage_parse_status`，避免覆盖率聚合时因为 join 漏掉无 usage 请求。

---

## 11. 管理 API

新增只读统计 API：

| 方法 | 路径 | 说明 |
|--------|------|------|
| `GET` | `/api/usage/summary` | 获取服务总请求数、provider 请求总数、今日 provider 请求数、token 消耗总量、今日 token 消耗、失败数、usage 覆盖率 |
| `GET` | `/api/usage/trends` | 获取按时间聚合的 token、请求数、失败数、usage 覆盖率趋势 |
| `GET` | `/api/usage/requests` | 获取请求日志分页 |
| `GET` | `/api/usage/providers` | 按 provider 聚合 |
| `GET` | `/api/usage/models` | 按模型聚合 |
| `GET` | `/api/usage/coverage` | 按 provider/API 地址/模型聚合 usage 覆盖率和无 usage 原因 |

查询参数：

| 参数 | 说明 |
|------|------|
| `from` / `to` | 时间范围，RFC3339 或日期 |
| `source_app` | `all`、`claude_code`、`unknown` |
| `source_entrypoint` | `all`、`cli`、`claude-vscode`、`unknown` 或其他已观察到的入口 |
| `provider_id` | provider 过滤 |
| `model` | 模型过滤 |
| `status` | `all`、`success`、`error` |
| `usage_source` | `all`、`provider`、`none` |
| `usage_parse_status` | `all` 或指定解析状态 |
| `request_path` | 请求路径过滤，默认 `all` |
| `q` | 搜索 provider 名称、API URL、模型、请求 ID、错误摘要 |
| `tz` | IANA 时区，例如 `Asia/Shanghai`；用于今日统计和按天聚合 |
| `page` / `page_size` | 请求日志分页 |

`tz` 默认使用服务端本地时区。前端应传入浏览器时区，确保“今日请求数”和“今日 token 消耗”符合用户所在时区。

状态页可以复用 `/api/usage/summary` 的轻量摘要，也可以由 `/api/status` 内部组合返回。无论采用哪种接口形式，状态页显示的数据定义必须与使用统计页一致。

---

## 12. 前端设计

### 12.1 顶级页面结构

在现有仪表盘增加 `usage` 页签：

```text
状态 / Providers / 证书 / 使用统计
```

统计页签使用更宽容器：

```text
max-w-[1440px]
```

所有页签统一使用 `max-w-[1440px]`，保持布局一致性。

### 12.2 状态页签摘要

状态页签保持轻量，不承载完整统计分析。建议展示：

1. 服务状态。
2. 运行时间。
3. 服务总请求数 `service_requests_total`，沿用现有中间件口径，包含健康检查和本地硬编码端点。
4. Provider 请求总数 `provider_requests_total`，只统计实际转发到 provider 的 messages 请求。
5. 今日 Provider 请求数。
6. 今日 token 消耗，注明“仅含 provider 返回 usage 的请求”。
7. Usage 覆盖率。
8. 当前 Provider。
9. 最近一次 provider 请求时间。

状态页不展示成本、模型排行、完整 provider 表格、请求日志或覆盖率详情。

### 12.3 使用统计内部结构

使用统计页内部使用二级页签：

```text
概览 / 请求日志 / Provider / 模型 / Usage 覆盖率
```

首版包含：

1. 顶部过滤：Claude Code 入口、provider、模型、状态、usage 来源、时间范围、搜索。
2. 摘要卡片：Provider 请求总数、失败数、token 消耗总量、缓存 token、usage 覆盖率。
3. 趋势图：输入、输出、缓存创建、缓存命中、请求数、失败数、usage 覆盖率。
4. 请求日志表：时间、provider、模型、入口、usage 来源、usage 状态、耗时（总耗时、响应头、首字节）、状态码、token 明细（总量、input、output、cache creation、cache read）。默认 10 条/页，可选 20、50、100 条/页。
5. Provider 统计：按 provider 聚合请求数、失败数、token、覆盖率、平均耗时。
6. 模型统计：按模型聚合请求数、失败数、token、覆盖率、平均耗时。
7. Usage 覆盖率：按 provider/API 地址/模型展示无 usage 请求数、覆盖率、主要原因、最近出现时间。

### 12.4 请求日志分页

请求日志表底部右对齐展示分页控件：

1. 每页条数选择器：默认 10 条/页，可选 20、50、100 条/页。
2. 页码导航：首页、上一页、当前页/总页数、下一页、末页。
3. 切换每页条数或修改筛选条件时自动重置到第 1 页。
4. 后端 API `/api/usage/requests` 已支持 `page` 和 `page_size` 参数。

### 12.5 辅助组件

1. `UsageCoverageHelp.vue`：在 usage 覆盖率标签旁显示帮助提示，解释覆盖率含义。
2. `formatters.ts`：格式化工具函数，包含 `formatPercent` 等。
3. 图表库使用 ECharts（见下节）。

### 12.6 图表库

当前前端依赖只有 Vue、Vue Router、Tailwind 和 lucide，没有图表库。

首版直接使用 `echarts`。

原因：

1. 趋势图需要同时展示 token、请求数、失败数、usage 覆盖率等多条序列，ECharts 的 tooltip、legend、坐标轴和响应式能力更完整。
2. 后续如果增加 provider/model 对比、堆叠图或更多交互，不需要重新替换图表库。
3. 虽然 ECharts 体积更大，但管理后台不是首屏营销页，首版可以接受体积换取成熟交互和维护成本更低。

---

## 13. 数据聚合口径

Token 消耗聚合只使用 `usage_source=provider` 的记录。

```text
token_consumption_total =
  input_tokens +
  output_tokens +
  cache_creation_input_tokens +
  cache_read_input_tokens
```

`token_consumption_total` 在前端展示为“token 消耗总量”。它表示按 provider 返回的 usage 分桶相加后的消耗量，不包含 `usage_source=none` 的请求，也不是本地估算值。

请求成功率按请求维度计算：

```text
success_requests = status_code >= 200 && status_code < 300 && error_type == ''
error_requests = error_type != '' || status_code < 200 || status_code >= 300
```

其中 `status_code` 为空且 `error_type` 属于 `network_error`、`upstream_timeout`、`client_aborted` 时，计入 `error_requests`。

Usage 覆盖率按请求维度计算：

```text
with_usage_requests = usage_source == 'provider'
without_usage_requests = usage_source == 'none'
usage_coverage = with_usage_requests / total_provider_requests
```

注意：

1. 成功请求但无 usage 不算请求失败。
2. 网络错误天然无 usage，计入 `usage_source=none` 和 `usage_parse_status=network_error`。
3. 4xx/5xx 请求计入请求失败，并计入 `usage_source=none` 和 `usage_parse_status=skipped_non_2xx`。
4. 趋势图中的 token 消耗只展示 provider usage，不填充估算值。
5. 今日统计和按天聚合使用 API 参数 `tz` 指定的时区；未指定时使用服务端本地时区。
6. 搜索参数 `q` 只匹配短文本字段，不搜索完整请求体或响应体。

---

## 14. 数据保留

首版增加一个保守默认：

```text
usage_retention_days = 90
```

位置：

1. 写入 `settings` 表。
2. 默认 90 天。
3. 首版不在启动时主动清理，后续再通过 admin API 或后台任务执行清理。

首版不做自动删除，避免用户刚上线就丢历史数据。只在规格中预留字段。

---

## 15. 测试计划

### 15.1 采集测试

1. 非流式 2xx JSON 响应带 `usage`，断言写入 provider usage，`usage_parse_status=ok`。
2. 非流式 2xx JSON 响应不带 `usage`，断言 `usage_source=none`、`usage_parse_status=missing`，请求仍为成功。
3. 流式 SSE 响应在 `message_start` / `message_delta` 返回 usage，断言最终 token 正确。
4. 流式 SSE 响应中 usage 分多次出现，断言 input/cache/output 字段按字段合并，不被最后一次局部 usage 覆盖丢失。
5. 流式 SSE 响应不带 usage，断言 `usage_parse_status=missing` 或 `unsupported_format`。
6. provider 4xx/5xx 响应，断言记录 status/error，`usage_parse_status=skipped_non_2xx`，且原始响应完整转发。
7. 网络错误，断言记录 `network_error`，`usage_parse_status=network_error`。
8. 观察器解析失败不影响响应体转发。
9. 每条 `usage_requests` 都对应一条 `usage_tokens`，包括 `usage_source=none` 的请求。

### 15.2 Session 补账测试

1. session 日志文件包含完整 usage 信息时，能匹配并更新已有请求的 token 字段。
2. 已有 `usage_source=provider` 的记录不被覆盖。
3. 文件偏移量正确保存，下次同步跳过已处理内容。
4. 无效或损坏的 session 日志行不会中断同步流程。

### 15.3 聚合测试

1. summary 接口聚合服务总请求、provider 请求总数、今日 provider 请求、token 消耗总量、今日 token 消耗、失败数、usage 覆盖率。
2. trends 接口按小时/天聚合 token 消耗、请求数、失败数、usage 覆盖率。
3. requests 接口支持分页、过滤和 `q` 搜索。
4. providers/models 接口聚合请求数、token、失败数、平均耗时和覆盖率。
5. coverage 接口按 provider/API 地址/模型/Claude Code 入口聚合无 usage 原因。
6. 今日统计按 `tz` 参数切换时区后结果正确。

### 15.4 前端验证

1. 状态页签展示轻量统计摘要，不与使用统计页重复。
2. 使用统计页签在宽屏下展示摘要卡片、趋势图和日志表。
3. 小屏下摘要卡片和表格不会破版。
4. 时间范围和过滤条件能正确刷新数据。
5. `usage_source=none` 有明确标记，且可进入 Usage 覆盖率子页签查看聚合原因。
6. 请求日志表底部有分页控件，切换每页条数和翻页正常工作。

---

## 16. 实施顺序

1. SQLite 配置存储改造先落地，确保 `proxy.db` 和迁移机制可用。
2. 增加 usage schema 迁移。
3. 增加 usage 存储层和聚合查询单元测试。
4. 在 proxy 处理器中接入请求生命周期采集。
5. 接入非流式响应 usage 解析。
6. 接入 SSE 观察器，并保留 heartbeat 行为。
7. 增加 admin usage API。
8. 增加状态页轻量统计摘要。
9. 增加前端使用统计页签和 Usage 覆盖率子页签。
10. 使用真实 Claude Code CLI 和 VS Code 扩展请求做手动验证。

---

## 17. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| SSE 解析破坏流式转发 | Claude Code 卡住或丢事件 | 观察器只旁路读取 chunk，写客户端路径保持原样 |
| provider usage 格式不一致 | token 统计为空 | 格式不识别时 `usage_source=none`，通过覆盖率视图暴露问题 |
| usage 解析失败污染请求失败率 | 成功请求被误判失败 | 请求错误和 usage 解析状态分开存储 |
| 图表页过宽影响现有页面 | Provider 配置页体验变差 | 只对 usage 页签使用宽容器 |
| 统计写库拖慢请求 | 增加代理延迟 | 请求结束后小事务写入；写入失败只打日志，不影响响应 |
| 错误摘要泄露敏感内容 | 日志表暴露 provider 返回的敏感片段 | `error_message` 只保存短摘要，避免保存完整 body 或 token |
| Session 补账匹配错误 | 补账写入错误的 token 数据 | 只匹配 `usage_source=none` 的记录，不覆盖已有 provider usage；匹配失败跳过不报错 |

---

## 18. 后续可选能力

以下能力不进入首版：

1. 按 `provider_id + model` 配置价格并计算成本。
2. 对 provider 未返回 usage 的请求做本地 token 估算。
3. 导出 CSV/JSON 报表。
4. 后台自动清理超过保留期的数据。
5. 针对特定 provider 增加非 Anthropic-style usage 适配器。当前 `ParseStatusUnsupportedFormat` 常量已定义但解析逻辑中尚未使用，预留用于未来检测非 Anthropic 格式的响应。
6. 会话记录浏览器；该功能拆分到 `docs/superpowers/specs/2026-05-18-session-browser-design.md`。

---

## 19. 参考

1. Anthropic streaming events and usage: https://platform.claude.com/docs/en/build-with-claude/streaming
2. cc-switch: https://github.com/farion1231/cc-switch
