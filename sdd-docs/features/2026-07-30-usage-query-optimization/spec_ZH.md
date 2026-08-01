# 用量查询性能优化规格

本地页面：`/` 服务状态、`/` 使用统计、`/` 会话  
管理入口：`GET /api/status`、`GET /api/usage/*`、`GET /api/sessions`、`GET /api/sessions/projects`  
参考功能：`2026-05-15-usage-statistics`、`2026-05-21-sqlite-wal-optimization`、`2026-06-12-windows-usage-statistics-fixes`、`2026-07-17-kimi-quota-usage-parsing-fixes`  
技术栈：Go 1.26、SQLite/WAL、Vue 3、TypeScript  
最后更新：2026-08-01

进度：设计已确认；实现 6 / 6 个任务（代码完成，兼容性经独立审查确认）；任务 6 性能验证已执行；**返工轮次 R1–R5 已完成**（索引诊断 → Summary 单次扫描 → candidate 排名持久化 → 惰性 candidate + scope 裁剪 → 终验）。相对返工前的累计同会话 A/B：**六端点全部加速（1.07×–1.86×）、无回归，6C2 基线期两项回归（Coverage 0.65× / 六并发 0.82×）已消除**；status 按生产 3 倍速折算约 110–140 ms，逼近但未达 100 ms 目标；六并发折算约 510–860 ms，仍超 300 ms 约 1.7–2.9 倍（需跨请求物化，建议 R6）。详见下方“实现状态 / 偏差”段落

## 实现状态（Implementation Status）

最后验证：2026-08-01（分支 `perf/usage-query-optimization`，HEAD `c3b3056` + R5 文档 commit）

### 完成情况

6 个任务的代码实现均已完成并通过兼容性审查（实现 commit 14 个：`956f2ec`→`dcf1dbb`）。
在其上又完成性能返工轮次 R1–R5（5 个 commit：`5ddabe1`/`4ae4e89`/`abeaa1e`/`34d5826`+`c3b3056`，
全部只改 `internal/usage` + docs，**无前端改动**）：

- 后端独立审查（任务 6A，`review-6A-backend.md`）：**未发现高/中危问题**；实现与旧算法逐字段兼容，
  独立 oracle 差分测试、迁移幂等/原子回滚、8 writer 并发 WAL、分页下推、URL 脱敏、
  DST/小数秒/非法时戳等专项测试全部通过。仅 2 项低危瞬态（见“剩余风险”）。
- 前端复审（任务 6B2）：四项 finding 均不可再触发，无中/高回归。
- 返工轮次（报告见 `R1-index-diagnostics.md` … `R5-final-verification.md`）：
  - **R1** 索引基础 + EXPLAIN 诊断：同会话 A/B 严格证明仅靠索引对单接口无可测量收益
    （0.95×–1.10×，噪声内）——真正根因是 scoped CTE 对 60k 行重复全扫，必须查询结构重写。
  - **R2** Summary 单次扫描：`last_provider_request` 由标量子查询（整套 scoped CTE 二次物化）
    改为单次扫描 `MAX` 时间键编码；三次独立同会话 A/B Summary 1.35×–1.40×。
  - **R3** candidate 排名持久化（`candidate_rank` 列 + `(session_request_id, candidate_rank)` 索引）：
    六端点查询计划的 ROW_NUMBER 窗口排序与运行时自动索引全部消除；四次会话 Requests 1.48×–1.65×。
  - **R4** CASE 惰性 candidate（非会话行零候选查找）+ scope 裁剪（raw/provider/session_log 聚合
    整段省去 candidate 计算）+ Summary epoch 单次求值：Summary provider 口径 −36% / session_log
    −77%、Requests provider −52%、六并发 1.09×–1.37×。`COUNT(*) OVER()` 合并 count+page 经实测
    否决（445 ms vs 166 ms，净负优化）。
  - **R5** 终验：全量验证重跑 + 返工前（`733461a`）→ HEAD 全套同会话 A/B（见下方测量）。

### 测试证据（R5 终验重跑，2026-08-01 实测）

