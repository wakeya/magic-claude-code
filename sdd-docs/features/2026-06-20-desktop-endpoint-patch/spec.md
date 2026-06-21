# CLI/Desktop Endpoint Completeness and TLS Hardening Spec

Local page: N/A (proxy handler + cert + server)
Proxy entry: `internal/proxy/hardcoded.go`, `internal/proxy/server.go`, `internal/proxy/handler.go`, `internal/cert/ca.go`, `internal/cert/cert.go`
Reference sources: `mcc-endpoint-patch-plan.md`; MCC v0.5.0 source and diff set in `magic-claude-code-v0.5.0`; Desktop request logs observed behind MCC
Stack: Go 1.26 stdlib (`net/http`, `crypto/tls`, `crypto/x509`, `encoding/json`)
Last updated: 2026-06-20
Progress: 9 / 9 completed

## Overall Analysis (CLI Baseline + Desktop Additions)

### Problem Summary

Through request-log analysis of Claude Code CLI traffic plus additional Desktop traffic running behind MCC, three categories of issues were identified:

| Category | Endpoint | Symptom | Root Cause |
| --- | --- | --- | --- |
| Path mismatch | `POST /api/event_logging/v2/batch` | 404 | MCC only registers v1 path |
| Unregistered endpoint | `HEAD/GET /api/desktop/{platform}/{arch}/{type}/update` | 404 | Desktop adds a probe path MCC did not previously intercept |
| Imprecise response | `policy_limits`, `bootstrap`, `settings` | 200 but client logs warnings | Empty `{}` missing required fields |

### Endpoint 1: Event Logging v2

Desktop calls `/api/event_logging/v2/batch` (v2), but MCC registers only `/api/event_logging/batch` (v1). The source only checks `statusCode === 200` — it does not parse the response body. A simple path addition fixes the 404.

Retry logic in source: max 3 retries (1s, 3s intervals); only retries on 5xx/429. A 404 stops immediately, so the failed request is harmless but produces noise in logs.

### Endpoint 2: Desktop Update Check

Desktop auto-checks for updates at startup via:

```
HEAD /api/desktop/{platform}/{arch}/{type}/update?device_id={UUID}
GET  /api/desktop/{platform}/{arch}/{type}/update?device_id={UUID}
```

`{type}` is `msix` or `squirrel` depending on installer format.

Source logic (two consumers):

1. **Version check** (`s7t()`): fetches URL, parses JSON, extracts `currentRelease` from the top-level payload. Non-OK → returns `null` silently.
2. **Auto-updater** (`oTn()`): MSIX mode uses Electron `autoUpdater` with `serverType: "json"`, expecting Squirrel/Nuts JSON.

Both consumers tolerate non-OK responses gracefully (return `null` or skip), so the 404 does not break functionality — it only wastes a request per launch. Returning `200` with `{"currentRelease": "<current>"}` tells Desktop "you are up to date."

### Endpoint 3: Policy Limits

Current response: `{}` (empty object).

Source validation (`k9t`) checks:
1. Value is not null
2. typeof is "object"
3. Not an array
4. `restrictions` must be a non-null, non-array object
5. `compliance_taints` must be undefined or array

Empty `{}` passes checks 1–3 but fails check 4 (`restrictions` is undefined). Validation failure logs a warning and degrades policy enforcement. Adding `restrictions: {}` and `compliance_taints: []` eliminates the warning.

### Endpoint 4: Bootstrap

Current response: `{}` (empty object).

Source parses: `r.client_data` (expected object), `r.additional_model_options` (expected array), and `r.cwk_cfg_key` (expected string or null). Empty `{}` makes all three `undefined`, which the source handles by degrading to defaults. Returning all three fields preserves the CLI baseline and the Desktop Cowork variant selection path.

### Endpoint 5: Remote Settings

Current response: `{}` (empty object).

Source parses: `data.settings` (expected object). Empty `{}` makes `settings` undefined, which degrades to `{}`. The source tolerates this (also treats 404/204 as empty settings). Wrapping in `{"settings": {}}` matches the expected shape and eliminates any degradation logging.

