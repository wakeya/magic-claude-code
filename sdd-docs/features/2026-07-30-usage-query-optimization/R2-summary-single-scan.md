# R2 返工报告（P0）：消除 Summary 标量子查询二次物化

- 分支：`perf/usage-query-optimization`
- 依据：R1 诊断报告 §1.2 / §5-P0。
- 范围约束：只改 Summary/status 查询结构；Trends/Providers/Models/Coverage/Requests 未动（EXPLAIN 逐行 diff 证实五端点计划字节级不变）；不改前端；保持 6A/6B2 逐字段兼容。
- 一句话结论：**Summary 的"最新 provider 请求时间"从标量子查询改为与主聚合共享同一次 scoped 扫描的 `MAX(定长时间键 || started_at)` 编码，EXPLAIN 证实计划从 2 份 scoped 消费（4 SCAN / 2 AUTO INDEX / 2 TEMP B-TREE / 3 子查询）降为 1 份（3 / 1 / 1 / 2）；同会话 A/B 三次独立会话 Summary 加速比 1.35×–1.40×（样本方差极小），六并发墙钟在无尖刺会话为 1.06×–1.21×；全部差分/回归/race/vet 验证通过。**

---

## 1. 问题（R1 §1.2 / §5-P0）

R1 之后的 Summary 查询计划（`explain_before.txt`，已含 R1 覆盖索引）：

```
MATERIALIZE candidate
CO-ROUTINE (subquery-6)
SCAN d USING COVERING INDEX idx_usage_dedupe_candidates_session_priority
...（candidate 窗口：SEARCH r/t ×2 + USE TEMP B-TREE FOR LAST 3 TERMS OF ORDER BY）
SCAN (subquery-6)
SCAN r                                          ← 主聚合：第 1 次 60k 全扫
SEARCH t ... / SEARCH r ... / SEARCH t ...
BLOOM FILTER ON candidate ...
SEARCH candidate USING AUTOMATIC PARTIAL COVERING INDEX ... LEFT-JOIN   ← 第 1 个自动索引
SCALAR SUBQUERY 4                               ← last_provider_request 标量子查询
SCAN r                                          ← 第 2 次 60k 全扫
SEARCH t ... / SEARCH r2 ...
BLOOM FILTER ON candidate ...
SEARCH candidate USING AUTOMATIC PARTIAL COVERING INDEX ... LEFT-JOIN   ← 第 2 个自动索引
USE TEMP B-TREE FOR ORDER BY                    ← 子查询 epoch/fraction 末级排序
```

`last_provider_request` 的标量子查询 `SELECT r2.started_at FROM scoped sc2 JOIN ... ORDER BY epoch DESC, fraction DESC LIMIT 1` 使 scoped 消费路径（60k 全扫 + LEFT JOIN candidate 探测 + 运行时自动索引重建 + 末级排序）**执行两次**。开销汇总：`SCAN=4 TEMP_BTREE=2 AUTO_INDEX=2 SUBQUERY/CO-Routine=3`。

## 2. 修复：单次扫描 MAX 时间键编码

`internal/usage/scoped_query.go`：

- 新增 `scopedTimeOrderKeyExpr(column)`：返回定长 29 字符时间排序键——
  `printf('%020d', epoch + 62135596800) || 9位小数秒`。整秒 epoch 偏置 Go 零值时间的 Unix 秒后恒非负（合法时戳 ≥ 1、非法时戳恰为 0），补零定长 20 位；键的字典序等价于“(整秒 epoch, 9 位小数秒)”元组序，精确复现旧 `ORDER BY epoch DESC, fraction DESC` 语义（含非法时戳容错为零值）。
- `buildSummaryQuery` 的 `last_provider_request` 由标量子查询改为同一 SELECT 内的单个聚合：
  `substr(MAX(<29字符时间键> || r.started_at), 30)`。
  定长前缀保证 MAX 字典序 == 时间序；第 30 位起即最晚行 started_at 原值，调用方 `parseTime` 不变。并列行（同时刻不同字符串表示）由 started_at 字符串降序决胜，与旧差分判定器“按 started_at DESC 迭代、严格 After 保留首个遇到的行”的并列语义一致。空数据集 MAX 返回 NULL → `sql.NullString` 无效 → nil 指针，与旧子查询 NULL 行为一致。
- 参数顺序与占位符数量不变（CTE 筛选+口径参数后接 4 个今日边界参数），`TestBuildSummaryQueryAggregatesScopedDataset` 的精确参数断言原样通过。

R2 之后的计划（`explain_after_r2.txt`）：

```
MATERIALIZE candidate
CO-ROUTINE (subquery-5)
SCAN d USING COVERING INDEX idx_usage_dedupe_candidates_session_priority
...（candidate 窗口，同上）
SCAN (subquery-5)
SCAN r                                          ← 唯一一次 60k 全扫
SEARCH t ... / SEARCH r ... / SEARCH t ...
BLOOM FILTER ON candidate ...
SEARCH candidate USING AUTOMATIC PARTIAL COVERING INDEX ... LEFT-JOIN   ← 唯一自动索引
```

### EXPLAIN 前后对比

| 指标 | R1（优化前） | R2（优化后） | 变化 |
|---|---:|---:|---|
| SCAN | 4 | **3** | 第二次 `SCAN r`（60k 全扫）消除 |
| AUTO INDEX | 2 | **1** | 第二个运行时自动索引消除 |
| TEMP B-TREE | 2 | **1** | 子查询末级 `ORDER BY` 排序消除 |
| SUBQUERY/CO-Routine | 3 | **2** | `SCALAR SUBQUERY` 消除 |
| USING_INDEX | 12 | 9 | 第二份探测的 3 个索引查找随之消除 |

