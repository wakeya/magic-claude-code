# M-1 修复报告：增量去重原始 TEXT 时间范围漏配历史偏移候选

依据 `review-gpt56-security-logic.md` §M-1（gpt-5.6 审查）。分支 `perf/usage-query-optimization`。

## 问题

增量候选查询（`internal/usage/dedupe.go` `incrementalDedupeCandidateQuery`）使用
`r.started_at >= ? AND r.started_at < ?`，参数为 canonical UTC 文本（`formatTime` 输出，
恒 `...Z` 结尾）。SQLite 对 TEXT 做字典序比较，不转换为同一 instant：历史脏库可能保留
`2026-07-30T12:00:00-07:00`（instant 19:00Z）这类带偏移文本，其字典序位置（`...T12...`）
落在 canonical 下界（如 `...T18:49:59Z`）之外而被排除，但旧 Go 去重（`time.Parse` 后
`Before/After` 含边界 ±10 分钟窗口）会配对。违反两条不变量：

1. 指纹时间差含边界 10 分钟窗口；
2. 完全兼容旧 Go 去重（迁移回填用 Go 解析正确，迁移后新增对端走增量却漏配）。

## 复现测试（TDD：先 RED 再修）

`internal/usage/dedupe_test.go` 新增
`TestDedupeIncrementalPairsHistoricalOffsetStartedAtText`（7 个子测试）：

| 子测试 | 场景 | 修复前 | 修复后 |
|---|---|---|---|
| historical session with -07:00 offset | 任务主复现：历史 session `2026-07-30T12:00:00-07:00`（19:00Z）+ Migrate + Record canonical provider 19:00Z | FAIL（漏配） | PASS |
| reverse order | 历史 provider 带偏移 + 新 session（反向插入顺序） | FAIL | PASS |
| fractional seconds | `2026-07-30T11:55:00.123456789-07:00` 小数秒偏移 | FAIL | PASS |
| inclusive boundary | 偏移文本 instant 恰为新行 +10 分钟整（含边界应配） | FAIL | PASS |
| just outside window | +10 分钟 +1ns（不得配，防修复过宽） | PASS | PASS |
| DST transition | -07:00（PDT）窗口内配对、-08:00（PST）65 分钟外不配 | FAIL | PASS |
| multiple offset spellings | 同 instant 三种表示（Z / -07:00 / +08:00 跨日）全配 | FAIL | PASS |

每个子测试均附加 `assertDedupeMarksMatchLegacyOracle` 差分：`store.Requests(raw)` 的
`DedupeStatus`/`DedupeRequestID` 与 `legacyOracleMarkDuplicates`（旧 Go 算法）逐字段一致。

## 修复方案

增量候选查询的时间过滤改为三层（`dedupe.go`）：

1. **epoch 决定性过滤**（新增）：`scopedEpochSecondsExpr(r.started_at) >= ? AND <= ?`，
   边界为 ±10 分钟窗口各加 1 秒整秒余量的 Unix 秒。`strftime('%s')` 按 instant 解析任意
   合法偏移的 RFC3339 文本，对偏移文本与 canonical 文本给出同一 instant 判定；±1 秒余量
   吸收小数秒截断与负 epoch（1970 年前）截断方向差异，构成 Go 含边界窗口的严格超集。
   非法时戳 COALESCE 到 Go 零值 Unix 秒，与 `parseTime` 容错一致（复用 R2 已有表达式）。
2. **TEXT 索引粗滤**（保留、放宽）：`r.started_at >= ? AND < ?` 仅为
   `idx_usage_requests_started_id` 加速，不决定候选。宽模式下按
   `maxHistoricalUTCOffsetSkew = 25h1s` 放宽（Go RFC3339 解析实测接受偏移至 ±24:59、
   拒绝 ±25:00；加 1 秒小数秒字典序余量），数学上覆盖窗口内任意合法偏移行的墙上文本位置。
3. **canonical 快速通道**（新增 OR 分支）：`(窄范围 ±10m±1s) OR substr(started_at,-1) <> 'Z'`。
   本系统全部写入经 `Record → formatTime` 恒输出 Z 结尾文本（已审计：`usage_requests`
   唯一生产写入点 `store.go` Record），Z 文本字典序即时序（±1 秒整秒边界吸收 RFC3339Nano
   变长小数字典序抖动），窄范围对 Z 行是 epoch 窗口的严格超集；非 Z 历史行经宽粗滤进入、
   由 epoch 决定。

**迁移期检测缓存**（`store.go`/`dedupe.go`）：settings 键
`usage_dedupe_offset_started_at_v1`，Migrate 时一次性检测
`EXISTS(... substr(started_at,-1) <> 'Z')` 并持久化（后续启动读 marker）。全 canonical
库（常态）粗滤边界收敛为窄边界 → 索引扫描范围与旧实现完全相同；含历史偏移文本的库启用
宽边界。状态未知（Migrate 未完成）保守按宽处理。运行期写入不引入非 Z 行，快照语义安全。

最终窗口判定仍为 `maintainDedupeCandidatesTx` 的 Go `Before/After` 含边界 ±10 分钟检查
（小数秒精度，未改动），与旧 Go 去重逐字段一致。回填路径（Go `time.Parse` + 二分窗口）
与 Coverage/聚合路径未改动。

## 性能验证（60k 行 / 30 天库，200 次均值，临时基准，未提交）

| 查询 | 单次耗时 |
|---|---|
| 旧实现（TEXT ±10m） | 176 µs |
| 新实现窄分支（全 canonical 库，常态） | 667 µs |
| 新实现宽分支（含偏移文本库） | 2.2 ms |

窄分支绝对增量 <0.5 ms（写路径噪声级，换得 epoch 统一决定语义）；宽分支为兼容历史脏库
的必要成本，仅偏移库触发。EXPLAIN 确认两分支均 `SEARCH r USING INDEX
idx_usage_requests_started_id (started_at>? AND started_at<?)`，
`TestDedupeIncrementalCandidateQueryUsesStartedAtIndex` 扩展为 wide={false,true} 双断言。

## 兼容性验证

- `go test ./internal/usage/`：全绿（含 7 个新回归子测试、marker 检测 3 子测试、边界宽度
  测试、既有 backfill/增量/并发/索引/差分 oracle 全部用例）。
- `go test ./...`：全绿。
- `go test -race ./internal/usage/`：全绿；`go test -race ./...`：全绿。
- `go vet ./...`：干净。
- legacy oracle 差分：新回归测试逐字段比对 `DedupeStatus`/`DedupeRequestID`；既有
  `legacy_oracle_test.go`/`sql_aggregate_test.go`/`ab_compare_test.go` 时区/DST/窗口/
  插入顺序覆盖全部保持通过。

## 变更文件

- `internal/usage/dedupe.go`：增量候选查询三层时间过滤；`incrementalDedupeSQLBounds`
  三组边界；`migrateOffsetStartedAtMarker` 检测持久化；`maintainDedupeCandidatesTx`
  改为 Store 方法读取检测缓存。
- `internal/usage/store.go`：`Store.hasOffsetStartedAt` 原子三态缓存；Migrate 接入
  marker 迁移；Record/recordIfAbsent 调用点改方法调用。
- `internal/usage/dedupe_test.go`：M-1 回归测试 + oracle 差分断言助手 + marker/边界测试 +
  索引计划双模式断言。
