# Claude Code Session Browser Decisions

## D-001: Read Local Files Instead of Proxy Capture

**Date:** 2026-05-20
**Status:** accepted

**Context:** The original v0.2 approach captured conversations by intercepting proxy traffic, reconstructing SSE streams, and grouping sessions by fingerprint. This required modifying the proxy handler, building an SSE observer, and guessing session boundaries.

Claude Code already stores complete session transcripts as JSONL files under `~/.claude/projects/`, including native sessionId, project path, and custom titles.

**Decision:** Read JSONL files directly from the local filesystem. No proxy modification needed.

**Consequences:** Eliminates SSE observer, normalizer, fingerprint, and privacy opt-in complexity. Session grouping is accurate. Requires the Claude projects directory to be accessible (local default `~/.claude/projects/`, Docker mount or override via `CLAUDE_PROJECTS_DIR`).

**Revisit when:** Multi-instance deployment needs to aggregate sessions from different machines.

## D-002: Project Directory as Session Filter

**Date:** 2026-05-20
**Status:** accepted

**Context:** cc-switch displays sessions in a flat list with a provider filter. Project directory is shown as metadata on each session card but is not used to locate sessions.

Claude Code organizes sessions by project directory in its filesystem layout (`~/.claude/projects/<encoded-path>/`). Users naturally think of sessions in the context of projects.

**Decision:** Projects are the first-level filter for the session list. Selecting a project shows its sessions. An "All Projects" option shows sessions across projects. The concrete layout is defined by D-006 as a left project dropdown plus session list.

**Consequences:** The UI keeps a clear two-level relationship (project filter → session list). Users can quickly find sessions for a specific project without scrolling through unrelated sessions.

**Revisit when:** Users have many projects and need project grouping (e.g., by parent directory or workspace).

## D-003: Direct File Reading with In-Memory Cache

**Date:** 2026-05-20
**Status:** accepted

**Context:** Session data lives in JSONL files that we don't control. Options for performance: (a) read files on every API call, (b) SQLite index, (c) in-memory cache with TTL.

**Decision:** Read files on demand with an in-memory cache (TTL ~30s). For listing, only read first/last lines of each JSONL. For detail, parse the full file.

**Consequences:** Simpler than maintaining a SQLite index. No sync issues. Cache TTL means the list may be slightly stale but a manual refresh is always available. Adequate for a single-user admin panel.

**Revisit when:** Users report slow listing with 1000+ sessions or need full-text search.

## D-004: Server-Side HTML Rendering for Export

**Date:** 2026-05-20
**Status:** accepted

**Context:** Exported HTML needs to work offline with no external dependencies. Options: (a) render server-side with Go template, (b) generate client-side with JS.

**Decision:** Render HTML server-side using `html/template`. The exported file contains inline CSS, server-rendered messages, and minimal JS for collapse toggles.

**Consequences:** Export works without JavaScript enabled. Simple to implement with Go standard library. No need for a Markdown library in v1 — plain text with basic formatting is sufficient.

**Revisit when:** Users need rich Markdown rendering (code highlighting, tables, links) in exported HTML.

## D-005: Admin UI Does Not Delete Claude Code Session Files

**Date:** 2026-05-20
**Status:** accepted

**Context:** Claude Code session JSONL files and sidecar directories are Claude Code-owned state. Deleting them directly from the admin panel would introduce accidental deletion, path traversal, symlink, and concurrent-write risks, and may violate Claude Code's own assumptions about project state.

**Decision:** The session browser does not expose an API that deletes JSONL files or sidecar directories, and the frontend does not perform deletion. When cleanup is needed, the UI only displays Claude Code CLI command hints. For project-level cleanup, prefer `claude project purge --dry-run <project-path>` as a preview, then tell the user to run `claude project purge -i <project-path>` in a terminal for interactive confirmation. Session-level cleanup depends on the current Claude Code CLI version and should be checked with its help output.

**Consequences:** The feature remains read-only and avoids destructive file operations. Users can still clean up via Claude Code's official CLI, but the admin panel is not responsible for file deletion.

**Revisit when:** Claude Code provides a stable, supported single-session deletion CLI/API with clear file and index consistency semantics.

## D-006: Left Project Dropdown and Session List, Right Session Detail

**Date:** 2026-05-21
**Status:** accepted

**Context:** The session browser had two viable layouts:

1. Left panel shows projects only; right panel switches between a session-list page and a session-detail page.
2. Left panel uses a project dropdown at the top, shows the current project's session list below, and the right panel always shows session detail.

The first layout keeps project navigation visually pure, but the session list disappears after opening detail. Switching to another session in the same project requires going back to the list. The frequent workflow is reviewing and switching between sessions, so keeping the session list visible is more important.

**Decision:** Use the second layout. The left panel has a project dropdown with "All Projects" and concrete projects, then an independently scrolling session list for the selected project. The right panel shows the selected session detail, export action, cleanup hint action, and user-message outline.

**Consequences:** Users can quickly switch between sessions in the same project without a back button. The left panel responsibility becomes "project filter + session navigation"; the right panel remains focused on detail reading and detail actions.

**Revisit when:** Project or session counts become too large for a single dropdown and left scroll list; add project search, session search, or list virtualization.
