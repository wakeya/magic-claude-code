# R3 返工报告（P1）：candidate 排名持久化，消除每查询自动索引与窗口排序

- 分支：`perf/usage-query-optimization`
- 依据：R1 诊断报告 §1.1 / §5-P1 + R2 报告 §6。
- 数据集：60,332 行确定性基准（seed=1，`modernc.org/sqlite` 纯 Go 驱动，WAL + `synchronous=NORMAL`）。
- 范围约束：只改 candidate 选取结构（`buildScopedCTE`）+ 持久化排名 schema/迁移/写入维护；Trends/Providers/Models/Coverage/Requests/Summary 的下游投影/聚合/排序未动；不改前端；保持 6A/6B2 逐字段兼容（尤其 candidate 去重语义与 fallback）。
- 一句话结论：**candidate 选取从“ROW_NUMBER CTE 每查询物化 + 运行时自动索引 + 窗口临时排序”改为“排名持久化于 `usage_dedupe_candidates.candidate_rank`（写入/回填原子维护，索引 `(session_request_id, candidate_rank)`）+ scoped 直接 JOIN 基表走持久索引 + `MIN(candidate_rank)` 相关子查询保留过滤后 fallback”。EXPLAIN 严格证实六端点计划中 AUTOMATIC INDEX 与 candidate 窗口 TEMP B-TREE（LAST 3 TERMS）全部消失、LEFT JOIN 改走持久索引；同会话 A/B 三次独立会话六端点逐字段全等价，Requests 稳定加速 ~1.4–1.5×，其余端点 candidate 开销占比小、收益落在共享机器噪声带；全部差分/回归/race/vet 验证通过。**

---

## 1. 问题（R1 §1.1 / §5-P1）

R1 已严格证明：每个端点的查询计划都含一段固定的 candidate 开销骨架——

```
MATERIALIZE candidate
CO-ROUTINE (subquery-N)
SCAN d USING COVERING INDEX idx_usage_dedupe_candidates_session_priority
USE TEMP B-TREE FOR LAST 3 TERMS OF ORDER BY        ← candidate ROW_NUMBER 窗口临时排序
BLOOM FILTER ON candidate (session_request_id=? AND candidate_rank=?)
SEARCH candidate USING AUTOMATIC PARTIAL COVERING INDEX (...) LEFT-JOIN   ← 运行时自建索引
```

根因：candidate 以 `ROW_NUMBER() OVER (PARTITION BY session_request_id ORDER BY model_priority, epoch, fraction, request_id)` 形式**每查询物化**，`scoped LEFT JOIN candidate ON candidate_rank=1` 在“物化后的 CTE 临时结果”上每查询重建自动索引。R1 证明**任何基表索引都无法消除它**（自动索引建在物化结果上，非基表），属查询结构问题，须持久化排名或改写相关子查询。

### 关键语义约束：candidate 选取是“过滤相关”的

`candidate` CTE 同时 `JOIN filtered session`（is_dedupe_session=1）与 `JOIN filtered provider`（is_provider_usage=1），即候选只在“会话行与 provider 行都通过用户筛选”时参与排名。差分测试**显式锁定**了由此产生的“过滤后 fallback”行为：

- `TestScopedQueryCandidatePriorityFallbackAndNoCandidate`：“filtered primary falls back to current candidate”（`From` 过滤排除最优 provider 后回退到次优）、“session without filtered provider has no marker”（provider 全被过滤则不打标）、“effective scope keeps session when candidate is filtered out”。
- `TestScopedQueryMatchesLegacyOracleAcrossFiltersAndScopes`：17 组 filter × 5 scope 逐字段对照 legacy oracle（oracle 先过滤再在过滤集内选最优候选）。

**因此不能简单 `JOIN ... ON candidate_rank=1`（全局最优）——必须保留“过滤后候选集中的最优”。** 这是 R3 的核心设计难点。

## 2. 修复：持久化稠密排名 + 持久索引 + MIN 相关子查询

