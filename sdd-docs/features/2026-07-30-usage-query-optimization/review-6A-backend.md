# 任务 6A：后端独立功能/安全/兼容性审查报告

- 审查范围：`032aa80`（spec commit）→ HEAD `dcf1dbb`
- 后端改动面（非测试）：仅 3 个文件
  - `internal/usage/dedupe.go`（新增 375 行：候选表迁移/回填/增量维护）
  - `internal/usage/scoped_query.go`（新增 439 行：scoped SQL builder）
  - `internal/usage/store.go`（重写 560 行：读取路径改为 scoped SQL 聚合/分页）
- 审查方式：只审查不修改；逐行对照 `032aa80:internal/usage/store.go` 旧算法核验兼容性。
- 结论：**未发现高危/中危问题**。实现谨慎、差分测试充分。仅有若干低危/信息级观察项。

## 验证命令与结果

| 命令 | 结果 |
|---|---|
| `go vet ./internal/usage/` | 通过（exit 0） |
| `go build ./...` | 通过 |
| `go test ./internal/usage/ -count=1` | ok 7.5s |
| `go test ./internal/usage/ -count=1 -race` | ok 170s（无数据竞争） |
| `go test ./... -count=1` | 全部 ok |

差分测试（`legacy_oracle_test.go` 独立复现旧算法作为 oracle）、迁移幂等/原子回滚测试、
8 writer 并发 WAL 测试、分页下推测试、URL 脱敏测试、DST/小数秒/非法历史时戳测试均通过。

## 重点项逐一核验（均通过）

### 1. 筛选后去重 + 候选回退（R3）— 正确
- `buildScopedCTE`（scoped_query.go:343）的 `candidate` CTE 将 `usage_dedupe_candidates d`
  同时 JOIN `filtered session`（`is_dedupe_session=1`）与 `filtered provider`（`is_provider_usage=1`），
  即**会话与供应商候选都必须先通过普通筛选**才参与去重，正确实现“筛选在前、去重在后”。
- 胜者选择 `ROW_NUMBER() OVER (PARTITION BY session ORDER BY model_priority ASC, epoch ASC,
  fraction ASC, request_id ASC)` 取 rank=1。首选供应商被筛选掉时，因不在 `filtered` 中，
  rank=1 自动落到下一个合格候选 → **回退语义正确**。
- `scoped` 的 `effective` 分支 `is_session_log=0 OR candidate.provider_request_id IS NULL`
  与旧 `applyStatsScope` 的 `!isSessionLogRow || DedupeStatus != duplicate` 逐字段等价。
- 去重标记对 raw/session_log  scope 仍可见（LEFT JOIN 始终计算），与旧“markDuplicates 对所有
  scope 先执行”一致；当筛选排除供应商时新旧都不标记（一致）。

### 2. 去重指纹/优先级/时间窗 — 与旧算法精确一致
- 指纹 = 模型匹配 + 四类 token 全等 + 含边界 ±10 分钟窗口。
- 增量路径（dedupe.go:280 `maintainDedupeCandidatesTx`）SQL 用**放宽**边界（±10min 再 ±1s + 整秒截断，
  dedupe.go:357 `incrementalDedupeSQLBounds`）取候选，再在 Go 中用全精度 `Before/After` 严格 ±10min
  过滤（dedupe.go:299）→ 无漏判、无误判；边界含等号（`Before/After` 为严格比较）与旧 `sort.Search`+`After(end)` 一致。
- model_priority：mapped 匹配=0，仅 original 匹配=1（dedupe.go:212/366），复现旧“先试 mapped 键再试 original 键”。
- 空模型/映射==原始/单键场景：绝对 priority 值不影响结果（仅相对序有意义），与旧 `dedupeModels` 去重跳空一致。

### 3. SQL 注入 — 无风险
- 所有用户输入（筛选值、`q` 搜索、scope、分页）均走 `?` 参数（filterWhere store.go:614；scope 以 `CASE ?` 参数化）。
- 所有字符串拼接的 SQL 片段均为**常量或硬编码列名**：`scopedSessionRowPredicate`、各 predicate/expr 常量、
  `scopedEpochSecondsExpr(column)`/`scopedStartedAtFractionExpr(column)` 的 column 实参恒为
  `r.started_at`/`r2.started_at`/`provider.started_at`；`buildAggregateQuery` 的 groupColumn/nameColumn
  恒为 `r.provider_id`/`r.provider_name`/`r.mapped_model`（store.go:430/436 调用处硬编码）。
