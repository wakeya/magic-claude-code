# Claude Code Session Browser Implementation Plan

**Goal:** Add a session browser tab that reads local Claude Code JSONL files, organizes sessions by project directory, and supports HTML export.

**Architecture:** A new `internal/session/` package handles filesystem scanning, JSONL parsing, and HTML export. Admin API handlers expose projects, sessions, and export endpoints. Frontend adds a two-panel browser tab. No proxy modification required.

**Tech Stack:** Go 1.26, `html/template`, Vue 3, TypeScript, Vite.

---

## File Map

Create:

1. `internal/session/types.go`: domain types for projects, sessions, messages.
2. `internal/session/scanner.go`: scan `~/.claude/projects/`, extract metadata from JSONL headers.
3. `internal/session/parser.go`: full JSONL parsing for session messages.
4. `internal/session/parser_test.go`: unit tests for JSONL parsing, message extraction, title detection.
5. `internal/session/scanner_test.go`: unit tests for project discovery and session listing.
6. `internal/session/export.go`: HTML template and export rendering.
7. `internal/session/export_test.go`: export output validation tests.
8. `internal/admin/session_handler.go`: authenticated admin API handlers.
9. `internal/admin/session_handler_test.go`: API handler tests.
10. `internal/frontend/src/components/SessionBrowser.vue`: two-panel session browser UI.
11. `internal/frontend/src/components/SessionDetail.vue`: message renderer.
12. `internal/frontend/src/components/SessionOutline.vue`: user-message outline.

Modify:

1. `internal/admin/server.go`: register `/api/sessions/*` routes.
2. `internal/frontend/src/composables/useApi.ts`: add session API client types and methods.
3. `internal/frontend/src/composables/useI18n.ts`: add zh/en strings.
4. `internal/frontend/src/views/DashboardView.vue`: add the `会话记录` tab and wide-layout behavior.

---

## Task 1: Add Session Domain Types

**Files:**

1. Create: `internal/session/types.go`