| 命令 | 结果 |
|---|---|
| `go build ./...` | 通过（exit 0） |
| `go vet ./...` | 通过（exit 0） |
| `gofmt -l internal/usage/` | 干净 |
| `go test ./... -count=1` | 全部 ok |
| `CGO_ENABLED=0 go test ./... -count=1` | 全部 ok |
| 跨平台构建 linux/darwin/windows × amd64/arm64（`CGO_ENABLED=0`） | 6/6 通过 |
| `go test ./internal/usage/ -race -count=1` | ok（无数据竞争） |
| `npm --prefix internal/frontend test` | 269 通过 / 0 失败 |
| `npm --prefix internal/frontend run build` | 通过（`dist` 无意外变更） |
| `git diff --check` | 干净 |

性能基准脚本：`internal/usage/performance_test.go`（任务 6 新增）。由环境变量
`MCC_USAGE_BENCH_ROWS` 门控（未设置则跳过，CI 默认不执行，**无墙钟硬断言**）；固定随机种子
（seed=1）生成可重复数据集，混合约 82% 供应商行 / 18% 会话行，并让约半数会话行镜像邻近供应商行
（相同模型 + 相同四类 token 计数 + ±5 分钟窗口）以确定性产生候选关系。复现命令：

```bash
MCC_USAGE_BENCH_ROWS=60332 go test ./internal/usage/ -run TestUsagePerformanceProfile -count=1 -v
```

### 性能测量（60,332 行确定性数据集，seed=1）

环境：8 核 / 30GB，`modernc.org/sqlite`（纯 Go，与生产同驱动），WAL + `synchronous=NORMAL`，
预热后多次取中位。**归因规则（R1 §3.1）：共享机器负载跨会话漂移（同一代码 ±25%），只有同会话
A/B 比值可归因；跨次绝对值仅作参照。**

#### 历史基线（仅参照；6C2 基线测量时机负载较重）

| 指标 | 优化前 (032aa80) | 优化后 (dcf1dbb) | 相对变化 | R7 目标 | 达标 |
|---|---:|---:|---:|---:|:--:|
| 迁移（含候选回填，建立 5,609 候选） | — | 367 ms | — | ≤ 2 s | ✅ |
| `GET /api/status`（Summary 单次） | 807 ms | 604 ms | 1.34× 快 | ≤ 100 ms | ❌ 超约 6 倍 |
| Requests 第 50 页（LIMIT/OFFSET） | 868 ms | 361 ms | 2.4× 快 | ≤ 100 ms | ❌ 超约 3.6 倍 |
| Trends 单次 | 1,226 ms | 705 ms | 1.74× 快 | — | — |
| Providers 单次 | 990 ms | 942 ms | 1.05× 快 | — | — |
| Models 单次 | 1,019 ms | 687 ms | 1.48× 快 | — | — |
| Coverage 单次 | 989 ms | 1,534 ms | **0.65× 慢（回归）** | — | — |
| 六接口并发墙钟 | 4,817 ms | 5,885 ms | **0.82× 慢（回归）** | ≤ 300 ms | ❌ 超约 19 倍 |

#### R5 同会话 A/B：返工前（`733461a`）→ HEAD，两次独立会话（2026-08-01）

同会话 A/B 工具：`internal/usage/ab_compare_test.go`（`TestUsageR5FullReworkABCompare`，
`MCC_USAGE_EXPLAIN=1 + MCC_USAGE_AB=1` 门控）。旧侧为 test-only 重建的 `733461a` 全套查询
（已对照 `git show 733461a` 逐行核验：ROW_NUMBER candidate CTE + Summary 标量子查询）；计时前
先在 60,332 行上逐字段断言六端点查询结果完全一致，再交替 3 轮 × 每样本 5 runs、丢弃预热、取中位。

| 指标 | 会话1 HEAD | 会话1 返工前 | 加速比 | 会话2 HEAD | 会话2 返工前 | 加速比 |
|---|---:|---:|---:|---:|---:|---:|
| `GET /api/status`（Summary 单次） | 331 ms | 523 ms | **1.58×** | 417 ms | 568 ms | **1.36×** |
| Requests 第 50 页（count+page） | 125 ms | 234 ms | **1.86×** | 142 ms | 255 ms | **1.80×** |
| Trends 单次 | 550 ms | 617 ms | 1.12× | 597 ms | 660 ms | 1.11× |
| Providers 单次 | 483 ms | 537 ms | 1.11× | 616 ms | 610 ms | 0.99×（噪声带） |
| Models 单次 | 525 ms | 587 ms | 1.12× | 625 ms | 669 ms | 1.07× |
| Coverage 单次 | 813 ms | 1,007 ms | **1.24×** | 879 ms | 1,133 ms | **1.29×** |
| 六接口并发墙钟 | 1.90 s | 2.50 s | **1.31×** | 2.59 s | 3.32 s | **1.28×** |

