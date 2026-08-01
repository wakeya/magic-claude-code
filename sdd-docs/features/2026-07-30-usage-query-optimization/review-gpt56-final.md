# gpt-5.6 M3-R1 终审与全分支可合并性

审查基线：`070c2f601d9681930b54826ad982fe3399639b57`（父提交
`84e159c3c1ff5de7b1266f4d8fab7570a850dd2d`）。范围为补修实现、测试、
`M3-int64-boundary.md`、`spec.md`/`spec_ZH.md`，并对照
`review-gpt56-recheck-fixes.md` 的 M3-R1 抵消型复现。

## 结论摘要

补修确实关闭了 M3-R1：parser、SSE、`Record`、`recordIfAbsent` 均拒绝负
token；`Record`/`recordIfAbsent` 也拒绝负 duration；`Migrate` 在候选回填前
将历史负值归零。因此行内 `[MaxInt64, 1, -1]` 与跨行 `[MaxInt64, MaxInt64,
-MaxInt64]` 的抵消型中间溢出不能通过受支持写入路径进入聚合，纯正向溢出仍按
driver 级测试返回显式错误。

严格终审不建议标记为“无条件全分支 PASS”：发现 1 个 Medium（历史归零
迁移不是原子事务）及 2 个 Low（公共状态枚举文档遗漏、统计语义影响未明确）。
M-1、M-2 与 M3-R1 本身 PASS；修复 Medium 后，M-3 补修可给出无条件合并结论。

## Findings

### Critical

无。

### High

无。

### Medium

#### M3-F1：历史负值归零不是原子迁移

- **位置**：`internal/usage/store.go:120-133`。
- **问题**：`migrateNonNegativeUsageValues` 逐条调用 `s.db.Exec`，每个
  `UPDATE` 都是独立 autocommit；没有 `BEGIN`/`COMMIT` 包住七个字段的归零。
  这与同一 `Migrate` 中候选回填、candidate rank、查询索引迁移使用事务的
  保证不一致。
- **复现**：准备同时含负 token/duration 的旧库，在前一个或中间
  `UPDATE` 提交后通过进程终止、数据库锁/触发器错误让后续 `UPDATE` 失败。
  `Migrate` 返回错误，但已执行字段保持 0，尚未执行字段仍为负；若另一个
  读者在失败与重启之间读取统计，会看到部分归零状态。再次启动会最终修复，
  所以这是可恢复但非原子的中间状态。
- **测试覆盖**：`nonnegative_boundary_test.go:122-149` 仅覆盖一次成功
  迁移和最终全为 0；没有重复重开、注入中途失败、事务回滚或并发读验证。
- **修复方向**：把七条 UPDATE 放入一个显式事务，任一失败统一 rollback；
  增加中途失败后断言所有列仍保持原值、重开后完整归零的测试。

### Low

#### M3-F2：新增 `invalid_value` 未同步既有公共状态枚举文档

- **位置**：`internal/usage/types.go:20-25`、`internal/usage/parse.go:54-57`；
  既有枚举文档 `sdd-docs/features/2026-05-15-usage-statistics/requirements.md:145-154`。
- **问题**：实现对负 token 返回公开的 `invalid_value`，但公共 requirements
  的状态表仍只有旧六种状态。前端当前将状态按字符串展示，不会立即崩溃，
  但 API/文档消费者可能把新值误判为未知。
- **复现**：发送 `{"usage":{"input_tokens":-1}}`，parser 返回
  `source=none,status=invalid_value`；查阅 canonical requirements 的枚举表
  找不到该状态。
- **测试覆盖**：`nonnegative_boundary_test.go:11-40,92-98` 覆盖内部返回值；
  没有公共枚举文档或 API 合同校验。
- **修复方向**：在既有 requirements/API 状态枚举中加入 `invalid_value`，说明
  负 token 被拒绝且该请求仍可作为无 usage 请求记录。

#### M3-F3：归零与拒绝对统计的差异未明确写入 spec

- **位置**：`spec.md:348-352`、`spec_ZH.md:292-294`；实现为
  `internal/usage/store.go:122-128` 与 `:150-152,220-223`。
