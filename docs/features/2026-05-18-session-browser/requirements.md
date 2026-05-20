# Claude Code Session Browser Requirements

**Version:** 0.2
**Date:** 2026-05-18
**Status:** draft
**Lifecycle state:** draft
**Source:** migrated from the legacy flat specs directory into feature-directory format

---

## 1. Objective

Add a local admin-page session browser for Claude Code conversations observed by this proxy.

The browser should show:

1. A left sidebar of sessions, defaulting to all sessions and sorted by most recent activity.
2. Project-based filtering using the best project name the proxy can infer.
3. A center column with the selected conversation content.
4. A right sidebar listing human `user` messages; clicking a user message scrolls the center column to that message.

## 2. Outcomes

The feature is successful when:

1. Conversation capture is explicitly opt-in and disabled by default.
2. Claude Code `/v1/messages` and `/anthropic/v1/messages` requests can be associated with a session.
3. Non-streaming assistant responses can be saved without affecting forwarding.
4. Streaming SSE assistant responses can be reconstructed without affecting forwarding.
5. Stored conversations can be browsed by project, session, message, and user-message outline.
6. Users can delete a session and its messages from the admin UI.

## 3. Evidence

Implementation must produce:

1. Unit tests for message normalization, truncation, session fingerprinting, and SSE reconstruction.
2. Store tests for session CRUD, request links, project grouping, pagination, and cascade deletion.
3. API handler tests for settings, project list, session list, session detail, outline, and deletion.
4. Frontend build verification.
5. Manual verification using a real Claude Code request after enabling capture.

## 4. Scope

Capture only provider-bound Claude Code messages requests:

1. `/v1/messages`
2. `/anthropic/v1/messages`
3. Equivalent provider messages paths after base URL joining.

Do not capture local hardcoded endpoints, OAuth, settings, quota, metrics, bootstrap, MCP registry, certificate routes, admin routes, or frontend assets.

Capture these request fields when present:

1. `system`
2. `messages`
3. `tools`
4. `tool_choice`
5. `model`
6. `stream`
7. mapped model
8. provider ID, provider name, API URL, source entrypoint, status code, duration, and usage status when available

## 5. Privacy Constraints

Conversation content is sensitive.

Required behavior:

1. Default `session_capture_enabled=false`.
2. The admin UI must show a warning before enabling capture.
3. Do not save `Authorization`, `X-Api-Key`, `Cookie`, or provider tokens.
4. Save provider errors as summaries, not full raw responses.
5. Default `tool_result` storage to a smaller limit than normal messages.
6. Mark truncated content with `truncated=true`.
7. Support deleting one session and all of its messages.

Default limits:

```text
session_message_max_bytes = 262144
session_tool_result_max_bytes = 65536
session_request_body_max_bytes = 2097152
session_retention_days = 30
```

The first implementation can store `session_retention_days` without automatic cleanup.

## 6. Session Grouping

Use this priority:

1. If a stable session ID is found in request metadata or headers, use it.
2. If project path is known, group by `project_path + source_entrypoint + conversation_fingerprint`.
3. If project path is unknown, group under `Unknown Project` and use request time plus message fingerprint.

`conversation_fingerprint` should use:

1. First user message text hash.
2. System prompt hash.
3. Source entrypoint.
4. Project path or `unknown`.

The grouping strategy must be conservative. Creating extra sessions is better than merging unrelated sessions.

## 7. Project Identification

Project grouping uses:

1. Current working directory or project path parsed from Claude Code `system` content.
2. Useful entrypoint hints from headers or User-Agent.
3. Path basename as `project_name` when a path is available.
4. Fallback:

```text
project_name = "Unknown Project"
project_path = ""
project_name_source = "unknown"
```

Allowed `project_name_source` values:

```text
system
header
derived_path
unknown
```

## 8. Message Model

The center column displays messages in stable sequence order.

Supported message types:

| Type | Source | Display |
|------|--------|---------|
| `system` | request `system` | collapsed by default |
| `user` | request `messages.role=user` | highlighted and included in right outline |
| `assistant` | provider response or request history | text and tool use content |
| `tool_use` | assistant content block | tool name and input summary |
| `tool_result` | user content block | summary with expandable content |
| `error` | provider or network error | error summary |

Large, binary, image, and unknown payloads should store type, size, and summary instead of raw content.

## 9. Streaming Reconstruction

The SSE observer must support these Anthropic-style events:

```text
message_start
content_block_start
content_block_delta
content_block_stop
message_delta
message_stop
error
```

Rules:

1. Append `text_delta` to the current assistant text block.
2. Append `input_json_delta` to the current tool input buffer.
3. Finalize the current block on `content_block_stop`.
4. Finalize the assistant message on `message_stop`.
5. Convert SSE `error` into an `error` message summary.
6. Ignore locally injected heartbeat events.
7. Capture failures must not change response forwarding.

## 10. Storage Requirements

Create these SQLite tables:

