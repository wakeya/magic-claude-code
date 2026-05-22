# SQLite WAL 模式与并发优化

**版本**: 0.2
**日期**: 2026-05-21
**状态**: 待审核

---

## 1. 背景

当前项目使用 SQLite 存储配置和用量数据（`internal/config/sqlite_store.go` 和 `internal/usage/store.go`），但存在两个性能隐患：

1. **未开启 WAL 模式**：使用默认的 delete journal 模式，写操作阻塞所有读操作。
2. **连接池限制为 1**：`config.SQLiteStore` 构造时调用 `db.SetMaxOpenConns(1)`，所有读写完全串行化。
3. **未设置 `synchronous=NORMAL`**：使用默认的 FULL 级别，每次事务提交都执行 `fsync()`，在 WAL 模式下是不必要的性能开销。
4. **未设置 `busy_timeout`**：写冲突时立即返回 `database is locked` 错误，而非等待重试。

在代理请求热路径上，`usage.Store.Record()` 需要写入用量数据。当并发代理请求 > 1 时，单连接串行化成为瓶颈。

---

## 2. 目标

1. 开启 `PRAGMA journal_mode=WAL`，实现读不阻塞写、写不阻塞读。
2. 设置 `PRAGMA synchronous=NORMAL`，在 WAL 模式下安全地减少 `fsync` 调用。
3. 设置 `PRAGMA busy_timeout=5000`，写冲突时等待 5 秒而非立即报错。
4. 连接池设为 8，允许多连接并发读。

---

## 3. 非目标

1. 不引入额外的数据库引擎或 ORM。
2. 不改变现有的表结构或查询逻辑。
3. 不实现读写分离或连接分片——SQLite 的 WAL 模式本身就提供了足够的并发读能力。

---

## 4. PRAGMA 说明

### 4.1 `PRAGMA journal_mode=WAL`

| 方面 | 说明 |
|------|------|
| 作用 | 写操作先写入 `.wal` 文件，读操作从原始数据库 + WAL 合并读取 |
| 并发效果 | 读不阻塞写，写不阻塞读（写与写之间仍然互斥） |
| 副作用 | 产生 `.db-wal` 和 `.db-shm` 文件；数据库文件不能再跨进程移动（需先 checkpoint） |
| 持久性 | WAL 模式是持久设置，数据库关闭后重新打开仍然是 WAL，无需每次设置 |

### 4.2 `PRAGMA synchronous=NORMAL`

| 方面 | 说明 |
|------|------|
| 作用 | 减少 `fsync()` 调用频率 |
| 安全性 | WAL 模式下几乎等同 FULL——损坏的仅是 WAL 文件而非主库，重启自动恢复 |
| 风险 | 极小概率（OS 在写入 WAL 头部时崩溃）丢失最后一个事务 |
| 参考 | SQLite 官方文档推荐 WAL + NORMAL 组合 |

### 4.3 `PRAGMA busy_timeout=5000`

| 方面 | 说明 |
|------|------|
| 作用 | 写冲突时等待最多 5 秒，而非立即返回 `database is locked` |
| 场景 | 两个并发代理请求同时触发 `usage.Record()` 写入时 |

### 4.4 DSN 内联 PRAGMA（`_pragma` 参数）

| 方面 | 说明 |
|------|------|
| 作用 | 在 DSN 中通过 `_pragma` 参数设置 PRAGMA，驱动在**每个新连接**创建时自动执行 |
| 解决的问题 | `foreign_keys` 是连接级设置（非数据库级），连接池扩容后新连接不会继承旧连接的 PRAGMA |
| 语法 | `file:path.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&...` |

---

## 5. 当前状态与目标状态对比

| 配置项 | 当前 | 目标 |
|--------|------|------|
| `journal_mode` | delete（默认） | WAL |
| `synchronous` | FULL（默认） | NORMAL |
| `busy_timeout` | 0（默认） | 5000ms |
| `foreign_keys` | ON（Exec 手动设置） | ON（DSN 自动设置） |
| `MaxOpenConns` | 1 | 8 |
| `MaxIdleConns` | 0（默认 2） | 8 |
| PRAGMA 设置方式 | `db.Exec("PRAGMA ...")` | DSN `_pragma` 参数 |

---

## 6. 约束与风险

1. **WAL 文件体积**：高写入负载下 `.db-wal` 可能增长。SQLite 会在 checkpoint 时自动回收，一般不需要手动干预。
2. **容器环境**：容器重启时 WAL 自动 checkpoint，不存在数据丢失风险。
3. **向后兼容**：开启 WAL 后，同一数据库文件不能被不支持 WAL 的旧版 SQLite 库打开。项目使用 `modernc.org/sqlite`（纯 Go 实现），始终支持 WAL。
4. **`foreign_keys` 连接级问题**：扩容到 8 连接后，必须通过 DSN `_pragma` 确保每个新连接都启用外键约束，否则连接池创建的新连接可能跳过外键检查。

---

## 7. 数据库可移植性说明

### 7.1 `INSERT OR IGNORE` — SQLite 专有语法

`recordIfAbsent` 使用 `INSERT OR IGNORE INTO` 实现原子去重。此语法**仅 SQLite 支持**，切换其他数据库时需适配：

| 数据库 | 语法 |
|----------|------|
| SQLite | `INSERT OR IGNORE INTO ...` |
| MySQL | `INSERT IGNORE INTO ...` |
| PostgreSQL | `INSERT INTO ... ON CONFLICT (id) DO NOTHING` |

### 7.2 DSN `_pragma` — SQLite 专有机制

通过 DSN `_pragma=...` 设置 PRAGMA 是 **SQLite 专有**的。其他数据库通过以下方式配置等效项：

| SQLite PRAGMA | PostgreSQL 等效配置 | MySQL 等效配置 |
|---------------|----------------------|------------------|
| `journal_mode=WAL` | `wal_level=replica` (postgresql.conf) | `innodb_flush_log_at_trx_commit=2` |
| `synchronous=NORMAL` | `synchronous_commit=off` | 默认即可 |
| `busy_timeout=5000` | `lock_timeout=5000ms` | `lock_wait_timeout=5` |
| `foreign_keys=ON` | 默认启用 | FK 检查器负责 |
| DSN `_pragma` | 连接串中 `connect_timeout`、`sslmode` 等 | 会话变量 |

---

## 8. 验收标准

1. 应用启动后，数据库文件产生 `.db-wal` 和 `.db-shm` 文件。
2. `PRAGMA journal_mode` 返回 `wal`。
3. `PRAGMA synchronous` 返回 `1`（NORMAL）。
4. `PRAGMA busy_timeout` 返回 `5000`。
5. 连接池中任意连接 `PRAGMA foreign_keys` 均返回 `1`。
6. 现有测试全部通过。
7. 代理请求并发写入 usage 数据时不报 `database is locked` 错误。
8. `recordIfAbsent` 并发测试通过——重复记录被静默跳过，无错误。