- `incrementalDedupeCandidateQuery` 的 `oppositeWhere` 为两个硬编码常量（dedupe.go:14/21）。
- `q` 的 LIKE 通配符（`%`/`_`）未转义属既有搜索语义（spec 明确要求保留），非注入。

### 4. 事务原子性（R2）— 正确
- `Record`/`recordIfAbsent`（store.go:91/152）：请求行→token 行→`maintainDedupeCandidatesTx`→Commit，
  `defer tx.Rollback()` 兜底。候选写入失败整体回滚，**不会提交不完整 usage 行**（trigger 强制失败测试
  `TestDedupeIncrementalRollsBackUsageWhenCandidateWriteFails` 验证 usage 行与候选数均为 0）。
- `migrateDedupeCandidates`（dedupe.go:57）：建表/索引→查标记→回填→写标记→Commit 全在单事务；
  标记写入失败时 schema 与索引一并回滚、usage 数据不受影响、可重试（`TestDedupeMigrationRollsBackBackfillAndMarkerTogether` 验证）。

### 5. 迁移重开/幂等 — 正确
- `CREATE TABLE/INDEX IF NOT EXISTS` + settings 标记 `usage_dedupe_candidates_backfill_v1`；
  已完成则空提交直接返回（`TestDedupeMigrationMarkerPreventsRepeatBackfill` 验证删空候选后不再回填）。
- 回填 insert 用 `ON CONFLICT DO UPDATE SET model_priority = MIN(...)`，重复执行幂等。
- 失败不写标记 → 下次启动安全重跑全量回填。

### 6. 并发 — 正确
- 增量候选维护与 usage 插入同事务；SQLite 写事务串行化，首条 insert 即持写锁，
  故后写入方总能看见先提交方 → “任意写入顺序一致”（8 writer WAL 测试 + `-race` 通过）。
- `candidate` CTE 的 LEFT JOIN 条件含 `candidate_rank=1`（每会话唯一），**不会引发行放大**。

### 7. 隐私脱敏（R5）— 正确
- Requests：`scanRequestRow`（store.go:559）输出边界对 BackendURL/ProviderAPIURL 二次 `RedactURL`。
- Coverage：SQL 按原始 url 分组，Go 侧用脱敏键合并（store.go:476 `RedactURL` + merge），输出脱敏 url；
  与旧“先脱敏再分组”逐字段一致。
- Summary/Trends/Providers/Models 的聚合 SQL **完全不投影/不解析 URL 列**（仅 `q` 筛选时在 filtered
  CTE 内对 provider_api_url 做 LIKE 判定，不外泄）。
- 错误/日志不含秘密：dedupe.go 的 `fmt.Errorf` 仅包装静态文案；候选表/标记不存秘密。

### 8. 错误泄漏 — 无
- `writeUsageJSON`（handler.go:152）对任何 store 错误统一返回 `{"error":"usage query failed"}` + 500，
  不回显 SQL/底层错误；`parseFilter` 仅返回通用 400。

### 9. 聚合兼容性（R1/R4）— 正确
- hasUsage/isFailed/tokenTotal/coverage/averageDuration 的 NULL 与浮点语义、本地日期分桶、
  topStatus 同数决胜（字典序最小）、主排序键均与旧算法逐字段一致，由 1768 行 `sql_aggregate_test.go`
  差分覆盖。`COALESCE(r.duration_ms,0)` 复现旧“NULL 不计入”求和。
- Summary last_provider_request 用 epoch+fraction 真时序取最大（匹配旧 `.After` 语义）；
  Requests 分页保留旧 `started_at DESC, id DESC` **字符串序**（含 'Z' 与 '.5Z' 同整秒误序的旧行为）——
  两处分别匹配各自旧路径，正确。

## Findings（按严重度）

