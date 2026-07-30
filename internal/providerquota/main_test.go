package providerquota

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const scriptWorkerTestBehaviorEnv = "MCC_TEST_SCRIPT_WORKER_BEHAVIOR"

func TestMain(m *testing.M) {
	if IsScriptWorkerInvocation(os.Args[1:]) {
		switch os.Getenv(scriptWorkerTestBehaviorEnv) {
		case "malformed":
			_, _ = fmt.Fprint(os.Stdout, "not-json")
			os.Exit(0)
		case "trailing":
			_, _ = fmt.Fprint(os.Stdout, `{"version":1,"ok":true,"payload":{"url":"https://example.com","method":"GET"}} trailing-log`)
			os.Exit(0)
		case "stdout_overflow":
			_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", maxScriptWorkerStdoutSize+1))
			os.Exit(0)
		case "stderr_overflow":
			_, _ = fmt.Fprint(os.Stderr, strings.Repeat("x", maxScriptWorkerStderrSize+1)+"fatal-worker-secret")
		case "nonzero":
			os.Exit(9)
		case "panic":
			panic("fatal-worker-secret")
		case "wrong_version":
			_, _ = fmt.Fprint(os.Stdout, `{"version":2,"ok":true,"payload":{"url":"https://example.com","method":"GET"}}`)
			os.Exit(0)
		case "failed_with_payload":
			_, _ = fmt.Fprint(os.Stdout, `{"version":1,"ok":false,"error":"fatal-worker-secret","payload":{}}`)
			os.Exit(0)
		case "sleep":
			time.Sleep(10 * time.Second)
		}
		os.Exit(RunScriptWorker(os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}