- **问题**：spec 已写“历史负值设为 0”和“新写入拒绝”，但未明确两者统计
  后果不同：历史行仍保留并计入请求/覆盖率分母，负 token 对 token 总量贡献
  变为 0，负 duration 对平均时长的分子贡献变为 0；新 `Record` 被拒绝时整行
  不插入（代理路径只记录失败日志）。这不是 M3-R1 绕过，但会影响迁移前后
  的历史统计解释。
- **复现**：旧库一行含负 token、负 duration，迁移后请求行仍存在、聚合 token
  和 duration 变为 0；同样数据通过新 `Record` 则无行。现有迁移测试只直接
  查询列值，没有对 Summary/Providers/Models/Trends 的迁移前后统计作锚定。
- **测试覆盖**：`nonnegative_boundary_test.go:122-149` 覆盖列归零；无统计
  语义回归。
- **修复方向**：在中英文 spec 明确“归零保留请求行/分母，拒绝新写入不产生
  行”的差异；补充至少 Summary 与平均 duration 的迁移前后断言。

## 逐项核验

### 1. 非负入口与绕过路径

生产写入入口只有 `Store.Record` 和包内 `recordIfAbsent`；proxy 通过
`Record`，session-sync 通过 `recordIfAbsent`。没有发现生产批量导入或其他
`usage_requests`/`usage_tokens` INSERT 绕过。直接外部 SQL 仍可制造脏值，但
启动 `Migrate` 会归零；这属于数据库外部修改，不是正常 API 入口。

`0`、`MaxInt64` 和合法边界未被拒绝：定向边界测试覆盖单值接近 Max、行内恰为
Max、跨行接近 Max 与 duration；负 token 在 parser/SSE 返回 `invalid_value`，
写入边界返回不泄漏 SQL 的 `ErrNegativeTokenCount`/
`ErrNegativeDuration`。

### 2. M3-R1 抵消路径与纯正向溢出

行内 `MaxInt64+1-1` 的负项无法从 parser、SSE 或两个 Store 写入入口进入；
跨行负项同理，历史负项在 `Migrate` 的候选回填前归零。`int64_boundary_test.go`
保留纯正向行内溢出（`MaxInt64+1`）和跨行 `SUM` 溢出（`2*MaxInt64`）的
显式错误断言，未因补修而退化。需要注意，单值 `MaxInt64` 仍是合法边界，
因此故意构造的纯正向聚合溢出仍可能由极端数据触发；它属于 spec 明确的
I2/I3 范围外，行为是显式错误而非静默回绕。

### 3. M-1/M-2/M-3 合并性

- **M-1：PASS**。本补修未触碰 4e82c87 的时间归一化/去重路径；既有复审结论
  和本次全量测试均无回归。
- **M-2：PASS**。本补修未触碰 b130fc9 的单只读事务快照路径；既有复审结论
  和本次全量测试均无回归。
- **M-3-R1：PASS**。负值抵消型中间溢出已不可达，纯正向溢出显式错误仍保留。
- **M-3 补修整体：条件 PASS**。功能修复成立，但 M3-F1 的迁移原子性尚未
  达到同一迁移体系的原子保证；修复后才是无条件 PASS。

## 验证证据

```text
rtk go test ./internal/usage -run 'Test(ExtractUsageRejectsNegativeTokenCounters|RecordRejectsNegative|RecordIfAbsentRejectsNegative|SSEObserverRejectsNegative|HistoricalNegativeUsageValuesAreNormalizedOnMigrate|NegativeCounterCancellationCannotReachAggregation|Int64Boundary)' -count=1 -> 43 passed
rtk go test ./internal/usage -count=1                                  -> 2887 passed
rtk go test -race ./internal/usage -count=1                              -> 2887 passed
rtk go test ./... -count=1                                                -> 4687 passed
rtk go vet ./...                                                          -> No issues found
rtk git diff --check 070c2f6^ 070c2f6                                    -> clean
```

## 终审结论

M3-R1 的原始抵消型复现已被新非负契约切断，且全量、race、vet 均通过；
M-1/M-2 未见回归。严格按“全分支各自 PASS 且可无条件合并”标准，本次结论为
**暂不无条件合并**：不存在 Critical/High 阻断，但应先将 M3-F1 改为事务原子
归零；M3-F2/M3-F3 可作为文档与语义澄清的低风险后续。若项目允许带 Medium
风险合并，则功能层面可合并，残余风险是迁移中断后的短暂部分归零状态及历史
统计解释不透明。