### TLS Certificate Chain Issues

Three certificate-related problems were identified from v0.5.0 source analysis:

#### Problem: Certificate Naming Inconsistency

CA certificate uses `Organization: "Claude Proxy Local CA"` and server certificate uses `Organization: "Claude Proxy"`. These names do not match the product identity ("MCC"). Renaming to `MCC Proxy Local CA` and `MCC Proxy` aligns the certificate identity with the project. Existing certificates are unaffected (only PEM format is validated on load); deleting old certs and restarting regenerates with the new names.

#### Problem: Incomplete Certificate Chain in server.crt

`SaveServerCert` writes only the server certificate PEM block, omitting the CA certificate. During TLS handshake, clients without the CA pre-installed cannot build the trust chain (`server → CA → root`). The fix appends the CA certificate DER after the server certificate in the same PEM file. Go's `tls.LoadX509KeyPair` automatically parses all CERTIFICATE blocks to build the chain.

Signature change: `SaveServerCert(certDER, caCertDER []byte, privateKey)` — adds `caCertDER` parameter. Caller `EnsureServerCert` passes `caCertDER` through.

#### Problem: TLS Handshake Errors Missing SNI

`http.Server.ListenAndServeTLS` delegates handshake to Go internals, producing error logs like:
```
http: TLS handshake error from 127.0.0.1:14638: remote error: tls: unknown certificate
```

These logs lack the SNI domain name, making it impossible to determine which domain triggered the failure.

The fix replaces `ListenAndServeTLS` with a custom `tlsListener`:
- `GetCertificate` callback captures SNI (`hello.ServerName`) into a `sync.Map` keyed by remote address.
- `Accept()` calls `tlsConn.Handshake()` explicitly; on failure, retrieves SNI from the store and logs `TLS handshake error from <addr> (SNI=<domain>): <err>`.
- Successful handshakes clean up the SNI store entry.

SNI availability depends on handshake stage:

| Scenario | SNI Available | Reason |
| --- | --- | --- |
| ClientHello received | Yes | SNI in ClientHello extension |
| Client disconnects before ClientHello | No | `GetCertificate` not called, logs `(no SNI)` |
| TLS version/cipher mismatch | No | Handshake fails before `GetCertificate` |
| Certificate verification failure | Yes | SNI already captured in `GetCertificate` phase |

### Request/Response Logs Missing Domain

Current request and response log lines use `r.URL.Path` only, omitting `r.Host`. When multiple domains are proxied (e.g., `api.anthropic.com`, other intercepted hosts), logs cannot distinguish which domain a request targeted.

The fix adds `r.Host` to both the request-entry log (line 132) and the response-exit log (line 252) in `handler.go`:

```
# Before
[abc123] >>> POST /v1/messages model=...
[abc123] <<< 200 model=...

# After
[abc123] >>> POST api.anthropic.com/v1/messages model=...
[abc123] <<< 200 api.anthropic.com/v1/messages model=...
```

`r.Host` originates from the TLS SNI value set during the handshake, propagated by Go's HTTP server.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | event_logging v2 path support | `hardcoded.go` (path match) | Unit test: v2 path returns 200 |
| 2 | Planned | Desktop update check endpoint | `hardcoded.go` (prefix match + handler) | Unit test: HEAD 200, GET JSON |
| 3 | Planned | Policy limits response precision | `hardcoded.go` (dedicated handler) | Unit test: response contains `restrictions` |
| 4 | Planned | Bootstrap response precision | `hardcoded.go` (handler update) | Unit test: response contains `client_data` |
| 5 | Planned | Remote settings response precision | `hardcoded.go` (dedicated handler) | Unit test: response contains `settings` |
| 6 | Planned | Certificate naming consistency | `ca.go`, `cert.go` | Build; verify generated cert subject names |
| 7 | Planned | Complete certificate chain in server.crt | `cert.go` (SaveServerCert + EnsureServerCert) | Unit test: server.crt contains 2 PEM blocks |
| 8 | Planned | TLS handshake SNI logging | `server.go` (custom tlsListener) | Unit test: SNI appears in error log |
| 9 | Planned | Request/response log domain | `handler.go` (add r.Host) | Manual: logs include domain name |