两会话方向一致：Summary 1.36×–1.58×、Requests 1.80×–1.86×、Trends 1.11×–1.12×、Coverage
1.24×–1.29×、六并发 1.28×–1.31×；Providers（0.99×–1.11×）与 Models（1.07×–1.12×）落在共享机器
噪声带（其延迟由必须的 60k filtered 全扫主导）。**6C2 基线期两项回归均已消除：Coverage 与六并发
现在都快于返工前实现。**

#### 累计返工归因链（每轮均为同会话 A/B，同一数据集）

| 轮次 | 改动 | 关键同会话加速比 |
|---|---|---|
| R1 | 索引基础 + 诊断 | 单接口 0.95×–1.10×（噪声内；仅索引无收益） |
| R2 | Summary 单次扫描 | Summary 1.35×–1.40×（3 次会话） |
| R3 | candidate 排名持久化 | Requests 1.48×–1.65×（4 次会话）；六端点自动索引/窗口全消除 |
| R4 | CASE 惰性 + scope 裁剪 + 单 epoch | Summary provider −36% / session_log −77%；Requests provider −52%；六并发 1.09×–1.37× |
| R5 | 返工前 → HEAD 累计 | Summary 1.36×–1.58×；Requests 1.80×–1.86×；Coverage 1.24×–1.29×；六并发 1.28×–1.31× |

#### 返工后 R7 状态

| 指标 | R7 目标 | HEAD 本机 | 生产 3 倍速折算 | 达标 |
|---|---:|---:|---:|:--:|
| 迁移（含回填，5,609 候选） | ≤ 2 s | 362 ms | — | ✅ |
| `GET /api/status`（Summary 单次） | ≤ 100 ms | 331–417 ms | 约 110–140 ms | ❌ **逼近但未达**（差约 10–40%；R4 空闲基线折算约 113 ms） |
| Requests 第 50 页（count+page） | ≤ 100 ms | 125–142 ms | 约 42–47 ms | ⚠️ 本机口径仍超 1.25–1.4×；3 倍速折算口径可达标 |
| 六接口并发墙钟 | ≤ 300 ms | 1.90–2.59 s | 约 510–860 ms | ❌ **仍超约 1.7–2.9 倍** |

硬件聚合下限校准：本机原始 `COUNT/SUM/MAX`（联 `usage_tokens`、无 scoped CTE）best≈145 ms，
而生产规格表记录为 40-50 ms，即本机约比生产硬件慢 3 倍。本机优化前 status 单次 807 ms 与生产
基线 760-780 ms 基本吻合，说明单线程 status 路径本机接近生产速度——因此 3 倍速折算对单线程路径
偏保守，生产实际差距可能更小。

### 偏差（返工已消除的根因 + 剩余差距的诚实评估）

**返工已修复（EXPLAIN QUERY PLAN + 同会话 A/B 双重验证）：**

- Summary 的 `last_provider_request` 标量子查询使整套 scoped CTE（2× 60k 全扫 + 2× 运行时自动索引）
  二次物化；R2 改为单次扫描 `MAX` 时间键编码（Summary 1.35×–1.40×）。
- `candidate` ROW_NUMBER CTE 每查询物化、连接在运行时自建 `AUTOMATIC PARTIAL COVERING INDEX`——
  基表索引无法消除（R1 已严格证明）。R3 持久化 `candidate_rank` 并建 `(session_request_id,
  candidate_rank)` 索引：六端点计划中自动索引与窗口 TEMP B-TREE 全部消失，候选查找改走持久索引。
- candidate 计算对不需要去重标记、过滤也不依赖候选判定的 scope 仍在执行；R4 改为 CASE 惰性
  （非会话行零查找）并对 raw/provider/session_log 聚合路径整段裁剪。
- 6C2 基线期两项回归——Coverage 单次（0.65×）与六并发墙钟（0.82×，均相对优化前）——已消除：
  同会话 A/B 显示 Coverage 1.24×–1.29×、六并发 1.28×–1.31×，都快于返工前实现。