- [ ] **Step 1: Create types**

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

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/session`

Expected: compiles with no errors.

## Task 2: Add JSONL Scanner and Parser

**Files:**

1. Create: `internal/session/scanner.go`
2. Create: `internal/session/parser.go`
3. Create: `internal/session/scanner_test.go`
4. Create: `internal/session/parser_test.go`

- [ ] **Step 1: Write failing scanner tests**

Tests:

1. `TestScanProjectsGroupsByCwd`
2. `TestScanSessionsExtractsMetadata`
3. `TestScanSkipsAgentFiles`
4. `TestScanHandlesEmptyDirectory`

Run: `go test ./internal/session -run TestScan -v`

Expected: fail because scanner functions are missing.

- [ ] **Step 2: Write failing parser tests**

Tests:

1. `TestParseMessagesExtractsUserAndAssistant`
2. `TestParseMessagesReclassifiesToolResult`
3. `TestParseMessagesSkipsMeta`
4. `TestParseMessagesHandlesContentArray`
5. `TestExtractTitleFromCustomTitle`
6. `TestExtractTitleFromFirstUserMessage`
7. `TestExtractTitleSkipsCaveatAndCommands`

Run: `go test ./internal/session -run TestParse -v`

Expected: fail because parser functions are missing.

- [ ] **Step 3: Implement scanner**

Define:

```go
func ScanProjects(root string) ([]Project, error)
func ScanSessions(root string, projectPath string) ([]Session, error)
```

Implementation requirements:

1. Walk `root` recursively to find `.jsonl` files.
2. Skip files starting with `agent-`.
3. For each file, read first 10 and last 30 lines for metadata.
4. Extract `sessionId`, all scanned `cwd` values, `timestamp`, and `customTitle` from the list window.
5. Extract `lastActiveAt` from tail lines.
6. Normalize `cwd` with `filepath.Clean`; when one scanned `cwd` is an ancestor of all other scanned `cwd` values in the same JSONL file, use that ancestor as the session project path.
7. Within the same Claude projects source directory, fold subdirectory session project paths into the inferred project root; ancestor checks must normalize slash direction, handle Windows drive paths case-insensitively, and compare path segments instead of relying on host-specific `filepath.Rel` behavior.
8. Apply title priority: last non-empty `custom-title` in the scan window > first valid user message > basename > ID prefix. Exclude local-command caveats, local command invocations, and local-command stdout/stderr wrappers from user-message title candidates.
9. Sort projects and sessions by `lastActiveAt DESC`.

- [ ] **Step 4: Implement parser**

Define:

```go
func ParseMessages(filePath string) ([]Message, error)
func ExtractTitle(headLines []string) string
```

Implementation requirements:

1. Parse each JSONL line, skip `isMeta` lines.
2. Extract `message.role` and `message.content`.
3. Flatten content arrays: join text, summarize tool_use/tool_result.
4. Reclassify user messages with only tool_result blocks as role `tool`.
5. Skip lines with empty content after extraction.
6. Handle partial/corrupted last line gracefully.

- [ ] **Step 5: Verify all tests pass**

Run: `go test ./internal/session -v`

Expected: PASS.

## Task 3: Add HTML Export

**Files:**

1. Create: `internal/session/export.go`
2. Create: `internal/session/export_test.go`

- [ ] **Step 1: Write failing export tests**

Tests:

1. `TestExportHTMLContainsSessionTitle`
2. `TestExportHTMLContainsMessages`
3. `TestExportHTMLIsSelfContained`
4. `TestExportHTMLCollapsesSystemMessages`

Run: `go test ./internal/session -run TestExport -v`

Expected: fail because `ExportHTML` is missing.

- [ ] **Step 2: Implement export**

Define:

```go
func ExportHTML(detail *SessionDetail) ([]byte, error)
```

Implementation requirements:

1. Use `html/template` with an inline template string.
2. Inline all CSS (dark theme, monospace code, print styles).
3. Render each message as a `<div>` with role-based class.
4. Wrap system and tool content in `<details>` elements.
5. Include session metadata in the header.
6. Add minimal JS for collapse toggling (optional).
7. Set filename-safe title in `<title>`.

- [ ] **Step 3: Verify tests pass**

Run: `go test ./internal/session -run TestExport -v`

Expected: PASS.

## Task 4: Add Admin Session API

**Files:**

1. Create: `internal/admin/session_handler.go`
2. Create: `internal/admin/session_handler_test.go`
3. Modify: `internal/admin/server.go`

- [ ] **Step 1: Write failing API tests**

Tests:

1. `TestSessionProjectsReturnsProjects`
2. `TestSessionListFilterByProject`
3. `TestSessionDetailReturnsMessages`
4. `TestSessionExportReturnsHTML`
5. `TestSessionRoutesDoNotDeleteFiles`
6. `TestSessionRoutesRequireAuth`

Run: `go test ./internal/admin -run TestSession -v`

Expected: fail because routes are missing.

- [ ] **Step 2: Register routes**

In `Server.Start`, add:

```go
mux.HandleFunc("/api/sessions", s.authMiddlewareFunc(s.handleSessions))
mux.HandleFunc("/api/sessions/projects", s.authMiddlewareFunc(s.handleSessionProjects))
mux.HandleFunc("/api/sessions/", s.authMiddlewareFunc(s.handleSessionRoutes))
```

- [ ] **Step 3: Implement handlers**

Handler responsibilities:

1. `handleSessionProjects`: call `session.ScanProjects(root)`, return JSON.
2. `handleSessions`: parse `project` query param, call `session.ScanSessions(root, project)`, return JSON with pagination.
3. `handleSessionRoutes`: dispatch by method and path suffix:
   - `GET .../export`: call `session.ExportHTML`, return `text/html`.
   - `GET .../cleanup-hint`: return Claude Code CLI cleanup command hint JSON, without deleting anything.
   - `GET` (detail): parse `source` query param, call `session.ParseMessages`, return JSON.
   - Do not provide a `DELETE` route; deletion/cleanup is only represented as Claude Code CLI command hints, and the server must not remove JSONL files or sidecar directories.
4. Clamp `page_size` to `1..100`.
5. Return 405 for unsupported methods.
6. Return JSON errors with `{"error":"..."}`.

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/admin -run TestSession -v`

Expected: PASS.

## Task 5: Add Frontend Components

**Files:**

1. Create: `internal/frontend/src/components/SessionBrowser.vue`
2. Create: `internal/frontend/src/components/SessionDetail.vue`
3. Create: `internal/frontend/src/components/SessionOutline.vue`
4. Modify: `internal/frontend/src/composables/useApi.ts`
5. Modify: `internal/frontend/src/composables/useI18n.ts`
6. Modify: `internal/frontend/src/views/DashboardView.vue`

