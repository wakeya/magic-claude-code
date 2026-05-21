# Claude Code Session Browser Validation

**Feature:** Claude Code 会话记录浏览器
**Status:** draft
**Last updated:** 2026-05-20

## Acceptance Criteria

1. Sessions are read from `~/.claude/projects/` without modifying proxy logic.
2. Projects are listed as primary navigation with session counts.
3. Selecting a project shows its sessions sorted by most recent activity.
4. Session detail displays messages in chronological order with correct roles.
5. User messages are highlighted and listed in the outline sidebar.
6. System prompts and tool results are collapsed by default.
7. Clicking an outline item scrolls to the matching message.
8. HTML export produces a self-contained `.html` file viewable offline.
9. The admin UI does not delete JSONL files or sidecar directories; the cleanup entry only displays Claude Code CLI command hints.
10. Agent session files (`agent-*`) are excluded from listing.
11. Corrupted or empty JSONL files do not crash the application.

## Automated Verification

Backend:

```bash
go test ./internal/session/... -v
go test ./internal/admin/... -run TestSession -v
go test ./... -cover
```

Frontend:

```bash
cd internal/frontend
npm run build
```

## Manual Verification

1. Ensure `~/.claude/projects/` is accessible (or mount in Docker).
2. Start the proxy/admin service.
3. Open the admin UI and navigate to the `会话记录` tab.
4. Verify the project list shows projects from `~/.claude/projects/`.
5. Select a project and verify sessions appear with correct titles.
6. Open a session and verify messages are displayed in order.
7. Verify user messages are highlighted and appear in the outline.
8. Click an outline item and verify scroll positioning.
9. Click "Export" and verify the downloaded HTML opens correctly in a browser.
10. Open the cleanup hint and verify it shows `claude project purge --dry-run <project-path>` plus interactive terminal cleanup guidance, and that no files are deleted by the admin panel.
11. Verify that the proxy continues to work normally (no proxy changes).

## Evidence Log

Implementation has not started. Record actual command output and manual observations here during validation.
