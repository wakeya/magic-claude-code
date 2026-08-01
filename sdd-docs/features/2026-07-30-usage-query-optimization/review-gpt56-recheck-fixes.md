# gpt-5.6 M-1/M-2/M-3 修复复审

复审范围：`git diff c094333..HEAD`，重点为 `4e82c87`（M-1）、`b130fc9`（M-2）、`bf5a2ad`（M-3）。原始依据为 `review-gpt56-security-logic.md` 与 `M1-fix-report.md`。本次未修改生产代码、测试或提交；仅新增本报告。

## 结论摘要

| 修复 | 结论 | 说明 |
|---|---|---|
| M-1 | PASS | 历史非 UTC `started_at` 按 instant 过滤，最终仍由 Go 精确窗口判定；7 个回归子测试均与独立 legacy oracle 逐字段一致。marker 三态缓存的并发访问无 data race，重启读取持久 marker 的代码路径正确。 |
| M-2 | PASS | Coverage 的 summary/status 在同一个 `ReadOnly` 事务中执行；WAL + driver barrier 测试确实在两查询之间提交 writer，修复后仍返回提交前一致快照，提交后调用可见新数据。 |
| M-3 | 需补修 | 终值边界、行内/跨行正常边界和直接溢出错误均已覆盖，但 spec 的 I2/I3 不变量遗漏“累加中间值不得溢出”。当前 parser 接受负数，故存在最终和合法但 SQLite 已先溢出并报错、旧 Go 得到可观察值的差异。 |

## Findings

### Critical

无。

### High

无。

### Medium

#### M3-R1：I2/I3 只约束最终和，未约束中间累加安全性

- **位置**：`internal/usage/scoped_query.go:63-65,134-146,261-267,315-332`；不变量记录于 `sdd-docs/features/2026-07-30-usage-query-optimization/spec_ZH.md:276-286`；现有边界测试为 `internal/usage/int64_boundary_test.go:53-217`。
- **问题**：I2 写成“每行四个 token 计数器之和在 int64 内”，I3 写成聚合组最终 token/duration 总和在 int64 内，但 SQL 行内表达式和 `SUM()` 都会按实际累加顺序处理。一个最终可表示的和，仍可能在中间步骤先溢出；SQLite 会把行内整数表达式提升为 REAL，或让 `SUM()` 直接报 `integer overflow`。这不属于当前 spec 声明的“不受支持”范围。与此同时 `usageFieldInt64` 接受负数，文档还明确允许 `MinInt64`，因此该场景不是只能靠非法数据库内容构造。
- **复现**：写入一条 `hasUsage=true` 的 token 行：`input_tokens=MaxInt64`、`output_tokens=1`、`cache_creation_input_tokens=-1`、`cache_read_input_tokens=0`。Go 旧逻辑左结合计算为 `MaxInt64`（`Max+1` 回绕为 `Min`，再减 1 回绕回 `Max`）；SQLite 先计算 `Max+1`，该中间结果已提升 REAL，即使随后减 1 回到数学上的 Max，Summary/Providers/Models/Trends 的 token 扫描仍返回 REAL 转 int64 的显式 `Scan error`。跨行同理：按聚合扫描顺序出现 `[MaxInt64, MaxInt64, -MaxInt64]` 时最终和为 MaxInt64，但 `SUM()` 可在前两个值处先报溢出。
- **测试覆盖**：现有 B1 覆盖 `MaxInt64+1` 和 `MinInt64-1` 这种最终也越界的行内和，B2 覆盖跨行最终溢出；A2/A3 覆盖最终恰为 Max 或接近 Max 的非负/单调组合，但没有“中间溢出后抵消”的行内或跨行用例。因此现有 49 项目标回归全绿不能证明 I2/I3 的完整语义。
- **修复方向**：二选一并写入 spec：
  1. 若承诺所有合法 int64 输入与旧 Go 兼容，则把 I2/I3 加强为每个实际累加前缀均在 int64 内，并在 SQL 中采用 checked arithmetic/fallback，或保留 Go 聚合处理会发生中间溢出的组；补充上述抵消用例的四端点 oracle 差分。
  2. 若产品契约是 token 非负，则在 parser/写入边界明确拒绝负 token，并把 I2/I3 定义为非负计数和的范围约束；同时补充约束回归，不能只靠文档声称真实数据不会出现负值。

