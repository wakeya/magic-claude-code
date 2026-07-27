# Frame Multi-file Publish Write-Gate Spec

**Proxy entry:** `internal/proxy/frame.go` (`handleFrameEndpoint`, ~L20-74; the 403 write-gate branch L46-53); `internal/proxy/hardcoded.go` (`/api/frame/` prefix match L88, dispatch to `handleFrameEndpoint` L297; `isHardcodedEndpoint` unchanged); `internal/proxy/helpers.go` (`methodAllowed`, where 405 is written)
**Test entry:** `internal/proxy/frame_test.go` (`TestFrameEndpointCompatibility`, new sub-tests)
**Reference sources:** `/home/www/workspace/open-software/claude_code/073_claude_spy/claude_code_src_2.1.220.js` (`Pi.post("/api/frame/deploy/prepare"...)`, `Pi.post("/api/frame/upload"...)`, fallback function `T`); `sdd-docs/research/2026-07-15-intercepted-endpoints.md` (section C, Frame sub-endpoint expansion); the 2.1.211 vs 2.1.220 path-literal diff (only 2 new, both in this family)
**Stack:** Go 1.26 standard library
**Last updated:** 2026-07-26
**Progress:** 0 / 3

## Overall Analysis (Source Analysis)

### Symptom

Claude Code 2.1.220 introduces a **multi-file frame publish** flow, adding two new firstParty Anthropic API endpoints:

- `POST /api/frame/deploy/prepare` — preflight: the client sends `{slug?, shas:[SHA…]}`, the server returns `{slug, missing:[SHA…]}` (which files need uploading).
- `POST /api/frame/upload` — upload: the client sends `{slug, files:[{path, content, contentType}]}`, uploading the contents of the files listed in `missing`.

Full publish flow: `prepare` (check missing) → `upload` (upload) → `deploy/init`|`deploy/direct` (submit) → `deploy/complete` (finish). Of these, `init`/`direct`/`complete` existed in 2.1.211 (mcc already handles them); `prepare`/`upload` are new in 2.1.220.

mcc currently has **no explicit case** for these two new paths: after the `/api/frame/` prefix matches and `handleFrameEndpoint` is entered, they fall through to the `default` branch (`frame.go:66`), where `methodAllowed(GET, DELETE)` fails for POST → `methodAllowed` in `helpers.go` writes **405 `method_not_allowed` + `Allow: GET, DELETE`**.

### Client source analysis (2.1.220)

**Call shape** (both `Pi.post`, with `host:"frame"` dispatch → `BASE_API_URL` → production default `https://api.anthropic.com`, i.e. firstParty traffic mcc must handle):

```js
// prepare
Pi.post("/api/frame/deploy/prepare",
  {...slug!==void 0&&{slug}, ...shas.length>0&&{shas}},
  {host:"frame", auth:"required", refreshOAuth:!0, timeout:30000, validateStatus:()=>!0})
// upload
Pi.post("/api/frame/upload",
  {slug, files:files.map(({f,wire})=>({path:f.path, content:wire, contentType:f.contentType}))},
  {host:"frame", auth:"required", refreshOAuth:!0, timeout:60000, validateStatus:()=>!0, maxBodyLength:2*I6})
```

**Response handling (decides whether a compatibility response is needed):**

- `validateStatus:()=>!0` — axios does not throw on any status code; the client inspects the status itself.
- prepare loop `for(G=0;G<2;G++)`: `let j=await F(...); if(!j.ok) break;` — **any non-2xx (incl. mcc's 405) exits the loop immediately, no retry**; only `j.ok && j.status===429` retries once per `retry-after`. upload is analogous (`if(L.ok&&L.status===429)` retry).
- Failure fallback `T`: `Oh(\`multi-file publish is not available here yet (server or proxy rejected it: ${I}${R?` — ${R.slice(0,200)}:""})…\`)` — **the client explicitly anticipates "proxy rejection"**, splicing the error detail (truncated to 200 chars) into the user-visible message.
- Upstream client switch: the source also has a `pe("artifact_publish","multifile_flag_off",...)` + `Oh("multi-file publish is not enabled for ...")` branch — when the flag is off, the client reports "not enabled" and **does not send prepare/upload at all**.

→ **Security conclusion:** mcc's current 405 is already gracefully degraded by the client (`!ok` break → `T` fallback). **No leak (fail-closed, no upstream forwarding), no retry storm, no crash.** Functional degradation already "works".

