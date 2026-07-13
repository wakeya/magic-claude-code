package failover

import (
	"database/sql"
	"time"
)

const (
	eventRetentionMaxRows = 1000
	eventRetentionAge     = 30 * 24 * time.Hour
	eventListDefaultLimit = 50
	eventListMaxLimit     = 100
)

// Store 在 MCC 自己的 SQLite（proxy.db）中持久化供应商摘除状态与全局事件。
// 它绝不写入 Claude JSONL（~/.claude/projects）；事件是 MCC 全局观测数据。
type Store struct {
	db *sql.DB
}

// NewStore 创建故障切换状态/事件存储。db 通常是 configStore.DB()（proxy.db）。
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Migrate 建表（幂等）。两张表均不存储任何密钥/请求体/响应体字段。
func (s *Store) Migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS provider_failover_state (
			provider_id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			reason TEXT NOT NULL,
			upstream_code INTEGER NOT NULL DEFAULT 0,
			disabled_until TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS provider_failover_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			occurred_at TEXT NOT NULL,
			from_provider_id TEXT NOT NULL DEFAULT '',
			to_provider_id TEXT NOT NULL DEFAULT '',
			from_provider_name TEXT NOT NULL DEFAULT '',
			to_provider_name TEXT NOT NULL DEFAULT '',
			original_model TEXT NOT NULL DEFAULT '',
			mapped_model TEXT NOT NULL DEFAULT '',
			upstream_code INTEGER NOT NULL DEFAULT 0,
			business_code TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			outcome TEXT NOT NULL,
			disabled_until TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_failover_events_occurred ON provider_failover_events(occurred_at DESC, id DESC);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return s.ensureEventColumns()
}

