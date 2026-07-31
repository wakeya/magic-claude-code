package usage

import (
	"database/sql"
	"fmt"
	"sort"
	"time"
)

const dedupeCandidatesBackfillMarker = "usage_dedupe_candidates_backfill_v1"

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
