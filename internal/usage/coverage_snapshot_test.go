package usage

// M-2 可控并发回归测试：Coverage 的 summary/status 两次查询在 WAL 下必须读到同一
// reader snapshot。旧实现（store.go Coverage 两次独立 s.db.Query）在两次查询之间
// 若有 writer 提交，summary 读到旧快照（Total/WithoutUsage 分子分母不含新行）、
// status 读到新快照（usage_parse_status 状态分布混入新行），返回结构不对应同一
// 筛选结果（top_usage_parse_status 被污染；反向时序下新分组的解析状态会被
// `if group, ok := groups[key]; ok` 静默丢弃）。修复后两条查询在同一只读事务内
// 共享同一快照（事务结束回滚），本测试通过 driver 拦截器在两次查询之间精确插入
// 一次 writer 提交，锁定该不变量。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// coverageStatusQueryMarker 是 Coverage status 查询独有的 GROUP BY 列集
// （含 t.usage_parse_status 的 5 列）；summary 查询的 GROUP BY 只有分组键 4 列，
// 不含该标记。拦截器据此区分两次查询。
const coverageStatusQueryMarker = "GROUP BY r.provider_name, r.provider_api_url, r.mapped_model, r.source_entrypoint, t.usage_parse_status"

// coverageBarrier 在 Coverage reader 的 summary 与 status 两次查询之间插入可控提交点：
// statusAttempted 在 status 查询被调用时关闭（此时 summary 查询已被完整消费）；
// releaseStatus 关闭后 status 查询才真正下发执行。测试在两者之间提交一条 writer，
// 使旧实现（两次独立 db.Query）的 status 查询必然读到新快照、summary 读到旧快照；
// 修复后（同一只读事务）两条查询共享事务快照，不受该提交影响。
type coverageBarrier struct {
	statusAttempted chan struct{}
	releaseStatus   chan struct{}
	statusOnce      sync.Once
	releaseOnce     sync.Once
}

func newCoverageBarrier() *coverageBarrier {
	return &coverageBarrier{
		statusAttempted: make(chan struct{}),
		releaseStatus:   make(chan struct{}),
	}
}

// blockOnStatus 在 status 查询执行前阻塞，直到测试提交 writer 后放行；其余查询直通。
func (b *coverageBarrier) blockOnStatus(query string) {
	if !strings.Contains(query, coverageStatusQueryMarker) {
		return
	}
	b.statusOnce.Do(func() { close(b.statusAttempted) })
	<-b.releaseStatus
}

// waitStatusAttempted 等待 status 查询被调用（等价于 summary 查询已完整执行完）。
func (b *coverageBarrier) waitStatusAttempted(t *testing.T) {
	t.Helper()
	select {
	case <-b.statusAttempted:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout: Coverage status query was never attempted (summary query did not complete)")
	}
}

func (b *coverageBarrier) release() {
	b.releaseOnce.Do(func() { close(b.releaseStatus) })
}

// coverageBarrierConnector 包装 modernc sqlite 驱动：每个新连接包一层查询拦截器，
// 使测试能在 Coverage 的 summary/status 两次查询之间精确提交 writer。
type coverageBarrierConnector struct {
	dsn     string
	barrier *coverageBarrier
}

func (c *coverageBarrierConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := (&sqlite.Driver{}).Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &coverageBarrierConn{Conn: conn, barrier: c.barrier}, nil
}

func (c *coverageBarrierConnector) Driver() driver.Driver { return coverageBarrierDriver{} }

// coverageBarrierDriver 仅供 sql.OpenDB 的 connector.Driver() 占位，Open 不会被调用。
type coverageBarrierDriver struct{}

func (coverageBarrierDriver) Open(string) (driver.Conn, error) {
	panic("coverageBarrierDriver.Open must not be called")
}

type coverageBarrierConn struct {
	driver.Conn
	barrier *coverageBarrier
}

func (c *coverageBarrierConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &coverageBarrierStmt{Stmt: stmt, query: query, barrier: c.barrier}, nil
}

func (c *coverageBarrierConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	stmt, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &coverageBarrierStmt{Stmt: stmt, query: query, barrier: c.barrier}, nil
}

func (c *coverageBarrierConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.barrier.blockOnStatus(query)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *coverageBarrierConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *coverageBarrierConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *coverageBarrierConn) Ping(ctx context.Context) error {
	return c.Conn.(driver.Pinger).Ping(ctx)
}

func (c *coverageBarrierConn) IsValid() bool {
	return c.Conn.(driver.Validator).IsValid()
}

func (c *coverageBarrierConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}

type coverageBarrierStmt struct {
	driver.Stmt
	query   string
	barrier *coverageBarrier
}

func (s *coverageBarrierStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.barrier.blockOnStatus(s.query)
	return s.Stmt.Query(args)
}

