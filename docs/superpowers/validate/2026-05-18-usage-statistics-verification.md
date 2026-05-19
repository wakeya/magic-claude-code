# 使用统计验证清单

**版本**: 0.2
**日期**: 2026-05-19
**规格**: [2026-05-15-usage-statistics-design.md](../specs/2026-05-15-usage-statistics-design.md)
**计划**: [2026-05-18-usage-statistics.md](../plans/2026-05-18-usage-statistics.md)

---

## 验证方法

实施完成后逐项检查，每项通过后标记 `[x]`。本验证只覆盖 usage 使用统计首版，不覆盖会话记录浏览器。

---

## 1. 范围控制

- [ ] 没有实现本地 token 估算
- [ ] 没有实现成本估算
- [ ] 没有实现模型价格配置
- [ ] 没有实现倍率配置
- [ ] 没有实现会话记录浏览器
- [ ] 没有统计本地硬编码端点为 provider 请求
- [ ] 只统计实际转发到 provider 的 messages 请求

## 2. SQLite Schema 设计

- [ ] `usage_requests` 表存在
- [ ] `usage_tokens` 表存在
- [ ] `usage_requests` 包含 `upstream_response_header_ms`
- [ ] `usage_requests` 包含 `time_to_first_byte_ms`
- [ ] `usage_requests` 包含 `method`
- [ ] `usage_requests` 包含 `request_path`
- [ ] `usage_requests` 包含 `backend_url`
- [ ] `usage_requests` 包含 `source_entrypoint`
- [ ] `usage_tokens` 包含 `usage_source`
- [ ] `usage_tokens` 包含 `usage_parse_status`
- [ ] `usage_tokens` 包含 `usage_parse_error`
- [ ] `usage_source` 支持 `provider`、`session_log`、`none`
- [ ] `settings` 表包含默认 `usage_retention_days=90`
- [ ] 规格中列出的所有 usage 索引存在
- [ ] 每条 `usage_requests` 都对应一条 `usage_tokens`

## 3. 请求采集

- [ ] `/v1/messages` 请求会写入 `usage_requests`
- [ ] `/anthropic/v1/messages` 请求会写入 `usage_requests`
- [ ] 本地硬编码 OAuth/settings/quota/mock 请求不会写入 `usage_requests`
- [ ] `provider_id/provider_name/provider_api_url` 保存请求开始时的快照
- [ ] `original_model` 保存请求体原始模型
- [ ] `mapped_model` 保存模型映射后的模型
- [ ] `request_bytes` 保存转发请求体大小
- [ ] `response_bytes` 保存 provider 响应体字节数，不包含本地注入心跳
- [ ] `backend_url` 已脱敏
- [ ] `user_agent` 最多 512 字节
- [ ] `error_message` 最多 1024 字节且已脱敏
- [ ] `usage_parse_error` 最多 512 字节且已脱敏

## 4. 来源识别

- [ ] 能从 `x-anthropic-billing-header` 解析 `cc_entrypoint=cli`
- [ ] 能从 `x-anthropic-billing-header` 解析 `cc_entrypoint=claude-vscode`
- [ ] 能识别 Claude Code 时 `source_app=claude_code`
- [ ] 无法识别时 `source_app=unknown`
- [ ] 无法识别时仍保存脱敏后的 `user_agent`
- [ ] `/api/usage/requests?source_entrypoint=cli` 只返回 CLI 请求
- [ ] `/api/usage/requests?source_entrypoint=claude-vscode` 只返回 VS Code 扩展请求

## 5. Usage 解析

- [ ] 非流式 2xx JSON 顶层 `usage` 写入 `usage_source=provider`
- [ ] 非流式 2xx 无 `usage` 写入 `usage_source=none`
- [ ] 非流式 2xx 无 `usage` 写入 `usage_parse_status=missing`
- [ ] 非流式 2xx 不支持格式写入 `usage_parse_status=unsupported_format`
- [ ] 非流式 JSON 解析失败写入 `usage_parse_status=parse_error`
- [ ] provider 4xx/5xx 写入 `usage_parse_status=skipped_non_2xx`
- [ ] 网络错误写入 `usage_parse_status=network_error`
- [ ] SSE `message_start` usage 可被提取
- [ ] SSE `message_delta` usage 可被提取
- [ ] SSE 多次 usage 按字段合并，不丢失 input/cache/output
- [ ] 本地注入 `ping` 不参与 usage 解析
- [ ] 本地注入 `ping` 不参与 `response_bytes`

## 5A. Session Usage 补账

