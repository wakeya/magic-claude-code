package usage

import (
	"database/sql"
	"sort"
	"strings"
	"testing"
	"time"
)

// legacyOracleQueryRows is intentionally test-only and independent from the
// candidate table. It preserves the former "filter first, dedupe second,
// scope last" algorithm as a differential oracle for SQL-backed reads.
func legacyOracleQueryRows(t *testing.T, db *sql.DB, filter Filter) []RequestRow {
	t.Helper()
	query := `SELECT
		r.id, r.started_at, r.ended_at, r.duration_ms, r.upstream_response_header_ms, r.time_to_first_byte_ms,
		r.status_code, r.error_type, r.error_message, r.method, r.request_path, r.backend_url,
		r.provider_id, r.provider_name, r.provider_api_url, r.source_app, r.source_entrypoint, r.user_agent,
		r.original_model, r.mapped_model, r.stream, r.request_bytes, r.response_bytes,
		t.input_tokens, t.output_tokens, t.cache_creation_input_tokens, t.cache_read_input_tokens,
		t.usage_source, t.usage_parse_status, t.usage_parse_error
		FROM usage_requests r
		JOIN usage_tokens t ON t.request_id = r.id`
	where, args := legacyOracleFilterWhere(filter)
	if where != "" {
		query += " WHERE " + where
	}
	query += " ORDER BY r.started_at DESC, r.id DESC"

	sqlRows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("query legacy oracle rows: %v", err)
	}
	defer sqlRows.Close()

	var rows []RequestRow
	for sqlRows.Next() {
		row, err := scanRequestRow(sqlRows)
		if err != nil {
			t.Fatalf("scan legacy oracle row: %v", err)
		}
		rows = append(rows, row)
	}
	if err := sqlRows.Err(); err != nil {
		t.Fatalf("read legacy oracle rows: %v", err)
	}
	legacyOracleMarkDuplicates(rows)
	return legacyOracleApplyScope(rows, filter.StatsScope)
}

