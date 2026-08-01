# R1 返工报告：索引基础 + 精确性能诊断

- 分支：`perf/usage-query-optimization`
- 数据集：60,332 行确定性基准（seed=1，`modernc.org/sqlite` 纯 Go 驱动，与生产同驱动，WAL + `synchronous=NORMAL`）
- 范围约束：只加索引 + 诊断 + 测量；不改查询语义/结构；保持 6A/6B2 兼容；不改前端。
- 一句话结论：**索引基础已按 6A 验证模式幂等落地，但同会话 A/B 严格证明——仅靠索引对单接口延迟无可测量收益（0.95×–1.10×，全在噪声内）；R7 缺口的真正根因是 scoped CTE 对 60k 行 `usage_requests` 的重复全表扫描（Summary 还因标量子查询二次物化），必须靠 R2+ 查询结构重写解决。**

---

## 1. 六端点 EXPLAIN QUERY PLAN 精确诊断（优化前基线）

诊断工具：`internal/usage/explain_diagnostic_test.go`（`MCC_USAGE_EXPLAIN=1` 门控，CI 不跑）。
完整原始计划见 `explain_before.txt`。所有六端点共享 `buildScopedCTE` 的同一份开销骨架：

### 1.1 全端点共有的 scoped CTE 开销骨架

每个端点的查询计划都包含以下固定结构（即 6C2 报告所述“scoped CTE 较重”的精确分解）：

| 计划行 | 含义 | 每端点出现 |
|---|---|---|
| `MATERIALIZE candidate` + `CO-ROUTINE` | candidate CTE（含 ROW_NUMBER 窗口）每查询物化一次 | 1（Summary 为 2，见下） |
| `SCAN d USING INDEX sqlite_autoindex_usage_dedupe_candidates_1` | 全扫 5,609 行候选表（走 PK 自动索引，非覆盖，需回表取 model_priority） | 1 |
| `USE TEMP B-TREE FOR LAST 4 TERMS OF ORDER BY` | candidate 的 `ROW_NUMBER() OVER (PARTITION BY session_request_id ORDER BY model_priority, epoch, fraction, request_id)` 窗口临时排序 | 1 |
| `SEARCH candidate USING AUTOMATIC PARTIAL COVERING INDEX (session_request_id=? AND candidate_rank=?)` + `BLOOM FILTER` | **运行时自建索引**：scoped `LEFT JOIN candidate` 在“物化后的 candidate CTE 结果”上每查询重建一次 | 1（Summary 为 2） |
| filtered CTE `SCAN r`（usage_requests 60k 行）+ `SEARCH t (request_id=?)` | 对 60,332 行主表全扫并联 usage_tokens | 1（Summary 为 2） |

**关键定位：`AUTOMATIC PARTIAL COVERING INDEX` 建在物化后的 candidate CTE 临时结果上，不是基表 `usage_dedupe_candidates`。因此任何基表索引都无法消除它——其根因是 candidate 以 ROW_NUMBER CTE 形式每查询物化，属查询结构问题（R2+）。**

### 1.2 各端点瓶颈定位（优化前）

| 端点 | SCAN | TEMP B-TREE | AUTO INDEX | 子查询/CO-Routine | 瓶颈定位 |
|---|---:|---:|---:|---:|---|
| **Summary/status** | 4 | 2 | 2 | 3 | 最重：`last_provider_request` 标量子查询**整套 scoped CTE 二次物化**（第二次全扫 60k + 第二个自动索引），末级 `ORDER BY epoch DESC, fraction DESC` 为计算列排序（索引不可优化） |
| **Requests** | 6 | 2 | 2 | 4 | count + page 各含一份完整 CTE（I2）；page 末级 `ORDER BY started_at DESC, id DESC` **已由 `idx_usage_requests_started_id` 消除**（计划无末级 TEMP B-TREE） |
| **Trends** | 6 | 3 | 2 | 4 | range + bucket 两次查询（L1）；bucket 额外 `TEMP B-TREE FOR GROUP BY`（按计算日期桶分组，索引不可优化） |
| **Providers** | 5 | 4 | 1 | 6 | ROW_NUMBER 取“首行”维度（`LAST 2 TERMS` 窗口排序）+ `GROUP BY` + 末级 `ORDER BY total_requests DESC, group_key ASC`（聚合输出，索引不可优化）；主扫走 `idx_usage_requests_provider` |
| **Models** | 5 | 4 | 1 | 6 | 同 Providers，主扫走 `idx_usage_requests_model` |
| **Coverage** | 8 | 5 | 2 | 8 | 最重并列：summary（ROW_NUMBER 4 项窗口 + GROUP BY，主扫退化为 PK 全扫 `sqlite_autoindex_usage_requests_1`）+ status（GROUP BY）两条查询 |

