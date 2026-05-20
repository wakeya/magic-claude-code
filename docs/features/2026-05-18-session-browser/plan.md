# Claude Code Session Browser Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in local conversation browser for Claude Code messages observed by the proxy.

**Architecture:** Add a small conversation domain package for normalization, truncation, session grouping, and SSE reconstruction. Persist sessions/messages in SQLite through a focused store, expose authenticated admin APIs, then add a Vue three-column browser tab. Capture runs as a side observer and must not mutate request or response forwarding.

**Tech Stack:** Go 1.26, `net/http`, `modernc.org/sqlite`, Vue 3, TypeScript, Vite.

---

## File Map

Create:

1. `internal/conversation/types.go`: domain types for settings, sessions, messages, outlines, filters, and capture statuses.
2. `internal/conversation/normalizer.go`: request message extraction, project detection, content truncation, and message summaries.
3. `internal/conversation/normalizer_test.go`: unit tests for request parsing, truncation, project detection, and message typing.
4. `internal/conversation/fingerprint.go`: conservative session fingerprint generation.
5. `internal/conversation/fingerprint_test.go`: deterministic fingerprint and grouping tests.
6. `internal/conversation/sse.go`: Anthropic-style SSE observer and assistant message reconstruction.
7. `internal/conversation/sse_test.go`: streaming reconstruction tests.
8. `internal/conversation/store.go`: SQLite conversation store and query methods.
9. `internal/conversation/store_test.go`: schema, CRUD, filtering, pagination, and cascade deletion tests.
10. `internal/admin/conversation_handler.go`: authenticated admin API handlers.
11. `internal/admin/conversation_handler_test.go`: API tests.
12. `internal/frontend/src/components/ConversationBrowser.vue`: three-column session browser UI.
13. `internal/frontend/src/components/ConversationMessage.vue`: message renderer.
14. `internal/frontend/src/components/ConversationOutline.vue`: user-message outline.

Modify:

1. `internal/config/config.go`: add session capture settings to config defaults.
2. `internal/config/sqlite_store.go`: persist settings and create conversation tables.
3. `internal/config/sqlite_store_test.go`: verify default and persisted session settings.
4. `internal/proxy/handler.go`: create capture context after request body read and observe responses.
5. `internal/proxy/server.go`: pass conversation store/capture service into the handler.
6. `internal/admin/server.go`: register `/api/conversations/*` routes.
7. `internal/frontend/src/composables/useApi.ts`: add conversation API client types and methods.
8. `internal/frontend/src/composables/useI18n.ts`: add zh/en strings.
9. `internal/frontend/src/views/DashboardView.vue`: add the `会话记录` tab and wide-layout behavior.

## Task 1: Add Conversation Domain Types

**Files:**

1. Create: `internal/conversation/types.go`

- [ ] **Step 1: Create domain constants and structs**

Add:

```go
package conversation

import "time"

type CaptureStatus string

const (
	CaptureStatusOK      CaptureStatus = "ok"
	CaptureStatusPartial CaptureStatus = "partial"
	CaptureStatusFailed  CaptureStatus = "failed"
)

type ProjectNameSource string

const (
	ProjectNameSourceSystem      ProjectNameSource = "system"
	ProjectNameSourceHeader      ProjectNameSource = "header"
	ProjectNameSourceDerivedPath ProjectNameSource = "derived_path"
	ProjectNameSourceUnknown     ProjectNameSource = "unknown"
)

type Settings struct {
	Enabled             bool `json:"enabled"`
	MessageMaxBytes     int  `json:"message_max_bytes"`
	ToolResultMaxBytes  int  `json:"tool_result_max_bytes"`
	RequestBodyMaxBytes int  `json:"request_body_max_bytes"`
	RetentionDays       int  `json:"retention_days"`
}

func DefaultSettings() Settings {
	return Settings{
		Enabled:             false,
		MessageMaxBytes:     262144,
		ToolResultMaxBytes:  65536,
		RequestBodyMaxBytes: 2097152,
		RetentionDays:       30,
	}
}

type Session struct {
	ID                string        `json:"id"`
	ProjectName       string        `json:"project_name"`
	ProjectPath       string        `json:"project_path"`
	ProjectNameSource string        `json:"project_name_source"`
	Title             string        `json:"title"`
	SourceEntrypoint  string        `json:"source_entrypoint"`
	FirstSeenAt       time.Time     `json:"first_seen_at"`
	LastSeenAt        time.Time     `json:"last_seen_at"`
	RequestCount      int           `json:"request_count"`
	MessageCount      int           `json:"message_count"`
	LastProviderID    string        `json:"last_provider_id"`
	LastProviderName  string        `json:"last_provider_name"`
	LastModel         string        `json:"last_model"`
	CaptureStatus     CaptureStatus `json:"capture_status"`
}

type Message struct {
	ID            string        `json:"id"`
	SessionID     string        `json:"session_id"`
	RequestID     string        `json:"request_id"`
	Role          string        `json:"role"`
	MessageType   string        `json:"message_type"`
	ContentText   string        `json:"content_text"`
	ContentJSON   string        `json:"content_json"`
	ToolName      string        `json:"tool_name"`
	Sequence      int           `json:"sequence"`
	CreatedAt     time.Time     `json:"created_at"`
	TokenInput     int           `json:"token_input"`
	TokenOutput    int           `json:"token_output"`
	Truncated     bool          `json:"truncated"`
	CaptureStatus CaptureStatus `json:"capture_status"`
}

type OutlineItem struct {
	MessageID   string    `json:"message_id"`
	Sequence    int       `json:"sequence"`
	Summary     string    `json:"summary"`
	CreatedAt   time.Time `json:"created_at"`
	Truncated   bool      `json:"truncated"`
}

type SessionFilters struct {
	ProjectName   string
	From          time.Time
	To            time.Time
	ProviderID    string
	Model         string
	CaptureStatus string
	Page          int
	PageSize      int
}
```