```sql
CREATE TABLE IF NOT EXISTS conversation_sessions (
  id TEXT PRIMARY KEY,
  project_name TEXT NOT NULL DEFAULT 'Unknown Project',
  project_path TEXT NOT NULL DEFAULT '',
  project_name_source TEXT NOT NULL DEFAULT 'unknown',
  title TEXT NOT NULL DEFAULT '',
  source_entrypoint TEXT NOT NULL DEFAULT '',
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  request_count INTEGER NOT NULL DEFAULT 0,
  message_count INTEGER NOT NULL DEFAULT 0,
  last_provider_id TEXT NOT NULL DEFAULT '',
  last_provider_name TEXT NOT NULL DEFAULT '',
  last_model TEXT NOT NULL DEFAULT '',
  capture_status TEXT NOT NULL DEFAULT 'ok'
);

CREATE TABLE IF NOT EXISTS conversation_messages (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  request_id TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL,
  message_type TEXT NOT NULL,
  content_text TEXT NOT NULL DEFAULT '',
  content_json TEXT NOT NULL DEFAULT '',
  tool_name TEXT NOT NULL DEFAULT '',
  sequence INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  token_input INTEGER NOT NULL DEFAULT 0,
  token_output INTEGER NOT NULL DEFAULT 0,
  truncated INTEGER NOT NULL DEFAULT 0,
  capture_status TEXT NOT NULL DEFAULT 'ok',
  FOREIGN KEY (session_id) REFERENCES conversation_sessions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS conversation_request_links (
  request_id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  request_sequence INTEGER NOT NULL,
  started_at TEXT NOT NULL,
  status_code INTEGER,
  usage_source TEXT NOT NULL DEFAULT 'none',
  FOREIGN KEY (session_id) REFERENCES conversation_sessions(id) ON DELETE CASCADE
);
```

Required indexes:

```sql
CREATE INDEX IF NOT EXISTS idx_conversation_sessions_recent ON conversation_sessions(last_seen_at);
CREATE INDEX IF NOT EXISTS idx_conversation_sessions_project ON conversation_sessions(project_name, last_seen_at);
CREATE INDEX IF NOT EXISTS idx_conversation_sessions_entrypoint ON conversation_sessions(source_entrypoint, last_seen_at);
CREATE INDEX IF NOT EXISTS idx_conversation_messages_session_sequence ON conversation_messages(session_id, sequence);
CREATE INDEX IF NOT EXISTS idx_conversation_messages_request ON conversation_messages(request_id);
CREATE INDEX IF NOT EXISTS idx_conversation_messages_role ON conversation_messages(session_id, role, sequence);
```

## 11. Admin API

Add these authenticated routes:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/conversations/settings` | Return capture settings |
| `PUT` | `/api/conversations/settings` | Update capture settings and limits |
| `GET` | `/api/conversations/projects` | Return project groups and counts |
| `GET` | `/api/conversations/sessions` | Return paginated session list |
| `GET` | `/api/conversations/sessions/{id}` | Return session detail and messages |
| `GET` | `/api/conversations/sessions/{id}/outline` | Return user-message outline |
| `DELETE` | `/api/conversations/sessions/{id}` | Delete one session |

Supported query parameters:

```text
project_name
from
to
provider_id
model
capture_status
page
page_size
```

`capture_status` accepts:

```text
all
ok
partial
failed
```

## 12. Frontend Requirements

Add a top-level tab:

```text
状态 / Providers / 证书 / 使用统计 / 会话记录
```

The session browser should use a wider layout than the current status/provider pages:

```text
max-width: 1600px
```

Left sidebar:

1. Project filter with `All Projects` default.
2. Project list with counts.
3. Session list sorted by `last_seen_at DESC`.
4. Session card fields: title, project name, provider, model, last updated, message count, capture status.

Center column:

1. Header with title, project, provider, model, and time range.
2. Ordered messages.
3. User messages visually distinct.
4. Assistant messages support text, tool use, and tool result blocks.
5. System and large tool result messages collapsed by default.
6. Scroll target for each user message.

Right sidebar:

1. Only `role=user` messages.
2. Summary and timestamp.
3. Click scrolls the center column to the corresponding message.
4. Current user message is highlighted based on scroll position.

Empty and disabled states:

1. Disabled capture state explains that full conversations may contain sensitive content and provides an enable action.
2. Enabled empty state says no sessions are available yet.

## 13. Non-Goals

1. No cloud sync.
2. No multi-instance session merge.
3. No attempt to fully reconstruct Claude Code local internal state.
4. No provider token or sensitive header persistence.
5. No usage aggregation from conversation content.
6. No full-text search in the first version.
7. No Markdown or JSON export in the first version.

## 14. Edge Cases

1. Capture disabled: no conversation rows are written.
2. Request body exceeds `session_request_body_max_bytes`: skip capture and leave proxy forwarding unchanged.
3. Unknown project: group under `Unknown Project`.
4. SSE parse failure: save partial content when possible and mark capture status `partial` or `failed`.
5. Client disconnect: finalize whatever was captured and mark status `partial`.
6. Non-2xx provider response: store a summarized `error` message without consuming the response body needed for forwarding.
7. Duplicate request replay: use request link uniqueness to avoid duplicate messages for the same request ID.

## 15. References

1. Anthropic streaming events and usage: https://platform.claude.com/docs/en/build-with-claude/streaming
2. cc-switch: https://github.com/farion1231/cc-switch