### 2.1 持久化排名（schema + 写入 + 回填）

`usage_dedupe_candidates` 增列 `candidate_rank INTEGER NOT NULL DEFAULT 0`，存“该 session 全部候选中的稠密名次（1..N）”，排序与旧 ROW_NUMBER 窗口**逐字一致**：

```
model_priority ASC,
epoch(provider.started_at) ASC,        -- 整秒，非法时戳容错为 Go 零值
fraction(provider.started_at) ASC,     -- 9 位小数秒
provider_request_id ASC
```

该排序由单一 helper `candidateRankOrderExpr(candidateAlias, providerAlias)` 生成，**写入重排、历史回填、（旧）查询窗口三处共用**，保证“model_priority 优先、最早 provider 候选选择”语义在写入/回填/读取完全一致（含非法历史时戳容错为零值、同整秒按小数秒与 request_id 决胜）。

- **写入路径**（`maintainDedupeCandidatesTx`，被 `Record`/`recordIfAbsent` 真实事务调用）：候选插入/`ON CONFLICT` 下调 priority 后，收集本次触及的 session 集合，逐个 `rerankCandidateSessionTx` 重排稠密名次。每 session 候选数极少（±10 分钟同指纹窗口），重排开销可忽略；**与 usage 写入同事务，失败一并回滚**。
- **历史回填**（`backfillCandidateRankTx`）：单条 `UPDATE ... FROM (ROW_NUMBER() OVER (PARTITION BY session_request_id ORDER BY <rank>))` 批量赋名次。

### 2.2 迁移（复用 6A/R1 原子幂等可重开模式）

`migrateCandidateRank()`（`migrate_candidate_rank.go`），由 `Migrate()` 在 `migrateDedupeCandidates` 之后调用：

- 单事务；settings 标记 `usage_candidate_rank_v1`；标记存在即空提交返回（重开跳过）。
- 否则依次：`ensureCandidateRankColumnTx`（PRAGMA 探测，旧库 `ALTER TABLE ADD COLUMN`，新库已由 CREATE TABLE 引入）→ `backfillCandidateRankTx`（全表回填）→ `CREATE INDEX IF NOT EXISTS idx_usage_dedupe_candidates_session_rank ON usage_dedupe_candidates(session_request_id, candidate_rank)` → 写标记 → Commit。
- 任一步失败整体回滚、不写标记、下次启动安全重跑（列/索引/标记原子）。

### 2.3 查询重写（`buildScopedCTE`）

删除 `candidate` ROW_NUMBER CTE，`scoped` 直接 LEFT JOIN 基表，用持久索引 + MIN 相关子查询取“过滤后最优候选”：

```sql
scoped AS (
	SELECT filtered.request_id, filtered.started_at, filtered.is_session_log,
		CASE WHEN d.provider_request_id IS NOT NULL THEN 'duplicate' ELSE '' END AS dedupe_status,
		COALESCE(d.provider_request_id, '') AS dedupe_request_id
	FROM filtered
	LEFT JOIN usage_dedupe_candidates d
		ON d.session_request_id = filtered.request_id
		AND filtered.is_dedupe_session = 1
		AND d.candidate_rank = (
			SELECT MIN(d2.candidate_rank)
			FROM usage_dedupe_candidates d2
			JOIN filtered provider2
				ON provider2.request_id = d2.provider_request_id
				AND provider2.is_provider_usage = 1
			WHERE d2.session_request_id = filtered.request_id
		)
	WHERE CASE ? WHEN 'raw' THEN 1 ... ELSE (filtered.is_session_log = 0 OR d.provider_request_id IS NULL) END
)
```

