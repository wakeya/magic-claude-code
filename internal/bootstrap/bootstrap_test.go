package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Mock adapters ---

type mockHosts struct {
	err        error
	hasMapping bool
}

func (m *mockHosts) EnsureHostMapping(domain, ip string) error { return m.err }
func (m *mockHosts) HasMapping(domain, ip string) bool         { return m.hasMapping }

type mockTrust struct {
	err error
}

func (m *mockTrust) InstallCA(certPath string) error { return m.err }

type mockEnv struct {
	err error
}

func (m *mockEnv) PersistRoot(rootDir string) error { return m.err }

// --- Resolver tests ---

func TestResolveMode_AllSuccess_SelectsTransparent(t *testing.T) {
	caps := Capabilities{CanEditHosts: true, CanTrustCA: true}
	hosts := StepResult{Attempted: true, Success: true}
	trust := StepResult{Attempted: true, Success: true}
	mode, rationale := resolveMode(ModeTransparent, caps, hosts, trust)
	if mode != ModeTransparent {
		t.Errorf("expected transparent, got %s (rationale: %s)", mode, rationale)
	}
	if rationale != "" {
		t.Errorf("expected empty rationale for success, got %q", rationale)
	}
}

func TestResolveMode_HostsFails_SelectsTunnel(t *testing.T) {
	caps := Capabilities{CanEditHosts: true, CanTrustCA: true}
	hosts := StepResult{Attempted: true, Success: false}
	trust := StepResult{Attempted: true, Success: true}
	mode, rationale := resolveMode(ModeTransparent, caps, hosts, trust)
	if mode != ModeTunnel {
		t.Errorf("expected tunnel, got %s (rationale: %s)", mode, rationale)
	}
	if rationale == "" {
		t.Error("expected non-empty rationale for fallback")
	}
}

func TestResolveMode_TrustFails_SelectsTunnel(t *testing.T) {
	caps := Capabilities{CanEditHosts: true, CanTrustCA: true}
	hosts := StepResult{Attempted: true, Success: true}
	trust := StepResult{Attempted: true, Success: false}
	mode, rationale := resolveMode(ModeTransparent, caps, hosts, trust)
	if mode != ModeTunnel {
		t.Errorf("expected tunnel, got %s (rationale: %s)", mode, rationale)
	}
	if rationale == "" {
		t.Error("expected non-empty rationale for fallback")
	}
}

func TestResolveMode_NoCapabilities_SelectsGateway(t *testing.T) {
	caps := Capabilities{CanEditHosts: false, CanTrustCA: false}
	hosts := StepResult{}
	trust := StepResult{}
	mode, rationale := resolveMode(ModeTransparent, caps, hosts, trust)
	if mode != ModeGateway {
		t.Errorf("expected gateway, got %s (rationale: %s)", mode, rationale)
	}
}

func TestResolveMode_HostsUnavailable_SelectsTunnel(t *testing.T) {
	caps := Capabilities{CanEditHosts: false, CanTrustCA: true}
	hosts := StepResult{Attempted: true, Success: false}
	trust := StepResult{Attempted: true, Success: true}
	mode, rationale := resolveMode(ModeTransparent, caps, hosts, trust)
	if mode != ModeTunnel {
		t.Fatalf("expected tunnel, got %s (rationale: %s)", mode, rationale)
	}
	if rationale == "" {
		t.Fatal("expected rationale for tunnel fallback")
	}
}

func TestResolveMode_DockerNoHelper_SelectsTunnel(t *testing.T) {
	caps := Capabilities{IsDocker: true, HasHostHelper: false}
	mode, rationale := resolveMode(ModeTransparent, caps, StepResult{}, StepResult{})
	if mode != ModeTunnel {
		t.Errorf("expected tunnel for docker without helper, got %s", mode)
	}
	if rationale == "" {
		t.Error("expected non-empty rationale for docker fallback")
	}
}

func TestResolveMode_ExplicitTunnel_StaysTunnel(t *testing.T) {
	mode, rationale := resolveMode(ModeTunnel, Capabilities{}, StepResult{}, StepResult{})
	if mode != ModeTunnel {
		t.Fatalf("expected tunnel, got %s", mode)
	}
	if rationale != "" {
		t.Fatalf("expected empty rationale, got %q", rationale)
	}
}

func TestResolveMode_ExplicitGateway_StaysGateway(t *testing.T) {
	mode, rationale := resolveMode(ModeGateway, Capabilities{}, StepResult{}, StepResult{})
	if mode != ModeGateway {
		t.Fatalf("expected gateway, got %s", mode)
	}
	if rationale != "" {
		t.Fatalf("expected empty rationale, got %q", rationale)
	}
}

func TestResolveModeLocalized_ChineseRationale(t *testing.T) {
	caps := Capabilities{CanEditHosts: true, CanTrustCA: true}
	hosts := StepResult{Attempted: true, Success: false, Err: &testError{"permission denied"}}
	trust := StepResult{Attempted: true, Success: true}
	_, rationale := resolveModeLocalized(ModeTransparent, caps, hosts, trust, "zh")
	if !contains(rationale, "hosts 修改失败") {
		t.Fatalf("expected Chinese rationale, got %q", rationale)
	}
}

// --- Executor tests ---

func TestExecutor_AllSuccess_TransparentMode(t *testing.T) {
	e := New("/tmp/test-mcc", "/tmp/test-mcc/ca.crt", "en",
		WithHostsAdapter(&mockHosts{}),
		WithTrustAdapter(&mockTrust{}),
		WithEnvAdapter(&mockEnv{}),
	)
	if e.dataDir != "/tmp/test-mcc" {
		t.Errorf("expected dataDir=/tmp/test-mcc, got %s", e.dataDir)
	}
	if e.caCertPath != "/tmp/test-mcc/ca.crt" {
		t.Errorf("expected caCertPath=/tmp/test-mcc/ca.crt, got %s", e.caCertPath)
	}
}

func TestExecutor_ExplicitTunnel_Mode(t *testing.T) {
	e := New("/tmp/test-mcc", "/tmp/test-mcc/ca.crt", "en",
		WithPreferredMode(ModeTunnel),
		WithHostsAdapter(&mockHosts{}),
		WithTrustAdapter(&mockTrust{}),
		WithEnvAdapter(&mockEnv{}),
	)
	result := e.Run()
	if result.SelectedMode != ModeTunnel {
		t.Fatalf("expected tunnel, got %s", result.SelectedMode)
	}
	if result.HostsResult.Attempted || result.TrustResult.Attempted {
		t.Fatalf("expected tunnel mode to skip transparent bootstrap steps: %+v %+v", result.HostsResult, result.TrustResult)
	}
}

