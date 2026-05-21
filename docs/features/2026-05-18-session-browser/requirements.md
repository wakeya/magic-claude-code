# Claude Code Session Browser Requirements

**Version:** 1.1
**Date:** 2026-05-21
**Status:** draft
**Supersedes:** v0.2 (proxy-based capture, archived)

---

## 1. Objective

Add a session browser tab to the admin panel that reads local Claude Code session files, organizes them by project directory, and supports exporting any session as a self-contained HTML file.

The browser should show:

1. A left panel with a project dropdown at the top and the selected project's session list below.
2. A right panel with the selected session's full conversation content.
3. An outline of user messages; clicking an outline item scrolls to the corresponding message.
4. An export button that downloads the session as a single HTML file.

## 2. Outcomes

The feature is successful when:

1. Sessions are read from `~/.claude/projects/` JSONL files with native sessionId.
2. Project directories are the first-level filter for the session list.
3. Session detail shows the full conversation with collapsible system and tool content.
4. Any session can be exported as a self-contained HTML file opened offline, with user messages shown as full light-green blocks.
5. The admin UI never deletes JSONL files; if cleanup is needed, it only shows Claude Code CLI command hints for session/project cleanup.
6. The left session list refreshes when the selected project changes and scrolls independently from the right detail panel.

## 3. Data Source

### 3.1 Directory Structure

Claude Code stores sessions under `~/.claude/projects/`:

```text
~/.claude/projects/
  <encoded-path>/
    <session-id>.jsonl        (conversation transcript)
    <session-id>/             (sidecar: subagents/, tool-results/)
```

One subdirectory per project. The subdirectory name encodes the project path. Each `.jsonl` file is one session. Files starting with `agent-` are agent sub-sessions and should be skipped in v1.

### 3.2 JSONL Line Format

Each `.jsonl` file contains one JSON object per line. Relevant line types:

| Type | Example |
|------|---------|
| Metadata | `{"sessionId":"abc","cwd":"/path/to/project","timestamp":"2026-05-20T10:00:00Z"}` |
| User message | `{"type":"user","message":{"role":"user","content":"hello"},"timestamp":"..."}` |
| Assistant message | `{"type":"assistant","message":{"role":"assistant","content":[...]},"timestamp":"..."}` |
| Custom title | `{"type":"custom-title","customTitle":"fix-login-bug"}` |
| Meta skip | `{"isMeta":true,...}` |

Content can be a plain string or an array of content blocks:

```json
"text"
[{"type":"text","text":"..."},{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]
[{"type":"tool_result","tool_use_id":"toolu_1","content":"file contents"}]
```

### 3.3 Metadata Extraction (Listing)

For the session list, read only the first 10 and last 30 lines of each JSONL file:

| Field | Source | Fallback |
|-------|--------|----------|
| `sessionId` | metadata line `sessionId` | filename stem |
| `cwd` | metadata line `cwd` | `"Unknown Project"` |
| `title` | last non-empty `custom-title` in the scan window > first user message (skip caveats/commands) > directory basename > ID prefix | session ID prefix |
| `createdAt` | first `timestamp` | file mod time |
| `lastActiveAt` | last non-meta `timestamp` | file mod time |
| `messageCount` | count of non-meta, non-summary lines | 0 |

### 3.4 Full Message Parsing (Detail)

When a specific session is opened, parse the entire JSONL file into ordered messages.

## 4. Project Directory Mapping

Projects are identified by the Claude projects source directory together with the `cwd` field from session metadata. A single JSONL session can contain multiple `cwd` values when Claude Code runs commands from subdirectories; if those values have an ancestor/child relationship, the session must keep the ancestor path as its project path. If one source directory contains sessions from a project root and sessions from its subdirectories, subdirectory sessions must also be folded into the project root so `/repo/internal/frontend` is not shown as a separate `frontend` project.

| Field | Value |
|-------|-------|
| `name` | `basename(projectPath)` |
| `path` | normalized project root path; fall back to full `cwd` when the root cannot be inferred |
| `sessionCount` | number of sessions folded into this project path |
| `lastActiveAt` | most recent `lastActiveAt` among sessions |