- **持久 rank 是全局稠密序**；“过滤后最优” = 过滤后候选中 `MIN(candidate_rank)`。因名次按 session 唯一，外层 `d.candidate_rank = (MIN ...)` 恰选出过滤后最优那一行，与旧“过滤集内 ROW_NUMBER=1”逐字段等价。
- LEFT JOIN 条件 `(session_request_id=?, candidate_rank=?)` 完整命中持久索引 `(session_request_id, candidate_rank)` → SQLite 直接 `SEARCH d USING INDEX idx_usage_dedupe_candidates_session_rank`，**不再物化 candidate CTE、不再建自动索引、不再窗口排序**。MIN 子查询同样走该持久索引（session_request_id 前缀）。

## 3. EXPLAIN 前后对比（六端点，结构与行数无关；3000 行实测，60,332 行同构）

诊断工具 `explain_diagnostic_test.go`（`MCC_USAGE_EXPLAIN=1`）。原始输出：`explain_before_r3.txt`（R2 态）、`explain_after_r3.txt`（R3 态）。

| 端点 | SCAN 前→后 | TEMP B-TREE 前→后 | **AUTO INDEX 前→后** | SUBQUERY/CO-Routine 前→后 |
|---|---:|---:|---:|---:|
| Summary/status | 3 → **1** | 1 → **0** | 1 → **0** | 2 → 1 |
| Requests | 6 → **2** | 2 → **0** | 2 → **0** | 4 → 2 |
| Trends | 6 → **2** | 3 → **1** | 2 → **0** | 4 → 2 |
| Providers | 5 → **3** | 4 → **3** | 1 → **0** | 6 → 5 |
| Models | 5 → **3** | 4 → **3** | 1 → **0** | 6 → 5 |
| Coverage | 8 → **4** | 5 → **3** | 2 → **0** | 8 → 6 |

- **AUTOMATIC INDEX：六端点全部 1–2 → 0**（运行时自动索引彻底消除）。
- **candidate 窗口 TEMP B-TREE（`LAST 3 TERMS`）：六端点全部消失**。
- LEFT JOIN 候选改走持久索引：每条查询出现 `SEARCH d USING INDEX idx_usage_dedupe_candidates_session_rank (session_request_id=? AND candidate_rank=?) LEFT-JOIN`，MIN 子查询 `SEARCH d2 USING INDEX idx_usage_dedupe_candidates_session_rank (session_request_id=?)`。
- 剩余 TEMP B-TREE 均为**合法的非 candidate 排序**，R1 已注明索引不可优化：Trends 的 `GROUP BY` 日期桶；Providers/Models 的 R5 聚合“首行”窗口（`LAST 2 TERMS`，非 candidate 窗口）+ `GROUP BY` + 末级 `ORDER BY total_requests`；Coverage 的 `GROUP BY`/`ORDER BY`。
- SCAN 大幅下降（Summary 3→1、Requests 6→2、Coverage 8→4）：candidate CTE 物化消除后，filtered 不再被 candidate 的 session/provider 双 JOIN 额外消费。

### CI 防回归 EXPLAIN 断言（新增，CI 默认执行）

- `TestUsageQueryPlansEliminateCandidateWindowAndAutoIndex`：对六端点当前 SQL 执行 EXPLAIN QUERY PLAN，断言**无 AUTOMATIC INDEX、无 candidate 窗口（LAST 3 TERMS）、且走持久索引**；任一端点回归即红。
- `TestSummaryQueryPlanScansScopedOnce`（R2 守卫，R3 升级）：Summary 仍单次扫描 usage_requests（`SCAN r` 恰 1，保 R2 P0），且自动索引 0、无 candidate 窗口、走持久索引。
- `TestBuildScopedCTEParameterizesFiltersInStableOrder` / 各端点结构断言：更新为断言新的持久化 rank JOIN 结构（`LEFT JOIN usage_dedupe_candidates d` + `MIN(d2.candidate_rank)`），并显式断言 ROW_NUMBER candidate 窗口不再出现。

## 4. 同会话 A/B 测量（`ab_compare_test.go` 新增 `TestUsageR3CandidateRankABCompare`，MCC_USAGE_AB=1，60,332 行 seed=1）

