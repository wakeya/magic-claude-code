# Log Exposed Model Label Spec

**Proxy entry:** `internal/proxy/handler.go` (request/response log lines, ~L144-147, L318); `internal/proxy/helpers.go` (new `formatModelLog`)
**Config entry:** `internal/config/config.go` (`ModelRoute`, `ResolveRoute`, ~L271-307); `internal/config/provider.go` (`ExposedModel`, ~L297-331)
**Reference sources:** `sdd-docs/features/README.md` (single-file spec template); `sdd-docs/features/2026-07-17-kimi-quota-usage-parsing-fixes/spec.md` (Task Details example); `internal/failover/manager.go` (confirms ExposedModel fixed-route never enters failover)
**Stack:** Go 1.26 stdlib
**Last updated:** 2026-07-19
**Progress:** 2 / 2

## Overall Analysis (Source Analysis)

### Symptom

When the user picks a model via Claude Code's `/model` menu, MCC routes the session to a fixed provider through an `ExposedModel`. Each `ExposedModel` carries a random ID such as `em-fceb31a8` (from `generateExposedModelID`, `provider.go:329-331`). Claude Code sends this ID back as the request `model` field, and MCC logs it verbatim:

```
2026/07/19 20:31:58 [a9e7c8a5] >>> POST api.anthropic.com/v1/messages model=em-fceb31a8 -> k3 stream=true ...
```

The left side `em-fceb31a8` is opaque to the operator. The human-readable name the user actually configured — `ExposedModel.Label` — is already stored but never surfaces in logs.

### Design Intent of the `em-` ID

Per `provider.go:326-328`, the ID is *intentionally* a random hex with no semantics:

> ID 纯内部用（Claude Code /model 菜单 value + mcc 路由键），用户无需感知，故用 em- 前缀 + 随机 hex，无语义、稳定。

It must be globally unique and stable across provider reordering, so it cannot reuse the user-editable `Label`. Logging it raw leaks an internal key the design explicitly says users should never perceive.

### Why the Label Is Not Already Available

`ResolveRoute` (`config.go:284-307`) resolves a request `model` into `ModelRoute{Provider, BackendModel, DefaultRouted}`. On an `ExposedModel` hit it returns the provider and `em.BackendModel`, but **drops `em.Label`**. The log site (`handler.go:144`) only has `metadata.OriginalModel` (the raw `em-` ID).

### Impact Surface

`grep` confirms `ModelRoute` / `ResolveRoute` is consumed only in `internal/proxy/handler.go` (L114, L116, L401, L421). Adding a field is safe — existing reads (`Provider`, `BackendModel`, `DefaultRouted`) are unaffected; the new field is purely additive.

### Failover Path Is Unaffected

`ExposedModel` hits are fixed routes (`DefaultRouted=false`); `shouldFailover` / `shouldFailoverOnError` (`handler.go:401-421`) short-circuit on `!route.DefaultRouted`. So the failover replay log (`handler.go:468`, `model=%s->%s`) never sees an `em-` ID and needs no change.

### Decision (Display Format)

**Pure Label (Option A).** When a request hits an `ExposedModel`, the log's left side shows `em.Label` instead of the raw `em-` ID.

| Option | Example (Label="Kimi K3") | Chosen? |
| --- | --- | --- |
| A. pure Label | `model=Kimi K3 -> k3` | ✅ |
| B. Label(em-id) | `model=Kimi K3(em-fceb31a8) -> k3` | — |
| C. em-id(Label) | `model=em-fceb31a8(Kimi K3) -> k3` | — |

Rationale: the `em-` ID has near-zero diagnostic value — correlation is better done via `provider_name` + `upstream_url`, both already logged. Option A is the most legible. The user confirmed this choice on 2026-07-19.

### What Does NOT Change

