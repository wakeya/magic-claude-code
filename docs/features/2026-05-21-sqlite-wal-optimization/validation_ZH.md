# 验证清单

## 自动化验证

- [ ] `go test ./...` 全部通过
- [ ] `go test ./internal/config/... -v` 配置存储测试通过
- [ ] `go test ./internal/usage/... -v` 用量存储测试通过

## 手动验证

- [ ] 启动服务后，`data/proxy.db-wal` 文件存在
- [ ] 启动服务后，`data/proxy.db-shm` 文件存在
- [ ] `sqlite3 data/proxy.db "PRAGMA journal_mode;"` 返回 `wal`
- [ ] `sqlite3 data/proxy.db "PRAGMA synchronous;"` 返回 `1`
- [ ] `sqlite3 data/proxy.db "PRAGMA busy_timeout;"` 返回 `5000`
- [ ] 管理面板正常加载配置
- [ ] 代理请求正常转发并记录用量

## 并发验证（可选）

- [ ] 多个并发代理请求通过代理时，用量数据正确记录，无 `database is locked` 错误

## 数据库可移植性测试

这些测试验证 `recordIfAbsent` 在并发写入下的行为。切换数据库时，需适配 SQL 语法后重新运行。

### 测试用例 1：原子去重 — `INSERT OR IGNORE`

- [ ] **前置**：打开一个测试数据库
- [ ] **操作**：并发调用 `recordIfAbsent` 两次，使用相同的 request ID
- [ ] **预期**：`usage_requests` 表中恰好 1 条记录，不返回错误
- [ ] **可移植**：将 `INSERT OR IGNORE` 改为目标数据库语法（MySQL: `INSERT IGNORE`，PostgreSQL: `ON CONFLICT DO NOTHING`），重新运行

### 测试用例 2：`RowsAffected()` 一致性

- [ ] **操作**：用新 ID 调用 `recordIfAbsent` → 期望 `RowsAffected() == 1`
- [ ] **操作**：用相同 ID 再次调用 → 期望 `RowsAffected() == 0`
- [ ] **预期**：`(bool, error)` 分别返回 `(true, nil)` 和 `(false, nil)`
- [ ] **可移植**：`sql.Result.RowsAffected()` 是 `database/sql` 标准接口，各驱动行为一致

### 测试用例 3：并发写入 — 无数据丢失

- [ ] **操作**：启动 N 个 goroutine，每个用**唯一** ID 调用 `recordIfAbsent`
- [ ] **预期**：N 条记录全部插入成功，无 `database is locked` 错误
- [ ] **可移植**：SQLite WAL 模式处理此场景；PostgreSQL/MySQL 原生支持行级锁