- [ ] **Step 2: Run package test**

Run: `go test ./internal/conversation`

Expected: fails until the package has at least one test file or reports no test files after compilation succeeds.

## Task 2: Add Request Normalization And Truncation

**Files:**

1. Create: `internal/conversation/normalizer.go`
2. Create: `internal/conversation/normalizer_test.go`

- [ ] **Step 1: Write failing tests**

Tests to add:

1. `TestNormalizeRequestMessagesExtractsSystemUserToolResult`
2. `TestTruncateMessageMarksTruncated`
3. `TestDetectProjectFromSystemPath`
4. `TestNormalizeSkipsOversizedRequest`

Run: `go test ./internal/conversation -run 'TestNormalize|TestTruncate|TestDetectProject' -v`

Expected: fail because functions are missing.

- [ ] **Step 2: Implement minimal normalizer API**

Define:

```go
type RequestMetadata struct {
	RequestID        string
	ProviderID       string
	ProviderName     string
	MappedModel      string
	SourceEntrypoint string
	StartedAt        time.Time
	Headers          http.Header
}

type NormalizedRequest struct {
	Model             string
	ProjectName       string
	ProjectPath       string
	ProjectNameSource ProjectNameSource
	Title             string
	Messages          []Message
	Skipped           bool
	SkipReason        string
}

func NormalizeRequest(body []byte, settings Settings, meta RequestMetadata) NormalizedRequest
func SummarizeText(text string, maxBytes int) (summary string, truncated bool)
```

Implementation requirements:

1. Return `Skipped=true` when `len(body) > settings.RequestBodyMaxBytes`.
2. Convert request `system` into `message_type=system`.
3. Convert `messages[].role=user` text into `message_type=user`.
4. Convert user `tool_result` blocks into `message_type=tool_result`.
5. Preserve sequence order.
6. Use `Unknown Project` fallback.

- [ ] **Step 3: Verify tests pass**

Run: `go test ./internal/conversation -run 'TestNormalize|TestTruncate|TestDetectProject' -v`

Expected: PASS.

## Task 3: Add Fingerprinting

**Files:**

1. Create: `internal/conversation/fingerprint.go`
2. Create: `internal/conversation/fingerprint_test.go`

- [ ] **Step 1: Write failing fingerprint tests**

Tests:

1. `TestConversationFingerprintIsStable`
2. `TestConversationFingerprintChangesByProject`
3. `TestConversationFingerprintUsesUnknownProjectFallback`

Run: `go test ./internal/conversation -run TestConversationFingerprint -v`

Expected: fail because `ConversationFingerprint` is missing.

- [ ] **Step 2: Implement fingerprint**

Define:

```go
func ConversationFingerprint(projectPath, sourceEntrypoint, systemText, firstUserText string) string
```

Rules:

1. Normalize whitespace before hashing.
2. Use `unknown` when project path is empty.
3. Return a hex SHA-256 string.

- [ ] **Step 3: Verify**

Run: `go test ./internal/conversation -run TestConversationFingerprint -v`

Expected: PASS.

## Task 4: Add SSE Reconstruction

**Files:**

1. Create: `internal/conversation/sse.go`
2. Create: `internal/conversation/sse_test.go`