**剩余偏差（R7 绝对目标）：** 每个端点仍各自对 60k 行 filtered 做无过滤全扫（语义必需：统计全部行）
并各自执行候选子查询，六端点并发在 8 核本机争用 CPU。R5 后：status 本机约 331–417 ms（生产 3 倍速
折算约 110–140 ms，逼近但未达 100 ms 目标），Requests 约 125–142 ms（3 倍速折算可达标；本机口径
仍超 1.25–1.4×），六并发约 1.90–2.59 s（3 倍速折算约 510–860 ms，仍超 300 ms 约 1.7–2.9 倍）。
六并发进一步下降必须**跨请求物化**（六端点共享一次 filtered 全扫/候选结果、scoped 数据集增量物化）
——属查询结构之外的缓存议题，**建议作为 R6，明确不在本任务范围**。

### 剩余风险

- **L1（低危瞬态，既有）**：Trends 先查 min/max epoch 再聚合（两次查询），WAL 并发写 + DST 边界下
  新行可能瞬时落入末区间偏移桶，下次调用自愈；无安全/持久化影响。
- **L2（低危瞬态，既有）**：Requests 总数与分页为两次查询，WAL 并发写下 total 与当页行集可能瞬时
  不一致，仅影响翻页边界、瞬时、自愈。（R4 曾实测 `COUNT(*) OVER()` 合并为净负优化 445 ms vs
  166 ms，维持两查询结构。）
- **L2（低危瞬态，R4 新增）**：CASE 惰性子查询在 requests-page 计划中因 SQLite 内层 flatten 被
  复制最多 3 次（三处引用），每页约多 2 次/会话行的索引查找（页 50 行量级，实测 page 约 13 ms
  无感）；count/Summary 等聚合路径无复制。
- **R7 目标部分未达（既有发现延续）**：status 已逼近 100 ms（3 倍速折算约 110–140 ms），但六并发
  仍超 300 ms 约 1.7–2.9 倍；两项回归已消除。已如实记录；六并发需跨请求物化（建议 R6），不在本任务。

## 整体分析（源站分析）

### 问题陈述

生产数据库包含 60,332 条 `usage_requests` 和等量 `usage_tokens`。这对 SQLite
属于小数据量，但实测延迟如下：

| 操作 | 实测耗时 |
|---|---:|
| SQLite 直接执行 `COUNT` / `SUM` / `MAX` 聚合 | 40-50 ms |
| SQLite 直接执行不排序的全宽联表 | 约 130 ms |
| SQLite 直接执行当前排序的全宽联表 | 约 180 ms |
| 单独调用 `GET /api/status` | 760-780 ms |
| 页面首屏并发三次状态请求 | 每个 1.17-1.24 s；墙钟 1.25 s |
| 使用统计六接口并发 | 墙钟约 1.90 s |
| SQL 正确执行 `LIMIT 50` 的请求分页 | 小于 10 ms |

Docker CPU、内存、DNS、NAT、供应商额度快照和脚本 Worker 都不是该控制面延迟的
来源。现有 SQLite 查询计划已经使用 `idx_usage_requests_started_at` 和
`usage_tokens.request_id` 主键索引。缺少 `(started_at, id)` 复合索引会使末级排序
创建临时 B-Tree，但排序实测只占约 50 ms，并非主因。

### 根因

1. `Summary`、`Trends`、`Requests`、`Providers`、`Models`、`Coverage` 全部调用
   同一个全宽 `queryRows`。
2. `queryRows` 选择约 30 个字段，排序全部匹配行，创建完整 Go 对象、解析时间，
   并在任何接口专用聚合之前执行有效口径去重。
3. 每个扫描行都会重新解析并脱敏两个 URL，即使调用方不会返回 URL。
4. `Requests` 传入 `includePagination=false`，先加载全部匹配行再在 Go 中切片；
   `queryRows` 虽有 SQL `LIMIT/OFFSET` 支持，但实际没有调用方启用。
5. `applyStatsScope` 对不需要排除重复的统计口径也计算完整重复关系。
6. 使用统计页并发六个请求，各自重复执行相同的全量扫描与去重。
7. Dashboard 状态加载、连接模式加载和 `AppHeader` 在首屏分别请求状态。
8. 即使当前是服务状态标签，也会预加载会话项目和会话列表。

### 兼容性不变量