方法：同一进程、同一数据集上交替测量 R3 新结构与 test-only 重建的 R3 前旧结构（`r3LegacyScopedCTE` 逐字复现旧 ROW_NUMBER candidate CTE）；**先逐字段断言六端点全部子查询结果完全一致**，再交替 3 轮（轮内顺序交替抵消趋势漂移）、丢弃预热、每样本 5 次运行中位。绝对值随共享机器负载漂移，仅同会话比值可归因；共运行 3 次独立会话（另附 1 次 runs=3 预运行）。

### 4.1 逐字段等价（三次会话均通过）

三次独立会话均在 60,332 行上断言六端点（Summary/Requests count+page/Trends range+bucket/Providers/Models/Coverage summary+status）新旧查询结果**完全相同**（all six endpoints IDENTICAL）——差分兼容的附加实数据证明。

### 4.2 加速比（三次独立会话，中位）

| 端点 | 会话#1 | 会话#2 | 会话#3 | 判定 |
|---|---:|---:|---:|---|
| **Requests** | **1.65×** | **1.50×** | **1.58×** | **稳定加速（另 runs=3 预运行 1.48×）** |
| Summary/status | 1.08× | 2.62×（旧配置遭 1.45–1.72s 负载尖刺污染） | 1.15× | 清洁会话 ~1.08–1.15× |
| Trends | 1.08× | 1.14× | 0.99× | 噪声内 |
| Providers | 1.36× | 0.95× | 1.01× | 噪声内（双向尖刺） |
| Models | 0.92× | 2.11×（旧配置 2.0s 尖刺） | 1.01× | 清洁样本无变化（#3 新[654,680,635]ms vs 旧[658,714,658]ms，极紧） |
| Coverage | 1.01× | 1.13× | 0.49×（新配置 2.66–3.94s 尖刺） | 噪声内（不可归因） |
| 六并发墙钟 | 0.81× | 1.09× | 0.96× | 负载敏感、方向不稳定（与 R1/R2 结论一致，仅定性） |

### 4.3 严格结论

- **Requests 是唯一稳定可归因的加速点：四次会话 1.48×–1.65×，样本极紧**（如 #3 新[190,704,173]ms vs 旧[300,1409,261]ms，中位新 190 vs 旧 300）。原因：Requests 含 count+page 两条查询、各带一份 candidate 开销，且其总延迟最低（~180–300ms），candidate 物化/自动索引/窗口占比最高，故 R3 消除后相对收益最大。
- **Summary 清洁会话 ~1.08–1.15×**（#2 的 2.62× 由旧配置负载尖刺抬高，不可信）。
- **Trends/Providers/Models/Coverage 与六并发落在共享机器噪声带**：这些端点总延迟由 60k 行 `usage_requests` 全扫主导（R1 已证），candidate 开销（5,609 行窗口 + 自动索引）是 small fixed cost，消除后的净节省被负载漂移淹没；Models 最紧样本（#3）显示新旧几乎无差（~1.0×），印证“candidate 占比极小”。六并发对负载极敏感，三次会话方向不一（0.81×/1.09×/0.96×），与 R1“六并发两次 A/B 方向相反”、R2“#2 遭外部尖刺污染”的结论一致，只能定性。
- **本次测量的主要价值在结构而非墙钟**：R3 的收益是“每查询恒定的 candidate 物化 + 自动索引重建 + 窗口排序”被彻底消除（EXPLAIN 已证，见 §3），其墙钟收益在 candidate 占比高的 Requests 上稳定显现（~1.5×），在 60k 全扫主导的其余端点上被噪声掩盖。进一步墙钟收益需 R4（P2 scope 裁剪 + P3 CTE 复用）削减 60k 全扫这一主导项。

## 5. 兼容性验证（全部通过）

| 命令 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `gofmt -l`（本次改动文件） | 干净 |
| `go test ./internal/usage/ -count=1` | ok |
| `go test ./... -count=1` | 全部 ok |
| `go test ./internal/usage/ -race -count=1` | ok |
| `go test ./... -race -count=1` | 全部 ok |