Projects are sorted by `lastActiveAt DESC`. Sessions within a project are also sorted by `lastActiveAt DESC`.

Path normalization for cross-platform support:

1. Use `filepath.Clean(cwd)` to normalize separators before grouping.
2. Within one JSONL file, collect all scanned non-empty `cwd` values. If one `cwd` is an ancestor of all other scanned `cwd` values, use that ancestor for the session's `projectPath` instead of the last-seen `cwd`.
3. Within the same Claude projects source directory, if one session `projectPath` is an ancestor of the other session `projectPath` values, use that ancestor as the source directory's project root.
4. Ancestor checks must use one consistent comparable representation: normalize `\` to `/`, clean with slash-style path semantics, apply case-insensitive comparison for Windows drive paths, and compare path segments instead of string prefixes. `/work/project-a` must not match `/work/project-api`.
5. On case-insensitive filesystems (Windows), apply `strings.ToLower` to the project path for deduplication.
6. Use `filepath.Base(projectPath)` for project name (works with both `/` and `\` separators).

## 5. Message Model

| Role | Detection Rule | Display |
|------|---------------|---------|
| `system` | first metadata/system content | collapsed by default |
| `user` | `message.role=user` with non-tool_result content | full light-green block highlight, included in outline |
| `assistant` | `message.role=assistant` | text + tool use blocks |
| `tool` | `message.role=user` where all content blocks are `tool_result` | tool name + result summary |

Content block handling:

| Block Type | Display |
|------------|---------|
| `text` | rendered as plain text |
| `tool_use` | `[Tool: {name}]` with expandable input |
| `tool_result` | summary with expandable detail |
| `image` / binary | type and size only |

User messages containing `<local-command-caveat>`, starting with `<command-name>`, or containing local-command stdout/stderr wrappers should be displayed but excluded from title candidates and de-emphasized in the outline.

## 6. HTML Export

Generate a self-contained HTML file for any session.

Requirements:

1. Single `.html` file, no external dependencies, works offline.
2. Inline CSS: dark theme, monospace code blocks, print-friendly.
3. Collapsible `<details>` elements for system prompts and tool results.
4. User messages visually distinct: the full message block uses an eye-friendly light-green background with green border and dark-green role label; a colored left border alone is not sufficient.
5. Timestamps shown per message.
6. Header: session title, project path, time range, model (if available).
7. Filename: `{title}-{date}.html` (date in `YYYY-MM-DD` format).

Template structure:

```html
<!DOCTYPE html>
<html lang="zh">
<head><meta charset="utf-8"><title>{title}</title><style>/* inline */</style></head>
<body>
  <header><!-- session metadata --></header>
  <main>
    <!-- messages rendered server-side -->
  </main>
  <script>/* toggle collapse, minimal */</script>
</body>
</html>
```

## 7. Admin API

Authenticated routes under `/api/sessions/`:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/sessions/projects` | List projects with session counts |
| `GET` | `/api/sessions?project={path}` | List sessions, optionally filtered by project |
| `GET` | `/api/sessions/{id}?source={path}` | Session detail with messages |
| `GET` | `/api/sessions/{id}/export?source={path}` | Export as HTML (returns `text/html`) |
| `GET` | `/api/sessions/{id}/cleanup-hint?source={path}` | Return Claude Code CLI cleanup command hints (does not delete) |

Query parameters:

```text
project      filter sessions by cwd path
page         page number (default 1)
page_size    items per page (default 20, max 100)
source       JSONL file path (required for detail/export/cleanup hint to disambiguate)
```

The `source` parameter is required for detail, export, and cleanup hints because session IDs are not globally unique across projects. The server must not expose an API that deletes JSONL files or sidecar directories.

Error responses use `{"error":"..."}`. Invalid methods return 405.

## 8. Frontend

### 8.1 Tab

Add `会话记录` tab to the dashboard navigation:

```text
状态 / Providers / 证书 / 使用统计 / 会话记录
```

### 8.2 Layout