**核心结论：六端点延迟由“每端点重复全扫 60k 行 `usage_requests` + 每查询物化 candidate + 每查询重建自动索引”主导。可被基表索引优化的只有 Requests 末级排序（已优化）与 candidate 表扫描本身（5,609 行，占比极小）。**

---

## 2. 新增索引清单

迁移文件：`internal/usage/migrate_indexes.go`，由 `store.go` 的 `Migrate()` 在 `migrateDedupeCandidates()` 之后调用。完全复现 6A 已验证保证：**单事务原子提交 + `CREATE INDEX IF NOT EXISTS` + settings 标记 `usage_query_indexes_v1` + 标记存在即重开跳过**。

| 索引 | 定义 | 目标 | EXPLAIN 实测效果 |
|---|---|---|---|
| `idx_usage_dedupe_candidates_session_priority` | `usage_dedupe_candidates(session_request_id, model_priority, provider_request_id)` | 任务 3：candidate CTE 覆盖索引（含其从 d 读取的全部列），按 PARTITION BY 键 + 首个 ORDER BY 项预排序 | `SCAN d` 由 PK 自动索引升级为 **COVERING INDEX**（免回表）；窗口临时排序由 **LAST 4 TERMS → LAST 3 TERMS** |

### 关于任务 2（末级排序索引）——已存在，未重复新增

任务 2 要求“按实际 ORDER BY（started_at DESC, id DESC）新增复合索引（如 `usage_requests(started_at, id)`）”。**经 EXPLAIN 核验，该索引已由 6A 候选表迁移引入：`idx_usage_requests_started_id ON usage_requests(started_at DESC, id DESC)`**（`dedupe.go`）。`requests-page` 计划显示 `SCAN r USING INDEX idx_usage_requests_started_id` 且**无末级 `USE TEMP B-TREE FOR ORDER BY`**，证明 Requests 末级排序的 TEMP B-TREE 已被消除。其余端点的末级/分组排序均作用于计算列（epoch/fraction/日期桶）或聚合输出（total_requests），任何 `usage_requests` 索引都无法优化。故**不再重复新增 ASC 复合索引**，以免在零读取收益下拖累写入（本任务低风险原则）。

### 关于任务 3 目标的诚实结论——自动索引无法由基表索引消除

任务 3 的预设目标是“消除 candidate 每查询自动索引”。**EXPLAIN 严格证明该目标不可由基表索引达成**：自动索引建在物化后的 candidate CTE 临时结果上（`SEARCH candidate USING AUTOMATIC ...`，优化后仍 10 处全部保留），基表索引触及不到。新增的覆盖索引是该访问模式下语义正确的持久索引（覆盖 + 预排序），但**不消除自动索引**。消除自动索引需将 candidate 排名持久化/索引化或改写为相关子查询，属 R2+ 查询结构重写。

---

## 3. 基准前后对比

### 3.1 方法学校准（重要）：跨次对比不可信，必须同会话 A/B

本机为共享机器，墙钟负载波动显著。证据：

- 同一“含索引”配置在同一个 A/B 会话内，Summary 首样本 713ms vs 末样本 568ms（±25% 漂移，纯 Go SQLite 冷启动 + 机器负载）。
- **本会话“无索引”（与 6C2 完全相同的代码）实测已优于 6C2 文档基线**：Summary 572 vs 604、Providers 609 vs 942、Coverage 1158 vs 1534、六并发 4201 vs 5885。代码未变而数字更好，唯一解释是 6C2 基线测量时机器负载更重。

**因此“R1 后 vs 6C2 基线”的表观提升大部分是负载漂移，不能归因于索引。** 唯一可信的归因来自同会话 A/B（`ab_compare_test.go`，`MCC_USAGE_AB=1` 门控）：在同一进程、同一数据集上交替测量含/不含索引并丢弃预热。

### 3.2 表观对比（vs 6C2 文档基线，仅供参照，含负载漂移）

