package usage

import (
	"database/sql"
	"fmt"
	"sort"
	"time"
)

const dedupeCandidatesBackfillMarker = "usage_dedupe_candidates_backfill_v1"

const (
	incrementalDedupeProviderWhere = `
		r.source_app = 'claude_code'
		AND t.usage_source = 'provider'
		AND t.usage_parse_status = 'ok'
		AND r.source_entrypoint <> 'session_log'
		AND r.provider_id <> '_session'`
	incrementalDedupeSessionWhere = `
		t.usage_parse_status = 'ok'
		AND (
			t.usage_source = 'session_log'
			OR r.source_entrypoint = 'session_log'
			OR r.provider_id = '_session'
		)`
)

var dedupeMigrationStatements = []string{
	`CREATE TABLE IF NOT EXISTS usage_dedupe_candidates (
		session_request_id TEXT NOT NULL,
		provider_request_id TEXT NOT NULL,
		model_priority INTEGER NOT NULL,
		PRIMARY KEY (session_request_id, provider_request_id),
		FOREIGN KEY (session_request_id) REFERENCES usage_requests(id) ON DELETE CASCADE,
		FOREIGN KEY (provider_request_id) REFERENCES usage_requests(id) ON DELETE CASCADE
	);`,
	`CREATE INDEX IF NOT EXISTS idx_usage_dedupe_provider
		ON usage_dedupe_candidates(provider_request_id);`,
	`CREATE INDEX IF NOT EXISTS idx_usage_requests_started_id
		ON usage_requests(started_at DESC, id DESC);`,
}

type dedupeBackfillProvider struct {
	id        string
	startedAt time.Time
}

type dedupeBackfillModelKey struct {
	key      duplicateIndexKey
	priority int
}

