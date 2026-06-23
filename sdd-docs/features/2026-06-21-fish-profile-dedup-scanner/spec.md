# Fish Profile Dedup Scanner Spec

Scope: `internal/bootstrap/adapters.go`, `internal/bootstrap/bootstrap_test.go`
Reference surface: `PersistRoot()`, `fishLineMatches()`, fish comment stripping, shell profile de-duplication
Reference sources: `sdd-docs/features/README.md`, `sdd-docs/features/2026-06-13-auto-update/spec.md`, `sdd-docs/features/2026-06-13-auto-update/spec_ZH.md`
Stack: Go 1.26 stdlib
Last updated: 2026-06-21
Progress: 3 / 3 completed

## Problem Statement

The bootstrap path already writes MCC environment entries into shell profile files so future launches can find the executable root and bundled CA path. Bash, zsh, and unknown-shell handling are stable enough, but the fish-specific matching logic has accumulated a series of conservative approximations:

- explicit export flags are recognized
- local-variable / non-export fish `set` forms are rejected
- quoted values and inline comments are partially handled
- backslash escaping is partially handled

This is good enough for the common cases, but it still leaves a remaining "less dedup than intended" gap for legal fish `set` forms that are semantically equivalent to the entry MCC writes.

The user wants the fish path tightened further, but not turned into a full shell parser.

## Goal

Make fish profile deduplication more semantic and less brittle while preserving safety:

- recognize MCC-equivalent exported fish lines more reliably
- keep rejecting non-export fish `set` forms
- keep inline comments and escapes from corrupting matching
- keep bash / zsh / unknown logic untouched
- do not introduce a complete fish parser

## Non-Goals

- Do not parse the full fish language.
- Do not model variable expansion, command substitution, multiline continuations, or nested shell evaluation.
- Do not change Windows profile behavior.
- Do not change bash / zsh / unknown matching rules.
- Do not alter how `PersistRoot()` chooses candidate profile files.

## Current Behavior

`PersistRoot()` currently:

- builds the target entry using `shellExportEntry()`
- checks each candidate profile for an existing equivalent entry
- appends the entry if no equivalent is found

The fish branch already distinguishes export-like `set` forms from local-variable forms, but it still relies on a hand-rolled state machine plus token reconstruction. That leaves some legal but slightly unusual fish syntax either:

- matched too conservatively, causing duplicate appends, or
- rejected too aggressively, causing a missed dedup

The scope of this feature is to tighten that logic in a small, testable unit.

## Proposed Design

### Recommended Approach: Small fish export scanner

Introduce a dedicated fish scanning helper that only understands the subset of fish syntax needed for profile deduplication:

- `set` command
- explicit export flags only:
  - `-x`
  - `-gx`
  - `--export`
- key token
- value span
- trailing inline comment
- quoting and backslash escaping in the value span

The helper should produce a decision based on structured facts rather than only raw token joins.

### Internal Shape

Keep the public surface small. The implementation can be expressed with helpers such as:

- `stripFishComment(line string) string`
- `splitFishExportLine(line string) (...)`
- `fishLineMatches(line, key, value string) bool`

The important part is the responsibility split:

- `stripFishComment` isolates comment removal
- a scanner/helper isolates fish token boundaries and quoting/escape semantics
- `fishLineMatches` becomes the policy layer that decides whether the parsed line is an equivalent dedup target

### Matching Rules

A fish line is a duplicate only if all of the following are true:

1. It is a `set` command.
2. It contains at least one explicit export flag from the allowlist.
3. The key matches exactly.
4. The value is semantically equivalent to the requested MCC value.
5. If the value is multi-token fish list syntax and not quoted, treat it as non-equivalent unless the scanner can prove it is a single scalar value.
6. A trailing inline comment must not affect matching.
7. `#` inside quotes or escaped as a literal must not be treated as a comment delimiter.

### Value Semantics

The value comparison should distinguish:

- quoted single string values
- unquoted single-token values
- multi-token fish list values

The matching policy should be conservative only when the syntax is ambiguous. The intent is:

- prefer correct dedup for a single scalar value
- prefer false over false-positive for fish list syntax

This preserves safety while improving dedup coverage for the common MCC-generated profile entries.

## Architecture

### 1. Comment Stripping Layer

`stripFishComment` remains the first pass over the raw line.

It should continue to:

- ignore `#` inside single or double quotes
- ignore `#` escaped in unquoted context
- ignore `#` immediately adjacent to a token
- remove trailing inline comments only when the `#` is a real comment delimiter

This layer should stay conservative. If a sequence is ambiguous, leave it in place instead of truncating the value.

### 2. Fish Export Scanner Layer

Add a small scanner that walks the post-comment line and records:

- whether the line starts with `set`
- whether an export flag appears before the key
- which token is the key
- the remaining value span
- whether the value span is quoted
- whether the value span is a single scalar or a fish list

