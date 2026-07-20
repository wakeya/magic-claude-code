# 功能文档结构

本目录按功能（而非文档类型）组织文档。新的功能文档优先使用单文件规格格式：英文 `spec.md` 与中文 `spec_ZH.md` 成对维护。

历史功能目录中可能存在小写 `spec_zh.md`；该命名保持兼容，新的功能规格统一使用 `spec_ZH.md`。

旧功能目录中仍可能存在 `requirements.md`、`plan.md`、`validation.md`、`decisions.md`、`status.md` 等拆分文档；这些文档保持历史兼容，后续修改时可逐步合并为新的 `spec.md` 格式。

## 目录布局

```text
sdd-docs/features/<yyyy-mm-dd-feature-name>/
  spec.md
  spec_ZH.md
```

历史拆分格式：

```text
sdd-docs/features/<yyyy-mm-dd-feature-name>/
  requirements.md
  plan.md
  validation.md
  decisions.md
  status.md
```

## 文件职责

| 文件 | 用途 |
|------|------|
| `spec.md` | 英文功能规格，集中记录整体分析、开发检查清单、需求和任务详情。 |
| `spec_ZH.md` | 中文功能规格，与 `spec.md` 保持语义一致，用于中文协作和审阅。 |
| `spec_zh.md` | 历史小写中文规格命名，保持兼容；新功能不再使用。 |
| `requirements.md` | 定义范围、目标、约束、边界条件和验收意图。是功能"做什么"的唯一事实来源。 |
| `plan.md` | 定义具体实现步骤、涉及的文件、需编写的测试、执行的命令和提交检查点。 |
| `validation.md` | 定义验证清单，并在实现后记录实际验证证据。 |
| `decisions.md` | 记录带日期的决策，包括上下文、选择、影响和重新评估触发条件。 |
| `status.md` | 跟踪生命周期状态、负责人、时间戳、阻塞项和下一步转换。 |

## 新规格模板

新的 `spec.md` / `spec_ZH.md` 必须包含以下四个一级章节：

```markdown
# Feature Name Spec

Local page / proxy entry / reference sources / stack / last updated / progress

## Overall Analysis (Source Analysis)
## Development Checklist
## Requirements
## Task Details
```

中文文档使用同等结构：

```markdown
# 功能名称规格

本地页面 / 代理入口 / 参考源站 / 技术栈 / 最后更新 / 进度

## 整体分析（源站分析）
## 开发检查清单
## 需求
## 任务详情
```

`Task Details` / `任务详情` 下每个任务应包含：

```markdown
### Task N: Task Name

#### Requirements

**Objective** - ...
**Outcomes** - ...
**Evidence** - ...
**Constraints** - ...
**Edge Cases** - ...
**Verification** - ...

#### Plan

#### Verification
```

Simple examples for each task are provided below; in practice, a more granular approach can be used to ensure the tasks can be implemented straightforwardly without requiring complex decision-making during execution.

```markdown
### Task 1: Kimi quota response tolerant parsing

#### Requirements

**Objective** — Make `queryKimi` parse the real `GET https://api.kimi.com/coding/v1/usages` response (RFC3339 `resetTime`, numeric-string counters, `window`-described limits, no `name`), eliminating the permanent `invalid_json` failure, with tolerance aligned to kimi-code's `managed-usage.ts`.

**Outcomes** — `token_plan.go` gains `kimiUsageDetail` (line 133), `usedOrDerived` (141), `kimiWindowLabel` (262), and a `json.RawMessage`-based `parseKimiResetTime` (286); `queryKimi` (152) returns correct five-hour and weekly tiers from the live payload; tests use live-shaped fixtures plus a legacy-shape backward-tolerance case.

**Evidence** — `go test ./internal/providerquota/` passes; an end-to-end run of `TokenPlanAdapter.Query("kimi", ..., "https://api.kimi.com/coding/", <real token>)` succeeds and reports tiers `five_hour` (utilization 39, resets 2026-07-17T16:20:25Z, label "5h limit") and `seven_day` (utilization 36, resets 2026-07-24T01:20:25Z, remaining 64).

