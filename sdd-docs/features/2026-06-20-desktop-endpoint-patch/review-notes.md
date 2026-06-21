# Desktop Endpoint Patch Review Notes

Date: 2026-06-20  
Reviewers: Codex and Claude Code

## Scope

This note summarizes the final review of the desktop endpoint patch and its follow-up hardening changes.

## Key Findings And Resolutions

1. `count_tokens` originally read the request body without a size limit.
   - Resolution: `handleCountTokens()` now uses `io.LimitReader(..., maxRequestBodySize+1)` and returns `413 Request Entity Too Large` when oversized.

2. TLS listener handshake handling originally had starvation and shutdown edge cases.
   - Resolution: handshake work is async, bounded by a timeout and an inflight limit, and `Close()` waits for in-flight handshakes before draining queued connections.

3. Test logging originally depended on the global logger and produced race noise under future parallelization.
   - Resolution: tests now inject local `log.Logger` instances backed by thread-safe buffers.

4. `newTLSListener()` could panic if a future caller passed a nil logger.
   - Resolution: nil now falls back to `log.Default()`.

5. `LoadServerCert()` writes and reads different certificate shapes.
   - Resolution: the function comment now explicitly says it returns only the first PEM block, and production TLS startup uses `tls.LoadX509KeyPair()`.

## Final Review Conclusion

No remaining logic defects or security defects were identified in the reviewed changes.

The final verification run passed:

```bash
go test ./... -race
```

## Residual Notes

- `LoadServerCert()` intentionally returns only the leaf certificate DER from `server.crt`.
- This is documented behavior and does not affect the current TLS startup path.

## Consistency Check

- `sdd-docs/features/README.md` now documents `review-notes.md` and `review-notes_ZH.md` as feature-level archival notes.
- `review-notes.md` and `review-notes_ZH.md` are present together for this feature.
- The review skills index is synchronized in both `~/.codex/skills/` and `~/.claude/skills/`.

## Final Format Check

- `git diff --check` passed with no formatting issues.
