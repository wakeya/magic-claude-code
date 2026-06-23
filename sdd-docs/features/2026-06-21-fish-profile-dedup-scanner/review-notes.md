# Fish Profile Dedup Scanner Review Notes

Date: 2026-06-21  
Reviewers: Codex and Claude Code

## Scope

Reviewed the fish profile dedup scanner changes in `internal/bootstrap/adapters.go` and the related tests in `internal/bootstrap/bootstrap_test.go`.

## Key Findings And Resolutions

1. Fish profile deduplication was refactored into a small scanner with explicit export-flag handling, comment stripping, and conservative list-syntax handling.
   - Resolution: Verified by targeted tests and full repository test runs; the implementation keeps bash / zsh / unknown behavior unchanged.

2. The implementation intentionally remains conservative for ambiguous fish syntax.
   - Resolution: Accepted as a maintenance tradeoff. Ambiguous inputs fail closed rather than being treated as duplicate exports.

## Final Review Conclusion

No logic defects or security defects remain in the reviewed change set. The fish-specific parser remains intentionally conservative, but it behaves safely and passes the full test suite.

## Residual Notes

- The fish scanner is not a full shell parser and still does not model every fish language edge case. This is an intentional scope boundary, not a defect.
