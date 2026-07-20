# Log Exposed Model Label Review Notes

Date: 2026-07-20  
Reviewers: Codex and Claude Code

## Scope

Reviewed branch `feat/log-exposed-model-label` against merge-base `0711f61f174ee666ce58886b6a3fd6d46d7d0d6e`, covering the feature spec and the source-like diff in:

- `internal/admin/failover_handler_test.go`
- `internal/config/config.go`
- `internal/config/failover_test.go`
- `internal/proxy/handler.go`
- `internal/proxy/helpers.go`
- `internal/proxy/helpers_test.go`

Codex Security scan report: `/tmp/codex-security-scans/magic-claude-code/feat_log_exposed_model_label_20260720T000000Z/report.md`.

## Key Findings And Resolutions

1. No blocking functional logic defect found.
   - Resolution: `ModelRoute.ExposedLabel` is additive and display-only; `ResolveRoute` still returns the same provider/backend/default-route semantics, request transformation still uses the backend model, and usage records still store original/mapped model values.

2. No reportable security vulnerability found.
   - Resolution: the label does not flow into routing, request body, upstream URL, headers, authentication, failover selection, or usage persistence. ExposedModel routes still have `DefaultRouted=false`, so the existing failover guard continues to block automatic replay and active-provider mutation for fixed `/model` routes.

3. Low-severity hardening note: `ExposedModel.Label` is operator-controlled and can contain internal control characters.
   - Resolution: this requires authenticated provider-configuration privilege and only affects local log readability, so it is not a reportable unauthenticated security issue for this branch. A future hardening patch can sanitize model log fields to a single line or reject control characters/overlong labels in `Provider.Validate`.

4. Test coverage note: log replacement is unit-tested but not integration-tested at the `ServeHTTP` log sites.
   - Resolution: current tests cover `formatModelLog` behavior and route-field contracts. Suggested follow-up coverage is a log-capture handler test for both `>>>` and `<<<`, plus a direct `Context1M + ExposedLabel` assertion.

## Final Review Conclusion

No blocking functional logic issue or reportable security issue remains in this branch. The implementation matches the spec's display-only intent, and the remaining items are hardening/test-depth notes rather than merge blockers.

## Residual Notes

- Verification run by Codex: `go test ./internal/config ./internal/proxy` passed; `git diff --check 0711f61f174ee666ce58886b6a3fd6d46d7d0d6e...HEAD` passed; `rtk go test ./...` passed with `1684 passed in 17 packages`; `rtk go vet ./...` reported no issues.
