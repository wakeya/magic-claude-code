# Claude Code 会话记录浏览器实现计划

**目标：** 新增会话记录浏览器标签页，读取本地 Claude Code JSONL 文件，按项目目录组织会话，支持 HTML 导出。

**架构：** 新建 `internal/session/` 包处理文件系统扫描、JSONL 解析和 HTML 导出。管理 API 暴露项目、会话和导出端点。前端新增双面板浏览器标签页。无需修改代理。

**技术栈：** Go 1.26、`html/template`、Vue 3、TypeScript、Vite。

---

## 文件清单

新建：

1. `internal/session/types.go`：项目、会话、消息的领域类型。
2. `internal/session/scanner.go`：扫描 `~/.claude/projects/`，从 JSONL 文件头尾提取元数据。
3. `internal/session/parser.go`：完整 JSONL 解析为会话消息。
4. `internal/session/parser_test.go`：JSONL 解析、消息提取、标题检测的单元测试。
5. `internal/session/scanner_test.go`：项目发现和会话列表的单元测试。
6. `internal/session/export.go`：HTML 模板和导出渲染。
7. `internal/session/export_test.go`：导出输出验证测试。
8. `internal/admin/session_handler.go`：认证管理 API 处理器。
9. `internal/admin/session_handler_test.go`：API 处理器测试。
10. `internal/frontend/src/components/SessionBrowser.vue`：双面板会话浏览器 UI。
11. `internal/frontend/src/components/SessionDetail.vue`：消息渲染器。
12. `internal/frontend/src/components/SessionOutline.vue`：用户消息大纲。

修改：

1. `internal/admin/server.go`：注册 `/api/sessions/*` 路由。
2. `internal/frontend/src/composables/useApi.ts`：新增会话 API 客户端类型和方法。
3. `internal/frontend/src/composables/useI18n.ts`：新增中英文字符串。
4. `internal/frontend/src/views/DashboardView.vue`：新增 `会话记录` 标签页和宽布局。

---

## 任务 1：创建会话领域类型

**文件：**

1. 新建：`internal/session/types.go`

- [ ] **步骤 1：创建类型**

```go
package session

import "time"

type Project struct {
    Path         string    `json:"path"`
    Name         string    `json:"name"`
    SessionCount int       `json:"session_count"`
    LastActiveAt time.Time `json:"last_active_at"`
}

type Session struct {
    ID           string    `json:"id"`
    Title        string    `json:"title"`
    ProjectPath  string    `json:"project_path"`
    SourcePath   string    `json:"source_path"`
    CreatedAt    time.Time `json:"created_at"`
    LastActiveAt time.Time `json:"last_active_at"`
    MessageCount int       `json:"message_count"`
}

type Message struct {
    Role      string `json:"role"`
    Content   string `json:"content"`
    Timestamp int64  `json:"ts,omitempty"`
}

type SessionDetail struct {
    Session  Session   `json:"session"`
    Messages []Message `json:"messages"`
}
```

- [ ] **步骤 2：验证编译**

运行：`go build ./internal/session`

预期：编译无错误。

## 任务 2：创建 JSONL 扫描器和解析器

**文件：**

1. 新建：`internal/session/scanner.go`
2. 新建：`internal/session/parser.go`
3. 新建：`internal/session/scanner_test.go`
4. 新建：`internal/session/parser_test.go`

- [ ] **步骤 1：编写失败的扫描器测试**

测试用例：

1. `TestScanProjectsGroupsByCwd` — 按工作目录分组项目
2. `TestScanSessionsExtractsMetadata` — 提取会话元数据
3. `TestScanSkipsAgentFiles` — 跳过 agent 文件
4. `TestScanHandlesEmptyDirectory` — 处理空目录

运行：`go test ./internal/session -run TestScan -v`

预期：因扫描器函数缺失而失败。

- [ ] **步骤 2：编写失败的解析器测试**

测试用例：

1. `TestParseMessagesExtractsUserAndAssistant` — 提取用户和助手消息
2. `TestParseMessagesReclassifiesToolResult` — 将纯工具结果消息重分类为 tool 角色
3. `TestParseMessagesSkipsMeta` — 跳过元信息行
4. `TestParseMessagesHandlesContentArray` — 处理内容块数组
5. `TestExtractTitleFromCustomTitle` — 从自定义标题提取标题
6. `TestExtractTitleFromFirstUserMessage` — 从首条用户消息提取标题
7. `TestExtractTitleSkipsCaveatAndCommands` — 跳过 caveat 和命令消息

