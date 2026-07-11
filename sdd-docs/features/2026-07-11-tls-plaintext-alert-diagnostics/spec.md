# TLS Plaintext Alert Diagnostics Spec

Local page: N/A<br>
Proxy entry: `internal/proxy/server.go` (`handleConn` handshake-error logging)<br>
Reference sources: Go 1.26 `crypto/tls` (`conn.go` `halfConn.decrypt`, `handshake_server_tls13.go`), RFC 8446 §6, RFC 6066<br>
Stack: Go 1.26 stdlib (`net`, `sync`)<br>
Last updated: 2026-07-11<br>
Progress: 5 / 5 (validated)

## Overall Analysis (Source Analysis)

### Symptom

The transparent proxy (`mcc`) terminates TLS for `api.anthropic.com`. During long conversations, `handleConn` intermittently logged six consecutive `local error: tls: bad record MAC` errors with a 3s/6s backoff pattern, always inside an active SSE streaming window.

### Why "bad record MAC" Is a Misleading Surface

In Go's `crypto/tls`, `alertBadRecordMAC` is returned by `halfConn.decrypt` (`conn.go:380-383`) whenever AEAD `Open` fails. `handleConn` logged only this Go-level error, which conflates two fundamentally different conditions:

1. **Genuine key/record failure** — the client's ciphertext cannot be decrypted with the negotiated handshake key.
2. **Client-side trust rejection** — the client sent a *plaintext* alert (e.g. `unknown_ca`) after failing to verify the proxy's self-signed certificate. The proxy, having already installed its handshake traffic key, tries to AEAD-decrypt this plaintext alert, which of course fails authentication → surfaces as `bad record MAC`.

### Root-Cause Chain (verified via byte-level dump)

1. Claude Code (Bun runtime + BoringSSL) issues background auxiliary requests during long conversations (context compaction, summarization) over a TLS path that **does not read `NODE_EXTRA_CA_CERTS`**.
2. The client cannot verify the proxy's self-signed CA → sends a plaintext `fatal / unknown_ca [48]` alert.
3. The proxy AEAD-decrypts the plaintext alert → fails → logs `bad record MAC`.
4. The client retries with 3s/6s backoff → the characteristic six-error burst.

A byte dump of a failing connection showed record sequence `[Handshake/1538 Alert/2]`; the trailing alert parsed as `level=2 (fatal), description=0x30 (48 = unknown_ca)`.

### Resolution Split

- **Client side (the actual root fix):** install the proxy CA into the **system CA store** (`update-ca-certificates`). A/B testing with `SSL_CERT_FILE` unset and Claude Code restarted confirmed the system store alone is sufficient — every Bun TLS path reads it. `SSL_CERT_FILE` is not required, and pointing it at a single CA file is actively harmful (it replaces rather than appends for some TLS implementations, breaking public-CA trust). Binary installs perform this automatically via `bootstrap`; Docker requires running `setup-host.sh`.
- **Proxy side (this feature):** an incremental TLS record parser (`alertDetectingConn`) that detects strictly-parsed plaintext alerts and appends the real reason to the handshake-failure log, so future occurrences report the truth instead of the misleading bare `bad record MAC`.

### Design Constraints (from review)

1. **No raw-byte buffering** — a first-N-byte buffer has no general guarantee of retaining a later alert (it depends on where the alert falls relative to N); the previous diagnostic write did not strictly enforce its nominal cap; and production code must not retain or log raw handshake bytes.
2. **Structured record parsing only** — never magic-byte search (`15 03 03 00 02`); handshake/AppData payloads can legitimately contain that sequence.
3. **Only `ContentType=21, length=2` records qualify** — TLS 1.3 encrypted alerts are outer AppData(23); TLS 1.2 encrypted alerts have length > 2 (MAC/padding). Both are correctly excluded.
4. **Zero allocation in the hot path** — only the 2-byte alert is retained.

## Development Checklist

- [x] `alertDetectingConn` incremental record parser (`internal/proxy/alert_detect.go`)
- [x] `alertName` / `alertLevelName` per RFC 8446 §6, verified against Go 1.26 stdlib
- [x] `handleConn` integration: wrap conn, append `hint()` to failure log
- [x] Unit tests: feed chunking, header/payload splits, truncated alert, payload false positives, AppData/encrypted-alert exclusion, unknown alert numbers
- [x] Integration test: `TestTLSListenerAlertHintOnUntrustedCert` drives `handleConn` end-to-end (TLS 1.2 plaintext alert)
- [x] FAQ entry in `CLAUDE.md`
- [x] Removed temporary diagnostic switches (`MCC_DISABLE_SESSION_TICKET`, `MCC_DEBUG_HANDSHAKE`)