| 指标 | 6C2 基线 | R1 后（3 次中位） | 表观变化 | 可信归因 |
|---|---:|---:|---:|---|
| 迁移（含回填，5,609 候选） | 367 ms | 362 ms | ≈持平 | 新增索引建立开销被噪声淹没，仍远低于 2s 目标 |
| Summary/status | 604 ms | 534 ms | 1.13× | 主要是负载漂移（见 3.3） |
| Requests p50 | 361 ms | 264 ms | 1.37× | 主要是负载漂移 |
| Trends | 705 ms | 671 ms | 1.05× | 噪声 |
| Providers | 942 ms | 574 ms | 1.64× | 主要是负载漂移 |
| Models | 687 ms | 653 ms | 1.05× | 噪声 |
| Coverage | 1,534 ms | 1,082 ms | 1.42× | 主要是负载漂移 |
| 六并发墙钟 | 5,885 ms | 3,503 ms | 1.68× | 主要是负载漂移 |

### 3.3 严格同会话 A/B（含索引 vs 无索引，丢弃预热，各取 2 样本中位）

| 指标 | 含索引 | 无索引 | 加速比 | 判定 |
|---|---:|---:|---:|---|
| Summary/status | 545 ms | 572 ms | 1.05× | 噪声内 |
| Requests p50 | 261 ms | 252 ms | 0.97× | 噪声内 |
| Trends | 659 ms | 658 ms | 1.00× | 无变化 |
| Providers | 642 ms | 609 ms | 0.95× | 噪声内 |
| Models | 651 ms | 641 ms | 0.98× | 噪声内 |
| Coverage | 1.055 s | 1.158 s | 1.10× | 噪声内 |
| 六并发墙钟 | 3.026 s | 4.201 s | 1.39× | 不稳定（另一次 A/B 为 0.94×，六并发对负载极敏感） |

**严格结论：新增覆盖索引对单接口延迟无可测量收益（全部落在 0.95×–1.10× 噪声带）；六并发表观提升在两次 A/B 间方向相反（1.39× / 0.94×），不可归因于索引。索引基础是正确且无害的，但不足以撬动 R7。**

---

## 4. 兼容性验证（全部通过）