## Requirements

### Deliverables

1. `POST /api/event_logging/v2/batch` is intercepted and returns `200 {}`, same as the existing v1 handler. Both v1 and v2 paths route to `handleEventLogging`.
2. `HEAD` and `GET` requests matching `/api/desktop/**/update` are intercepted. HEAD returns `200` with empty body. GET returns `200 {"currentRelease": "<version>"}` where `<version>` is the Desktop version constant.
3. `GET /api/claude_code/policy_limits` returns `{"restrictions": {}, "compliance_taints": []}` instead of `{}`.
4. `GET /api/claude_cli/bootstrap` returns `{"client_data": {}, "additional_model_options": [], "cwk_cfg_key": null}` instead of `{}`.
5. `GET /api/claude_code/settings` returns `{"settings": {}}` instead of `{}`.
6. Unit tests cover all five changes.
7. No existing endpoint behavior regresses.
8. CA certificate `Organization` and `CommonName` are `MCC Proxy Local CA`; server certificate `Organization` is `MCC Proxy`. Existing certificates are not regenerated automatically — only newly generated certs use the new names.
9. `SaveServerCert` writes both the server certificate and the CA certificate PEM blocks to `server.crt`, forming a complete trust chain. `EnsureServerCert` passes the CA DER to `SaveServerCert`.
10. TLS handshake errors include the SNI domain name when available (`(SNI=api.anthropic.com)` or `(no SNI)`).
11. Request-entry and response-exit logs in `handler.go` include `r.Host` before `r.URL.Path`. `r.Host` is debug metadata from the incoming request, not a trusted security boundary.
12. Unit tests cover the certificate chain (multi-PEM-block) and TLS SNI logging.
13. No existing test or endpoint behavior regresses after the server.go TLS listener refactor.

### Constraints

1. Tasks 1–5 are confined to `internal/proxy/hardcoded.go`.
2. The desktop update version constant (`1.13576.0`) should be defined as a named constant, not a magic string, for easy maintenance.
3. `policy_limits` and `settings` must be extracted from the `handleEmptyResponse` case group to dedicated handlers.
4. The desktop update prefix `/api/desktop/` must not be too broad — it should only match paths ending in `/update` to avoid intercepting unrelated desktop API traffic.
5. Response bodies must use `map[string]any` and be written via the existing `writeJSONResponse` helper.
6. The `SaveServerCert` signature change (`caCertDER` parameter) must update all call sites; the only caller is `EnsureServerCert`.
7. The custom `tlsListener` must preserve the existing `http.Server` configuration (timeouts, handler, stats middleware). Only the TLS layer is refactored.
8. The SNI store uses `sync.Map` to avoid lock contention; entries are cleaned up on both success and failure paths.
9. Existing certificates are not migrated — only newly generated certificates use the updated names and chain format. Document the manual migration step (`rm data/server.crt data/server.key`).

### Edge Cases

1. Desktop sends `GET /api/desktop/win32/x64/msix/update?device_id=...` — matched by prefix, returns version JSON.
2. Desktop sends `GET /api/desktop/darwin/arm64/squirrel/update` — also matched.
3. An unrelated path like `/api/desktop/something_else` — must NOT be intercepted (guard on `/update` suffix or specific pattern).
4. event_logging v1 path still works after adding v2.
5. HEAD request to desktop update — returns 200 with no body.
6. Client disconnects before sending ClientHello — TLS error logs `(no SNI)`.
7. Client sends ClientHello with SNI then fails cert verification — logs include `(SNI=<domain>)`.
8. Existing `server.crt` is a single PEM block — `LoadServerCert` uses `pem.Decode` which reads only the first block; after fix, new certs have two blocks but existing loading logic still works (first block is the server cert).
9. `SaveServerCert` is called during initial cert generation only; existing certs are loaded, not re-saved.

### Non-Goals