- **逐字段差分（candidate 去重语义 + fallback）**：`TestScopedQueryMatchesLegacyOracleAcrossFiltersAndScopes`（17 filter × 5 scope）、`TestScopedQueryCandidatePriorityFallbackAndNoCandidate`（多候选/优先级回退/过滤后无标记/effective 保留）、`TestScopedQueryOrdersRFC3339NanoChronologically`（同整秒小数秒时序）、`TestScopedQueryTreatsInvalidHistoricalTimeAsGoZeroTime`（非法时戳容错）全过。
- **六端点 legacy oracle 差分**：Summary/Trends/Providers/Models/Coverage/Requests/ScopedQuery（含多候选、两种插入顺序、无匹配、session 先到、provider 先到、重复写入冲突）逐字段全过。
- **候选写入与 usage 写入同事务、失败回滚**：`TestDedupeIncrementalRollsBackUsageWhenCandidateWriteFails` 等全过；新增 `TestCandidateRankIncrementalMaintainsDenseRank`（两种插入顺序稠密 rank 一致）、`TestCandidateRankIncrementalReranksOnPriorityDowngrade`（priority 下调后重排）。
- **迁移幂等/重开/原子**：新增 `migrate_candidate_rank_test.go` 镜像 6A/R1——建列+索引+标记、升级库（旧 schema 无列）回填正确 rank、幂等重开（标记后删索引不重建）、标记失败列/索引/标记一并回滚后重试成功。
- **A/B 逐字段等价**：三次会话均在 60,332 行上断言六端点新旧查询结果完全相同。

## 6. 交付物

- `internal/usage/dedupe.go`：`candidate_rank` 列（CREATE TABLE）；`candidateRankOrderExpr` / `rerankCandidateSessionTx` / `backfillCandidateRankTx`；`maintainDedupeCandidatesTx` 增量重排。
- `internal/usage/migrate_candidate_rank.go`：R3 迁移（原子/幂等/可重开：补列 + 回填 + 持久索引 + 标记）。
- `internal/usage/migrate_candidate_rank_test.go`：迁移与增量排名测试（6 个）。
- `internal/usage/scoped_query.go`：`buildScopedCTE` 持久化 rank JOIN 重写。
- `internal/usage/store.go`：`Migrate()` 接入 `migrateCandidateRank`。
- `internal/usage/explain_diagnostic_test.go`：`TestUsageQueryPlansEliminateCandidateWindowAndAutoIndex`（CI 防回归）。
- `internal/usage/sql_aggregate_test.go` / `scoped_query_test.go`：结构断言更新 + R2 守卫升级。
- `internal/usage/ab_compare_test.go`：`TestUsageR3CandidateRankABCompare` + `r3LegacyScopedCTE`（同会话 R3 前后 A/B，含六端点逐字段等价）。
- `internal/usage/performance_test.go`：基准库重置同步移除 candidate_rank 标记（模拟生产升级路径）。
- `explain_before_r3.txt` / `explain_after_r3.txt`：六端点 EXPLAIN 原始输出。
- 本报告。

## 7. 后续（供协调者拆解 R4+）

- **R4（P2/P3）**：按 scope 裁剪 candidate 计算（provider/session_log/raw scope 无需去重标记时跳过 candidate JOIN）；Requests count/page 单 CTE 复用。
- R1 覆盖索引 `idx_usage_dedupe_candidates_session_priority` 在 R3 后已不被读取路径使用（candidate 访问改走 `session_rank`），保留以兼容 R1 迁移测试、对 5,609 行表写入开销可忽略；如需精简写入可在后续独立评估移除。
- 进一步逼近单扫描下限仍需削减 filtered 的 60k 全扫（六端点延迟主导项）：P2 scope 裁剪 + P3 CTE 复用是关键路径。