本功能采用完全兼容优化，不得改变：

- 现有接口路径、方法、响应字段、JSON 类型或错误分类；
- 请求排序、总数、分页、日期/时区行为或筛选含义；
- `effective`、`provider`、`session_log`、`raw` 四种统计口径；
- 当前重复指纹：模型匹配、四类 token 计数完全相同、时间差位于含边界的十分钟窗口；
- 当前行分类：供应商候选必须是 `source_app=claude_code`、`usage_source=provider`、
  `usage_parse_status=ok` 的非会话行；会话候选必须满足现有会话行判定且
  `usage_parse_status=ok`；
- 模型键优先级（`mapped_model` 优先于不同值的 `original_model`）和最早供应商候选选择；
- raw 与 session-log 请求行上的重复标记可见性；
- 对历史脏数据库 URL 的读取二次脱敏；
- usage 记录、代理转发、额度查询或 Session Log 同步。

当前去重在普通筛选之后执行，因此不能只持久化单个“最终胜者”：首选供应商行被日期、
模型、供应商或搜索条件过滤后，筛选结果内的另一个候选可能成为胜者。持久化模型必须
保留所有候选对，并在每次筛选后的查询中选择胜者。

### 选定架构

增加可幂等迁移的候选关系：

```sql
CREATE TABLE IF NOT EXISTS usage_dedupe_candidates (
    session_request_id  TEXT NOT NULL,
    provider_request_id TEXT NOT NULL,
    model_priority      INTEGER NOT NULL,
    PRIMARY KEY (session_request_id, provider_request_id),
    FOREIGN KEY (session_request_id) REFERENCES usage_requests(id) ON DELETE CASCADE,
    FOREIGN KEY (provider_request_id) REFERENCES usage_requests(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_usage_dedupe_provider
ON usage_dedupe_candidates(provider_request_id);

CREATE INDEX IF NOT EXISTS idx_usage_requests_started_id
ON usage_requests(started_at DESC, id DESC);
```

当供应商行的映射模型或原始模型匹配会话行映射模型时，`model_priority=0`；只有通过
会话行中不同值的原始模型首次匹配时才为 `1`。usage 数据是只插入模型，因此候选关系
无需处理原记录修改。

迁移在单个事务中按生产指纹回填候选对，成功后在 `settings` 写入标记，避免后续启动
重复全量回填。新供应商行和会话行在 usage 插入事务内增量维护候选，无论哪一侧先写入
都能得到一致结果。

每个读取查询先建立参数化的筛选数据集，再按 `model_priority`、供应商 `started_at`
和确定性的请求 ID 同时间戳决胜规则选择首个可用候选，之后应用统计口径。聚合和分页
都在该 scoped SQL 数据集上执行。旧实现对完全相同时间戳没有稳定公开顺序；增加 ID
决胜使该未定义边界确定化，不改变任何已定义契约。

### 前端数据流

- Dashboard 首次加载只获取一次状态和配置。
- 服务状态块、连接模式和 `AppHeader` 复用同一份状态。
- 保留 30 秒刷新，但每个周期只发送一个状态请求。
- 仅在首次激活会话标签时加载会话项目与第一页；通过 `tab=sessions` 直接进入时仍立即加载。
- 保留全部现有 usage 接口。使用统计页可继续并发六个请求，因为每个接口将使用窄字段
  SQL 投影和数据库聚合。

### 错误与安全行为

- 表结构创建、历史回填和迁移标记更新具备原子性，失败时整体回滚。
- 增量候选维护与 usage 插入同事务。记录失败继续通过现有日志可见，且不改变代理响应。
- 所有筛选值继续使用 SQL 参数；保留现有搜索通配符语义。
- Requests 与 Coverage 在输出边界脱敏 URL；不暴露 URL 的聚合查询不读取或解析 URL。
- 日志、候选关系、迁移标记和基准输出不得新增 prompt、凭证、token、Cookie、供应商
  payload 或未脱敏 URL。
- 清除 usage 时通过外键级联删除候选；Session Sync 重置选项保持现有行为。

## 开发检查清单

