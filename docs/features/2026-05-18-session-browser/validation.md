# Claude Code Session Browser Validation

**Feature:** Claude Code 会话记录浏览器
**Status:** draft
**Last updated:** 2026-05-18

## Acceptance Criteria

1. Capture is disabled by default.
2. Enabling capture requires an explicit admin UI action.
3. No provider tokens or sensitive request headers are persisted.
4. Non-streaming conversations are saved and forwarded unchanged.
5. Streaming SSE conversations are reconstructed and forwarded unchanged.
6. Project grouping uses the configured conservative fallback behavior.
7. Session list supports project, time, provider, model, capture status, and pagination filters.
8. Session detail returns messages in stable sequence order.
9. Outline contains only `user` messages.
10. Deleting a session removes linked messages and request links.

## Automated Verification

Backend:

```bash
go test ./internal/conversation
go test ./internal/proxy -run 'TestProxy.*Capture' -v
go test ./internal/admin -run TestConversation -v
go test ./...
```

Frontend:

```bash
cd internal/frontend
npm run build
```

## Manual Verification

1. Start the proxy/admin service.
2. Open the admin UI.
3. Open the `会话记录` tab.
4. Verify the disabled state explains privacy risk.
5. Enable capture.
6. Send one Claude Code CLI request through the proxy.
7. Verify a session appears.
8. Open the session.
9. Verify center messages and right-side user outline.
10. Click a user outline item and verify scroll positioning.
11. Delete the session and verify it disappears.

## Evidence Log

Implementation has not started. Record actual command output and manual observations here during validation.