**Constraints** — Endpoint/headers/interface unchanged; unknown response fields ignored; label fallback order fixed as `name → title → scope → window`; `strconv` added to imports.

**Edge Cases** — `resetTime` variants (RFC3339Nano, unix seconds, unix millis, numeric string, null, garbage); `used` explicit-zero vs derived; `remaining > limit` clamp; `TIME_UNIT_*` enum units; missing `detail` fields skipped when `limit <= 0`.

**Verification** — Package tests green; live query succeeds; legacy-shape fixture still parses (backward tolerance).

#### Plan

1. In `internal/providerquota/token_plan.go`, replace the anonymous response struct and helpers (old lines 131–233) with:
   - `kimiUsageDetail{ Limit, Used, Remaining json.Number; ResetTime json.RawMessage }` shared by `limits[].detail` and `usage`.
   - `usedOrDerived(limit, remaining float64) float64` — explicit `used` wins when present (including a real `0`), else `max(limit-remaining, 0)`.
   - `limits[]` item gains `Title`, `Scope`, and `Window{ Duration json.Number; TimeUnit string }`; label = first non-empty of `Name/Title/Scope/kimiWindowLabel(Window)`.
   - `kimiWindowLabel(duration, timeUnit)` — minute multiples of 60 → `"<h>h limit"`, other minutes → `"<m>m limit"`, hours → `"<h>h limit"`, seconds → `"<s>s limit"`, else `""`; unit matched case-insensitively by substring (`MINUTE`/`HOUR`/`SECOND`) to accept `TIME_UNIT_MINUTE`.
   - `parseKimiResetTime(raw json.RawMessage) time.Time` — trim; `null`/empty → zero; `strconv.Unquote` quoted values then try `time.Parse(time.RFC3339Nano, …)`; finally `strconv.ParseFloat` → unix seconds, or `time.UnixMilli` when > 1e12.
   - `kimiUtilization(used, limit)` — percentage clamped to [0, 100] so over-quota tiers pass `NormalizeTier`; `Used`/`Remaining` display values stay as reported.
   - Weekly tier sets `Remaining` (previously only `Used`/`Total`).
2. Rewrite `TestParseKimiResponse` and `TestKimiIntegration` fixtures to the live shape (string counters, RFC3339Nano `resetTime`, `window` without `name`); add `TestParseKimiResetTime` (8 cases), `TestKimiUsedOrDerived` (3 cases), `TestKimiWindowLabel` (6 cases), `TestKimiUtilization` (4 cases incl. over-quota clamp), `TestKimiIntegrationOverQuota` (used=120/limit=100 → success, utilization 100, used reported as 120), and `TestKimiIntegrationLegacyShape` (numeric fields, unix `resetTime`, `name` label).
3. Run `go test ./internal/providerquota/` and `go test ./...`.
4. End-to-end probe (temporary `main.go` under `.tmp-kimi-check/`, deleted afterwards): `providerquota.NewTokenPlanAdapter(10s).Query(ctx, "kimi", nil, "https://api.kimi.com/coding/", token)` with the real kimi-k3 token; confirm `success:true` and both tiers.

#### Verification

- [x] `go test ./internal/providerquota/` — ok (2.749s); `go test ./...` — all 15 packages ok, `go vet` clean.
- [x] Live end-to-end probe (2026-07-17): `success:true`, tier `five_hour` label "5h limit", used 39 / total 100 / remaining 61, resets 2026-07-17T16:20:25Z; tier `seven_day` used 36 / total 100 / remaining 64, resets 2026-07-24T01:20:25Z.
- [x] `TestKimiIntegrationLegacyShape` proves the pre-change fixture shape (JSON numbers, unix `resetTime`, `name`) still parses — no regression for older API versions.
```


中文任务详情使用同等结构：

```markdown
### 任务 N：任务名称

#### 需求

**Objective（目标）** — ...
**Outcomes（成果）** — ...
**Evidence（证据）** — ...
**Constraints（约束）** — ...
**Edge Cases（边界）** — ...
**Verification（验证）** — ...

#### 计划

#### 验证
```

任务详情下每个任务的简单举例如下，实际可使用更细致颗粒度的方式保证实现时可无脑直接实现：

```markdown
### 任务 1：Kimi 配额响应宽松解析

