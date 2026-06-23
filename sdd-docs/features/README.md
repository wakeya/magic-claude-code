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
