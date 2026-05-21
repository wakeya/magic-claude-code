# 实现计划

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|----------|------|
| `internal/config/sqlite_store.go` | 修改 | DSN 中内联 PRAGMA，调整连接池，移除 init() 中的手动 PRAGMA Exec |
| `internal/usage/store.go` | 修改 | 删除重复的 `PRAGMA foreign_keys`（已在 DSN 中自动设置） |

## 实现步骤

### 步骤 1：修改 `internal/config/sqlite_store.go` — DSN 内联 PRAGMA

将 PRAGMA 设置从 `init()` 中的 `db.Exec()` 改为 DSN 参数，确保连接池中每个新连接都自动初始化：

```go
func NewSQLiteStore(dbPath string, legacyJSONPath string) (*SQLiteStore, error) {
    // ... 确保目录和检查文件是否存在的逻辑不变

    dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", dbPath)
    db, err := sql.Open("sqlite", dsn)
    if err != nil {
        return nil, err
    }
    db.SetMaxOpenConns(8)
    db.SetMaxIdleConns(8)

    // ...
}
```

同时从 `init()` 中移除 `PRAGMA foreign_keys = ON` 行（已在 DSN 中设置）。

### 步骤 2：添加 `fmt` 导入

`fmt.Sprintf` 需要导入 `fmt` 包。检查现有 import，若缺少则添加。

### 步骤 3：清理 `internal/usage/store.go`

从 `Migrate()` 的 `stmts` 列表中移除 `` `PRAGMA foreign_keys = ON;` ``。该 PRAGMA 已通过 DSN 在每个连接上自动生效。

### 步骤 4：验证

```bash
# 运行所有测试
go test ./...

# 手动验证 PRAGMA 设置（启动服务后）
sqlite3 data/proxy.db "PRAGMA journal_mode; PRAGMA synchronous; PRAGMA busy_timeout;"
```

## 关键设计决策

### 为什么用 DSN `_pragma` 而非 `db.Exec()`

`db.Exec("PRAGMA ...")` 只在执行时所在的**那一个连接**上生效。`foreign_keys` 是连接级设置（非数据库级），连接池创建新连接时不会继承。

`MaxOpenConns(1)` 时这不是问题（永远只有一个连接）。扩容到 8 后，新连接可能跳过外键约束检查，导致数据完整性风险。

DSN `_pragma` 参数由驱动层在每个新连接创建时自动执行，无需额外代码。

> **可移植性提醒**：DSN `_pragma` 是 SQLite 专有的。切换数据库时需替换为目标数据库的连接初始化方式（如 PostgreSQL 的 `afterConnect` 钩子、MySQL 的会话变量）。

### 为什么 `MaxOpenConns(8)`

- SQLite 单写者模型，写吞吐不因连接数增加。
- 8 个连接主要服务于并发读场景（admin 面板查询 + 后台 session log 同步 + 代理请求中的读操作）。
- 对于单机代理的规模，8 是充裕的，不会造成过多锁争用。

### `INSERT OR IGNORE` 原子去重（`recordIfAbsent`）

用 `INSERT OR IGNORE` + `RowsAffected()` 替代了旧的先查后写模式：

- **旧实现**：`SELECT COUNT(*)` → 判断 → `Record()`（2 次数据库往返，存在 TOCTOU 竞态窗口）
- **新实现**：`INSERT OR IGNORE` → `RowsAffected()`（1 次数据库往返，原子操作，无竞态）

性能对比：

| 场景 | 旧实现 | 新实现 |
|------|--------|--------|
| 未冲突（插入） | 2 次索引查找 + 1 次写入 + 2 次往返 | 1 次索引查找 + 1 次写入 + 1 次往返 |
| 冲突（重复 id） | 2 次索引查找 + 0 次写入 + 2 次往返 | 1 次索引查找 + 0 次写入 + 1 次往返 |

> **可移植性提醒**：`INSERT OR IGNORE` 是 SQLite 专有语法。切换数据库时：
> - MySQL：`INSERT IGNORE INTO ...`
> - PostgreSQL：`INSERT INTO ... ON CONFLICT (id) DO NOTHING`
>
> `RowsAffected()` 行为在 Go `database/sql` 各驱动间一致。

## 不需要修改的部分

- `cmd/server/main.go`：无需变更，`configStore.DB()` 已经传递给 usage store。
- 测试文件：现有测试不依赖 journal_mode 设置。
- Docker 配置：无影响。
