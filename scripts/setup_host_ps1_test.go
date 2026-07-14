package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestDockerHostHelperRejectsStaleCATrustMarker(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(cert, []byte("new-ca"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ca-trust-installed"), []byte(`{"action":"ca-trust-installed","fingerprint":"stale"}`), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", "docker-host-helper.sh", "trust", "install", cert)
	cmd.Env = append(os.Environ(), "MCC_DATA_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected stale CA marker to fail, output: %s", out)
	}
	if !strings.Contains(string(out), "stale") && !strings.Contains(string(out), "mismatch") {
		t.Fatalf("expected stale marker guidance, got: %s", out)
	}
}

func TestSetupHostScriptsAvoidSymlinkMarkerWrites(t *testing.T) {
	shellContent, err := os.ReadFile("setup-host.sh")
	if err != nil {
		t.Fatalf("read setup-host.sh: %v", err)
	}
	psContent, err := os.ReadFile("setup-host.ps1")
	if err != nil {
		t.Fatalf("read setup-host.ps1: %v", err)
	}

	for name, content := range map[string]string{
		"setup-host.sh":  string(shellContent),
		"setup-host.ps1": string(psContent),
	} {
		if !strings.Contains(content, "safe_marker_path") && !strings.Contains(content, "Test-SafeMarkerPath") {
			t.Errorf("%s should validate marker path before writing", name)
		}
	}
}

func TestWindowsServiceScriptsUseSafePasswordDefaults(t *testing.T) {
	for _, path := range []string{"start-mcc.ps1", "register-mcc-task.ps1", "stop-mcc.ps1"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s should exist: %v", path, err)
		}
	}
	startContent, err := os.ReadFile("start-mcc.ps1")
	if err != nil {
		t.Fatalf("read start-mcc.ps1: %v", err)
	}
	registerContent, err := os.ReadFile("register-mcc-task.ps1")
	if err != nil {
		t.Fatalf("read register-mcc-task.ps1: %v", err)
	}
	if strings.Contains(string(startContent), `Password = "admin123"`) {
		t.Fatal("start-mcc.ps1 must not default to admin123")
	}
	if strings.Contains(string(registerContent), `Password = "admin123"`) {
		t.Fatal("register-mcc-task.ps1 must not default to admin123")
	}
	if !strings.Contains(string(registerContent), "password is stored in the scheduled task") {
		t.Fatal("register-mcc-task.ps1 should warn when persisting a password in task arguments")
	}
}

func TestReleasePackageIncludesWindowsServiceScripts(t *testing.T) {
	content, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatalf("read release.sh: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"scripts/start-mcc.ps1",
		"scripts/stop-mcc.ps1",
		"scripts/register-mcc-task.ps1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release.sh should package %s", want)
		}
	}
}

func TestReleasePackageIncludesScriptUsageDocs(t *testing.T) {
	content, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatalf("read release.sh: %v", err)
	}
	text := string(content)

	for _, want := range []string{
		"README.en.md",
		"scripts/SCRIPTS.md",
		"scripts/SCRIPTS.en.md",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release.sh should package %s", want)
		}
	}

	for _, path := range []string{"SCRIPTS.md", "SCRIPTS.en.md"} {
		doc, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s should exist: %v", path, err)
		}
		docText := string(doc)
		for _, want := range []string{"setup-host.sh", "setup-host.ps1", "register-mcc-task.ps1"} {
			if !strings.Contains(docText, want) {
				t.Errorf("%s should document %s", path, want)
			}
		}
	}
}

func TestReleasePackageUsesPlatformSpecificScripts(t *testing.T) {
	content, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatalf("read release.sh: %v", err)
	}
	text := string(content)

	for _, want := range []string{
		`cp scripts/setup-host.ps1 "$pkg_dir/setup-host.ps1"`,
		`cp scripts/start-mcc.ps1 "$pkg_dir/start-mcc.ps1"`,
		`cp scripts/setup-host.sh "$pkg_dir/setup-host.sh"`,
		`cp scripts/docker-host-helper.sh "$pkg_dir/docker-host-helper.sh"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release.sh should include platform-specific copy rule %q", want)
		}
	}
}

func TestGitHubReleaseWorkflowPackagesSameSupportFiles(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read GitHub release workflow: %v", err)
	}
	text := string(content)

	for _, want := range []string{
		"README.en.md",
		"scripts/SCRIPTS.md",
		"scripts/SCRIPTS.en.md",
		"scripts/start-mcc.ps1",
		"scripts/stop-mcc.ps1",
		"scripts/register-mcc-task.ps1",
		"scripts/docker-host-helper.sh",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("GitHub release workflow should package %s", want)
		}
	}
}

func TestSetupHostSHPrintsLinuxSSLCertFileGuidance(t *testing.T) {
	content, err := os.ReadFile("setup-host.sh")
	if err != nil {
		t.Fatalf("read setup-host.sh: %v", err)
	}
	text := string(content)

	for _, want := range []string{
		"SSL_CERT_FILE",
		"/etc/ssl/certs/ca-certificates.crt",
		"完整系统 CA bundle",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("setup-host.sh should print Linux SSL_CERT_FILE guidance containing %q", want)
		}
	}
}

func TestDockerHostHelperUsesPortableFingerprintParsing(t *testing.T) {
	content, err := os.ReadFile("docker-host-helper.sh")
	if err != nil {
		t.Fatalf("read docker-host-helper.sh: %v", err)
	}
	text := string(content)

	if strings.Contains(text, `\L`) {
		t.Fatal("docker-host-helper.sh must not rely on GNU sed \\L; helper may run under non-GNU /bin/sh environments")
	}
	if !strings.Contains(text, "tr 'A-F' 'a-f'") {
		t.Fatal("docker-host-helper.sh should lowercase marker fingerprints with portable tr")
	}
}
