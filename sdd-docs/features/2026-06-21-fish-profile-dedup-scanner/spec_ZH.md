# Fish Profile 去重扫描器 规范

范围：`internal/bootstrap/adapters.go`、`internal/bootstrap/bootstrap_test.go`
参考面：`PersistRoot()`、`fishLineMatches()`、fish 注释处理、shell profile 去重
参考源：`sdd-docs/features/README.md`、`sdd-docs/features/2026-06-13-auto-update/spec.md`、`sdd-docs/features/2026-06-13-auto-update/spec_ZH.md`
技术栈：Go 1.26 标准库
最后更新：2026-06-21
进度：3 / 3 已完成

## 问题说明

当前 MCC 的 bootstrap 流程已经会把环境条目写入 shell profile，以便后续任意目录启动时仍能找到可执行文件根目录和内置 CA。bash、zsh、unknown shell 的处理已经比较稳定，但 fish 分支的匹配逻辑现在还是一个“保守近似”实现：

- 能识别明确的导出 flag
- 能拒绝 local / erase 这类非导出语义
- 能处理部分 quoted value 和 inline comment
- 能处理部分 backslash escape

这足够覆盖常见场景，但对一些合法 fish 写法来说，仍然存在“少去重”的残余边界。

用户希望把 fish 语义再收紧一些，但不希望把它做成完整 shell parser。

## 目标

把 fish profile 去重做得更语义化、更稳，同时保持安全：

- 更可靠地识别 MCC 等价的 fish 导出行
- 继续拒绝非导出 fish `set` 形式
- 保持 comment 和 escape 的正确处理
- bash / zsh / unknown 逻辑完全不变
- 不引入完整 fish 解析器

## 非目标

- 不解析完整 fish 语言。
- 不处理变量展开、命令替换、换行续接、嵌套求值等复杂语法。
- 不修改 Windows profile 行为。
- 不修改 bash / zsh / unknown 的匹配规则。
- 不改变 `PersistRoot()` 选择候选 profile 的逻辑。

## 当前行为

`PersistRoot()` 现在会：

- 生成目标条目
- 逐个检查候选 profile 里是否已存在等价条目
- 如果没有，再追加写入

fish 分支已经有了一套手工实现的 comment stripping、flag 白名单和 value 比对逻辑，但它仍然依赖较多 token 级近似，因此在一些合法但稍微复杂一点的 fish 写法上，可能会：

- 误判成不相等，从而重复追加
- 或者误判成相等，从而不写入真正需要的条目

本次需求就是把这部分收口到一个更清晰的小扫描器里。

## 推荐方案

### 推荐：小型 fish 导出扫描器

新增一个只服务于 profile 去重的 fish 扫描器，专门理解 MCC 需要的 fish 子集：

- `set` 命令
- 明确的导出 flag 白名单：
  - `-x`
  - `-gx`
  - `--export`
- key token
- value span
- trailing inline comment
- 引号和反斜杠转义

这个扫描器应该返回结构化判断，而不是只做原始 token 拼接。

### 内部结构

保持对外接口尽量小。实现上可以拆成这些小帮助函数：

- `stripFishComment(line string) string`
- `splitFishExportLine(line string) (...)`
- `fishLineMatches(line, key, value string) bool`

职责划分如下：

- `stripFishComment` 只负责 comment 处理
- 扫描器负责解析 fish token 边界和 quoting / escaping
- `fishLineMatches` 负责最终的“是否等价”判定

### 匹配规则

只有同时满足下列条件时，fish 行才算重复：

1. 它是 `set` 命令。
2. 它包含至少一个允许的导出 flag。
3. key 完全匹配。
4. value 在语义上与目标 MCC value 等价。
5. 如果 value 是 fish list 语法且不是 quoted scalar，就不要把它当成等价。
6. 尾随 inline comment 不能影响匹配结果。
7. 引号内或被转义的 `#` 不能被当作 comment 起点。

### value 语义

value 比较要区分：

- quoted 单字符串
- unquoted 单 token
- fish list 多 token

匹配策略应该在安全前提下尽量覆盖常见语义：

- 对单字符串尽量去重
- 对歧义的 list 语法保持保守

这样能改善去重覆盖，同时避免错误地把用户配置看成已经写入。

## 架构设计

### 1. Comment 处理层

`stripFishComment` 仍然是第一层处理。

它应该继续做到：

- 忽略单引号和双引号内的 `#`
- 忽略未转义的 `\#`
- 忽略紧贴 token 的 `#`
- 只在 `#` 真正是 comment 起点时裁断行尾

这层要保持保守。若无法明确判断，就保留原文本，不要错误截断。

### 2. Fish 导出扫描层

新增一个小扫描器，负责记录：

- 行首是否是 `set`
- 导出 flag 是否在白名单中
- key 是什么
- value span 是什么
- value 是否 quoted
- value 是否是单值还是 list

这个扫描器不尝试理解完整 fish 语法，只覆盖 MCC 需要的 profile 场景。

### 3. 去重策略层

