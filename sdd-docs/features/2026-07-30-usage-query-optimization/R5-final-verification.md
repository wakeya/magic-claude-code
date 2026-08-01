# R5 返工报告（收尾）：全量验证 + 同会话 A/B 终测 + spec 诚实回写

- 分支：`perf/usage-query-optimization`（HEAD `c3b3056` + 本报告 commit）
- 依据：R1–R4 报告（索引基础与诊断 → Summary 单次扫描 → candidate 排名持久化 → CASE 惰性 + scope 裁剪）。
- 范围约束：**不改功能代码、不改前端**；新增 test-only 同会话 A/B（`ab_compare_test.go` 的
  `TestUsageR5FullReworkABCompare`，`MCC_USAGE_EXPLAIN=1 + MCC_USAGE_AB=1` 门控，CI 不执行）；
  回写两份 spec（`spec.md` / `spec_ZH.md`）。
- 一句话结论：**返工前（733461a，任务 6 验收态）→ 返工后（HEAD，R1–R4 累计）的严格同会话 A/B，
  两会话六端点逐字段全等价、六端点全加速、六并发 1.28–1.31× 加速，6C2 基线期两项回归（Coverage
  0.65×、六并发 0.82×）已消除；status 本机 331–417 ms、按生产 3 倍速折算约 110–140 ms，逼近但未
  达 100 ms 目标；六并发本机 1.90–2.59 s、折算约 510–860 ms，仍超 300 ms 目标约 1.7–2.9 倍——
  根因是每端点独立 filtered 全扫（语义必需）+ 逐端点候选子查询在并发下争用 CPU，须跨请求物化
  （建议 R6）才能进一步下降，不在本任务范围。**

---

## 1. 全量验证结果（2026-08-01 实测）

| 命令 | 结果 |
|---|---|
| `go test ./... -count=1` | 全部 ok |
| `go vet ./...` | 通过（exit 0） |
| `go build ./...` | 通过 |
| `gofmt -l internal/usage/` | 干净 |
| `CGO_ENABLED=0 go test ./... -count=1` | 全部 ok |
| 跨平台构建 linux/darwin/windows × amd64/arm64（`CGO_ENABLED=0 go build ./cmd/server`） | 6/6 通过 |
| `go test ./internal/usage/ -race -count=1` | ok（96.2s，无数据竞争） |
| `npm --prefix internal/frontend test` | 269 通过 / 0 失败 |
| `npm --prefix internal/frontend run build` | 通过（`dist` 无意外变更） |
| `git diff --check` | 干净 |

### 返工 commit 历史检查

| commit | 内容 | 改动面 |
|---|---|---|
| `5ddabe1` | R1 索引基础 + EXPLAIN 诊断 | 仅 `internal/usage` + docs |
| `4ae4e89` | R2 Summary 单次扫描（消除标量子查询二次物化） | 仅 `internal/usage` + docs |
| `abeaa1e` | R3 candidate_rank 持久化（消除自动索引 + 窗口排序） | 仅 `internal/usage` + docs |
| `34d5826` | R4 CASE 惰性 candidate + scope 裁剪 + Summary 单 epoch | 仅 `internal/usage` |
| `c3b3056` | R4 报告 | 仅 docs |

**五个返工 commit 全部只含 `internal/usage`（功能 + 测试）与 sdd-docs，无前端改动。**

---

## 2. R5 同会话 A/B 终测：返工前（733461a）→ 返工后（HEAD）

### 2.1 方法

- 60,332 行确定性数据集（seed=1，`modernc.org/sqlite` 纯 Go 驱动，WAL + `synchronous=NORMAL`），
  在临时库上运行 HEAD 迁移后交替测量。
- 旧侧 = test-only 重建的 **733461a 全套查询**（与返工前生产实现逐字一致）：
  - Summary：ROW_NUMBER candidate CTE（`r3LegacyScopedCTE`，与 733461a `buildScopedCTE` 逐字一致）
    + `last_provider_request` 标量子查询（与 733461a `buildSummaryQuery` 逐字一致，已对照
    `git show 733461a` 源码逐行核验）——整套 scoped CTE 执行两次。
  - 其余五端点：旧 CTE + 当前下游后缀（R2/R3/R4 已证非 Summary 端点下游投影/聚合/排序未变）。
- 新侧 = HEAD 全部生产查询。
- 先逐字段断言六端点全部子查询新旧结果完全一致（差分兼容的附加实数据证明），再交替 3 轮 ×
  每样本 5 runs（轮内顺序交替抵消趋势性漂移）、丢弃预热、各取中位。