- [ ] **Step 1: Write failing SSE tests**

Tests:

1. `TestSSEReconstructsTextDelta`
2. `TestSSEReconstructsToolInputJSONDelta`
3. `TestSSEErrorCreatesErrorMessage`
4. `TestSSEIgnoresHeartbeatComments`

Run: `go test ./internal/conversation -run TestSSE -v`

Expected: fail because `SSEObserver` is missing.

- [ ] **Step 2: Implement observer**

Define:

```go
type SSEObserver struct {
	// unexported buffers
}

func NewSSEObserver(requestID string, startedAt time.Time) *SSEObserver
func (o *SSEObserver) ObserveLine(line []byte)
func (o *SSEObserver) Finish() []Message
func (o *SSEObserver) Status() CaptureStatus
```

Rules:

1. Parse `event:` and `data:` lines.
2. Ignore lines beginning with `:`.
3. Append `text_delta`.
4. Append `input_json_delta`.
5. Mark malformed JSON as `partial`, not panic.

- [ ] **Step 3: Verify**

Run: `go test ./internal/conversation -run TestSSE -v`

Expected: PASS.

## Task 5: Add SQLite Store

**Files:**

1. Create: `internal/conversation/store.go`
2. Create: `internal/conversation/store_test.go`
3. Modify: `internal/config/sqlite_store.go`

- [ ] **Step 1: Write failing store tests**

Tests:

1. `TestStoreMigratesConversationTables`
2. `TestStoreUpsertsSessionAndMessages`
3. `TestStoreListsProjectsWithCounts`
4. `TestStoreListsSessionsWithFilters`
5. `TestStoreDeleteSessionCascadesMessages`

Run: `go test ./internal/conversation -run TestStore -v`

Expected: fail because store does not exist.

- [ ] **Step 2: Implement schema migration helper**

Add a function in `internal/conversation/store.go`:

```go
func Migrate(db *sql.DB) error
```

It must create the three tables and six indexes from `requirements.md`.

- [ ] **Step 3: Call migration from config store initialization**

In `internal/config/sqlite_store.go`, after existing config tables are created, call `conversation.Migrate(s.db)`.

- [ ] **Step 4: Implement store methods**

Define:

```go
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store
func (s *Store) GetSettings(ctx context.Context) (Settings, error)
func (s *Store) SaveSettings(ctx context.Context, settings Settings) error
func (s *Store) UpsertSession(ctx context.Context, session Session) error
func (s *Store) AddMessages(ctx context.Context, messages []Message) error
func (s *Store) LinkRequest(ctx context.Context, requestID, sessionID string, requestSequence int, startedAt time.Time, statusCode int, usageSource string) error
func (s *Store) ListProjects(ctx context.Context) ([]ProjectSummary, error)
func (s *Store) ListSessions(ctx context.Context, filters SessionFilters) ([]Session, int, error)
func (s *Store) GetSession(ctx context.Context, id string) (Session, []Message, error)
func (s *Store) GetOutline(ctx context.Context, id string) ([]OutlineItem, error)
func (s *Store) DeleteSession(ctx context.Context, id string) error
```

- [ ] **Step 5: Verify store tests**

Run: `go test ./internal/conversation -run TestStore -v`

Expected: PASS.

## Task 6: Integrate Proxy Capture

**Files:**

1. Modify: `internal/proxy/handler.go`
2. Modify: `internal/proxy/server.go`
3. Add tests in: `internal/proxy/server_test.go` or `internal/proxy/handler_test.go`

- [ ] **Step 1: Write failing proxy tests**

Tests:

1. `TestProxyDoesNotCaptureWhenDisabled`
2. `TestProxyCapturesNonStreamingConversation`
3. `TestProxyCapturesStreamingConversationWithoutChangingForwardedBody`
4. `TestProxyCaptureFailureStillForwardsResponse`

Run: `go test ./internal/proxy -run 'TestProxy.*Capture' -v`

Expected: fail because capture is not wired.

- [ ] **Step 2: Add optional capture dependency**

Extend `Handler` with an optional conversation capture service:

```go
type ConversationCapture interface {
	Start(r *http.Request, originalBody []byte, modifiedBody []byte, provider *config.Provider) (*conversation.CaptureContext, error)
	ObserveNonStreaming(ctx context.Context, cap *conversation.CaptureContext, statusCode int, body []byte) error
	WrapStreaming(ctx context.Context, cap *conversation.CaptureContext, statusCode int, body io.Reader) io.Reader
}
```

Keep `nil` capture as a no-op for existing tests.