// legacyOracleSummary is intentionally test-only and independent from the SQL
// aggregate path. It reproduces the former "scoped full-row scan, then Go
// aggregation" Summary algorithm field-for-field (hasUsage/isFailed/today
// range/usage coverage/last provider request) on top of legacyOracleQueryRows,
// serving as the differential oracle for the SQL COUNT/SUM rewrite.
func legacyOracleSummary(t *testing.T, db *sql.DB, filter Filter) Summary {
	t.Helper()
	rows := legacyOracleQueryRows(t, db, filter)
	startOfToday, endOfToday, err := todayRange(filter)
	if err != nil {
		t.Fatalf("legacy oracle today range: %v", err)
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
	return summary
}

// legacyOracleTrends is intentionally test-only and independent from the SQL
// bucket path. It reproduces the former "scoped full-row scan, then Go local
// date bucket aggregation" Trends algorithm field-for-field (timezone bucket
// label, hasUsage/isFailed gating, token sums, usage coverage, missing buckets,
// bucket string ascending order) on top of legacyOracleQueryRows, serving as
// the differential oracle for the SQL GROUP BY time-bucket rewrite.
func legacyOracleTrends(t *testing.T, db *sql.DB, filter Filter) []TrendPoint {
	t.Helper()
	rows := legacyOracleQueryRows(t, db, filter)
	loc, err := filterLocation(filter)
	if err != nil {
		t.Fatalf("legacy oracle location: %v", err)
	}

	type trendAccum struct {
		point     TrendPoint
		withUsage int64
	}
	groups := make(map[string]*trendAccum)
	for _, row := range rows {
		bucket := row.StartedAt.In(loc).Format("2006-01-02")
		group := groups[bucket]
		if group == nil {
			group = &trendAccum{}
			groups[bucket] = group
		}
		group.point.ProviderRequestsTotal++
		if isFailed(row.RequestRecord) {
			group.point.FailedRequests++
		}
		if hasUsage(row.TokenRecord) {
			group.withUsage++
			group.point.InputTokens += row.InputTokens
			group.point.OutputTokens += row.OutputTokens
			group.point.CacheCreationInputTokens += row.CacheCreationInputTokens
			group.point.CacheReadInputTokens += row.CacheReadInputTokens
			group.point.TokenConsumptionTotal += tokenTotal(row.TokenRecord)
		}
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
	return out
}

// legacyOracleAggregate is intentionally test-only and independent from the SQL
// GROUP BY path. It reproduces the former "scoped full-row scan, then Go
// per-row map aggregation" Providers/Models algorithm field-for-field on top of
// legacyOracleQueryRows: grouping key, first-encountered-row dimension fields
// (legacyOracleQueryRows preserves the old started_at DESC, id DESC string
// ordering, so the first row of each group is the same row the old aggregate
// saw first), hasUsage/isFailed gating, token sums, NULL-duration skipping,
// usage coverage and average duration. The only deliberate refinement is a
// deterministic group_key ascending tie-breaker behind the documented primary
// sort key (TotalRequests descending); the old unstable sort left equal-count
// groups in undefined map order, which R1 explicitly permits making
// deterministic. It serves as the differential oracle for the SQL GROUP BY
// rewrite of the Providers and Models dimensions.
func legacyOracleAggregate(t *testing.T, db *sql.DB, filter Filter, keyFn func(RequestRow) (string, string)) []AggregateRow {
	t.Helper()
	rows := legacyOracleQueryRows(t, db, filter)

	type aggregateGroup struct {
		row           AggregateRow
		withUsage     int64
		durationTotal int64
	}
	groups := make(map[string]*aggregateGroup)
	for _, row := range rows {
		key, name := keyFn(row)
		group := groups[key]
		if group == nil {
			group = &aggregateGroup{row: AggregateRow{Name: name, ProviderID: row.ProviderID, ProviderName: row.ProviderName, MappedModel: row.MappedModel}}
			groups[key] = group
		}
		group.row.TotalRequests++
		if isFailed(row.RequestRecord) {
			group.row.FailedRequests++
		}
		if hasUsage(row.TokenRecord) {
			group.withUsage++
			group.row.TokenConsumptionTotal += tokenTotal(row.TokenRecord)
		}
		if row.DurationMS != nil {
			group.durationTotal += *row.DurationMS
		}
	}

	type keyedRow struct {
		row AggregateRow
		key string
	}
	keyed := make([]keyedRow, 0, len(groups))
	for key, group := range groups {
		row := group.row
		if row.TotalRequests > 0 {
			row.UsageCoverage = float64(group.withUsage) / float64(row.TotalRequests)
			row.AverageDurationMS = float64(group.durationTotal) / float64(row.TotalRequests)
		}
		keyed = append(keyed, keyedRow{row: row, key: key})
	}
	sort.Slice(keyed, func(i, j int) bool {
		if keyed[i].row.TotalRequests != keyed[j].row.TotalRequests {
			return keyed[i].row.TotalRequests > keyed[j].row.TotalRequests
		}
		return keyed[i].key < keyed[j].key
	})
	out := make([]AggregateRow, 0, len(keyed))
	for _, kr := range keyed {
		out = append(out, kr.row)
	}
	return out
}

// legacyOracleCoverage is intentionally test-only and independent from the SQL
// GROUP BY path. It reproduces the former "scoped full-row scan, then Go
// per-row map aggregation" Coverage algorithm field-for-field on top of
// legacyOracleQueryRows: grouping by the redacted (provider_name,
// provider_api_url, mapped_model, source_entrypoint) key (legacyOracleQueryRows
// redacts URLs exactly like the old scanRequestRow did before the old Coverage
// grouped, so rows whose dirty URLs redact to the same string merge into one
// group), isFailed/hasUsage gating, success/error split, parse-status tallies
// for rows without usage, top-status tie-breaking, usage coverage and last seen
// time. The only deliberate refinement is a deterministic group key ascending
// tie-breaker behind the documented primary sort key (LastSeenAt descending);
// the old unstable sort left equal-time groups in undefined map order, which R1
// explicitly permits making deterministic. It serves as the differential oracle
// for the SQL GROUP BY rewrite of the Coverage endpoint.
func legacyOracleCoverage(t *testing.T, db *sql.DB, filter Filter) []CoverageRow {
	t.Helper()
	rows := legacyOracleQueryRows(t, db, filter)

	type coverageGroup struct {
		row           CoverageRow
		parseStatuses map[string]int64
	}
	groups := make(map[string]*coverageGroup)
	for _, row := range rows {
		key := strings.Join([]string{row.ProviderName, row.ProviderAPIURL, row.MappedModel, row.SourceEntrypoint}, "\x00")
		group := groups[key]
		if group == nil {
			group = &coverageGroup{
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
		group.row.TotalRequests++
		if isFailed(row.RequestRecord) {
			group.row.ErrorRequests++
		} else {
			group.row.SuccessRequests++
		}
		if hasUsage(row.TokenRecord) {
			group.row.WithUsageRequests++
		} else {
			group.row.WithoutUsageRequests++
			group.parseStatuses[row.UsageParseStatus]++
		}
		if row.StartedAt.After(group.row.LastSeenAt) {
			group.row.LastSeenAt = row.StartedAt
		}
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
		if !out[i].LastSeenAt.Equal(out[j].LastSeenAt) {
			return out[i].LastSeenAt.After(out[j].LastSeenAt)
		}
		return coverageSortKey(out[i]) < coverageSortKey(out[j])
	})
	return out
}

func legacyOracleFilterWhere(filter Filter) (string, []any) {
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

func legacyOracleMarkDuplicates(rows []RequestRow) {
	for sessionIndex := range rows {
		session := rows[sessionIndex]
		if !legacyOracleIsSessionRow(session) || session.UsageParseStatus != ParseStatusOK {
			continue
		}

		bestIndex := -1
		bestPriority := 2
		for providerIndex, provider := range rows {
			if !legacyOracleIsProviderRow(provider) ||
				!legacyOracleTokensEqual(session, provider) {
				continue
			}
			if provider.StartedAt.Before(session.StartedAt.Add(-10*time.Minute)) ||
				provider.StartedAt.After(session.StartedAt.Add(10*time.Minute)) {
				continue
			}
			priority, ok := legacyOracleModelPriority(session, provider)
			if !ok ||
				bestIndex >= 0 && !legacyOracleCandidateLess(provider, priority, rows[bestIndex], bestPriority) {
				continue
			}
			bestIndex = providerIndex
			bestPriority = priority
		}
		if bestIndex >= 0 {
			rows[sessionIndex].DedupeStatus = DedupeStatusDuplicate
			rows[sessionIndex].DedupeRequestID = rows[bestIndex].ID
		}
	}
}

func legacyOracleApplyScope(rows []RequestRow, scope string) []RequestRow {
	var out []RequestRow
	for _, row := range rows {
		switch scope {
		case StatsScopeRaw:
			out = append(out, row)
		case StatsScopeProvider:
			if !legacyOracleIsSessionRow(row) {
				out = append(out, row)
			}
		case StatsScopeSessionLog:
			if legacyOracleIsSessionRow(row) {
				out = append(out, row)
			}
		default:
			if !legacyOracleIsSessionRow(row) || row.DedupeStatus != DedupeStatusDuplicate {
				out = append(out, row)
			}
		}
	}
	return out
}

func legacyOracleIsSessionRow(row RequestRow) bool {
	return row.UsageSource == UsageSourceSessionLog ||
		row.SourceEntrypoint == "session_log" ||
		row.ProviderID == "_session"
}

func legacyOracleIsProviderRow(row RequestRow) bool {
	return !legacyOracleIsSessionRow(row) &&
		row.SourceApp == "claude_code" &&
		row.UsageSource == UsageSourceProvider &&
		row.UsageParseStatus == ParseStatusOK
}

func legacyOracleTokensEqual(left, right RequestRow) bool {
	return left.InputTokens == right.InputTokens &&
		left.OutputTokens == right.OutputTokens &&
		left.CacheCreationInputTokens == right.CacheCreationInputTokens &&
		left.CacheReadInputTokens == right.CacheReadInputTokens
}

func legacyOracleModelPriority(session, provider RequestRow) (int, bool) {
	providerModels := legacyOracleModels(provider.MappedModel, provider.OriginalModel)
	if session.MappedModel != "" && legacyOracleContains(providerModels, session.MappedModel) {
		return 0, true
	}
	if session.OriginalModel != "" &&
		session.OriginalModel != session.MappedModel &&
		legacyOracleContains(providerModels, session.OriginalModel) {
		return 1, true
	}
	return 0, false
}

func legacyOracleModels(values ...string) []string {
	var out []string
	for _, value := range values {
		if value != "" && !legacyOracleContains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func legacyOracleContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func legacyOracleCandidateLess(left RequestRow, leftPriority int, right RequestRow, rightPriority int) bool {
	if leftPriority != rightPriority {
		return leftPriority < rightPriority
	}
	if !left.StartedAt.Equal(right.StartedAt) {
		return left.StartedAt.Before(right.StartedAt)
	}
	return left.ID < right.ID
}
