package usage

import "time"

const (
	UsageSourceProvider   = "provider"
	UsageSourceSessionLog = "session_log"
	UsageSourceNone       = "none"

	ParseStatusOK                = "ok"
	ParseStatusMissing           = "missing"
	ParseStatusUnsupportedFormat = "unsupported_format"
	ParseStatusParseError        = "parse_error"
	ParseStatusSkippedNon2xx     = "skipped_non_2xx"
	ParseStatusNetworkError      = "network_error"

	ErrorHTTP            = "http_error"
	ErrorNetwork         = "network_error"
	ErrorUpstreamTimeout = "upstream_timeout"
	ErrorClientAborted   = "client_aborted"
)

type RequestRecord struct {
	ID                       string     `json:"id"`
	StartedAt                time.Time  `json:"started_at"`
	EndedAt                  *time.Time `json:"ended_at"`
	DurationMS               *int64     `json:"duration_ms"`
	UpstreamResponseHeaderMS *int64     `json:"upstream_response_header_ms"`
	TimeToFirstByteMS        *int64     `json:"time_to_first_byte_ms"`
	StatusCode               *int       `json:"status_code"`
	ErrorType                string     `json:"error_type"`
	ErrorMessage             string     `json:"error_message"`
	Method                   string     `json:"method"`
	RequestPath              string     `json:"request_path"`
	BackendURL               string     `json:"backend_url"`
	ProviderID               string     `json:"provider_id"`
	ProviderName             string     `json:"provider_name"`
	ProviderAPIURL           string     `json:"provider_api_url"`
	SourceApp                string     `json:"source_app"`
	SourceEntrypoint         string     `json:"source_entrypoint"`
	UserAgent                string     `json:"user_agent"`
	OriginalModel            string     `json:"original_model"`
	MappedModel              string     `json:"mapped_model"`
	Stream                   bool       `json:"stream"`
	RequestBytes             int64      `json:"request_bytes"`
	ResponseBytes            int64      `json:"response_bytes"`
}

type TokenRecord struct {
	RequestID                string `json:"request_id"`
	InputTokens              int64  `json:"input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	UsageSource              string `json:"usage_source"`
	UsageParseStatus         string `json:"usage_parse_status"`
	UsageParseError          string `json:"usage_parse_error"`
}

type UsageValues struct {
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	HasAny                   bool
}

type RequestMetadata struct {
	OriginalModel    string
	Stream           bool
	SourceApp        string
	SourceEntrypoint string
	UserAgent        string
}

type Filter struct {
	From             time.Time
	To               time.Time
	Now              time.Time
	TZ               string
	SourceApp        string
	SourceEntrypoint string
	ProviderID       string
	Model            string
	Status           string
	UsageSource      string
	UsageParseStatus string
	RequestPath      string
	Query            string
	Page             int
	PageSize         int
}

type Summary struct {
	ServiceRequestsTotal  int64      `json:"service_requests_total"`
	ProviderRequestsTotal int64      `json:"provider_requests_total"`
	TodayProviderRequests int64      `json:"today_provider_requests"`
	TokenConsumptionTotal int64      `json:"token_consumption_total"`
	TodayTokenConsumption int64      `json:"today_token_consumption"`
	FailedRequests        int64      `json:"failed_requests"`
	UsageCoverage         float64    `json:"usage_coverage"`
	LastProviderRequest   *time.Time `json:"last_provider_request"`
}

type TrendPoint struct {
	Bucket                   string  `json:"bucket"`
	InputTokens              int64   `json:"input_tokens"`
	OutputTokens             int64   `json:"output_tokens"`
	CacheCreationInputTokens int64   `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64   `json:"cache_read_input_tokens"`
	TokenConsumptionTotal    int64   `json:"token_consumption_total"`
	ProviderRequestsTotal    int64   `json:"provider_requests_total"`
	FailedRequests           int64   `json:"failed_requests"`
	UsageCoverage            float64 `json:"usage_coverage"`
}

type RequestRow struct {
	RequestRecord
	TokenRecord
}

type RequestPage struct {
	Rows     []RequestRow `json:"rows"`
	Total    int64        `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

type AggregateRow struct {
	Name                  string  `json:"name"`
	ProviderID            string  `json:"provider_id,omitempty"`
	ProviderName          string  `json:"provider_name,omitempty"`
	MappedModel           string  `json:"mapped_model,omitempty"`
	TotalRequests         int64   `json:"total_requests"`
	FailedRequests        int64   `json:"failed_requests"`
	TokenConsumptionTotal int64   `json:"token_consumption_total"`
	UsageCoverage         float64 `json:"usage_coverage"`
	AverageDurationMS     float64 `json:"average_duration_ms"`
}

type CoverageRow struct {
	ProviderName         string    `json:"provider_name"`
	ProviderAPIURL       string    `json:"provider_api_url"`
	MappedModel          string    `json:"mapped_model"`
	SourceEntrypoint     string    `json:"source_entrypoint"`
	TotalRequests        int64     `json:"total_requests"`
	SuccessRequests      int64     `json:"success_requests"`
	ErrorRequests        int64     `json:"error_requests"`
	WithUsageRequests    int64     `json:"with_usage_requests"`
	WithoutUsageRequests int64     `json:"without_usage_requests"`
	UsageCoverage        float64   `json:"usage_coverage"`
	TopUsageParseStatus  string    `json:"top_usage_parse_status"`
	LastSeenAt           time.Time `json:"last_seen_at"`
}
