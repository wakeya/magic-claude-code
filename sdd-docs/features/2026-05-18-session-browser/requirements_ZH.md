# Claude Code 会话记录浏览器需求文档

**版本：** 1.1
**日期：** 2026-05-21
**状态：** draft
**替代：** v0.2（基于代理拦截的方案，已归档）

---

## 1. 目标

在管理面板中新增会话记录浏览器标签页，读取本地 Claude Code 会话文件，按项目目录组织会话，并支持将任意会话导出为自包含的 HTML 文件。

浏览器应包含：

1. 左侧面板：顶部提供项目下拉选择器，下面展示当前项目的会话列表。
2. 右侧面板：展示所选会话的完整对话内容。
3. 用户消息大纲：点击大纲项可滚动定位到对应消息。
4. 导出按钮：下载会话为单个 HTML 文件。

## 2. 成功标准

功能实现成功的标志：

1. 会话从 `~/.claude/projects/` 的 JSONL 文件中读取，使用原生 sessionId。
2. 项目目录是会话列表的一级筛选维度。
3. 会话详情展示完整对话，系统和工具内容可折叠。
4. 任意会话可导出为离线可打开的自包含 HTML 文件，且导出文件中用户消息整块使用醒目的护眼浅绿色背景。
5. 管理界面不删除 JSONL 文件；如需清理会话或项目状态，仅展示 Claude Code CLI 删除/清理命令提示。
6. 左侧会话列表在项目切换后刷新并独立滚动，右侧详情保持为当前选中会话。

## 3. 数据源

### 3.1 目录结构

Claude Code 在 `~/.claude/projects/` 下存储会话：

```text
~/.claude/projects/
  <编码路径>/
    <session-id>.jsonl        （对话记录）
    <session-id>/             （附属目录：subagents/、tool-results/）
```

每个项目对应一个子目录，子目录名编码了项目路径。每个 `.jsonl` 文件为一个会话。以 `agent-` 开头的文件为 agent 子会话，v1 中跳过。

### 3.2 JSONL 行格式

每个 `.jsonl` 文件每行一个 JSON 对象，相关行类型：

| 类型 | 示例 |
|------|------|
| 元数据 | `{"sessionId":"abc","cwd":"/path/to/project","timestamp":"2026-05-20T10:00:00Z"}` |
| 用户消息 | `{"type":"user","message":{"role":"user","content":"hello"},"timestamp":"..."}` |
| 助手消息 | `{"type":"assistant","message":{"role":"assistant","content":[...]},"timestamp":"..."}` |
| 自定义标题 | `{"type":"custom-title","customTitle":"fix-login-bug"}` |
| 元信息跳过 | `{"isMeta":true,...}` |

内容可以是纯字符串或内容块数组：

```json
"text"
[{"type":"text","text":"..."},{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]
[{"type":"tool_result","tool_use_id":"toolu_1","content":"文件内容"}]
```

### 3.3 元数据提取（列表用途）

对于会话列表，仅读取每个 JSONL 文件的前 10 行和后 30 行：

| 字段 | 来源 | 降级方案 |
|------|------|----------|
| `sessionId` | 元数据行 `sessionId` | 文件名主干 |
| `cwd` | 元数据行 `cwd` | `"Unknown Project"` |
| `title` | 扫描窗口中最后一个非空 `custom-title` > 首条用户消息（跳过 caveat/命令/本地命令输出） > 目录名 > ID 前缀 | sessionId 前缀 |
| `createdAt` | 首个 `timestamp` | 文件修改时间 |
| `lastActiveAt` | 最后一个非 meta `timestamp` | 文件修改时间 |
| `messageCount` | 非元信息、非摘要行计数 | 0 |

### 3.4 完整消息解析（详情用途）

打开具体会话时，解析整个 JSONL 文件为有序消息列表。

## 4. 项目目录映射

项目优先通过 Claude projects 下的同一源目录和会话元数据中的 `cwd` 共同标识。单个 JSONL 会话中可能因为 Claude Code 在子目录执行命令而出现多个 `cwd`；当这些 `cwd` 存在祖先/子路径关系时，该会话应保留祖先路径作为项目路径。若同一源目录下存在项目根目录会话和其子目录会话，也应将子目录会话归并到项目根目录，避免 `/repo/internal/frontend` 被展示为独立的 `frontend` 项目。

| 字段 | 值 |
|------|-----|
| `name` | `basename(projectPath)` |
| `path` | 规范化后的项目根路径；无法推断根路径时使用完整 `cwd` |
| `sessionCount` | 归并到此项目路径的会话数量 |
| `lastActiveAt` | 该项目下所有会话中最近的 `lastActiveAt` |

项目按 `lastActiveAt DESC` 排序。项目内的会话同样按 `lastActiveAt DESC` 排序。

跨平台路径规范化：

