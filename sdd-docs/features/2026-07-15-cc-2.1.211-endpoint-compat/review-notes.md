# CC 2.1.211 Endpoint Compat Review Notes

Date: 2026-07-16
Reviewers: Claude Code (static analysis + independent re-derivation from the real
obfuscated client `claude_code_src_2.1.211.js`, cross-checked by a second read pass)
Review type: security + functional review of the glm-5.2 implementation
Commit range reviewed: `67c783b` (code) + `fa8ae42` / `f0c94b8` (docs)

## Scope

The diff intercepts three endpoint groups newly added in Claude Code 2.1.211,
locally faking responses so the client does not throw or fall into the fail-closed
404 (`mcc_blocked_unknown_endpoint`):

| Endpoint | Code path | Response |
|----------|-----------|----------|
| `GET /v1/design/grants` | `hardcoded.go:317` | `200 {"grants":[]}` |
| `POST /v1/design/grants` | `hardcoded.go:321` | `403 {"reason":"write_gate_disabled"}` |
| `GET /v1/ultrareview/preflight` | `hardcoded.go:345` → `handleEmptyResponse` | `200 {}` |
| `GET /v1/code/triggers[/{id}][/run]` | `hardcoded.go:302` → `triggers.go` | `200 {"data":[]}` |
| `POST /v1/code/triggers[/{id}]` | `triggers.go:22` | `403 {"reason":"write_gate_disabled"}` |

Changed files: `internal/proxy/hardcoded.go`, `internal/proxy/triggers.go`,
`internal/proxy/hardcoded_test.go`, `internal/proxy/triggers_test.go`, plus the
spec pair and the baseline research doc. No change to `endpoint_policy.go`
(`modelForwardPaths`) — verified.

## Verification Performed

1. Re-derived each client contract from the real obfuscated source (two independent
   read passes — direct grep plus a thorough sweep — not trusting the spec):
   - `GET /v1/design/grants` — client reads `e.data?.grants`, requires
     `Array.isArray` (`probe_shape` otherwise), 404 emits `probe_404_old_server`
     telemetry, and a null/non-array result downgrades to a per-batch plan flow.
     `200 {"grants":[]}` → empty Set → Design grant disabled, no 404 noise. **Match.**
   - `POST /v1/design/grants` — `validateStatus:(n)=>n<300||n===404`, `!r.ok→throw`.
     403 lands in `!r.ok` → write fails closed. **Match.** Client treats 404 and 403
     as functionally-equivalent throws; the implementation chose 403, which reads as
     "policy-gate blocked" rather than 404's "project black-listed + cannot hold a
     durable grant" — the correct semantic for MCC local-mode write-disable.
   - `GET /v1/ultrareview/preflight` — client validates the body against a zod schema
     (`action: enum["proceed","confirm","blocked"]`, optional `billing_note`/`confirm`/
     `blocked`). On `!t.ok` it branches on `t.reason`; on a 200 whose body fails
     `safeParse` it warns `fetchUltrareviewPreflight schema mismatch` + telemetry
     `schema_mismatch` → returns `null`; the gate `kDo()` treats `null` as `proceed`
     (allow). So `200 {}` works — but via the "malformed ⇒ allow" path, emitting one
     `schema_mismatch` telemetry event, not by matching a real `proceed`. The desired
     "do not block" outcome is still achieved. See Residual Notes.
   - `GET /v1/code/triggers` — `validateStatus:()=>!0` (never throws on HTTP status),
     `e.data.data??[]`, `!e.ok→throw "triggers unavailable"`. `200 {"data":[]}` →
     `u.ok` true, empty array, no throw. POST 403 → `!u.ok` → throw "Remote triggers
     unavailable". **Match.** The triggers surface is a tool with actions
     list/get/create/update/run, all on `/v1/code/triggers[/{id}][/run]` — the prefix
     interception covers every variant.
2. Fail-closed guard: wrote a temporary test asserting all 5 paths × GET/POST/PUT/DELETE
   never classify as `endpointActionForwardModel`. PASS (temp test removed after).
3. `go test ./internal/proxy/ -race -count=1` — all pass, no data race.
4. Working tree clean; no leftover files.

## Key Findings And Resolutions

1. No logic defects found in the three endpoint handlers.
   - Response shape, status codes, and the `write_gate_disabled` reason all line up
     with the actual client handling. The 405 branch uses the shared `methodAllowed`
     helper (Allow header + JSON body), equivalent to and more consistent than the
     hand-rolled form in the spec's Plan.
