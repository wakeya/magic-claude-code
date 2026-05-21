# SQLite WAL Mode & Concurrency Optimization

**Version**: 0.2
**Date**: 2026-05-21
**Status**: Pending Review

---

## 1. Background

The project uses SQLite for config and usage data storage (`internal/config/sqlite_store.go` and `internal/usage/store.go`), but has several performance issues:

1. **WAL mode not enabled**: Using the default delete journal mode, writes block all reads.
2. **Connection pool limited to 1**: `config.SQLiteStore` calls `db.SetMaxOpenConns(1)`, fully serializing all reads and writes.
3. **`synchronous` not set to NORMAL**: Using the default FULL level, which calls `fsync()` on every transaction commit — unnecessary overhead under WAL mode.
4. **`busy_timeout` not set**: Write conflicts immediately return `database is locked` instead of waiting and retrying.

On the proxy request hot path, `usage.Store.Record()` writes usage data. With concurrent proxy requests > 1, single-connection serialization becomes a bottleneck.

---

## 2. Goals

1. Enable `PRAGMA journal_mode=WAL` for non-blocking concurrent reads and writes.
2. Set `PRAGMA synchronous=NORMAL` to safely reduce `fsync` calls under WAL mode.
3. Set `PRAGMA busy_timeout=5000` to wait up to 5 seconds on write conflicts instead of failing immediately.
4. Set connection pool to 8, allowing concurrent reads across multiple connections.

---

## 3. Non-Goals

1. No additional database engines or ORMs.
2. No changes to existing table structures or query logic.
3. No read-write splitting or connection sharding — SQLite's WAL mode provides sufficient concurrent read capability.

---

## 4. PRAGMA Details

### 4.1 `PRAGMA journal_mode=WAL`

| Aspect | Description |
|--------|-------------|
| Purpose | Writes go to a `.wal` file first; reads merge from the original database + WAL |
| Concurrency | Reads don't block writes, writes don't block reads (writes are still mutually exclusive) |
| Side effects | Produces `.db-wal` and `.db-shm` files; database file cannot be moved cross-process without checkpoint |
| Persistence | WAL mode is a persistent setting — survives database close/reopen, no need to set on every connection |

### 4.2 `PRAGMA synchronous=NORMAL`

| Aspect | Description |
|--------|-------------|
| Purpose | Reduces `fsync()` call frequency |
| Safety | Nearly equivalent to FULL under WAL mode — only the WAL file can be corrupted, not the main database; auto-recovery on restart |
| Risk | Extremely unlikely (OS crash during WAL header write) to lose the last transaction |
| Reference | SQLite official docs recommend WAL + NORMAL combination |

### 4.3 `PRAGMA busy_timeout=5000`

| Aspect | Description |
|--------|-------------|
| Purpose | Wait up to 5 seconds on write conflicts instead of immediately returning `database is locked` |
| Scenario | Two concurrent proxy requests triggering `usage.Record()` writes simultaneously |

### 4.4 DSN Inline PRAGMA (`_pragma` parameter)

| Aspect | Description |
|--------|-------------|
| Purpose | Set PRAGMAs via `_pragma` in the DSN; the driver auto-executes them when creating **each new connection** |
| Problem solved | `foreign_keys` is a per-connection setting (not database-level); new connections from an expanded pool won't inherit PRAGMAs from existing connections |
| Syntax | `file:path.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&...` |

---

## 5. Current vs. Target State

| Setting | Current | Target |
|---------|---------|--------|
| `journal_mode` | delete (default) | WAL |
| `synchronous` | FULL (default) | NORMAL |
| `busy_timeout` | 0 (default) | 5000ms |
| `foreign_keys` | ON (manual Exec) | ON (DSN auto-set) |
| `MaxOpenConns` | 1 | 8 |
| `MaxIdleConns` | 0 (default 2) | 8 |
| PRAGMA mechanism | `db.Exec("PRAGMA ...")` | DSN `_pragma` parameters |

---

## 6. Constraints & Risks

1. **WAL file size**: Under high write load, `.db-wal` may grow. SQLite auto-reclaims via checkpoint; manual intervention is rarely needed.
2. **Container environment**: WAL auto-checkpoints on container restart; no data loss risk.
3. **Backward compatibility**: Once WAL is enabled, the database file cannot be opened by SQLite libraries that don't support WAL. The project uses `modernc.org/sqlite` (pure Go), which always supports WAL.
4. **`foreign_keys` per-connection issue**: With 8 connections, DSN `_pragma` must be used to ensure every new connection enables foreign key constraints. Otherwise, pool-created connections may skip FK checks.

---

## 7. Database Portability Notes

### 7.1 `INSERT OR IGNORE` — SQLite-Specific Syntax

`recordIfAbsent` uses `INSERT OR IGNORE INTO` for atomic deduplication. This syntax is **SQLite-specific** and must be adapted when switching to other databases:

| Database | Syntax |
|----------|--------|
| SQLite | `INSERT OR IGNORE INTO ...` |
| MySQL | `INSERT IGNORE INTO ...` |
| PostgreSQL | `INSERT INTO ... ON CONFLICT (id) DO NOTHING` |

### 7.2 DSN `_pragma` — SQLite-Specific Mechanism

PRAGMA settings via DSN `_pragma=...` are **SQLite-specific**. Other databases configure these equivalently through:

| SQLite PRAGMA | PostgreSQL Equivalent | MySQL Equivalent |
|---------------|----------------------|------------------|
| `journal_mode=WAL` | `wal_level=replica` (postgresql.conf) | `innodb_flush_log_at_trx_commit=2` |
| `synchronous=NORMAL` | `synchronous_commit=off` | Default is sufficient |
| `busy_timeout=5000` | `lock_timeout=5000ms` | `lock_wait_timeout=5` |
| `foreign_keys=ON` | Enabled by default | Checked by FK checker |
| DSN `_pragma` | `connect_timeout`, `sslmode` etc. in connection string | Session variables |

---

## 8. Acceptance Criteria

1. `.db-wal` and `.db-shm` files exist after application startup.
2. `PRAGMA journal_mode` returns `wal`.
3. `PRAGMA synchronous` returns `1` (NORMAL).
4. `PRAGMA busy_timeout` returns `5000`.
5. `PRAGMA foreign_keys` returns `1` on any connection in the pool.
6. All existing tests pass.
7. Concurrent proxy request writes to usage data do not produce `database is locked` errors.
8. `recordIfAbsent` concurrency test passes — duplicate records are silently skipped, no errors.