运行：`go test ./internal/session -run TestParse -v`

预期：因解析器函数缺失而失败。

- [ ] **步骤 3：实现扫描器**

定义：

```go
func ScanProjects(root string) ([]Project, error)
func ScanSessions(root string, projectPath string) ([]Session, error)
```

实现要求：

1. 递归遍历 `root` 查找 `.jsonl` 文件。
2. 跳过以 `agent-` 开头的文件。
3. 对每个文件，读取前 10 行和后 30 行获取元数据。
4. 从列表扫描窗口中提取 `sessionId`、所有扫描到的 `cwd`、`timestamp`、`customTitle`。
5. 从尾部行提取 `lastActiveAt`。
6. 使用 `filepath.Clean` 规范化 `cwd`；当同一 JSONL 文件中某个扫描到的 `cwd` 是其他所有扫描到的 `cwd` 的祖先路径时，使用该祖先路径作为会话项目路径。
7. 在同一 Claude projects 源目录内，将子目录会话项目路径归并到推断出的项目根目录；祖先路径判断必须统一路径分隔符、对 Windows 盘符路径进行大小写不敏感比较，并按路径段比较，不依赖当前运行系统的 `filepath.Rel` 行为。
8. 应用标题优先级：扫描窗口中最后一个非空 `custom-title` > 首条有效用户消息 > 目录名 > ID 前缀。从用户消息标题候选中排除 local-command caveat、本地命令调用、本地命令 stdout/stderr 包裹内容。
9. 项目和会话均按 `lastActiveAt DESC` 排序。

- [ ] **步骤 4：实现解析器**

定义：

```go
func ParseMessages(filePath string) ([]Message, error)
func ExtractTitle(headLines []string) string
```

实现要求：

1. 逐行解析 JSONL，跳过 `isMeta` 行。
2. 提取 `message.role` 和 `message.content`。
3. 展平内容数组：合并文本，摘要展示 tool_use/tool_result。
4. 将仅含 tool_result 块的用户消息重分类为 `tool` 角色。
5. 跳过提取后内容为空的行。
6. 优雅处理不完整的最后一行。

- [ ] **步骤 5：验证所有测试通过**

运行：`go test ./internal/session -v`

预期：全部通过。

## 任务 3：创建 HTML 导出

**文件：**

1. 新建：`internal/session/export.go`
2. 新建：`internal/session/export_test.go`

- [ ] **步骤 1：编写失败的导出测试**

测试用例：

1. `TestExportHTMLContainsSessionTitle` — 包含会话标题
2. `TestExportHTMLContainsMessages` — 包含消息内容
3. `TestExportHTMLIsSelfContained` — 自包含（无外部依赖）
4. `TestExportHTMLCollapsesSystemMessages` — 系统消息可折叠

运行：`go test ./internal/session -run TestExport -v`

预期：因 `ExportHTML` 缺失而失败。

- [ ] **步骤 2：实现导出**

定义：

```go
func ExportHTML(detail *SessionDetail) ([]byte, error)
```

实现要求：

1. 使用 `html/template` 配合内联模板字符串。
2. 内联所有 CSS（暗色主题、等宽代码、打印样式）。
3. 每条消息渲染为 `<div>`，使用角色相关的 CSS 类。
4. 系统和工具内容包裹在 `<details>` 元素中。
5. 页眉包含会话元信息。
6. 添加极简 JS 用于折叠切换（可选）。
7. 设置文件名安全的 `<title>`。

- [ ] **步骤 3：验证测试通过**

运行：`go test ./internal/session -run TestExport -v`

预期：全部通过。

## 任务 4：创建管理会话 API

**文件：**

1. 新建：`internal/admin/session_handler.go`
2. 新建：`internal/admin/session_handler_test.go`
3. 修改：`internal/admin/server.go`

- [ ] **步骤 1：编写失败的 API 测试**

测试用例：

1. `TestSessionProjectsReturnsProjects` — 返回项目列表
2. `TestSessionListFilterByProject` — 按项目过滤会话
3. `TestSessionDetailReturnsMessages` — 返回会话详情及消息
4. `TestSessionExportReturnsHTML` — 导出返回 HTML
5. `TestSessionRoutesDoNotDeleteFiles` — 不提供删除文件路由
6. `TestSessionRoutesRequireAuth` — 路由需要认证

