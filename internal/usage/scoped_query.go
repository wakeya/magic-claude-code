package usage

const scopedSessionRowPredicate = `(
	t.usage_source = 'session_log'
	OR r.source_entrypoint = 'session_log'
	OR r.provider_id = '_session'
)`

const scopedCandidateEpochSeconds = `COALESCE(
	CAST(strftime('%s', provider.started_at) AS INTEGER),
	-62135596800
)`

const scopedCandidateStartedAtFraction = `CASE
	WHEN strftime('%s', provider.started_at) IS NULL THEN '000000000'
	WHEN instr(provider.started_at, '.') = 0 THEN '000000000'
	WHEN substr(provider.started_at, -1) = 'Z' THEN
		substr(
			substr(
				provider.started_at,
				instr(provider.started_at, '.') + 1,
				length(provider.started_at) - instr(provider.started_at, '.') - 1
			) || '000000000',
			1,
			9
		)
	ELSE
		substr(
			substr(
				provider.started_at,
				instr(provider.started_at, '.') + 1,
				length(provider.started_at) - instr(provider.started_at, '.') - 6
			) || '000000000',
			1,
			9
		)
END`

// buildScopedCTE returns the common filtered/candidate/scoped datasets used by
// SQL-backed usage reads. Callers append their own projection, aggregation,
// ordering, and pagination and pass the returned arguments unchanged.
func buildScopedCTE(filter Filter) (string, []any) {
	where, args := filterWhere(filter)
	filteredWhere := ""
	if where != "" {
		filteredWhere = "\n\tWHERE " + where
	}

	scope := filter.StatsScope
	if scope == "" {
		scope = StatsScopeEffective
	}
	args = append(args, scope)

	return `WITH filtered AS (
	SELECT
		r.id AS request_id,
		r.started_at,
		CASE WHEN ` + scopedSessionRowPredicate + ` THEN 1 ELSE 0 END AS is_session_log,
		CASE
			WHEN t.usage_parse_status = 'ok'
				AND ` + scopedSessionRowPredicate + `
			THEN 1
			ELSE 0
		END AS is_dedupe_session,
		CASE
			WHEN NOT ` + scopedSessionRowPredicate + `
				AND r.source_app = 'claude_code'
				AND t.usage_source = 'provider'
				AND t.usage_parse_status = 'ok'
			THEN 1
			ELSE 0
		END AS is_provider_usage
	FROM usage_requests r
	JOIN usage_tokens t ON t.request_id = r.id` + filteredWhere + `
),
candidate AS (
	SELECT
		d.session_request_id,
		d.provider_request_id,
		ROW_NUMBER() OVER (
			PARTITION BY d.session_request_id
			ORDER BY
				d.model_priority ASC,
				` + scopedCandidateEpochSeconds + ` ASC,
				` + scopedCandidateStartedAtFraction + ` ASC,
				provider.request_id ASC
		) AS candidate_rank
	FROM usage_dedupe_candidates d
	JOIN filtered session
		ON session.request_id = d.session_request_id
		AND session.is_dedupe_session = 1
	JOIN filtered provider
		ON provider.request_id = d.provider_request_id
		AND provider.is_provider_usage = 1
),
scoped AS (
	SELECT
		filtered.request_id,
		filtered.started_at,
		filtered.is_session_log,
		CASE
			WHEN candidate.provider_request_id IS NOT NULL THEN 'duplicate'
			ELSE ''
		END AS dedupe_status,
		COALESCE(candidate.provider_request_id, '') AS dedupe_request_id
	FROM filtered
	LEFT JOIN candidate
		ON candidate.session_request_id = filtered.request_id
		AND candidate.candidate_rank = 1
	WHERE CASE ?
		WHEN 'raw' THEN 1
		WHEN 'provider' THEN filtered.is_session_log = 0
		WHEN 'session_log' THEN filtered.is_session_log = 1
		ELSE (
			filtered.is_session_log = 0
			OR candidate.provider_request_id IS NULL
		)
	END
)`, args
}