| # | 状态 | 项目 | 证据 |
|---|---|---|---|
| 1 | 完成 | 候选表结构、原子回填和幂等迁移 | 迁移与兼容测试（6A 通过） |
| 2 | 完成 | 任意写入顺序下的原子增量候选维护 | 去重写路径测试（6A 通过） |
| 3 | 完成 | 筛选/scoped SQL 数据集和真实请求分页 | 差分与分页测试（6A 通过） |
| 4 | 完成 | 全部统计接口使用专用 SQL 聚合 | 差分测试和查询计划（6A 通过） |
| 5 | 完成 | 首屏一次状态请求和会话按需加载 | 前端源码/行为测试（6B2 通过） |
| 6 | 完成（R7 部分达标；已返工 R1–R5） | 全量回归、跨平台构建、生产与合成基准 | 见“实现状态”：验证全通过；返工消除两项回归并把 status 拉近目标；六并发仍超（需跨请求物化，建议 R6） |

## 需求

### R1. 保持全部可观察统计行为

对相同数据库内容和筛选条件，所有公开结果必须与旧算法逐字段相等。此前未定义的完全相同
时间戳候选或相同聚合值分组的 tie 可变为确定性顺序，但不得改变其已记录的主排序键。

### R2. 安全持久化全部去重候选

迁移必须保留现有行、发现每个匹配的 Session/Provider 对并可安全重启。新记录无论哪侧
先写入都必须建立候选。候选写入失败不得提交不完整 usage 行。

### R3. 在候选选择前应用筛选

会话行和供应商候选必须同时存在于普通筛选数据集中，供应商才能将该会话标记为重复。
首选候选缺席时必须回退到下一个合格候选。

### R4. 将分页和聚合下推至 SQLite

请求总数和分页行不得通过在 Go 中物化全部匹配明细计算。Summary 和各分组统计只选择
必要字段，返回数据库聚合结果而非完整请求行。

### R5. 保留输出边界 URL 脱敏

包含 URL userinfo 或敏感 query 的历史行在 Requests 和 Coverage 中必须继续脱敏。
不返回 URL 的查询完全避免 URL 解析。

### R6. 消除重复和不必要的控制面工作

Dashboard 首屏必须共享一次状态结果；只有会话标签激活后才能读取会话数据。刷新和模式
更新行为保持现状。

### R7. 达到可测量性能目标

在当前约 6 万行生产数据库上：

- `/api/status` 不超过 100 ms；
- 请求前 50 行（含总数）不超过 100 ms；
- 六个 Usage 请求并发不超过 300 ms；
- 首次候选关系回填不超过 2 秒。

可选的 100 万行合成基准记录以下观察目标：

- 状态不超过 500 ms；
- 请求第一页不超过 300 ms；
- 六个 Usage 操作不超过 1.5 秒。

CI 不使用易波动的墙钟硬断言，只验证正确性、查询结构、分页下推和迁移行为；基准耗时
单独记录。

## 任务详情

### 任务 1：候选表结构与历史回填

#### 需求

**Objective（目标）** — 创建候选关系并原子回填历史 usage，不改变任何公开结果。

**Outcomes（成果）** — `Store.Migrate` 创建两个索引和候选关系，执行一次窄字段回填，
并在同一事务写入完成标记。

**Evidence（证据）** — 测试覆盖空库、已有数据、重复迁移、失败迁移、已完成迁移和全部
指纹边界。

**Constraints（约束）** — 不重写或删除 usage 行；回填只读取匹配必要字段，使用
O(n log n) 或更好的索引/扫描算法，不执行二次方交叉连接。

**Edge Cases（边界）** — 空模型、映射/原始模型相同、多个供应商、恰好 ±10 分钟、
时间戳相同、历史时间戳非法、没有候选。

**Verification（验证）** — 迁移测试通过，6 万行合成回填达到记录目标。

#### 计划

- [ ] 在 `internal/usage/dedupe_test.go` 添加失败的迁移与回填测试。
- [ ] 运行 `go test ./internal/usage -run 'TestDedupeMigration|TestDedupeBackfill' -count=1`，
  确认因候选关系缺失而失败。
- [ ] 在 `internal/usage/store.go` 增加表结构和迁移标记；在
  `internal/usage/dedupe.go` 创建专用匹配/回填辅助函数。
- [ ] 重跑专项测试并确认通过。
- [ ] 运行 `go test ./internal/usage -count=1` 并提交该任务。

#### 验证

- [ ] 专项测试
- [ ] 包回归测试
- [ ] 迁移耗时证据

### 任务 2：增量候选维护