### 低危

**L1. Trends 两次查询非快照一致，DST 边界并发写入可能瞬时错桶**
- 位置：`internal/usage/store.go:311-317`（先 `buildTrendsRangeQuery` 取 min/max epoch，再
  `buildTrendsQuery` 聚合）；`internal/usage/scoped_query.go:187/204`。
- 现象：两次查询之间若有并发写入，且新行时戳落在 DST 切换区间之外（>已推导的 maxEpoch），
  会落入 ELSE 末区间偏移桶，理论上可能用错偏移。代码注释已声明为“可忽略的瞬时偏差”，
  下次调用自愈。旧实现为单查询。
- 影响：仅 Trends 日桶在并发写+DST 边界的瞬时偏差，无安全/持久化影响。
- 建议（可选）：将两次查询包入同一只读事务/快照，或在文档中明示该瞬态。非阻塞。

**L2. Requests 总数与分页为两次查询，并发写下 total 与行集可能瞬时不一致**
- 位置：`internal/usage/store.go:382`（count）与 `:389`（page）。
- 现象：旧实现单查询切片，total 与行天然一致；新实现 count 与 page 分两次，WAL 并发写下
  边界可能出现 total=N 但当页行数与 total 短暂不符。仅影响翻页边界、瞬时、自愈。
- 建议（可选）：同一只读事务内执行两条查询。非阻塞。

### 信息级

**I1. page/page_size 无上限（既有行为，非回归）**
- 位置：`internal/usage/handler.go:168 parsePositiveInt`、`internal/usage/store.go:388`。
- 现象：超大 page_size 仅返回全部行（与旧“全量加载后切片”上界相同）；超大 page 使
  `(page-1)*pageSize` 在 int64 溢出为负时，SQLite 将负 OFFSET 视作 0（返回首页），无崩溃、
  无超出数据集的内存放大。属既有语义，本次未引入新风险。
- 建议（可选）：如需加固可对 page_size 设服务端上限。非本任务范围。

**I2. candidate CTE 对所有读取（含非 effective scope）均构建；Requests 构建整套 CTE 两次**
- 位置：`internal/usage/scoped_query.go:343 buildScopedCTE`（被 Summary/Trends/Providers/Models/
  Coverage/Requests 共用）；Requests 的 countSQL 与 pageSQL 各含一份完整 CTE。
- 现象：provider/session_log/raw scope 下去重对过滤无作用但仍计算 candidate（Requests 需要标记可见性）；
  聚合接口不投影 dedupe_status 但仍 LEFT JOIN candidate。属 spec R7 明确接受的“每接口重建 scoped”设计，
  无正确性影响（LEFT JOIN rank=1 不放大行）。
- 建议（可选）：后续若需进一步降延迟可考虑按 scope 裁剪 candidate 计算。非阻塞。

**I3. 时区偏移区间二分假设“单日至多一次切换”**
- 位置：`internal/usage/scoped_query.go:131 scopedZoneOffsetIntervals`（按 86400s 步进 + 二分）。
- 现象：对全部真实 tzdata 恒成立（切换相隔数天）；纯理论边界，无实际影响。

## 剩余风险（无阻塞项）

- 上述 L1/L2 均为 WAL 并发写下的**瞬时**一致性偏差，非安全/持久化缺陷，调用自愈；
  在单进程控制面 + 低写入频率的实际场景下几乎不可观测。
- 兼容性由独立 oracle 差分测试（非复用产品代码）强保证；时区/DST/小数秒/非法时戳/空键/分组同数等
  边界均有专项测试。
- 性能目标（R7）的墙钟证据属任务 6 范围，本审查未做基准实测（仅确认正确性、查询结构、分页下推、
  迁移行为相关的测试全部通过）。

## 结论

后端 schema、迁移、事务候选维护、scoped SQL、分页与全部聚合的实现**正确、安全、与旧算法逐字段兼容**，
无高危/中危问题，无 SQL 注入、无错误泄漏、无脱敏缺口、事务原子性与迁移幂等/并发均经测试验证。
建议合并；L1/L2 可作为后续可选优化跟进，不阻塞本次发布。