- **Request body / routing match:** `metadata.OriginalModel` and `ResolveRoute` matching still use the raw `em-` ID. Only the **log display layer** substitutes the Label.
- **Right side of `->`:** `mappedModel` (the `BackendModel`, e.g. `k3`) is already a human-readable backend model name; no further mapping.
- **Default-route logs:** requests that fall back to the active provider (e.g. `model=claude-opus-4-8 -> glm-5.2`) have no `ExposedModel` and no Label; they stay as-is.
- **Failover replay log** (`handler.go:468`): unchanged (ExposedModel fixed routes never enter failover).
- **`[1m]` suffix:** `Context1M` menu values carry `[1m]` for Claude Code's context-window detection, but the request `model` reaching MCC has it stripped. `ResolveRoute` matches on the pure ID; the returned Label never contained `[1m]`. No special handling needed.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | ✅ | `ResolveRoute` surfaces `ExposedModel.Label` via a new `ModelRoute.ExposedLabel` field | `internal/config/config.go`; `internal/config/failover_test.go` | `go test ./internal/config/` — extended route tests assert Label |
| 2 | ✅ | Proxy request/response log shows Label instead of `em-` ID (extract `formatModelLog` for testability) | `internal/proxy/helpers.go`; `internal/proxy/handler.go`; `internal/proxy/helpers_test.go` | `go test ./internal/proxy/` and `go test ./...` green |

## Requirements

### Deliverables

