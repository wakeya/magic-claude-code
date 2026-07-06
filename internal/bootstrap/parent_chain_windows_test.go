//go:build windows

package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Windows F-1: an unmounted drive root cannot be created by MkdirAll, so the
// profile pre-scan must reject it before setx mutates the user environment.
func TestPersistNodeCACert_Windows_MissingVolumeRoot_NoSetx(t *testing.T) {
	var missingRoot string
	for drive := 'Z'; drive >= 'D'; drive-- {
		candidate := fmt.Sprintf("%c:\\", drive)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			missingRoot = candidate
			break
		}
	}
	if missingRoot == "" {
		t.Skip("no unused Windows drive letter available")
	}

	fakeHome := filepath.Join(missingRoot, "mcc-missing-home")
	withPwshHooks(t, fakeHome)

	calls := 0
	previous := setxEnvVar
	setxEnvVar = func(string, string) error {
		calls++
		return nil
	}
	t.Cleanup(func() { setxEnvVar = previous })

	err := (&osEnvAdapter{}).persistNodeCACertWindows(filepath.Join(t.TempDir(), "ca.crt"))
	if err == nil {
		t.Fatal("expected missing volume root to fail profile scan")
	}
	if calls != 0 {
		t.Fatalf("setx called %d times; want 0", calls)
	}
}
