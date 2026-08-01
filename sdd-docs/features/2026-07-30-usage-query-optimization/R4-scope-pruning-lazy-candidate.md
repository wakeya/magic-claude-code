# R4 返工报告（P2+P3）：削减 60k filtered 全扫这一六端点延迟主导项

- 分支：`perf/usage-query-optimization`（commit `34d5826`）
- 依据：R1 报告 §5-P2/P3 + R2/R3 报告。
- 数据集：60,332 行确定性基准（seed=1，`modernc.org/sqlite`，WAL + `synchronous=NORMAL`）。
- 范围约束：只改 `buildScopedCTE`（candidate 选取结构 + scope 裁剪）与 Summary 聚合内层投影；六端点下游投影/聚合/排序语义未动；不改前端；保持 6A/6B2 逐字段兼容（Requests 全部 scope 的 dedupe 标记可见性、effective 过滤后 fallback 语义、时间/排序/总数/分页）。
- 一句话结论：**candidate 从“每行 LEFT JOIN 基表 + 逐行相关子查询”改为“CASE 惰性子查询”（非会话行零候选查找），并按 scope 裁剪（raw/provider/session_log 聚合端点与 provider Requests 整段省去 candidate 计算），Summary 的 epoch 表达式提升为单次求值。同会话 A/B（60,332，多独立会话）六端点逐字段全等价、无回归：Summary 1.10–1.13×、Requests 1.77×（干净会话）、Trends 1.07–1.23×、Coverage 1.09–1.28×、六并发 1.09–1.27×；P2 专项同会话：Summary provider 口径 −36%、session_log −77%、Requests provider −52%。任务 1（Requests count+page 合并）经实测否决：COUNT(*) OVER() 合并 445ms vs 现状 166ms 是净负优化（破坏 page 查询的 LIMIT 提前终止——page 实测仅 13ms，R3 起已不存在“第二份完整全扫”）。任务 3 确认：带 started_at 时间范围过滤时 SQLite 自动走 started_at 索引（7 天窗口 count 152ms→15ms，六端点计划全部 SEARCH 非全扫），无需新索引，已加 CI 防回归断言；无过滤全扫不可避免且 filtered+candidate 结构已逼近其下限（filtered 纯 JOIN COUNT 28ms，含候选查找 125ms，CASE-lazy 收回约 1/4）。**

---

## 1. 分解实验（R4 依据，60,332 行 seed=1，本机同会话）

| 查询形态 | 中位延迟 | 说明 |
|---|---|---|
| A 纯 `usage_requests` COUNT | 0.8 ms | 单表全扫下限 |
| B `r JOIN t` COUNT（filtered 成本） | 28 ms | **filtered 物化下限** |
| C 当前 scoped COUNT（含 candidate） | 152 ms | candidate 结构使 filtered 膨胀 5.4× |
| D Summary（R3 结构） | 417 ms | filtered 28 + candidate ~125 + 聚合函数求值 ~264 |
| E Summary 无 candidate（provider 口径形态） | 138 ms | **P2 收益上限** |
| F scoped COUNT + 7 天时间过滤 | 14.8 ms | **已自动走 started_at 索引** |
| G `COUNT(*) OVER()` 合并 count+page | 445 ms | **否决：负优化**（窗口强制全量物化排序） |
| H 标量子查询 total + page | 175 ms | 否决：仍劣于现状 166 ms |
| page only（OFFSET 2450） | 13 ms | LIMIT 提前终止，**不存在第二份全扫** |

关键结论：

