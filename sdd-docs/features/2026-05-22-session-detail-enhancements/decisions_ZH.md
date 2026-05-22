# 会话详情增强 — 设计决策

**日期**: 2026-05-22

## D1: JSONL 路径 — 显示位置

**决策**: 将 JSONL 文件名放在项目路径和时间戳之间，而非第一行。

**理由**: 会话标题是主要标识符，项目路径提供上下文，JSONL 文件名是辅助元数据——对调试/复制有用但不应主导视觉层次。

## D2: 消息计数 — 两阶段策略

**决策**: 列表扫描使用近似计数（head/tail 采样），仅详情 API 返回准确计数。

**备选方案**:
- 扫描时读取完整文件: 已否决。88 个文件 × 41,685 行总计导致 2 秒以上加载时间。
- 后台预扫描: 已否决。增加复杂度但无法解决首次加载问题。
- 侧栏不显示计数: 已否决。用户期望一眼看到消息密度。

**理由**: 列表视图需要快速（亚秒级）。详情 API 已经解析完整文件（`ParseMessages`），`len(messages)` 无额外开销。选择会话时更新侧栏，恰好在用户关注时提供准确计数。

## D3: 彩色边框 — CSS 变量选择

**决策**: assistant 使用 `--session-accent`（蓝色），technical 消息硬编码 `#f59e0b`（琥珀色）。

**理由**: 导出 HTML 模板已定义这些颜色。使用相同值确保管理面板和导出 HTML 之间的视觉一致性。琥珀色不依赖主题——在 light 和 dark 模式下都提供足够对比度。

## D4: 图标按钮背景

**决策**: 使用 `var(--session-border)` 作为默认背景，而非 `var(--session-surface-muted)`。

**理由**: `--session-surface-muted` 与 dark 主题背景过于接近，按钮不可见。`--session-border` 在 light 下为 `#dbeafe`，dark 下为 `#263449`，两种主题下都有可见对比度且不突兀。

## D5: GitHub 图标 — 内联 SVG

**决策**: 内联 GitHub SVG 路径，而非从 lucide-vue-next 导入。

**理由**: Lucide 不包含品牌图标（GitHub、Twitter 等）。GitHub 标志是一个简单的单路径 SVG。内联避免了增加额外依赖。

## D6: SSE Usage 提取 — 剥离 Accept-Encoding

**决策**: 代理在转发请求到上游 provider 前剥离 `Accept-Encoding` 和 `TE` 头，而非在 SSE 管道中增加 gzip 解压。

**问题**: MiniMax（及其他潜在 provider）在收到 `Accept-Encoding: gzip` 时会压缩 SSE 响应。Go 的 `http.Transport` 在应用层显式设置了 `Accept-Encoding` 时**不会**自动解压——它认为应用自己处理。SSEObserver 收到的是压缩后的二进制数据，无法解析 usage token。

**备选方案**:
- SSE 管道中增加 gzip reader: 已否决。增加复杂度、延迟和边界情况（chunk 边界跨越 gzip 帧）。
- Transport 设置 `DisableCompression: false`: 已否决。仅影响 Transport 自行添加的头——无法覆盖应用层设置的头。
- 包装 `resp.Body` 为 `gzip.NewReader`: 已否决。需要检测 `Content-Encoding` 并同时处理压缩/未压缩响应。

**理由**: 剥离头是从源头阻止问题的单行修复。上游 SSE 响应不会被压缩，SSEObserver 始终收到明文。忽略 `Accept-Encoding` 的其他 provider（Zhipu GLM、Kimi）不受影响。

## D7: 项目名推断 — 同目录有效 cwd 优先 + 目录名兜底

**决策**: `foldSourceProjectSessions` 收集同目录 session 的 `ProjectPath` 时过滤掉 `""` 和 `"Unknown Project"`，仅用有效路径推断。全部无效时，从目录名最后一段提取项目名兜底。

**问题**: 某些 jsonl 文件缺少 `cwd` 字段（如在 `~` 目录启动的会话），导致 `scanSessionFile` 返回 `"Unknown Project"`。旧代码将 "Unknown Project" 与其他有效路径一起传给 `inferProjectRoot`，由于 `isAncestorOfAll` 对 "Unknown Project" 返回 false，整组推断失败——即使同目录有 session 包含正确的 `cwd`。

**备选方案**:
- 尝试从目录名完整解码项目路径: 已否决。路径编码（`/` → `-`）是有损的，项目名若含 `-` 则无法可靠还原。例如 `-home-www-claude-workspace` 可能是 `/home/www/claude/workspace` 或 `/home/www/claude-workspace`。
- 仅过滤无效路径不做兜底: 部分解决。但当目录下所有 session 都缺 cwd 时，仍然显示 "Unknown Project"。
- 从目录名推断完整路径: 已否决。不必要——`projectName()` 只取最后一段作为显示名，完整路径不是必需的。

**理由**: 两层策略——优先信赖数据（同目录有效 cwd），仅在全部缺失时用目录名兜底。目录名兜底虽对有 `-` 的项目名有损（`pm0511-lvshixiehui` → `lvshixiehui`），但仍优于 "Unknown Project"。

## D8: nil slice JSON 序列化 — 后端 + 前端双层防御

**决策**: 后端在 `handleSessionDetail` 和 `handleSessionExport` 中将 nil messages 转为空切片；前端 `SessionOutline.vue` 对 `props.messages` 添加 `|| []` 防御。

**问题**: Go 中 `var msgs []Message` 声明后是 nil slice，`json.Marshal` 输出 `null` 而非 `[]`。TypeScript 类型标注 `SessionMessage[]` 无法反映运行时的 null 可能性。`SessionOutline.vue` 的 computed 属性直接调用 `props.messages.map(...)`，在 messages 为 null 时抛出 `TypeError: Cannot read properties of null (reading 'map')`。

**备选方案**:
- 仅前端修复: 可解决当前错误，但其他可能的消费者仍会踩坑。
- 仅后端修复: 可确保数据完整性，但前端缺少防御层。

**理由**: 双层修复——后端保证数据契约（`"messages"` 始终是数组），前端兜底防止意外的 null。Go 中 `json.Marshal([]Message(nil))` → `null` 是经典陷阱，值得在 handler 层显式防范。**根因**在前端 `SessionOutline.vue:28` 的 `.map()` 调用，后端修复是数据完整性保障。
