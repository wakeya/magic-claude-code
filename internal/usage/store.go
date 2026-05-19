package usage

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Migrate() error {
	stmts := []string{
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS usage_requests (
			id TEXT PRIMARY KEY,
			started_at TEXT NOT NULL,
			ended_at TEXT,
			duration_ms INTEGER,
			upstream_response_header_ms INTEGER,
			time_to_first_byte_ms INTEGER,
			status_code INTEGER,
			error_type TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT '',
			request_path TEXT NOT NULL DEFAULT '',
			backend_url TEXT NOT NULL DEFAULT '',
			provider_id TEXT NOT NULL DEFAULT '',
			provider_name TEXT NOT NULL DEFAULT '',
			provider_api_url TEXT NOT NULL DEFAULT '',
			source_app TEXT NOT NULL DEFAULT 'unknown',
			source_entrypoint TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			original_model TEXT NOT NULL DEFAULT '',
			mapped_model TEXT NOT NULL DEFAULT '',
			stream INTEGER NOT NULL DEFAULT 0,
			request_bytes INTEGER NOT NULL DEFAULT 0,
			response_bytes INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS usage_tokens (
			request_id TEXT PRIMARY KEY,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
			usage_source TEXT NOT NULL DEFAULT 'none',
			usage_parse_status TEXT NOT NULL DEFAULT 'missing',
			usage_parse_error TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (request_id) REFERENCES usage_requests(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS session_log_sync (
			file_path TEXT PRIMARY KEY,
			last_modified INTEGER NOT NULL DEFAULT 0,
			last_line_offset INTEGER NOT NULL DEFAULT 0,
			last_synced_at TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_started_at ON usage_requests(started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_provider ON usage_requests(provider_id, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_provider_url ON usage_requests(provider_api_url, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_entrypoint ON usage_requests(source_entrypoint, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_path ON usage_requests(request_path, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_model ON usage_requests(mapped_model, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_source ON usage_requests(source_app, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_status ON usage_requests(status_code, error_type, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_tokens_source ON usage_tokens(usage_source);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_tokens_parse_status ON usage_tokens(usage_parse_status);`,
		`CREATE INDEX IF NOT EXISTS idx_session_log_sync_synced_at ON session_log_sync(last_synced_at);`,
		`INSERT OR IGNORE INTO settings(key, value) VALUES ('usage_retention_days', '90');`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Record(req RequestRecord, tok TokenRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if tok.RequestID == "" {
		tok.RequestID = req.ID
	}
	_, err = tx.Exec(
		`INSERT INTO usage_requests(
			id, started_at, ended_at, duration_ms, upstream_response_header_ms, time_to_first_byte_ms,
			status_code, error_type, error_message, method, request_path, backend_url,
			provider_id, provider_name, provider_api_url, source_app, source_entrypoint, user_agent,
			original_model, mapped_model, stream, request_bytes, response_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID,
		formatTime(req.StartedAt),
		formatOptionalTime(req.EndedAt),
		req.DurationMS,
		req.UpstreamResponseHeaderMS,
		req.TimeToFirstByteMS,
		req.StatusCode,
		req.ErrorType,
		req.ErrorMessage,
		req.Method,
		req.RequestPath,
		req.BackendURL,
		req.ProviderID,
		req.ProviderName,
		req.ProviderAPIURL,
		defaultString(req.SourceApp, "unknown"),
		req.SourceEntrypoint,
		req.UserAgent,
		req.OriginalModel,
		req.MappedModel,
		boolToInt(req.Stream),
		req.RequestBytes,
		req.ResponseBytes,
	)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO usage_tokens(
			request_id, input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens,
			usage_source, usage_parse_status, usage_parse_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tok.RequestID,
		tok.InputTokens,
		tok.OutputTokens,
		tok.CacheCreationInputTokens,
		tok.CacheReadInputTokens,
		defaultString(tok.UsageSource, UsageSourceNone),
		defaultString(tok.UsageParseStatus, ParseStatusMissing),
		tok.UsageParseError,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) recordIfAbsent(req RequestRecord, tok TokenRecord) (bool, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM usage_requests WHERE id = ?`, req.ID).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 {
		return false, nil
	}
	return true, s.Record(req, tok)
}

func (s *Store) Summary(filter Filter) (Summary, error) {
	rows, err := s.queryRows(filter, false)
	if err != nil {
		return Summary{}, err
	}
	startOfToday, endOfToday, err := todayRange(filter)
	if err != nil {
		return Summary{}, err
	}

	var summary Summary
	var withUsage int64
	for _, row := range rows {
		summary.ProviderRequestsTotal++
		if hasUsage(row.TokenRecord) {
			withUsage++
			summary.TokenConsumptionTotal += tokenTotal(row.TokenRecord)
		}
		if isFailed(row.RequestRecord) {
			summary.FailedRequests++
		}
		if summary.LastProviderRequest == nil || row.StartedAt.After(*summary.LastProviderRequest) {
			started := row.StartedAt
			summary.LastProviderRequest = &started
		}
		if !row.StartedAt.Before(startOfToday) && row.StartedAt.Before(endOfToday) {
			summary.TodayProviderRequests++
			if hasUsage(row.TokenRecord) {
				summary.TodayTokenConsumption += tokenTotal(row.TokenRecord)
			}
		}
	}
	if summary.ProviderRequestsTotal > 0 {
		summary.UsageCoverage = float64(withUsage) / float64(summary.ProviderRequestsTotal)
	}
	return summary, nil
}

func (s *Store) Trends(filter Filter) ([]TrendPoint, error) {
	rows, err := s.queryRows(filter, false)
	if err != nil {
		return nil, err
	}
	loc, err := filterLocation(filter)
	if err != nil {
		return nil, err
	}
	groups := make(map[string]*trendAccumulator)
	for _, row := range rows {
		bucket := row.StartedAt.In(loc).Format("2006-01-02")
		group := groups[bucket]
		if group == nil {
			group = &trendAccumulator{}
			groups[bucket] = group
		}
		group.add(row)
	}
	out := make([]TrendPoint, 0, len(groups))
	for bucket, group := range groups {
		point := group.point
		point.Bucket = bucket
		if point.ProviderRequestsTotal > 0 {
			point.UsageCoverage = float64(group.withUsage) / float64(point.ProviderRequestsTotal)
		}
		out = append(out, point)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket < out[j].Bucket })
	return out, nil
}

func (s *Store) Requests(filter Filter) (RequestPage, error) {
	all, err := s.queryRows(filter, false)
	if err != nil {
		return RequestPage{}, err
	}
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	start := (page - 1) * pageSize
	if start > len(all) {
		start = len(all)
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return RequestPage{Rows: all[start:end], Total: int64(len(all)), Page: page, PageSize: pageSize}, nil
}

func (s *Store) Providers(filter Filter) ([]AggregateRow, error) {
	return s.aggregate(filter, func(row RequestRow) (key, name string) {
		return row.ProviderID, row.ProviderName
	})
}

func (s *Store) Models(filter Filter) ([]AggregateRow, error) {
	return s.aggregate(filter, func(row RequestRow) (key, name string) {
		return row.MappedModel, row.MappedModel
	})
}

func (s *Store) aggregate(filter Filter, keyFn func(RequestRow) (string, string)) ([]AggregateRow, error) {
	rows, err := s.queryRows(filter, false)
	if err != nil {
		return nil, err
	}
	groups := make(map[string]*aggregateAccumulator)
	for _, row := range rows {
		key, name := keyFn(row)
		group := groups[key]
		if group == nil {
			group = &aggregateAccumulator{row: AggregateRow{Name: name, ProviderID: row.ProviderID, ProviderName: row.ProviderName, MappedModel: row.MappedModel}}
			groups[key] = group
		}
		group.add(row)
	}
	out := make([]AggregateRow, 0, len(groups))
	for _, group := range groups {
		row := group.row
		if row.TotalRequests > 0 {
			row.UsageCoverage = float64(group.withUsage) / float64(row.TotalRequests)
			row.AverageDurationMS = float64(group.durationTotal) / float64(row.TotalRequests)
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TotalRequests > out[j].TotalRequests })
	return out, nil
}

func (s *Store) Coverage(filter Filter) ([]CoverageRow, error) {
	rows, err := s.queryRows(filter, false)
	if err != nil {
		return nil, err
	}
	groups := make(map[string]*coverageAccumulator)
	for _, row := range rows {
		key := strings.Join([]string{row.ProviderName, row.ProviderAPIURL, row.MappedModel, row.SourceEntrypoint}, "\x00")
		group := groups[key]
		if group == nil {
			group = &coverageAccumulator{
				row: CoverageRow{
					ProviderName:     row.ProviderName,
					ProviderAPIURL:   row.ProviderAPIURL,
					MappedModel:      row.MappedModel,
					SourceEntrypoint: row.SourceEntrypoint,
				},
				parseStatuses: make(map[string]int64),
			}
			groups[key] = group
		}
		group.add(row)
	}

	out := make([]CoverageRow, 0, len(groups))
	for _, group := range groups {
		row := group.row
		if row.TotalRequests > 0 {
			row.UsageCoverage = float64(row.WithUsageRequests) / float64(row.TotalRequests)
		}
		row.TopUsageParseStatus = topStatus(group.parseStatuses)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})
	return out, nil
}

func (s *Store) queryRows(filter Filter, includePagination bool) ([]RequestRow, error) {
	query := `SELECT
		r.id, r.started_at, r.ended_at, r.duration_ms, r.upstream_response_header_ms, r.time_to_first_byte_ms,
		r.status_code, r.error_type, r.error_message, r.method, r.request_path, r.backend_url,
		r.provider_id, r.provider_name, r.provider_api_url, r.source_app, r.source_entrypoint, r.user_agent,
		r.original_model, r.mapped_model, r.stream, r.request_bytes, r.response_bytes,
		t.input_tokens, t.output_tokens, t.cache_creation_input_tokens, t.cache_read_input_tokens,
		t.usage_source, t.usage_parse_status, t.usage_parse_error
		FROM usage_requests r JOIN usage_tokens t ON t.request_id = r.id`
	where, args := filterWhere(filter)
	if where != "" {
		query += " WHERE " + where
	}
	query += " ORDER BY r.started_at DESC, r.id DESC"
	if includePagination && filter.PageSize > 0 {
		page := filter.Page
		if page <= 0 {
			page = 1
		}
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", filter.PageSize, (page-1)*filter.PageSize)
	}

	sqlRows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer sqlRows.Close()

	var rows []RequestRow
	for sqlRows.Next() {
		row, err := scanRequestRow(sqlRows)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := sqlRows.Err(); err != nil {
		return nil, err
	}
	return applyStatsScope(rows, filter.StatsScope), nil
}

func filterWhere(filter Filter) (string, []any) {
	var parts []string
	var args []any
	add := func(sql string, arg any) {
		parts = append(parts, sql)
		args = append(args, arg)
	}
	if !filter.From.IsZero() {
		add("r.started_at >= ?", formatTime(filter.From))
	}
	if !filter.To.IsZero() {
		add("r.started_at < ?", formatTime(filter.To))
	}
	if filter.SourceApp != "" && filter.SourceApp != "all" {
		add("r.source_app = ?", filter.SourceApp)
	}
	if filter.SourceEntrypoint != "" && filter.SourceEntrypoint != "all" {
		add("r.source_entrypoint = ?", filter.SourceEntrypoint)
	}
	if filter.ProviderID != "" && filter.ProviderID != "all" {
		add("r.provider_id = ?", filter.ProviderID)
	}
	if filter.Model != "" && filter.Model != "all" {
		add("r.mapped_model = ?", filter.Model)
	}
	if filter.RequestPath != "" && filter.RequestPath != "all" {
		add("r.request_path = ?", filter.RequestPath)
	}
	if filter.UsageSource != "" && filter.UsageSource != "all" {
		add("t.usage_source = ?", filter.UsageSource)
	}
	if filter.UsageParseStatus != "" && filter.UsageParseStatus != "all" {
		add("t.usage_parse_status = ?", filter.UsageParseStatus)
	}
	switch filter.Status {
	case "success":
		parts = append(parts, "(r.error_type = '' AND r.status_code >= 200 AND r.status_code < 300)")
	case "error":
		parts = append(parts, "(r.error_type != '' OR r.status_code IS NULL OR r.status_code < 200 OR r.status_code >= 300)")
	}
	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		parts = append(parts, "(r.provider_name LIKE ? OR r.provider_api_url LIKE ? OR r.mapped_model LIKE ? OR r.id LIKE ? OR r.error_message LIKE ?)")
		args = append(args, like, like, like, like, like)
	}
	return strings.Join(parts, " AND "), args
}

func scanRequestRow(rows *sql.Rows) (RequestRow, error) {
	var row RequestRow
	var startedAt string
	var endedAt sql.NullString
	var duration, header, firstByte, status sql.NullInt64
	var stream int
	err := rows.Scan(
		&row.ID, &startedAt, &endedAt, &duration, &header, &firstByte,
		&status, &row.ErrorType, &row.ErrorMessage, &row.Method, &row.RequestPath, &row.BackendURL,
		&row.ProviderID, &row.ProviderName, &row.ProviderAPIURL, &row.SourceApp, &row.SourceEntrypoint, &row.UserAgent,
		&row.OriginalModel, &row.MappedModel, &stream, &row.RequestBytes, &row.ResponseBytes,
		&row.InputTokens, &row.OutputTokens, &row.CacheCreationInputTokens, &row.CacheReadInputTokens,
		&row.UsageSource, &row.UsageParseStatus, &row.UsageParseError,
	)
	if err != nil {
		return RequestRow{}, err
	}
	row.StartedAt = parseTime(startedAt)
	row.EndedAt = parseOptionalTime(endedAt)
	row.DurationMS = optionalInt64(duration)
	row.UpstreamResponseHeaderMS = optionalInt64(header)
	row.TimeToFirstByteMS = optionalInt64(firstByte)
	if status.Valid {
		v := int(status.Int64)
		row.StatusCode = &v
	}
	row.Stream = stream == 1
	row.RequestID = row.ID
	return row, nil
}

func applyStatsScope(rows []RequestRow, scope string) []RequestRow {
	if scope == "" {
		scope = StatsScopeEffective
	}
	markDuplicateSessionRows(rows)
	out := rows[:0]
	for _, row := range rows {
		switch scope {
		case StatsScopeRaw:
			out = append(out, row)
		case StatsScopeProvider:
			if !isSessionLogRow(row) {
				out = append(out, row)
			}
		case StatsScopeSessionLog:
			if isSessionLogRow(row) {
				out = append(out, row)
			}
		default:
			if !isSessionLogRow(row) || row.DedupeStatus != DedupeStatusDuplicate {
				out = append(out, row)
			}
		}
	}
	return out
}

func markDuplicateSessionRows(rows []RequestRow) {
	providerIndex := buildProviderDuplicateIndex(rows)
	for i := range rows {
		if !isSessionLogRow(rows[i]) || rows[i].UsageParseStatus != ParseStatusOK {
			continue
		}
		if candidateIndex, ok := duplicateProviderCandidate(rows[i], providerIndex); ok {
			candidate := rows[candidateIndex]
			rows[i].DedupeStatus = DedupeStatusDuplicate
			rows[i].DedupeRequestID = candidate.ID
		}
	}
}

type duplicateCandidate struct {
	rowIndex  int
	startedAt time.Time
}

type duplicateIndexKey struct {
	model                    string
	inputTokens              int64
	outputTokens             int64
	cacheCreationInputTokens int64
	cacheReadInputTokens     int64
}

func buildProviderDuplicateIndex(rows []RequestRow) map[duplicateIndexKey][]duplicateCandidate {
	index := make(map[duplicateIndexKey][]duplicateCandidate)
	for i, row := range rows {
		if !isProviderUsageRow(row) {
			continue
		}
		for _, key := range duplicateKeys(row) {
			index[key] = append(index[key], duplicateCandidate{rowIndex: i, startedAt: row.StartedAt})
		}
	}
	for key := range index {
		sort.Slice(index[key], func(i, j int) bool {
			return index[key][i].startedAt.Before(index[key][j].startedAt)
		})
	}
	return index
}

func duplicateProviderCandidate(row RequestRow, index map[duplicateIndexKey][]duplicateCandidate) (int, bool) {
	for _, key := range duplicateKeys(row) {
		candidates := index[key]
		if len(candidates) == 0 {
			continue
		}
		start := row.StartedAt.Add(-10 * time.Minute)
		end := row.StartedAt.Add(10 * time.Minute)
		first := sort.Search(len(candidates), func(i int) bool {
			return !candidates[i].startedAt.Before(start)
		})
		if first == len(candidates) || candidates[first].startedAt.After(end) {
			continue
		}
		return candidates[first].rowIndex, true
	}
	return 0, false
}

func duplicateKeys(row RequestRow) []duplicateIndexKey {
	models := dedupeModels(row.MappedModel, row.OriginalModel)
	keys := make([]duplicateIndexKey, 0, len(models))
	for _, model := range models {
		keys = append(keys, duplicateIndexKey{
			model:                    model,
			inputTokens:              row.InputTokens,
			outputTokens:             row.OutputTokens,
			cacheCreationInputTokens: row.CacheCreationInputTokens,
			cacheReadInputTokens:     row.CacheReadInputTokens,
		})
	}
	return keys
}

func dedupeModels(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isProviderUsageRow(row RequestRow) bool {
	return !isSessionLogRow(row) &&
		row.SourceApp == "claude_code" &&
		row.UsageSource == UsageSourceProvider &&
		row.UsageParseStatus == ParseStatusOK
}

func isSessionLogRow(row RequestRow) bool {
	return row.UsageSource == UsageSourceSessionLog || row.SourceEntrypoint == "session_log" || row.ProviderID == "_session"
}

type coverageAccumulator struct {
	row           CoverageRow
	parseStatuses map[string]int64
}

type trendAccumulator struct {
	point     TrendPoint
	withUsage int64
}

func (a *trendAccumulator) add(row RequestRow) {
	a.point.ProviderRequestsTotal++
	if isFailed(row.RequestRecord) {
		a.point.FailedRequests++
	}
	if hasUsage(row.TokenRecord) {
		a.withUsage++
		a.point.InputTokens += row.InputTokens
		a.point.OutputTokens += row.OutputTokens
		a.point.CacheCreationInputTokens += row.CacheCreationInputTokens
		a.point.CacheReadInputTokens += row.CacheReadInputTokens
		a.point.TokenConsumptionTotal += tokenTotal(row.TokenRecord)
	}
}

type aggregateAccumulator struct {
	row           AggregateRow
	withUsage     int64
	durationTotal int64
}

func (a *aggregateAccumulator) add(row RequestRow) {
	a.row.TotalRequests++
	if isFailed(row.RequestRecord) {
		a.row.FailedRequests++
	}
	if hasUsage(row.TokenRecord) {
		a.withUsage++
		a.row.TokenConsumptionTotal += tokenTotal(row.TokenRecord)
	}
	if row.DurationMS != nil {
		a.durationTotal += *row.DurationMS
	}
}

func (a *coverageAccumulator) add(row RequestRow) {
	a.row.TotalRequests++
	if isFailed(row.RequestRecord) {
		a.row.ErrorRequests++
	} else {
		a.row.SuccessRequests++
	}
	if hasUsage(row.TokenRecord) {
		a.row.WithUsageRequests++
	} else {
		a.row.WithoutUsageRequests++
		a.parseStatuses[row.UsageParseStatus]++
	}
	if row.StartedAt.After(a.row.LastSeenAt) {
		a.row.LastSeenAt = row.StartedAt
	}
}

func todayRange(filter Filter) (time.Time, time.Time, error) {
	loc, err := filterLocation(filter)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	now := filter.Now
	if now.IsZero() {
		now = time.Now()
	}
	localNow := now.In(loc)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	return start.UTC(), start.AddDate(0, 0, 1).UTC(), nil
}

func filterLocation(filter Filter) (*time.Location, error) {
	if filter.TZ == "" {
		return time.Local, nil
	}
	return time.LoadLocation(filter.TZ)
}

func isFailed(req RequestRecord) bool {
	if req.ErrorType != "" {
		return true
	}
	if req.StatusCode == nil {
		return true
	}
	return *req.StatusCode < 200 || *req.StatusCode >= 300
}

func tokenTotal(tok TokenRecord) int64 {
	return tok.InputTokens + tok.OutputTokens + tok.CacheCreationInputTokens + tok.CacheReadInputTokens
}

func hasUsage(tok TokenRecord) bool {
	return tok.UsageSource != "" && tok.UsageSource != UsageSourceNone && tok.UsageParseStatus == ParseStatusOK
}

func topStatus(counts map[string]int64) string {
	var top string
	var topCount int64
	for status, count := range counts {
		if count > topCount || count == topCount && status < top {
			top = status
			topCount = count
		}
	}
	return top
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func parseTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func parseOptionalTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	t := parseTime(value.String)
	return &t
}

func optionalInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
