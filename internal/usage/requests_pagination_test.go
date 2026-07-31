package usage

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestRequestsMatchesLegacyOracleAcrossFiltersScopesAndPages 是 Requests 分页下推后的
// 兼容性差分网：对每个筛选/口径/页大小组合，逐页比较 store.Requests 与旧算法判定器
// （legacyOracleQueryRows + 内存切片）的总数、页码、页大小、返回行（含全部字段、
// 排序、重复标记、URL 脱敏）。任何字段漂移都会失败。
func TestRequestsMatchesLegacyOracleAcrossFiltersScopesAndPages(t *testing.T) {
	store := newTestStore(t)
	base := seedRequestsDifferentialFixture(t, store)

	filterCases := []struct {
		name   string
		filter Filter
	}{
		{name: "all"},
		{name: "from", filter: Filter{From: base.Add(3 * time.Minute)}},
		{name: "to", filter: Filter{To: base.Add(5 * time.Minute)}},
		{name: "source_app", filter: Filter{SourceApp: "other-app"}},
		{name: "source_app_all", filter: Filter{SourceApp: "all"}},
		{name: "entrypoint_session", filter: Filter{SourceEntrypoint: "session_log"}},
		{name: "provider", filter: Filter{ProviderID: "provider-a"}},
		{name: "model_alpha", filter: Filter{Model: "alpha"}},
		{name: "request_path", filter: Filter{RequestPath: "/v1/messages"}},
		{name: "usage_source_provider", filter: Filter{UsageSource: UsageSourceProvider}},
		{name: "usage_source_session", filter: Filter{UsageSource: UsageSourceSessionLog}},
		{name: "parse_ok", filter: Filter{UsageParseStatus: ParseStatusOK}},
		{name: "parse_missing", filter: Filter{UsageParseStatus: ParseStatusMissing}},
		{name: "status_success", filter: Filter{Status: "success"}},
		{name: "status_error", filter: Filter{Status: "error"}},
		{name: "query", filter: Filter{Query: "boom"}},
		{name: "zero_results", filter: Filter{Query: "does-not-exist"}},
	}
	scopes := []string{"", StatsScopeEffective, StatsScopeProvider, StatsScopeSessionLog, StatsScopeRaw}
	pageSizes := []int{0, 1, 3, 7, 1000}

	for _, fc := range filterCases {
		for _, scope := range scopes {
			for _, pageSize := range pageSizes {
				probe := fc.filter
				probe.StatsScope = scope
				probe.Page = 1
				probe.PageSize = pageSize
				probeTotal := legacyOracleRequestsPage(t, store.db, probe).Total
				effectiveSize := int64(pageSizeOrDefault(pageSize))
				totalPages := int(probeTotal/effectiveSize) + 1
				if totalPages < 1 {
					totalPages = 1
				}
				// 覆盖 page=0（默认→1）、每个有效页，以及超出总数的页码。
				for page := 0; page <= totalPages+1; page++ {
					filter := fc.filter
					filter.StatsScope = scope
					filter.Page = page
					filter.PageSize = pageSize
					t.Run(fmt.Sprintf("%s/%s/page=%d_ps=%d", fc.name, scopeName(scope), page, pageSize), func(t *testing.T) {
						got, err := store.Requests(filter)
						if err != nil {
							t.Fatalf("Requests() error = %v", err)
						}
						want := legacyOracleRequestsPage(t, store.db, filter)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("Requests() = %#v\nwant legacy  = %#v", got, want)
						}
					})
				}
			}
		}
	}
}