### Problem (why change anyway)

The 405 response body `Allow: GET, DELETE` is **misleading** for a **POST endpoint** — these two endpoints are POST in the Anthropic API proper; mcc simply does not implement the write gate, rather than the "method being wrong". This misleading text flows verbatim into the client's `T` error detail (`server or proxy rejected it: ...Only GET, DELETE are allowed...`), misleading the user into thinking the method is wrong.

By contrast, the same-family `deploy/init`|`deploy/direct` (`frame.go:46-53`) already explicitly returns **403 `write_gate_disabled`** (comment: "the client publish path recognizes write_gate_disabled"); `prepare`/`upload` falling to default → 405 is **inconsistent** and semantically wrong.

### Design decision

Fold `deploy/prepare` and `upload` into the existing 403 `write_gate_disabled` branch (same family and response as `deploy/init`|`deploy/direct`).

| Option | Description | Adopted |
| --- | --- | --- |
| A. Fold into the existing 403 case | Add the two paths to the `deploy/init\|\|deploy/direct` case, reusing the same 403 `write_gate_disabled` response | ✅ |
| B. New independent case | A separate case returning a different body | — (no difference from the family; redundant) |
| C. Keep 405 | No change | — (misleading text, inconsistent with the family) |
| D. Disable via feature flag | Turn off `multifile_flag` in `/api/feature/` so the client sends nothing | — (flag name unstable, and even if sent it's already gracefully degraded by 403; benefit doesn't justify maintenance cost; leave for later if needed) |

Why A:

1. **Semantically correct**: the write gate is closed, not the method being wrong;
2. **Consistent with the family**: the four deploy-family write endpoints (init/direct/prepare) + upload uniformly return 403 `write_gate_disabled`;
3. **Client experience**: the `T` fallback's error detail changes from `Only GET, DELETE allowed` to `write_gate_disabled`, clearer;
4. **User-visible outcome unchanged**: still hits `T`'s "not available here yet" degradation, no functional regression;
5. **Minimal change**: one case-condition expansion + two tests.

### Impact

| File | Change |
|------|--------|
| `internal/proxy/frame.go` | L46 case condition extended with `\|\| path=="/api/frame/deploy/prepare" \|\| path=="/api/frame/upload"`; the function header comment (L8-17) item 4 extended to list prepare/upload |
| `internal/proxy/frame_test.go` | 2 new `t.Run` sub-tests: `deploy prepare returns write-gate denied`, `upload returns write-gate denied` |
| Other | **unchanged**: `isHardcodedEndpoint` (prefix `/api/frame/` already covers), `hardcoded.go` dispatch, other frame sub-paths, routing, failover, usage |

### Backward compatibility

- Client 2.1.211 and earlier: does not send prepare/upload (the flow is introduced in 2.1.220); the new case is unreachable to them, zero impact.
- Client 2.1.220: old 405 → new 403 `write_gate_disabled`; both gracefully degraded by `T`, user-visible "not available" unchanged; error detail clearer.
- Does not affect mcc's existing responses for init/direct/complete/track/frames/contract/{slug}.

## Development Checklist

| # | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | `frame.go`: extend the 403 write-gate case to cover `deploy/prepare` + `upload`; update the header comment | `internal/proxy/frame.go` | `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` |
| 2 | Planned | `frame_test.go`: add 2 sub-tests asserting 403 `write_gate_disabled` | `internal/proxy/frame_test.go` | same as above |
| 3 | Planned | Full regression + commit | verification record | `go test ./...` + `go vet ./...`; `git commit` (no push) |

## Requirements

### Deliverables

1. The 403 write-gate case condition in `handleFrameEndpoint` (`frame.go:46`) is extended from `path == "/api/frame/deploy/init" || path == "/api/frame/deploy/direct"` additionally `|| path == "/api/frame/deploy/prepare" || path == "/api/frame/upload"`; the response body is unchanged (`{"error":"Frame publishing is unavailable in MCC local mode","reason":"write_gate_disabled"}`, HTTP 403).
2. The `frame.go` function header comment (L8-17) item 4 is updated from "deploy/init|direct" to "deploy/init|direct|prepare, frame/upload", keeping the routing-table doc consistent with code.
3. `frame_test.go` adds two `t.Run` sub-tests (structure aligned with the existing `deploy init returns write-gate denied`, L57-75): for `POST /api/frame/deploy/prepare` and `POST /api/frame/upload` respectively, assert `rec.Code == 403`, `resp.Reason == "write_gate_disabled"`, and `resp.Error` contains "unavailable".
4. Do not change `isHardcodedEndpoint`, `hardcoded.go` dispatch, other frame sub-paths, `methodAllowed`, routing, failover, or usage.