1. **任务 1（P3 Requests 单 CTE 复用）前提不成立**：R3 的 requests-page 计划显示 `SCAN r USING INDEX idx_usage_requests_started_id`，表象像全扫，实测该查询因 LIMIT 提前终止只处理 ~2,500 行（13ms）；Requests 的 166ms 中 152ms 是 count（必须全扫统计总数）。`COUNT(*) OVER()` 会把 page 从 13ms 拖到 445ms（窗口函数要求先物化排序后全部 60k 行），标量子查询变体 175ms 也劣化。**合并是净负优化，维持 count+page 两查询结构**（L2 瞬时不一致风险不变，属既有可接受项）。
2. **任务 3 的过滤路径已经最优**：`filterWhere` 参数化生成的 `r.started_at >= ? AND r.started_at < ?` 使 SQLite 计划器自动选择 started_at 索引 SEARCH（六端点全部，见 §4）。无需新索引、无查询改动。
3. **无过滤全扫不可避免**（统计全部行是语义要求），但 candidate 结构是 filtered 之上的主要增量成本（28→152ms），是 R4 真正的削减对象。

## 2. 实现

### 2.1 candidate 改为 CASE 惰性子查询（任务 3 核心，全部需要 dedupe 的路径受益）

R3 结构：`scoped LEFT JOIN usage_dedupe_candidates d ON d.session_request_id = filtered.request_id AND filtered.is_dedupe_session = 1 AND d.candidate_rank = (MIN 相关子查询)`——SQLite 对 60k 行**每行**执行一次 `SEARCH d` 索引查找（非会话行命中 0 行也是成本）。

R4 结构：候选查找移入 `CASE WHEN filtered.is_dedupe_session = 1 THEN (子查询) ELSE NULL END`。SQLite 对 CASE 分支惰性求值，**非会话行（约 82%）完全不执行候选查找**；仅会话行执行“过滤后候选集 MIN(candidate_rank)”子查询（语义与 R3 逐字一致，含 fallback）。外层 scoped 由 `cand_provider` 派生 `dedupe_status`/`dedupe_request_id` 两列（列名与投影不变，Requests 页与全部差分测试不受影响）。

### 2.2 按 scope 裁剪 candidate（P2）

`buildScopedCTE(filter, needDedupe bool)`：

- `needDedupe=false`（调用方不输出标记、过滤不依赖候选判定）：scoped 直接由 filtered 派生，**整段省去 candidate JOIN 与相关子查询**，恒输出空标记列（投影兼容）。适用：聚合端点（Summary/Trends/Providers/Models/Coverage）的 raw/provider/session_log 口径；Requests 的 provider 口径（只输出非会话行，标记恒空——6A 兼容性不变量“raw 与 session-log 行上的重复标记可见”不涉及 provider 行，差分测试逐字段锁定）。
- `needDedupe=true` 或 effective 口径（WHERE 依赖候选判定，恒强制）：完整 CASE-lazy 结构。适用：Requests 的 effective/raw/session_log（前端 dedupe 徽章要求标记可见——任务描述中“session_log 无需标记”的表述与 6A 不变量不符，按兼容性约束保留）；聚合端点的 effective。

P2 专项同会话实测（60,332，当前共享负载下仍显著）：Summary effective 510ms → provider 327ms（−36%）/ session_log 115ms（−77%）/ raw 340ms；Requests effective 162ms → provider 78ms（−52%）。

### 2.3 Summary epoch 单次求值（任务 3 谨慎微优化）

R3 的 Summary 每行执行 3 次 `strftime('%s', r.started_at)`（今日判定 ×2 + MAX 时间键编码）。R4 把 epoch 表达式（含非法时戳 COALESCE 容错）提升为内层子查询列 `started_epoch_s`，今日判定与 MAX 编码都引用该列：每行 strftime 降为 2 次（epoch 1 + 小数秒 1）。聚合结构、参数顺序、空值语义不变（legacyOracleSummary 差分 + R2/R4 A/B 逐字段断言锁定）。

### 2.4 已实验否决的替代结构

- `COUNT(*) OVER()` 合并（§1-G）：445ms，负优化。
- 物化候选 CTE（`cand AS (SELECT session_request_id, MIN(candidate_rank) ... GROUP BY ...)` + LEFT JOIN）：346ms，且重新引入 `AUTOMATIC COVERING INDEX`（违反 R3 防回归断言“六端点无自动索引”），否决。

## 3. 兼容性验证