`fishLineMatches` 只负责消费扫描结果，并在以下情况下返回 `true`：

- 语法结构清晰
- 导出语义明确
- key 和 value 语义等价

这样能把将来鱼壳相关边界集中在一个点上，不再散落在多个 ad hoc 分支里。

## 错误处理

解析失败必须安全失败：

- 如果 fish 行无法被明确解析，就视为不匹配
- 不要把模糊或坏掉的语法当成等价条目
- 不要仅凭匹配逻辑去改写用户配置

这段逻辑的用途只是去重，不是执行 shell。

## 测试计划

在 `internal/bootstrap/bootstrap_test.go` 中补以下单测：

### 必须保持通过的既有行为

- `set -x MCC_ROOT /opt/mcc` 匹配
- `set -gx MCC_ROOT /opt/mcc` 匹配
- `set --export MCC_ROOT /opt/mcc` 匹配
- `set -l MCC_ROOT /opt/mcc` 不匹配
- `set -e MCC_ROOT /opt/mcc` 不匹配
- `set -u MCC_ROOT /opt/mcc` 不匹配
- `set MCC_ROOT /opt/mcc` 不匹配
- bash / zsh / unknown 行为保持不变

### comment / escape 处理

- 尾随 inline comment 被忽略
- 单引号中的 `#` 不会被当 comment
- 双引号中的 `#` 不会被当 comment
- `\#` 不会被当 comment
- 被转义的引号不会破坏匹配
- 紧贴 token 的 `#` 不会被当 comment

### fish scalar / list 语义

- 带空格的 quoted scalar 能匹配
- unquoted list 不能和 quoted scalar 混成一类
- 歧义较强的 fish list 语法保持不匹配

### 端到端去重

- 已有语义等价的 fish 导出行时，`PersistRoot()` 不再重复追加
- 只有 local 变量或歧义 fish 语法时，`PersistRoot()` 仍会继续写入真正的导出条目

## 任务详情

### 任务 1：Fish Profile 去重扫描器收口

#### 需求

**Objective（目标）** — 让 fish profile 去重更语义化、更稳，同时不把它做成完整 shell parser。

**Outcomes（成果）** — `fishLineMatches()` 仍然是 fish 行去重的唯一判定点；可以引入一个小型 helper scanner；歧义 fish 输入继续视为不匹配；bash / zsh / unknown 行为完全不变。

**Evidence（证据）** — 单测覆盖明确导出 flag、非导出 `set` 形式、尾随 comment、转义处理、带空格的 quoted scalar、unquoted list 语法，以及 `PersistRoot()` 的端到端去重行为。

**Constraints（约束）** — 范围只限于 `internal/bootstrap/adapters.go` 和 `internal/bootstrap/bootstrap_test.go`；继续保持当前安全偏好；不引入完整 fish parser；不改变 Windows 或非 fish 逻辑。

**Edge Cases（边界）** — 行尾 inline comment；被转义的 `#`；引号内的 `#`；带空格的 quoted scalar；unquoted fish list；格式不完整或歧义的 fish 语法。

**Verification（验证）** — `go test ./...` 通过，fish 专项测试矩阵证明目标去重行为成立。

#### 计划

1. 保持 `PersistRoot()` 的候选 profile 选择逻辑和非 fish 逻辑不变。
2. 将 fish 解析收口成一个小扫描器 / helper，提取：
   - 命令类型
   - 导出 flag 是否存在
   - key token
   - value span
   - value 是否 quoted
   - value 是 list 还是 scalar
3. 让 `stripFishComment()` 保持保守：
   - 忽略引号内的 `#`
   - 忽略被转义的 `#`
   - 忽略紧贴 token 的 `#`
   - 遇到歧义时不要裁断
4. 让 `fishLineMatches()` 消费扫描结果，只在明确等价的导出形式上返回 `true`。
5. 保留现有 fish 导出白名单：
   - `-x`
   - `-gx`
   - `--export`
6. 在 `internal/bootstrap/bootstrap_test.go` 中补或更新测试，覆盖：
   - 导出 flag 白名单
   - 非导出 fish `set` 形式
   - 尾随 comment
   - 转义处理
   - 带空格的 quoted scalar
   - unquoted list 语法
   - `PersistRoot()` 端到端去重

#### 验证

- `go test ./...` 通过。
- fish 导出标量继续正确去重。
- 歧义 fish 列表语法继续保守失败。
- bash / zsh / unknown 行为不变。

## 验收标准

- `go test ./...` 通过
- fish 去重不回退已有案例
- fish comment / escape 处理保持安全保守
- bash / zsh / unknown 不受影响
- 实现规模仍然足够小，不需要完整 shell parser

## 拒绝的方案

### 继续堆 ad hoc 规则

拒绝原因：现在的逻辑已经接近 parser 复杂度，继续堆分支只会让后续更难维护。

### 直接做完整 fish parser

拒绝原因：范围过大，远超本次 profile 去重问题。

### 放宽匹配

拒绝原因：错误地把用户配置视为已存在，会导致真正的环境条目不再写入，风险方向不对。
