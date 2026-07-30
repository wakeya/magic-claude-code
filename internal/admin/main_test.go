package admin

import (
	"os"
	"testing"

	"magic-claude-code/internal/providerquota"
)

func TestMain(m *testing.M) {
	if providerquota.IsScriptWorkerInvocation(os.Args[1:]) {
		os.Exit(providerquota.RunScriptWorker(os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}