## Requirements

### Functional

- TLS handshake-failure logs MUST distinguish "client sent a plaintext alert" from genuine key/record failures.
- The original Go error MUST be preserved (the hint is appended, not a replacement).
- Only strictly-parsed plaintext alert records (`ContentType=21, length=2`) trigger the hint.

### Non-functional

- Zero allocation in the `Read` hot path; the parser operates on fixed-size struct fields.
- No retention of raw handshake bytes.
- No log injection: alert description is a single byte; unknown values format numerically.

### Out of scope

- Client-side CA configuration (documented in FAQ, not in proxy code).
- Encrypted-alert detection (TLS 1.3 post-ServerHello; TLS 1.2 post-CCS) — structurally indistinguishable from application data without the key.

> **Plan format note:** This spec is a post-implementation archive. The plans below record the actual implementation steps retrospectively, not a forward TDD red-green sequence. Verification checkboxes reflect evidence gathered after implementation.

## Task Details

### Task 1: Incremental TLS Record Parser

#### Requirements

**Objective** - Detect plaintext alerts the client sends during a failing TLS handshake, without buffering raw bytes.

**Outcomes** - `alertDetectingConn` wraps `net.Conn`, incrementally parses TLS records in `Read`, retains only the last detected 2-byte plaintext alert; `hint()` returns the formatted annotation.

**Evidence** - `internal/proxy/alert_detect.go`; `feed` is allocation-free; 14 unit tests pass.

**Constraints** - Structured record parsing (5-byte header + declared payload length); only `ContentType=21, length=2` records captured; fixed-size parser state.

**Edge Cases** - Records split across reads; header split; alert payload split; truncated alert (declared length=2 but fewer bytes received); handshake payload containing alert-like bytes; AppData records; TLS 1.2 encrypted alerts (length>2).

**Verification** - `go test ./internal/proxy -run 'TestFeed|TestAlert|TestHint' -v`.

#### Plan

1. Define `alertDetectingConn` with fixed-size fields: 5-byte header buffer, `hdrFilled`, `payloadType`, `payloadRemain`, `readingAlert`, 2-byte `alertBuf`, detected `level`/`desc`.
2. Implement `feed` as a state machine: if `payloadRemain>0` consume payload (capture when `readingAlert`, else skip via `min(len(data), payloadRemain)`); else read header; on header completion set `readingAlert = payloadType==21 && payloadRemain==2`.
3. Implement `Read` to call `feed(b[:n])` under mutex after each underlying read.
4. Implement `hint()` (mutex-guarded; returns `""` or `(client sent plaintext <level> alert: <name> [<desc>])`).
5. Implement `alertName` / `alertLevelName` per RFC 8446 §6 / RFC 6066.

#### Verification

- [x] `TestFeedAlertSameRead`, `TestFeedAlertByteByByte`, `TestFeedHeaderSplit`, `TestFeedAlertPayloadSplit`, `TestFeedTruncatedAlert`
- [x] `TestFeedNoFalsePositiveInHandshakePayload`, `TestFeedNoFalsePositiveAppData`, `TestFeedNoFalsePositiveEncryptedAlert`
- [x] `TestFeedUnknownAlertNumber`, `TestAlertNameKnown`, `TestAlertLevelName`, `TestFeedMultipleAlertsKeepsLast`, `TestHintEmptyWhenNotDetected`, `TestHintFormat`

### Task 2: handleConn Integration

#### Requirements

**Objective** - Surface the detected alert in the handshake-failure log.

**Outcomes** - `handleConn` wraps every accepted conn with `alertDetectingConn`; the failure branch appends `ac.hint()`; log becomes `bad record MAC (client sent plaintext fatal alert: unknown_ca [48])` when an alert is detected, unchanged otherwise.

**Evidence** - `internal/proxy/server.go` `handleConn`; `TestTLSListenerAlertHintOnUntrustedCert` verifies the annotation through a real TLS listener. After the CA fix, production no longer produces this failure, so no post-fix runtime annotation was available to observe.

**Constraints** - Default-on (no env switch); original Go error preserved.

**Edge Cases** - No alert detected → empty hint, log unchanged; normal handshakes unaffected (parser state is per-conn and discarded with it).