运行：`go test ./internal/admin -run TestSession -v`

预期：因路由缺失而失败。

- [ ] **步骤 2：注册路由**

在 `Server.Start` 中添加：

```go
mux.HandleFunc("/api/sessions", s.authMiddlewareFunc(s.handleSessions))
mux.HandleFunc("/api/sessions/projects", s.authMiddlewareFunc(s.handleSessionProjects))
mux.HandleFunc("/api/sessions/", s.authMiddlewareFunc(s.handleSessionRoutes))
```

- [ ] **步骤 3：实现处理器**

处理器职责：

1. `handleSessionProjects`：调用 `session.ScanProjects(root)`，返回 JSON。
2. `handleSessions`：解析 `project` 查询参数，调用 `session.ScanSessions(root, project)`，返回带分页的 JSON。
3. `handleSessionRoutes`：按方法和路径后缀分发：
   - `GET .../export`：调用 `session.ExportHTML`，返回 `text/html`。
   - `GET .../cleanup-hint`：返回 Claude Code CLI 清理命令提示 JSON，不执行删除。响应包含 Linux/macOS 预览命令、交互命令，以及 Windows 预览命令、交互命令。
   - `GET`（详情）：解析 `source` 查询参数，调用 `session.ParseMessages`，返回 JSON。
   - 不提供 `DELETE` 路由；删除或清理只能通过 Claude Code CLI 提示完成，服务端不得移除 JSONL 文件或附属目录。
4. `page_size` 限制在 `1..100`。
5. 不支持的方法返回 405。
6. 返回 `{"error":"..."}` 格式的 JSON 错误。

- [ ] **步骤 4：验证测试通过**

运行：`go test ./internal/admin -run TestSession -v`

预期：全部通过。

### 任务 4.1：Windows 清理命令提示

已于 2026-06-11 实现。

- `internal/session/types.go`：`CleanupHint` 增加 `windows_preview_command` 和 `windows_interactive_command`。
- `internal/admin/session_handler.go`：`handleSessionCleanupHint` 同时生成 Linux/macOS 和 Windows 命令提示。
- Windows 路径转换：
  - `/home/<user>/...` 和 `/Users/<user>/...` 转为 `C:\Users\用户名代理\...`。
  - `/mnt/c/...` 转为 `C:\...`。
  - 原生 `C:\Users\<user>\...` 路径保留盘符，并将用户名替换为 `用户名代理`。
  - 展示前清洗命令敏感字符和 Windows 非法路径字符。
- `internal/admin/session_handler_test.go`：覆盖 Linux home 路径转换、原生 Windows 盘符路径转换和危险字符清洗。

## 任务 5：创建前端组件

**文件：**

1. 新建：`internal/frontend/src/components/SessionBrowser.vue`
2. 新建：`internal/frontend/src/components/SessionDetail.vue`
3. 新建：`internal/frontend/src/components/SessionOutline.vue`
4. 修改：`internal/frontend/src/composables/useApi.ts`
5. 修改：`internal/frontend/src/composables/useI18n.ts`
6. 修改：`internal/frontend/src/views/DashboardView.vue`

- [ ] **步骤 1：添加 TypeScript API 类型和方法**

在 `useApi.ts` 中添加：

```ts
interface SessionProject { path: string; name: string; session_count: number; last_active_at: string }
interface SessionItem { id: string; title: string; project_path: string; source_path: string; created_at: string; last_active_at: string; message_count: number }
interface SessionMessage { role: string; content: string; ts?: number }
interface SessionDetailResponse { session: SessionItem; messages: SessionMessage[] }
interface SessionCleanupHint { project_path: string; preview_command: string; interactive_command: string; note: string }

getSessionProjects()
getSessionList(params: { project?: string; page?: number; page_size?: number })
getSessionDetail(id: string, source: string)
exportSessionHTML(id: string, source: string)
getSessionCleanupHint(id: string, source: string): Promise<SessionCleanupHint>
```

- [ ] **步骤 2：构建 SessionBrowser.vue**

双面板布局：

左侧面板：
1. 项目列表，带会话数量徽标。
2. 顶部 "全部项目" 选项。
3. 选中项目的会话列表。
4. 会话卡片：标题、相对时间、消息数。