- [ ] **Step 1: Add TypeScript API types and methods**

Add to `useApi.ts`:

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

- [ ] **Step 2: Build SessionBrowser.vue**

Two-panel layout:

Left panel:
1. Project list with session count badges.
2. "All Projects" option at the top.
3. Session list for selected project.
4. Session cards: title, relative time, message count.

Right panel:
1. Empty state or selected session detail.
2. Header with title, project, export button, and Claude Code CLI cleanup hint button.

- [ ] **Step 3: Build SessionDetail.vue**

1. Header: session title, project path, time range.
2. Message list with role-based styling.
3. System and tool content collapsed with `<details>`.
4. User messages highlighted with colored border.
5. Auto-scroll to message when triggered by outline.

- [ ] **Step 4: Build SessionOutline.vue**

1. Only `role=user` messages.
2. Preview text (first 50 chars) and relative time.
3. Click scrolls the detail view to the matching message.
4. Responsive: sidebar on `xl+`, floating dialog on smaller screens.

- [ ] **Step 5: Add tab and layout**

In `DashboardView.vue`:

1. Add `sessions` to tabs with label `会话记录`.
2. Use `max-w-[1600px]` when active tab is `sessions`.
3. Render `<SessionBrowser />`.

- [ ] **Step 6: Add i18n strings**

Add zh/en strings for session browser labels in `useI18n.ts`.

- [ ] **Step 7: Verify frontend build**

Run:

```bash
cd internal/frontend
npm run build
```

Expected: Vite build succeeds.

## Task 6: Full Verification

**Files:**

1. Update: `docs/features/2026-05-18-session-browser/validation.md`
2. Update: `docs/features/2026-05-18-session-browser/status.md`

- [ ] **Step 1: Run backend tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run frontend build**

Run:

```bash
cd internal/frontend
npm run build
```

Expected: PASS.

- [ ] **Step 3: Manual verification**

1. Ensure `~/.claude/projects/` is accessible.
2. Start the proxy/admin service.
3. Open the admin UI → `会话记录` tab.
4. Verify projects appear with correct session counts.
5. Select a project and verify sessions list.
6. Open a session and verify messages display correctly.
7. Test outline click-to-scroll.
8. Export a session as HTML and verify it opens offline.
9. Open the cleanup hint and verify it only displays Claude Code CLI command guidance without deleting files.
10. Verify the proxy still works normally.

- [ ] **Step 4: Record evidence**

Write command outputs and manual observations into `validation.md`.

- [ ] **Step 5: Update lifecycle**

Set `status.md` lifecycle to `validated` only after all verification passes.

---

## v1.1 Refinement Plan: Project Dropdown, User Highlight, Cleanup Command Editor

**Goal:** Refine the shipped session browser UI to match the confirmed layout and visual requirements without changing backend API contracts.

**Files:**

1. Modify `internal/frontend/src/components/SessionBrowser.vue`: replace left project button list with a project dropdown and keep the left panel body as an independently scrolling session list.
2. Modify `internal/frontend/src/components/SessionDetail.vue`: render `user` messages as full eye-friendly light-green blocks instead of relying on a blue left border.
3. Modify `internal/session/export.go` and `internal/session/export_test.go`: make exported HTML use the same full light-green user-message treatment.
4. Create `internal/frontend/src/utils/sessionCommands.ts` and `internal/frontend/src/utils/sessionCommands.test.ts`: tokenize cleanup commands for lightweight syntax highlighting.
5. Modify `internal/frontend/src/components/SessionBrowser.vue`: render cleanup commands in dark code-editor style blocks with highlighted command, keyword, flag, and path tokens.

**Execution order:**

1. Write failing Go export test for full green user-message blocks.
2. Update `internal/session/export.go` and verify `go test ./internal/session -run TestExport -v`.
3. Write failing frontend utility tests for cleanup command tokenization.
4. Add the tokenizer helper and verify `npm --prefix internal/frontend test -- --test-name-pattern tokenizeCommand`.
5. Update `SessionBrowser.vue` layout and cleanup command block rendering.
6. Update `SessionDetail.vue` user-message styling.
7. Run `go test ./...`, `npm --prefix internal/frontend test`, and `npm --prefix internal/frontend run build`.