### Low

无独立 Low 级生产缺陷。M-1 宽分支相对窄分支的额外扫描是有意为历史偏移兼容付出的成本：修复报告给出 60k/30 天库约 `667 µs`（窄）与 `2.2 ms`（宽），现有 EXPLAIN 双分支均走 `idx_usage_requests_started_id`；未见超过既定目标的确定性资源漏洞，但该基准不是自动性能门槛。

## 逐项复审

### M-1（4e82c87）

实现位于 `internal/usage/dedupe.go:408-495`。查询先用 TEXT 范围作为索引粗滤，再用 `strftime('%s')` 的 epoch 范围统一解析偏移文本与 canonical UTC 文本，最后由 `maintainDedupeCandidatesTx` 的 Go `Before/After` 在 ±10 分钟含边界窗口内做最终判定；因此 epoch 的 ±1 秒余量只扩大候选集，不改变最终去重语义。宽模式的 ±25 小时粗滤覆盖 Go 可接受的合法 RFC3339 偏移，窄模式对全 canonical 库收敛到原有范围；固定 `oppositeWhere` 和参数绑定未引入 SQL 注入路径。

`TestDedupeIncrementalPairsHistoricalOffsetStartedAtText` 的 7 个子测试分别覆盖偏移、反向插入、小数秒、含边界/刚越界、DST 和多种 offset spelling；每个用例都调用独立实现的 `legacyOracleMarkDuplicates` 逐行比较 `DedupeStatus/DedupeRequestID`，不是只断言候选表自身。marker 在 `internal/usage/store.go:12-28` 使用 `atomic.Int32` 三态，`internal/usage/dedupe.go:503-537` 启动时优先读持久 marker、未知状态按宽处理；当前写入路径统一经 `formatTime` 输出 `Z`，所以在系统契约内不存在 false→true 的运行期转换。没有发现过宽配对、过窄漏配、并发 data race 或快照跨重启泄漏。

### M-2（b130fc9）

`internal/usage/store.go:514-624` 通过 `BeginTx(..., ReadOnly: true)` 建立事务，summary/status 均使用 `tx.Query`，并在所有返回路径 defer `Rollback`；两条查询只读、不提交写入，事务结束后连接/快照释放。`coverage_snapshot_test.go` 的 barrier 识别 status 查询，在其真正执行前阻塞；测试随后通过另一连接提交 WAL writer，再放行 status。因此它能稳定复现原实现“两次 db.Query 两个快照”的时序，而非仅做静态 SQL 断言。修复后结果与提交前 `legacyOracleCoverage` 一致，之后的新 Coverage 调用能看到 writer，证明快照不跨调用泄漏。

### M-3（bf5a2ad）

现有测试确实覆盖：四个 token 单值接近 MaxInt64、行内和恰为 MaxInt64、跨行和为 MaxInt64-1、duration 接近 MaxInt64，以及行内/跨行两种直接溢出在四个聚合端点的显式 driver 错误。`go test ./internal/usage`、race、全仓测试和 vet 均通过。上述 M3-R1 是对“不变量定义与覆盖边界”的遗漏，不否定已测的直接溢出点行为；但在补齐前不能宣称对 spec 当前 I1-I3 范围完全兼容。

## 新缺陷与安全检查

- **SQL 注入**：未发现。M-1 的动态片段仅来自固定内部谓词/列名，所有值参数化；M-2 只改变查询载体；M-3 未改变 SQL 结构。
- **兼容性/资源**：M-1 常态窄分支保留 started_at 索引范围，历史偏移库宽分支会扫描更宽时间窗，报告中的性能退化已被说明且未构成确定性错误；M-2 事务范围只覆盖两条快速只读查询；M-3 的明确错误只触发聚合中间/最终溢出数据。
- **legacy oracle**：全仓 `go test ./...` 通过；M-1 新增测试逐字段差分，M-2/M-3 各自使用独立 legacy oracle/边界期望。未发现修复代码被 oracle 复用而形成自我证明。

## 验证命令

```text
go test ./internal/usage -run 'M-1/M-2/M-3 targeted tests' -count=1  -> 49 passed
go test ./internal/usage -count=1                                -> 2879 passed
go test -race ./internal/usage -count=1                          -> 2879 passed
go test ./... -count=1                                            -> 4679 passed
go vet ./...                                                      -> clean
```

