package providerquota

import "encoding/json"

const (
	ScriptWorkerArg                         = "__script-worker"
	scriptWorkerProtocolVersion             = 1
	scriptWorkerOperationParseRequest       = "parse_request"
	scriptWorkerOperationRunExtractor       = "run_extractor"
	maxScriptWorkerInputSize          int64 = 3 * 1024 * 1024
	maxScriptSourceSize                     = 64 * 1024
)

type scriptWorkerRequest struct {
	Version      int    `json:"version"`
	Operation    string `json:"operation"`
	Script       string `json:"script"`
	ResponseBody string `json:"response_body,omitempty"`
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