func TestExecutor_HostsFails_FallsBackToTunnel(t *testing.T) {
	hostErr := &testError{"permission denied"}
	New("/tmp/test-mcc", "/tmp/test-mcc/ca.crt", "en",
		WithHostsAdapter(&mockHosts{err: hostErr}),
		WithTrustAdapter(&mockTrust{}),
		WithEnvAdapter(&mockEnv{}),
	)
	// Manually build result since detectCapabilities reads real OS
	caps := Capabilities{CanEditHosts: true, CanTrustCA: true, CanPersistEnv: true}
	result := Result{
		Caps:       caps,
		CACertPath: "/tmp/test-mcc/ca.crt",
		HostsResult: StepResult{
			Attempted: true,
			Success:   false,
			Err:       hostErr,
		},
		TrustResult: StepResult{Attempted: true, Success: true},
	}
	result.SelectedMode, result.Rationale = resolveMode(ModeTransparent, caps, result.HostsResult, result.TrustResult)
	if result.SelectedMode != ModeTunnel {
		t.Errorf("expected tunnel, got %s", result.SelectedMode)
	}
}

// --- Already-configured detection tests (non-privileged launches) ---

func TestExecutor_TryHosts_AlreadyMapped_SkipsWrite(t *testing.T) {
	e := New("/tmp/test-mcc", "/tmp/test-mcc/ca.crt", "en",
		WithHostsAdapter(&mockHosts{hasMapping: true, err: errors.New("should not write")}),
	)
	r := e.tryHosts()
	if !r.Success || r.Attempted {
		t.Errorf("expected Success=true Attempted=false when already mapped, got %+v", r)
	}
}

func TestExecutor_TryHosts_NotMapped_AttemptsWrite(t *testing.T) {
	e := New("/tmp/test-mcc", "/tmp/test-mcc/ca.crt", "en",
		WithHostsAdapter(&mockHosts{hasMapping: false}),
	)
	r := e.tryHosts()
	if !r.Success || !r.Attempted {
		t.Errorf("expected Success=true Attempted=true when not mapped, got %+v", r)
	}
}

func TestExecutor_TryTrustCA_MarkerMatchesFingerprint_SkipsInstall(t *testing.T) {
	dir := t.TempDir()
	// 写一份 CA 证书，并写入匹配其 fingerprint 的标记
	caPath := filepath.Join(dir, "ca.crt")
	caContent := []byte("-----BEGIN CERTIFICATE-----\nfake-cert-body\n-----END CERTIFICATE-----\n")
	if err := os.WriteFile(caPath, caContent, 0644); err != nil {
		t.Fatal(err)
	}
	fp, err := caFingerprint(caPath)
	if err != nil {
		t.Fatalf("caFingerprint() error = %v", err)
	}
	marker, _ := json.Marshal(caTrustMarker{Action: "ca-trust-installed", Fingerprint: fp})
	if err := os.WriteFile(filepath.Join(dir, caTrustMarkerName), marker, 0644); err != nil {
		t.Fatal(err)
	}
	e := New(dir, caPath, "en",
		WithTrustAdapter(&mockTrust{err: errors.New("should not install")}),
	)
	r := e.tryTrustCA()
	if !r.Success || r.Attempted {
		t.Errorf("expected Success=true Attempted=false when fingerprint matches, got %+v", r)
	}
}

func TestExecutor_TryTrustCA_MarkerStaleFingerprint_Reinstalls(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, []byte("current-cert"), 0644); err != nil {
		t.Fatal(err)
	}
	// 写一个 fingerprint 不匹配的标记（模拟证书重新生成后标记过期）
	stale, _ := json.Marshal(caTrustMarker{Action: "ca-trust-installed", Fingerprint: "stale-fingerprint"})
	if err := os.WriteFile(filepath.Join(dir, caTrustMarkerName), stale, 0644); err != nil {
		t.Fatal(err)
	}
	e := New(dir, caPath, "en",
		WithTrustAdapter(&mockTrust{}),
	)
	r := e.tryTrustCA()
	if !r.Success || !r.Attempted {
		t.Errorf("expected reinstall (Success=true Attempted=true) on stale marker, got %+v", r)
	}
	// 标记应被刷新为当前 fingerprint
	if !hasCATrustMarker(dir, caPath) {
		t.Errorf("expected marker to be refreshed with current fingerprint")
	}
}

func TestExecutor_TryTrustCA_NoMarker_InstallsAndWritesMarker(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, []byte("cert-content"), 0644); err != nil {
		t.Fatal(err)
	}
	e := New(dir, caPath, "en",
		WithTrustAdapter(&mockTrust{}),
	)
	r := e.tryTrustCA()
	if !r.Success || !r.Attempted {
		t.Errorf("expected Success=true Attempted=true after install, got %+v", r)
	}
	if !hasCATrustMarker(dir, caPath) {
		t.Errorf("expected marker %q to be written after successful install", caTrustMarkerName)
	}
}