// TestRequestsPaginationBoundaries 显式验证页码/页大小边界语义与旧实现一致：
// page<=0 归一为 1、page_size<=0 归一为 50、超出总数的页返回空行但总数正确、
// 末页不足一页时返回剩余行。
func TestRequestsPaginationBoundaries(t *testing.T) {
	store := newTestStore(t)
	seedRequestsDifferentialFixture(t, store)
	// raw 口径下固定 13 行，便于断言边界。
	rawTotal := legacyOracleRequestsPage(t, store.db, Filter{StatsScope: StatsScopeRaw}).Total
	if rawTotal != 13 {
		t.Fatalf("fixture raw total = %d, want 13 (fixture drifted)", rawTotal)
	}

	tests := []struct {
		name         string
		filter       Filter
		wantTotal    int64
		wantRowCount int
		wantPage     int
		wantPageSize int
	}{
		{
			name:         "page zero defaults to one",
			filter:       Filter{StatsScope: StatsScopeRaw, Page: 0, PageSize: 5},
			wantTotal:    13,
			wantRowCount: 5,
			wantPage:     1,
			wantPageSize: 5,
		},
		{
			name:         "negative page defaults to one",
			filter:       Filter{StatsScope: StatsScopeRaw, Page: -3, PageSize: 5},
			wantTotal:    13,
			wantRowCount: 5,
			wantPage:     1,
			wantPageSize: 5,
		},
		{
			name:         "page size zero defaults to fifty",
			filter:       Filter{StatsScope: StatsScopeRaw, Page: 1, PageSize: 0},
			wantTotal:    13,
			wantRowCount: 13,
			wantPage:     1,
			wantPageSize: 50,
		},
		{
			name:         "last partial page",
			filter:       Filter{StatsScope: StatsScopeRaw, Page: 5, PageSize: 3},
			wantTotal:    13,
			wantRowCount: 1,
			wantPage:     5,
			wantPageSize: 3,
		},
		{
			name:         "page beyond total returns empty rows",
			filter:       Filter{StatsScope: StatsScopeRaw, Page: 99, PageSize: 3},
			wantTotal:    13,
			wantRowCount: 0,
			wantPage:     99,
			wantPageSize: 3,
		},
		{
			name:         "offset lands exactly on total returns empty rows",
			filter:       Filter{StatsScope: StatsScopeRaw, Page: 2, PageSize: 13},
			wantTotal:    13,
			wantRowCount: 0,
			wantPage:     2,
			wantPageSize: 13,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.Requests(tt.filter)
			if err != nil {
				t.Fatalf("Requests() error = %v", err)
			}
			if got.Total != tt.wantTotal {
				t.Fatalf("Total = %d, want %d", got.Total, tt.wantTotal)
			}
			if len(got.Rows) != tt.wantRowCount {
				t.Fatalf("len(Rows) = %d, want %d", len(got.Rows), tt.wantRowCount)
			}
			if got.Page != tt.wantPage {
				t.Fatalf("Page = %d, want %d", got.Page, tt.wantPage)
			}
			if got.PageSize != tt.wantPageSize {
				t.Fatalf("PageSize = %d, want %d", got.PageSize, tt.wantPageSize)
			}
			// 边界页与旧算法逐字段对齐（含排序与标记）。
			want := legacyOracleRequestsPage(t, store.db, tt.filter)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Requests() = %#v\nwant legacy  = %#v", got, want)
			}
		})
	}
}