// ensureEventColumns 给已存在的 provider_failover_events 表补齐后续新增列（向前兼容早期 dev DB）。
func (s *Store) ensureEventColumns() error {
	rows, err := s.db.Query(`PRAGMA table_info(provider_failover_events)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	existing := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	add := map[string]string{
		"business_code": `ALTER TABLE provider_failover_events ADD COLUMN business_code TEXT NOT NULL DEFAULT ''`,
	}
	for name, stmt := range add {
		if _, ok := existing[name]; !ok {
			if _, err := s.db.Exec(stmt); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetState upsert 一条供应商摘除状态。
func (s *Store) SetState(st FailoverState) error {
	_, err := s.db.Exec(
		`INSERT INTO provider_failover_state(provider_id, kind, reason, upstream_code, disabled_until, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(provider_id) DO UPDATE SET
		   kind = excluded.kind,
		   reason = excluded.reason,
		   upstream_code = excluded.upstream_code,
		   disabled_until = excluded.disabled_until,
		   updated_at = excluded.updated_at`,
		st.ProviderID, string(st.Kind), st.Reason, st.UpstreamCode, formatStoreTime(st.DisabledUntil), formatStoreTime(st.UpdatedAt),
	)
	return err
}

// GetState 读取供应商摘除状态。
func (s *Store) GetState(providerID string) (FailoverState, bool) {
	var st FailoverState
	var kind, reason, disabledUntil, updatedAt string
	err := s.db.QueryRow(
		`SELECT kind, reason, upstream_code, disabled_until, updated_at FROM provider_failover_state WHERE provider_id = ?`,
		providerID,
	).Scan(&kind, &reason, &st.UpstreamCode, &disabledUntil, &updatedAt)
	if err == sql.ErrNoRows {
		return FailoverState{}, false
	}
	if err != nil {
		return FailoverState{}, false
	}
	st.ProviderID = providerID
	st.Kind = StateKind(kind)
	st.Reason = reason
	st.DisabledUntil = parseStoreTime(disabledUntil)
	st.UpdatedAt = parseStoreTime(updatedAt)
	return st, true
}

// DeleteState 删除供应商摘除状态（恢复或供应商被删除时调用）。
func (s *Store) DeleteState(providerID string) error {
	_, err := s.db.Exec(`DELETE FROM provider_failover_state WHERE provider_id = ?`, providerID)
	return err
}

// ListQuarantined 返回当前（在 t 时刻）仍处于摘除中的供应商状态。
func (s *Store) ListQuarantined(t time.Time) []FailoverState {
	rows, err := s.db.Query(`SELECT provider_id, kind, reason, upstream_code, disabled_until, updated_at FROM provider_failover_state`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []FailoverState
	for rows.Next() {
		var st FailoverState
		var kind, reason, disabledUntil, updatedAt string
		if err := rows.Scan(&st.ProviderID, &kind, &reason, &st.UpstreamCode, &disabledUntil, &updatedAt); err != nil {
			return nil
		}
		st.Kind = StateKind(kind)
		st.Reason = reason
		st.DisabledUntil = parseStoreTime(disabledUntil)
		st.UpdatedAt = parseStoreTime(updatedAt)
		if st.IsQuarantinedAt(t) {
			out = append(out, st)
		}
	}
	return out
}

// EventInput 是记录事件时传入的原始输入。Reason 等字段必须已脱敏（不含 token/body/query）。
type EventInput struct {
	OccurredAt       time.Time
	FromProviderID   string
	ToProviderID     string
	FromProviderName string
	ToProviderName   string
	OriginalModel    string
	MappedModel      string
	UpstreamCode     int
	BusinessCode     string
	Reason           string
	Outcome          Outcome
	DisabledUntil    time.Time // 零值表示无
}

// InsertEvent 追加一条事件并执行保留策略（30 天 / 1000 行）。OccurredAt 零值取当前时间。
func (s *Store) InsertEvent(in EventInput) error {
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT INTO provider_failover_events(occurred_at, from_provider_id, to_provider_id, from_provider_name, to_provider_name, original_model, mapped_model, upstream_code, business_code, reason, outcome, disabled_until)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatStoreTime(in.OccurredAt),
		in.FromProviderID, in.ToProviderID,
		in.FromProviderName, in.ToProviderName,
		in.OriginalModel, in.MappedModel,
		in.UpstreamCode, in.BusinessCode, in.Reason, string(in.Outcome),
		formatStoreTime(in.DisabledUntil),
	)
	if err != nil {
		return err
	}
	return s.pruneEvents()
}

func (s *Store) pruneEvents() error {
	cutoff := time.Now().UTC().Add(-eventRetentionAge)
	if _, err := s.db.Exec(`DELETE FROM provider_failover_events WHERE occurred_at < ?`, formatStoreTime(cutoff)); err != nil {
		return err
	}
	// 仅保留最近 eventRetentionMaxRows 条（按 id DESC）。
	if _, err := s.db.Exec(
		`DELETE FROM provider_failover_events WHERE id NOT IN (
			SELECT id FROM provider_failover_events ORDER BY id DESC LIMIT ?
		)`,
		eventRetentionMaxRows,
	); err != nil {
		return err
	}
	return nil
}

// ListEvents 返回事件列表（最新优先）。limit 钳制到 [1,100]，0 → 默认 50。
// knownProviderIDs 为当前存在的供应商 ID 集合；引用已删除供应商的事件会抹空其 ID（保留名字用于历史展示）。
func (s *Store) ListEvents(limit int, knownProviderIDs map[string]bool) []FailoverEvent {
	if limit <= 0 {
		limit = eventListDefaultLimit
	}
	if limit > eventListMaxLimit {
		limit = eventListMaxLimit
	}
	rows, err := s.db.Query(
		`SELECT id, occurred_at, from_provider_id, to_provider_id, from_provider_name, to_provider_name, original_model, mapped_model, upstream_code, business_code, reason, outcome, disabled_until
		 FROM provider_failover_events ORDER BY occurred_at DESC, id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []FailoverEvent
	for rows.Next() {
		var e FailoverEvent
		var occurredAt, fromID, toID, fromName, toName, origModel, mappedModel, reason, outcome, disabledUntil string
		if err := rows.Scan(&e.ID, &occurredAt, &fromID, &toID, &fromName, &toName, &origModel, &mappedModel, &e.UpstreamCode, &e.BusinessCode, &reason, &outcome, &disabledUntil); err != nil {
			return nil
		}
		e.OccurredAt = parseStoreTime(occurredAt)
		e.OriginalModel = origModel
		e.MappedModel = mappedModel
		e.Reason = reason
		e.Outcome = Outcome(outcome)
		if t := parseStoreTime(disabledUntil); !t.IsZero() {
			e.DisabledUntil = &t
		}
		// 抹空已删除供应商的 ID；名字保留。
		if knownProviderIDs == nil || knownProviderIDs[fromID] {
			e.FromProviderID = fromID
		}
		if knownProviderIDs == nil || knownProviderIDs[toID] {
			e.ToProviderID = toID
		}
		e.FromProviderName = fromName
		e.ToProviderName = toName
		out = append(out, e)
	}
	return out
}

// CountEvents 返回事件总数（测试/诊断用）。
func (s *Store) CountEvents() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM provider_failover_events`).Scan(&n)
	return n, err
}

func formatStoreTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseStoreTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}
	}
	return t
}
