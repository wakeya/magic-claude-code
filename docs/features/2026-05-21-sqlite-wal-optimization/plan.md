# Implementation Plan

## Affected Files

| File | Change | Description |
|------|--------|-------------|
| `internal/config/sqlite_store.go` | Modify | Inline PRAGMAs in DSN, adjust connection pool, remove manual PRAGMA Exec from init() |
| `internal/usage/store.go` | Modify | Remove redundant `PRAGMA foreign_keys` (auto-set via DSN) |

## Steps

### Step 1: Modify `internal/config/sqlite_store.go` — DSN Inline PRAGMA

Replace manual `db.Exec("PRAGMA ...")` calls with DSN parameters, ensuring every new connection in the pool is automatically initialized:

```go
func NewSQLiteStore(dbPath string, legacyJSONPath string) (*SQLiteStore, error) {
    // ... directory and file-existence checks unchanged

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

Also remove the `PRAGMA foreign_keys = ON` line from `init()` (now set via DSN).

### Step 2: Add `fmt` import

`fmt.Sprintf` requires the `fmt` package. Check existing imports and add if missing.

### Step 3: Clean up `internal/usage/store.go`

Remove `` `PRAGMA foreign_keys = ON;` `` from the `Migrate()` stmts list. The PRAGMA is now automatically applied to every connection via the DSN.

### Step 4: Verify

```bash
# Run all tests
go test ./...

# Manually verify PRAGMA settings (after starting the service)
sqlite3 data/proxy.db "PRAGMA journal_mode; PRAGMA synchronous; PRAGMA busy_timeout;"
```

## Key Design Decisions

### Why DSN `_pragma` over `db.Exec()`

`db.Exec("PRAGMA ...")` only takes effect on the **single connection** it executes on. `foreign_keys` is a per-connection setting (not database-level); new connections created by the pool won't inherit it.

This was not an issue with `MaxOpenConns(1)` (always one connection). After expanding to 8, new connections could skip foreign key constraint checks, creating data integrity risks.

DSN `_pragma` parameters are executed by the driver on each new connection creation, requiring no additional code.

> **Portability note**: DSN `_pragma` is SQLite-specific. When switching databases, replace with target-specific connection initialization (e.g., PostgreSQL `SET` via `afterConnect` hook, MySQL session variables).

### Why `MaxOpenConns(8)`

- SQLite uses a single-writer model; write throughput does not increase with more connections.
- 8 connections primarily serve concurrent read scenarios (admin panel queries + background session log sync + proxy request reads).
- For a single-machine proxy at this scale, 8 is sufficient without causing excessive lock contention.

### `INSERT OR IGNORE` for Atomic Dedup in `recordIfAbsent`

Replaced the old SELECT-then-INSERT pattern with `INSERT OR IGNORE` + `RowsAffected()`:

- **Old**: `SELECT COUNT(*)` → check → `Record()` (2 DB roundtrips, TOCTOU race window)
- **New**: `INSERT OR IGNORE` → `RowsAffected()` (1 DB roundtrip, atomic, no race)

Performance comparison:

| Scenario | Old | New |
|----------|-----|-----|
| No conflict (insert) | 2 index lookups, 1 write, 2 roundtrips | 1 index lookup, 1 write, 1 roundtrip |
| Conflict (duplicate) | 2 index lookups, 0 writes, 2 roundtrips | 1 index lookup, 0 writes, 1 roundtrip |

> **Portability note**: `INSERT OR IGNORE` is SQLite-specific. When switching databases:
> - MySQL: `INSERT IGNORE INTO ...`
> - PostgreSQL: `INSERT INTO ... ON CONFLICT (id) DO NOTHING`
>
> `RowsAffected()` behavior is consistent across Go `database/sql` drivers.

## Unchanged Files

- `cmd/server/main.go`: No changes needed; `configStore.DB()` already passes to usage store.
- Test files: Existing tests do not depend on journal_mode settings.
- Docker configuration: No impact.