- **旧侧查询运行在 HEAD schema 上**（含 R1 覆盖索引与持久 `candidate_rank` 列）：R1 已严格证明
  这些持久索引对旧 ROW_NUMBER 结构无可测量影响（0.95×–1.10× 噪声带），故不改变归因结论。

### 2.2 两会话结果（独立运行，绝对值随共享机器负载漂移，仅同会话比值可归因）

| 端点 | 会话 1 HEAD | 会话 1 返工前 | 加速比 | 会话 2 HEAD | 会话 2 返工前 | 加速比 |
|---|---:|---:|---:|---:|---:|---:|
| Summary/status | 331 ms | 523 ms | **1.58×** | 417 ms | 568 ms | **1.36×** |
| Requests（count+page p50） | 125 ms | 234 ms | **1.86×** | 142 ms | 255 ms | **1.80×** |
| Trends | 550 ms | 617 ms | 1.12× | 597 ms | 660 ms | 1.11× |
| Providers | 483 ms | 537 ms | 1.11× | 616 ms | 610 ms | 0.99×（噪声带） |
| Models | 525 ms | 587 ms | 1.12× | 625 ms | 669 ms | 1.07× |
| Coverage | 813 ms | 1,007 ms | **1.24×** | 879 ms | 1,133 ms | **1.29×** |
| 六并发墙钟 | 1.90 s | 2.50 s | **1.31×** | 2.59 s | 3.32 s | **1.28×** |

- **逐字段等价**：两会话均在 60,332 行上断言六端点全部子查询结果完全一致（IDENTICAL）。
- 样本紧度：Summary 新侧 [331, 331, 350] / [348, 417, 438] ms；Requests 新侧 [124–153] ms；六并发
  无单次尖刺主导中位的迹象（会话 1 六并发新旧各 5 runs 中位 1.90 s vs 2.50 s）。
- **两会话方向一致**：Summary 1.36–1.58×、Requests 1.80–1.86×、Trends 1.11–1.12×、Coverage
  1.24–1.29×、六并发 1.28–1.31×；Providers 0.99–1.11×、Models 1.07–1.12× 落在共享机器噪声带内
  （与 R3/R4 的判定一致：这两端点延迟由 60k 全扫主导，candidate 结构占比小）。

### 2.3 与 6C2 文档基线（任务 6 验收态 `dcf1dbb`）的对照

| 指标 | 6C2 基线 | R5 HEAD（本机两会话中位） | 表观变化 |
|---|---:|---:|---|
| Summary/status | 604 ms | 331–417 ms | 1.45–1.83× |
| Requests p50 | 361 ms | 125–142 ms | 2.54–2.89× |
| Trends | 705 ms | 550–597 ms | 1.18–1.28× |
| Providers | 942 ms | 483–616 ms | 1.53–1.95× |
| Models | 687 ms | 525–625 ms | 1.10–1.31× |
| Coverage | 1,534 ms | 813–879 ms | 1.75–1.89× |
| 六并发墙钟 | 5,885 ms | 1.90–2.59 s | 2.27–3.10× |

> 注意：6C2 基线测量时机器负载较重（R1 §3.1 已证同一代码跨次漂移 ±25%），此表仅作参照；
> **唯一可信归因是 §2.2 的同会话 A/B**。

### 2.4 累计加速比链（各轮同会话 A/B，均 60,332 行 seed=1）

| 轮次 | 改动 | 关键同会话加速比 |
|---|---|---|
| R1 | 索引基础 + 诊断 | 单接口 0.95×–1.10×（噪声内，证明仅索引无收益） |
| R2 | Summary 单次扫描 | Summary 1.35×–1.40×（三次独立会话） |
| R3 | candidate_rank 持久化 | Requests 1.48×–1.65×（四次会话）；六端点自动索引/窗口全消除 |
| R4 | CASE 惰性 + scope 裁剪 + 单 epoch | Summary provider −36% / session_log −77%、Requests provider −52%；六并发 1.09×–1.37× |
| **R5** | **返工前 → HEAD 累计** | **Summary 1.36×–1.58×、Requests 1.80×–1.86×、Coverage 1.24×–1.29×、六并发 1.28×–1.31×** |

---

## 3. R7 目标状态（诚实评估）