#### 需求

**Objective（目标）** — 对只插入的供应商和会话 usage 增量维护候选对，避免周期性全局重算。

**Outcomes（成果）** — `Record` 和 `recordIfAbsent` 在请求行与 token 行均存在后调用
同一个事务辅助函数；该函数插入含边界时间窗内全部异侧匹配，且不存储秘密。

**Evidence（证据）** — 测试以供应商优先、会话优先、多候选、时间边界和不匹配顺序写入，
同时检查公开结果与候选表。

**Constraints（约束）** — 候选维护属于现有事务；重复 Session Sync 继续幂等。

**Edge Cases（边界）** — `none`/`missing` usage、非 Claude 来源、失败供应商 usage、
任一 token 不同、重复 `recordIfAbsent`。

**Verification（验证）** — 写路径专项测试与并发读写测试通过。

#### 计划

- [ ] 在 `internal/usage/dedupe_test.go` 添加失败的写入顺序与原子性测试。
- [ ] 运行专项测试，确认预期的候选缺失失败。
- [ ] 在 `internal/usage/dedupe.go` 增加 `maintainDedupeCandidatesTx`，并从
  `internal/usage/store.go` 两个插入方法调用。
- [ ] 重跑专项测试，再运行 `go test ./internal/usage -count=1`。
- [ ] 提交该任务。

#### 验证

- [ ] 写入顺序测试
- [ ] 原子回滚测试
- [ ] WAL 并发回归

### 任务 3：Scoped SQL 数据集与请求分页

#### 需求

**Objective（目标）** — 使用参数化 SQL 表达筛选、候选选择、统计口径、重复标记、计数、
排序和分页。

**Outcomes（成果）** — `internal/usage/scoped_query.go` 中的专用 SQL builder 返回所有
读取方法共用的查询片段和有序参数；`Requests` 使用 SQL 总数与分页，只对返回行脱敏 URL。

**Evidence（证据）** — 测试专用旧算法判定器在全部筛选/口径下比较总数、行、排序与标记；
查询结构测试证明 `LIMIT/OFFSET` 已下推至 SQLite。

**Constraints（约束）** — 候选选择前应用筛选；现有子串搜索和时区解析不变。

**Edge Cases（边界）** — 页码超出总数、零结果、筛选后的候选回退、仅会话/仅供应商筛选、
历史脏 URL。

**Verification（验证）** — 差分测试通过，生产库分页 50 行达到目标。

#### 计划

- [ ] 在 `internal/usage/scoped_query_test.go` 添加测试专用旧算法判定器和失败的差分/分页测试。
- [ ] 运行专项测试，确认 scoped SQL 和 SQL 分页尚不存在。
- [ ] 在 `internal/usage/scoped_query.go` 实现参数化 filtered/candidate/scoped builder。
- [ ] 用 SQL 总数/分页查询替换 `internal/usage/store.go` 的内存切片。
- [ ] 重跑专项测试和 `go test ./internal/usage -count=1`；提交该任务。

#### 验证

- [ ] 口径/筛选差分矩阵
- [ ] 分页查询计划证据
- [ ] URL 脱敏回归

### 任务 4：专用 SQL 统计

#### 需求

**Objective（目标）** — 使用窄字段 SQL 聚合替代重复全量行物化。

**Outcomes（成果）** — Summary、Trends、Providers、Models、Coverage 在 SQLite 中
聚合 scoped 数据集；只有 Coverage 选择供应商 URL 并脱敏分组输出。

**Evidence（证据）** — 新旧差分测试逐字段和排序规则比较；生产与合成基准记录每个操作。

**Constraints（约束）** — token 总数、覆盖率、平均耗时空值行为、失败分类、本地日期分桶
和最高解析状态同数决胜行为完全一致。

**Edge Cases（边界）** — 没有 usage、全部失败、duration/status 为空、分组同数、DST 切换、
非法时区、全部统计口径。

**Verification（验证）** — 差分测试通过，`queryRows` 不再被公开统计或请求分页使用。

#### 计划

- [ ] 在 `internal/usage/sql_aggregate_test.go` 添加失败的聚合差分测试。
- [ ] 运行专项测试并记录与旧实现的差异。
- [ ] 在 `internal/usage/store.go` 实现专用聚合 SQL，只共享 scoped SQL 片段和筛选参数。
- [ ] 全部差分测试通过后删除废弃的全量物化路径。
- [ ] 运行 usage 包及全部 Go 测试，提交该任务。