右侧面板：
1. 空状态或选中会话的详情。
2. 页眉包含标题、项目、导出按钮和 Claude Code CLI 清理提示按钮。

- [ ] **步骤 3：构建 SessionDetail.vue**

1. 页眉：会话标题、项目路径、时间范围。
2. 消息列表，按角色设置样式。
3. 系统和工具内容默认折叠（`<details>`）。
4. 用户消息以彩色边框高亮。
5. 大纲触发时自动滚动到对应消息。

- [ ] **步骤 4：构建 SessionOutline.vue**

1. 仅展示 `role=user` 的消息。
2. 预览文本（前 50 字符）和相对时间。
3. 点击滚动详情视图到对应消息。
4. 响应式：`xl+` 屏幕为侧边栏，较小屏幕为浮动对话框。

- [ ] **步骤 5：添加标签页和布局**

在 `DashboardView.vue` 中：

1. 将 `sessions` 添加到标签页，标签为 `会话记录`。
2. 活跃标签为 `sessions` 时使用 `max-w-[1600px]`。
3. 渲染 `<SessionBrowser />`。

- [ ] **步骤 6：添加国际化字符串**

在 `useI18n.ts` 中添加会话浏览器的中英文字符串。

- [ ] **步骤 7：验证前端构建**

运行：

```bash
cd internal/frontend
npm run build
```

预期：Vite 构建成功。

## 任务 6：完整验证

**文件：**

1. 更新：`docs/features/2026-05-18-session-browser/validation.md`
2. 更新：`docs/features/2026-05-18-session-browser/status.md`

- [ ] **步骤 1：运行后端测试**

运行：

```bash
go test ./...
```

预期：全部通过。

- [ ] **步骤 2：运行前端构建**

运行：

```bash
cd internal/frontend
npm run build
```

预期：构建成功。

- [ ] **步骤 3：手动验证**

1. 确认 `~/.claude/projects/` 可访问。
2. 启动代理/管理服务。
3. 打开管理界面 → `会话记录` 标签页。
4. 验证项目列表正确显示会话数量。
5. 选择一个项目，验证会话列表。
6. 打开会话，验证消息正确显示。
7. 测试大纲点击滚动功能。
8. 导出会话为 HTML，验证离线可打开。
9. 打开清理提示，验证仅展示 Claude Code CLI 命令提示，不执行删除。
10. 验证代理仍正常工作。

- [ ] **步骤 4：记录证据**

将命令输出和手动观察结果写入 `validation.md`。

- [ ] **步骤 5：更新生命周期**

所有验证通过后，将 `status.md` 生命周期设为 `validated`。

---

## v1.1 增量改造计划：项目下拉、用户消息高亮、清理命令代码框

**目标：** 在不改变后端 API 契约的前提下，按已确认的布局和视觉要求改造现有会话记录浏览器。

**文件：**

1. 修改 `internal/frontend/src/components/SessionBrowser.vue`：将左侧项目按钮列表替换为项目下拉框，左侧主体只保留可独立滚动的会话列表。
2. 修改 `internal/frontend/src/components/SessionDetail.vue`：将 `user` 消息渲染为整块护眼浅绿色背景，不再只依赖蓝色左边框提示。
3. 修改 `internal/session/export.go` 和 `internal/session/export_test.go`：导出 HTML 使用同样的整块浅绿色用户消息展示。
4. 新建 `internal/frontend/src/utils/sessionCommands.ts` 和 `internal/frontend/src/utils/sessionCommands.test.ts`：为清理命令做轻量 token 分段，支持语法高亮。
5. 修改 `internal/frontend/src/components/SessionBrowser.vue`：清理提示命令采用深色代码编辑器风格，区分命令、关键字、参数和路径。

**执行顺序：**

1. 先写导出 HTML 用户消息整块绿色高亮的失败测试。
2. 修改 `internal/session/export.go`，验证 `go test ./internal/session -run TestExport -v` 通过。
3. 先写清理命令 token 分段的失败前端测试。
4. 新增 tokenizer 工具，验证 `npm --prefix internal/frontend test -- --test-name-pattern tokenizeCommand` 通过。
5. 修改 `SessionBrowser.vue` 布局和清理命令展示。
6. 修改 `SessionDetail.vue` 用户消息样式。
7. 运行 `go test ./...`、`npm --prefix internal/frontend test`、`npm --prefix internal/frontend run build`。