- [ ] `session_log_sync` 表存在于 `proxy.db`
- [ ] 后台 goroutine 每分钟扫描 `$CLAUDE_PROJECTS_DIR` 下的 `.jsonl` 文件
- [ ] session 日志中的 usage 以独立 `session:<message.id>` 请求记录导入
- [ ] 补账成功后记录的 `usage_source=session_log`
- [ ] Session Log 记录的 `provider_id=_session`
- [ ] Session Log 记录的 `source_entrypoint=session_log`
- [ ] 已有 `usage_source=provider` 的记录不被补账覆盖
- [ ] 已有 `usage_source=provider` 的记录不被补账删除
- [ ] 文件偏移量正确保存，下次同步跳过已处理内容
- [ ] 无效或损坏的 JSONL 行不会中断同步流程
- [ ] provider/session_log 四项 token 相同、模型相同、时间接近时，默认有效统计只计入 provider
- [ ] `stats_scope=session_log` 能看到重复 Session Log 记录
- [ ] `stats_scope=raw` 能看到全部原始记录

## 6. 转发安全

- [ ] usage 观察器不改变 provider 请求体
- [ ] usage 观察器不改变 provider 响应体
- [ ] 4xx/5xx provider 原始响应完整转发给客户端
- [ ] 非流式响应解析失败不影响客户端收到响应
- [ ] SSE 响应解析失败不影响客户端收到流式响应
- [ ] 客户端断开时记录 `client_aborted`，且不导致进程崩溃
- [ ] usage 写库失败只打日志，不影响响应转发

## 7. 数据聚合口径

- [ ] token 消耗总量默认使用 `stats_scope=effective`
- [ ] `stats_scope=effective` 统计 provider 记录和非重复 Session Log 记录
- [ ] `stats_scope=effective` 排除重复 Session Log 记录
- [ ] `stats_scope=provider` 只统计 `usage_source=provider`
- [ ] `stats_scope=session_log` 只统计 `usage_source=session_log`
- [ ] `stats_scope=raw` 不做去重，展示全部原始记录
- [ ] `token_consumption_total = input + output + cache_creation + cache_read`
- [ ] `usage_source=none` 请求不参与 token 消耗总量
- [ ] 成功请求但无 usage 不计入请求失败
- [ ] 4xx/5xx 计入请求失败
- [ ] 网络错误计入请求失败
- [ ] Provider Usage 覆盖率只衡量 provider 响应是否返回 usage
- [ ] 有效 Usage 覆盖率计入非重复 Session Log 补账
- [ ] 今日统计使用 API `tz` 参数
- [ ] 未传 `tz` 时使用服务端本地时区

## 8. 管理 API

- [ ] `GET /api/usage/summary` 返回服务总请求数
- [ ] `GET /api/usage/summary` 返回 provider 请求总数
- [ ] `GET /api/usage/summary` 返回今日 provider 请求数
- [ ] `GET /api/usage/summary` 返回 token 消耗总量
- [ ] `GET /api/usage/summary` 返回今日 token 消耗
- [ ] `GET /api/usage/summary` 返回 usage 覆盖率
- [ ] `GET /api/usage/trends` 支持按小时聚合
- [ ] `GET /api/usage/trends` 支持按天聚合
- [ ] `GET /api/usage/requests` 支持分页
- [ ] `GET /api/usage/requests` 支持 `q` 搜索
- [ ] `GET /api/usage/requests` 支持 `source_entrypoint` 过滤
- [ ] `GET /api/usage/requests` 支持 `usage_source` 过滤
- [ ] `GET /api/usage/requests` 支持 `stats_scope=effective`
- [ ] `GET /api/usage/requests` 支持 `stats_scope=provider`
- [ ] `GET /api/usage/requests` 支持 `stats_scope=session_log`
- [ ] `GET /api/usage/requests` 支持 `stats_scope=raw`
- [ ] 无效 `stats_scope` 返回明确 400 错误
- [ ] `GET /api/usage/providers` 返回 provider 聚合
- [ ] `GET /api/usage/models` 返回模型聚合
- [ ] `GET /api/usage/coverage` 返回 provider/API 地址/模型/Claude Code 入口覆盖率
- [ ] `GET /api/usage/coverage` 同时保留 Provider Usage 覆盖率和有效 Usage 覆盖率
- [ ] 所有 `/api/usage/*` 接口需要登录 session
- [ ] 无效 `tz` 返回明确 400 错误

## 9. 状态页

- [ ] 状态页显示服务状态
- [ ] 状态页显示运行时间
- [ ] 状态页显示 `service_requests_total`
- [ ] 状态页"总请求数"旁有问号提示，说明其含义
- [ ] 状态页显示 `provider_requests_total`
- [ ] 状态页显示今日 provider 请求数
- [ ] 状态页显示今日 token 消耗
- [ ] 状态页显示 usage 覆盖率
- [ ] 状态页显示当前 provider
- [ ] 状态页显示最近一次 provider 请求时间
- [ ] 状态页不显示完整请求日志
- [ ] 状态页不显示成本

## 10. 使用统计页面

