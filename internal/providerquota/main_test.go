package providerquota

import (
	"fmt"
	"os"
	"testing"
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
		}
		os.Exit(RunScriptWorker(os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}