func TestDockerHelperInvokedForHostsAndTrust(t *testing.T) {
	dir := t.TempDir()
	helperLog := filepath.Join(dir, "helper.log")
	helperScript := filepath.Join(dir, "helper.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HELPER_LOG\"\n"
	if err := os.WriteFile(helperScript, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("MCC_HOST_HELPER", helperScript)
	t.Setenv("HELPER_LOG", helperLog)

	oldDocker := isDockerEnvFn
	isDockerEnvFn = func() bool { return true }
	t.Cleanup(func() { isDockerEnvFn = oldDocker })

	if err := (&osHostsAdapter{}).EnsureHostMapping("api.anthropic.com", "127.0.0.1"); err != nil {
		t.Fatalf("EnsureHostMapping() error = %v", err)
	}
	got, err := os.ReadFile(helperLog)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if want := "hosts\nadd\napi.anthropic.com\n127.0.0.1\n"; string(got) != want {
		t.Fatalf("helper log = %q, want %q", string(got), want)
	}

	if err := (&osTrustAdapter{}).InstallCA("/tmp/ca.crt"); err != nil {
		t.Fatalf("InstallCA() error = %v", err)
	}
	got, err = os.ReadFile(helperLog)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if want := "trust\ninstall\n/tmp/ca.crt\n"; string(got) != want {
		t.Fatalf("helper log = %q, want %q", string(got), want)
	}
}

func TestDetectCapabilities_DockerWithHelper_NoEnvPersistence(t *testing.T) {
	dir := t.TempDir()
	helperScript := filepath.Join(dir, "helper.sh")
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(helperScript, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("MCC_HOST_HELPER", helperScript)

	oldDocker := isDockerEnvFn
	isDockerEnvFn = func() bool { return true }
	t.Cleanup(func() { isDockerEnvFn = oldDocker })

	caps := detectCapabilities()
	if !caps.IsDocker || !caps.HasHostHelper {
		t.Fatalf("expected Docker+helper, got caps=%+v", caps)
	}
	if !caps.CanEditHosts || !caps.CanTrustCA {
		t.Errorf("expected CanEditHosts and CanTrustCA for Docker+helper")
	}
	if caps.CanPersistEnv {
		t.Error("CanPersistEnv should be false in Docker: container profile is meaningless for host")
	}
}

// --- Instruction tests ---

func TestGenerateInstructions_Tunnel_ContainsCAPath(t *testing.T) {
	r := Result{
		SelectedMode: ModeTunnel,
		CACertPath:   "/custom/path/ca.crt",
		Caps:         Capabilities{CanEditHosts: true},
		HostsResult:  StepResult{Attempted: true, Success: false},
	}
	lines := generateInstructions(r, "en")
	found := false
	for _, l := range lines {
		if l != "" && contains(l, "/custom/path/ca.crt") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CA path in tunnel instructions, got: %v", lines)
	}
}

func TestGenerateInstructions_Gateway_ContainsANTHROPIC_BASE_URL(t *testing.T) {
	r := Result{
		SelectedMode: ModeGateway,
		CACertPath:   "/ca.crt",
		Caps:         Capabilities{},
	}
	lines := generateInstructions(r, "zh")
	found := false
	for _, l := range lines {
		if contains(l, "ANTHROPIC_BASE_URL") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ANTHROPIC_BASE_URL in gateway instructions, got: %v", lines)
	}
}

func TestGenerateInstructions_TransparentSuccess_Zh(t *testing.T) {
	r := Result{
		SelectedMode: ModeTransparent,
		HostsResult:  StepResult{Attempted: true, Success: true},
		TrustResult:  StepResult{Attempted: true, Success: true},
	}
	lines := generateInstructions(r, "zh")
	if len(lines) == 0 {
		t.Fatal("expected non-empty instructions")
	}
	if !contains(lines[0], "透明模式") {
		t.Errorf("expected Chinese text, got: %s", lines[0])
	}
}

func TestGenerateInstructions_TransparentSuccess_En(t *testing.T) {
	r := Result{
		SelectedMode: ModeTransparent,
		HostsResult:  StepResult{Attempted: true, Success: true},
		TrustResult:  StepResult{Attempted: true, Success: true},
	}
	lines := generateInstructions(r, "en")
	if len(lines) == 0 {
		t.Fatal("expected non-empty instructions")
	}
	if !contains(lines[0], "Transparent mode") {
		t.Errorf("expected English text, got: %s", lines[0])
	}
}

func TestGenerateInstructions_TransparentEnvFailure(t *testing.T) {
	r := Result{
		SelectedMode: ModeTransparent,
		ExecRootDir:  "/opt/mcc",
		HostsResult:  StepResult{Attempted: true, Success: true},
		TrustResult:  StepResult{Attempted: true, Success: true},
		EnvResult:    StepResult{Attempted: true, Success: false, Err: &testError{"permission denied"}},
	}
	lines := generateInstructions(r, "en")
	if len(lines) == 0 {
		t.Fatal("expected non-empty instructions")
	}
	if !contains(lines[0], "environment persistence failed") {
		t.Fatalf("expected env failure guidance, got: %v", lines)
	}
	foundRoot := false
	for _, line := range lines {
		if contains(line, "MCC_ROOT") && contains(line, "/opt/mcc") {
			foundRoot = true
			break
		}
	}
	if !foundRoot {
		t.Fatalf("expected MCC_ROOT guidance, got: %v", lines)
	}
}

func TestGenerateInstructions_TransparentEnvFailure_QuotesSpaces(t *testing.T) {
	r := Result{
		SelectedMode: ModeTransparent,
		ExecRootDir:  "/opt/My MCC Root",
		HostsResult:  StepResult{Attempted: true, Success: true},
		TrustResult:  StepResult{Attempted: true, Success: true},
		EnvResult:    StepResult{Attempted: true, Success: false, Err: &testError{"permission denied"}},
	}
	lines := generateInstructions(r, "en")
	found := false
	for _, line := range lines {
		if contains(line, "export MCC_ROOT='/opt/My MCC Root'") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected quoted MCC_ROOT export for spaced path, got: %v", lines)
	}
}

func TestGenerateInstructions_Tunnel_QuotesCAPath(t *testing.T) {
	r := Result{
		SelectedMode: ModeTunnel,
		CACertPath:   "/opt/My CA/cacert.pem",
	}
	lines := generateInstructions(r, "en")
	found := false
	for _, line := range lines {
		if contains(line, "export NODE_EXTRA_CA_CERTS='/opt/My CA/cacert.pem'") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected quoted CA path in tunnel instructions, got: %v", lines)
	}
}

func TestWindowsInstructionHelpers(t *testing.T) {
	if got := windowsSet("NODE_EXTRA_CA_CERTS", `C:\Program Files\mcc\ca.crt`); got != `  set "NODE_EXTRA_CA_CERTS=C:\Program Files\mcc\ca.crt"` {
		t.Fatalf("windowsSet() = %q", got)
	}
	if got := windowsQuote(`C:\Program Files\mcc`); got != `"C:\Program Files\mcc"` {
		t.Fatalf("windowsQuote() = %q", got)
	}
}

func TestRunHostHelperRejectsRelativePath(t *testing.T) {
	if err := runHostHelper("helper.sh", "hosts", "add", "api.anthropic.com", "127.0.0.1"); err == nil {
		t.Fatal("expected error for relative helper path")
	}
}

func TestValidateHostHelperPathRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "helper.sh")
	link := filepath.Join(dir, "helper-link.sh")

	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not available in test environment: %v", err)
	}

	err := validateHostHelperPath(link)
	if err == nil {
		t.Fatal("expected symlink helper path to be rejected")
	}
	if !strings.Contains(err.Error(), "symlink") || !strings.Contains(err.Error(), link) {
		t.Fatalf("expected symlink-specific error, got: %v", err)
	}
}

func TestRunHostHelperTruncatesOutputOnFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "helper.sh")
	payload := strings.Repeat("0123456789", 80)
	body := "#!/bin/sh\nprintf '%s\\n' '" + payload + "' >&2\nexit 7\n"

	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := runHostHelper(script, "hosts", "add", "api.anthropic.com", "127.0.0.1")
	if err == nil {
		t.Fatal("expected helper failure")
	}
	msg := err.Error()
	if strings.Contains(msg, payload) {
		t.Fatalf("expected helper output to be truncated, got full payload in error: %s", msg)
	}
	if !strings.Contains(msg, "truncated to 512 bytes") {
		t.Fatalf("expected truncation hint in error, got: %s", msg)
	}
}

