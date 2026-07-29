package providerquota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"time"
)

const (
	maxScriptWorkerStdoutSize  = 4 * 1024 * 1024
	maxScriptWorkerStderrSize  = 64 * 1024
	scriptWorkerProcessTimeout = 3 * time.Second
)

type scriptWorkerRunner interface {
	ParseRequest(context.Context, string) (*ScriptRequest, error)
	RunExtractor(context.Context, string, string) (any, error)
}

type processScriptWorkerRunner struct {
	executable     func() (string, error)
	commandContext func(context.Context, string, ...string) *exec.Cmd
}

func newProcessScriptWorkerRunner() *processScriptWorkerRunner {
	return &processScriptWorkerRunner{
		executable:     os.Executable,
		commandContext: exec.CommandContext,
	}
}

func (r *processScriptWorkerRunner) ParseRequest(ctx context.Context, script string) (*ScriptRequest, error) {
	payload, err := r.run(ctx, scriptWorkerRequest{
		Version:   scriptWorkerProtocolVersion,
		Operation: scriptWorkerOperationParseRequest,
		Script:    script,
	})
	if err != nil {
		return nil, err
	}

	var req ScriptRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, errors.New("invalid script worker response")
	}
	return &req, nil
}

func (r *processScriptWorkerRunner) RunExtractor(ctx context.Context, script, responseBody string) (any, error) {
	payload, err := r.run(ctx, scriptWorkerRequest{
		Version:      scriptWorkerProtocolVersion,
		Operation:    scriptWorkerOperationRunExtractor,
		Script:       script,
		ResponseBody: responseBody,
	})
	if err != nil {
		return nil, err
	}

	var extracted any
	if err := json.Unmarshal(payload, &extracted); err != nil {
		return nil, errors.New("invalid script worker response")
	}
	return extracted, nil
}

func (r *processScriptWorkerRunner) run(ctx context.Context, req scriptWorkerRequest) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.New("script worker canceled")
	}

	input, err := json.Marshal(req)
	if err != nil {
		return nil, errors.New("invalid script worker request")
	}
	executable, err := r.executable()
	if err != nil {
		return nil, errors.New("script worker executable is unavailable")
	}

	workerCtx, cancel := context.WithTimeout(ctx, scriptWorkerProcessTimeout)
	defer cancel()

	cmd := r.commandContext(workerCtx, executable, ScriptWorkerArg)
	cmd.Stdin = bytes.NewReader(input)
	stdout := newBoundedWorkerBuffer(maxScriptWorkerStdoutSize)
	stderr := newBoundedWorkerBuffer(maxScriptWorkerStderrSize)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("script worker output limit exceeded")
	}
	if err := workerCtx.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("script worker canceled")
		}
		return nil, errors.New("script worker execution timed out")
	}

	resp, valid := decodeScriptWorkerResponse(stdout.Bytes())
	if !valid {
		if runErr != nil {
			return nil, errors.New("script worker terminated unexpectedly")
		}
		return nil, errors.New("invalid script worker response")
	}

	if resp.OK {
		if runErr != nil || resp.Error != "" || len(resp.Payload) == 0 {
			return nil, errors.New("invalid script worker response")
		}
		return resp.Payload, nil
	}
	if resp.Error == "" || len(resp.Payload) != 0 {
		return nil, errors.New("invalid script worker response")
	}
	return nil, errors.New(resp.Error)
}

func decodeScriptWorkerResponse(data []byte) (scriptWorkerResponse, bool) {
	var resp scriptWorkerResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&resp); err != nil || !jsonDecoderAtEOF(decoder) {
		return scriptWorkerResponse{}, false
	}
	if resp.Version != scriptWorkerProtocolVersion {
		return scriptWorkerResponse{}, false
	}
	return resp, true
}

type boundedWorkerBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func newBoundedWorkerBuffer(limit int) *boundedWorkerBuffer {
	return &boundedWorkerBuffer{limit: limit}
}

func (b *boundedWorkerBuffer) Write(p []byte) (int, error) {
	originalLength := len(p)
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.exceeded = b.exceeded || originalLength > 0
		return originalLength, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.exceeded = true
	}
	_, _ = b.buffer.Write(p)
	return originalLength, nil
}

func (b *boundedWorkerBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

func (b *boundedWorkerBuffer) Len() int {
	return b.buffer.Len()
}
