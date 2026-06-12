# Windows 使用统计可靠性规格

本地页面: `/` 服务状态、`/` 使用统计 | 运行目标: Windows amd64 二进制
技术栈: Go 1.26 + SQLite + Vue 3 + 内嵌前端 | 最后更新: 2026-06-12
进度: 5 / 5 已完成

---

## 问题分析

### 现象

Windows 上代理可以正常处理 Claude Code 模型请求，并且 `data/proxy.db` 中已经有 usage 数据，但管理后台仍显示统计为空：

| 页面/位置 | 观察结果 |
|-----------|----------|
| 服务状态页 | Provider 请求总数、今日 Provider 请求数、今日 Token 消耗、Usage 覆盖率、最近一次 Provider 请求显示 0 或空 |
| 使用统计页 | 选择“今天”和请求日志均无数据 |
| 命令行日志 | `POST /v1/messages` 已进入代理并返回 HTTP 200 |
| SQLite 数据库 | `usage_requests` 和 `usage_tokens` 中存在 provider 与 session_log 记录 |

### 根因

1. **Windows 时区查询失败**
   - 前端会把浏览器时区作为 `tz` 参数传给后端，例如 `Asia/Shanghai`。
   - 后端在统计汇总和筛选时调用 `time.LoadLocation(tz)`。
   - Linux 通常有系统 zoneinfo 数据，Windows 独立二进制不一定能找到 IANA 时区库。
   - 时区加载失败后，Usage API 返回错误；服务状态汇总会吞掉错误并显示 0。

2. **SSE 统计写入依赖上游 EOF**
   - 部分上游 SSE 响应会发出 `message_stop`，但 HTTP 连接不立即关闭。
   - 旧逻辑必须等 `io.EOF` 才会调用 `finishUsageRecord`。
   - 在 Windows 与该上游组合下，统计落库会延迟或不可见。

3. **终止 SSE 事件也可能携带最终 usage**
   - 部分兼容 provider 可能把最终 usage 放在 `message_stop` payload 中。
   - parser 必须先合并终止事件中的 usage，再标记流完成。

### 证据

Windows 日志特征：

```text
[id] >>> POST /v1/messages model=claude-opus-4-6 -> mimo-v2.5-pro stream=true ...
[id] <<< 200 model=claude-opus-4-6 -> mimo-v2.5-pro upstream=...
[Stream] SSE stream detected ..., enabling heartbeat injection
```

SQLite 证据：

```text
usage_requests: 33 rows
usage_tokens: 33 rows
2026-06-12T03:*Z 存在 provider 记录，usage_source=provider，usage_parse_status=ok
```

---

## 需求

### R1. Windows 二进制必须支持浏览器 IANA 时区

**目标** - Windows 独立构建必须支持 `time.LoadLocation("Asia/Shanghai")` 等浏览器返回的 IANA 时区 ID。

**结果** - Windows 上状态页和 Usage API 可以正确计算本地日期范围与图表日期分桶，不依赖系统 zoneinfo 文件。

**实现** - 引入 Go 内嵌时区数据库：

```go
_ "time/tzdata"
```

**验收标准**

- `GET /api/status?tz=Asia/Shanghai` 在存在匹配记录时返回非 0 统计。
- `GET /api/usage/summary?tz=Asia/Shanghai&from=...&to=...` 不因缺少时区数据失败。

### R2. SSE usage 写入不能只依赖 EOF

**目标** - 收到终止 SSE 事件后，即使上游连接仍保持打开，也应完成本次 usage 记录。

**终止事件**

| 事件 | 完成信号 |
|------|----------|
| `event: message_stop` | 完成 |
| `data: {"type":"message_stop"}` | 完成 |
| `data: [DONE]` | 完成 |

**结果** - 收到终止事件后可以走到 `finishUsageRecord`。

**验收标准**

- 测试后端写入 `message_stop`、flush 后不关闭连接时，代理仍写入一条 usage 记录。
- 下游响应在代理停止复制前已经包含终止 SSE 数据。

### R3. 终止 SSE 事件中的 usage 必须先合并再完成

