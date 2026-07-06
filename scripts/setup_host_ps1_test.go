package scripts

import (
	"os"
	"strings"
	"testing"
)

func TestSetupHostPS1_NodeCAGuidance(t *testing.T) {
	content, err := os.ReadFile("setup-host.ps1")
	if err != nil {
		t.Fatalf("read setup-host.ps1: %v", err)
	}
	text := string(content)

	if strings.Contains(text, "setx NODE_EXTRA_CA_CERTS") {
		t.Fatal("elevated host setup must not tell users to write the elevated account's HKCU")
	}
	for _, want := range []string{
		"普通用户启动一次 mcc",
		"完全退出并重新启动 Orca",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("setup-host.ps1 missing guidance %q", want)
		}
	}
}