func (s *coverageBarrierStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.barrier.blockOnStatus(s.query)
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
}

func (s *coverageBarrierStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

// TestCoverageSummaryStatusShareSnapshotUnderConcurrentWALWrite 是 M-2 的可控并发回归
// 测试：在 Coverage reader 的 summary 与 status 两次查询之间，精确提交一条与已有
// 分组相同、无 usage、usage_parse_status='error' 的 writer 行。旧实现两次独立
// db.Query 在 WAL 下分别取快照：summary 读旧快照（Total/WithoutUsage 不含新行），
// status 读新快照（状态分布混入 error 行），top_usage_parse_status 被污染、分子分母
// 与状态分布不对应同一筛选结果；修复后两条查询在同一只读事务内共享同一 reader
// snapshot，返回结果与 writer 提交前的一致快照逐字段相同（legacyOracleCoverage
// 差分），且提交后再查询可见新行（事务快照随事务结束释放，不跨调用泄漏）。
func TestCoverageSummaryStatusShareSnapshotUnderConcurrentWALWrite(t *testing.T) {
	barrier := newCoverageBarrier()
	dbPath := filepath.Join(t.TempDir(), "coverage-wal.db")
	dsn := "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db := sql.OpenDB(&coverageBarrierConnector{dsn: dsn, barrier: barrier})
	db.SetMaxOpenConns(4) // reader 事务占 1 连接时 writer 仍能拿到连接提交，避免死锁
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	base := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	alpha := func(id string, started time.Time) RequestRecord {
		req := testUsageRequest(id, started)
		req.ProviderID = "prov-alpha"
		req.ProviderName = "Coverage Alpha"
		req.ProviderAPIURL = "https://alpha.example.com/api"
		req.MappedModel = "model-alpha"
		req.OriginalModel = "model-alpha"
		req.SourceEntrypoint = "cli"
		return req
	}
	record := func(req RequestRecord, tok TokenRecord) {
		t.Helper()
		if err := store.Record(req, tok); err != nil {
			t.Fatalf("Record(%q) error = %v", req.ID, err)
		}
	}
	// 已有分组 Alpha：1 条有 usage（ok），1 条无 usage（missing）。
	record(alpha("cov-w-a1", base), dedupeToken("cov-w-a1", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 1}))
	record(alpha("cov-w-a2", base.Add(time.Minute)), dedupeToken("cov-w-a2", UsageSourceNone, ParseStatusMissing, UsageValues{}))

	filter := Filter{StatsScope: StatsScopeEffective}

	// writer 提交前的一致快照期望值：修复后 reader 的只读事务在此刻开始，两次查询
	// 都应看到该状态（Total=2/WithoutUsage=1/Top=missing）。
	want := legacyOracleCoverage(t, db, filter)

	// Coverage reader 在 goroutine 中运行；拦截器在 status 查询处暂停，等待测试在
	// summary 与 status 之间提交 writer。
	type covResult struct {
		rows []CoverageRow
		err  error
	}
	done := make(chan covResult, 1)
	go func() {
		rows, err := store.Coverage(filter)
		done <- covResult{rows: rows, err: err}
	}()

	// 等 status 查询被调用（summary 已完整消费）后，提交与 Alpha 同组、无 usage、
	// usage_parse_status='error' 的 writer 行（'error' 字典序小于 'missing'，同数
	// 决胜时必翻转 top_usage_parse_status，使旧实现的可观测污染确定复现）。
	barrier.waitStatusAttempted(t)
	record(alpha("cov-w-a3", base.Add(2*time.Minute)), dedupeToken("cov-w-a3", UsageSourceNone, "error", UsageValues{}))
	barrier.release()

	res := <-done
	if res.err != nil {
		t.Fatalf("Coverage() error = %v", res.err)
	}
	for _, row := range res.rows {
		if row.WithoutUsageRequests > 0 && row.TopUsageParseStatus == "" {
			t.Fatalf("group %q has without_usage=%d but empty top_usage_parse_status: %#v",
				coverageSortKey(row), row.WithoutUsageRequests, row)
		}
	}
	if !reflect.DeepEqual(res.rows, want) {
		t.Fatalf("Coverage() under concurrent write between summary/status = %#v,\nwant consistent pre-write snapshot %#v", res.rows, want)
	}

	// 提交后的一致性自检：writer 提交之后的新调用必须看到新快照（两条查询都在提交
	// 之后），证明只读事务快照随事务结束释放、不跨调用泄漏。
	after, err := store.Coverage(filter)
	if err != nil {
		t.Fatalf("Coverage(after commit) error = %v", err)
	}
	wantAfter := legacyOracleCoverage(t, db, filter)
	if !reflect.DeepEqual(after, wantAfter) {
		t.Fatalf("Coverage() after commit = %#v, want post-write snapshot %#v", after, wantAfter)
	}
}