| 命令 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./internal/usage/` | 通过 |
| `go test ./internal/usage/ -count=1` | ok（0 个 FAIL） |
| `go test ./... -count=1` | 全部 ok |
| `go test ./internal/usage/ -run 'TestUsageQueryIndexes\|TestDedupeMigration' -race` | ok（无数据竞争） |

- **逐字段兼容**：8 个 legacy oracle 差分测试全过（Summary/Trends/Providers/Models/Coverage/Requests/ScopedQuery 跨 filter/scope/timezone），证明加索引未触碰查询语义。
- **迁移幂等/重开/原子性**：新增 3 个测试（`migrate_indexes_test.go`）镜像 6A 验证模式——
  - `TestUsageQueryIndexesMigrationCreatesIndexAndMarker`：首次 Migrate 建索引 + 标记。
  - `TestUsageQueryIndexesMigrationIdempotentReopen`：重复 Migrate 幂等；标记存在时重开跳过（删索引后不再重建，复现 6A 标记防重跑语义）。
  - `TestUsageQueryIndexesMigrationRollsBackIndexAndMarkerTogether`：标记写入失败时索引与标记一并回滚，移除故障后重试成功。

---

## 5. 针对剩余回归与 R7 冲刺的优先级优化建议（供协调者拆解 R2+）

按“收益/风险”排序。所有项均需**查询结构重写**，超出 R1“只加索引”范围；建议用本报告的同会话 A/B 工具（`ab_compare_test.go`）逐项测量归因。

### P0 — Summary 标量子查询二次物化（最高收益，风险中）
- **问题**：`buildSummaryQuery` 的 `last_provider_request` 用标量子查询 `SELECT r2.started_at FROM scoped sc2 JOIN ... ORDER BY epoch DESC, fraction DESC LIMIT 1`，使整套 scoped CTE（含 60k 全扫 + candidate 物化 + 自动索引）**执行两次**。这是 Summary 为原始聚合下限 4.4× 的主因。
- **建议**：将“最新 started_at”改为与主聚合**共享同一次 scoped 扫描**——在主聚合查询里用 `MAX(...)` 配合 epoch/fraction 编码取最晚行，或拆为一次 CTE + 两个下游聚合（同一物化复用）。预期 Summary 接近单次扫描成本（≈减半）。

### P1 — candidate 排名持久化，消除每查询自动索引与窗口排序（高收益，风险中高）
- **问题**：candidate 以 ROW_NUMBER CTE 每查询物化 → 每查询重建自动索引 + 窗口临时排序。基表索引无法消除（已证）。
- **建议**：在 `usage_dedupe_candidates` 增列持久化 `candidate_rank`（写入/回填时维护，索引 `(session_request_id, candidate_rank)`），scoped 直接 `JOIN ... ON candidate_rank=1`，免去窗口与自动索引。或改写为相关子查询走持久索引。需配套增量维护与回填迁移（可复用 6A 原子迁移模式）。

### P2 — 按 scope 裁剪 candidate 计算（中收益，风险低中，对应 6A I2）
- **问题**：provider/session_log/raw scope 下去重对过滤无作用，但仍计算 candidate；聚合接口不投影 dedupe_status 但仍 LEFT JOIN candidate。
- **建议**：`buildScopedCTE` 增加 scope 参数，仅在 effective/raw（需标记）或确需去重的 scope 构建 candidate CTE；provider/session_log scope 直接跳过 candidate。六端点多数调用可省去整段 candidate 物化。

### P3 — 减少 filtered 重复全扫 / Requests 双 CTE（中收益，风险低中，对应 6A I2/L2）
- **问题**：filtered CTE 对 60k 行全扫在每个端点发生一次（Summary 两次）；Requests 的 count 与 page 各含一份完整 CTE。
- **建议**：Requests 将 count 与 page 合并为单 CTE 复用（`COUNT(*) OVER ()` 或同一只读事务两查询共享物化）；聚合接口评估是否可用同一物化 scoped 喂多个下游。配合 P2 收益叠加。

### P4 — Coverage 主扫退化为 PK 全扫（低中收益，风险低）
- **问题**：`coverage-summary` 主扫走 `sqlite_autoindex_usage_requests_1`（PK 全扫）而非二级索引；status 查询走 `idx_usage_requests_model`（与分组键不完全匹配）。
- **建议**：评估为 Coverage 分组键 `(provider_name, provider_api_url, mapped_model, source_entrypoint)` 建复合索引是否能让 GROUP BY 走索引有序聚合（消除 `TEMP B-TREE FOR GROUP BY`）。需用 A/B 验证（无过滤全量场景下全扫可能仍最优）。

### 关于 Coverage 0.65× 与六并发 0.82× 回归（相对优化前）
- 二者同因：scoped CTE 比旧“全量加载 + Go 汇总”更重（多次全扫 + 物化 + 窗口 + 自动索引），并发下相互争用 CPU 放大回归。
- **P0+P1+P2 组合是消除回归的关键路径**：P0 砍掉 Summary 二次物化、P1 砍掉每查询自动索引/窗口、P2 让多数 scope 免算 candidate。三者叠加后 scoped 单查询成本有望逼近原始聚合下限（本机 ≈145ms），六并发争用随之下降。
- 仅靠 R1 索引基础**不能**消除这两个回归（已严格证明）。

### R7 达标路径判断
- R7 目标（status ≤100ms、六并发 ≤300ms）相对本机原始聚合下限（145ms）意味着必须做到“单次扫描 + 无每查询物化/自动索引”。这要求 P0+P1（结构重写），可能还需 P2/P3。
- 建议 R2 先做 P0（Summary，单点最高收益、可独立 A/B 验证），R3 做 P1（candidate 持久化，消除自动索引），R4 做 P2/P3（scope 裁剪 + CTE 复用），每步用同会话 A/B 测量并在生产 3 倍速折算下评估 R7 可达性。

---

## 6. 交付物清单

- `internal/usage/migrate_indexes.go`：R1 索引迁移（幂等/原子/标记，6A 模式）。
- `internal/usage/migrate_indexes_test.go`：迁移幂等/重开/原子回滚测试（3 个）。
- `internal/usage/explain_diagnostic_test.go`：六端点 EXPLAIN 诊断工具（`MCC_USAGE_EXPLAIN=1`）。
- `internal/usage/ab_compare_test.go`：同会话含/不含索引 A/B 对照工具（`MCC_USAGE_AB=1`）。
- `internal/usage/store.go`：`Migrate()` 接入新索引迁移。
- `explain_before.txt`：优化前六端点完整 EXPLAIN 原始输出。
- 本报告 `R1-index-diagnostics.md`。