**目标** - 如果兼容 provider 在 `message_stop` 事件中携带 usage，parser 不能丢弃。

**结果** - 终止事件 payload 会先解析并合并 usage，再设置 `complete=true`。

**验收标准**

- `event: message_stop` 携带 `{"usage":{"output_tokens":9}}` 时，结果为 `UsageSourceProvider`、`ParseStatusOK`、`OutputTokens=9`。

### R4. 保持既有日期快捷筛选语义

**目标** - 本次修复不改变 Usage 页日期快捷筛选行为。

**结果** - 维持原有语义：

| 快捷项 | 范围 |
|--------|------|
| 今日 | 浏览器本地时间今日 00:00:00 到 23:59:59 |
| 近 7 天 | 过去 7 个完整日，不包含今天 |
| 近 30 天 | 过去 30 个完整日，不包含今天 |

### R5. Windows 二进制包保持自包含

**目标** - Windows 二进制必须包含后端修复和当前内嵌前端资源。

**验收标准**

- 后端和前端资源变更后重新构建 `bin/mcc-windows-amd64/mcc.exe`。
- 二进制中包含前端入口 `index-Dxc_BCfC.js`。
- 二进制中包含 Go `time/tzdata` 符号。

---

## 实现摘要

| 区域 | 文件 | 变更 |
|------|------|------|
| 时区数据 | `cmd/server/main.go` | 增加 `_ "time/tzdata"` 空导入 |
| SSE parser | `internal/usage/sse.go` | 增加完成状态，终止事件先合并 usage 再完成 |
| SSE 复制循环 | `internal/proxy/heartbeat.go` | observer 完成后停止复制 |
| 流式 observer | `internal/proxy/handler.go` | 暴露 `IsComplete()` |
| 回归测试 | `internal/usage/sse_test.go` | 覆盖 `[DONE]`、`message_stop`、终止事件 usage 合并 |
| 回归测试 | `internal/proxy/heartbeat_test.go` | 覆盖 observer 完成后复制循环返回 |
| 回归测试 | `internal/proxy/server_test.go` | 覆盖上游发出 `message_stop` 后不关闭连接 |

---

## 验证

### 自动化验证

| 命令 | 期望结果 |
|------|----------|
| `go test ./...` | 328 个测试通过 |
| `npm --prefix internal/frontend test` | 45 个测试通过 |
| `git diff --check` | 无空白或补丁格式问题 |

### Windows 手动验证

1. 启动重新构建的 Windows 二进制：

```powershell
.\mcc.exe -password admin
```

2. 通过代理发送一次 Claude Code 请求。

3. 确认控制台日志包含：

```text
>>> POST /v1/messages
<<< 200
[Stream] SSE stream detected
```

4. 打开管理后台验证：

| 页面 | 期望 |
|------|------|
| 服务状态 | Provider 计数和最近一次 Provider 请求有数据 |
| 使用统计 / 今日 | 可以看到 Provider 行和 token 汇总 |
| 使用统计 / 请求日志 | 可以看到 `/v1/messages` 记录 |

---

## 风险与边界

| 风险 | 处理 |
|------|------|
| 嵌入 tzdata 增加二进制体积 | Windows 自包含运行需要，接受该权衡 |
| 非终止事件在顶层误带 `"type":"message_stop"` | 只检查顶层 `type`，符合 Anthropic SSE 事件约定 |
| 终止事件前出现 malformed JSON | 沿用现有 parse_error 行为 |
| 上游既没有 usage 也没有终止事件 | 保留 EOF fallback 行为 |

---

## 完成状态

| # | 任务 | 状态 |
|---|------|------|
| 1 | 使用真实 `proxy.db` 诊断 Windows 页面为空 | 已完成 |
| 2 | 嵌入 tzdata 支持 Windows IANA 时区 | 已完成 |
| 3 | SSE 复制循环支持终止事件完成 | 已完成 |
| 4 | 终止事件 usage 先合并再完成 | 已完成 |
| 5 | 重建 Windows 二进制并完成测试 | 已完成 |
