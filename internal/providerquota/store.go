package providerquota

import (
	"database/sql"
	"time"
)

// SnapshotStore manages quota snapshots in SQLite.
// It stores only the latest attempt and latest success per provider.
type SnapshotStore struct {
	db *sql.DB
}

// NewSnapshotStore creates a SnapshotStore that writes to the given database.
func NewSnapshotStore(db *sql.DB) *SnapshotStore {
	return &SnapshotStore{db: db}
}

// SaveUpsert inserts or updates the snapshot for a provider.
// On failure, result contains the failed result but lastSuccess is preserved.
func (s *SnapshotStore) SaveUpsert(providerID string, result *ProviderQuotaResult) error {
	resultJSON, err := EncodeResult(result)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// If this result is a success, update last_success_json too.
	if result != nil && result.Success {
		_, err = s.db.Exec(
			`INSERT INTO provider_quota_snapshots(provider_id, result_json, last_success_json, queried_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(provider_id) DO UPDATE SET
			 result_json = excluded.result_json,
			 last_success_json = excluded.last_success_json,
			 queried_at = excluded.queried_at,
			 updated_at = excluded.updated_at`,
			providerID, resultJSON, resultJSON, now, now,
		)
	} else {
		// Failure: update result_json but preserve last_success_json.
		_, err = s.db.Exec(
			`INSERT INTO provider_quota_snapshots(provider_id, result_json, last_success_json, queried_at, updated_at)
			 VALUES (?, ?, NULL, ?, ?)
			 ON CONFLICT(provider_id) DO UPDATE SET
			 result_json = excluded.result_json,
			 queried_at = excluded.queried_at,
			 updated_at = excluded.updated_at`,
			providerID, resultJSON, now, now,
		)
	}
	return err
}

// Get returns the latest snapshot for a provider, or nil if none exists.
func (s *SnapshotStore) Get(providerID string) (*QuotaSnapshot, error) {
	var resultJSON, queriedAt, updatedAt string
	var lastSuccessJSON sql.NullString

	err := s.db.QueryRow(
		`SELECT result_json, last_success_json, queried_at, updated_at FROM provider_quota_snapshots WHERE provider_id = ?`,
		providerID,
	).Scan(&resultJSON, &lastSuccessJSON, &queriedAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	result, err := DecodeResult(resultJSON)
	if err != nil {
		return nil, err
	}

	snap := &QuotaSnapshot{
		ProviderID: providerID,
		Result:     result,
		QueriedAt:  parseTime(queriedAt),
		UpdatedAt:  parseTime(updatedAt),
	}

	if lastSuccessJSON.Valid && lastSuccessJSON.String != "" && lastSuccessJSON.String != "null" {
		lastSuccess, err := DecodeResult(lastSuccessJSON.String)
		if err != nil {
			return nil, err
		}
		snap.LastSuccess = lastSuccess
	}

	return snap, nil
}

// GetAll returns snapshots for all providers that have one.
func (s *SnapshotStore) GetAll() (map[string]*QuotaSnapshot, error) {
	rows, err := s.db.Query(
		`SELECT provider_id, result_json, last_success_json, queried_at, updated_at FROM provider_quota_snapshots`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*QuotaSnapshot)
	for rows.Next() {
		var providerID, resultJSON, queriedAt, updatedAt string
		var lastSuccessJSON sql.NullString
		if err := rows.Scan(&providerID, &resultJSON, &lastSuccessJSON, &queriedAt, &updatedAt); err != nil {
			return nil, err
		}
		r, err := DecodeResult(resultJSON)
		if err != nil {
			return nil, err
		}
		snap := &QuotaSnapshot{
			ProviderID: providerID,
			Result:     r,
			QueriedAt:  parseTime(queriedAt),
			UpdatedAt:  parseTime(updatedAt),
		}
		if lastSuccessJSON.Valid && lastSuccessJSON.String != "" && lastSuccessJSON.String != "null" {
			ls, err := DecodeResult(lastSuccessJSON.String)
			if err != nil {
				return nil, err
			}
			snap.LastSuccess = ls
		}
		result[providerID] = snap
	}
	return result, rows.Err()
}

// Delete removes the snapshot for a provider.
func (s *SnapshotStore) Delete(providerID string) error {
	_, err := s.db.Exec(`DELETE FROM provider_quota_snapshots WHERE provider_id = ?`, providerID)
	return err
}

// DeleteAll removes all snapshots.
func (s *SnapshotStore) DeleteAll() error {
	_, err := s.db.Exec(`DELETE FROM provider_quota_snapshots`)
	return err
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