func TestDetectCapabilities_IgnoresRelativeHostHelper(t *testing.T) {
	orig := isDockerEnvFn
	isDockerEnvFn = func() bool { return true }
	t.Cleanup(func() { isDockerEnvFn = orig })

	t.Setenv("MCC_HOST_HELPER", "helper.sh")

	caps := detectCapabilities()
	if caps.HasHostHelper {
		t.Fatal("expected relative helper path to be ignored")
	}
	if caps.CanEditHosts || caps.CanTrustCA || caps.CanPersistEnv {
		t.Fatalf("unexpected capabilities for relative helper: %+v", caps)
	}
}

// --- State suppression tests ---

func TestShouldSuppress_SameState_ReturnsTrue(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, ".bootstrap-state")
	r := Result{
		SelectedMode: ModeTunnel,
		CACertPath:   "/ca.crt",
		Caps:         Capabilities{IsDocker: true, HasHostHelper: false},
		HostsResult:  StepResult{Attempted: false, Success: false},
		TrustResult:  StepResult{Attempted: false, Success: false},
	}

	saveState(statePath, r)
	if !shouldSuppress(statePath, r) {
		t.Error("expected shouldSuppress=true for same state")
	}
}

func TestShouldSuppress_DifferentState_ReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, ".bootstrap-state")

	r1 := Result{
		SelectedMode: ModeTunnel,
		HostsResult:  StepResult{Attempted: true, Success: false, Err: &testError{"permission denied"}},
	}
	saveState(statePath, r1)

	r2 := Result{
		SelectedMode: ModeGateway,
		HostsResult:  StepResult{Attempted: true, Success: false, Err: &testError{"command not found"}},
	}
	if shouldSuppress(statePath, r2) {
		t.Error("expected shouldSuppress=false for different error details")
	}
}

func TestShouldSuppress_NoStateFile_ReturnsFalse(t *testing.T) {
	if shouldSuppress("/nonexistent/.bootstrap-state", Result{}) {
		t.Error("expected false when state file doesn't exist")
	}
}

func TestSaveState_UnwritableDir_DoesNotPanic(t *testing.T) {
	// Point statePath at a path whose parent is a regular file → MkdirAll/WriteFile will fail.
	// saveState should log the error but not panic.
	statePath := filepath.Join(t.TempDir(), "blocking-file", ".bootstrap-state")
	if err := os.WriteFile(filepath.Join(t.TempDir(), "blocking-file"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	saveState(statePath, Result{SelectedMode: ModeTransparent})
	// If we get here without panic, the test passes.
}

func TestShouldSuppress_DifferentPreferredMode_ReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, ".bootstrap-state")

	r1 := Result{
		SelectedMode:  ModeTunnel,
		PreferredMode: ModeTransparent,
		HostsResult:   StepResult{Attempted: true, Success: false, Err: &testError{"permission denied"}},
	}
	saveState(statePath, r1)

	r2 := Result{
		SelectedMode:  ModeTunnel,
		PreferredMode: ModeTunnel,
		HostsResult:   StepResult{Attempted: true, Success: false, Err: &testError{"permission denied"}},
	}
	if shouldSuppress(statePath, r2) {
		t.Error("expected shouldSuppress=false when PreferredMode changed but SelectedMode is the same")
	}
}

// --- Docker boundary ---

func TestDetectCapabilities_DockerEnv(t *testing.T) {
	t.Setenv("MCC_TEST_FORCE_DOCKER", "1")
	// We can't easily mock /.dockerenv, but we can verify the function doesn't panic
	// and returns a valid Capabilities struct
	caps := detectCapabilities()
	_ = caps
}

// --- Helpers ---

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- Hosts replacement tests ---

func TestProcessHostsContent_ReplacesOldMapping(t *testing.T) {
	content := "127.0.0.1 localhost\n10.0.0.1 api.anthropic.com\n"
	out, changed := processHostsContent(content, "api.anthropic.com", "127.0.0.1")
	if !changed {
		t.Error("expected changed=true when old mapping has wrong IP")
	}
	if contains(out, "10.0.0.1") {
		t.Errorf("old IP should be removed from mapping, got: %s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	found := false
	for _, l := range lines {
		if l == "127.0.0.1 api.anthropic.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected correct mapping 127.0.0.1 api.anthropic.com, got: %s", out)
	}
}

func TestProcessHostsContent_NoChangeWhenCorrectMappingExists(t *testing.T) {
	content := "127.0.0.1 localhost\n127.0.0.1 api.anthropic.com\n"
	out, changed := processHostsContent(content, "api.anthropic.com", "127.0.0.1")
	if changed {
		t.Error("expected changed=false when correct mapping already exists")
	}
	if !contains(out, "127.0.0.1 api.anthropic.com") {
		t.Errorf("correct mapping should be preserved, got: %s", out)
	}
}

func TestProcessHostsContent_PreservesOtherHostnames(t *testing.T) {
	content := "10.0.0.1 api.anthropic.com example.com\n"
	out, _ := processHostsContent(content, "api.anthropic.com", "127.0.0.1")
	if !contains(out, "10.0.0.1 example.com") {
		t.Errorf("other hostnames should be preserved, got: %s", out)
	}
}

func TestProcessHostsContent_AddsMappingWhenMissing(t *testing.T) {
	content := "127.0.0.1 localhost\n"
	out, changed := processHostsContent(content, "api.anthropic.com", "127.0.0.1")
	if !changed {
		t.Error("expected changed=true when mapping is missing")
	}
	if !contains(out, "127.0.0.1 api.anthropic.com") {
		t.Errorf("expected new mapping to be added, got: %s", out)
	}
}

func TestProcessHostsContent_EmptyContentDoesNotAddLeadingBlankLine(t *testing.T) {
	out, changed := processHostsContent("", "api.anthropic.com", "127.0.0.1")
	if !changed {
		t.Error("expected changed=true for empty hosts content")
	}
	if strings.HasPrefix(out, "\n") {
		t.Errorf("expected no leading blank line, got: %q", out)
	}
	if !contains(out, "127.0.0.1 api.anthropic.com") {
		t.Errorf("expected mapping to be added, got: %q", out)
	}
}

func TestProcessHostsContent_PreservesLeadingCommentHeader(t *testing.T) {
	content := "# generated by admin\n127.0.0.1 localhost\n"
	out, _ := processHostsContent(content, "api.anthropic.com", "127.0.0.1")
	if !strings.HasPrefix(out, "# generated by admin\n") {
		t.Errorf("expected leading comment header to be preserved, got: %q", out)
	}
}

func TestProcessHostsContent_RemovesDuplicateDomainLines(t *testing.T) {
	content := "10.0.0.1 api.anthropic.com\n192.168.1.1 api.anthropic.com\n127.0.0.1 localhost\n"
	out, _ := processHostsContent(content, "api.anthropic.com", "127.0.0.1")
	count := strings.Count(out, "api.anthropic.com")
	if count != 1 {
		t.Errorf("expected exactly 1 occurrence of api.anthropic.com, got %d in: %s", count, out)
	}
}

func TestProcessHostsContent_CorrectAndOldMapping_ChangedTrue(t *testing.T) {
	content := "127.0.0.1 localhost\n127.0.0.1 api.anthropic.com\n10.0.0.1 api.anthropic.com\n"
	out, changed := processHostsContent(content, "api.anthropic.com", "127.0.0.1")
	if !changed {
		t.Error("expected changed=true when old wrong mapping coexists with correct one")
	}
	if contains(out, "10.0.0.1") {
		t.Errorf("old wrong IP should be removed, got: %s", out)
	}
	if !contains(out, "127.0.0.1 api.anthropic.com") {
		t.Errorf("correct mapping should be preserved, got: %s", out)
	}
}

// --- Shell profile selection tests ---

func TestResolveShellProfile(t *testing.T) {
	tests := []struct {
		name     string
		shell    string
		home     string
		expected string
	}{
		{name: "zsh", shell: "/bin/zsh", home: "/home/user", expected: "/home/user/.zshrc"},
		{name: "bash", shell: "/bin/bash", home: "/home/user", expected: "/home/user/.bashrc"},
		{name: "fish", shell: "/usr/bin/fish", home: "/home/user", expected: "/home/user/.config/fish/config.fish"},
		{name: "unknown shell falls back to .profile", shell: "/usr/bin/nushell", home: "/home/user", expected: "/home/user/.profile"},
		{name: "empty shell falls back to .profile", shell: "", home: "/root", expected: "/root/.profile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveShellProfile(tt.shell, tt.home)
			if got != tt.expected {
				t.Errorf("resolveShellProfile(%q, %q) = %q, want %q", tt.shell, tt.home, got, tt.expected)
			}
		})
	}
}

