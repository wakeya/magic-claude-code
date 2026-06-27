# Provider Quota Query Review Notes

Date: 2026-06-27  
Reviewers: Codex and Claude Code

## Scope

Reviewed commit `33af2ed` against `058a766`, using `spec.md`, `spec_ZH.md`, and the local `cc-switch` implementation as the acceptance baseline. The review covered 28 changed source files, full Go/frontend verification, race, vet, build, and focused security reproductions.

## Conclusion

**Rejected; changes are required before another review.** Configuration persistence and partial UI rendering exist, but the production service never wires the quota Manager, so real queries and scheduling do not work. Several native adapters are tested against invented response shapes that contradict the referenced implementation. The claimed completed acceptance table is not supported.

## Key Findings And Required Resolutions

1. **[Blocker] No production Manager wiring.** `cmd/server/main.go` never creates a `SnapshotStore`/`Manager`, injects it with `SetQuotaManager`, starts it, or stops it. Test query and refresh therefore return 500, batch snapshots stay empty, and scheduling never runs. Add production lifecycle wiring and an end-to-end startup test.

2. **[Blocker] Native adapter contracts are incorrect.** Compared with local `cc-switch`, Kimi models `usage` as an object rather than an array; Zhipu uses `data.limits[]`; MiniMax uses root `model_remains[]`; ZenMux uses `used_value_usd`, `max_value_usd`, and `resets_at`; Volcengine requires POST header-based V4 signing with service `ark` and AFP/QuotaUsage response parsing; Novita returns top-level `availableBalance`. Replace synthetic fixtures with reference-derived and real-response fixtures that exercise actual parsers/adapters.

3. **[High] Script network and redaction controls are broken.** Initial request URLs are never checked against the effective Base URL. An overlay test reproduced cross-origin delivery of `Bearer review-secret`. `sanitizeError` only truncates, and a second reproduction returned `review-secret` in a connection error URL. These paths are currently dormant only because Manager wiring is absent; fix them before wiring production execution.

4. **[High] Config validation and secret semantics are incomplete.** Base URL/origin rules and template-specific required fields are not enforced; the full AccessKey ID is returned instead of a mask; and a blank quota API key persists a copy of the provider APIToken, breaking fallback/update semantics.

5. **[High] Token Plan cannot be fully configured in the UI.** There is no `coding_plan_provider` field, ZenMux URL/key inputs are hidden, first-time Volcengine AK/SK inputs cannot be reached, MiMo detection is hardcoded false, and a saved interval of zero is changed to five through `value || 5`. Test/query API wrappers also ignore non-2xx status.

6. **[High] Query semantics and lifecycle are incomplete.** NewAPI `success=false` becomes a successful balance item; malformed percentages are silently clamped; draft tests and production queries deduplicate under the same provider-only key; startup jitter is unused; overwrite imports and fallback-token changes do not invalidate snapshots.

7. **[Medium] Provider-card behavior misses accepted details.** It omits the recent-query timestamp, uses the wrong title-row order, loses both failure and refresh UI after an initial failure, allows refresh on disabled providers, has no responsive wrap strategy, and formats CNY with `$`.

8. **[High] Test evidence is insufficient.** New frontend quota tests are source-regex assertions rather than mounted behavior tests. There is no `ProviderUsageView` test or production wiring test. Adapter tests encode incorrect synthetic contracts. The specs remain `draft`, `0 / 10 planned`, with no viewport, restart, or manual mock evidence.

## Verification

- `go test ./...`: passed.
- `go test -race ./internal/providerquota ./internal/admin ./internal/config`: passed.
- `go vet ./...`: passed.
- Frontend tests: 99/99 passed, but new quota coverage is source-regex based.
- Frontend build: passed.
- `git diff --check`: passed.
- Two temporary negative security tests failed as intended, reproducing cross-origin credential delivery and secret-bearing errors.

## Final Review Conclusion

The commit is not releasable. Production Manager wiring, real-protocol native adapters, script security controls, complete template configuration, and behavior-level tests are required before re-review.

## Residual Notes

- A native textarea is explicitly allowed by the phase-one spec and is not a defect.
- `goja` maintenance is a residual dependency concern; memory/complexity denial-of-service behavior should be assessed before enabling production execution.
- Security scan report: `/tmp/codex-security-scans/magic-claude-code/33af2edd9fc3_20260627T121034Z/report.md`.