### Constraints

- Local response only, never forward upstream (Frame family fail-closed unchanged).
- Do not change the response-body schema (`error`/`reason` fields), keep it identical to init/direct to avoid client-recognition divergence.
- Do not introduce new imports or change the `methodAllowed` signature.
- Keep bilingual docs/spec in sync (see global Bilingual Output Requirement).

### Edge cases

- POST `/api/frame/deploy/prepare` → 403 `write_gate_disabled` (new).
- POST `/api/frame/upload` → 403 `write_gate_disabled` (new).
- Non-POST methods (GET/DELETE/PUT) on these two paths → still fail the case's `methodAllowed(POST)` short-circuit → 405 (preserves current behavior).
- `deploy/init`|`deploy/direct` behavior unchanged (still 403 write_gate_disabled).
- Other frame sub-paths (frames/track/deploy/complete/contract/*/{slug}) behavior unchanged.
- Query string (e.g. `?beta=true`) is stripped by `r.URL.Path`, does not affect path matching.

## Task Details

### Task 1: frame.go — 403 write-gate case extension + comment sync

#### Requirements

**Objective** — Make `handleFrameEndpoint` return the same 403 `write_gate_disabled` as the same-family `deploy/init`|`deploy/direct` for the 2.1.220-new `POST /api/frame/deploy/prepare` and `POST /api/frame/upload`, replacing the misleading 405 from the current default branch, so the write-gate semantics are correct and the client's degradation text is clear.

**Outcomes** — The case condition at `internal/proxy/frame.go:46` is extended from `path == "/api/frame/deploy/init" || path == "/api/frame/deploy/direct"` additionally `|| path == "/api/frame/deploy/prepare" || path == "/api/frame/upload"`; the case body (`methodAllowed(POST)` short-circuit + 403 `write_gate_disabled` JSON) is unchanged. The function header comment L14 item 4 ("deploy/init|direct") is updated to "deploy/init|direct|prepare and frame/upload (write gate closed)".

**Evidence** — `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` passes: the two new sub-tests `deploy prepare returns write-gate denied` and `upload returns write-gate denied` assert status=403, reason=write_gate_disabled, error contains unavailable.

**Constraints** — Only expand the case condition; do not change the case body, response schema, or `isHardcodedEndpoint`; keep the `methodAllowed(w,r,POST)` short-circuit (non-POST still 405); introduce no new imports.

**Edge Cases** — GET/DELETE/PUT on prepare/upload → the case's `methodAllowed(POST)` fails → 405 (preserved); POST prepare/upload → 403 write_gate_disabled (new); with query (`?beta=true`) → `r.URL.Path` excludes the query, matching unaffected.

**Verification** — `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` fully green; `go vet ./internal/proxy/` clean.

#### Plan

1. **Write the failing test first.** In `TestFrameEndpointCompatibility` in `internal/proxy/frame_test.go`, after the `deploy direct returns write-gate denied` sub-test, add two sub-tests (structure aligned with the `deploy init` sub-test at L57-75):
   ```go
   t.Run("deploy prepare returns write-gate denied", func(t *testing.T) {
       req := httptest.NewRequest(http.MethodPost, "/api/frame/deploy/prepare", strings.NewReader(`{"slug":"x","shas":["abc"]}`))
       rec := httptest.NewRecorder()
       handler.handleHardcodedEndpoint(rec, req)
       if rec.Code != http.StatusForbidden {
           t.Fatalf("status = %d, want 403", rec.Code)
       }
       var resp struct {
           Error  string `json:"error"`
           Reason string `json:"reason"`
       }
       json.NewDecoder(rec.Body).Decode(&resp)
       if resp.Reason != "write_gate_disabled" {
           t.Errorf("reason = %q, want write_gate_disabled", resp.Reason)
       }
       if !strings.Contains(resp.Error, "unavailable") {
           t.Errorf("error = %q", resp.Error)
       }
   })

   t.Run("upload returns write-gate denied", func(t *testing.T) {
       req := httptest.NewRequest(http.MethodPost, "/api/frame/upload", strings.NewReader(`{"slug":"x","files":[]}`))
       rec := httptest.NewRecorder()
       handler.handleHardcodedEndpoint(rec, req)
       if rec.Code != http.StatusForbidden {
           t.Fatalf("status = %d, want 403", rec.Code)
       }
       var resp struct {
           Error  string `json:"error"`
           Reason string `json:"reason"`
       }
       json.NewDecoder(rec.Body).Decode(&resp)
       if resp.Reason != "write_gate_disabled" {
           t.Errorf("reason = %q, want write_gate_disabled", resp.Reason)
       }
   })
   ```
2. **Confirm failure.** `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` → the two new sub-tests fail (status=405, want 403).
3. **Minimal implementation.** In `internal/proxy/frame.go`:
   - Change the L46 case condition to:
     ```go
     // deploy init/direct/prepare, frame/upload - POST 403, client publish path recognizes write_gate_disabled
     case path == "/api/frame/deploy/init" || path == "/api/frame/deploy/direct" ||
         path == "/api/frame/deploy/prepare" || path == "/api/frame/upload":
     ```
     The case body (`methodAllowed(POST)` short-circuit + 403 write_gate_disabled JSON) is unchanged.
   - Change the function header comment L14 item 4 from `//  4. POST /api/frame/deploy/init|direct -> 403 write_gate_disabled` to:
     ```
     //  4. POST /api/frame/deploy/init|direct|prepare, POST /api/frame/upload -> 403 write_gate_disabled
     ```
4. **Confirm pass.** `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` → all sub-tests pass (incl. the two new).
5. **Regression.** `go test ./internal/proxy/`, `go vet ./internal/proxy/`.
6. **Commit.** `git add internal/proxy/frame.go internal/proxy/frame_test.go && git commit -m "feat(proxy): frame write-gate covers 2.1.220 prepare/upload endpoints"`.

#### Verification

- [ ] `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` — all sub-tests pass (incl. the two new deploy prepare, upload).
- [ ] `go vet ./internal/proxy/` — clean.

### Task 2: Full regression + commit wrap-up

#### Requirements

**Objective** — Confirm the frame write-gate extension breaks no existing behavior; `make test` (race + coverage) fully green; working tree contains only this task's changes.

**Outcomes** — `go test -race ./...` all packages ok, 0 failures; `go vet ./...` clean; `git status --short` shows only `internal/proxy/frame.go`, `internal/proxy/frame_test.go` (+ the spec dir).

**Evidence** — Test output shows all 15 packages ok; the existing `deploy init`/`deploy direct` sub-tests still pass (confirming init/direct behavior unchanged); the existing default-405 sub-test (`wrong method returns 405`) still passes (confirming non-POST still 405).

**Constraints** — Do not change production logic to appease tests; commit only, no push (see global Local Commit Before Push).

**Edge Cases** — None (Task 1 already covers the functional edges).

**Verification** — `make test` equivalent fully green.

#### Plan

1. `go test -race ./...`.
2. `go vet ./...`.
3. `git status --short && git diff --stat` to verify the change scope.
4. Consolidate the commit; **do not push**, wait for user confirmation.

#### Verification

- [ ] `go test -race ./...` — all packages ok, 0 failures.
- [ ] `go vet ./...` — clean.
- [ ] `git status` shows only this feature's changes; not pushed.

### Task 3 (optional): annotate the intercepted-endpoints research doc

#### Requirements

**Objective** — Archive the 2.1.220 audit findings and this fix, so the next version audit has a reference.

**Outcomes** — In section C (Frame sub-endpoint expansion) of `sdd-docs/research/2026-07-15-intercepted-endpoints.md`, add a line for deploy/prepare|upload → 403 write_gate_disabled (CC 2.1.220), or add a "2026-07-26 re-audit of 2.1.220" note at the end: only 2 new (both frame family, already covered by the prefix), no new unintercepted leaked endpoint; this PR converges the default-405 to an explicit 403.

**Constraints** — Documentation-only, no code change; semantically consistent with this spec.

**Verification** — The doc is readable and its counts stay self-consistent.

#### Plan

1. Edit section C's table in `sdd-docs/research/2026-07-15-intercepted-endpoints.md` adding 2 rows / or add a re-audit note section at the end.
2. Commit (may be folded into Task 2's commit or separate).

#### Verification

- [ ] The research doc reflects the 2.1.220 re-audit conclusion and this convergence.
