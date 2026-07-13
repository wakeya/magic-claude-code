package failover

import "time"

// StateKind 分类故障切换的根因，决定恢复策略：
//   - quota_exhausted / deployment_unavailable / provider_unavailable：基于时间恢复，
//     到 DisabledUntil 自动重新入候选。
//   - credential_invalid：无时间恢复，只有 Token 实际变更或供应商测试成功才清除。
type StateKind string

const (
	StateKindQuota        StateKind = "quota_exhausted"
	StateKindCredential   StateKind = "credential_invalid"
	StateKindDeployment   StateKind = "deployment_unavailable"
	StateKindAvailability StateKind = "provider_unavailable"
)

// HasTimeBasedRecovery 报告该类别是否在 DisabledUntil 到期后自动恢复。
// 凭据失效类别不基于时间恢复，必须等待管理员行动。
func (k StateKind) HasTimeBasedRecovery() bool {
	return k == StateKindQuota || k == StateKindDeployment || k == StateKindAvailability
}

// Classification 是分类器对一次上游响应/错误的判定。
type Classification struct {
	// Eligible 为 true 时，该响应应触发自动故障切换；false 时保持原供应商。
	Eligible bool
	// Kind 失败类别（仅 Eligible=true 时有意义）。
	Kind StateKind
	// Reason 人类可读的稳定原因码（如 five_hour_quota_exhausted），用于事件展示与日志。
	Reason string
	// UpstreamCode 上游 HTTP 状态码（网络错误时为 0）。
	UpstreamCode int
	// BusinessCode 上游业务错误码（如 1308/1310/1210），来自 error.code，无则为空。
	// 与 UpstreamCode（HTTP 状态码）分开存储，满足事件展示「HTTP 状态码 / 业务码」。
	BusinessCode string
	// UpstreamError 上游错误摘要（脱敏后的 code/message 片段，进入事件展示）。
	UpstreamError string
	// DisabledUntil 摘除截止时间。零值表示无时间恢复（凭据失效）。
	// 到期后该供应商重新进入候选池。
	DisabledUntil time.Time
}

// FailoverState 是单个供应商的持久化摘除状态。
type FailoverState struct {
	ProviderID    string
	Kind          StateKind
	Reason        string
	UpstreamCode  int
	DisabledUntil time.Time // 零值 = 仅凭据失效等无时间恢复
	UpdatedAt     time.Time
}

// IsQuarantinedAt 报告该状态在 t 时刻是否仍处于摘除中。
// 无时间恢复的类别（凭据失效）永远返回 true，直到被显式清除。
func (s FailoverState) IsQuarantinedAt(t time.Time) bool {
	if s.ProviderID == "" {
		return false
	}
	if !s.Kind.HasTimeBasedRecovery() {
		return true
	}
	return s.DisabledUntil.After(t)
}

// Outcome 是故障切换事件的结局类型。
type Outcome string

const (
	OutcomeSwitched    Outcome = "switched"     // 成功切换到候选供应商
	OutcomeExhausted   Outcome = "exhausted"    // 所有候选均不可用/失败，未能切换
	OutcomeRetryFailed Outcome = "retry_failed" // 候选重试失败（中间态）
	OutcomeRecovered   Outcome = "recovered"    // 供应商从摘除中恢复
)

// FailoverEvent 是一条脱敏的全局故障切换事件记录。
// 事件是 MCC 全局观测数据，不绑定 Claude JSONL 会话，不写入 ~/.claude/projects。
type FailoverEvent struct {
	ID               int64     `json:"id"`
	OccurredAt       time.Time `json:"occurred_at"`
	FromProviderID   string    `json:"from_provider_id"`
	ToProviderID     string    `json:"to_provider_id"`
	FromProviderName string    `json:"from_provider_name"`
	ToProviderName   string    `json:"to_provider_name"`
	OriginalModel    string    `json:"original_model"`
	MappedModel      string    `json:"mapped_model"`
	UpstreamCode     int       `json:"upstream_code"`
	BusinessCode     string    `json:"business_code"`
	Reason           string    `json:"reason"`
	Outcome          Outcome   `json:"outcome"`
	DisabledUntil    *time.Time `json:"disabled_until,omitempty"`
}
