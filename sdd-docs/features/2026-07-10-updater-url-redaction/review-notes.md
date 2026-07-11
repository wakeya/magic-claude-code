# Updater Download URL Redaction Review Notes

Date: 2026-07-10  
Reviewer: Codex

## Scope

Reviewed `spec.md` and `spec_ZH.md` against the current updater and admin API implementation. Verified the existing proxy-dependent reproduction, Go 1.26 `net/http` behavior, malformed redirect handling, opaque URL handling, and the current updater test baseline. No implementation diff exists on this branch yet.

## Key Findings And Resolutions

1. **Security defect — opaque or non-HTTP URLs bypass the proposed redaction.**
   - Reproduction: `redactURLForError("user:password-secret@example.com/path?token=query-secret")` removes the query but retains `password-secret` in the URL's opaque component. `http.NewRequestWithContext` accepts that scheme, and `client.Do` returns an error containing the retained value. The spec's claim that the existing redaction rules are already correct is therefore false for attacker-influenced or misconfigured source URLs.
   - Resolution required: make URL rendering fail closed unless the value is an absolute `http`/`https` URL with a host, or otherwise ensure `Opaque` and every credential-bearing component are removed. Update the scheme-validation non-goal and add an opaque/unsupported-scheme regression test.

2. **Security defect — mutating only `*url.Error.URL` does not sanitize malformed redirect errors.**
   - Reproduction: a local server returning `Location: https://redirect.example/%zz?token=redirect-secret` causes Go 1.26 to place the raw `Location` value in `urlErr.Err`. Redacting only the outer `urlErr.URL` leaves `redirect-secret` in `urlErr.Error()` and therefore in the admin API response.
   - Resolution required: define a safe observable error representation for `client.Do` failures instead of assuming all sensitive text is in the outer `URL` field. Add a hermetic malformed-redirect test. If preserving error identity or unwrapping is required, specify and test those semantics explicitly.

3. **Test gap — the planned `NewRequest` fix has no direct regression test.**
   - Resolution required: add a deterministic malformed URL case containing distinct credential/query markers and assert that the returned error is fail-closed (for example `<invalid-url>`) without echoing the raw parse error.

4. **Scope and path inventory are incomplete.**
   - `downloadFileWithLimit` has five error returns, not four: the `io.ReadAll` failure path is omitted from the table. A normal remote peer cannot freely choose a Go body-read error string, so this is not demonstrated as an exploitable leak, but the spec must either include it in the guarantee or explicitly scope it out.
   - The current admin handler returns updater errors to `POST /api/update/apply`; it does not log those errors. Replace the unsupported “process log” claim or identify and test an actual logging path.

5. **Go 1.26 behavior and test assertions need correction.**
   - `http.Client` uses `stripPassword`, so the password in its generated `*url.Error.URL` appears as `***`; username, query, and fragment can still be exposed. The spec's example showing the original password is inaccurate for the stated Go version.
   - Use distinct markers for username, password, query, and fragment, and assert every marker plus sensitive keys such as `token=` is absent. The integration test should not rely only on `user:pass`, which Go already masks. The focused helper test should assert the exact sanitized URL and preservation semantics, not only selected substrings.

6. **Helper contract is ambiguous for wrapped errors.**
   - `errors.As` may find a nested `*url.Error`, while returning that inner error drops outer context; returning the original wrapper can still expose a cached pre-redaction message. Since `http.Client.Do` returns a top-level `*url.Error`, constrain the helper to that contract or specify safe wrapped-error behavior and add a test.

## Verification

- `go test ./internal/updater/ -run TestDownloadAndApplyRedactsInvalidDownloadURL -count=1` — passed on the normal network path.
- `HTTPS_PROXY=http://127.0.0.1:9 go test ./internal/updater/ -run TestDownloadAndApplyRedactsInvalidDownloadURL -count=1` — failed deterministically and exposed the query secret.
- `go test ./internal/updater/ -count=1` — passed baseline.
- Temporary hermetic probes confirmed both the malformed-redirect nested leak and the opaque URL leak; the probe file was removed afterward.

## Final Review Conclusion

The reported proxy-dependent leak is real, but the current plan does not yet provide its stated all-path redaction guarantee. Two reproducible security gaps remain in the specification: opaque/non-HTTP URLs and sensitive malformed redirect locations. Update the sanitizer/error contract and regression matrix before implementation.

## Resolution Update

The specification was revised after review and approval of the fail-closed design. It now rejects opaque/non-HTTP URLs before transport, exposes origin-only diagnostics, discards all untrusted low-level error text and unwrap chains, covers malformed redirects and body-read failures, and adds an admin API sink-level acceptance test. The design findings above are resolved in the specification; implementation and validation remain pending.

## Residual Notes

