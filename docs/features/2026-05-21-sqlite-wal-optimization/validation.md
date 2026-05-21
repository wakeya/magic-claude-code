# Validation Checklist

## Automated Verification

- [ ] `go test ./...` all pass
- [ ] `go test ./internal/config/... -v` config store tests pass
- [ ] `go test ./internal/usage/... -v` usage store tests pass

## Manual Verification

- [ ] `data/proxy.db-wal` file exists after service startup
- [ ] `data/proxy.db-shm` file exists after service startup
- [ ] `sqlite3 data/proxy.db "PRAGMA journal_mode;"` returns `wal`
- [ ] `sqlite3 data/proxy.db "PRAGMA synchronous;"` returns `1`
- [ ] `sqlite3 data/proxy.db "PRAGMA busy_timeout;"` returns `5000`
- [ ] Admin panel loads config normally
- [ ] Proxy requests forward and record usage normally

## Concurrency Verification (Optional)

- [ ] Multiple concurrent proxy requests record usage data correctly with no `database is locked` errors

## Database Portability Tests

These tests verify `recordIfAbsent` behavior under concurrent writes. When switching databases, adapt the SQL syntax and re-run these tests.

### Test Case 1: Atomic Dedup — `INSERT OR IGNORE`

- [ ] **Setup**: Open a single test database
- [ ] **Action**: Call `recordIfAbsent` twice concurrently with the same request ID
- [ ] **Expected**: Exactly 1 row in `usage_requests`, no error returned
- [ ] **Portability**: Change `INSERT OR IGNORE` to target syntax (MySQL: `INSERT IGNORE`, PostgreSQL: `ON CONFLICT DO NOTHING`), re-run

### Test Case 2: `RowsAffected()` Consistency

- [ ] **Action**: Call `recordIfAbsent` with a new ID → expect `RowsAffected() == 1`
- [ ] **Action**: Call `recordIfAbsent` with the same ID again → expect `RowsAffected() == 0`
- [ ] **Expected**: `(bool, error)` returns `(true, nil)` then `(false, nil)`
- [ ] **Portability**: `sql.Result.RowsAffected()` is standard `database/sql` — should work across all drivers

### Test Case 3: Concurrent Writes — No Data Loss

- [ ] **Action**: Launch N goroutines, each calling `recordIfAbsent` with a **unique** ID
- [ ] **Expected**: All N rows inserted, no `database is locked` errors
- [ ] **Portability**: WAL mode handles this for SQLite; PostgreSQL/MySQL handle natively with row-level locks