1. Do not implement actual update distribution for Claude Desktop (MCC is not a Desktop update server).
2. Do not modify hosts redirection.
3. Do not add new configuration UI for endpoint responses.
4. Do not implement Squirrel/Nuts full release manifest — only the minimal `currentRelease` field needed for "no update" signaling.
5. Do not migrate or regenerate existing certificates automatically.
6. Do not implement automatic CA trust installation for client OS (documented as a manual step).
7. Do not implement mTLS or client certificate verification.

## Task Details

### Task 1: Event Logging v2 Path

#### Requirements

**Objective** - Intercept the v2 event logging endpoint that Desktop uses instead of v1.

**Outcomes** - `/api/event_logging/v2/batch` returns `200 {}` exactly like v1; both paths route to the same handler.

**Evidence** - Unit test sends POST to v2 path and asserts 200 response.

**Constraints** - Must not remove v1 path; both coexist.

**Edge Cases** - Only POST is expected; other methods still return 200 (existing behavior, harmless).

**Verification** - Unit test for v2 path; manual log check shows no more 404 for v2.

#### Plan

1. Add `"/api/event_logging/v2/batch"` to `exactMatches` in `isHardcodedEndpoint`.
2. Add `path == "/api/event_logging/v2/batch"` to the switch case for event logging (OR with existing v1 case).

#### Verification

- [ ] POST to `/api/event_logging/v2/batch` returns 200.
- [ ] POST to `/api/event_logging/batch` (v1) still returns 200.

### Task 2: Desktop Update Check Endpoint

#### Requirements

**Objective** - Intercept desktop update check requests and respond with "no update available".

**Outcomes** - HEAD `/api/desktop/**/update` returns 200; GET returns `{"currentRelease": "1.13576.0"}` as a top-level JSON field.

**Evidence** - Unit test sends HEAD and GET to a desktop update path and asserts correct responses.

**Constraints** - Only match paths under `/api/desktop/` that end with `/update`; do not intercept other desktop API paths. Define version as a named constant `desktopCurrentRelease = "1.13576.0"`. The GET response must stay top-level `currentRelease` to match the existing Desktop probe contract and avoid changing the payload shape.

**Edge Cases** - Paths like `/api/desktop/win32/x64/msix/update?device_id=...` (with query string); `/api/desktop/darwin/arm64/squirrel/update`; HEAD vs GET.

**Verification** - Unit test for both methods; verify query parameters do not break matching.

#### Plan

1. Add a dedicated `isHardcodedEndpoint` branch for `strings.HasPrefix(path, "/api/desktop/") && strings.HasSuffix(path, "/update")`.
2. Add a switch case matching the same `HasPrefix + HasSuffix` condition.
3. Implement `handleDesktopUpdate(w, r)`: HEAD → 200; GET → JSON with `currentRelease`.
4. Define constant `desktopCurrentRelease`.

#### Verification

- [ ] HEAD `/api/desktop/win32/x64/msix/update` returns 200 with empty body.
- [ ] GET returns `{"currentRelease": "1.13576.0"}`.
- [ ] Path `/api/desktop/other` is NOT intercepted (returns false, falls through to 404).

### Task 3: Policy Limits Response Precision

#### Requirements

**Objective** - Return a response that passes the client's policy-limits validation without warnings.

**Outcomes** - `GET /api/claude_code/policy_limits` returns `{"restrictions": {}, "compliance_taints": []}`.

**Evidence** - Unit test asserts response body contains `restrictions` and `compliance_taints`.

**Constraints** - Extract from `handleEmptyResponse` group to a dedicated handler.

**Edge Cases** - None beyond standard GET handling.

**Verification** - Unit test for the response body.

#### Plan

1. Remove `path == "/api/claude_code/policy_limits"` from the `handleEmptyResponse` case group.
2. Add a dedicated switch case calling `handlePolicyLimits(w)`.
3. Implement `handlePolicyLimits(w)` returning the enriched response.

#### Verification

- [ ] Response body contains `"restrictions": {}` and `"compliance_taints": []`.

### Task 4: Bootstrap Response Precision