func TestShellExportEntry(t *testing.T) {
	tests := []struct {
		name     string
		shell    string
		wantFish bool
		wantBash bool
	}{
		{name: "fish uses set -x", shell: "/usr/bin/fish", wantFish: true},
		{name: "bash uses export", shell: "/bin/bash", wantBash: true},
		{name: "zsh uses export", shell: "/bin/zsh", wantBash: true},
		{name: "unknown uses export", shell: "", wantBash: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := shellExportEntry(tt.shell, "MCC_ROOT", "/opt/mcc")
			if tt.wantFish && !contains(entry, "set -x") {
				t.Errorf("expected fish set -x syntax, got: %s", entry)
			}
			if tt.wantBash && !contains(entry, "export") {
				t.Errorf("expected export syntax, got: %s", entry)
			}
			if !contains(entry, "MCC_ROOT") || !contains(entry, "/opt/mcc") {
				t.Errorf("entry should contain key and value, got: %s", entry)
			}
		})
	}
}

// --- Gateway instruction platform tests ---

func TestGenerateInstructions_Gateway_Zh_ContainsANTHROPIC_BASE_URL(t *testing.T) {
	r := Result{SelectedMode: ModeGateway, CACertPath: "/ca.crt"}
	lines := generateInstructions(r, "zh")
	found := false
	for _, l := range lines {
		if contains(l, "ANTHROPIC_BASE_URL") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ANTHROPIC_BASE_URL in gateway instructions, got: %v", lines)
	}
}