- [ ] 顶级页签包含“使用统计”
- [ ] 所有页签统一使用 `max-w-[1440px]` 容器宽度
- [ ] 使用统计内部包含“概览 / 请求日志 / Provider / 模型 / Usage 覆盖率”
- [ ] 顶部过滤包含 Claude Code 入口
- [ ] 顶部过滤包含 provider
- [ ] 顶部过滤包含模型
- [ ] 顶部过滤包含状态
- [ ] 顶部过滤包含 usage 来源
- [ ] 顶部过滤或分段控件包含统计口径：有效统计 / 实时请求 / Session Log / 全部原始
- [ ] 顶部过滤包含时间范围
- [ ] 顶部过滤包含搜索
- [ ] 摘要卡片显示 Provider 请求总数
- [ ] 摘要卡片显示失败数
- [ ] 摘要卡片显示 token 消耗总量
- [ ] 摘要卡片显示缓存 token
- [ ] 摘要卡片显示 usage 覆盖率
- [ ] 请求日志表显示 usage 状态
- [ ] 请求日志表显示 `usage_source=session_log`
- [ ] 请求日志表对重复 Session Log 行显示“与实时请求重复”或等价标记
- [ ] Usage 覆盖率表显示无 usage 原因
- [ ] Usage 覆盖率表保留 Provider Usage 覆盖率
- [ ] 概览 usage 覆盖率问号提示说明 Session Log 补账计入有效覆盖率
- [ ] 请求日志表底部有分页控件
- [ ] 分页控件左侧显示总条数（如"共 3,446 条"）
- [ ] 分页默认 10 条/页
- [ ] 分页可选 20、50、100 条/页
- [ ] 切换每页条数时自动重置到第 1 页
- [ ] 修改筛选条件时自动重置到第 1 页
- [ ] 翻页后请求日志数据正确更新

## 11. ECharts

- [ ] `internal/frontend/package.json` 包含 `echarts`
- [ ] 趋势图使用 ECharts 渲染
- [ ] 趋势图包含输入 token 序列
- [ ] 趋势图包含输出 token 序列
- [ ] 趋势图包含缓存创建 token 序列
- [ ] 趋势图包含缓存命中 token 序列
- [ ] 趋势图包含请求数序列
- [ ] 趋势图包含失败数序列
- [ ] 趋势图包含 usage 覆盖率序列
- [ ] 切换页签或卸载页面时 ECharts 实例被释放
- [ ] 小屏下图表和表格不破版

## 12. 自动化测试

- [ ] `env GOCACHE=/tmp/go-build go test ./internal/usage -count=1` 通过
- [ ] `env GOCACHE=/tmp/go-build go test ./internal/proxy -count=1` 通过
- [ ] `env GOCACHE=/tmp/go-build go test ./internal/admin -count=1` 通过
- [ ] `env GOCACHE=/tmp/go-build go test ./... -count=1` 通过
- [ ] `cd internal/frontend && npm run build` 通过
- [ ] 新增测试覆盖 usage schema 迁移
- [ ] 新增测试覆盖 usage parser
- [ ] 新增测试覆盖 SSE 观察器
- [ ] 新增测试覆盖 proxy recording
- [ ] 新增测试覆盖 usage API filters
- [ ] 新增测试覆盖 timezone aggregation
- [ ] 新增测试覆盖 session 补账同步

## 13. 容器与手动验证

- [ ] `docker compose up -d --build` 成功
- [ ] 容器启动日志无 usage schema 初始化错误
- [ ] `docker-compose.yml` 包含 `CLAUDE_PROJECTS_DIR` 挂载（只读）
- [ ] Windows 部署文档说明需要显式设置 `CLAUDE_PROJECTS_DIR`
- [ ] 宿主机 `data/proxy.db` 包含 usage 表
- [ ] 真实 Claude Code CLI 请求成功
- [ ] 真实 Claude Code VS Code 扩展请求成功
- [ ] usage 请求日志中能看到 CLI 请求
- [ ] usage 请求日志中能看到 VS Code 扩展请求
- [ ] 覆盖率页面能显示 provider/API 地址/模型覆盖率
- [ ] provider 返回 usage 时 token 消耗总量增加
- [ ] provider 未返回 usage 时无 usage 请求数增加
- [ ] session 补账能从宿主机 Claude session 日志补充 usage 数据
- [ ] 补账后的请求 `usage_source` 显示为 `session_log`
- [ ] 默认有效统计不把同一条 provider/session_log 重复相加
- [ ] Session Log 视图能单独列出补账记录

## 14. 最终确认

- [ ] `git status --short` 中没有意外文件
- [ ] `git diff --stat` 符合计划的文件范围
- [ ] 所有新增 Go 文件已 `gofmt`
- [ ] 所有新增前端文件已通过构建
- [ ] `UsageCoverageHelp.vue` 组件已包含
- [ ] `formatters.ts` 工具已包含
- [ ] 请求日志分页控件已包含，分页控件左侧显示总条数
- [ ] 所有新增文档链接有效
- [ ] usage 规格、计划、验证清单三者范围一致