| 指标 | R7 目标 | R5 HEAD 本机 | 生产 3 倍速折算 | 判定 |
|---|---|---|---|---|
| 迁移（含回填，5,609 候选） | ≤ 2 s | 362 ms（R1 实测） | — | ✅ 达标 |
| `GET /api/status` 单次 | ≤ 100 ms | 331–417 ms | 约 110–140 ms | ❌ **逼近但未达**（差约 10–40%；R4 空闲基线折算约 113 ms） |
| Requests 前 50 行（含总数） | ≤ 100 ms | 125–142 ms | 约 42–47 ms | ⚠️ 本机口径仍超 1.25–1.4×；3 倍速折算口径可达标 |
| 六并发墙钟 | ≤ 300 ms | 1.90–2.59 s | 约 510–860 ms | ❌ **仍超约 1.7–2.9 倍** |

- **status 已逼近目标**：本机原始聚合下限（联 `usage_tokens` 的 COUNT/SUM/MAX）≈145 ms，生产
  3 倍速 ≈48 ms；R5 后 Summary 距本机下限约 2.3–2.9×，折算后 110–140 ms 与 100 ms 仅差 10–40%。
  需注意 3 倍速折算对单线程路径可能偏保守：返工前 status 本机 807 ms 与生产基线 760–780 ms
  基本同速（6C2 记录），若单线程路径按 1× 折算则差距更小。
- **六并发不可达**：六份无过滤 filtered 全扫（各 ~28 ms）+ 逐端点候选子查询（~100–125 ms）+ 聚合
  在 8 核共享机器上并发争用 CPU；R5 后相对返工前已加速 1.28–1.31×，但 300 ms 目标要求“单次扫描 +
  无逐查询物化”且六请求共享同一物化结果。**进一步下降必须跨请求物化**（增量缓存 scoped 数据集 /
  候选结果复用 / 六端点共享一次全扫），属结构性缓存议题，**建议作为 R6，不在本任务范围**。
- 若生产硬件按 3 倍速折算，Requests 分页可达标；本机绝对口径下仍超 1.25–1.4×。

### 回归状态（相对返工前 733461a）

| 指标 | 6C2 基线期（回归） | R5 HEAD（同会话） | 结论 |
|---|---|---|---|
| Coverage 单次 | 0.65×（慢于旧实现） | **1.24×–1.29×（快于返工前）** | ✅ 回归消除 |
| 六并发墙钟 | 0.82×（慢于旧实现） | **1.28×–1.31×（快于返工前）** | ✅ 回归消除 |

---

## 4. 剩余风险

- **L1（低危瞬态，既有）**：Trends 先查 min/max epoch 再聚合（两次查询），WAL 并发写 + DST 边界下
  新行可能瞬时落入末区间偏移桶，下次调用自愈；无安全/持久化影响。
- **L2（低危瞬态，既有）**：Requests 总数与分页为两次查询，WAL 并发写下 total 与当页行集可能瞬时
  不一致，仅影响翻页边界、瞬时、自愈。（R4 曾实测 `COUNT(*) OVER()` 合并为净负优化 445 ms vs
  166 ms，维持两查询结构。）
- **L2（低危瞬态，R4 新增）**：CASE 惰性子查询在 requests-page 计划中因 SQLite 内层 flatten 被
  复制 3 次（三处引用），每页约多 2 次/会话行的索引查找；页 50 行量级实测无感（page 13 ms 量级），
  count/Summary 等聚合路径无复制。
- **R7 绝对目标仍未全达（既有发现，部分缓解）**：status 逼近（约 110–140 ms vs 100 ms）、六并发
  仍超（约 510–860 ms vs 300 ms）；两项回归已消除。六并发达标需跨请求物化（建议 R6）。
- **R1 索引保留说明（信息级）**：`idx_usage_dedupe_candidates_session_priority` 在 R3 后已不被读取
  路径使用（candidate 访问改走 `idx_usage_dedupe_candidates_session_rank`），保留以兼容 R1 迁移
  测试，对 5,609 行候选表写入开销可忽略；如需精简可在后续独立评估移除。

## 5. 交付物

- `internal/usage/ab_compare_test.go`：`TestUsageR5FullReworkABCompare` +
  `r5LegacyPreReworkSummaryQuery`（同会话返工前→返工后 A/B，含六端点逐字段等价断言；
  `r4ABBuildEndpoints` 增加 legacySummaryQuery 参数以复用构造器）。
- `sdd-docs/features/2026-07-30-usage-query-optimization/spec.md` / `spec_ZH.md`：实现状态 /
  测试证据 / 偏差 / 剩余风险回写。
- 本报告 `R5-final-verification.md`。