#### 需求

**Objective（目标）** — 让 `queryKimi` 能解析真实的 `GET https://api.kimi.com/coding/v1/usages` 响应（RFC3339 `resetTime`、数字字符串计数器、`window` 描述的 limits、无 `name`），消除永久性的 `invalid_json` 失败，容忍度对齐 kimi-code 的 `managed-usage.ts`。

**Outcomes（成果）** — `token_plan.go` 新增 `kimiUsageDetail`（第 133 行）、`usedOrDerived`（141）、`kimiWindowLabel`（262）和基于 `json.RawMessage` 的 `parseKimiResetTime`（286）；`queryKimi`（152）能从线上 payload 产出正确的 5 小时与周限额 tier；测试改用线上形态假数据，并保留旧形态的向后兼容用例。

**Evidence（证据）** — `go test ./internal/providerquota/` 通过；端到端运行 `TokenPlanAdapter.Query("kimi", ..., "https://api.kimi.com/coding/", <真实 token>)` 成功，返回 tier `five_hour`（利用率 39，重置 2026-07-17T16:20:25Z，标签 "5h limit"）与 `seven_day`（利用率 36，重置 2026-07-24T01:20:25Z，剩余 64）。

**Constraints（约束）** — 端点/请求头/接口不变；忽略未知响应字段；标签回退顺序固定为 `name → title → scope → window`；新增 `strconv` 导入。

**Edge Cases（边界）** — `resetTime` 各变体（RFC3339Nano、unix 秒、unix 毫秒、数字字符串、null、乱码）；`used` 显式零与推导；`remaining > limit` 钳位；`TIME_UNIT_*` 枚举单位；`detail` 字段缺失且 `limit <= 0` 时跳过。

**Verification（验证）** — 包测试全绿；真实查询成功；旧形态假数据仍可解析（向后兼容）。

#### 计划

1. 在 `internal/providerquota/token_plan.go` 中替换原匿名响应结构体与辅助函数（旧 131–233 行）：
   - `kimiUsageDetail{ Limit, Used, Remaining json.Number; ResetTime json.RawMessage }`，由 `limits[].detail` 与 `usage` 共用。
   - `usedOrDerived(limit, remaining float64) float64`——显式 `used`（含真实的 `0`）优先，否则 `max(limit-remaining, 0)`。
   - `limits[]` 项新增 `Title`、`Scope` 与 `Window{ Duration json.Number; TimeUnit string }`；标签取 `Name/Title/Scope/kimiWindowLabel(Window)` 中第一个非空值。
   - `kimiWindowLabel(duration, timeUnit)`——分钟数能被 60 整除 → `"<h>h limit"`，其余分钟 → `"<m>m limit"`，小时 → `"<h>h limit"`，秒 → `"<s>s limit"`，否则 `""`；单位按大小写不敏感子串（`MINUTE`/`HOUR`/`SECOND`）匹配，兼容 `TIME_UNIT_MINUTE`。
   - `parseKimiResetTime(raw json.RawMessage) time.Time`——trim；`null`/空 → 零值；带引号值先 `strconv.Unquote` 再试 `time.Parse(time.RFC3339Nano, …)`；最后 `strconv.ParseFloat` → unix 秒，> 1e12 时 `time.UnixMilli`。
   - `kimiUtilization(used, limit)`——百分比钳到 [0, 100]，使超限 tier 通过 `NormalizeTier` 校验；`Used`/`Remaining` 展示值保留 API 原样。
   - 周限额 tier 设置 `Remaining`（此前只有 `Used`/`Total`）。