func TestGenerateInstructions_Gateway_IPv6_URLFormat(t *testing.T) {
	// IPv6 地址必须用 [::1]:17487 格式（RFC 2732），而非 ::1:17487。
	r := Result{
		SelectedMode:      ModeGateway,
		GatewayListenAddr: "::1",
		GatewayListenPort: 17487,
		CACertPath:        "/ca.crt",
		Caps:              Capabilities{},
	}
	for _, locale := range []string{"zh", "en"} {
		lines := generateInstructions(r, locale)
		want := "http://[::1]:17487"
		found := false
		for _, l := range lines {
			if contains(l, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("[%s] expected %q in gateway instructions, got: %v", locale, want, lines)
		}
	}
}

// Ensure temp files are cleaned up
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// --- Shell profile candidate list tests ---

func TestResolveShellProfiles(t *testing.T) {
	tests := []struct {
		name     string
		shell    string
		home     string
		expected []string
	}{
		{name: "zsh", shell: "/bin/zsh", home: "/home/u", expected: []string{"/home/u/.zshrc"}},
		{name: "bash", shell: "/bin/bash", home: "/home/u", expected: []string{"/home/u/.bashrc"}},
		{name: "fish", shell: "/usr/bin/fish", home: "/home/u", expected: []string{"/home/u/.config/fish/config.fish"}},
		{name: "unknown tries profile then bashrc", shell: "/usr/bin/nushell", home: "/home/u",
			expected: []string{"/home/u/.profile", "/home/u/.bashrc"}},
		{name: "empty shell tries profile then bashrc", shell: "", home: "/root",
			expected: []string{"/root/.profile", "/root/.bashrc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveShellProfiles(tt.shell, tt.home)
			if len(got) != len(tt.expected) {
				t.Fatalf("resolveShellProfiles(%q, %q) = %v, want %v", tt.shell, tt.home, got, tt.expected)
			}
			for i, want := range tt.expected {
				if got[i] != want {
					t.Errorf("resolveShellProfiles[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

// TestPersistRoot_UnknownShell_FallsBackToBashrc verifies that when the shell
// is unknown and ~/.profile cannot be opened, PersistRoot writes to ~/.bashrc.
func TestPersistRoot_UnknownShell_FallsBackToBashrc(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "")

	// Block ~/.profile by creating a directory with that name.
	if err := os.Mkdir(filepath.Join(home, ".profile"), 0755); err != nil {
		t.Fatalf("mkdir .profile: %v", err)
	}

	a := &osEnvAdapter{}
	if err := a.PersistRoot("/test/mcc"); err != nil {
		t.Fatalf("PersistRoot should fall back to .bashrc, got: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatalf("expected .bashrc to be written: %v", err)
	}
	if !contains(string(content), "MCC_ROOT") || !contains(string(content), "/test/mcc") {
		t.Errorf("expected .bashrc to contain MCC_ROOT=/test/mcc, got: %s", content)
	}
}

// TestPersistRoot_DeduplicatesExistingEntry verifies that calling PersistRoot
// twice with the same root does not append duplicate export lines.
func TestPersistRoot_DeduplicatesExistingEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")

	a := &osEnvAdapter{}
	if err := a.PersistRoot("/test/mcc"); err != nil {
		t.Fatalf("first PersistRoot: %v", err)
	}
	if err := a.PersistRoot("/test/mcc"); err != nil {
		t.Fatalf("second PersistRoot: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(content), "export MCC_ROOT"); got != 1 {
		t.Errorf("expected exactly 1 export MCC_ROOT entry, got %d in: %s", got, content)
	}
}

// TestPersistRoot_CommentNotTreatedAsDuplicate verifies that a commented-out
// line that contains the same export text does NOT suppress the real write.
func TestPersistRoot_CommentNotTreatedAsDuplicate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")

	bashrc := filepath.Join(home, ".bashrc")
	commented := "# export MCC_ROOT=\"/test/mcc\"\n"
	if err := os.WriteFile(bashrc, []byte(commented), 0644); err != nil {
		t.Fatal(err)
	}

	a := &osEnvAdapter{}
	if err := a.PersistRoot("/test/mcc"); err != nil {
		t.Fatalf("PersistRoot: %v", err)
	}

	content, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}
	want := "export MCC_ROOT='/test/mcc'"
	activeFound := false
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == want {
			activeFound = true
			break
		}
	}
	if !activeFound {
		t.Errorf("expected active export line after commented-out line, got: %s", content)
	}
}

// TestPersistRoot_Fish_CreatesMissingParentDir verifies that PersistRoot
// succeeds even when ~/.config/fish/ does not exist yet, since OpenFile does
// not create parent directories on its own.
func TestPersistRoot_Fish_CreatesMissingParentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/usr/bin/fish")

	fishDir := filepath.Join(home, ".config", "fish")
	if _, err := os.Stat(fishDir); !os.IsNotExist(err) {
		t.Fatalf("expected %s to not exist initially, got err=%v", fishDir, err)
	}

	a := &osEnvAdapter{}
	if err := a.PersistRoot("/test/mcc"); err != nil {
		t.Fatalf("PersistRoot should create parent dirs: %v", err)
	}

	target := filepath.Join(fishDir, "config.fish")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected config.fish to be written: %v", err)
	}
	if !contains(string(content), "MCC_ROOT") || !contains(string(content), "/test/mcc") {
		t.Errorf("expected MCC_ROOT entry, got: %s", content)
	}
	if !contains(string(content), "set -x") {
		t.Errorf("expected fish set -x syntax, got: %s", content)
	}
}

// --- writeCloser fake for writeProfileEntry tests ---

type fakeWriteCloser struct {
	writeErr error
	closeErr error
	closed   bool
}

func (f *fakeWriteCloser) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}

func (f *fakeWriteCloser) Close() error {
	f.closed = true
	return f.closeErr
}

// TestWriteProfileEntry_CloseErrorPropagates verifies that a Close failure is
// not silently dropped — it must surface as a non-nil error from writeProfileEntry.
func TestWriteProfileEntry_CloseErrorPropagates(t *testing.T) {
	open := func(string) (writeCloser, error) {
		return &fakeWriteCloser{closeErr: fmt.Errorf("disk full")}, nil
	}
	err := writeProfileEntry(open, "/fake/profile", "export MCC_ROOT=/x")
	if err == nil {
		t.Fatal("expected Close error to propagate, got nil")
	}
	if !contains(err.Error(), "disk full") || !contains(err.Error(), "close") {
		t.Errorf("expected error to wrap close failure, got: %v", err)
	}
}

// TestWriteProfileEntry_WriteErrorStillCloses verifies that on Write failure
// the file is still closed and the Write error is returned.
func TestWriteProfileEntry_WriteErrorStillCloses(t *testing.T) {
	fake := &fakeWriteCloser{writeErr: fmt.Errorf("write failed")}
	open := func(string) (writeCloser, error) { return fake, nil }

	err := writeProfileEntry(open, "/fake/profile", "export MCC_ROOT=/x")
	if err == nil {
		t.Fatal("expected Write error to propagate")
	}
	if !fake.closed {
		t.Error("Close should still be called after Write failure")
	}
	if !contains(err.Error(), "write") {
		t.Errorf("expected write error, got: %v", err)
	}
}

// --- profileHasExactEntry tests ---

func TestProfileHasExactEntry(t *testing.T) {
	entry := "\nexport MCC_ROOT=\"/opt/mcc\"\n"
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "exact line matches", content: "# header\nexport MCC_ROOT=\"/opt/mcc\"\n", want: true},
		{name: "commented line does not match", content: "# export MCC_ROOT=\"/opt/mcc\"\n", want: false},
		{name: "substring inside a sentence does not match",
			content: "# note: export MCC_ROOT=\"/opt/mcc\" is documented elsewhere\n", want: false},
		{name: "trailing whitespace tolerated", content: "export MCC_ROOT=\"/opt/mcc\"   \n", want: true},
		{name: "leading whitespace tolerated", content: "  export MCC_ROOT=\"/opt/mcc\"\n", want: true},
		{name: "different value not matched", content: "export MCC_ROOT=\"/other\"\n", want: false},
		{name: "empty content", content: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := profileHasExactEntry(tt.content, entry); got != tt.want {
				t.Errorf("profileHasExactEntry = %v, want %v (content=%q)", got, tt.want, tt.content)
			}
		})
	}
}