- [ ] **Step 3: Preserve response forwarding**

For non-streaming responses, read the full body once, pass a copy to capture, then write the same bytes to the client.

For SSE responses, wrap `resp.Body` with an observing reader before `copyWithHeartbeat`.

- [ ] **Step 4: Verify proxy tests**

Run: `go test ./internal/proxy -run 'TestProxy.*Capture' -v`

Expected: PASS.

## Task 7: Add Admin Conversation API

**Files:**

1. Create: `internal/admin/conversation_handler.go`
2. Create: `internal/admin/conversation_handler_test.go`
3. Modify: `internal/admin/server.go`

- [ ] **Step 1: Write failing API tests**

Tests:

1. `TestConversationSettingsRequiresAuth`
2. `TestConversationSettingsRoundTrip`
3. `TestConversationProjects`
4. `TestConversationSessionsFilters`
5. `TestConversationSessionDetail`
6. `TestConversationOutlineOnlyUserMessages`
7. `TestConversationDeleteSession`

Run: `go test ./internal/admin -run TestConversation -v`

Expected: fail because routes are missing.

- [ ] **Step 2: Register routes**

In `Server.Start`, add:

```go
mux.HandleFunc("/api/conversations/settings", s.authMiddlewareFunc(s.handleConversationSettings))
mux.HandleFunc("/api/conversations/projects", s.authMiddlewareFunc(s.handleConversationProjects))
mux.HandleFunc("/api/conversations/sessions", s.authMiddlewareFunc(s.handleConversationSessions))
mux.HandleFunc("/api/conversations/sessions/", s.authMiddlewareFunc(s.handleConversationSessionRoutes))
```

- [ ] **Step 3: Implement handlers**

Requirements:

1. Reject invalid methods with HTTP 405.
2. Clamp `page_size` to `1..100`.
3. Return JSON errors with `{"error":"..."}`.
4. Do not expose provider tokens or sensitive headers.

- [ ] **Step 4: Verify admin tests**

Run: `go test ./internal/admin -run TestConversation -v`

Expected: PASS.

## Task 8: Add Frontend API Client And Components

**Files:**

1. Modify: `internal/frontend/src/composables/useApi.ts`
2. Modify: `internal/frontend/src/composables/useI18n.ts`
3. Create: `internal/frontend/src/components/ConversationBrowser.vue`
4. Create: `internal/frontend/src/components/ConversationMessage.vue`
5. Create: `internal/frontend/src/components/ConversationOutline.vue`
6. Modify: `internal/frontend/src/views/DashboardView.vue`

- [ ] **Step 1: Add TypeScript API types**

Add interfaces mirroring the backend JSON:

```ts
export interface ConversationSettings
export interface ConversationProject
export interface ConversationSession
export interface ConversationMessage
export interface ConversationOutlineItem
export interface ConversationDetail
```

- [ ] **Step 2: Add API methods**

Add:

```ts
getConversationSettings()
updateConversationSettings(settings)
getConversationProjects()
getConversationSessions(params)
getConversationSession(id)
getConversationOutline(id)
deleteConversationSession(id)
```

- [ ] **Step 3: Build components**

`ConversationBrowser.vue` owns data loading and selected session state.

`ConversationMessage.vue` renders one message and supports collapsed `system` and `tool_result` content.

`ConversationOutline.vue` renders user-message anchors and emits selected message IDs.

- [ ] **Step 4: Add tab and layout**

In `DashboardView.vue`:

1. Add `sessions` to `tabs`.
2. Use `max-w-[1600px]` when active tab is `sessions`.
3. Render `<ConversationBrowser />`.

- [ ] **Step 5: Verify frontend build**

Run:

```bash
cd internal/frontend
npm run build
```

Expected: Vite build succeeds.

## Task 9: Full Verification

**Files:**

1. Update: `docs/features/2026-05-18-session-browser/validation.md`
2. Update: `docs/features/2026-05-18-session-browser/status.md`
3. Update: `docs/features/2026-05-18-session-browser/decisions.md` if implementation choices changed.

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

Run the app, enable session capture in the admin UI, make one Claude Code request, then verify:

1. Session appears in the left sidebar.
2. Project grouping works or falls back to `Unknown Project`.
3. Center column shows user and assistant messages.
4. Right outline lists only user messages.
5. Clicking outline scrolls to the matching center message.
6. Deleting the session removes it from the list.

- [ ] **Step 4: Record evidence**

Write command outputs and manual observations into `validation.md`.

- [ ] **Step 5: Update lifecycle**

Set `status.md` to `validated` only after all required verification passes.
