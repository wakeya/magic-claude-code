# Claude Code 2.1.211 Endpoint Compatibility Spec

Local page: none (proxy backend only)
Proxy entry: `internal/proxy/handler.go` → `handleHardcodedEndpoint` (`internal/proxy/hardcoded.go:107`)
Reference source: `claude_code_src_2.1.211.js` (vs `2.1.206`, under `073_claude_spy/`)
Stack: Go 1.26 standard library (`net/http`)
Last updated: 2026-07-15
Progress: 0 / 4
Tracking issue: [#17](https://github.com/wakeya/magic-claude-code/issues/17)

## Overall Analysis (Source Analysis)

### Analysis Method

1. Bidirectional `comm` diff of API path literals and template strings between `2.1.206` and `2.1.211` (with `${...}` placeholders stripped) to locate paths newly added across versions.
2. Filter for "live" runtime requests using the `await client.get/post(...)` pattern, excluding Anthropic SDK class constants (`/v1/agents`, `/v1/vaults`, etc.) and cURL documentation examples (the `# Claude API — cURL / Raw HTTP` section).
3. Inspect each client-side response handling (`validateStatus`, failure branch, retry behavior, thrown exceptions) to determine the impact of a 404.

### Security Premise

`classifyForwardingEndpoint` (`internal/proxy/endpoint_policy.go:31`) uses **exact map matching**; the forward allowlist is only:

```go
var modelForwardPaths = map[string]struct{}{
    "/v1/messages":           {},
    "/anthropic/v1/messages": {},
}
```

None of the 8 endpoints below hit the forward allowlist; they currently all flow through `handleBlockedEndpoint` and return a local `404 mcc_blocked_unknown_endpoint`. **They are never forwarded to a third-party provider; there is no data-leak risk.** This spec only addresses functional compatibility (avoiding client exceptions / telemetry noise); it does not involve forwarding or leakage.

### Group A — Current 404 Is Already Optimal, Keep As-Is (5)

| Endpoint | Client response handling (source evidence) | Conclusion |
|----------|--------------------------------------------|------------|
| `GET /v1/code/local/memory/mounts` | `validateStatus:(r)=>r===200\|\|r===404`; `status===404 → {kind:"off"}`, cached 24h (`aLg=86400000`) | 404 is the client-defined "memory not enabled" normal branch; acts as the discovery gate that short-circuits the whole memory chain |
| `POST /v1/code/local/memory/credential` | `validateStatus:()=>!0`; invoked only after discovery(mounts) returns `{kind:"stores"}` | Not reached when mounts=404→off |
| `/v1/code/memory/*` (read/write) | Depends on discovery returning stores | Not currently triggered |
| `POST /v1/code/github/import-token` | Uploads `token.reveal()` (GitHub token); failure → `{ok:false}` | Security-sensitive: 404 makes login fail, token drained and never leaked |
| `POST /v1/filestore/fs/readFile` | `if(!i.ok) return {ok:false}` + warn `stage_file_read_gated` | Graceful degradation |

### Group B — Hardcoded Compatibility Response Needed (3 paths)

| Endpoint | Client response handling | Local response | Rationale |
|----------|--------------------------|----------------|-----------|
| `GET /v1/design/grants` | `status!==200→null`; 404 emits telemetry `probe_404_old_server` | `200 {grants:[]}` | Empty grant list disables Design authorization, avoids 404 telemetry noise |
| `POST /v1/design/grants` | `validateStatus:(n)=>n<300\|\|n===404`; `!ok→throw Error` | `403 {error,reason:"write_gate_disabled"}` | Consistent with the frame deploy write-gate; explicit "write closed" rather than 404 |
| `GET /v1/ultrareview/preflight` | `if(!t.ok) switch(t.reason){...}` | `200 {}` | Consistent with sibling `/v1/ultrareview/quota` (`handleEmptyResponse`) |
| `GET /v1/code/triggers` | `if(!e.ok) throw Error("triggers unavailable")` (worst tolerance) | `200 {data:[]}` | Empty list avoids the exception; POST writes return 403 |

> `/v1/design/grants` is a single path split into GET/POST responses; `/v1/code/triggers` uses a prefix to cover both the list and `/triggers/{id}` sub-paths.

### Risk Summary

1. **Do not over-activate the memory chain**: Group A keeping 404 is intentional. Returning 200 + fake data for mounts would activate credential / memory read-write and introduce a larger behavior surface, increasing risk.
2. **Triggers semantic imperfection**: `GET /v1/code/triggers/{id}` (single item) also returns `{data:[]}` (list shape) — semantically imperfect. Acceptable since third-party users do not use CCR triggers; the goal is merely "no exception thrown".
3. **The grants POST 403 must carry `reason:"write_gate_disabled"`**: the client uses this to recognize the write-gate is closed (mirrors deploy init/direct at `frame.go:50`).
4. **`modelForwardPaths` unchanged**: none of the 8 are model-inference endpoints; the forward allowlist stays as-is.

## Development Checklist

| # | Status | Task | Output | Verification |
|---|--------|------|--------|--------------|
| 1 | planned | design grants GET/POST split | `hardcoded.go` + `hardcoded_test.go` | unit: GET `{grants:[]}`, POST 403, PUT 405 |
| 2 | planned | ultrareview preflight joins quota group | `hardcoded.go` + `hardcoded_test.go` | unit: GET `200 {}` |
| 3 | planned | triggers prefix + dedicated handler | `hardcoded.go` + `triggers.go` + `triggers_test.go` | unit: GET `{data:[]}`, POST 403, sub-path covered |
| 4 | planned | baseline doc update 52→55 | `2026-07-15-intercepted-endpoints.md` | count-check script outputs 55 |

## Requirements

### Deliverables

1. `isHardcodedEndpoint` (`internal/proxy/hardcoded.go:19`) additions:
   - exactMatches: `/v1/design/grants`, `/v1/ultrareview/preflight`
   - prefixMatches: `/v1/code/triggers`
2. `handleHardcodedEndpoint` (`hardcoded.go:107`) switch new branches (exact insertion points in each task's Plan).
3. New file `internal/proxy/triggers.go` implementing `handleTriggersEndpoint`.
4. Unit tests covering all new branches (code in each task's Plan).
5. Update `sdd-docs/research/2026-07-15-intercepted-endpoints.md` (line-by-line changes in Task 4).

### Directory Layout

```text
internal/proxy/
  hardcoded.go          (modified: isHardcodedEndpoint + handleHardcodedEndpoint switch)
  hardcoded_test.go     (modified: 3 tables add paths + 2 new test funcs/subtests)
  triggers.go           (new: handleTriggersEndpoint)
  triggers_test.go      (new)
sdd-docs/research/
  2026-07-15-intercepted-endpoints.md  (modified: list 52→55)
sdd-docs/features/2026-07-15-cc-2.1.211-endpoint-compat/
  spec.md / spec_ZH.md  (new)
```

### Constraints

1. Do not modify `modelForwardPaths` (`endpoint_policy.go`).
2. `/v1/design/grants` splits by `r.Method`: GET→200, POST→403, other→405.
3. The 403 body for POST grants and POST triggers must contain `"reason":"write_gate_disabled"`.
4. `/v1/code/triggers` uses prefix matching (covers both `/triggers` and `/triggers/{id}`).
5. Group A's 5 endpoints keep their current behavior (fail-closed 404).
6. New handler functions must carry doc comments; test style matches existing `hardcoded_test.go`.

### Edge Cases

1. grants/preflight carrying a query (e.g. `?beta=true`) — `r.URL.Path` already strips the query.
2. `/v1/code/triggers/{id}` — matched by prefix, GET returns `{data:[]}`.
3. PUT/DELETE and other methods — return 405 + `Allow` header.
4. POST grants carrying a `project_id` body — `drainRequestBodyLimited` has drained it; not parsed.

### Non-Goals

1. No new local mock for Group A's 5 endpoints.
2. No real business logic for memory / design / triggers.
3. No non-model endpoint is added to the forward allowlist.
4. Anthropic SDK class-constant paths are not handled.

## Task Details

### Task 1: design grants GET/POST split

#### Requirements

**Objective** — Provide a local compatibility response for `/v1/design/grants`: GET returns empty grants (disabling Design authorization), POST returns write-gate closed.

**Outcomes** — `isHardcodedEndpoint` exactMatches adds `/v1/design/grants`; `handleHardcodedEndpoint` switch gains a case splitting by `r.Method`; `hardcoded_test.go` adds `TestHardcodedDesignGrants`.

**Evidence** — `go test ./internal/proxy/ -run TestHardcodedDesignGrants -v` — three subtests green.

**Constraints** — POST 403 body structure matches the write_gate at `frame.go:50`; forward allowlist unchanged.

**Edge Cases** — `?beta=true` (path already stripped); non-GET/POST methods; POST carrying project_id body.

**Verification** — `go test ./internal/proxy/ -run TestHardcodedDesignGrants -v` passes.

#### Plan

**Step 1.1** — In `isHardcodedEndpoint` exactMatches (`hardcoded.go:61-64`), add one line after `"/v1/design/mcp"`:

```go
		// Claude Design consent / MCP bridge
		"/v1/design/consent",
		"/v1/design/mcp",
		"/v1/design/grants", // GET empty grants / POST write-gate closed (CC 2.1.211)
```

**Step 1.2** — In the `handleHardcodedEndpoint` switch, insert a new case after the `/v1/design/mcp` case (`hardcoded.go:302-304`):

```go
	// Claude Design MCP bridge - POST returns controlled unsupported
	case path == "/v1/design/mcp":
		h.handleDesignMCP(w, r)
		return true

	// Claude Design grants - GET returns empty grants (disables Design auth); POST write-gate closed
	case path == "/v1/design/grants":
		switch r.Method {
		case http.MethodGet:
			writeJSONResponse(w, http.StatusOK, map[string]any{"grants": []any{}})
		case http.MethodPost:
			writeJSONResponse(w, http.StatusForbidden, map[string]any{
				"error":  "Design grants write is unavailable in MCC local mode",
				"reason": "write_gate_disabled",
			})
		default:
			w.Header().Set("Allow", "GET, POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return true
```

**Step 1.3** — In `hardcoded_test.go` `TestIsHardcodedEndpoint` table (exact-match area around `hardcoded_test.go:50-53`), add:

```go
		// CC 2.1.211 additions
		{"/v1/design/grants", true},
		{"/v1/ultrareview/preflight", true},
		{"/v1/code/triggers", true},
		{"/v1/code/triggers/t1", true},
```

**Step 1.4** — In `hardcoded_test.go` `TestHandleHardcodedEndpoint` table (`hardcoded_test.go:487-520`), add 4 rows:

```go
		{"/v1/design/grants"},
		{"/v1/ultrareview/preflight"},
		{"/v1/code/triggers"},
		{"/v1/code/triggers/t1"},
```

**Step 1.5** — Append `TestHardcodedDesignGrants` to `hardcoded_test.go`:

```go
func TestHardcodedDesignGrants(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	t.Run("GET returns empty grants", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/design/grants", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle design grants")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Grants []any `json:"grants"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Grants) != 0 {
			t.Errorf("grants = %v, want empty", resp.Grants)
		}
	})

	t.Run("POST returns 403 write_gate_disabled", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/design/grants", strings.NewReader(`{"project_id":"p1"}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		var resp struct {
			Reason string `json:"reason"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Reason != "write_gate_disabled" {
			t.Errorf("reason = %q, want write_gate_disabled", resp.Reason)
		}
	})

	t.Run("PUT returns 405 with Allow", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/v1/design/grants", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, POST" {
			t.Errorf("Allow = %q, want GET, POST", got)
		}
	})
}
```

#### Verification

- [ ] `GET /v1/design/grants` returns `200 {"grants":[]}`.
- [ ] `POST /v1/design/grants` returns `403`, body contains `"reason":"write_gate_disabled"`.
- [ ] `PUT /v1/design/grants` returns `405` + `Allow: GET, POST`.

### Task 2: ultrareview preflight joins the quota group

#### Requirements

**Objective** — `/v1/ultrareview/preflight` returns `200 {}`, consistent with sibling quota.

**Outcomes** — `isHardcodedEndpoint` exactMatches adds the path; `handleHardcodedEndpoint` low-priority case routes it to `handleEmptyResponse`; `TestHardcodedLowRiskClaudeCodeEndpoints` adds a subtest.

**Evidence** — Subtest `ultrareview preflight returns empty object` passes.

**Constraints** — Identical treatment to quota (`handleEmptyResponse` returns `200 {}`).

**Edge Cases** — Non-GET method — `handleEmptyResponse` does not check method, but the client only uses GET for preflight.

**Verification** — `go test ./internal/proxy/ -run TestHardcodedLowRiskClaudeCodeEndpoints -v` passes.

#### Plan

**Step 2.1** — In `isHardcodedEndpoint` exactMatches (`hardcoded.go:41`), add after `"/v1/ultrareview/quota"`:

```go
		"/v1/ultrareview/quota",
		"/v1/ultrareview/preflight", // CC 2.1.211: joins quota via handleEmptyResponse
```

**Step 2.2** — In the `handleHardcodedEndpoint` low-priority case (`hardcoded.go:312-327`), add one line after `path == "/v1/ultrareview/quota"` (`hardcoded.go:319`):

```go
		path == "/v1/ultrareview/quota",
		path == "/v1/ultrareview/preflight",
```

**Step 2.3** — Add rows to `TestIsHardcodedEndpoint` and `TestHandleHardcodedEndpoint` tables (already included in steps 1.3 and 1.4).

**Step 2.4** — In `TestHardcodedLowRiskClaudeCodeEndpoints` (`hardcoded_test.go:959`), after the onboarding subtest and before the count_tokens subtest, add:

```go
	t.Run("ultrareview preflight returns empty object", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/ultrareview/preflight", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle ultrareview preflight")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if strings.TrimSpace(rec.Body.String()) != "{}" {
			t.Errorf("body = %q, want {}", rec.Body.String())
		}
	})
```

#### Verification

- [ ] `GET /v1/ultrareview/preflight` returns `200 {}`.
- [ ] `GET /v1/ultrareview/quota` behavior unchanged (regression).

### Task 3: triggers prefix + dedicated handler

#### Requirements

**Objective** — `/v1/code/triggers` (including sub-paths) returns an empty list to avoid the client throwing `Error("triggers unavailable")`.

**Outcomes** — `isHardcodedEndpoint` prefixMatches adds `/v1/code/triggers`; new `internal/proxy/triggers.go` and `triggers_test.go`; `handleHardcodedEndpoint` switch gains a prefix case.

**Evidence** — `go test ./internal/proxy/ -run TestHardcodedTriggers -v` — all green (GET list/sub-path, POST, DELETE).

**Constraints** — Prefix match covers list and sub-paths; POST 403 carries `write_gate_disabled`; handler has a doc comment.

**Edge Cases** — get-single returns list shape (acceptable); deep sub-path `/triggers/t1/run`.

**Verification** — `go test ./internal/proxy/ -run TestHardcodedTriggers -v` passes.

#### Plan

**Step 3.1** — In `isHardcodedEndpoint` prefixMatches (`hardcoded.go:73-85`), add after `"/v1/code/sessions/"` (`hardcoded.go:82`):

```go
		"/v1/code/sessions/",
		"/v1/code/triggers", // CC 2.1.211: CCR triggers, local empty response
```

**Step 3.2** — Create `internal/proxy/triggers.go`:

```go
package proxy

import "net/http"

// handleTriggersEndpoint handles CCR trigger endpoints, all locally responded, never forwarded upstream.
//
// Routing contract:
//   - GET /v1/code/triggers or /v1/code/triggers/{id}[/run] -> 200 {"data":[]}
//   - POST (create) -> 403 write_gate_disabled
//   - other methods -> 405
//
// The request body has already been drained in handleHardcodedEndpoint.
// Third-party-provider users do not use CCR triggers; the goal is merely to
// prevent the client from throwing Error("triggers unavailable"); real
// trigger semantics are intentionally not implemented.
func (h *Handler) handleTriggersEndpoint(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSONResponse(w, http.StatusOK, map[string]any{"data": []any{}})
	case http.MethodPost:
		writeJSONResponse(w, http.StatusForbidden, map[string]any{
			"error":  "Triggers write is unavailable in MCC local mode",
			"reason": "write_gate_disabled",
		})
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
```

**Step 3.3** — In the `handleHardcodedEndpoint` switch, insert a prefix case before the `/api/ws/` case (`hardcoded.go:307-309`), right after the frame block:

```go
	// Frame artifact compat - list/track/deploy/contract/slug all handled locally
	case strings.HasPrefix(path, "/api/frame/"):
		h.handleFrameEndpoint(w, r)
		return true

	// CCR triggers - prefix covers list and sub-paths, local empty response (CC 2.1.211)
	case strings.HasPrefix(path, "/v1/code/triggers"):
		h.handleTriggersEndpoint(w, r)
		return true

	// Claude Design consent - GET/POST/DELETE local state
	case path == "/v1/design/consent":
```

**Step 3.4** — Add rows to `TestIsHardcodedEndpoint` and `TestHandleHardcodedEndpoint` tables (already included in steps 1.3 and 1.4).

**Step 3.5** — Create `internal/proxy/triggers_test.go`:

```go
package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"magic-claude-code/internal/config"
)

func TestHardcodedTriggers(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	for _, path := range []string{
		"/v1/code/triggers",
		"/v1/code/triggers/t1",
		"/v1/code/triggers/t1/run",
	} {
		t.Run("GET "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			if !handler.handleHardcodedEndpoint(rec, req) {
				t.Fatalf("should handle %s", path)
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var resp struct {
				Data []any `json:"data"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(resp.Data) != 0 {
				t.Errorf("data = %v, want empty", resp.Data)
			}
		})
	}

	t.Run("POST returns 403 write_gate_disabled", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/code/triggers", strings.NewReader(`{"name":"t"}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		var resp struct {
			Reason string `json:"reason"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Reason != "write_gate_disabled" {
			t.Errorf("reason = %q, want write_gate_disabled", resp.Reason)
		}
	})

	t.Run("DELETE returns 405 with Allow", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/code/triggers/t1", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, POST" {
			t.Errorf("Allow = %q, want GET, POST", got)
		}
	})
}
```

#### Verification

- [ ] `GET /v1/code/triggers` returns `200 {"data":[]}`.
- [ ] `GET /v1/code/triggers/t1` (sub-path) returns `200 {"data":[]}`.
- [ ] `POST /v1/code/triggers` returns `403`, `reason=="write_gate_disabled"`.
- [ ] `DELETE /v1/code/triggers/t1` returns `405` + `Allow: GET, POST`.

### Task 4: baseline doc line-by-line update 52→55

#### Requirements

**Objective** — `2026-07-15-intercepted-endpoints.md` reflects the 3 new endpoints; total count 52→55.

**Outcomes** — Five sections synced: count summary, exact-match list, prefix list, version identifier, appendix check script.

**Evidence** — Appendix script output: exact 39, prefix 12, total 55.

**Constraints** — Only update numbers and lists; do not touch unrelated sections like "fallback rules" or "log safety".

**Edge Cases** — None; documentation-only change.

**Verification** — Appendix script output matches the doc numbers.

#### Plan

Doc path `sdd-docs/research/2026-07-15-intercepted-endpoints.md`. Line numbers refer to the current doc.

**Step 4.1** — Count summary table (`lines 9-14`):

| Field | Old | New |
|-------|-----|-----|
| Local hardcoded interception | 50 | **53** |
| Exact-match endpoints | 37 | **39** |
| Prefix-match endpoints | 11 | **12** |
| Total top-level endpoints | 52 | **55** |

> Pattern-match endpoints (2) and model-forward endpoints (2) are unchanged.

**Step 4.2** — Add 2 rows to the exact-match lists (A3 policy area and A6 Design area):

- A3 "Policy / Limits / Compliance" table (`lines 83-88`), after the `/v1/ultrareview/quota` row, add:

```
| 20b | `GET` | `/v1/ultrareview/preflight` | ultrareview preflight (joins quota at 200 {}) |
```

- A6 "Claude Design" table (`lines 112-116`), after the `/v1/design/mcp` row, add:

```
| 33b | `GET`/`POST` | `/v1/design/grants` | GET empty grants disables Design; POST 403 write_gate_disabled |
```

(Numbering uses a `b` suffix to avoid renumbering existing 1–37; a full renumber during implementation is equally fine.)

**Step 4.3** — Prefix-match list (`lines 131-145`), after `/v1/code/sessions/*` (row 9), add:

```
| 12 | `GET`/`POST` | `/v1/code/triggers*` | CCR triggers: GET `{data:[]}`, POST 403 write_gate_disabled |
```

**Step 4.4** — API version identifier table (`line 204`, v1 row): after `/v1/ultrareview/quota` add `/v1/ultrareview/preflight`; after `/v1/design/mcp` add `/v1/design/grants`; after `/v1/code/sessions/*` add `/v1/code/triggers*`.

**Step 4.5** — Appendix check-script comments (`lines 213-230`):

```
# Exact-match endpoint count -> 39   (was 37)
# Prefix-match endpoint count -> 12  (was 11)
# Total: 39 + 12 + 2 (local interception) + 2 (model forward) = 55
```

#### Verification

- [ ] Appendix script actually outputs exact 39, prefix 12 (verify with `awk ... | grep -cE`).
- [ ] Count summary table "Total top-level endpoints" = 55.
- [ ] Prefix list contains `/v1/code/triggers*`; exact lists contain grants and preflight.

---

## Post-Implementation Writeback

After implementation, write back the header "Progress: 0 / 4 → 4 / 4", set each task's "Status: planned → done", tick the `#### Verification` checkboxes of tasks 1-3, and paste the actual script output into task 4.
