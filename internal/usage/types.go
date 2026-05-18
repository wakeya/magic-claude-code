package usage

import "time"

const (
	UsageSourceProvider = "provider"
	UsageSourceNone     = "none"

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
	ID                       string
	StartedAt                time.Time
	EndedAt                  *time.Time
	DurationMS               *int64
	UpstreamResponseHeaderMS *int64
	TimeToFirstByteMS        *int64
	StatusCode               *int
	ErrorType                string
	ErrorMessage             string
	Method                   string
	RequestPath              string
	BackendURL               string
	ProviderID               string
	ProviderName             string
	ProviderAPIURL           string
	SourceApp                string
	SourceEntrypoint         string
	UserAgent                string
	OriginalModel            string
	MappedModel              string
	Stream                   bool
	RequestBytes             int64
	ResponseBytes            int64
}

type TokenRecord struct {
	RequestID                string
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	UsageSource              string
	UsageParseStatus         string
	UsageParseError          string
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