// TestProfileHasEquivalentEntry covers semantic-equivalence deduplication
// across the supported syntactic variants, for both POSIX shells and fish.
func TestProfileHasEquivalentEntry(t *testing.T) {
	const key, value = "MCC_ROOT", "/opt/mcc"

	posixCases := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "unquoted export matches", content: "export MCC_ROOT=/opt/mcc\n", want: true},
		{name: "double-quoted export matches", content: "export MCC_ROOT=\"/opt/mcc\"\n", want: true},
		{name: "single-quoted export matches", content: "export MCC_ROOT='/opt/mcc'\n", want: true},
		{name: "unquoted assignment matches", content: "MCC_ROOT=/opt/mcc\n", want: true},
		{name: "double-quoted assignment matches", content: "MCC_ROOT=\"/opt/mcc\"\n", want: true},
		{name: "commented export does not match", content: "# export MCC_ROOT=/opt/mcc\n", want: false},
		{name: "sentence containing tokens does not match",
			content: "# see export MCC_ROOT=/opt/mcc for details\n", want: false},
		{name: "different value does not match", content: "export MCC_ROOT=/other\n", want: false},
		{name: "different key does not match", content: "export OTHER=/opt/mcc\n", want: false},
		{name: "blank and comment lines ignored",
			content: "\n# hello\nexport MCC_ROOT=/opt/mcc\n", want: true},
	}
	for _, tt := range posixCases {
		t.Run("bash/"+tt.name, func(t *testing.T) {
			if got := profileHasEquivalentEntry("/bin/bash", tt.content, key, value); got != tt.want {
				t.Errorf("posix profileHasEquivalentEntry = %v, want %v (content=%q)", got, tt.want, tt.content)
			}
		})
		t.Run("zsh/"+tt.name, func(t *testing.T) {
			if got := profileHasEquivalentEntry("/bin/zsh", tt.content, key, value); got != tt.want {
				t.Errorf("zsh profileHasEquivalentEntry = %v, want %v (content=%q)", got, tt.want, tt.content)
			}
		})
		t.Run("unknown/"+tt.name, func(t *testing.T) {
			if got := profileHasEquivalentEntry("", tt.content, key, value); got != tt.want {
				t.Errorf("unknown profileHasEquivalentEntry = %v, want %v (content=%q)", got, tt.want, tt.content)
			}
		})
	}

	fishCases := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "set -x unquoted matches", content: "set -x MCC_ROOT /opt/mcc\n", want: true},
		{name: "set -x quoted matches", content: "set -x MCC_ROOT \"/opt/mcc\"\n", want: true},
		{name: "set -gx matches", content: "set -gx MCC_ROOT /opt/mcc\n", want: true},
		{name: "set --export matches", content: "set --export MCC_ROOT /opt/mcc\n", want: true},
		{name: "set -l local does not match", content: "set -l MCC_ROOT /opt/mcc\n", want: false},
		{name: "set -e erase does not match", content: "set -e MCC_ROOT /opt/mcc\n", want: false},
		{name: "set -u unexport does not match", content: "set -u MCC_ROOT /opt/mcc\n", want: false},
		{name: "set without export flag does not match", content: "set MCC_ROOT /opt/mcc\n", want: false},
		{name: "commented set does not match", content: "# set -x MCC_ROOT /opt/mcc\n", want: false},
		{name: "different value does not match", content: "set -x MCC_ROOT /other\n", want: false},
		{name: "different key does not match", content: "set -x OTHER /opt/mcc\n", want: false},
		{name: "posix export not matched for fish", content: "export MCC_ROOT=/opt/mcc\n", want: false},
	}
	for _, tt := range fishCases {
		t.Run("fish/"+tt.name, func(t *testing.T) {
			if got := profileHasEquivalentEntry("/usr/bin/fish", tt.content, key, value); got != tt.want {
				t.Errorf("fish profileHasEquivalentEntry = %v, want %v (content=%q)", got, tt.want, tt.content)
			}
		})
	}
}

// TestFishLineMatches_ValueWithSpaces verifies that fish export lines carrying
// whitespace-containing values are recognized as duplicates only when the
// value span is properly quoted — never when the whitespace comes from fish
// list syntax (which would otherwise falsely match a single-value target).
func TestFishLineMatches_ValueWithSpaces(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		key   string
		value string
		want  bool
	}{
		{name: "double-quoted empty value matches",
			line: `set -x MCC_ROOT ""`, key: "MCC_ROOT", value: "", want: true},
		{name: "single-quoted empty value matches",
			line: `set -x MCC_ROOT ''`, key: "MCC_ROOT", value: "", want: true},
		{name: "double-quoted value with space matches",
			line: `set -x MCC_ROOT "/opt/mcc path"`, key: "MCC_ROOT", value: "/opt/mcc path", want: true},
		{name: "single-quoted value with space matches",
			line: `set -x MCC_ROOT '/opt/mcc path'`, key: "MCC_ROOT", value: "/opt/mcc path", want: true},
		{name: "double-quoted value with space via -gx matches",
			line: `set -gx MCC_ROOT "/opt/mcc path"`, key: "MCC_ROOT", value: "/opt/mcc path", want: true},
		{name: "double-quoted value with space via --export matches",
			line: `set --export MCC_ROOT "/opt/mcc path"`, key: "MCC_ROOT", value: "/opt/mcc path", want: true},
		{name: "unquoted list does NOT match single-value target",
			line: `set -x MCC_ROOT /opt/mcc path`, key: "MCC_ROOT", value: "/opt/mcc path", want: false},
		{name: "unquoted list does NOT match first-element target",
			line: `set -x MCC_ROOT /opt/mcc path`, key: "MCC_ROOT", value: "/opt/mcc", want: false},
		{name: "double-quoted single value still matches",
			line: `set -x MCC_ROOT "/opt/mcc"`, key: "MCC_ROOT", value: "/opt/mcc", want: true},
		{name: "unquoted single value still matches",
			line: `set -x MCC_ROOT /opt/mcc`, key: "MCC_ROOT", value: "/opt/mcc", want: true},
		{name: "mixed quoted and unquoted tokens do not match",
			line: `set -x MCC_ROOT "a" b`, key: "MCC_ROOT", value: "a b", want: false},
		{name: "different value inside quotes does not match",
			line: `set -x MCC_ROOT "/other path"`, key: "MCC_ROOT", value: "/opt/mcc path", want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := fishLineMatches(tt.line, tt.key, tt.value); got != tt.want {
				t.Errorf("fishLineMatches(%q, %q, %q) = %v, want %v",
					tt.line, tt.key, tt.value, got, tt.want)
			}
		})
	}
}

// TestFishLineMatches_InlineComment verifies that a trailing fish inline
// comment (`# ...`) is safely ignored when deciding whether an export line is
// a duplicate. The comment must NOT be stripped when it appears inside quotes
// or is glued directly to a token (`token#x` is not a comment in fish).
func TestFishLineMatches_InlineComment(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		key   string
		value string
		want  bool
	}{
		{name: "single-value with trailing comment matches",
			line: "set -x MCC_ROOT /opt/mcc # user comment", key: "MCC_ROOT", value: "/opt/mcc", want: true},
		{name: "double-quoted value with trailing comment matches",
			line: `set -x MCC_ROOT "/opt/mcc path" # comment`, key: "MCC_ROOT", value: "/opt/mcc path", want: true},
		{name: "single-quoted value with trailing comment matches",
			line: `set -x MCC_ROOT '/opt/mcc path' # comment`, key: "MCC_ROOT", value: "/opt/mcc path", want: true},
		{name: "unquoted list with comment still does NOT match single-value target",
			line: "set -x MCC_ROOT /opt/mcc path # comment", key: "MCC_ROOT", value: "/opt/mcc path", want: false},
		{name: "unquoted list with comment does NOT match first-element target",
			line: "set -x MCC_ROOT /opt/mcc path # comment", key: "MCC_ROOT", value: "/opt/mcc", want: false},
		{name: "hash inside double quotes is not a comment",
			line: `set -x MCC_ROOT "/path#hash"`, key: "MCC_ROOT", value: "/path#hash", want: true},
		{name: "hash glued to token is not a comment",
			line: "set -x MCC_ROOT /opt/mcc#notcomment", key: "MCC_ROOT", value: "/opt/mcc#notcomment", want: true},
		{name: "comment-only value does not falsely match",
			line: "set -x MCC_ROOT /opt/mcc # mcc root path", key: "MCC_ROOT", value: "/opt/mcc # mcc root path", want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := fishLineMatches(tt.line, tt.key, tt.value); got != tt.want {
				t.Errorf("fishLineMatches(%q, %q, %q) = %v, want %v",
					tt.line, tt.key, tt.value, got, tt.want)
			}
		})
	}
}

