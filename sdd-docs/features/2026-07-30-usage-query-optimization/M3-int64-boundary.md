# M-3 明确并加固 SQL token/duration 聚合的 int64 边界语义

审查来源：`review-gpt56-security-logic.md` §M-3（中危，边界库）。本记录给出数据边界
结论、加固方式与验证证据。结论同步写入 `spec.md` / `spec_ZH.md`「受支持数据范围
（int64 聚合边界）」。

## 1. 问题重述

`scoped_query.go:63-65,134-146,261-267,315-332` 把旧 Go 逐行 int64 累加（`store.go`
旧实现 `tokenTotal`/各 accumulator，逐行 `+=`，静默回绕）下推为 SQL 行内四 token 相加
与 `SUM()`。SQLite 整数表达式溢出时提升为 REAL、`SUM()` 整数累计溢出时可能报错；
若 modernc driver 把 REAL 静默扫描成 int64，公开响应会出现舍入/失真，违反响应数值
类型与兼容聚合语义。

## 2. 数据边界结论（实测 + 代码调研）

### 写入路径单值边界（I1）

| 写入路径 | 单值上限 | 依据 |
|---|---|---|
| API parser（`parse.go:usageFieldInt64`） | 整数恰 `MaxInt64`/`MinInt64` 可写入；`> MaxInt64` 整数与 `≥ 2^63` 浮点按垃圾忽略 | `parse_test.go:TestExtractUsageInt64Boundary`、`TestExtractUsageRejectsOutOfRangeNumbers` 已锁定 |
| session-sync（`session_sync.go`） | `encoding/json` 对 int64 字段拒绝超界 | Go 标准行为 |
| `duration_ms` | Go `time.Since().Milliseconds()` ≤ ~9.2e12 ms | 代码路径 |

即：**单值恒为合法 int64（I1），但恰为 MaxInt64 的单值是 parser 允许的输入**。

### 聚合边界（I2/I3）

- **I2 行内和**：parser 允许单值 MaxInt64，因此理论上可构造 `input=MaxInt64, output=1`
  使行内和溢出——但真实 token 计数（≤ ~1e9/计数器）下不可能。
- **I3 跨行和**：60k 行生产数据集 × 单值 ~1e9 ≈ 6e13 ≪ 9.2e18（富余 5+ 个数量级）；
  duration 更可证明安全：单值 ≤ 9.2e12 ms × 10^6 行 = 9.2e18。
- 无 schema 约束、历史库由同一 parser 写入：真实数据恒满足 I2/I3；只有手工编辑
  数据库写入接近 2^63 的伪造计数才会越界。

**结论：产品数据范围安全**（审查认可的分支：「如果产品明确规定数据库 token 必须远离
边界，此项可降为低危」）。按任务约束「优先明确+测试避免过度工程」，采用「明确数据
边界 + driver 级边界回归测试 + 文档定义溢出点行为」方案，不做旧回绕语义的 fallback
（旧回绕本身输出负数垃圾，且现实数据永不触发）。

## 3. 溢出点实测行为（目标 driver：modernc.org/sqlite v1.50.1 + Go 1.26.2）

| 场景 | SQLite 行为 | driver/database/sql 行为 |
|---|---|---|
| 行内和溢出（`MaxInt64+1`、`MinInt64-1`） | 整数表达式提升为 REAL（float64） | `Scan` 到 `*int64` 报 `Scan error ... converting driver.Value type float64 ... to a int64: invalid syntax`（显式错误） |
| 跨行 `SUM()` 溢出（`2×MaxInt64`、`2×MinInt64`） | 查询时报 `SQL logic error: integer overflow (1)` | 查询失败（显式错误） |
| 范围内边界（恰 `MaxInt64`、`MaxInt64-1` 等） | 恒 INTEGER | `Scan` 精确 int64 |

**实测没有出现审查担心的「静默舍入/失真」**：两种溢出路径都是显式错误，行为确定、
可测试锁定。旧 Go 实现在这两点静默回绕（如 `MaxInt64+1 → MinInt64`、`2×MaxInt64 → -2`），
新路径选择显式报错——仅对不受支持的超界数据生效。

## 4. 加固与验证

新增 `internal/usage/int64_boundary_test.go`（driver 级，Store 端点 + legacyOracle 差分）：

- **A 组（范围内锁定 SQL == 旧 Go 语义，逐字段一致）**：
  - 单值接近 MaxInt64（四个计数器各测一次）；
  - 行内和恰为 MaxInt64（`MaxInt64/2 + MaxInt64/2+1`、单值恰 `MaxInt64`、单值恰 `MinInt64`）；
  - 跨行总和接近 MaxInt64（两行各 MaxInt64/2 → 和 MaxInt64-1，含绝对期望值锚定）；
  - duration 接近 MaxInt64（单行、两行两种）；
  - 四个端点全覆盖：Summary/Providers/Models/Trends。
- **B 组（溢出点行为确定，锁定显式错误 + 旧回绕值文档对照）**：
  - 行内和溢出（正/负方向）→ 断言 `Scan error`（含 `float64`）；
  - 跨行 `SUM()` 溢出（正/负方向）→ 断言 `integer overflow`；
  - 同时断言 legacyOracle 的回绕值（`MinInt64`/`-2`/`MaxInt64`/`0`）作文档对照。

文档：`spec.md` / `spec_ZH.md` 新增「受支持数据范围（int64 聚合边界）」小节，记录
I1/I2/I3 不变量、真实量级富余论证与溢出点行为定义。

## 5. 验证结果

```text
go test ./internal/usage/ -run TestInt64Boundary            → ok（A/B 全绿）
go test ./...                                               → 全绿
go test -race ./internal/usage/                             → 全绿
go vet ./...                                                → 全绿
```

## 6. 提交

（见 git log：M-3 边界测试与 spec 数据边界记录 commit）