1. 分组前使用 `filepath.Clean(cwd)` 规范化路径分隔符。
2. 单个 JSONL 文件内，收集扫描窗口中所有非空 `cwd`。如果某个 `cwd` 是其他所有扫描到的 `cwd` 的祖先路径，则使用该祖先路径作为该会话的 `projectPath`，而不是简单使用最后出现的 `cwd`。
3. 同一 Claude projects 源目录下，如果某个会话 `projectPath` 是其他会话 `projectPath` 的祖先路径，则使用该祖先路径作为该源目录的项目根路径。
4. 祖先路径判断必须使用一致的可比较路径表示：将 `\` 统一为 `/`，按 slash 风格路径语义清理，对 Windows 盘符路径进行大小写不敏感比较，并按路径段比较而不是按字符串前缀比较。`/work/project-a` 不得误匹配 `/work/project-api`。
5. 在大小写不敏感的文件系统（Windows）上，对项目路径应用 `strings.ToLower` 进行去重。
6. 使用 `filepath.Base(projectPath)` 获取项目名称（同时兼容 `/` 和 `\` 分隔符）。

## 5. 会话模型

| 字段 | 来源 | 展示 |
|------|------|------|
| ID | `sessionId` | 前 8 位作为短 ID |
| 标题 | custom-title / 首条用户消息 / 目录名 / ID 前缀 | 会话卡片标题 |
| 项目 | `cwd` | 项目标签 |
| 创建时间 | 首个时间戳 | 相对时间 |
| 最后活跃 | 最后时间戳 | 相对时间 |
| 消息数 | 非元信息行计数 | 徽标 |

## 6. 消息模型

| 角色 | 检测规则 | 展示方式 |
|------|---------|---------|
| `system` | 首条元数据/系统内容 | 默认折叠 |
| `user` | `message.role=user` 且含非 tool_result 内容 | 整块护眼浅绿色高亮，纳入大纲 |
| `assistant` | `message.role=assistant` | 文本 + 工具使用块 |
| `tool` | `message.role=user` 且所有内容块均为 `tool_result` | 工具名称 + 结果摘要 |

内容块处理：

| 块类型 | 展示方式 |
|--------|---------|
| `text` | 原样渲染 |
| `tool_use` | `[Tool: {name}]` 带可展开的输入详情 |
| `tool_result` | 摘要，可展开查看详情 |
| `image` / 二进制 | 仅显示类型和大小 |

包含 `<local-command-caveat>`、以 `<command-name>` 开头，或包含本地命令 stdout/stderr 包裹标签的用户消息应展示，但从标题候选中排除，在大纲中弱化显示。

## 7. HTML 导出

为任意会话生成自包含 HTML 文件。

要求：

1. 单个 `.html` 文件，无外部依赖，离线可用。
2. 内联 CSS：暗色主题，等宽代码块，打印友好。
3. 可折叠的 `<details>` 元素用于系统提示和工具结果。
4. 用户消息视觉区分：整块使用醒目的护眼浅绿色背景，并搭配绿色边框和深绿色角色标识；不得仅依赖左边框提示。
5. 每条消息显示时间戳。
6. 页眉：会话标题、项目路径、时间范围、模型（如可用）。
7. 文件名：`{title}-{date}.html`（日期格式 `YYYY-MM-DD`）。

模板结构：

```html
<!DOCTYPE html>
<html lang="zh">
<head><meta charset="utf-8"><title>{标题}</title><style>/* 内联样式 */</style></head>
<body>
  <header><!-- 会话元信息 --></header>
  <main>
    <!-- 服务端渲染的消息 -->
  </main>
  <script>/* 折叠切换，极简 */</script>