**Verification** - `go test ./internal/proxy -run TestTLSListener`; existing `TestTLSListenerLogsSNIOnUntrustedCert` still passes.

#### Plan

1. In `handleConn`, before `tls.Server`, wrap: `ac := &alertDetectingConn{Conn: conn}; conn = ac`.
2. In the `Handshake()` failure branch, compute `extra := ac.hint()` and append `%s` to both log format strings.

#### Verification

- [x] `TestTLSListenerLogsSNIOnUntrustedCert` passes (no regression).
- [x] `TestTLSListenerAlertHintOnUntrustedCert` demonstrates the annotation end-to-end (asserts `bad_certificate [42]` + the original `remote error: tls: bad certificate` error). Note: after the CA fix, no such failures occur in production; the pre-fix `unknown_ca` was captured by a temporary byte dump, and the post-fix annotation is verified by the integration test, not by runtime observation.

### Task 3: alertName Mapping Correction

#### Requirements

**Objective** - Correct alert-name mappings to RFC 6066 / RFC 8446, matching Go 1.26 stdlib.

**Outcomes** - `alertName` cases 111–116 + 121 align with stdlib; the erroneous `case 117` removed.

**Evidence** - Review caught the 111–117 shift; `alert_detect.go` corrected; `TestAlertNameKnown` extended.

**Constraints** - Must match Go 1.26 `crypto/tls` alert assignments.

**Edge Cases** - Unknown numbers format as `alert_<N>` (single byte, no injection).

**Verification** - `go test ./internal/proxy -run TestAlertNameKnown -v`.

#### Plan

1. Audit `alertName` cases 109–121 against RFC 6066 / RFC 8446 and Go stdlib.
2. Add 111 (`certificate_unobtainable`), 112 (`unrecognized_name`); correct 113–116; remove bogus 117; add 116 (`certificate_required`) and 121 (`encrypted_client_hello_required`).
3. Extend `TestAlertNameKnown` with 111–116, 120, 121.

#### Verification

- [x] `TestAlertNameKnown` covers 0, 40, 42, 45, 48, 51, 70, 111–116, 120, 121.

### Task 4: End-to-End handleConn Integration Test

#### Requirements

**Objective** - Verify the full `Read → feed → detected → hint → log` path through a real `tlsListener`.

**Outcomes** - `TestTLSListenerAlertHintOnUntrustedCert` in `server_test.go`.

**Evidence** - Test passes under `-race`; asserts both original-error preservation and hint append.

**Constraints** - Pin both client and server to `MaxVersion: VersionTLS12` so the client's certificate-verification alert is plaintext (pre-ChangeCipherSpec). TLS 1.3 same-stage alerts are encrypted (outer AppData) and out of scope.

**Edge Cases** - Handshake succeeds unexpectedly → `t.Fatal`, because the generated self-signed test certificate must remain untrusted and a skip would hide a broken acceptance test.

**Verification** - `go test -race ./internal/proxy -run TestTLSListenerAlertHint -v`.

#### Plan

1. Reuse the `TestTLSListenerLogsSNIOnUntrustedCert` scaffolding (test cert, `newTLSListener` with a `logBuf`).
2. Pin `tlsCfg.MaxVersion` and the `tls.Dial` config to `VersionTLS12`.
3. Fail the test if the dial unexpectedly succeeds; after the expected failure, wait for the `client sent plaintext` log entry.
4. Assert the log contains the exact `bad_certificate [42]` hint and preserves the original `remote error: tls: bad certificate` error.

#### Verification

- [x] `TestTLSListenerAlertHintOnUntrustedCert` passes under `-race`.

### Task 5: FAQ Documentation

#### Requirements

**Objective** - Document the root cause and fix for operators encountering this log.

**Outcomes** - `CLAUDE.md` 常见问题 entry.

**Evidence** - `CLAUDE.md` "bad record MAC / unknown_ca" Q&A.

**Constraints** - The bilingual-output policy does not cover CLAUDE.md (developer doc, kept in Chinese to match existing entries); the system CA store is the primary fix; `SSL_CERT_FILE` is a fallback only, pointing at the system bundle.

**Verification** - Manual review.

#### Plan

1. Add a Q&A under `## 常见问题`.
2. Explain the root cause (a client TLS path not reading `NODE_EXTRA_CA_CERTS`) and the misleading surface.
3. Give the system-CA-store fix; note `SSL_CERT_FILE` as a fallback only, pointing at the system bundle (never a single CA file).

#### Verification

- [x] Entry added and reviewed.