#### Requirements

**Objective** - Return a bootstrap response with the expected field shape.

**Outcomes** - `GET /api/claude_cli/bootstrap` returns `{"client_data": {}, "additional_model_options": [], "cwk_cfg_key": null}`.

**Evidence** - Unit test asserts response body contains `client_data`, `additional_model_options`, and `cwk_cfg_key`.

**Constraints** - Update existing `handleBootstrap` function body.

**Edge Cases** - None.

**Verification** - Unit test for the response body.

#### Plan

1. Update `handleBootstrap` to return `{"client_data": {}, "additional_model_options": [], "cwk_cfg_key": null}`.

#### Verification

- [ ] Response body contains `"client_data": {}`, `"additional_model_options": []`, and `"cwk_cfg_key": null`.

### Task 5: Remote Settings Response Precision

#### Requirements

**Objective** - Return a settings response with the expected nesting.

**Outcomes** - `GET /api/claude_code/settings` returns `{"settings": {}}`.

**Evidence** - Unit test asserts response body contains `settings`.

**Constraints** - Extract from `handleEmptyResponse` group to a dedicated handler.

**Edge Cases** - None.

**Verification** - Unit test for the response body.

#### Plan

1. Remove `path == "/api/claude_code/settings"` from the `handleEmptyResponse` case group.
2. Add a dedicated switch case calling `handleRemoteSettings(w)`.
3. Implement `handleRemoteSettings(w)` returning `{"settings": {}}`.

#### Verification

- [ ] Response body contains `"settings": {}`.

### Task 6: Certificate Naming Consistency

#### Requirements

**Objective** - Align certificate subject names with the product identity ("MCC") instead of the legacy "Claude Proxy" naming.

**Outcomes** - CA cert `Organization` and `CommonName` become `MCC Proxy Local CA`; server cert `Organization` becomes `MCC Proxy`. Server cert `CommonName` remains `api.anthropic.com`.

**Evidence** - After deleting old certs and restarting, `openssl x509 -in data/ca.crt -noout -subject` shows `O=MCC Proxy Local CA, CN=MCC Proxy Local CA`.

**Constraints** - Existing certs are not auto-regenerated; only newly generated certs use new names. Document the migration step (`rm data/ca.crt data/ca.key data/server.crt data/server.key` then restart), and note that any client OS that previously trusted the old CA must manually install the regenerated `ca.crt` again.

**Edge Cases** - Users with existing certs see no change until they regenerate; this is expected and documented.

**Verification** - Generate fresh certs; verify subject names via openssl or Go's `x509.ParseCertificate`.

#### Plan

1. In `ca.go` `GenerateCA`, change `Organization` and `CommonName` from `"Claude Proxy Local CA"` to `"MCC Proxy Local CA"`.
2. In `cert.go` `GenerateServerCert`, change `Organization` from `"Claude Proxy"` to `"MCC Proxy"`. Keep `CommonName` as `api.anthropic.com`.

#### Verification

- [ ] CA cert subject contains `MCC Proxy Local CA`.
- [ ] Server cert subject contains `MCC Proxy`.
- [ ] Server cert CN remains `api.anthropic.com`.

### Task 7: Complete Certificate Chain in server.crt

#### Requirements

**Objective** - Write the CA certificate after the server certificate in `server.crt` so TLS clients can build the full trust chain without pre-installing the CA.

**Outcomes** - `server.crt` contains two CERTIFICATE PEM blocks (server cert + CA cert). `tls.LoadX509KeyPair` parses both automatically.

**Evidence** - Unit test calls `SaveServerCert` with mock DERs, then reads `server.crt` and asserts two PEM blocks. `openssl crl2pkcs7 -nocrl -certfile data/server.crt | openssl pkcs7 -print_certs` shows both certs.

**Constraints** - `SaveServerCert` signature gains `caCertDER []byte` parameter. Only caller is `EnsureServerCert`, which has `caCertDER` in scope. Existing single-block certs still load correctly (first block is the server cert).