- Keep `spec.md` and `spec_ZH.md` semantically aligned when incorporating these findings.
- After implementation, run focused tests, `go test ./internal/updater/`, `go test ./...`, and `go test -race ./internal/updater/` before closing the review.

## Implementation Verification (2026-07-10)

Implemented per the approved fail-closed design and validated in TDD order.

### Files Changed

- `internal/updater/updater.go`: added `parseDownloadURL`, `safeURLOrigin`, the revised `redactURLForError`, `checksumURLForAsset`, and `requestFailureCategory`; rewrote `downloadFileWithLimit` to emit origin-only fixed messages; replaced the `strings.LastIndex` checksum derivation in `DownloadAndApply` with `checksumURLForAsset`; added the `errors` import.
- `internal/updater/updater_test.go`: added the section-7 helpers (`roundTripFunc`, `errorReadCloser`, `assertNoSensitiveMarkers`, marker constants, `validURLWithMarkers`, `allSensitiveMarkers`) and all section 8.1–8.9 tests; updated the status/size tests to expect origin-only output.
- `internal/admin/update_handler_test.go`: added a package-local fake `updater.ReleaseSource` and the section 8.10 sink-level malformed-redirect regression.

### Red → Green Evidence

Tests were written before the implementation. Before the rewrite, `go test ./internal/updater/` reported 56 passed / 13 failed, and the failures exposed the real leak surface of the old code:

- `TestDownloadFileRejectsUnsafeURL`: opaque / unsupported-scheme / relative / missing-host / malformed-escape URLs all reached the transport (`transport must not be called for invalid URL`).
- `TestDownloadFileTransportErrorDiscardsRawCause` / `TestDownloadFileCanceledRequest` / `TestDownloadFileDeadlineExceeded`: the old `client.Do` `*url.Error` leaked `Get "https://username-secret:***@example.com/path-secret?query-key=query-secret#fragment-secret"` (Go masks the password as `***`, but username/path/query/fragment remain exposed).
- `TestDownloadFileMalformedRedirectDiscardsLocation`: the malformed redirect Location entered the nested error and leaked `redirect.example` / `%zz`.
- `TestDownloadFileBodyReadErrorDiscardsRawCause`: the old code returned the raw `body-secret` text.
- `TestDownloadAndApplyRedactsInvalidDownloadURL`: the `%w` chain propagated the raw `*url.Error` through the `download asset:` wrapper.

After the rewrite, `go test ./internal/updater/` reports 69 passed (green).

### Validation Commands And Results (all exit 0)

| Command | Result |
| --- | --- |
| `gofmt -w internal/updater/updater.go internal/updater/updater_test.go internal/admin/update_handler_test.go` | OK; `gofmt -l` empty |
| `go test ./internal/updater/ -count=1` | 69 passed |
| `go test ./internal/admin/ -run 'TestHandleUpdateApply\|TestWriteUpdateApply' -count=1` | 5 passed |
| `HTTPS_PROXY=http://127.0.0.1:9 go test ./internal/updater/ -count=1` | 69 passed |
| `go test -race ./internal/updater/ ./internal/admin/` | 188 passed (2 packages) |
| `go test ./...` | 1395 passed (16 packages) |
| `go vet ./internal/updater/ ./internal/admin/` | No issues |
| `make test` (CI entry) | exit 0 |

### Security Behavior Confirmed

- A download URL is accepted only when `url.Parse` succeeds, `Opaque==""`, the scheme is `http`/`https` (case-insensitive, normalized lowercase), and `Host!=""`; otherwise a fixed `invalid download URL: <invalid-url>` is returned before any transport call.
- Every download error exposes at most the normalized origin (scheme+host[:port]) plus a fixed category/status/size; raw `err` text is always discarded, untrusted errors are never wrapped with `%w`, and custom errors carry no `Unwrap`.
- `TestDownloadFileTransportErrorDiscardsRawCause` asserts `errors.Unwrap(err)==nil`, proving no raw cause is reachable at the helper boundary.
- The checksum URL is built from the parsed URL via `ResolveReference`; userinfo/directory path are preserved only as a request URL and never enter diagnostic output.
- The admin API end-to-end test (`TestHandleUpdateApplyRedactsMalformedRedirect`) verifies the actual security sink: the JSON body contains none of the 12 redirect/path/query/userinfo/underlying-error markers and retains only the safe `download asset:` context.
- All focused tests are hermetic and perform no public-network access.

### Change Scope

`git diff --stat` contains only the 3 feature files plus the user's pre-existing `sdd-docs/features/README.md` and this feature's spec directory; no unrelated code or frontend artifacts were touched. No open issues remain.

## Independent Re-review (2026-07-10)

### Finding

