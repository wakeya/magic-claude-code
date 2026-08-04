# DeepSeek Cache Usage Parsing Review Notes

Date: 2026-08-04  
Reviewers: Codex coordinator and pi security-review worker

## Scope

Reviewed commit `05a507f` on `fix/deepseek-cache-usage-parsing`, covering the shared usage normalizer and its Chat Completions / Responses non-streaming and SSE call paths.

## Key Findings And Resolutions

1. **Low — negative zero bypasses validation.** `usageNumber` rejects `number < 0`, but IEEE-754 `-0.0 < 0` is false. A response containing `cached_tokens: -0.0` is treated as an explicitly present zero, blocks fallback to `prompt_cache_hit_tokens`, and can under-report cache reads. Resolution: recommend rejecting `math.Signbit(number)` and adding a `-0.0` regression test before treating the branch as fully hardened.
2. **Informational — Responses input arithmetic changed intentionally.** The new code subtracts cache-read and cache-creation tokens from `input_tokens`. This is correct for OpenAI Responses semantics where cached tokens are included in total input, and is documented in the spec; a non-conforming provider that reports cached tokens separately would be under-counted. Resolution: retain for the current scope; consider provider-specific semantics if such a backend is introduced.
3. **No high-severity security defect found.** The review found no reachable panic, unbounded allocation, command/path/network sink, injection, authorization bypass, or numeric overflow introduced by this change. Upstream usage values remain inherently trusted for reporting, as they were before this commit.

## Final Review Conclusion

The branch is functionally sound for its stated DeepSeek/OpenAI usage-parsing scope and has no identified exploitable security vulnerability. It should receive the one-line negative-zero hardening fix if exact cache statistics are required; otherwise the residual issue is low-severity correctness robustness, not a security blocker.

## Residual Notes

- Verified `go test ./...`, `go vet ./internal/proxy/transform`, and `git diff --check`.
- The separate protocol-conversion cache degradation remains out of scope and is tracked by the independent protocol-cache-prefix-stability spec.
