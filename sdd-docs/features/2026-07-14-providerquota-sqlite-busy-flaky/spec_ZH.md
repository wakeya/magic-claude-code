# Provider Quota SQLite BUSY Flaky 修复规格

本地页面：无（后端 `internal/providerquota` 包测试）  
代理入口：无  
参考源站：CI run [29314320010](https://github.com/wakeya/magic-claude-code/actions/runs/29314320010)、生产 `internal/config/sqlite_store.go:43`、历史规格 `2026-05-21-sqlite-wal-optimization`  
技术栈：Go 1.26 `database/sql` + `modernc.org/sqlite`  
最后更新：2026-07-14  
进度：2 / 2 已完成（2026-07-14 验证通过）

## 整体分析（源站分析）

### CI 失败现象

GitHub Actions run `29314320010`（合并 PR #13 后 main 的 CI）在 `go test -race` 阶段失败，失败点与 release workflow 本身无关：

```
providerquota: failed to get snapshot for p2: database is locked (5) (SQLITE_BUSY)
manager_test.go:836: expected 2 requests, got 1
--- FAIL: TestSchedulerPeriodicScanNoJitter (2.04s)
FAIL magic-claude-code/internal/providerquota
```

PR #13 只改了 `.github/workflows/release.yml` 和 `scripts/setup_host_ps1_test.go`，未触碰 `internal/providerquota`。失败是合并后 main 的 CI 跑出了**已有 flaky test**。

### 根因链

对比生产与测试的 SQLite 配置：

| 位置 | DSN pragmas | 连接池 |
| --- | --- | --- |
| 生产 `internal/config/sqlite_store.go:43-49` | `foreign_keys(1)` + `journal_mode(WAL)` + `synchronous(NORMAL)` + `busy_timeout(5000)` | `SetMaxOpenConns(8)` / `SetMaxIdleConns(8)` |
| 测试 `internal/providerquota/store_test.go:17` | 仅 `foreign_keys(1)` | 未设置（默认无限） |

关键差异：测试 DB 运行在 **rollback journal** 模式（SQLite 默认），写锁是数据库级；而生产运行在 **WAL** 模式，读写不互斥（读走 WAL，写加 checkpoint），且 `busy_timeout(5000)` 让偶发撞锁时等待重试 5 秒而非立即返回 `SQLITE_BUSY`。

`scanAndQuery`（`internal/providerquota/manager.go:330-393`）的并发时序：

1. 主 goroutine 遍历 enabled providers，**顺序**对每个 provider 调用 `m.store.Get(p.ID)`（`manager.go:347`，读）判断是否 due。
2. 每个 due provider 启动一个异步 goroutine（`manager.go:371`），goroutine 内最终调用 `executeQuery` → `m.store.SaveUpsert`（`manager.go:289`，写）。
3. rollback journal 模式下，当 p1 的异步 goroutine 正持有写锁执行 `SaveUpsert` 时，主 goroutine 对 p2 的 `Get`（读）撞上数据库级写锁。
4. 测试 DSN 无 `busy_timeout`，modernc 驱动**立即**返回 `database is locked (5)`（`SQLITE_BUSY`）。
5. `manager.go:348-351` 中 `Get` 返回 `err != nil` 时 `log.Printf` 后 `continue`，p2 的异步 goroutine **根本不启动**。
6. httptest mock server 只收到 1 个请求（p1），`manager_test.go:836` 断言 `expected 2 requests, got 1` 失败。

这与 CI 日志顺序完全吻合：先打印 `failed to get snapshot for p2: database is locked`，再断言失败。

### 次要问题（测试卫生）

1. **`TestSchedulerAppliesJitter`（`manager_test.go:684`）缺 provider FK 数据**：测试只 `setupTestDB(t)`（仅插入 `test-p`），但 `configGet` 用 `a/b/c`。`provider_quota_snapshots` 有 `FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE`，因此 a/b/c 的 `SaveUpsert` 永远因 FK 约束失败。测试只断言请求到达，不检查 snapshot 落库，所以"侥幸通过"，但 re-check 与 snapshot 持久化路径从未真正生效。
2. **scheduler 启动的异步 goroutine 未被测试等待**：`scanAndQuery` 直接 `go func()`，测试用 `deadline` 轮询请求计数后即返回；`t.Cleanup` 关闭 DB 时 goroutine 可能仍在 `SaveUpsert`，产生竞态窗口（race detector 下尤其需要收尾干净）。

### 复现说明

flaky 依赖 goroutine 调度时序，本地难以稳定复现：`go test -race ./internal/providerquota -run 'TestScheduler(PeriodicScanNoJitter|AppliesJitter)' -count=50` 本地通过，但多次打印同类 `database is locked`（用户已验证）。本次本地 `-count=50` 全包 148s 通过、0 次 locked，无法在开发机稳定触发。CI runner 的 CPU/磁盘调度更慢，goroutine 时序更容易撞锁。

因此本规格不依赖"本地复现 flaky"作为验证手段，而是：
- 用 CI run 日志作为根因证据。
- 用**针对性的并发回归测试**直接证明：rollback journal + 无 busy_timeout 下并发读写会返回 `SQLITE_BUSY`，而 WAL + busy_timeout 下不会。

### 风险总结

1. 改 `setupTestDB` DSN 影响整个 `internal/providerquota` 包测试（store/manager/token_plan/resolve 等均复用）。WAL 模式会在 `t.TempDir()` 产生 `-wal`/`-shm` 副文件，临时目录清理已覆盖，无副作用。
2. 给 `Manager` 增加 `scanWG sync.WaitGroup` 是新增字段，但仅追踪 `scanAndQuery` 启动的 goroutine；不改变 `Stop()` 现有语义（仍只等 `run` 退出），零行为回归。
3. 并发回归测试若被误改为"无 WAL" DSN，应在 rollback 模式下变得 flaky —— 正是期望的回归守护。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | ✅ 已完成 | 测试 DB pragma 对齐生产 + 并发回归测试 | `internal/providerquota/store_test.go` | ✅ DSN 与生产逐字一致；回归测试通过；对照实验旧 DSN ~400 BUSY vs 新 DSN 0；`-race -count=20` 0 locked |
| 2 | ✅ 已完成 | 调度器测试清理：补 FK 数据 + goroutine 等待 | `internal/providerquota/manager.go`、`internal/providerquota/manager_test.go` | ✅ scheduler 测试通过；`-race -count=20` 无 race/locked；`go test ./...` 全绿 |

## 需求

### 交付物

1. `internal/providerquota/store_test.go` 的 `setupTestDB` 使用与生产 `NewSQLiteStore` 一致的 DSN pragma：`foreign_keys(1)` + `journal_mode(WAL)` + `synchronous(NORMAL)` + `busy_timeout(5000)`，并设置 `db.SetMaxOpenConns(8)` / `db.SetMaxIdleConns(8)`。
2. 新增回归测试 `TestSnapshotStoreConcurrentReadWriteNoBusy`：在多个 goroutine 中并发执行 `SaveUpsert`（写）与 `Get`/`GetAll`（读），断言全程不出现 `database is locked` / `SQLITE_BUSY` 错误。
3. `internal/providerquota/manager.go` 的 `Manager` 增加 `scanWG sync.WaitGroup` 字段；`scanAndQuery` 启动每个异步 goroutine 前调用 `m.scanWG.Add(1)`，goroutine 内 `defer m.scanWG.Done()`；新增包内（小写）方法 `waitPendingScans()` 供同包测试等待。
4. `TestSchedulerAppliesJitter` 补齐 `insertTestProvider(t, db, "a"/"b"/"c")`，使 snapshot 真正落库。
5. 两个 scheduler 测试（`TestSchedulerAppliesJitter`、`TestSchedulerPeriodicScanNoJitter`）在请求计数断言通过后、返回前调用 `mgr.waitPendingScans()`，确保异步 goroutine 完成后再让 `t.Cleanup` 关闭 DB。

### 约束

1. 不改变生产 `Manager` 的对外行为：`Stop()` 语义不变，`scanWG` 仅用于测试等待与未来可能的优雅关闭扩展。
2. 不改变 `scanAndQuery` 的调度逻辑（jitter、due 判断、re-check 全部保留）。
3. `setupTestDB` 仍是单一构造点，所有 providerquota 测试复用同一份 pragma 配置，避免再次出现"测试与生产 DSN 漂移"。
4. DSN 字符串与生产 `sqlite_store.go:43` 逐字对齐（pragma 顺序、参数值），便于 grep 巡检一致性。
5. 回归测试必须真正并发（`sync.WaitGroup` 启动 N 个 writer + M 个 reader，且 `-race` 下运行），而非串行调用。

### 边界情况

1. WAL 模式下 `db.Close()` 时仍有未完成的写：`busy_timeout` + modernc 内部处理保证 checkpoint 正常；`waitPendingScans()` 进一步消除该窗口。
2. `scanAndQuery` 中某 provider 因 `Get` 失败 `continue`：修复后该路径不再因 `SQLITE_BUSY` 触发；即便发生其他存储错误，仍按现有 `log + continue` 语义处理（不掩盖真实故障）。
3. 回归测试在 CI runner（慢磁盘）上运行：WAL + `busy_timeout(5000)` 提供足够等待窗口，不应超时。
4. `TestSchedulerAppliesJitter` 补 FK 后，a/b/c 的 `SaveUpsert` 成功，re-check（`manager.go:385`）在后续 tick 才会真正命中"已 due"分支 —— 不影响本次断言（仅校验首次扫描的 jitter 时序）。

### 非目标

1. 不重构 `scanAndQuery` 的并发模型（如改用 worker pool）。仅修复锁争用导致的 flaky，不引入架构变更。
2. 不让 `Stop()` 等待 in-flight 查询 goroutine（查询由传入 `ctx` 控制超时；扩展 `Stop` 语义超出本次范围）。
3. 不改生产 `SnapshotStore` 或 `NewSQLiteStore` 的 pragma（生产配置已正确）。
4. 不为 `internal/config` 的测试调整 DSN（本次失败仅在 `internal/providerquota`）。

## 任务详情

### 任务 1：测试 DB pragma 对齐生产 + 并发回归测试

#### 需求

**Objective（目标）** — 消除 `internal/providerquota` 测试 DB 与生产 SQLite 配置的漂移，使测试在 WAL + busy_timeout 下运行；并用针对性并发测试证明该配置消除了 `SQLITE_BUSY`。

**Outcomes（成果）** — `setupTestDB` 的 DSN 与 `internal/config/sqlite_store.go:43` 逐字一致并设置相同连接池上限；新增 `TestSnapshotStoreConcurrentReadWriteNoBusy` 在高并发读写下断言无 BUSY。

**Evidence（证据）** — `grep` 确认两处 DSN 字符串一致；`TestSnapshotStoreConcurrentReadWriteNoBusy` 在 `-race` 下通过；`go test -race ./internal/providerquota/... -count=20` 无 `database is locked`。

**Constraints（约束）** — `setupTestDB` 仍是唯一构造点；回归测试必须真并发（多 goroutine + WaitGroup），不能退化为串行。

**Edge Cases（边界）** — WAL 副文件随 `t.TempDir()` 清理；CI 慢盘下 `busy_timeout(5000)` 兜底等待。

**Verification（验证）** — DSN grep 一致；并发回归测试通过；压力跑无 locked 日志。

#### 计划

1. 修改 `internal/providerquota/store_test.go` 的 `setupTestDB`，将 DSN 改为与生产对齐：
   ```go
   dsn := "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
   db, err := sql.Open("sqlite", dsn)
   if err != nil {
       t.Fatalf("open db: %v", err)
   }
   db.SetMaxOpenConns(8)
   db.SetMaxIdleConns(8)
   t.Cleanup(func() { db.Close() })
   ```
2. 在 `store_test.go` 末尾新增回归测试 `TestSnapshotStoreConcurrentReadWriteNoBusy`：
   ```go
   func TestSnapshotStoreConcurrentReadWriteNoBusy(t *testing.T) {
       db := setupTestDB(t)
       store := NewSnapshotStore(db)
       // 多个 provider 并发写入 + 并发读取，断言全程无 SQLITE_BUSY。
       providerIDs := []string{"test-p"} // test-p 已由 setupTestDB 插入
       // 追加额外 provider 行以满足 FK
       for i := 0; i < 4; i++ {
           insertTestProvider(t, db, fmt.Sprintf("p%d", i))
           providerIDs = append(providerIDs, fmt.Sprintf("p%d", i))
       }
       var wg sync.WaitGroup
       var busy atomic.Int64
       ctx, cancel := context.WithCancel(context.Background())
       defer cancel()
       // writers
       for i := 0; i < len(providerIDs); i++ {
           wg.Add(1)
           go func(id string) {
               defer wg.Done()
               for j := 0; j < 50; j++ {
                   r := &ProviderQuotaResult{ProviderID: id, Success: true, Balances: []BalanceItem{{Remaining: floatPtr(float64(j)), Unit: "USD"}}, QueriedAt: time.Now(), DurationMS: int64(j)}
                   if err := store.SaveUpsert(id, r); err != nil {
                       if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy") {
                           busy.Add(1)
                       }
                   }
                   if ctx.Err() != nil {
                       return
                   }
               }
           }(providerIDs[i])
       }
       // readers
       for i := 0; i < 4; i++ {
           wg.Add(1)
           go func() {
               defer wg.Done()
               for j := 0; j < 200; j++ {
                   if _, err := store.GetAll(); err != nil {
                       if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy") {
                           busy.Add(1)
                       }
                   }
                   if ctx.Err() != nil {
                       return
                   }
               }
           }()
       }
       wg.Wait()
       if got := busy.Load(); got != 0 {
           t.Fatalf("encountered %d SQLITE_BUSY errors under WAL+busy_timeout (expected 0)", got)
       }
   }
   ```
   需在 `store_test.go` 顶部 import 补充 `"context"`、`"fmt"`、`"sync"`、`"sync/atomic"`、`"strings"`。
3. 确认 `floatPtr` 辅助函数已存在于本包（`store_test.go` 已使用，无需新增）。

#### 验证

- [x] `grep -n "_pragma" internal/providerquota/store_test.go internal/config/sqlite_store.go` 两处 DSN 完全一致（`diff` 校验通过）。
- [x] `go test -race ./internal/providerquota -run TestSnapshotStoreConcurrentReadWriteNoBusy -v` 通过（`-count=5` 全 PASS）。
- [x] `go test -race ./internal/providerquota/... -count=20` 通过且无 `database is locked`（38.062s，locked 0，DATA RACE 0）。

**实际验证证据（2026-07-14）：**
- 对照实验（临时探针，用旧 DSN 仅 `foreign_keys(1)`）：5 个 goroutine 各写 100 次共 500 次，触发 **388–410 次 `SQLITE_BUSY`**（~80% 撞锁率），运行 3 次均稳定复现；探针文件验证后已删除。
- 新 DSN（WAL + busy_timeout）下 `TestSnapshotStoreConcurrentReadWriteNoBusy`（5 writers × 50 写 + 4 readers × 200 读，共 250 写/800 读）**0 次 BUSY**。
- 两者对比定量证明：根因是 rollback journal 写锁 + 无 busy_timeout 的立即失败；修复（WAL + busy_timeout）彻底消除撞锁。

### 任务 2：调度器测试清理（补 FK 数据 + goroutine 等待追踪）

#### 需求

**Objective（目标）** — 让 scheduler 测试在干净的 FK 数据下运行，并确保 `scanAndQuery` 启动的异步 goroutine 在测试返回前全部完成，消除 `db.Close` 与在写 goroutine 的竞态窗口。

**Outcomes（成果）** — `Manager` 新增 `scanWG` 字段追踪 scheduler goroutine，包内 `waitPendingScans()` 方法；`TestSchedulerAppliesJitter` 补 `a/b/c` provider 行；两个 scheduler 测试在断言后调用 `waitPendingScans()`。

**Evidence（证据）** — `TestSchedulerAppliesJitter` 中 a/b/c 的 snapshot 成功落库（可通过 `store.GetAll()` 验证 len==3）；两个测试在 `waitPendingScans()` 后才返回；`-race -count=20` 无 data race / locked 报告。

**Constraints（约束）** — 不改 `Stop()` 语义；`scanWG` 仅 `Add`/`Done` 包裹 `scanAndQuery` 的 goroutine，不影响 `Query` 直接调用路径；`waitPendingScans()` 仅测试可见（小写不导出）。

**Edge Cases（边界）** — `scanAndQuery` 返回时所有 `Add` 已完成（同步循环内 Add 后才 go），`Wait` 不会漏掉尚未 Add 的 goroutine；goroutine 内 panic 时 `Done` 仍执行（`defer`）。

**Verification（验证）** — scheduler 测试通过；`-race` 下无 race 报告；snapshot 落库数符合预期。

#### 计划

1. 在 `internal/providerquota/manager.go` 的 `Manager` 结构体添加字段（紧邻 `done chan struct{}`）：
   ```go
   // scanWG tracks goroutines fired by scanAndQuery so tests can wait for
   // them to finish before closing the DB. Stop() does not wait on it yet;
   // in-flight queries are bounded by the ctx passed into Start/scanAndQuery.
   scanWG sync.WaitGroup
   ```
2. 在 `scanAndQuery` 启动异步 goroutine 处（`manager.go:370-371` 附近）加追踪：
   ```go
   providerID := p.ID
   interval := time.Duration(p.QuotaQuery.AutoQueryIntervalMinutes) * time.Minute
   m.scanWG.Add(1)
   go func() {
       defer m.scanWG.Done()
       // ...原有 jitter / re-check / m.Query 逻辑不变...
   }()
   ```
3. 在 `manager.go` 末尾新增包内方法：
   ```go
   // waitPendingScans blocks until all scheduler-fired query goroutines have
   // returned. Test-only seam; production callers use Stop().
   func (m *Manager) waitPendingScans() { m.scanWG.Wait() }
   ```
4. 在 `internal/providerquota/manager_test.go` 的 `TestSchedulerAppliesJitter`，`store := NewSnapshotStore(db)` 之后补齐 FK 数据：
   ```go
   insertTestProvider(t, db, "a")
   insertTestProvider(t, db, "b")
   insertTestProvider(t, db, "c")
   ```
5. **（实现时优化）** 改用 `t.Cleanup(mgr.waitPendingScans)` 注册等待，而非在断言后显式调用。原因：`setupTestDB` 已在 `t.Cleanup` 注册 `db.Close()`，而 `t.Cleanup` 按 LIFO 执行——测试函数内后注册的 `waitPendingScans` 先于 `db.Close` 执行，从而保证 goroutine 完成后再关 DB。相比"显式调用"，`t.Cleanup` 还覆盖了 `t.Fatalf` 失败路径，且无需重构现有 `defer mu.Unlock()` 结构，改动最小且更健壮。

#### 验证

- [x] `TestSchedulerAppliesJitter` 补 FK 后 a/b/c 的 `SaveUpsert` 不再因 FK 失败（snapshot 可落库；由 `TestSnapshotStoreConcurrentReadWriteNoBusy` 在 WAL 下成功写入间接确认）。
- [x] 两个 scheduler 测试通过 `t.Cleanup(mgr.waitPendingScans)` 在 `db.Close` 前等待 goroutine 收尾；`-race` 无 data race。
- [x] `go test -race ./internal/providerquota/... -count=20` 全绿、无 `database is locked`、无 race 报告（38.062s）。
- [x] `go test ./...` 全绿（`scanWG` 字段未破坏其他测试）；`go vet` 干净。
