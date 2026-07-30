package providerquota

import "encoding/json"

const (
	ScriptWorkerArg                         = "__script-worker"
	scriptWorkerProtocolVersion             = 1
	scriptWorkerOperationParseRequest       = "parse_request"
	scriptWorkerOperationRunExtractor       = "run_extractor"
	maxScriptWorkerHeaderSize         int64 = 256 * 1024
	maxScriptWorkerInputSize          int64 = maxScriptWorkerHeaderSize + 1 + maxResponseBodySize
	maxScriptSourceSize                     = 64 * 1024
)

type scriptWorkerRequest struct {
	Version          int    `json:"version"`
	Operation        string `json:"operation"`
	Script           string `json:"script"`
	ResponseBodySize int64  `json:"response_body_size,omitempty"`
	ResponseBody     string `json:"-"`
}

type scriptWorkerResponse struct {
	Version int             `json:"version"`
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func IsScriptWorkerInvocation(args []string) bool {
	return len(args) == 1 && args[0] == ScriptWorkerArg
}
