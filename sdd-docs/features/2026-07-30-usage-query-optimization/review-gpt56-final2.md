# gpt-5.6 终审：6fbda0a 后 M3-F1/F2/F3 与全分支合并性

审查对象：`6fbda0ad9f7f25a220314ca1842a5aff7bf9d12e`（`perf/usage-query-optimization`）

对照材料：`review-gpt56-final.md` 的 M3-F1/M3-F2/M3-F3；重点检查
`internal/usage/store.go`、`internal/usage/nonnegative_boundary_test.go`、
`spec.md`、`spec_ZH.md` 与公共 `requirements.md`。

## 结论摘要

本次未发现 Critical/High/Medium/Low findings。`6fbda0a` 已将七条历史负值归零
更新放入一个显式事务；中途失败会整体回滚，并通过 barrier、触发器、并发读、失败
后重试覆盖原非原子问题。`invalid_value` 已补入公共 requirements 枚举，中英文
spec 也准确区分了历史归零（保留请求行/分母、token/duration 贡献为 0）与新写入
拒绝（不插入行）的统计语义。

最终结论：M-1、M-2、M-3-R1、M3-F1、M3-F2、M3-F3 全部 PASS，达到“全分支可
无条件合并”。残余风险仅为迁移本身仍是启动阶段的写事务：数据量极大或外部写事务
长期持锁时，七条更新可能延长 writer 等待；这属于 SQLite 启动迁移的一般运行时
风险，不构成该补修的 correctness 缺陷。

## Findings

### Critical

无。

### High

无。

### Medium

无。M3-F1 已关闭：`internal/usage/store.go:120-140` 在循环前
`Begin`，七条 `UPDATE` 均使用 `tx.Exec`，成功后才 `Commit`；`defer tx.Rollback()`
覆盖 begin 后的任一执行失败。归零迁移没有独立 marker，其完成条件是每次按
`WHERE < 0` 扫描后的无负值状态，因此天然幂等；它在候选 backfill 前运行。即使
归零已提交而后续 candidate/index marker 迁移失败，下一次启动仍会安全地重复执行
无副作用的归零并继续重开，未发现 marker/归零之间的可恢复性缺陷。

### Low

无。M3-F2 已关闭：`sdd-docs/features/2026-05-15-usage-statistics/requirements.md:145-155`
的公共 `usage_parse_status` 枚举已包含 `invalid_value`，并准确说明负 token
被拒绝、请求作为无 usage 行保留、token 贡献为 0。M3-F3 已关闭：
`spec.md:327-373` 与 `spec_ZH.md:276-307` 均明确写出历史负值归零保留请求行及
请求/coverage 分母，token 与 duration 聚合贡献变为 0；而 `Record`/
`recordIfAbsent` 的新负值在插入前拒绝，不产生请求行。

## 逐项核验

### M3-F1：事务、原子性、测试复现与重开

- 七条更新准确覆盖四个 token 列和三个 duration/时延列：
  `store.go:127-135`。
- 事务边界为单个显式 `sql.Tx`；任一 `tx.Exec` 返回错误时函数立即返回，defer
  rollback；只有七条都成功才 commit：`store.go:121-140`。
- 新测试的 `migrationUpdateBarrier` 在首条 token UPDATE 执行后暂停，事务尚未
  commit；并发读在 `nonnegative_boundary_test.go:195-198` 断言仍见七个原始负值。
  父提交 `070c2f6:120-134` 使用逐条 `s.db.Exec` autocommit，因此同一 barrier
  下首条 UPDATE 已提交，读者会看到部分归零，测试确实能复现原问题而非只检查
  最终状态。
- `fail_negative_usage_migration` 在第五条 request UPDATE 前触发 abort：
  `nonnegative_boundary_test.go:181-186`；释放 barrier 后测试在
  `:200-205` 断言错误与七列全部保持原值，在 `:207-213` 删除故障并重新
  `Migrate`，断言七列全部归零。该测试及 race 版本均 GREEN。
- 事务范围仅包含七条短 UPDATE，不包含候选回填或其它 marker 迁移；因此没有把
  大型 backfill 纳入该锁。WAL 下读者可在事务期间读取旧快照，写者可能等待该
  短事务，但未见新增死锁或不可恢复锁持有路径。

### M3-F2：公共枚举

实现常量仍为 `ParseStatusInvalidValue = "invalid_value"`，公共 requirements
已同步该字符串及行为说明；没有发现中英文公共契约不一致。

### M3-F3：统计语义

代码路径确认 proxy 对 `invalid_value` 仍调用 `finishUsageRecord`，以零 token
写入请求行；历史迁移则只把已有负列设为 0，不删除行。当前 SQL 聚合使用
`COALESCE(r.duration_ms, 0)` 并按请求总数计算平均时长，因此“duration 贡献为 0”
指分子/总和贡献为 0，同时该历史请求仍保留在请求计数与平均值分母中；文档表述与
实现一致。

## 验证证据

```text
rtk go test ./internal/usage -run 'TestHistoricalNegativeUsage(MigrationIsAtomicAndReaderConsistent|ValuesAreNormalizedOnMigrate)$' -count=1 -v  -> PASS
rtk go test -race ./internal/usage -run 'TestHistoricalNegativeUsage(MigrationIsAtomicAndReaderConsistent|ValuesAreNormalizedOnMigrate)$' -count=1 -v -> PASS
rtk go test ./internal/usage -count=1  -> 2888 passed
rtk go test -race ./internal/usage -count=1 -> 2888 passed
rtk go test ./... -count=1 -> 4688 passed
rtk go vet ./... -> No issues found
rtk git diff --check 070c2f6^ 6fbda0a -> clean
```

## 终审结论

**PASS：M-1/M-2/M-3（含 M3-R1、M3-F1、M3-F2、M3-F3）全部通过，可无条件合并。**

未发现会阻止合并的残余 correctness、回滚、幂等、文档契约或并发快照问题。仅保留
SQLite 启动迁移写事务在超大历史库或外部长事务下可能增加 writer 等待的运行时风险；
这不是本 commit 引入的功能缺陷，也不改变本次无条件合并结论。