</body>
</html>
```

## 8. 管理 API

`/api/sessions/` 下的认证路由：

| 方法 | 路径 | 用途 |
|------|------|------|
| `GET` | `/api/sessions/projects` | 列出项目及其会话数量 |
| `GET` | `/api/sessions?project={path}` | 列出会话，可按项目过滤 |
| `GET` | `/api/sessions/{id}?source={path}` | 会话详情及消息 |
| `GET` | `/api/sessions/{id}/export?source={path}` | 导出为 HTML（返回 `text/html`） |
| `GET` | `/api/sessions/{id}/cleanup-hint?source={path}` | 返回 Claude Code CLI 清理命令提示（不执行删除） |

查询参数：

```text
project      按 cwd 路径过滤会话
page         页码（默认 1）
page_size    每页条数（默认 20，最大 100）
source       JSONL 文件路径（详情/导出/清理提示时必需，用于消除歧义）
```

`source` 参数在详情、导出和清理提示操作中必需，因为 session ID 在不同项目间不唯一。服务端不得提供删除 JSONL 文件或附属目录的 API。

错误响应使用 `{"error":"..."}`。不支持的方法返回 405。

## 9. 前端

### 9.1 标签页

在仪表盘导航中新增 `会话记录` 标签：

```text
状态 / Providers / 证书 / 使用统计 / 会话记录
```

### 9.2 布局

双面板布局，`max-width: 1600px`：

**左侧面板（建议 360px）：**

1. 顶部为项目下拉选择器，按 `lastActiveAt DESC` 排序。
2. 下拉选择器包含 `"全部项目"` 和各具体项目；每项显示项目名称和会话数量。
3. 选择项目后，左侧主体刷新为该项目下的会话列表。
4. 会话列表在左侧面板内独立上下滚动，不影响右侧详情滚动位置。
5. 会话卡片展示标题、相对时间、消息数；在 `"全部项目"` 模式下还应展示项目名称或项目路径摘要。
6. 当前选中会话在左侧列表中高亮。

**右侧面板：**

1. 未选中会话时展示空状态，引导用户从左侧选择会话。
2. 选中会话后展示页眉：标题、项目路径、时间范围、导出按钮、Claude Code CLI 清理提示按钮。
3. 消息列表展示完整对话；用户消息整块使用醒目的护眼浅绿色背景，助手消息保持白色或中性背景，系统和工具消息继续使用可折叠样式。
4. `xl+` 屏幕上的大纲侧边栏：仅用户消息，点击滚动定位；当用户消息较多时，大纲区域必须设置最大高度并支持鼠标滚轮独立上下滚动。
5. 较小屏幕上的大纲浮动按钮 + 对话框。
6. 不设置“返回会话列表”按钮；左侧会话列表始终可见，切换会话直接点击左侧列表项。

### 9.3 清理提示弹窗

清理提示弹窗只展示命令，不执行删除。命令展示区必须采用现代代码编辑器风格：

1. 使用深色背景代码框，和弹窗白色背景形成明显对比。
2. 使用等宽字体，支持长命令换行且不撑破弹窗。
3. 对命令关键词、参数、路径做轻量高亮；高亮可以由前端按 token 简单分段完成，不要求引入完整语法高亮库。
4. 复制按钮保留在代码框右上或右侧，悬停状态清晰。
5. 预览命令和交互命令各自独立展示，标签清楚。
6. 弹窗必须同时支持 Linux/macOS 命令和 Windows 命令。
7. Windows 命令提示必须使用 Windows 风格路径。原始项目路径位于 Unix home 目录下时，Windows 路径使用 `C:\Users\用户名代理\...` 作为用户占位；原始项目路径已经是 `C:\Users\Alice\project` 这类原生 Windows 路径时，应保留盘符，并将用户名段替换为 `用户名代理`。
8. Windows 命令路径展示前必须清洗命令敏感字符或 Windows 非法路径字符，包括控制字符、`"`、`<`、`>`、`:`、`|`、`?`、`*`。管理面板仍然只展示命令，不执行命令。

### 9.4 状态展示

| 状态 | 展示内容 |
|------|---------|
| 配置目录不可访问 | 错误提示及挂载说明 |
| 未找到项目 | "未找到 Claude Code 会话"及路径提示 |
| 空项目 | "该项目暂无会话" |
| 加载中 | 加载动画 |

## 10. 非目标

1. 不修改代理 — 此功能完全独立于代理逻辑。
2. 不需要 SSE 重建 — 读取本地文件无需此功能。
3. 不支持多 provider — v1 仅支持 Claude Code。
4. v1 不提供全文搜索。
5. 不提供会话恢复或终端集成（超出 Web 管理面板范围）。
6. 不做云同步或多实例合并。
7. 不使用 SQLite 索引 — 直接读取文件，内存缓存带 TTL。

## 11. 边界情况

1. `.claude/projects/` 未找到或不可读：返回空列表并附带错误提示。
2. JSONL 某行解析失败：跳过该行，继续解析。
3. JSONL 文件完全损坏：跳过该文件，记录警告。
4. 缺少 `cwd`：降级为 `"Unknown Project"`。
5. 缺少 `sessionId`：使用文件名主干作为 ID。
6. Agent 会话文件（`agent-*`）：v1 中跳过。
7. 清理提示：仅展示命令，不在管理面板执行删除。项目级清理提示应优先使用 `claude project purge --dry-run <project-path>` 预览，再提示用户在终端自行运行 `claude project purge -i <project-path>` 交互确认。弹窗必须分别展示 Linux/macOS 命令和 Windows 命令。单会话清理命令以当前 Claude Code 版本的 `claude --help` / `claude project --help` 为准。
8. Claude Code 并发写入：JSONL 文件是追加写入的，最后一行可能不完整，应优雅处理。
9. 符号链接的项目路径：解析为真实路径以去重。

## 12. 部署

Claude projects 目录必须对应用可访问：

- **本地运行：** 默认 `~/.claude/projects/`（通过 `os.UserHomeDir()` 获取）
- **Docker：** 将 `~/.claude/projects/` 挂载为卷，通过 `CLAUDE_PROJECTS_DIR` 环境变量配置

跨平台支持：`os.UserHomeDir()` 和 `filepath` 系列函数自动兼容 Linux、macOS 和 Windows。`CLAUDE_PROJECTS_DIR` 环境变量可在任意平台上覆盖默认 projects 路径。

## 13. 参考

1. cc-switch 会话管理器：https://github.com/farion1231/cc-switch
2. cc-switch Claude provider 实现：`src-tauri/src/session_manager/providers/claude.rs`
3. cc-switch 会话 UI：`src/components/sessions/`