2. No security defects found.
   - All three endpoints are non-model; `classifyForwardingEndpoint` blocks them from
     the forward allowlist (verified by test). No request body is parsed (drained via
     `drainRequestBodyLimited`, bounded at 1MB — no DoS). No auth/header/query is
     logged on these paths. Nothing new is forwarded upstream, so no data-leak surface
     is introduced.
3. `GET /v1/code/triggers/{id}` (single item) returns the list shape `{data:[]}`.
   - Resolution: semantically imperfect, but the client tolerates it (`data.data??[]`),
     third-party users do not use CCR triggers, and the spec explicitly accepts this
     (Risk Summary #2). Acceptable.

## Final Review Conclusion

The glm-5.2 implementation of the CC 2.1.211 endpoint compatibility is correct and
safe. The three endpoint groups reproduce the client contract faithfully enough to
achieve the intended client behavior (no thrown exception, no spurious block), the
fail-closed forwarding guard remains intact, and tests (including `-race`) pass.
No logic or security defects remain.

## Residual Notes

- **preflight `{}` reaches "allow" via `schema_mismatch`, not via a real `proceed` —
  but in the third-party scenario the body is never actually consulted.**
  Re-verified against source: preflight runs through `kDo(){ let e=await Hpp(); if(!e)
  return {kind:"proceed"} ... }`, and `Hpp` issues `GET /v1/ultrareview/preflight` with
  `auth:"teleport-org"`. When there is no claude.ai OAuth token (always the case for a
  third-party provider), the client resolves `t.reason==="no-auth"` → `!t.ok` → it
  returns a blocked result ("Ultrareview requires a Claude.ai account") **on its own**,
  before the response body is ever parsed. So ultrareview is already "unavailable" in
  the target scenario regardless of what body MCC returns; `{}` vs `{"action":"proceed"}`
  is a moot distinction here (both are 200-ok bodies that are never reached).
  - Keeping `{}` is therefore the right call — but for a YAGNI / minimal-implementation
    reason, not because it is "more conservative". `{}` is semantically "allow via
    schema_mismatch" (it emits one `api_ultrareview_preflight / schema_mismatch`
    telemetry event), yet that path is unreachable under `no-auth`. Switching to
    `{"action":"proceed"}` would neither activate ultrareview (teleport-org auth would
    still gate it) nor provide real value. No change recommended; do not bother
    crafting a precise body for an endpoint whose body is never read in the target
    scenario.
  - **Correction to a review claim**: one re-review asserted `fetchUltrareviewPreflight`
    (`Hpp`) is dead code with no caller. That is **incorrect** — `kDo` calls `Hpp`, and
    `kDo` is itself invoked from two real ultrareview entry points
    (`runUltrareviewHeadless` and the interactive flow). A naive `grep` word-boundary
    match misses the call because the minified call site is written compactly
    (`await Hpp()` with no space). The "preflight is never requested" conclusion is
    wrong; the endpoint *is* reachable, but is short-circuited by `no-auth` in the
    third-party scenario as described above. The client also honors
    `CLAUDE_CODE_ULTRAREVIEW_PREFLIGHT_FIXTURE` to short-circuit preflight locally.
- **Prefix `/v1/code/triggers` has no trailing slash** (neighbors like
  `/v1/code/sessions/` do), so `/v1/code/triggersXXX` would also be intercepted.
  Verified the real 2.1.211 client has no such sibling path (only `triggers`,
  `triggers/{id}`, `triggers/{id}/run`), so this is a robustness/style note, not a
  defect. If a future client adds e.g. `/v1/code/triggersettings`, tighten to
  `path == "/v1/code/triggers" || strings.HasPrefix(path, "/v1/code/triggers/")`.
- The spec's "8 endpoints" framing: only 3 needed new responses (Group B); the other
  5 (Group A: memory chain, github import-token, filestore readFile) are correctly
  left at fail-closed 404 — confirmed they are not in `isHardcodedEndpoint`, so the
  memory chain is not over-activated (spec Risk Summary #1 honored).
- `design/grants` requires an OAuth token with `user:design:read` scope and uses a
  distinct `auth:"none" + Bearer` path (not the `auth:"teleport-org"` used by
  preflight/triggers). Third-party-provider setups never have that token, so this
  path is largely defensive. Harmless to keep.