Two-panel layout, `max-width: 1600px`:

**Left panel (recommended 360px):**

1. Project dropdown at the top, sorted by `lastActiveAt DESC`.
2. Dropdown includes `"All Projects"` and concrete projects; each option shows project name and session count.
3. Selecting a project refreshes the left panel body with that project's sessions.
4. Session list scrolls vertically inside the left panel and does not affect the right detail scroll position.
5. Session cards show title, relative time, and message count; in `"All Projects"` mode, they also show a project name or path summary.
6. The currently selected session is highlighted in the left list.

**Right panel:**

1. When no session is selected, show an empty state that prompts the user to select a session from the left panel.
2. When a session is selected, show header: title, project path, time range, export button, Claude Code CLI cleanup hint button.
3. Message list shows the full conversation; user messages use a full light-green block background, assistant messages use white or neutral background, and system/tool messages remain collapsible.
4. Outline sidebar on `xl+` screens: user messages only, click to scroll; when many user messages exist, the outline region must have a maximum height and support independent mouse-wheel vertical scrolling.
5. Outline floating button + dialog on smaller screens.
6. Do not add a "back to session list" button; the left session list remains visible, and switching sessions is done by clicking left list items.

### 8.3 Cleanup Hint Dialog

The cleanup hint dialog only displays commands and never executes deletion. Command display must use a modern code-editor style:

1. Dark code box with clear contrast against the white dialog background.
2. Monospace font, long-command wrapping, and no horizontal overflow that breaks the dialog.
3. Lightweight highlighting for command keywords, flags, and paths; frontend token splitting is enough and a full syntax-highlighting library is not required.
4. Copy button remains visible at the top-right or right side of the code box with clear hover state.
5. Preview and interactive commands are shown as separate labeled code blocks.

### 8.4 States

| State | Display |
|-------|---------|
| Config dir not accessible | Error message with mount instructions |
| No projects found | "No Claude Code sessions found" with path hint |
| Empty project | "This project has no sessions" |
| Loading | Spinner |

## 9. Non-Goals

1. No proxy modification — this feature is completely independent of proxy logic.
2. No SSE reconstruction — not needed when reading local files.
3. No multi-provider support — Claude Code only in v1.
4. No full-text search in v1.
5. No session resume or terminal integration (out of scope for a web admin panel).
6. No cloud sync or multi-instance merge.
7. No SQLite index — read files directly, cache in memory with TTL.

## 10. Edge Cases

1. `.claude/projects/` not found or not readable: return empty list with error hint.
2. JSONL parse failure on a line: skip the line, continue parsing.
3. JSONL file completely corrupted: skip the file, log warning.
4. Missing `cwd`: fall back to `"Unknown Project"`.
5. Missing `sessionId`: use filename stem as ID.
6. Agent session files (`agent-*`): skip in v1.
7. Cleanup hint: show commands only; do not execute deletion from the admin panel. For project-level cleanup, prefer `claude project purge --dry-run <project-path>` as a preview, then tell the user to run `claude project purge -i <project-path>` in a terminal for interactive confirmation. Session-level cleanup commands are version-dependent and should be checked with the current `claude --help` / `claude project --help`.
8. Concurrent Claude Code writes: JSONL files are append-only; partial last line is expected and should be handled gracefully.
9. Symlinked project paths: resolve to real path for deduplication.

## 11. Deployment

The Claude projects directory must be accessible to the application:

- **Local run:** default `~/.claude/projects/` (via `os.UserHomeDir()`)
- **Docker:** mount `~/.claude/projects/` as a volume, configure via `CLAUDE_PROJECTS_DIR` environment variable

Cross-platform: `os.UserHomeDir()` and `filepath` functions handle Linux, macOS, and Windows automatically. The `CLAUDE_PROJECTS_DIR` environment variable can override the default projects path on any platform.

## 12. References

1. cc-switch session manager: https://github.com/farion1231/cc-switch
2. cc-switch Claude provider implementation: `src-tauri/src/session_manager/providers/claude.rs`
3. cc-switch session UI: `src/components/sessions/`