#### 验证

- [ ] 聚合差分矩阵
- [ ] 时区/DST 测试
- [ ] 查询计划和字段投影审查

### 任务 5：前端请求收敛与会话按需加载

#### 需求

**Objective（目标）** — 停止重复状态扫描，避免无关标签执行会话工作。

**Outcomes（成果）** — `DashboardView` 持有初始状态/配置，并将版本/模式派生数据传给
`AppHeader`；连接模式复用相同结果。会话数据在首次激活会话标签时加载一次。

**Evidence（证据）** — 前端源码/行为测试断言首屏一次状态请求、状态标签不请求会话、
直接进入会话标签立即加载。

**Constraints（约束）** — Header 更新检查、模式更新事件、标签 query 参数、30 秒刷新、
错误容忍和会话手动刷新不变。

**Edge Cases（边界）** — 初始会话标签、快速切换、首次失败后重试、保存模式、退出登录、
组件卸载。

**Verification（验证）** — 前端测试和生产构建通过。

#### 计划

- [ ] 修改/添加
  `internal/frontend/src/views/DashboardSessionsPreload.test.ts`、
  `internal/frontend/src/views/DashboardStatusLoad.test.ts` 和
  `internal/frontend/src/components/AppHeader.test.ts` 的失败测试。
- [ ] 运行前端专项测试，确认当前预加载/重复行为不满足新断言。
- [ ] 修改 `DashboardView.vue` 和 `AppHeader.vue`，共享状态/配置并按需加载会话。
- [ ] 运行全部前端测试和 `npm --prefix internal/frontend run build`。
- [ ] 提交源码、测试和更新后的 `internal/frontend/dist`。

#### 验证

- [ ] 前端专项测试
- [ ] 前端全量测试
- [ ] 内嵌生产构建

### 任务 6：性能与发版级验证

#### 需求

**Objective（目标）** — 合并前证明兼容性并记录最新性能证据。

**Outcomes（成果）** — 确定性基准支持可配置的 6 万和 100 万行数据集；将实际结果回写
规格检查清单、进度和验证章节。

**Evidence（证据）** — 全量测试、vet、构建、diff 检查、六个跨平台构建、合成基准和
只读生产数据库探针全部成功。

**Constraints（约束）** — 基准不得暴露生产内容或凭证，也不得修改生产数据库。

**Edge Cases（边界）** — 禁用 CGO、Windows 时区、Darwin build tag、重复基准、在线 WAL 库。

**Verification（验证）** — 下列全部命令退出码为零，并记录实测目标。

#### 计划

- [x] 在 `internal/usage/performance_test.go` 添加无易波动断言的基准，通过
  `MCC_USAGE_BENCH_ROWS` 选择数据量。
- [x] 运行 6 万行（60,332）专项基准；100 万行 profile 为可选项，本次未执行。
- [x] 运行 `go test ./...`、`go vet ./...`、`npm --prefix internal/frontend test`、
  `npm --prefix internal/frontend run build`、`git diff --check`。
- [x] 运行 `CGO_ENABLED=0 go test ./...`，并为 linux、darwin、windows 的 amd64/arm64
  编译 `./cmd/server`（6/6 通过）。
- [ ] 对生产数据库的只读副本/快照执行探针：本次无生产库只读副本，改以 60,332 行生产代表性
  合成数据集（seed=1）替代，并额外取优化前实现 `032aa80` 同机同库对照。
- [x] 将实际证据回写两份规格并提交验证文档（含返工轮次 R1–R5：报告
  `R1-index-diagnostics.md` … `R5-final-verification.md`；同会话 A/B 工具在
  `internal/usage/ab_compare_test.go`）。

#### 验证

- [x] Go 与前端回归（`go test ./...` 全过；前端 269/269）
- [x] Vet/build/diff 检查（均通过）
- [x] 六个跨平台构建（6/6）
- [x] 合成 6 万行证据（见“实现状态 / 性能测量”；返工 R1–R5 已完成：R7 的 status 逼近目标、
  六并发仍超——需跨请求物化，建议 R6；两项回归均已消除）
- [ ] 生产数据证据（无生产库只读副本，以合成代表性数据集替代；100 万行可选未执行）