1. **Test defect — the admin API sink-level redaction assertions inspect an already-consumed buffer.**
   - `TestHandleUpdateApplyRedactsMalformedRedirect` decodes JSON directly from `rr.Body` and then assigns `body := rr.Body.String()`. `bytes.Buffer.String()` returns only the unread portion, which is empty after `json.Decoder.Decode`; consequently, the 12-marker loop passes even if the serialized response contained a sensitive marker. A temporary probe reproduced this behavior and was removed afterward.
   - The test URL also contains path/query markers but not the userinfo/fragment markers claimed by its comment and marker list.
   - Resolution required: snapshot the response body before decoding (or decode a copy), run marker assertions against that snapshot or `resp.Error`, and include userinfo/fragment in the local asset URL if the test claims sink-level coverage for them. Run the focused admin test again after correction.

### Independent Verification

- `go test ./internal/updater/ -count=1` — PASS.
- `HTTPS_PROXY=http://127.0.0.1:9 go test ./internal/updater/ -count=1` — PASS.
- `go test ./internal/admin/ -run 'TestHandleUpdateApply|TestWriteUpdateApply' -count=1` — PASS, but the finding above makes the negative sink assertion ineffective.
- `go test -race ./internal/updater/ ./internal/admin/` — PASS.
- `go test ./...` — PASS.
- `go vet ./internal/updater/ ./internal/admin/`, `gofmt -l`, and `git diff --check` — PASS/clean.

### Re-review Conclusion

No implementation-level URL leak was found in the fail-closed updater code, and the updater-level security tests exercise the intended paths. One reproducible test defect remains in the mandatory admin API acceptance test, so the feature should not be considered fully validated until that test is corrected and rerun. The earlier “no open issues remain” statement is superseded by this re-review.

## Re-review Finding Resolved (2026-07-10)

The re-review finding is addressed. Both reported problems were confirmed real and fixed:

### Root Cause Confirmed

`bytes.Buffer.String()` returns `buf[off:]` (the unread portion); `json.NewDecoder(rr.Body).Decode` advances `off`, so a subsequent `rr.Body.String()` yields an empty string and the 12-marker absence assertions degrade to checks against `""`, which always pass.

### Fix (`internal/admin/update_handler_test.go`)

1. Snapshot `body := rr.Body.String()` before decoding, then `json.Unmarshal([]byte(body), &resp)`; added a sanity assertion that `body` is non-empty and contains `download asset:`, proving the snapshot is the real response, not `""`.
2. Injected userinfo and fragment markers into the local asset URL: `http://username-secret:password-secret@<host>/path-secret?query-key=query-secret#fragment-secret`, so the sink test genuinely covers the userinfo/fragment vectors rather than only path/query.
3. The 12-marker negative assertions now run against the captured `body` snapshot.

### Effectiveness Probe

To prove the negative assertions are no longer vacuous, the `client.Do` error branch in `downloadFileWithLimit` was temporarily reverted to the raw `return nil, err` (the original leaking form) and only `TestHandleUpdateApplyRedactsMalformedRedirect` was run:

```
[FAIL] TestHandleUpdateApplyRedactsMalformedRedirect
   update_handler_test.go:199: response body leaked "username-secret": {"success":false,"restarting":...
```

The test caught the `username-secret` marker reaching the JSON sink through the `download asset: %w` chain, confirming the captured body is non-empty, the userinfo vector is exercised, and the assertion is effective. The redaction was restored immediately (`git diff` confirms the only difference is `return nil, err` → redacted message; no PROBE residue).

### Re-run Validation (all exit 0)

| Command | Result |
| --- | --- |
| `gofmt -w ...` + `gofmt -l` | OK, empty |
| `go test ./internal/updater/ -count=1` | 69 passed |
| `go test ./internal/admin/ -run 'TestHandleUpdateApply\|TestWriteUpdateApply' -count=1` | 5 passed |
| `HTTPS_PROXY=http://127.0.0.1:9 go test ./internal/updater/ -count=1` | 69 passed |
| `go test -race ./internal/updater/ ./internal/admin/` | 188 passed (2 packages) |
| `go test ./...` | 1395 passed (16 packages) |
| `go vet ./internal/updater/ ./internal/admin/` | No issues |

### Conclusion

The sink-level test defect from the re-review is fixed and proven effective by probe; no new URL leak exists in the implementation. The feature is now fully validated.

## Final Codex Verification (2026-07-10)

Codex independently verified the correction. The test snapshots the non-empty JSON body before decoding, exercises userinfo/path/query/fragment plus a malformed redirect target, and checks all markers against the captured sink output. As a negative control, temporarily restoring the raw `return nil, err` branch made `TestHandleUpdateApplyRedactsMalformedRedirect` fail on `username-secret` and display the complete old leak chain; the fail-closed branch was then restored and no probe residue remained.

Fresh verification passed: focused admin tests, updater tests, forced-proxy updater tests, `go test ./...`, `go test -race ./internal/updater/ ./internal/admin/`, `go vet`, `gofmt -l`, and `git diff --check`. No remaining logic, security, or test defects were found in this change.