func (s *Store) migrateDedupeCandidates() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range dedupeMigrationStatements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("create usage dedupe schema: %w", err)
		}
	}

	var completed int
	if err := tx.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM settings WHERE key = ? AND value = '1'
		)`,
		dedupeCandidatesBackfillMarker,
	).Scan(&completed); err != nil {
		return fmt.Errorf("query usage dedupe backfill marker: %w", err)
	}
	if completed == 1 {
		return tx.Commit()
	}

	if err := backfillDedupeCandidatesTx(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES (?, '1')
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		dedupeCandidatesBackfillMarker,
	); err != nil {
		return fmt.Errorf("write usage dedupe backfill marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit usage dedupe migration: %w", err)
	}
	return nil
}

func backfillDedupeCandidatesTx(tx *sql.Tx) error {
	rows, err := tx.Query(
		`SELECT
			r.id, r.started_at, r.source_app, r.source_entrypoint, r.provider_id,
			r.original_model, r.mapped_model,
			t.input_tokens, t.output_tokens, t.cache_creation_input_tokens, t.cache_read_input_tokens,
			t.usage_source, t.usage_parse_status
		 FROM usage_requests r
		 JOIN usage_tokens t ON t.request_id = r.id
		 WHERE t.usage_parse_status = ?
		   AND (
				t.usage_source = ?
				OR r.source_entrypoint = 'session_log'
				OR r.provider_id = '_session'
				OR (r.source_app = 'claude_code' AND t.usage_source = ?)
		   )`,
		ParseStatusOK,
		UsageSourceSessionLog,
		UsageSourceProvider,
	)
	if err != nil {
		return fmt.Errorf("query usage dedupe backfill rows: %w", err)
	}

	var providers []RequestRow
	var sessions []RequestRow
	for rows.Next() {
		var row RequestRow
		var startedAt string
		if err := rows.Scan(
			&row.ID,
			&startedAt,
			&row.SourceApp,
			&row.SourceEntrypoint,
			&row.ProviderID,
			&row.OriginalModel,
			&row.MappedModel,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationInputTokens,
			&row.CacheReadInputTokens,
			&row.UsageSource,
			&row.UsageParseStatus,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan usage dedupe backfill row: %w", err)
		}
		row.StartedAt = parseTime(startedAt)
		switch {
		case isSessionLogRow(row):
			sessions = append(sessions, row)
		case isProviderUsageRow(row):
			providers = append(providers, row)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read usage dedupe backfill rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close usage dedupe backfill rows: %w", err)
	}

	providerIndex := make(map[duplicateIndexKey][]dedupeBackfillProvider)
	for _, provider := range providers {
		for _, key := range duplicateKeys(provider) {
			providerIndex[key] = append(providerIndex[key], dedupeBackfillProvider{
				id:        provider.ID,
				startedAt: provider.StartedAt,
			})
		}
	}
	for key := range providerIndex {
		candidates := providerIndex[key]
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].startedAt.Equal(candidates[j].startedAt) {
				return candidates[i].id < candidates[j].id
			}
			return candidates[i].startedAt.Before(candidates[j].startedAt)
		})
	}

	insert, err := tx.Prepare(
		`INSERT INTO usage_dedupe_candidates(
			session_request_id, provider_request_id, model_priority
		 ) VALUES (?, ?, ?)
		 ON CONFLICT(session_request_id, provider_request_id) DO UPDATE SET
			model_priority = MIN(model_priority, excluded.model_priority)`,
	)
	if err != nil {
		return fmt.Errorf("prepare usage dedupe candidate insert: %w", err)
	}
	defer insert.Close()

	for _, session := range sessions {
		for _, modelKey := range dedupeBackfillModelKeys(session) {
			candidates := providerIndex[modelKey.key]
			start := session.StartedAt.Add(-10 * time.Minute)
			end := session.StartedAt.Add(10 * time.Minute)
			first := sort.Search(len(candidates), func(i int) bool {
				return !candidates[i].startedAt.Before(start)
			})
			for i := first; i < len(candidates) && !candidates[i].startedAt.After(end); i++ {
				if _, err := insert.Exec(session.ID, candidates[i].id, modelKey.priority); err != nil {
					return fmt.Errorf("insert usage dedupe candidate: %w", err)
				}
			}
		}
	}
	return nil
}

func dedupeBackfillModelKeys(row RequestRow) []dedupeBackfillModelKey {
	keys := make([]dedupeBackfillModelKey, 0, 2)
	if row.MappedModel != "" {
		keys = append(keys, dedupeBackfillModelKey{
			key: duplicateIndexKey{
				model:                    row.MappedModel,
				inputTokens:              row.InputTokens,
				outputTokens:             row.OutputTokens,
				cacheCreationInputTokens: row.CacheCreationInputTokens,
				cacheReadInputTokens:     row.CacheReadInputTokens,
			},
			priority: 0,
		})
	}
	if row.OriginalModel != "" && row.OriginalModel != row.MappedModel {
		keys = append(keys, dedupeBackfillModelKey{
			key: duplicateIndexKey{
				model:                    row.OriginalModel,
				inputTokens:              row.InputTokens,
				outputTokens:             row.OutputTokens,
				cacheCreationInputTokens: row.CacheCreationInputTokens,
				cacheReadInputTokens:     row.CacheReadInputTokens,
			},
			priority: 1,
		})
	}
	return keys
}

func maintainDedupeCandidatesTx(tx *sql.Tx, req RequestRecord, tok TokenRecord) error {
	current := RequestRow{RequestRecord: req, TokenRecord: tok}
	current.SourceApp = defaultString(current.SourceApp, "unknown")
	current.UsageSource = defaultString(current.UsageSource, UsageSourceNone)
	current.UsageParseStatus = defaultString(current.UsageParseStatus, ParseStatusMissing)

	var oppositeWhere string
	switch {
	case isSessionLogRow(current) && current.UsageParseStatus == ParseStatusOK:
		oppositeWhere = incrementalDedupeProviderWhere
	case isProviderUsageRow(current):
		oppositeWhere = incrementalDedupeSessionWhere
	default:
		return nil
	}

	opposite, err := queryDedupeOppositeRowsTx(tx, current, oppositeWhere)
	if err != nil {
		return err
	}
	insert, err := tx.Prepare(
		`INSERT INTO usage_dedupe_candidates(
			session_request_id, provider_request_id, model_priority
		 ) VALUES (?, ?, ?)
		 ON CONFLICT(session_request_id, provider_request_id) DO UPDATE SET
			model_priority = MIN(model_priority, excluded.model_priority)`,
	)
	if err != nil {
		return fmt.Errorf("prepare incremental usage dedupe candidate insert: %w", err)
	}
	defer insert.Close()

	for _, candidate := range opposite {
		session, provider := current, candidate
		if isProviderUsageRow(current) {
			session, provider = candidate, current
		}
		if provider.StartedAt.Before(session.StartedAt.Add(-10*time.Minute)) ||
			provider.StartedAt.After(session.StartedAt.Add(10*time.Minute)) {
			continue
		}
		priority, ok := dedupeCandidateModelPriority(session, provider)
		if !ok {
			continue
		}
		if _, err := insert.Exec(session.ID, provider.ID, priority); err != nil {
			return fmt.Errorf("insert incremental usage dedupe candidate: %w", err)
		}
	}
	return nil
}

func queryDedupeOppositeRowsTx(tx *sql.Tx, current RequestRow, oppositeWhere string) ([]RequestRow, error) {
	query, args := incrementalDedupeCandidateQuery(current, oppositeWhere)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query incremental usage dedupe candidates: %w", err)
	}

	var candidates []RequestRow
	for rows.Next() {
		var candidate RequestRow
		var startedAt string
		if err := rows.Scan(
			&candidate.ID,
			&startedAt,
			&candidate.OriginalModel,
			&candidate.MappedModel,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan incremental usage dedupe candidate: %w", err)
		}
		candidate.StartedAt = parseTime(startedAt)
		candidate.InputTokens = current.InputTokens
		candidate.OutputTokens = current.OutputTokens
		candidate.CacheCreationInputTokens = current.CacheCreationInputTokens
		candidate.CacheReadInputTokens = current.CacheReadInputTokens
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read incremental usage dedupe candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close incremental usage dedupe candidates: %w", err)
	}
	return candidates, nil
}

func incrementalDedupeCandidateQuery(current RequestRow, oppositeWhere string) (string, []any) {
	query := `SELECT
			r.id, r.started_at, r.original_model, r.mapped_model
		 FROM usage_requests r INDEXED BY idx_usage_requests_started_id
		 JOIN usage_tokens t ON t.request_id = r.id
		 WHERE ` + oppositeWhere + `
		   AND r.started_at >= ?
		   AND r.started_at < ?
		   AND t.input_tokens = ?
		   AND t.output_tokens = ?
		   AND t.cache_creation_input_tokens = ?
		   AND t.cache_read_input_tokens = ?
		   AND (
				(r.mapped_model <> '' AND (r.mapped_model = ? OR r.mapped_model = ?))
				OR
				(r.original_model <> '' AND (r.original_model = ? OR r.original_model = ?))
		   )`
	lowerBound, upperBound := incrementalDedupeSQLBounds(current.StartedAt)
	args := []any{
		lowerBound,
		upperBound,
		current.InputTokens,
		current.OutputTokens,
		current.CacheCreationInputTokens,
		current.CacheReadInputTokens,
		current.MappedModel,
		current.OriginalModel,
		current.MappedModel,
		current.OriginalModel,
	}
	return query, args
}

func incrementalDedupeSQLBounds(startedAt time.Time) (string, string) {
	lower := startedAt.Add(-10 * time.Minute).Truncate(time.Second).Add(-time.Second)
	upper := startedAt.Add(10 * time.Minute).Truncate(time.Second).Add(time.Second)
	return formatTime(lower), formatTime(upper)
}

func dedupeCandidateModelPriority(session, provider RequestRow) (int, bool) {
	providerKeys := make(map[duplicateIndexKey]struct{})
	for _, key := range duplicateKeys(provider) {
		providerKeys[key] = struct{}{}
	}
	for _, modelKey := range dedupeBackfillModelKeys(session) {
		if _, ok := providerKeys[modelKey.key]; ok {
			return modelKey.priority, true
		}
	}
	return 0, false
}
