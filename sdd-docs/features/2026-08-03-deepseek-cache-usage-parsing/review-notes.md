# DeepSeek Cache Usage Parsing Review Notes

Date: 2026-08-04
Reviewers: Codex coordinator and pi security-review worker

## Scope

- Round 1 reviewed commit `05a507f` on `fix/deepseek-cache-usage-parsing`, covering the shared usage normalizer and its Chat Completions / Responses non-streaming and SSE call paths.
- Round 2 (this document revision) re-reviewed the branch at current HEAD `f896a53` (implementation `05a507f` + negative-zero fix `be8ed50` + docs `f896a53`) against `main`, read-only, covering the same conversion paths plus the downstream `internal/usage` int64/DB boundary.

## Key Findings And Resolutions

### Round 1 (2026-08-04)

1. **F-01 — Resolved low — negative zero bypassed validation.** `usageNumber` originally rejected `number < 0`, but IEEE-754 `-0.0 < 0` is false. Commit `be8ed50` now rejects `math.Signbit(number)` and adds a runtime-negative-zero regression test, so `cached_tokens: -0.0` no longer blocks fallback to `prompt_cache_hit_tokens`.
2. **F-02 — Informational — input arithmetic assumes total includes cached tokens.** The new code subtracts cache-read and cache-creation tokens from total input. This is correct for OpenAI/DeepSeek semantics where cached tokens are included in the total; a non-conforming provider that reports cached tokens outside the total would be under-counted. Resolution: retain for the current scope; consider provider-specific semantics if such a backend is introduced. (Restated precisely in Round 2 below.)
3. **No high-severity security defect found.** The review found no reachable panic, unbounded allocation, command/path/network sink, injection, authorization bypass, or numeric overflow introduced by this change. Upstream usage values remain inherently trusted for reporting, as they were before this commit.

### Round 2 (2026-08-04, HEAD f896a53)

1. **No new defects.** The re-review at HEAD `f896a53` found no new high-, medium-, or low-severity functional logic defects and no exploitable security vulnerability. Verified with adversarial probes against the real code (negative zero via JSON/string/json.Number/float32, NaN/Inf/1e300, fractional values, all numeric types, hit/miss/write/total contradictions, nil/junk fields, SSE/non-stream parity): all resolve to clamped, non-negative output with no panic.
2. **F-01 — Confirmed fully closed by `be8ed50`.** Every reachable runtime negative-zero form is rejected: JSON `-0`/`-0.0` decoded by `encoding/json`, strings `"-0"`/`"-0.0"` via `strconv.ParseFloat`, `json.Number("-0")`/`"-0e0"`, and a runtime float32 negative zero (sign preserved through the float64 conversion). No false positives: explicit `+0` (JSON `0`, strings `"0"`/`"+0"`) is preserved as an explicit zero and correctly blocks the fallback. Note: `math.Signbit` reports both negative zero and negative NaN; negative NaN is already excluded by the adjacent `math.IsNaN` check, so no legitimate value can be misclassified.
3. **F-02 — Restated: applies to every endpoint.** The `total - cache_read - cache_creation` arithmetic assumes the total input already includes cached tokens. If a non-standard vendor reports cache tokens *outside* the total, BOTH Chat Completions and Responses conversion can under-count uncached input: the derived uncached value is `total - hit - write` instead of the true miss count, and it can clamp to zero while real uncached input is nonzero. DeepSeek's and OpenAI's currently frozen semantics are that the total includes cached tokens, so this remains informational and does not block the branch; provider-specific configuration would be required if a non-conforming backend is introduced.
4. **Informational — huge finite values and fractional tokens.** The transform passes huge finite numbers (e.g. `1e300`) and fractional token values (e.g. `600.7`) through to the client-facing JSON. The downstream int64 boundary (`internal/usage` `usageFieldInt64`) rejects out-of-range values and truncates fractions, so this is the existing tolerance policy — a record may drop the field or record a truncated count, but there is no exploitable DB overflow or negative wrap. No change required for this branch.
5. **Low-priority test gaps.** Not covered by the current tests: the Responses `input_tokens_details.cached_tokens` fallback path, the Chat top-level `cache_read_input_tokens` legacy path, and SSE/non-stream output parity for the explicit-zero cache case. Optional follow-up, not blocking.
6. **Race not run.** `go test -race` was not executed because this change introduces no shared concurrent state: the normalizer is pure per-request map handling and both SSE conversion paths are single-goroutine.

## Final Review Conclusion

The branch is functionally sound for its stated DeepSeek/OpenAI usage-parsing scope and has no identified exploitable security vulnerability. The negative-zero correctness gap (F-01) is fully closed in `be8ed50`; the total-includes-cached-tokens assumption (F-02) applies to both Chat and Responses and is informational under current DeepSeek/OpenAI semantics; the remaining robustness observations and test gaps are informational or low priority.

## Residual Notes

- Round-2 verification ledger (all passing):
  - `go test ./internal/proxy/transform -count=1` → ok
  - `go test ./...` → ok (15 packages, 0 FAIL)
  - `go vet ./internal/proxy/transform` → clean
  - key usage/cache tests `go test ./internal/proxy/transform -run 'PromptCache|CachedTokens|CacheTokens|ExplicitZero|UncachedUsage' -count=20` → ok
  - `git diff main..HEAD --check` → clean
- The separate protocol-conversion cache degradation remains out of scope and is tracked by the independent protocol-cache-prefix-stability spec.
