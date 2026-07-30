package providerquota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type scriptWorkerLimitFunc func() (cleanup func(), err error)

func RunScriptWorker(in io.Reader, out io.Writer) int {
	return runScriptWorker(in, out, applyScriptWorkerResourceLimits)
}

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

	req, err := decodeScriptWorkerRequest(in)
	if err != nil {
		writeScriptWorkerProtocolError(out)
		return 2
	}
	if req.Version != scriptWorkerProtocolVersion ||
		len(req.Script) == 0 ||
		len(req.Script) > maxScriptSourceSize ||
		req.ResponseBodySize != int64(len(req.ResponseBody)) ||
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

func encodeScriptWorkerRequest(req scriptWorkerRequest) ([]byte, error) {
	req.ResponseBodySize = int64(len(req.ResponseBody))
	body := []byte(req.ResponseBody)
	if req.ResponseBodySize > maxResponseBodySize {
		return nil, fmt.Errorf("worker response body exceeds %d bytes", maxResponseBodySize)
	}
	header := req
	header.ResponseBody = ""
	// The header is a trusted in-process frame decoded by json.Unmarshal, so
	// HTML escaping adds no security and would 6x-expand scripts full of
	// '<', '>', '&' past maxScriptWorkerHeaderSize. Disable it.
	var headerBuf bytes.Buffer
	enc := json.NewEncoder(&headerBuf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(header); err != nil {
		return nil, err
	}
	rawHeader := headerBuf.Bytes()
	if n := len(rawHeader); n > 0 && rawHeader[n-1] == '\n' {
		rawHeader = rawHeader[:n-1] // Encoder.Encode appends '\n'; we emit our own frame delimiter below.
	}
	if int64(len(rawHeader)) > maxScriptWorkerHeaderSize {
		return nil, fmt.Errorf("worker header exceeds %d bytes", maxScriptWorkerHeaderSize)
	}
	input := make([]byte, 0, len(rawHeader)+1+len(body))
	input = append(input, rawHeader...)
	input = append(input, '\n')
	input = append(input, body...)
	return input, nil
}

func decodeScriptWorkerRequest(in io.Reader) (scriptWorkerRequest, error) {
	data, err := io.ReadAll(io.LimitReader(in, maxScriptWorkerInputSize+1))
	if err != nil {
		return scriptWorkerRequest{}, err
	}
	if int64(len(data)) > maxScriptWorkerInputSize {
		return scriptWorkerRequest{}, fmt.Errorf("worker input exceeds %d bytes", maxScriptWorkerInputSize)
	}
	headerBytes, bodyBytes, found := bytes.Cut(data, []byte{'\n'})
	if !found || int64(len(headerBytes)) > maxScriptWorkerHeaderSize {
		return scriptWorkerRequest{}, fmt.Errorf("invalid worker request frame")
	}

	var req scriptWorkerRequest
	decoder := json.NewDecoder(bytes.NewReader(headerBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || !jsonDecoderAtEOF(decoder) {
		return scriptWorkerRequest{}, fmt.Errorf("invalid worker request header")
	}
	if req.ResponseBodySize < 0 ||
		req.ResponseBodySize > maxResponseBodySize ||
		req.ResponseBodySize != int64(len(bodyBytes)) {
		return scriptWorkerRequest{}, fmt.Errorf("invalid worker response body frame")
	}
	req.ResponseBody = string(bodyBytes)
	return req, nil
}

func jsonDecoderAtEOF(decoder *json.Decoder) bool {
	var extra any
	return decoder.Decode(&extra) == io.EOF
}