// TestFishLineMatches_EscapeHandling verifies that backslash escapes do not
// fool the comment stripper: an escaped `#` is not a comment start, and an
// escaped `\"` inside double quotes does not close the quoted span.
func TestFishLineMatches_EscapeHandling(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		key   string
		value string
		want  bool
	}{
		{name: "unquoted escaped hash is not a comment",
			line: `set -x MCC_ROOT /opt/mcc\#v1`, key: "MCC_ROOT", value: `/opt/mcc#v1`, want: true},
		{name: "unquoted escaped space is preserved as a literal space",
			line: `set -x MCC_ROOT /opt/mcc\ path`, key: "MCC_ROOT", value: "/opt/mcc path", want: true},
		{name: "escaped whitespace before hash keeps hash inside the token",
			line: `set -x MCC_ROOT /opt/mcc\ #notcomment`, key: "MCC_ROOT", value: "/opt/mcc #notcomment", want: true},
		{name: "escaped hash followed by real comment is stripped correctly",
			line: `set -x MCC_ROOT /opt/mcc\#v1 # real comment`, key: "MCC_ROOT", value: `/opt/mcc#v1`, want: true},
		{name: "escaped double quote inside double-quoted span does not close the span",
			line: `set -x MCC_ROOT "/path with \"quote\" # value"`, key: "MCC_ROOT", value: `/path with "quote" # value`, want: true},
		{name: "escaped dollar inside double quotes is unescaped",
			line: `set -x MCC_ROOT "/path/\$name"`, key: "MCC_ROOT", value: "/path/$name", want: true},
		{name: "backslash-hash inside double quotes is preserved, not a comment",
			line: `set -x MCC_ROOT "/path\#hash"`, key: "MCC_ROOT", value: `/path\#hash`, want: true},
		{name: "escaped backslash inside double quotes does not break span",
			line: `set -x MCC_ROOT "/path\\backslash"`, key: "MCC_ROOT", value: `/path\backslash`, want: true},
		{name: "single-quoted backslash-hash is literal, not a comment",
			line: `set -x MCC_ROOT '/path\#hash'`, key: "MCC_ROOT", value: `/path\#hash`, want: true},
		{name: "single-quoted escaped quote does not close the span",
			line: `set -x MCC_ROOT 'can\'t'`, key: "MCC_ROOT", value: "can't", want: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := fishLineMatches(tt.line, tt.key, tt.value); got != tt.want {
				t.Errorf("fishLineMatches(%q, %q, %q) = %v, want %v",
					tt.line, tt.key, tt.value, got, tt.want)
			}
		})
	}
}

// TestPersistRoot_DedupRecognizesVariantQuoting verifies the end-to-end
// behavior: when the user has hand-written `export MCC_ROOT=/opt/mcc`
// (unquoted) in .bashrc, PersistRoot must NOT append its own quoted form.
func TestPersistRoot_DedupRecognizesVariantQuoting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")

	bashrc := filepath.Join(home, ".bashrc")
	existing := "# user config\nexport MCC_ROOT=/opt/mcc\n"
	if err := os.WriteFile(bashrc, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	a := &osEnvAdapter{}
	if err := a.PersistRoot("/opt/mcc"); err != nil {
		t.Fatalf("PersistRoot: %v", err)
	}

	content, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}
	// The hand-written line should remain, and no new line should have been
	// appended by PersistRoot.
	if !contains(string(content), "export MCC_ROOT=/opt/mcc") {
		t.Errorf("expected the original unquoted line preserved, got: %s", content)
	}
	if got := strings.Count(string(content), "MCC_ROOT"); got != 1 {
		t.Errorf("expected exactly 1 MCC_ROOT occurrence (no duplicate), got %d in: %s", got, content)
	}
}

// TestPersistRoot_DedupRecognizesFishVariant verifies the same end-to-end
// behavior for fish: an existing `set -x MCC_ROOT /opt/mcc` line suppresses
// the append.
func TestPersistRoot_DedupRecognizesFishVariant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/usr/bin/fish")

	fishDir := filepath.Join(home, ".config", "fish")
	if err := os.MkdirAll(fishDir, 0755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(fishDir, "config.fish")
	existing := "set -x MCC_ROOT /opt/mcc\n"
	if err := os.WriteFile(config, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	a := &osEnvAdapter{}
	if err := a.PersistRoot("/opt/mcc"); err != nil {
		t.Fatalf("PersistRoot: %v", err)
	}

	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(content), "MCC_ROOT"); got != 1 {
		t.Errorf("expected exactly 1 MCC_ROOT occurrence, got %d in: %s", got, content)
	}
}

func TestIsTransparentReady(t *testing.T) {
	tests := []struct {
		name    string
		result  Result
		ready   bool
	}{
		{
			name: "all success",
			result: Result{
				SelectedMode: ModeTransparent,
				HostsResult:  StepResult{Success: true},
				TrustResult:  StepResult{Success: true},
				EnvResult:    StepResult{Success: true},
			},
			ready: true,
		},
		{
			name: "hosts+CA success but env failed",
			result: Result{
				SelectedMode: ModeTransparent,
				HostsResult:  StepResult{Success: true},
				TrustResult:  StepResult{Success: true},
				EnvResult:    StepResult{Attempted: true, Success: false},
			},
			ready: false,
		},
		{
			name: "hosts failed",
			result: Result{
				SelectedMode: ModeTunnel,
				HostsResult:  StepResult{Attempted: true, Success: false},
			},
			ready: false,
		},
		{
			name: "env not attempted (zero value)",
			result: Result{
				SelectedMode: ModeTransparent,
				HostsResult:  StepResult{Success: true},
				TrustResult:  StepResult{Success: true},
			},
			ready: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTransparentReady(tt.result); got != tt.ready {
				t.Errorf("IsTransparentReady() = %v, want %v", got, tt.ready)
			}
		})
	}
}

func TestExecWithTimeout_QuickCommand_ReturnsSuccessfully(t *testing.T) {
	out, err := execWithTimeout("true")
	if err != nil {
		t.Fatalf("execWithTimeout(true) unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty output, got %q", string(out))
	}
}