2. 重写 `TestParseKimiResponse` 与 `TestKimiIntegration` 的假数据为线上形态（字符串计数器、RFC3339Nano `resetTime`、无 `name` 的 `window`）；新增 `TestParseKimiResetTime`（8 例）、`TestKimiUsedOrDerived`（3 例）、`TestKimiWindowLabel`（6 例）、`TestKimiUtilization`（4 例，含超限钳位）、`TestKimiIntegrationOverQuota`（used=120/limit=100 → 查询成功、utilization 100、used 展示 120）与 `TestKimiIntegrationLegacyShape`（数字字段、unix `resetTime`、`name` 标签）。
3. 运行 `go test ./internal/providerquota/` 与 `go test ./...`。
4. 端到端探针（临时 `main.go` 置于 `.tmp-kimi-check/`，用后删除）：用真实 kimi-k3 token 执行 `providerquota.NewTokenPlanAdapter(10s).Query(ctx, "kimi", nil, "https://api.kimi.com/coding/", token)`，确认 `success:true` 且两个 tier 正确。

#### 验证

- [x] `go test ./internal/providerquota/` — ok（2.749s）；`go test ./...` — 15 个包全部 ok，`go vet` 干净。
- [x] 端到端真实探针（2026-07-17）：`success:true`，tier `five_hour` 标签 "5h limit"，used 39 / total 100 / remaining 61，重置 2026-07-17T16:20:25Z；tier `seven_day` used 36 / total 100 / remaining 64，重置 2026-07-24T01:20:25Z。
- [x] `TestKimiIntegrationLegacyShape` 证明旧假数据形态（JSON 数字、unix `resetTime`、`name`）仍可解析——对旧版 API 无回归。
```


### 单文件计划规则

新功能的实现计划必须直接维护在对应 `spec.md` / `spec_ZH.md` 的 `Task Details` / `任务详情` 中，不再为新功能创建独立的 `plan.md` 或跨目录计划文件。这样需求、设计、执行步骤和验证证据始终通过同一个 feature 目录关联。

每个任务的 `Plan` / `计划` 必须包含：

1. 精确的修改、创建和测试文件路径。
2. 按 TDD 顺序拆分的可追踪复选步骤：失败测试、失败确认、最小实现、通过确认、回归验证和提交。
3. 代码修改步骤所需的具体代码或接口形态，不使用 `TBD`、`TODO` 或“稍后实现”等占位描述。
4. 精确的验证命令和预期结果。
5. 实现完成后回写同一规格中的进度、检查清单和实际验证证据。

英文与中文规格中的实现计划必须保持语义一致。历史目录中的独立 `plan.md` 和 `sdd-docs/superpowers/plans/` 文件继续保留，但不作为新功能规格的默认格式。

## 审查归档

部分功能会在 `spec.md` / `spec_ZH.md` 之外附带审查归档文件：

- `review-notes.md`
- `review-notes_ZH.md`

这类文件用于记录最终审查结论、验证结果和少量残余维护性说明。它们应与对应 feature 目录并排维护，语义上属于该功能的附加归档，而不是独立功能规格。

## 当前新格式规格

| 功能 | 英文 | 中文 |
|------|------|------|
| Windows Usage Statistics Reliability Fixes | `2026-06-12-windows-usage-statistics-fixes/spec.md` | `2026-06-12-windows-usage-statistics-fixes/spec_zh.md` |
| OpenAI-Compatible API Format Support | `2026-06-12-openai-compatible-api-format/spec.md` | `2026-06-12-openai-compatible-api-format/spec_ZH.md` |
| Auto-Update | `2026-06-13-auto-update/spec.md` | `2026-06-13-auto-update/spec_ZH.md` |
| Version Display Fix | `2026-06-14-version-display-fix/spec.md` | `2026-06-14-version-display-fix/spec_ZH.md` |
| Proactive Content Block Cleanup | `2026-06-15-proactive-content-block-cleanup/spec.md` | `2026-06-15-proactive-content-block-cleanup/spec_ZH.md` |
| Startup Message i18n | `2026-06-15-improve-startup-message-i18n/spec.md` | `2026-06-15-improve-startup-message-i18n/spec_ZH.md` |
| Provider Rate-Limit Queue | `2026-06-16-provider-rate-limit-queue/spec.md` | `2026-06-16-provider-rate-limit-queue/spec_ZH.md` |
| Desktop Endpoint Completeness and TLS Hardening | `2026-06-20-desktop-endpoint-patch/spec.md` | `2026-06-20-desktop-endpoint-patch/spec_ZH.md` |
| Brand Logo Replacement | `2026-06-20-brand-logo/spec.md` | `2026-06-20-brand-logo/spec_ZH.md` |
| Transparent Mode Bootstrap and Fallback | `2026-06-20-transparent-mode-bootstrap-and-fallback/spec.md` | `2026-06-20-transparent-mode-bootstrap-and-fallback/spec_ZH.md` |
| Fish Profile Dedup Scanner | `2026-06-21-fish-profile-dedup-scanner/spec.md` | `2026-06-21-fish-profile-dedup-scanner/spec_ZH.md` |
| Provider Quota Query | `2026-06-27-provider-quota-query/spec.md` | `2026-06-27-provider-quota-query/spec_ZH.md` |
| SSE-Labeled HTTP Error Handling | `2026-06-30-non-2xx-sse-error-handling/spec.md` | `2026-06-30-non-2xx-sse-error-handling/spec_ZH.md` |
| Zhipu Web Tool Compatibility Recovery | `2026-07-01-zhipu-web-tools-compat/spec.md` | `2026-07-01-zhipu-web-tools-compat/spec_ZH.md` |
| Node.js Client CA Trust Auto-Setup | `2026-07-04-node-extra-ca-certs-auto-setup/spec.md` | `2026-07-04-node-extra-ca-certs-auto-setup/spec_ZH.md` |
| Cross-Provider Model Routing | `2026-07-08-cross-provider-model-routing/spec.md` | `2026-07-08-cross-provider-model-routing/spec_ZH.md` |
| NPM Audit Fix | `2026-07-10-npm-audit-fix/spec.md` | `2026-07-10-npm-audit-fix/spec_ZH.md` |
| Updater Download URL Redaction | `2026-07-10-updater-url-redaction/spec.md` | `2026-07-10-updater-url-redaction/spec_ZH.md` |
| TLS Plaintext Alert Diagnostics | `2026-07-11-tls-plaintext-alert-diagnostics/spec.md` | `2026-07-11-tls-plaintext-alert-diagnostics/spec_ZH.md` |
| Linux SSL_CERT_FILE Bootstrap | `2026-07-13-linux-ssl-cert-file-bootstrap/spec.md` | `2026-07-13-linux-ssl-cert-file-bootstrap/spec_ZH.md` |
| Claude Code 2.1.211 Endpoint Compatibility | `2026-07-15-cc-2.1.211-endpoint-compat/spec.md` | `2026-07-15-cc-2.1.211-endpoint-compat/spec_ZH.md` |
| Kimi Quota Query and Usage Statistics Parsing Fixes | `2026-07-17-kimi-quota-usage-parsing-fixes/spec.md` | `2026-07-17-kimi-quota-usage-parsing-fixes/spec_ZH.md` |

## 状态生命周期

功能允许的状态：

```text
draft -> approved -> planned -> implementing -> implemented -> validating -> validated -> shipped
```

异常状态：

```text
deferred
blocked
cancelled
superseded
```

规则：

1. `draft`：需求正在讨论中。
2. `approved`：需求已审阅通过，但实现计划可能尚未制定。
3. `planned`：`plan.md` 和 `validation.md` 已就绪，可开始执行。
4. `implementing`：代码开发已开始。
5. `implemented`：代码变更已完成，但验证尚未完成。
6. `validating`：自动化或手动验证正在进行。
7. `validated`：所需验证已通过，证据已记录。
8. `shipped`：变更已合并、发布或集成。
9. `deferred`：有意推迟；`status.md` 必须包含重新评估触发条件。
10. `blocked`：等待具体依赖或决策。
11. `cancelled`：不再实施。
12. `superseded`：已被其他功能文档替代。

## 决策记录

每个功能使用 `decisions.md` 记录局部决策。条目格式如下：

```markdown
## D-001: 简短决策标题

**日期：** YYYY-MM-DD
**状态：** accepted | superseded | deferred

**背景：** 为什么需要做此决策。

**决策：** 选择了什么方案。

**影响：** 此方案带来了什么能力和权衡。

**重新评估条件：** 应触发重新评估的具体条件。
```

项目级架构决策如果影响多个功能，后续可迁移至全局 ADR 目录。
