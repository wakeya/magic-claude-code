# Claude Code Session Browser Decisions

## D-001: Session Capture Is Disabled By Default

**Date:** 2026-05-18
**Status:** accepted

**Context:** Conversation records can contain source code, file paths, terminal output, tool results, secrets copied into prompts, and provider error details.

**Decision:** Set `session_capture_enabled=false` by default. The admin UI must show an explicit warning before enabling capture.

**Consequences:** First-time users will not see historical conversations until they opt in. The project avoids silently storing sensitive content.

**Revisit when:** Users ask for import from Claude Code local history or a deployment mode needs managed retention and encryption.

## D-002: Keep Session Browser Separate From Usage Statistics

**Date:** 2026-05-18
**Status:** accepted

**Context:** Usage statistics stores request metadata and provider usage. Session browsing stores full message content and has different privacy, retention, and UI needs.

**Decision:** Implement the session browser as a separate top-level admin tab and separate data model. Usage records can link to sessions through request IDs, but usage aggregation must not depend on conversation content.

**Consequences:** The implementation has more tables and API routes, but the privacy boundary is clearer.

**Revisit when:** The UI needs a single investigation page that joins request metrics and conversation content.

## D-003: Use Conservative Session Grouping

**Date:** 2026-05-18
**Status:** accepted

**Context:** The proxy layer may not receive Claude Code's local session ID. Aggressive grouping can merge unrelated conversations.

**Decision:** Group by stable session ID when available. Otherwise group by project path, source entrypoint, and a conversation fingerprint. When uncertain, create a new session.

**Consequences:** The UI may show more sessions than Claude Code itself, but it avoids corrupting conversation history by merging unrelated chats.

**Revisit when:** A stable upstream session identifier is observed in real Claude Code CLI or VS Code extension traffic.

## D-004: Rebuild Streaming Assistant Messages In A Side Observer

**Date:** 2026-05-18
**Status:** accepted

**Context:** Claude Code commonly uses SSE. Response forwarding must remain byte-compatible with the current proxy behavior.

**Decision:** Add a side observer for SSE events. The observer can mark capture as partial or failed, but it must not mutate the forwarded response stream.

**Consequences:** Capture bugs should not break Claude Code. The implementation must test forwarding and capture separately.

**Revisit when:** The proxy response pipeline is refactored into reusable stream middleware.
