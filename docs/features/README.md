# 功能文档结构

本目录按功能（而非文档类型）组织文档。每个功能将需求、实现计划、验证清单、决策记录和生命周期状态集中管理。

## 目录布局

```text
docs/features/<yyyy-mm-dd-feature-name>/
  requirements.md
  plan.md
  validation.md
  decisions.md
  status.md
```

## 文件职责

| 文件 | 用途 |
|------|------|
| `requirements.md` | 定义范围、目标、约束、边界条件和验收意图。是功能"做什么"的唯一事实来源。 |
| `plan.md` | 定义具体实现步骤、涉及的文件、需编写的测试、执行的命令和提交检查点。 |
| `validation.md` | 定义验证清单，并在实现后记录实际验证证据。 |
| `decisions.md` | 记录带日期的决策，包括上下文、选择、影响和重新评估触发条件。 |
| `status.md` | 跟踪生命周期状态、负责人、时间戳、阻塞项和下一步转换。 |

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