// TestRequestsNeverReturnsMoreRowsThanPageSize 证明分页在数据库层完成：即便总数远大于
// 页大小，单次 Requests 也只返回页大小数量的行，绝不物化整张表后在内存切片。
func TestRequestsNeverReturnsMoreRowsThanPageSize(t *testing.T) {
	store := newTestStore(t)
	seedRequestsDifferentialFixture(t, store)

	page, err := store.Requests(Filter{StatsScope: StatsScopeRaw, Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}
	if page.Total <= int64(page.PageSize) {
		t.Fatalf("fixture too small: Total=%d PageSize=%d (need Total>PageSize)", page.Total, page.PageSize)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want exactly PageSize=2 (pagination must happen in SQL)", len(page.Rows))
	}
}

// TestRequestsPushesCountAndLimitOffsetToSQL 是分页下推的结构性证据：Requests 的查询
// 必须以 scoped COUNT(*) 计算总数、以 LIMIT/OFFSET 取页，且页查询回连 usage_requests/
// usage_tokens 投影完整 RequestRow（含重复标记），而非全量加载后切片。
func TestRequestsPushesCountAndLimitOffsetToSQL(t *testing.T) {
	filter := Filter{
		From:             time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
		To:               time.Date(2026, 7, 30, 5, 6, 7, 0, time.UTC),
		SourceApp:        "claude_code",
		SourceEntrypoint: "cli",
		ProviderID:       "provider-a",
		Model:            "alpha",
		RequestPath:      "/v1/messages",
		UsageSource:      UsageSourceProvider,
		UsageParseStatus: ParseStatusOK,
		Status:           "success",
		Query:            "needle",
		StatsScope:       StatsScopeEffective,
	}
	countSQL, pageSQL, args := buildRequestsQueries(filter)

	if !strings.Contains(countSQL, "SELECT COUNT(*) FROM scoped") {
		t.Fatalf("count query must aggregate the scoped dataset, got:\n%s", countSQL)
	}
	if !strings.Contains(pageSQL, "LIMIT ? OFFSET ?") {
		t.Fatalf("page query must push LIMIT/OFFSET to SQLite, got:\n%s", pageSQL)
	}
	if !strings.Contains(pageSQL, "JOIN usage_requests r") || !strings.Contains(pageSQL, "JOIN usage_tokens t") {
		t.Fatalf("page query must project full RequestRow via usage_requests/usage_tokens, got:\n%s", pageSQL)
	}
	if !strings.Contains(pageSQL, "scoped.dedupe_status") || !strings.Contains(pageSQL, "scoped.dedupe_request_id") {
		t.Fatalf("page query must carry scoped dedupe markers, got:\n%s", pageSQL)
	}
	if !strings.Contains(pageSQL, "ORDER BY scoped.started_at DESC, scoped.request_id DESC") {
		t.Fatalf("page query must keep deterministic started_at/id ordering, got:\n%s", pageSQL)
	}
	// countSQL 只含筛选+口径参数；pageSQL 在其之上多出 LIMIT/OFFSET 两个占位符。
	if strings.Count(countSQL, "?") != len(args) {
		t.Fatalf("count query placeholders = %d, base args = %d", strings.Count(countSQL, "?"), len(args))
	}
	if strings.Count(pageSQL, "?") != len(args)+2 {
		t.Fatalf("page query placeholders = %d, want %d (base args+2 LIMIT/OFFSET appended by caller)",
			strings.Count(pageSQL, "?"), len(args)+2)
	}
}

// TestRequestsRedactsLegacyDirtyURLViaScopedPath 确保分页（含重复标记）路径上的输出
// 边界脱敏与旧实现一致，不泄露带 userinfo/敏感 query 的历史脏 URL。
func TestRequestsRedactsLegacyDirtyURLViaScopedPath(t *testing.T) {
	store := newTestStore(t)
	base := seedRequestsDifferentialFixture(t, store)

	page, err := store.Requests(Filter{StatsScope: StatsScopeRaw, Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}
	for _, row := range page.Rows {
		if row.ID != "err-dirty" {
			continue
		}
		for _, secret := range []string{"secret-pass", "user:secret-pass@", "token=abc"} {
			if strings.Contains(row.BackendURL, secret) {
				t.Errorf("BackendURL leaked %q: %q", secret, row.BackendURL)
			}
			if strings.Contains(row.ProviderAPIURL, secret) {
				t.Errorf("ProviderAPIURL leaked %q: %q", secret, row.ProviderAPIURL)
			}
		}
		// 与旧算法对该行逐字段对齐（含脱敏后的 URL）。
		want := legacyOracleRequestsPage(t, store.db, Filter{StatsScope: StatsScopeRaw, Page: 1, PageSize: 50})
		for _, wantRow := range want.Rows {
			if wantRow.ID == "err-dirty" && !reflect.DeepEqual(row, wantRow) {
				t.Fatalf("err-dirty row = %#v\nwant legacy = %#v", row, wantRow)
			}
		}
		return
	}
	t.Fatalf("err-dirty row missing from page (base=%v); page=%#v", base, page)
}

// legacyOracleRequestsPage 在 legacyOracleQueryRows 之上复刻旧 Requests 的内存分页
// 语义（page<=0→1、page_size<=0→50、越界截断），作为差分判定器。
func legacyOracleRequestsPage(t *testing.T, db *sql.DB, filter Filter) RequestPage {
	t.Helper()
	rows := legacyOracleQueryRows(t, db, filter)
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	start := (page - 1) * pageSize
	if start > len(rows) {
		start = len(rows)
	}
	end := start + pageSize
	if end > len(rows) {
		end = len(rows)
	}
	return RequestPage{
		Rows:     rows[start:end],
		Total:    int64(len(rows)),
		Page:     page,
		PageSize: pageSize,
	}
}

func pageSizeOrDefault(pageSize int) int {
	if pageSize <= 0 {
		return 50
	}
	return pageSize
}

// seedRequestsDifferentialFixture 构造一组覆盖去重、口径、错误、脏 URL 与分页体量的
// 差分数据。raw 口径共 13 行（与 TestRequestsPaginationBoundaries 的断言绑定）。
func seedRequestsDifferentialFixture(t *testing.T, store *Store) time.Time {
	t.Helper()
	base := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	providerModels := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	for i, model := range providerModels {
		req := dedupeProviderRequest(
			"prov-"+model,
			base.Add(time.Duration(i)*time.Minute),
			model,
			model,
		)
		req.ProviderName = "Provider " + model
		req.RequestPath = "/v1/messages"
		recordScopedQueryFixture(t, store, req, dedupeToken(
			req.ID,
			UsageSourceProvider,
			ParseStatusOK,
			UsageValues{InputTokens: int64(i + 1)},
		))
	}

	// 与 prov-alpha / prov-gamma 构成重复对（同模型+同 token、±10 分钟内）。
	sessAlpha := dedupeSessionRequest("sess-alpha", base.Add(30*time.Second), "alpha", "alpha")
	recordScopedQueryFixture(t, store, sessAlpha, dedupeToken(
		sessAlpha.ID,
		UsageSourceSessionLog,
		ParseStatusOK,
		UsageValues{InputTokens: 1},
	))
	sessGamma := dedupeSessionRequest("sess-gamma", base.Add(2*time.Minute+30*time.Second), "gamma", "gamma")
	recordScopedQueryFixture(t, store, sessGamma, dedupeToken(
		sessGamma.ID,
		UsageSourceSessionLog,
		ParseStatusOK,
		UsageValues{InputTokens: 3},
	))

	// 无匹配供应商的纯会话行。
	sessSolo := dedupeSessionRequest("sess-solo", base.Add(8*time.Minute), "solo-model", "solo-model")
	recordScopedQueryFixture(t, store, sessSolo, dedupeToken(
		sessSolo.ID,
		UsageSourceSessionLog,
		ParseStatusOK,
		UsageValues{InputTokens: 42},
	))

	// 带 userinfo + 敏感 query 的历史脏 URL 错误行。
	errReq := testUsageRequest("err-dirty", base.Add(9*time.Minute))
	status := 500
	errReq.StatusCode = &status
	errReq.ErrorType = ErrorHTTP
	errReq.ErrorMessage = "searchable boom"
	errReq.MappedModel = "err-model"
	errReq.OriginalModel = "err-model"
	errReq.ProviderName = "Dirty Provider"
	errReq.ProviderAPIURL = "https://user:secret-pass@dirty.example.com/v1?token=abc"
	errReq.BackendURL = "https://user:secret-pass@dirty.example.com/v1/messages?sign=xyz"
	errReq.SourceApp = "claude_code"
	recordScopedQueryFixture(t, store, errReq, TokenRecord{
		RequestID:        errReq.ID,
		UsageSource:      UsageSourceNone,
		UsageParseStatus: ParseStatusSkippedNon2xx,
	})

	// 非 claude_code 来源行（不参与供应商候选匹配）。
	other := testUsageRequest("other-app-1", base.Add(10*time.Minute))
	other.SourceApp = "other-app"
	other.SourceEntrypoint = "other-entry"
	other.ProviderID = "other-provider"
	other.ProviderName = "Other Provider"
	other.MappedModel = "other-model"
	other.OriginalModel = "other-model"
	other.RequestPath = "/other"
	recordScopedQueryFixture(t, store, other, TokenRecord{
		RequestID:        other.ID,
		UsageSource:      UsageSourceNone,
		UsageParseStatus: ParseStatusMissing,
	})

	return base
}
