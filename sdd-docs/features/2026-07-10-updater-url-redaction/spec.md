# Updater Download URL Redaction Spec

Status: validated  
Local endpoint: Admin update API (`POST /api/update/apply`)  
Implementation entry: `internal/updater/updater.go` (`DownloadAndApply`, `downloadFileWithLimit`)  
Tests: `internal/updater/updater_test.go`, `internal/admin/update_handler_test.go`  
Runtime: Go 1.26 standard library  
Last updated: 2026-07-10  
Progress: 4 / 4 implementation tasks

## 1. Objective

Prevent attacker-controlled or misconfigured update URLs, redirect targets, and network error messages from exposing credentials or signed URL material through:

1. any error string returned by `Updater.DownloadAndApply` or `downloadFileWithLimit`; and
2. the JSON `error` field returned by `POST /api/update/apply`.

The implementation MUST be fail-closed. It MUST NOT attempt to preserve arbitrary low-level error text. Safe diagnostics are limited to a normalized URL origin, a stable operation/category, an HTTP status code, and a size limit.

## 2. Normative Language

`MUST`, `MUST NOT`, `SHOULD`, and `MAY` are normative requirements. Code examples define the intended contract; minor naming or formatting changes are allowed only when all tests and observable behavior remain equivalent.

## 3. Current Behavior And Verified Root Causes

### 3.1 Existing leak

`downloadFileWithLimit` currently returns raw errors from:

- `http.NewRequestWithContext`;
- `u.client.Do`; and
- `io.ReadAll`.

`DownloadAndApply` wraps those errors with `%w`, and `handleUpdateApply` writes `err.Error()` into the admin API response. There is no current process-log sink for this updater error; the security-relevant sink is the authenticated admin API response and any future caller that formats the returned error.

The existing test uses the real URL `https://user:pass@example.com?token=secret`. It passes when `example.com` responds and fails when a proxy or network failure makes `client.Do` return an error. Verified reproduction against the current code:

```bash
HTTPS_PROXY=http://127.0.0.1:9 \
  go test ./internal/updater/ \
  -run TestDownloadAndApplyRedactsInvalidDownloadURL \
  -count=1
```

### 3.2 Go 1.26 behavior

`http.Client.Do` normally returns a top-level `*url.Error`. Go 1.26 masks a password in the outer `URL` field as `***`, but it can still expose the username, query, fragment, path, and redirect-related text. Therefore tests MUST NOT rely on the password alone to demonstrate the leak.

### 3.3 Why field-only mutation is insufficient

Mutating only `*url.Error.URL` does not sanitize `*url.Error.Err`. For example, an invalid redirect header such as:

```text
Location: https://redirect.example/%zz?token=redirect-secret
```

causes Go to include the raw `Location` value in the nested error. Returning or wrapping that nested error still leaks `redirect-secret`.

### 3.4 Why the existing URL formatter is insufficient

`redactURLForError` removes `User`, `RawQuery`, and `Fragment` from a hierarchical URL, but `url.Parse` also accepts opaque URLs. For example:

```text
user:password-secret@example.com/path?token=query-secret
```

is parsed with scheme `user` and an opaque payload. Removing `RawQuery` does not remove `password-secret`. `http.NewRequestWithContext` accepts that scheme; the later unsupported-protocol error can echo the opaque payload.

## 4. Threat Model And Security Boundary

### 4.1 Untrusted inputs

Treat all of the following as untrusted and potentially secret-bearing:

- `UpdateInfo.DownloadURL` from `ReleaseSource.AssetURL` or a published release asset;
- every URL component: userinfo, path, raw path, query, forced query, fragment, and opaque payload;
- redirect `Location` headers;
- `error` values returned by `RoundTripper`, redirect processing, response bodies, and context/network layers.

### 4.2 Allowed public diagnostic data

An error MAY expose only:

- the normalized lowercase scheme (`http` or `https`);
- the URL host and optional port;
- a fixed operation/category string defined in this spec;
- a numeric HTTP status code; and
- the configured maximum byte count.

The safe display target is the URL origin only:

```text
https://user:pass@example.com:8443/releases/private/asset.tar.gz?token=secret#fragment
  -> https://example.com:8443
```

Path is deliberately omitted because signed CDNs may carry credentials in path segments.

### 4.3 Forbidden public diagnostic data

Returned `Error()` strings and admin JSON MUST NOT contain:

- username or password;
- path or raw path;
- query keys or values, including a bare trailing `?`;
- fragment or raw fragment;
- opaque URL payload;
- redirect `Location` text; or
- any underlying `err.Error()` text from request construction, transport, redirect, body read, or context layers.

### 4.4 URL acceptance policy

A download URL is valid only when all conditions hold:

1. `url.Parse` succeeds;
2. `Opaque == ""`;
3. scheme is `http` or `https`, compared case-insensitively and normalized to lowercase;
4. `Host != ""`;
5. the reconstructed URL is accepted by `http.NewRequestWithContext`.

Userinfo, query, and fragment are allowed in the request URL because some sources use authenticated or signed URLs. They are never allowed in diagnostic output. Relative URLs, opaque URLs, missing-host URLs, unsupported schemes, and malformed escapes are rejected before any transport call.

## 5. Required Design

### 5.1 Parse once and derive a safe origin

Add two focused helpers in `internal/updater/updater.go` near the existing redaction helper:

```go
func parseDownloadURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.Host == "" {
		return nil, false
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil, false
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return parsed, true
}

func safeURLOrigin(parsed *url.URL) string {
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
}

func redactURLForError(raw string) string {
	parsed, ok := parseDownloadURL(raw)
	if !ok {
		return "<invalid-url>"
	}
	return safeURLOrigin(parsed)
}
```

Required properties:

- `parseDownloadURL` MUST discard the parser error; callers MUST NOT format it.
- `safeURLOrigin` receives only a successfully validated URL.
- `redactURLForError` MUST return exactly `<invalid-url>` for every rejected URL.
- Do not add a `redactURLError` helper. Field mutation cannot satisfy the nested-error requirement.

### 5.2 Stable request-failure category

Add a helper that maps errors to fixed public categories without returning the source error text:

```go
func requestFailureCategory(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		return "was canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timed out"
	default:
		return "failed"
	}
}
```

The helper MAY inspect the error with `errors.Is`, but MUST NOT include the error in the returned string. Add the `errors` import to `updater.go`.

### 5.3 Safe `downloadFileWithLimit` flow

Implement the function in this exact order:

1. Parse and validate the raw URL.
2. Derive `target := safeURLOrigin(parsed)`.
3. Build the request from `parsed.String()`.
4. Perform the request.
5. Handle status, body read, and size-limit failures with fixed messages.

Required observable messages:

| Failure | Required format |
| --- | --- |
| URL rejected | `invalid download URL: <invalid-url>` |
| request construction | `create download request for <origin> failed` |
| `client.Do` canceled | `download request to <origin> was canceled` |
| `client.Do` deadline | `download request to <origin> timed out` |
| other `client.Do` failure | `download request to <origin> failed` |
| non-200 status | `unexpected status <code> from <origin>` |
| response-body read | `read download response from <origin> failed` |
| size limit | `download from <origin> exceeds maximum size of <n> bytes` |

Reference structure:

```go
func (u *Updater) downloadFileWithLimit(ctx context.Context, raw string, maxSize int) ([]byte, error) {
	parsed, ok := parseDownloadURL(raw)
	if !ok {
		return nil, errors.New("invalid download URL: <invalid-url>")
	}
	target := safeURLOrigin(parsed)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create download request for %s failed", target)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request to %s %s", target, requestFailureCategory(ctx, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, target)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxSize)+1))
	if err != nil {
		return nil, fmt.Errorf("read download response from %s failed", target)
	}
	if len(data) > maxSize {
		return nil, fmt.Errorf("download from %s exceeds maximum size of %d bytes", target, maxSize)
	}
	return data, nil
}
```

Security constraints:

- None of the new errors may use `%w` or include the local `err` value.
- `DownloadAndApply` MAY keep its existing contextual `%w` wrappers because the wrapped updater error is already safe and has no unsafe unwrap chain.
- Do not return a custom error type with `Unwrap`; preserving the raw cause would reintroduce an unsafe error chain for future callers.
- The success path and the 200-response body bytes remain unchanged.

### 5.4 Safe checksum URL construction

Replace the raw `strings.LastIndex(info.DownloadURL, "/")` logic. Raw string slicing can select a slash inside a query and is inconsistent with the new URL policy.

Add:

```go
func checksumURLForAsset(raw string) (string, bool) {
	base, ok := parseDownloadURL(raw)
	if !ok {
		return "", false
	}
	base.RawQuery = ""
	base.ForceQuery = false
	base.Fragment = ""
	base.RawFragment = ""
	return base.ResolveReference(&url.URL{Path: "SHA256SUMS.txt"}).String(), true
}
```

In `DownloadAndApply`:

```go
sumsURL, ok := checksumURLForAsset(info.DownloadURL)
if !ok {
	return nil, errors.New("invalid download URL: <invalid-url>")
}
```

Then use `sumsURL` as before. The helper intentionally preserves valid scheme, host, port, userinfo, and directory path while removing the asset query/fragment and replacing only the final path segment. Do not expose the returned URL in errors.

## 6. Complete Error-Path Inventory

All paths in `downloadFileWithLimit` are in scope:

| Order | Path | Raw data risk | Required handling |
| --- | --- | --- | --- |
| 1 | URL parse/policy rejection | raw URL and parser error | fixed `<invalid-url>` message |
| 2 | `NewRequestWithContext` | raw URL/parser text | origin + fixed category, discard `err` |
| 3 | `client.Do` | request URL, redirect Location, transport error | origin + fixed category, discard `err` text/chain |
| 4 | non-200 response | request/final response URL | status code + original origin only |
| 5 | `io.ReadAll` | body/transport error text | origin + fixed category, discard `err` |
| 6 | size limit | original URL | limit + origin only |
| 7 | success | none | return bytes unchanged |

The two `DownloadAndApply` download calls—archive and `SHA256SUMS.txt`—MUST both use this same safe path.

## 7. Required Test Helpers

Add these package-local helpers to `internal/updater/updater_test.go` unless equivalent helpers already exist:

```go
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errorReadCloser struct{ err error }

func (r errorReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (errorReadCloser) Close() error                { return nil }
```

Tests MUST use unique markers so a masked password cannot accidentally satisfy an assertion:

```text
username-secret
password-secret
path-secret
query-key
query-secret
fragment-secret
redirect-secret
transport-secret
body-secret
```

Use a shared assertion helper if desired:

```go
func assertNoSensitiveMarkers(t *testing.T, got string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(got, marker) {
			t.Fatalf("output leaked %q: %s", marker, got)
		}
	}
}
```

## 8. Mandatory Test Matrix

### 8.1 URL policy and safe origin

Add table-driven tests for `parseDownloadURL` and `redactURLForError`:

| Input | Valid | Safe display |
| --- | --- | --- |
| `https://example.com/a` | yes | `https://example.com` |
| `HTTP://example.com:8080/a?x=y` | yes | `http://example.com:8080` |
| `https://username-secret:password-secret@example.com/path-secret?query-key=query-secret#fragment-secret` | yes | `https://example.com` |
| `user:password-secret@example.com/path?query-key=query-secret` | no | `<invalid-url>` |
| `ftp://example.com/file` | no | `<invalid-url>` |
| `/relative/path?query-key=query-secret` | no | `<invalid-url>` |
| `https:///missing-host` | no | `<invalid-url>` |
| `https://example.com/%zz?query-key=query-secret` | no | `<invalid-url>` |

For every invalid case, also verify the raw markers are absent from the returned display string.

### 8.2 Reject before transport

Inject a `roundTripFunc` that increments an atomic/counter and fails the test if called. For every invalid URL above, call `downloadFileWithLimit` and assert:

- an error is returned;
- the error is exactly `invalid download URL: <invalid-url>`;
- the transport call count remains zero; and
- no sensitive marker appears.

### 8.3 Transport error

Inject a transport returning `errors.New("transport-secret: " + req.URL.String())`. Call with a valid URL containing all URL markers. Assert:

- the error is `download request to https://example.com failed`;
- it contains none of the URL markers, `transport-secret`, `query-key`, or `token=`; and
- it contains no unwrap-accessible raw cause (`errors.Unwrap(err) == nil` at the direct helper boundary).

### 8.4 Cancellation and deadline

Use transports returning `context.Canceled` and `context.DeadlineExceeded`. Assert exact fixed messages ending in `was canceled` and `timed out`, with no underlying error text beyond that fixed category.

### 8.5 Malformed redirect

Use `httptest.NewServer` to return status 302 and:

```text
Location: https://redirect.example/%zz?query-key=redirect-secret
```

Call `downloadFile` with an original URL whose path/query also contain markers. Assert the returned error contains the original server origin but none of `redirect.example`, `%zz`, `query-key`, or `redirect-secret`.

### 8.6 Response-body read error

Inject a transport returning a 200 response with `Body: errorReadCloser{err: errors.New("body-secret")}` and a non-nil `Request`. Assert the exact fixed read-error message and absence of `body-secret` and all URL markers.

### 8.7 Status and size paths

Update the existing status and size tests to expect origin-only output. The test MUST prove that path, query, fragment, and userinfo are absent. Preserve the existing maximum-size behavior assertion.

### 8.8 Checksum URL construction

Add a table-driven `TestChecksumURLForAsset` covering:

```text
https://user:pass@example.com/releases/v1/asset.tar.gz?token=secret#fragment
  -> https://user:pass@example.com/releases/v1/SHA256SUMS.txt

https://example.com/asset.tar.gz
  -> https://example.com/SHA256SUMS.txt

opaque / relative / unsupported scheme
  -> ok == false, result == ""
```

The test may inspect the internal URL string because this helper produces a request URL, not a diagnostic string. Never print it from production errors.

### 8.9 Hermetic `DownloadAndApply` regression

Rewrite `TestDownloadAndApplyRedactsInvalidDownloadURL` to inject a deterministic failing transport. It MUST:

- perform no outbound network access;
- use unique URL and transport markers;
- assert the contextual prefix `download asset:` remains;
- assert the only URL detail retained is `https://example.com`; and
- assert every sensitive marker is absent.

### 8.10 Admin API end-to-end regression

In `internal/admin/update_handler_test.go`, add a package-local fake `updater.ReleaseSource` that returns a valid newer release and whose `AssetURL` points to a local `httptest` server. Have that server return the malformed redirect from section 8.5. Inject the updater with `Server.SetUpdater`, call `handleUpdateApply`, decode `updateApplyResponse`, and assert:

- HTTP status is 200, matching existing business-error behavior;
- `Success == false`;
- `Error` is non-empty and retains safe high-level context;
- the JSON body contains none of the redirect, path, query, userinfo, or underlying-error markers.

This is the acceptance test for the actual security sink.

## 9. Implementation Tasks

### Task 1 — Write failing URL-policy tests

Files: `internal/updater/updater_test.go`

1. Add the test helpers from section 7.
2. Replace `TestRedactURLForError` with the table in section 8.1.
3. Add reject-before-transport and checksum URL tests.
4. Run:

   ```bash
   go test ./internal/updater/ -run 'Test(ParseDownloadURL|RedactURLForError|DownloadFileRejectsUnsafeURL|ChecksumURLForAsset)' -count=1
   ```

5. Expected before implementation: compile failure for missing helpers or assertion failures against old behavior.

### Task 2 — Implement fail-closed URL parsing and checksum derivation

Files: `internal/updater/updater.go`

1. Add `errors` import.
2. Add `parseDownloadURL`, `safeURLOrigin`, and the revised `redactURLForError`.
3. Add `checksumURLForAsset`.
4. Replace `strings.LastIndex` checksum derivation in `DownloadAndApply`.
5. Run the Task 1 command and require PASS.

### Task 3 — Write and satisfy every download error-path test

Files: `internal/updater/updater.go`, `internal/updater/updater_test.go`

1. Add failing tests from sections 8.3 through 8.9.
2. Run them and confirm at least the transport, redirect, and body tests fail against the old raw-error behavior.
3. Add `requestFailureCategory` and rewrite `downloadFileWithLimit` per section 5.3.
4. Run:

   ```bash
   go test ./internal/updater/ -run 'TestDownload|TestRequestFailureCategory' -count=1
   ```

5. Require PASS and no external network dependency.

### Task 4 — Add sink-level coverage and run full validation

Files: `internal/admin/update_handler_test.go`

1. Add the admin API malformed-redirect regression from section 8.10.
2. Run the focused admin test and require PASS.
3. Run all commands in section 11.
4. Update both spec progress fields and the feature review notes only after all commands pass.

## 10. Non-Goals

- Changing release-source authentication or provider APIs.
- Removing support for valid userinfo or signed query parameters in actual download requests.
- Adding retries, redirect restrictions, certificate changes, or SSRF policy beyond the explicit `http`/`https` absolute-URL requirement.
- Changing checksum verification, archive extraction, binary replacement, restart behavior, or frontend UI.
- Logging raw errors. No debug mode may bypass this redaction contract.

## 11. Validation And Acceptance

Run from the repository root:

```bash
gofmt -w internal/updater/updater.go internal/updater/updater_test.go internal/admin/update_handler_test.go
go test ./internal/updater/ -count=1
go test ./internal/admin/ -run 'TestHandleUpdateApply|TestWriteUpdateApply' -count=1
HTTPS_PROXY=http://127.0.0.1:9 go test ./internal/updater/ -count=1
go test -race ./internal/updater/ ./internal/admin/
go test ./...
```

Acceptance requires all of the following:

1. Every command exits 0.
2. Focused tests perform no public-network access.
3. Invalid URLs never reach a transport.
4. Returned updater errors and admin JSON expose at most a normalized origin and fixed diagnostic fields.
5. Opaque URL, malformed redirect, transport error, body-read error, status, size, cancellation, and deadline cases are covered.
6. No production error path formats or unwraps an untrusted low-level error.
7. Existing successful download and updater behavior remains green.

## 12. Implementation Completion Checklist

- [x] URL policy tests fail before implementation and pass afterward.
- [x] `parseDownloadURL` accepts only absolute hierarchical HTTP(S) URLs with a host.
- [x] `redactURLForError` returns origin-only or `<invalid-url>`.
- [x] Opaque URLs are rejected before transport.
- [x] Checksum URL construction uses parsed URL resolution, not raw string slicing.
- [x] Request construction errors discard raw error text.
- [x] Transport and redirect errors discard raw error text and unwrap chain.
- [x] Body-read errors discard raw error text.
- [x] Status and size errors use origin-only diagnostics.
- [x] Hermetic `DownloadAndApply` test covers all markers.
- [x] Admin API sink-level test covers malformed redirect leakage.
- [x] Package, race, and repository-wide tests pass.