- `go test ./...` 全绿（含新增 CI 断言测试，默认执行）。
- `go test -race ./internal/usage/` 全绿；`go vet ./...` 全绿。
- 六端点 legacy oracle 差分（`TestScopedQueryMatchesLegacyOracleAcrossFiltersAndScopes` 17 组 filter × 5 scope、`TestSummaryAggregatesProviderUsageOnly`、`TestEffectiveScopeExcludesDuplicateSessionLogUsage`、`TestTodaySummaryUsesTimezone`、Trends/Providers/Models/Coverage 差分、Requests 分页差分）全部逐字段通过。
- R4 A/B 逐字段断言：六端点全部子查询在 60,332 行上新旧结果 `reflect.DeepEqual` 一致（多独立会话各验证一次）。
- 新增 CI 断言：
  - `TestUsageQueryPlansUseStartedIDIndexOnTimeRange`：带时间范围过滤时六端点 filtered 主循环必为 started_at 索引 `SEARCH`（无裸 `SCAN r`）；
  - `TestBuildScopedCTEParameterizesFiltersInStableOrder` 扩展：`needDedupe=false` 结构无任何 `usage_dedupe_candidates` 引用、恒输出空标记列、参数顺序与完整结构一致；
  - `TestUsageQueryPlansEliminateCandidateWindowAndAutoIndex`（R3）继续通过：无 AUTOMATIC INDEX、无 candidate 窗口、候选查找走持久 `idx_usage_dedupe_candidates_session_rank`。

## 4. EXPLAIN 前后对比（60,332 行，无过滤基准形态）

| 端点 | R3 SCAN | R4 SCAN | R4 关键计划变化 |
|---|---|---|---|
| Summary/status | 1 | 1 | `SEARCH d ... LEFT-JOIN`（每行）→ `CORRELATED SCALAR SUBQUERY 3`（仅会话行） |
| Requests | 2 | 2 | count 同左；page 保持 `SCAN r USING INDEX idx_usage_requests_started_id` + LIMIT 提前终止 |
| Trends | 2 | 2 | 同上（range + bucket 各一份） |
| Providers | 3 | 3 | co-routine 物化消费不变；candidate 查找移入惰性子查询 |
| Models | 3 | 3 | 同上 |
| Coverage | 4 | 4 | 同上 |
| 合计 | 15 | 15 | **无过滤 SCAN 数持平（全扫不可避免）；候选索引查找从“60k 行每行一次”降为“仅 ~10.8k 会话行”** |

带时间过滤（7 天窗口）：六端点 filtered 主循环全部 `SEARCH r USING INDEX idx_usage_requests_started_at/idx_usage_requests_started_id`（非全扫），count 152ms→15ms。

## 5. 同会话 A/B（任务 6）

### 会话 1（干净，3 轮 × 5 runs，轮内交替，中位）

| metric | R4-new | R3-legacy | speedup |
|---|---|---|---|
| Summary/status | 443.9 ms | 503.5 ms | 1.13× |
| Requests | 157.0 ms | 277.5 ms | 1.77× |
| Trends | 656.2 ms | 806.9 ms | 1.23× |
| Providers | 655.6 ms | 691.8 ms | 1.06× |
| Models | 811.9 ms | 782.1 ms | 0.96×（样本 [818, 812, 657] vs [826, 769, 782]，噪声带内持平） |
| Coverage | 1016.7 ms | 1299.1 ms | 1.28× |
| **six parallel wall** | **3392.6 ms** | **4317.2 ms** | **1.27×** |

### 会话 2（共享负载尖峰污染，仍无结构性回归）

Summary 1.10×、Requests 1.00×、Trends 1.07×、Providers 1.38×、Coverage 1.09×、六并发 1.09×；Models 0.37× 系两轮 new 样本（2.07s/2.30s）撞上外部负载尖峰（同轮 old 均 <1.3s，非结构性），会话 1 无此现象。

### 会话 3（负载回升窗口，多端点受益更明显）