The scanner should not attempt to normalize full fish syntax. It only needs to support the exact forms MCC cares about.

### 3. Dedup Policy Layer

`fishLineMatches` should consume the scanner output and return `true` only when the line is a strong semantic match for the requested `key` / `value`.

This keeps the decision logic centralized and makes it easy to reason about when future fish cases are added.

## Error Handling

Parsing failure must be safe failure:

- if a fish line cannot be confidently parsed, treat it as non-matching
- never treat a malformed or ambiguous line as equivalent
- never delete or rewrite user configuration from matching logic alone

This is important because the code is used only for deduplication, not for active shell execution.

## Testing Plan

Add targeted unit coverage in `internal/bootstrap/bootstrap_test.go` for:

### Existing behavior that must stay green

- `set -x MCC_ROOT /opt/mcc` matches
- `set -gx MCC_ROOT /opt/mcc` matches
- `set --export MCC_ROOT /opt/mcc` matches
- `set -l MCC_ROOT /opt/mcc` does not match
- `set -e MCC_ROOT /opt/mcc` does not match
- `set -u MCC_ROOT /opt/mcc` does not match
- `set MCC_ROOT /opt/mcc` does not match
- bash / zsh / unknown cases continue to behave as before

### Comment and escape handling

- trailing inline comments are ignored
- `#` inside single quotes is preserved
- `#` inside double quotes is preserved
- `\#` in unquoted context is preserved
- escaped quotes inside double quotes do not break matching
- token-adjacent `#` is not treated as a comment delimiter

### Fish scalar vs list semantics

- a quoted multi-word scalar like `"/opt/mcc path"` matches
- an unquoted fish list like `/opt/mcc path` does not get conflated with the quoted scalar
- ambiguous fish list syntax stays non-matching

### End-to-end de-dup

- `PersistRoot()` does not append a second entry when the file already contains a semantically equivalent fish export line
- `PersistRoot()` still appends when only local-variable forms or ambiguous fish syntax exist

## Task Details

### Task 1: Fish Export Scanner Consolidation

#### Requirements

**Objective** - Make fish profile deduplication more semantic and less brittle without turning it into a full shell parser.

**Outcomes** - `fishLineMatches()` remains the single dedup decision point; a small scanner/helper may be introduced to classify fish `set` lines; ambiguous fish input remains non-matching; bash / zsh / unknown behavior remains unchanged.

**Evidence** - Unit tests cover explicit export flags, non-export `set` forms, trailing comments, escape handling, quoted scalars with spaces, unquoted list syntax, and end-to-end `PersistRoot()` dedup behavior.

**Constraints** - Keep the scope limited to `internal/bootstrap/adapters.go` and `internal/bootstrap/bootstrap_test.go`; preserve the current safety bias; do not add a full fish parser; do not change Windows or non-fish logic.

**Edge Cases** - Inline comments after valid exports; escaped `#`; `#` inside single or double quotes; quoted multi-word scalars; unquoted fish lists; malformed or ambiguous fish syntax.

**Verification** - `go test ./...` passes and the fish-specific test matrix demonstrates the intended dedup behavior.

#### Plan

1. Keep `PersistRoot()` candidate selection and non-fish logic unchanged.
2. Refactor fish parsing into a small helper that identifies:
   - `set` command shape
   - explicit export flag presence
   - key token
   - value span
   - quoted vs. unquoted value span
   - scalar vs. list classification
3. Keep `stripFishComment()` as the first pass and make it conservative:
   - ignore `#` inside quotes
   - ignore escaped `#`
   - ignore token-adjacent `#`
   - avoid truncation on ambiguous input
4. Make `fishLineMatches()` consume the helper output and return `true` only for clearly equivalent export forms.
5. Preserve the current fish export allowlist:
   - `-x`
   - `-gx`
   - `--export`
6. Expand tests in `internal/bootstrap/bootstrap_test.go` to cover:
   - export flag allowlist
   - non-export fish `set` forms
   - trailing comments
   - escape handling
   - quoted scalars with spaces
   - unquoted list syntax
   - end-to-end `PersistRoot()` dedup behavior

#### Verification

- `go test ./...` passes.
- Fish exported scalar forms still dedup correctly.
- Fish list syntax still fails closed when ambiguous.
- Bash / zsh / unknown behavior remains unchanged.

## Acceptance Criteria

- `go test ./...` passes
- fish dedup does not regress existing cases
- fish comment and escape handling stay safe and conservative
- bash / zsh / unknown behavior remains unchanged
- the implementation remains small enough to understand without a full shell parser

## Rejected Alternatives

### Continue stacking ad hoc rules

Rejected because the logic is already approaching parser complexity. More one-off branches would make future behavior harder to reason about.

### Full fish parser

Rejected because it is too large for the problem and would expand scope far beyond profile deduplication.

### Loosen matching further

Rejected because the risk profile is wrong: false positives could suppress writing the real environment entry.