**Edge Cases** - Existing `server.crt` files are single-block; `LoadServerCert` uses `pem.Decode` which reads the first block — still correct. New certs will have two blocks.

**Verification** - Unit test for multi-block PEM output; manual openssl check.

#### Plan

1. Add `caCertDER []byte` parameter to `SaveServerCert`.
2. After encoding the server cert PEM block, encode a second PEM block with `caCertDER`.
3. Update `EnsureServerCert` call site: `m.SaveServerCert(serverCert, caCertDER, serverKey)`.

#### Verification

- [ ] `server.crt` contains two CERTIFICATE PEM blocks.
- [ ] `LoadServerCert` still returns the server cert DER (first block).
- [ ] `tls.LoadX509KeyPair` succeeds on the new file.

### Task 8: TLS Handshake SNI Logging

#### Requirements

**Objective** - Capture the SNI domain name during TLS handshake and include it in handshake error logs for debugging.

**Outcomes** - TLS handshake errors are logged as `TLS handshake error from <addr> (SNI=<domain>): <err>` or `TLS handshake error from <addr> (no SNI): <err>`.

**Evidence** - Unit test creates a `tlsListener` with a mock TLS config, triggers a handshake failure, and asserts the log output contains `SNI=` or `no SNI`.

**Constraints** - Replace `ListenAndServeTLS` with `tls.LoadX509KeyPair` + custom `net.Listener` + `server.Serve`. Preserve all existing `http.Server` configuration (timeouts, handler, stats middleware). Use `sync.Map` for SNI store keyed by `conn.RemoteAddr().String()`; clean up entries on both success and failure. Keep the existing TLS certificate parameters and set an explicit minimum TLS version if the implementation needs to override defaults, but do not widen cipher/protocol support.

**Edge Cases** - Client disconnects before ClientHello (`no SNI` in log); client sends SNI then fails cert verification (`SNI=<domain>` in log); successful handshake cleans up SNI store.

**Verification** - Unit test for SNI capture; manual log observation with untrusted clients.

#### Plan

1. In `server.go` `Start`, replace `ListenAndServeTLS` with `tls.LoadX509KeyPair`.
2. Build `tls.Config` with `GetCertificate` callback that stores `hello.ServerName` in `sync.Map` keyed by `conn.RemoteAddr().String()`.
3. Create `net.Listen("tcp", addr)`, wrap in `tlsListener` struct.
4. Implement `tlsListener.Accept`: accept conn, wrap with `tls.Server`, call `Handshake()`, on error log SNI and close conn, on success clean up SNI store.
5. Call `s.server.Serve(tlsLn)`.

#### Verification

- [ ] Handshake error logs include `(SNI=<domain>)` or `(no SNI)`.
- [ ] Successful handshakes do not produce error logs.
- [ ] Server still starts and serves requests normally.
- [ ] Existing `http.Server` timeouts and handler are preserved.

### Task 9: Request/Response Log Domain

#### Requirements

**Objective** - Include `r.Host` in request-entry and response-exit logs for traceability, prepended to the existing `r.URL.Path` in both log lines.

**Outcomes** - Request log shows `>>> POST api.anthropic.com/v1/messages ...`; response log shows `<<< 200 api.anthropic.com/v1/messages ...`.

**Evidence** - Manual log observation after sending a proxied request shows the domain name prepended to the path.

**Constraints** - Only two `log.Printf` calls change (line 132 and line 252 in `handler.go`). Format: `%s%s` where first `%s` is `r.Host` and second is `r.URL.Path`. Treat the value as debug metadata from the incoming HTTP request, not as a trusted security boundary.

**Edge Cases** - `r.Host` may be empty for HTTP/1.0 requests without Host header; this is rare in practice.

**Verification** - Manual test: send a request through the proxy and check logs.

#### Plan

1. In `handler.go` request-entry log (line 132), add `r.Host` before `r.URL.Path`.
2. In `handler.go` response-exit log (line 252), add `r.Host` before `r.URL.Path`.

#### Verification

- [ ] Request-entry log includes domain name.
- [ ] Response-exit log includes domain name.