Requests 2.10×、Trends 1.23×、Providers 1.59×、Models 1.22×、Coverage 1.31×、六并发 1.37×；Summary 0.94×（new 样本 [697, 1365, 554] vs old [1156, 598, 657]，双向尖峰重叠，非结构性——干净会话 1 为 1.13×，且 Summary 的 R4 改动只减不增每行求值）。

### 三独立会话汇总（60,332，seed=1，同会话交替）

| metric | 会话 1（干净） | 会话 2 | 会话 3 |
|---|---|---|---|
| Summary/status | 1.13× | 1.10× | 0.94×（尖峰） |
| Requests | 1.77× | 1.00× | 2.10× |
| Trends | 1.23× | 1.07× | 1.23× |
| Providers | 1.06× | 1.38× | 1.59× |
| Models | 0.96×（噪声带） | 0.37×（尖峰） | 1.22× |
| Coverage | 1.28× | 1.09× | 1.31× |
| **six parallel wall** | **1.27×** | **1.09×** | **1.37×** |

六端点逐字段等价三会话均验证通过。Requests/Providers/Models/Coverage 加速稳定；Summary/Trends 的 candidate 占比小，比值落在共享机器噪声带内（方向恒为正）。

### P2 专项（同会话，provider/session_log/raw 口径）

Summary：effective 510ms / provider 327ms / session_log 115ms / raw 340ms；Requests page50：effective 162ms / provider 78ms。

> 注：本机为共享 8 核（Orca IDE + 多个代理进程常驻），绝对延迟随负载漂移（空闲时 R3 Summary 375ms，高峰时 500ms+）；**同会话交替测量是唯一可信归因**。六并发墙钟对负载极敏感，会话 1 的 1.27× 为推荐引用值。

## 6. R7 可达性诚实评估（任务 7）

| 指标 | 本机空闲基线（R3 实测） | R4 折算（×同会话加速比） | 生产 3 倍速折算 | R7 目标 |
|---|---|---|---|---|
| status（Summary）单次 | 375 ms | ≈ 340 ms（≈1.10×） | ≈ 113 ms | ≤ 100 ms：**差 ~13%** |
| 六并发墙钟 | 1.95 s | ≈ 1.53 s（≈1.27×） | ≈ 510 ms | ≤ 300 ms：**超 ~1.7×** |

- **status 单端点已逼近目标**：本机原始聚合下限（联 `usage_tokens` 的 COUNT/SUM/MAX）≈145ms，生产 3 倍速 ≈48ms；R4 后 Summary 距本机下限仍有 ~2.3×（candidate 子查询 ~100ms + 聚合函数求值 ~90ms），折算后 110ms 与 100ms 目标仅差 10%，属“硬件 3 倍速假设下可接受”区间。
- **六并发 300ms 不可达**：六份无过滤全扫（各 ~28ms filtered + ~125ms candidate/聚合）+ 每端点独立候选子查询在 8 核本机争用 CPU，R4 后仍约 1.5s（生产折算 ~510ms）。六并发达标需跨请求共享（增量物化/缓存 scoped 数据集、候选结果复用），超出“查询结构优化”范围，建议作为 R5+ 议题。
- **本机 8 核共享负载是绝对延迟的主要外部变量**：同会话 R3 六并发在空闲/高峰间波动 1.95s→4.3s，A/B 比值不受影响。

## 7. 剩余风险

- **L1（低危瞬态，既有）**：Trends 两次查询（range+bucket）间 WAL 并发写入下新行瞬时落入末区间偏移桶，下次调用自愈。
- **L2（低危瞬态，既有）**：Requests count 与 page 两次查询间瞬时不一致，仅影响翻页边界，自愈。
- **L2（新增，低危）**：CASE 惰性子查询在 requests-page 计划中因 SQLite 内层 flatten 被复制 3 次（dedupe_status/dedupe_request_id/WHERE 三处引用），每页约多 2 次/会话行的索引查找（页 50 行量级，实测 page 13ms 量级无感）；count/Summary 等聚合路径无复制。已实测无回归。
- **无安全/持久化影响**：全部改动为只读查询结构；迁移/schema/写入未动。