1. `config.ModelRoute` gains a field `ExposedLabel string`. It is non-empty **iff** `ResolveRoute` matched an `ExposedModel`; empty for the active-provider fallback and the no-active case.
2. `Config.ResolveRoute` sets `ExposedLabel: em.Label` on the ExposedModel-hit return path (`config.go:297`). The two fallback returns (active provider, no active) leave it zero.
3. A new helper `formatModelLog(originalModel, mappedModel, exposedLabel string) string` in `internal/proxy/helpers.go` encapsulates the log-model construction: prefer `exposedLabel` when non-empty, else `originalModel`; collapse to a single token when the display model equals `mappedModel` (preserving today's unmapped behavior); otherwise emit `"<display> -> <mappedModel>"`.
4. `handler.go` (~L144-147) replaces its inline `modelStr` construction with `formatModelLog(metadata.OriginalModel, mappedModel, route.ExposedLabel)`. Entry log (~L159) and exit log (~L318) both consume the same `modelStr`, so both are covered by one change.
5. No change to the request body, routing match, `usage.RequestRecord`, failover logic, or the failover replay log line (`handler.go:468`).
6. `Provider.Validate` already enforces `Label` non-empty (`provider.go:218`), so the guard `exposedLabel != ""` inside `formatModelLog` is sufficient; no additional validation needed.

### Data Model

```go
// internal/config/config.go
type ModelRoute struct {
    Provider      *Provider
    BackendModel  string
    DefaultRouted bool
    ExposedLabel  string  // NEW: non-empty iff an ExposedModel was matched; for log display only
}
```

## Task Details

### Task 1: ResolveRoute surfaces ExposedModel.Label

#### Requirements

**Objective** — Carry `ExposedModel.Label` out of `ResolveRoute` so the proxy log layer can display the human-readable name the user configured, instead of the opaque `em-<hex>` ID, without altering routing or the request body.

**Outcomes** — `internal/config/config.go` adds field `ExposedLabel string` to `ModelRoute` (~L271-275) and sets `ExposedLabel: em.Label` on the ExposedModel-hit return inside `ResolveRoute` (~L297); the active-fallback and no-active returns keep the zero value. `internal/config/failover_test.go` extends its four existing `ResolveRoute` tests with `ExposedLabel` assertions.

**Evidence** — `go test ./internal/config/` passes with the extended assertions; a hit on `ExposedModel{ID:"em-deadbeef", Label:"Kimi K3", BackendModel:"k3"}` yields `r.ExposedLabel == "Kimi K3"`, while the active-fallback, disabled-exposed-fallback, and no-active cases all yield `r.ExposedLabel == ""`.

**Constraints** — `ModelRoute` is consumed only in `internal/proxy/handler.go` (verified by grep: L114, L116, L401, L421); the new field is additive and does not affect existing reads. Routing match still uses the raw `em-` ID; only the returned struct gains a display-only field. `ResolveModel` (the backward-compat wrapper) is unchanged because it forwards `r.Provider` and `r.BackendModel` only.

**Edge Cases** — Active-provider fallback (`DefaultRouted=true`) → `ExposedLabel == ""`; disabled provider's exposed model skipped → fallback to active → `ExposedLabel == ""`; no active provider (`Provider == nil`) → `ExposedLabel == ""`; `Context1M` model (request `model` with `[1m]` stripped) matches the pure ID and returns the Label (which never contains `[1m]`).

**Verification** — `go test ./internal/config/` green; the four extended tests assert both the positive (hit returns Label) and negative (all three fallback paths return empty) contracts.

#### Plan

1. **Failing test first.** In `internal/config/failover_test.go`, extend the four existing tests with `ExposedLabel` assertions (do not change routing setup):
   - `TestResolveRouteExposedModelIsNotDefaultRouted` (uses `ExposedModel{ID:"glm-4.6", Label:"GLM-4.6", ...}`) — add: `if r.ExposedLabel != "GLM-4.6" { t.Fatalf("expected exposed label GLM-4.6, got %q", r.ExposedLabel) }`.
   - `TestResolveRouteActiveFallbackIsDefaultRouted` — add: `if r.ExposedLabel != "" { t.Fatalf("active fallback must have empty ExposedLabel, got %q", r.ExposedLabel) }`.
   - `TestResolveRouteSkipsDisabledExposedModel` — add: `if r.ExposedLabel != "" { t.Fatalf("disabled-exposed fallback must have empty ExposedLabel, got %q", r.ExposedLabel) }`.
   - `TestResolveRouteWithoutActiveProvider` — add: `if r.ExposedLabel != "" { t.Fatalf("no-active route must have empty ExposedLabel, got %q", r.ExposedLabel) }`.
2. **Confirm failure.** `go test ./internal/config/ -run TestResolveRoute` → compile error (`r.ExposedLabel undefined`).
3. **Minimal implementation.** In `internal/config/config.go`:
   - Add field to `ModelRoute` (~L274): `ExposedLabel string` with the doc comment `// 命中 ExposedModel 时为 em.Label，其余为空；仅供日志展示层使用，不参与路由。`.
   - In `ResolveRoute` (~L297), change the hit return to: `return ModelRoute{Provider: p, BackendModel: em.BackendModel, DefaultRouted: false, ExposedLabel: em.Label}`. Leave the two fallback returns unchanged (zero value).
4. **Confirm pass.** `go test ./internal/config/ -run TestResolveRoute` → all four pass.
5. **Regression.** `go test ./internal/config/` and `go vet ./internal/config/`.
6. **Commit.** `git add internal/config/config.go internal/config/failover_test.go && git commit -m "feat(config): ResolveRoute surfaces ExposedModel.Label for log display"`.

#### Verification

- [x] `go test ./internal/config/ -run TestResolveRoute` — 4/4 pass.
- [x] `go test ./internal/config/` — 108 passed; `go vet ./internal/config/` clean.
- [x] Grep confirms `ModelRoute` still consumed only in `internal/proxy/handler.go`; no other reader broke.

### Task 2: Proxy request/response log shows Label instead of em- ID

#### Requirements

**Objective** — Make the `>>>` entry log and `<<<` exit log display the `ExposedModel.Label` on the left side of `model=` when the request hit an exposed model, replacing the opaque `em-<hex>` ID; preserve the `-> <BackendModel>` mapping structure and today's single-token collapse for unmapped models.

**Outcomes** — `internal/proxy/helpers.go` gains `formatModelLog(originalModel, mappedModel, exposedLabel string) string` (imports `fmt`); `internal/proxy/handler.go` (~L144-147) replaces its inline `modelStr` block with a single call `modelStr := formatModelLog(metadata.OriginalModel, mappedModel, route.ExposedLabel)`; `internal/proxy/helpers_test.go` (new file) adds a table-driven `TestFormatModelLog` covering label substitution, label-equals-backend collapse, default route, unmapped collapse, and empty-label fallback.

**Evidence** — `go test ./internal/proxy/` passes; `TestFormatModelLog` asserts `formatModelLog("em-fceb31a8", "k3", "Kimi K3") == "Kimi K3 -> k3"` and `formatModelLog("em-deadbeef", "k3", "k3") == "k3"` (collapse). Full suite `go test ./...` green.

**Constraints** — Only the log display layer changes; `metadata.OriginalModel`, the request body, routing, `usage.RequestRecord`, and the failover replay log (`handler.go:468`) are untouched. The entry (~L159) and exit (~L318) logs already share `modelStr`, so one edit covers both. `formatModelLog` is pure (no I/O) to keep it unit-testable.

**Edge Cases** — `exposedLabel == ""` → fall back to `originalModel` (default route, e.g. `claude-opus-4-8 -> glm-5.2`); `exposedLabel == mappedModel` → collapse to single token (e.g. Label `k3`, backend `k3` → `model=k3`); `originalModel == mappedModel` with empty label → collapse (unmapped default, e.g. `model=claude-opus-4-8`); `Context1M` request — `route.ExposedLabel` carries the plain Label (no `[1m]`), so the log shows the clean Label.

**Verification** — `go test ./internal/proxy/` green (new `TestFormatModelLog`); `go test ./...` full suite green; `go vet ./...` clean.

#### Plan

1. **Failing test first.** Create `internal/proxy/helpers_test.go` with a table-driven `TestFormatModelLog`:
   ```go
   package proxy

   import "testing"

   func TestFormatModelLog(t *testing.T) {
       tests := []struct {
           name          string
           originalModel string
           mappedModel   string
           exposedLabel  string
           want          string
       }{
           {"exposed label replaces em id", "em-fceb31a8", "k3", "Kimi K3", "Kimi K3 -> k3"},
           {"label equals backend collapses to single token", "em-deadbeef", "k3", "k3", "k3"},
           {"default route keeps original with mapping", "claude-opus-4-8", "glm-5.2", "", "claude-opus-4-8 -> glm-5.2"},
           {"unmapped default collapses", "claude-opus-4-8", "claude-opus-4-8", "", "claude-opus-4-8"},
           {"empty label falls back to original", "em-x", "k3", "", "em-x -> k3"},
       }
       for _, tt := range tests {
           t.Run(tt.name, func(t *testing.T) {
               if got := formatModelLog(tt.originalModel, tt.mappedModel, tt.exposedLabel); got != tt.want {
                   t.Fatalf("formatModelLog(%q, %q, %q) = %q, want %q",
                       tt.originalModel, tt.mappedModel, tt.exposedLabel, got, tt.want)
               }
           })
       }
   }
   ```
2. **Confirm failure.** `go test ./internal/proxy/ -run TestFormatModelLog` → compile error (`formatModelLog undefined`).
3. **Minimal implementation.** In `internal/proxy/helpers.go`, add `"fmt"` to the import block and append:
   ```go
   // formatModelLog 构造请求/响应日志里的 model 字段。
   // 命中 ExposedModel 时优先用人类可读的 Label 替代原始 em-<hex> ID（仅影响日志展示，
   // 不改路由/请求体）；mappedModel 与展示模型相同时折叠为单 token，
   // 与未做模型映射时的行为一致。
   func formatModelLog(originalModel, mappedModel, exposedLabel string) string {
       display := originalModel
       if exposedLabel != "" {
           display = exposedLabel
       }
       if mappedModel == display {
           return display
       }
       return fmt.Sprintf("%s -> %s", display, mappedModel)
   }
   ```
4. **Confirm pass.** `go test ./internal/proxy/ -run TestFormatModelLog` → 5/5 sub-tests pass.
5. **Wire into handler.** In `internal/proxy/handler.go`, replace the inline block (~L144-147):
   ```go
   // before
   modelStr := metadata.OriginalModel
   if mappedModel != metadata.OriginalModel {
       modelStr = fmt.Sprintf("%s -> %s", metadata.OriginalModel, mappedModel)
   }
   ```
   with:
   ```go
   modelStr := formatModelLog(metadata.OriginalModel, mappedModel, route.ExposedLabel)
   ```
   (Entry log ~L159 and exit log ~L318 already consume `modelStr`, so both update automatically. `fmt` may become unused in `handler.go` — if so, remove it from the import block; the build will flag it.)
6. **Regression.** `go test ./internal/proxy/`, then `go test ./...` and `go vet ./...`.
7. **Commit.** `git add internal/proxy/helpers.go internal/proxy/helpers_test.go internal/proxy/handler.go && git commit -m "feat(proxy): log shows ExposedModel.Label instead of em- id"`.

#### Verification

- [x] `go test ./internal/proxy/ -run TestFormatModelLog` — 5/5 sub-tests pass.
- [x] `go test ./...` — 1684 passed (0 failed); `go vet ./...` clean.
- [x] Manual (2026-07-19): ExposedModel Label=`kimi-k3`, BackendModel=`k3`; both `>>>` entry and `<<<` exit logs show `model=kimi-k3 -> k3` (em- ID replaced by Label).
