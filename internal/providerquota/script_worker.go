package providerquota

import (
	"bytes"
	"encoding/json"
	"io"
)

type scriptWorkerLimitFunc func() (cleanup func(), err error)

func runScriptWorker(in io.Reader, out io.Writer, applyLimits scriptWorkerLimitFunc) int {
	cleanup, err := applyLimits()
	if err != nil {
		writeScriptWorkerResponse(out, scriptWorkerResponse{
			Version: scriptWorkerProtocolVersion,
			Error:   "script worker resource limit setup failed",
		})
		return 3
	}
	if cleanup != nil {
		defer cleanup()
	}

	data, err := io.ReadAll(io.LimitReader(in, maxScriptWorkerInputSize+1))
	if err != nil || int64(len(data)) > maxScriptWorkerInputSize {
		writeScriptWorkerProtocolError(out)
		return 2
	}

	var req scriptWorkerRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeScriptWorkerProtocolError(out)
		return 2
	}
	if req.Version != scriptWorkerProtocolVersion ||
		len(req.Script) == 0 ||
		len(req.Script) > maxScriptSourceSize ||
		len(req.ResponseBody) > maxResponseBodySize {
		writeScriptWorkerProtocolError(out)
		return 2
	}

	var payload any
	switch req.Operation {
	case scriptWorkerOperationParseRequest:
		if req.ResponseBody != "" {
			writeScriptWorkerProtocolError(out)
			return 2
		}
		payload, err = parseRequestInProcess(req.Script)
	case scriptWorkerOperationRunExtractor:
		payload, err = runExtractorInProcess(req.Script, req.ResponseBody)
	default:
		writeScriptWorkerProtocolError(out)
		return 2
	}
	if err != nil {
		writeScriptWorkerResponse(out, scriptWorkerResponse{
			Version: scriptWorkerProtocolVersion,
			Error:   err.Error(),
		})
		return 1
	}

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		writeScriptWorkerResponse(out, scriptWorkerResponse{
			Version: scriptWorkerProtocolVersion,
			Error:   "script worker result is not serializable",
		})
		return 1
	}
	writeScriptWorkerResponse(out, scriptWorkerResponse{
		Version: scriptWorkerProtocolVersion,
		OK:      true,
		Payload: rawPayload,
	})
	return 0
}

func writeScriptWorkerProtocolError(out io.Writer) {
	writeScriptWorkerResponse(out, scriptWorkerResponse{
		Version: scriptWorkerProtocolVersion,
		Error:   "invalid script worker protocol request",
	})
}

func writeScriptWorkerResponse(out io.Writer, resp scriptWorkerResponse) {
	_ = json.NewEncoder(out).Encode(resp)
}