计划形态与 Requests-count 等单扫描端点同构。**五端点计划逐行 diff：字节级不变**（`diff` 仅 Summary 两行成本汇总变化），满足“只改 Summary/status”约束。

## 3. 同会话 A/B 测量（`ab_compare_test.go` 新增 `TestUsageSummaryR2ABCompare`，MCC_USAGE_AB=1，60,332 行 seed=1）

方法：同一进程、同一数据集上交替测量 R2 新结构与 test-only 重建的 R1 旧结构（逐字复现旧标量子查询）；先逐字段断言两套查询结果完全一致，再交替 3 轮（轮内顺序交替抵消趋势漂移），各取中位。Summary 每样本 5 次运行中位；六并发每样本 5 次运行中位（六并发对负载极敏感，小样本会被单次尖刺主导，R1 已证）。

三次独立 A/B 会话（绝对值随机器负载漂移，仅同会话比值可归因）：

| 会话 | Summary 旧 | Summary 新 | **加速比** | 六并发旧 | 六并发新 | 加速比 |
|---|---:|---:|---:|---:|---:|---:|
| #1 | 489 ms | 360 ms | **1.36×** | 2.31 s | 2.18 s | 1.06× |
| #2 | 560 ms | 400 ms | **1.40×** | 3.20 s | 4.51 s | 0.71×（新配置轮遭外部负载尖刺 4.5–4.8s，同代码同配置上一会话仅 2.2s，数据污染不可归因） |
| #3 | 553 ms | 409 ms | **1.35×** | 4.27 s | 3.53 s | 1.21×（3 轮全部新<旧） |

- **Summary 加速比 1.35×–1.40×，三次会话高度一致**；样本内方差极小（如 #1：旧 [489, 487, 493] ms、新 [365, 358, 360] ms），差异远超噪声带（R1 索引 A/B 为 0.95×–1.10×）。
- 六并发墙钟在无尖刺会话为 1.06×–1.21×；#2 被外部尖刺污染（与 R1“六并发两次 A/B 方向相反”的结论一致：该指标对共享机器负载极敏感，只能定性）。
- **逐字段等价**：三次会话均在 60,332 行上断言新旧查询 7 列聚合结果完全相同（`last_started=2026-07-30T11:57:41.38601597Z` 一致）。

### 为何是 ~1.37× 而非 2×

旧查询的 candidate 物化（`MATERIALIZE candidate` + 窗口排序）在语句内只发生一次、被主聚合与子查询**共享**，并非两份；且新结构在主扫描每行上增加了时间键计算（printf + 字符串拼接 + MAX 比较）。因此“减半”的上界不成立：节省的是“一次 60k 全扫 + 一次自动索引重建与探测 + 一次末级排序”减去“每行键计算”，实测净节省约 26–29%。进一步逼近单扫描下限需 R3（P1：candidate 排名持久化，消除每查询自动索引与窗口排序）。

## 4. 兼容性验证（全部通过）

| 命令 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `gofmt -l`（本次改动 4 文件） | 干净 |
| `go test ./internal/usage/ -count=1` | ok |
| `go test ./... -count=1` | 全部 ok |
| `go test ./internal/usage/ -race -count=1` | ok（126.7s） |
| `go test ./... -race -count=1` | 全部 ok |

- **逐字段差分**：Summary legacy oracle 差分（21 组 filter × 5 scope，含小数秒/同整秒/非法历史时戳/空数据/跨时区/最新请求时间语义）全过；`TestSummarySQLAggregationAnchorsExpectedTotals` 手工锚定值（含 `LastProviderRequest = 2026-07-30T18:00:00.75Z`）全过；Trends/Providers/Models/Coverage/Requests 的 8 组 oracle 差分未受影响全过。
- **新增结构防回归测试** `TestSummaryQueryPlanScansScopedOnce`（CI 默认执行）：对 Summary 查询执行 EXPLAIN QUERY PLAN，断言主表全扫恰 1 次、自动索引恰 1 个、无标量子查询。该测试在旧实现上为红（实测输出 2/2/1），新实现为绿，防止结构回归。

## 5. 交付物

- `internal/usage/scoped_query.go`：`scopedTimeOrderKeyExpr` + `scopedTimeOrderKeyLength`；`buildSummaryQuery` 单次扫描重写。
- `internal/usage/store.go`：`Summary` 注释同步（子查询 → MAX 聚合）。
- `internal/usage/sql_aggregate_test.go`：`TestSummaryQueryPlanScansScopedOnce`（计划形态防回归）。
- `internal/usage/ab_compare_test.go`：`TestUsageSummaryR2ABCompare` + `r2LegacyScalarSummaryQuery`（同会话 R2 前后 A/B 工具，含逐字段等价断言）。
- `explain_after_r2.txt`：R2 后六端点完整 EXPLAIN 原始输出。
- 本报告。

## 6. 后续（供协调者拆解 R3/R4）

- **R3（P1）**：candidate 排名持久化（`usage_dedupe_candidates.candidate_rank` + 索引 `(session_request_id, candidate_rank)`），消除**所有端点**每查询的自动索引与窗口临时排序——这是 Summary 从 1.37× 进一步逼近单扫描下限、以及六端点共同受益的关键路径。
- **R4（P2/P3）**：按 scope 裁剪 candidate 计算；Requests count/page 单 CTE 复用。
