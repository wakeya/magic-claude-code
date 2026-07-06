package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	err               error
	nodeCAErr         error  // PersistNodeCACert 错误，nil 时 fallback 到 err
	caCertArg         string // 记录 PersistNodeCACert 收到的参数
	nodeCAValue       string
	nodeCAValueSet    bool
	nodeCALookupErr   error
	nodeCALookupCalls int
}

func (m *mockEnv) PersistRoot(rootDir string) error { return m.err }
func (m *mockEnv) LookupNodeCACert() (string, bool, error) {
	m.nodeCALookupCalls++
	return m.nodeCAValue, m.nodeCAValueSet, m.nodeCALookupErr
}
func (m *mockEnv) PersistNodeCACert(caCertPath string) error {
	m.caCertArg = caCertPath
	if m.nodeCAErr != nil {
		return m.nodeCAErr
	}
	return m.err
}

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
		{name: "fish uses set -gx", shell: "/usr/bin/fish", wantFish: true},
		{name: "bash uses export", shell: "/bin/bash", wantBash: true},
		{name: "zsh uses export", shell: "/bin/zsh", wantBash: true},
		{name: "unknown uses export", shell: "", wantBash: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := shellExportEntry(tt.shell, "MCC_ROOT", "/opt/mcc")
			if tt.wantFish && !contains(entry, "set -gx") {
				t.Errorf("expected fish set -gx syntax, got: %s", entry)
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
	if !contains(string(content), "set -gx") {
		t.Errorf("expected fish set -gx syntax, got: %s", content)
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
		name   string
		result Result
		ready  bool
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

// --- tryPersistNodeCA tests ---

func TestTryPersistNodeCA_EmptyCertPath_Skips(t *testing.T) {
	e := New("/tmp/test-mcc", "", "en",
		WithEnvAdapter(&mockEnv{}),
	)
	r := e.tryPersistNodeCA()
	if r.Attempted {
		t.Errorf("expected Attempted=false for empty caCertPath, got %+v", r)
	}
}

func TestTryPersistNodeCA_CertNotExists_ReportsError(t *testing.T) {
	e := New(t.TempDir(), "/nonexistent/ca.crt", "en",
		WithEnvAdapter(&mockEnv{}),
	)
	r := e.tryPersistNodeCA()
	if !r.Attempted || r.Success {
		t.Errorf("expected Attempted=true Success=false when cert file missing, got %+v", r)
	}
	if r.Err == nil {
		t.Error("expected non-nil Err indicating stat failure")
	}
}

func TestTryPersistNodeCA_RelativePath_UsesAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	absCA := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relCA, err := filepath.Rel(cwd, absCA)
	if err != nil {
		t.Fatal(err)
	}
	setPrivileged(t, false)

	env := &mockEnv{}
	r := New(dir, relCA, "en", WithEnvAdapter(env)).tryPersistNodeCA()
	if !r.Success {
		t.Fatalf("expected success, got %+v", r)
	}
	if env.caCertArg != absCA {
		t.Fatalf("PersistNodeCACert path = %q, want absolute %q", env.caCertArg, absCA)
	}
	if !hasNodeCAMarker(dir, absCA) {
		t.Fatal("marker must record and match the absolute CA path")
	}
}

func TestTryPersistNodeCA_CustomPersistedValue_IsPreserved(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")
	setPrivileged(t, false)
	env := &mockEnv{nodeCAValue: filepath.Join(dir, "corporate-ca.pem"), nodeCAValueSet: true}

	r := New(dir, caPath, "en", WithEnvAdapter(env)).tryPersistNodeCA()
	if !errors.Is(r.Err, ErrUserCustomValue) || env.caCertArg != "" || env.nodeCALookupCalls != 1 {
		t.Fatalf("custom value was not preserved: result=%+v arg=%q lookupCalls=%d", r, env.caCertArg, env.nodeCALookupCalls)
	}
	if _, err := os.Stat(filepath.Join(dir, nodeCAMarkerName)); !os.IsNotExist(err) {
		t.Fatalf("custom value must not create marker: %v", err)
	}
}

func TestTryPersistNodeCA_MatchingMarker_DoesNotHideCustomValue(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")
	setPrivileged(t, false)
	writeNodeCAMarker(dir, caPath)
	env := &mockEnv{nodeCAValue: filepath.Join(dir, "corporate-ca.pem"), nodeCAValueSet: true}

	r := New(dir, caPath, "en", WithEnvAdapter(env)).tryPersistNodeCA()
	if !errors.Is(r.Err, ErrUserCustomValue) || env.caCertArg != "" || env.nodeCALookupCalls != 1 {
		t.Fatalf("marker hid custom value: result=%+v arg=%q lookupCalls=%d", r, env.caCertArg, env.nodeCALookupCalls)
	}
}

func TestTryPersistNodeCA_ExistingDesiredValue_RepairsProfiles(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")
	setPrivileged(t, false)
	env := &mockEnv{nodeCAValue: caPath, nodeCAValueSet: true}

	r := New(dir, caPath, "en", WithEnvAdapter(env)).tryPersistNodeCA()
	if !r.Success || env.caCertArg != caPath || env.nodeCALookupCalls != 1 {
		t.Fatalf("existing value did not repair profiles: result=%+v arg=%q lookupCalls=%d", r, env.caCertArg, env.nodeCALookupCalls)
	}
}

func TestTryPersistNodeCA_PreviousManagedValue_IsMigrated(t *testing.T) {
	dir := t.TempDir()
	oldPath := writeFile(t, filepath.Join(dir, "old-ca.crt"), "same-cert")
	newPath := writeFile(t, filepath.Join(dir, "new-ca.crt"), "same-cert")
	setPrivileged(t, false)
	writeNodeCAMarker(dir, oldPath)
	env := &mockEnv{nodeCAValue: oldPath, nodeCAValueSet: true}

	r := New(dir, newPath, "en", WithEnvAdapter(env)).tryPersistNodeCA()
	if !r.Success || env.caCertArg != newPath || env.nodeCALookupCalls != 1 || !hasNodeCAMarker(dir, newPath) {
		t.Fatalf("managed value did not migrate: result=%+v arg=%q lookupCalls=%d", r, env.caCertArg, env.nodeCALookupCalls)
	}
}

func TestTryPersistNodeCA_LookupFailure_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")
	setPrivileged(t, false)
	env := &mockEnv{nodeCALookupErr: errors.New("lookup failed")}

	r := New(dir, caPath, "en", WithEnvAdapter(env)).tryPersistNodeCA()
	if r.Err == nil || !strings.Contains(r.Err.Error(), "lookup failed") || env.caCertArg != "" || env.nodeCALookupCalls != 1 {
		t.Fatalf("lookup did not fail closed: result=%+v arg=%q lookupCalls=%d", r, env.caCertArg, env.nodeCALookupCalls)
	}
}

func TestTryPersistNodeCA_CertExists_CallsPersistAndWritesMarker(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n")

	env := &mockEnv{}
	e := New(dir, caPath, "en", WithEnvAdapter(env))

	r := e.tryPersistNodeCA()
	if !r.Attempted || !r.Success {
		t.Errorf("expected Attempted=true Success=true, got %+v", r)
	}
	if env.caCertArg != caPath {
		t.Errorf("PersistNodeCACert called with %q, want %q", env.caCertArg, caPath)
	}
	if !hasNodeCAMarker(dir, caPath) {
		t.Error("expected marker to be written after successful persist")
	}
}

func TestTryPersistNodeCA_PersistFails_DoesNotWriteMarker(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	envErr := &testError{"setx failed"}

	env := &mockEnv{nodeCAErr: envErr}
	e := New(dir, caPath, "en", WithEnvAdapter(env))

	r := e.tryPersistNodeCA()
	if !r.Attempted || r.Success {
		t.Errorf("expected Attempted=true Success=false, got %+v", r)
	}
	if r.Err == nil || r.Err.Error() != "setx failed" {
		t.Errorf("expected error 'setx failed', got %v", r.Err)
	}
	if hasNodeCAMarker(dir, caPath) {
		t.Error("marker should NOT be written when persist fails")
	}
}

func TestTryPersistNodeCA_MarkerMatches_SkipsPersist(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "stable-cert-content")

	// Pre-write the matching marker
	writeNodeCAMarker(dir, caPath)

	env := &mockEnv{nodeCAErr: &testError{"should not be called"}}
	e := New(dir, caPath, "en", WithEnvAdapter(env))

	r := e.tryPersistNodeCA()
	if !r.Success || r.Attempted {
		t.Errorf("expected Success=true Attempted=false (marker hit), got %+v", r)
	}
	if env.caCertArg != "" {
		t.Errorf("PersistNodeCACert should NOT be called when marker matches, but got arg %q", env.caCertArg)
	}
}

func TestTryPersistNodeCA_MarkerStaleCertChanged_Repersists(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "original-cert")

	// Write marker for original cert
	writeNodeCAMarker(dir, caPath)

	// Change cert content (simulates CA regeneration)
	if err := os.WriteFile(caPath, []byte("new-cert-content"), 0644); err != nil {
		t.Fatal(err)
	}

	env := &mockEnv{}
	e := New(dir, caPath, "en", WithEnvAdapter(env))

	r := e.tryPersistNodeCA()
	if !r.Attempted || !r.Success {
		t.Errorf("expected re-persist on stale marker, got %+v", r)
	}
	if env.caCertArg != caPath {
		t.Errorf("PersistNodeCACert should be called with %q, got %q", caPath, env.caCertArg)
	}
	// Marker should be refreshed
	if !hasNodeCAMarker(dir, caPath) {
		t.Error("expected marker to be refreshed with new cert fingerprint")
	}
}

// F-3: marker 记录证书路径；路径变化（指纹相同）应重新持久化，不能仅凭指纹跳过。
func TestHasNodeCAMarker_PathChanged_Repersists(t *testing.T) {
	dir := t.TempDir()
	caPath1 := writeFile(t, filepath.Join(dir, "ca1.crt"), "same-cert-content")
	caPath2 := writeFile(t, filepath.Join(dir, "ca2.crt"), "same-cert-content") // 同内容不同路径

	writeNodeCAMarker(dir, caPath1)
	if hasNodeCAMarker(dir, caPath2) {
		t.Error("expected hasNodeCAMarker=false when cert path changed (same content), got true")
	}
}

// F-4: marker 记录 HOME；HOME 变化（如 sudo→普通用户）应重新持久化。
func TestHasNodeCAMarker_HomeChanged_Repersists(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")

	home1 := t.TempDir()
	t.Setenv("HOME", home1)
	t.Setenv("USERPROFILE", home1)
	writeNodeCAMarker(dir, caPath) // marker 记录 home1

	home2 := t.TempDir()
	t.Setenv("HOME", home2)
	t.Setenv("USERPROFILE", home2)

	if hasNodeCAMarker(dir, caPath) {
		t.Error("expected hasNodeCAMarker=false when HOME changed, got true")
	}
}

// 旧纯文本 marker（修复前格式）应视为 stale，触发重新持久化。
func TestHasNodeCAMarker_LegacyTextMarker_Repersists(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	fp, err := caFingerprint(caPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, nodeCAMarkerName), []byte(fp+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if hasNodeCAMarker(dir, caPath) {
		t.Error("expected hasNodeCAMarker=false for legacy plain-text marker, got true")
	}
}

// F-4 (UID): marker 记录的 UID 与当前不同 → 重新持久化（unix 普通用户；root 跳过）。
func TestHasNodeCAMarker_UIDChanged_Repersists(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("requires non-root uid to exercise UID matching")
	}
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")

	fp, err := caFingerprint(caPath)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	m := nodeCAMarker{
		Fingerprint: fp,
		CertPath:    caPath,
		Home:        home,
		UID:         os.Getuid() + 999, // 不同于当前 UID
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, nodeCAMarkerName), data, 0644); err != nil {
		t.Fatal(err)
	}

	if hasNodeCAMarker(dir, caPath) {
		t.Error("expected hasNodeCAMarker=false when UID differs, got true")
	}
}

// P2-2 (POSIX): 1b 特权运行时 symlink profile 的 target 不被跟随修改（fail-closed）。
func TestWritePOSIXProfileNodeCA_Privileged_SymlinkTargetNotFollowed(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert-content")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")
	setPrivileged(t, true)

	target := filepath.Join(home, ".bashrc.real")
	writeFile(t, target, "original-target")
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.Symlink(target, bashrc); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}

	a := &osEnvAdapter{}
	if err := a.writePOSIXProfileNodeCA(caPath); !errors.Is(err, ErrUnsafeProfile) {
		t.Errorf("expected ErrUnsafeProfile under privileged run, got %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "original-target" {
		t.Errorf("privileged: symlink target must not be modified, got %q", got)
	}
}

// P2-2 (POSIX): 1b 非特权运行跟随 symlink，mcc block 写入 target（dotfiles 兼容）。
func TestWritePOSIXProfileNodeCA_Unprivileged_FollowsSymlink(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert-content")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")
	setPrivileged(t, false)

	target := filepath.Join(home, ".bashrc.real")
	writeFile(t, target, "original-target")
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.Symlink(target, bashrc); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}

	a := &osEnvAdapter{}
	if err := a.writePOSIXProfileNodeCA(caPath); err != nil {
		t.Fatalf("writePOSIXProfileNodeCA: %v", err)
	}
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "NODE_EXTRA_CA_CERTS") {
		t.Errorf("unprivileged: mcc block should be written via symlink, got %q", got)
	}
}

// P2-2 (Pwsh): profile 是符号链接时，写入不能跟随链接修改目标文件（CWE-59）。
// 1b: 高权限运行时 symlink profile 的 target 不被 WriteFile 跟随修改（fail-closed）。
// 非特权运行时会跟随 symlink 写入（dotfiles 兼容），不适用此断言——由 scan 层测试覆盖。
func TestWritePwshProfileNodeCA_Privileged_SymlinkTargetNotFollowed(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert-content")

	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	target := filepath.Join(home, "profile.real")
	writeFile(t, target, "original-target")
	if err := os.MkdirAll(filepath.Dir(profile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, profile); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}
	withPwshHooks(t, home)
	setPrivileged(t, true)

	a := &osEnvAdapter{}
	if err := a.writePwshProfileNodeCA(caPath); !errors.Is(err, ErrUnsafeProfile) {
		t.Errorf("expected ErrUnsafeProfile under privileged run, got %v", err)
	}

	got, _ := os.ReadFile(target)
	if string(got) != "original-target" {
		t.Errorf("privileged: symlink target must not be modified via WriteFile follow, got %q", got)
	}
}

// isSafeForWrite 单元测试：覆盖不存在/常规/符号链接/非常规（目录）四个分支。
func TestIsSafeForWrite(t *testing.T) {
	dir := t.TempDir()

	// 不存在 → 安全（将创建）
	if err := isSafeForWrite(filepath.Join(dir, "absent")); err != nil {
		t.Errorf("absent profile should be safe: %v", err)
	}

	// 常规文件 → 安全
	regular := filepath.Join(dir, "regular")
	writeFile(t, regular, "x")
	if err := isSafeForWrite(regular); err != nil {
		t.Errorf("regular profile should be safe: %v", err)
	}

	// 符号链接 → 拒绝
	target := filepath.Join(dir, "target")
	writeFile(t, target, "x")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := isSafeForWrite(link); err == nil {
		t.Error("symlink profile should be rejected, got nil")
	}

	// 非常规（目录）→ 拒绝
	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := isSafeForWrite(subdir); err == nil {
		t.Error("non-regular profile (directory) should be rejected, got nil")
	}
}

// P2-2 (marker): .node-ca-persisted 是符号链接时，hasNodeCAMarker 返回 false（stale），
// writeNodeCAMarker 拒绝写（链接目标不变）。
func TestNodeCAMarker_Symlink_NotFollowed(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	target := filepath.Join(dir, "marker.real")
	writeFile(t, target, "original-target")
	markerPath := filepath.Join(dir, nodeCAMarkerName)
	if err := os.Symlink(target, markerPath); err != nil {
		t.Fatal(err)
	}

	if hasNodeCAMarker(dir, caPath) {
		t.Error("expected hasNodeCAMarker=false for symlink marker, got true")
	}
	writeNodeCAMarker(dir, caPath)
	got, _ := os.ReadFile(target)
	if string(got) != "original-target" {
		t.Errorf("symlink marker target modified: got %q, want %q", got, "original-target")
	}
}

// setPrivileged 注入 isPrivilegedRun 的 mock 值，测试结束自动还原。
// 测试默认串行（无 t.Parallel），包级 var 覆盖安全。
func setPrivileged(t *testing.T, priv bool) {
	t.Helper()
	prev := isPrivilegedRun
	isPrivilegedRun = func() bool { return priv }
	t.Cleanup(func() { isPrivilegedRun = prev })
}

// makeSymlinkProfile 创建一个指向 target 内容的 symlink profile，用于 scan/写入
// 的 symlink 场景测试。平台不支持创建 symlink 时 t.Skip（Windows 需开发者模式/admin）。
func makeSymlinkProfile(t *testing.T, profile, content string) {
	t.Helper()
	target := profile + ".target"
	writeFile(t, target, content)
	if err := os.MkdirAll(filepath.Dir(profile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, profile); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}
}

// P2-2/F-1 (1b): 高权限运行遇 symlink pwsh profile → scan 返回 ErrUnsafeProfile，
// 不跟随读。Fail-closed 保证 setx 不会在 profile 未读时被执行。
func TestScanPwshProfilesForCustomValue_Privileged_SymlinkFailsClosed(t *testing.T) {
	home := t.TempDir()
	withPwshHooks(t, home)
	setPrivileged(t, true)
	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	makeSymlinkProfile(t, profile, "$env:NODE_EXTRA_CA_CERTS = 'C:\\user\\custom\\ca.crt'\n")

	custom, err := scanPwshProfilesForCustomValue(home, isPrivilegedRun())
	if custom {
		t.Error("expected custom=false: privileged scan must not follow symlink")
	}
	if !errors.Is(err, ErrUnsafeProfile) {
		t.Errorf("expected ErrUnsafeProfile under privileged run, got %v", err)
	}
}

// P2-2/F-1 (1b): 非特权运行跟随 symlink pwsh profile，能读到自定义值（dotfiles 兼容）。
func TestScanPwshProfilesForCustomValue_Unprivileged_FollowsSymlink(t *testing.T) {
	home := t.TempDir()
	withPwshHooks(t, home)
	setPrivileged(t, false)
	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	makeSymlinkProfile(t, profile, "$env:NODE_EXTRA_CA_CERTS = 'C:\\user\\custom\\ca.crt'\n")

	custom, err := scanPwshProfilesForCustomValue(home, isPrivilegedRun())
	if err != nil {
		t.Fatalf("expected nil err under unprivileged run, got %v", err)
	}
	if !custom {
		t.Error("expected custom=true: unprivileged scan should follow symlink and detect custom value")
	}
}

// P2-2/F-1 (1b): 高权限运行遇 symlink POSIX profile → scan 返回 ErrUnsafeProfile。
func TestScanPOSIXProfilesForCustomValue_Privileged_SymlinkFailsClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	setPrivileged(t, true)
	profile := filepath.Join(home, ".bashrc")
	makeSymlinkProfile(t, profile, "export NODE_EXTRA_CA_CERTS=/custom/ca.crt\n")

	custom, err := scanPOSIXProfilesForCustomValue("/bin/bash", home, isPrivilegedRun())
	if custom {
		t.Error("expected custom=false: privileged scan must not follow symlink")
	}
	if !errors.Is(err, ErrUnsafeProfile) {
		t.Errorf("expected ErrUnsafeProfile under privileged run, got %v", err)
	}
}

// P2-2/F-1 (1b): 非特权运行跟随 symlink POSIX profile，能读到自定义值。
func TestScanPOSIXProfilesForCustomValue_Unprivileged_FollowsSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	setPrivileged(t, false)
	profile := filepath.Join(home, ".bashrc")
	makeSymlinkProfile(t, profile, "export NODE_EXTRA_CA_CERTS=/custom/ca.crt\n")

	custom, err := scanPOSIXProfilesForCustomValue("/bin/bash", home, isPrivilegedRun())
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !custom {
		t.Error("expected custom=true: unprivileged scan should follow symlink")
	}
}

// P2-2/F-1 集成: 高权限运行遇 symlink pwsh profile → persistNodeCACertWindows 不调用 setx。
// 这关闭了原 F-1 fail-open："profile 未改但环境已被覆盖"。
func TestPersistNodeCACert_Windows_Privileged_SymlinkProfile_NoSetx(t *testing.T) {
	home := t.TempDir()
	withPwshHooks(t, home)
	setPrivileged(t, true)
	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	makeSymlinkProfile(t, profile, "# clean profile\n")

	calls := 0
	prev := setxEnvVar
	setxEnvVar = func(k, v string) error { calls++; return nil }
	t.Cleanup(func() { setxEnvVar = prev })

	a := &osEnvAdapter{}
	err := a.persistNodeCACertWindows(`C:\fake\ca.crt`)
	if !errors.Is(err, ErrUnsafeProfile) {
		t.Errorf("expected ErrUnsafeProfile, got %v", err)
	}
	if calls != 0 {
		t.Errorf("expected setx 0 calls (privileged+symlink), got %d", calls)
	}
}

// P2-2/F-1 集成: 高权限运行遇 symlink POSIX profile → persistNodeCACertDarwin 不调用 launchctl。
func TestPersistNodeCACert_Darwin_Privileged_SymlinkProfile_NoLaunchctl(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows: os.UserHomeDir 读 USERPROFILE
	t.Setenv("SHELL", "/bin/zsh")
	setPrivileged(t, true)
	profile := filepath.Join(home, ".zshrc")
	makeSymlinkProfile(t, profile, "# clean profile\n")

	launchctlCalls := 0
	prevHas := hasLaunchctl
	prevSet := launchctlSetenv
	hasLaunchctl = func() bool { return true }
	launchctlSetenv = func(k, v string) error { launchctlCalls++; return nil }
	t.Cleanup(func() {
		hasLaunchctl = prevHas
		launchctlSetenv = prevSet
	})

	a := &osEnvAdapter{}
	err := a.persistNodeCACertDarwin("/fake/ca.crt")
	if !errors.Is(err, ErrUnsafeProfile) {
		t.Errorf("expected ErrUnsafeProfile, got %v", err)
	}
	if launchctlCalls != 0 {
		t.Errorf("expected launchctl 0 calls (privileged+symlink), got %d", launchctlCalls)
	}
}

// writeMarkerJSON 写一个 marker JSON 到 dir/.node-ca-persisted，用于 F-4 测试。
func writeMarkerJSON(t *testing.T, dir string, m nodeCAMarker) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, nodeCAMarkerName), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// F-4: marker 缺 HOME 字段 → stale（避免任意用户命中）
func TestNodeCAMarker_MissingHome_Stale(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	fp, err := caFingerprint(caPath)
	if err != nil {
		t.Fatal(err)
	}
	writeMarkerJSON(t, dir, nodeCAMarker{Fingerprint: fp, CertPath: caPath}) // 无 Home
	if hasNodeCAMarker(dir, caPath) {
		t.Error("marker without Home must be stale")
	}
}

// F-4: marker Home 为空字符串 → stale
func TestNodeCAMarker_EmptyHome_Stale(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	fp, err := caFingerprint(caPath)
	if err != nil {
		t.Fatal(err)
	}
	writeMarkerJSON(t, dir, nodeCAMarker{Fingerprint: fp, CertPath: caPath, Home: ""})
	if hasNodeCAMarker(dir, caPath) {
		t.Error("marker with empty Home must be stale")
	}
}

// F-4: marker Home 与当前进程不匹配 → stale
func TestNodeCAMarker_HomeMismatch_Stale(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	fp, err := caFingerprint(caPath)
	if err != nil {
		t.Fatal(err)
	}
	writeMarkerJSON(t, dir, nodeCAMarker{Fingerprint: fp, CertPath: caPath, Home: "/definitely/not/home"})
	if hasNodeCAMarker(dir, caPath) {
		t.Error("marker with mismatched Home must be stale")
	}
}

// F-4: Unix 非根用户，marker UID 不匹配 → stale
func TestNodeCAMarker_UIDMismatch_Stale(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UID enforcement is Unix-only")
	}
	uid := os.Getuid()
	if uid <= 0 {
		t.Skip("test requires non-root unix uid")
	}
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	fp, err := caFingerprint(caPath)
	if err != nil {
		t.Fatal(err)
	}
	writeMarkerJSON(t, dir, nodeCAMarker{Fingerprint: fp, CertPath: caPath, Home: dir, UID: uid + 9999})
	if hasNodeCAMarker(dir, caPath) {
		t.Error("marker with mismatched UID must be stale")
	}
}

// F-4: Unix 非根用户，marker 缺 UID → stale（必须记录匹配 UID）
func TestNodeCAMarker_MissingUID_UnprivilegedUnix_Stale(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UID enforcement is Unix-only")
	}
	uid := os.Getuid()
	if uid <= 0 {
		t.Skip("test requires non-root unix uid")
	}
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	fp, err := caFingerprint(caPath)
	if err != nil {
		t.Fatal(err)
	}
	writeMarkerJSON(t, dir, nodeCAMarker{Fingerprint: fp, CertPath: caPath, Home: dir}) // 无 UID
	if hasNodeCAMarker(dir, caPath) {
		t.Error("marker without UID must be stale for non-root unix user")
	}
}

// F-4: HOME（+ Unix UID）完全匹配 → hasNodeCAMarker 命中
func TestNodeCAMarker_MatchingMarker_Hit(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	fp, err := caFingerprint(caPath)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeCAMarker{Fingerprint: fp, CertPath: caPath, Home: dir}
	if uid := os.Getuid(); uid > 0 {
		m.UID = uid
	}
	writeMarkerJSON(t, dir, m)
	if !hasNodeCAMarker(dir, caPath) {
		t.Error("matching marker should hit")
	}
}

// F-4: UserHomeDir 失败时 writeNodeCAMarker 不写出可跨用户命中的 marker。
func TestWriteNodeCAMarker_UserHomeDirFails_NoMarker(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", "")
	} else {
		t.Setenv("HOME", "")
	}
	writeNodeCAMarker(dir, caPath)
	if _, err := os.Stat(filepath.Join(dir, nodeCAMarkerName)); !os.IsNotExist(err) {
		t.Errorf("marker must not be written when UserHomeDir fails: %v", err)
	}
}

// P2-2: 高权限运行时 tryPersistNodeCA 拒绝写 profile，不调用 PersistNodeCACert。
// 这同时关闭"sudo mcc 写 root profile 对真实用户 Node 客户端无效"的功能 bug，
// 和"高权限 + 用户可控 HOME 越权写"的安全风险。
func TestTryPersistNodeCA_PrivilegedRun_Rejects(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert-content")
	setPrivileged(t, true)

	env := &mockEnv{nodeCAErr: errors.New("should not be called")}
	e := New(dir, caPath, "en", WithEnvAdapter(env))

	r := e.tryPersistNodeCA()
	if !r.Attempted || r.Success {
		t.Errorf("expected Attempted=true Success=false, got %+v", r)
	}
	if !errors.Is(r.Err, ErrPrivilegedRun) {
		t.Errorf("expected ErrPrivilegedRun, got %v", r.Err)
	}
	if env.caCertArg != "" {
		t.Errorf("PersistNodeCACert must NOT be called under privileged run, got arg %q", env.caCertArg)
	}
}

// P2-2: instructions 在 ErrPrivilegedRun 时打印"非特权重启"提示（中英）。
func TestGenerateInstructions_TransparentSuccess_PrivilegedRun_PrintsHint(t *testing.T) {
	r := Result{
		SelectedMode: ModeTransparent,
		HostsResult:  StepResult{Success: true},
		TrustResult:  StepResult{Success: true},
		EnvResult:    StepResult{Attempted: true, Success: true},
		NodeCAResult: StepResult{Attempted: true, Success: false, Err: ErrPrivilegedRun},
	}
	for _, locale := range []string{"zh", "en"} {
		lines := generateInstructions(r, locale)
		found := false
		for _, l := range lines {
			if strings.Contains(l, "非特权") || strings.Contains(l, "non-privileged") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("[%s] expected privileged-run hint, got %v", locale, lines)
		}
	}
}

// F-1: scanPwshProfilesForCustomValue 遇非 NotExist 读取错误（profile 是目录）→ 返回 error。
// 用目录作为 profile，os.ReadFile 必失败且非 NotExist，跨平台稳定（不依赖 chmod）。
func TestScanPwshProfilesForCustomValue_ProfileUnreadable_ReturnsError(t *testing.T) {
	home := t.TempDir()
	withPwshHooks(t, home)
	setPrivileged(t, false) // 非特权跳过 isSafeForWrite，直接 readProfile
	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	if err := os.MkdirAll(profile, 0755); err != nil {
		t.Fatal(err)
	}
	custom, err := scanPwshProfilesForCustomValue(home, false)
	if err == nil {
		t.Error("expected error when profile is a directory (unreadable)")
	}
	if custom {
		t.Error("expected custom=false when scan fails")
	}
}

// F-1: scanPOSIXProfilesForCustomValue 遇非 NotExist 读取错误 → 返回 error。
func TestScanPOSIXProfilesForCustomValue_ProfileUnreadable_ReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")
	setPrivileged(t, false)
	profile := filepath.Join(home, ".bashrc")
	if err := os.MkdirAll(profile, 0755); err != nil {
		t.Fatal(err)
	}
	custom, err := scanPOSIXProfilesForCustomValue("/bin/bash", home, false)
	if err == nil {
		t.Error("expected error when profile is a directory (unreadable)")
	}
	if custom {
		t.Error("expected custom=false when scan fails")
	}
}

// F-1: scanPwshProfilesForCustomValue profile 不存在 → 无错误，走正常创建路径。
func TestScanPwshProfilesForCustomValue_ProfileAbsent_NoError(t *testing.T) {
	home := t.TempDir()
	withPwshHooks(t, home)
	custom, err := scanPwshProfilesForCustomValue(home, false)
	if err != nil {
		t.Errorf("absent profile should not error (treated as empty), got %v", err)
	}
	if custom {
		t.Error("absent profile → custom=false")
	}
}

// F-1: 文件系统根本身不存在时不能把 profile 视为可创建。Windows 的未挂载
// 盘符或不存在的 UNC share 无法由 MkdirAll 创建；这里注入 stat 结果，使该决策
// 在 Linux 上也能确定性验证。
func TestValidateParentChain_MissingRoot_ReturnsError(t *testing.T) {
	statMissing := func(path string) (os.FileInfo, error) {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
	}
	profile := filepath.Join(string(filepath.Separator), "missing", "profile")
	if err := validateParentChainWithStat(profile, statMissing); err == nil {
		t.Fatal("expected missing filesystem root to fail closed")
	}
}

// F-1: scan 读取失败时 persistNodeCACertWindows 不调用 setx。
func TestPersistNodeCACert_Windows_ProfileUnreadable_NoSetx(t *testing.T) {
	home := t.TempDir()
	withPwshHooks(t, home)
	setPrivileged(t, false)
	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	if err := os.MkdirAll(profile, 0755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	prev := setxEnvVar
	setxEnvVar = func(k, v string) error { calls++; return nil }
	t.Cleanup(func() { setxEnvVar = prev })

	a := &osEnvAdapter{}
	err := a.persistNodeCACertWindows(`C:\fake\ca.crt`)
	if err == nil {
		t.Error("expected error from scan when profile is a directory")
	}
	if calls != 0 {
		t.Errorf("expected setx 0 calls when scan fails, got %d", calls)
	}
}

// F-1: scan 读取失败时 persistNodeCACertDarwin 不调用 launchctl。
func TestPersistNodeCACert_Darwin_ProfileUnreadable_NoLaunchctl(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/zsh")
	setPrivileged(t, false)
	profile := filepath.Join(home, ".zshrc")
	if err := os.MkdirAll(profile, 0755); err != nil {
		t.Fatal(err)
	}
	launchctlCalls := 0
	prevHas := hasLaunchctl
	prevSet := launchctlSetenv
	hasLaunchctl = func() bool { return true }
	launchctlSetenv = func(k, v string) error { launchctlCalls++; return nil }
	t.Cleanup(func() {
		hasLaunchctl = prevHas
		launchctlSetenv = prevSet
	})

	a := &osEnvAdapter{}
	err := a.persistNodeCACertDarwin("/fake/ca.crt")
	if err == nil {
		t.Error("expected error from scan when profile is a directory")
	}
	if launchctlCalls != 0 {
		t.Errorf("expected launchctl 0 calls when scan fails, got %d", launchctlCalls)
	}
}

// P3: decidePrivileged 是 fail-closed 决策的纯函数，覆盖 Windows token 探测的
// 所有错误路径（无需真实 Windows token，跨平台可测）。
// err != nil（无法确定权限）→ 视为特权 → 拒绝 profile 修改。
func TestDecidePrivileged(t *testing.T) {
	cases := []struct {
		name     string
		elevated bool
		err      error
		want     bool // true = 拒绝（特权或未知权限）
	}{
		{"elevated → reject", true, nil, true},
		{"non-elevated → allow", false, nil, false},
		{"token open error → fail-closed", false, errors.New("open process token: access denied"), true},
		{"elevation query error → fail-closed", false, errors.New("get token elevation: failed"), true},
		{"elevation returned short → fail-closed", false, errors.New("token elevation returned 0 bytes, want 4"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decidePrivileged(tc.elevated, tc.err); got != tc.want {
				t.Errorf("decidePrivileged(elevated=%v, err=%v) = %v, want %v", tc.elevated, tc.err, got, tc.want)
			}
		})
	}
}

// writeFile is a helper that writes content to path and returns the path.
func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- replaceMarkedBlock tests ---

func TestReplaceMarkedBlock_AppendWhenNoExistingBlock(t *testing.T) {
	existing := "# user config\nexport FOO=bar\n"
	begin := "# >>> mcc >>>"
	end := "# <<< mcc <<<"
	block := "# >>> mcc >>>\nnew content\n# <<< mcc <<<\n"

	out, changed := replaceMarkedBlock(existing, begin, end, block)
	if !changed {
		t.Error("expected changed=true when no existing block")
	}
	if !contains(out, "# user config") {
		t.Error("original content should be preserved")
	}
	if !contains(out, "new content") {
		t.Error("new block should be appended")
	}
}

func TestReplaceMarkedBlock_AppendWhenEmptyContent(t *testing.T) {
	block := "# >>> mcc >>>\nnew content\n# <<< mcc <<<\n"
	out, changed := replaceMarkedBlock("", "# >>> mcc >>>", "# <<< mcc <<<", block)
	if !changed {
		t.Error("expected changed=true for empty content")
	}
	if out != block {
		t.Errorf("expected block as-is, got %q", out)
	}
}

func TestReplaceMarkedBlock_NoChangeWhenIdentical(t *testing.T) {
	block := "# >>> mcc >>>\nnew content\n# <<< mcc <<<"
	existing := "header\n" + block + "\nfooter\n"

	out, changed := replaceMarkedBlock(existing, "# >>> mcc >>>", "# <<< mcc <<<", block+"\n")
	if changed {
		t.Error("expected changed=false when block is identical")
	}
	if out != existing {
		t.Errorf("content should not be modified")
	}
}

func TestReplaceMarkedBlock_ReplaceWhenPathChanged(t *testing.T) {
	oldBlock := "# >>> mcc >>>\nold content\n# <<< mcc <<<"
	newBlock := "# >>> mcc >>>\nnew content\n# <<< mcc <<<\n"
	existing := "header\n" + oldBlock + "\nfooter\n"

	out, changed := replaceMarkedBlock(existing, "# >>> mcc >>>", "# <<< mcc <<<", newBlock)
	if !changed {
		t.Error("expected changed=true when block content differs")
	}
	if !contains(out, "new content") {
		t.Error("new block should replace old")
	}
	if contains(out, "old content") {
		t.Error("old content should be gone")
	}
	if !contains(out, "header") || !contains(out, "footer") {
		t.Error("surrounding content should be preserved")
	}
}

func TestReplaceMarkedBlock_EnsureTrailingNewlineBeforeAppend(t *testing.T) {
	existing := "no-trailing-newline"
	block := "# >>> mcc >>>\ncontent\n# <<< mcc <<<\n"
	out, changed := replaceMarkedBlock(existing, "# >>> mcc >>>", "# <<< mcc <<<", block)
	if !changed {
		t.Error("expected changed=true")
	}
	if !strings.Contains(out, "no-trailing-newline\n# >>> mcc >>>") {
		t.Errorf("expected newline inserted before block, got: %q", out)
	}
}

// --- writePOSIXProfileNodeCA tests ---

func TestWritePOSIXProfileNodeCA_Bash_WritesExport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")

	caPath := filepath.Join(home, "data", "ca.crt")
	writeFile(t, caPath, "cert-content")

	a := &osEnvAdapter{}
	if err := a.writePOSIXProfileNodeCA(caPath); err != nil {
		t.Fatalf("writePOSIXProfileNodeCA: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)
	if !contains(s, "NODE_EXTRA_CA_CERTS") {
		t.Errorf("expected NODE_EXTRA_CA_CERTS in .bashrc, got: %s", s)
	}
	if !contains(s, "export") {
		t.Errorf("expected export syntax for bash, got: %s", s)
	}
	if !contains(s, caPath) {
		t.Errorf("expected CA path %q in .bashrc, got: %s", caPath, s)
	}
}

func TestWritePOSIXProfileNodeCA_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")

	caPath := filepath.Join(home, "ca.crt")
	writeFile(t, caPath, "cert-content")

	a := &osEnvAdapter{}
	if err := a.writePOSIXProfileNodeCA(caPath); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := a.writePOSIXProfileNodeCA(caPath); err != nil {
		t.Fatalf("second call: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(content), "NODE_EXTRA_CA_CERTS"); got != 1 {
		t.Errorf("expected exactly 1 NODE_EXTRA_CA_CERTS, got %d in: %s", got, content)
	}
}

func TestWritePOSIXProfileNodeCA_CAPathChanged_UpdatesProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")

	oldPath := filepath.Join(home, "old-ca.crt")
	writeFile(t, oldPath, "old-cert")
	newPath := filepath.Join(home, "new-ca.crt")
	writeFile(t, newPath, "new-cert")

	a := &osEnvAdapter{}
	if err := a.writePOSIXProfileNodeCA(oldPath); err != nil {
		t.Fatalf("first call with old path: %v", err)
	}
	if err := a.writePOSIXProfileNodeCA(newPath); err != nil {
		t.Fatalf("second call with new path: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)
	if !contains(s, newPath) {
		t.Errorf("expected new CA path in profile, got: %s", s)
	}
	// The profile should still have exactly 1 NODE_EXTRA_CA_CERTS line (replaced, not appended)
	if got := strings.Count(s, "NODE_EXTRA_CA_CERTS"); got != 1 {
		t.Errorf("expected exactly 1 NODE_EXTRA_CA_CERTS after update, got %d in: %s", got, s)
	}
}

func TestWritePOSIXProfileNodeCA_Fish_UsesSetGx(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/usr/bin/fish")

	caPath := filepath.Join(home, "ca.crt")
	writeFile(t, caPath, "cert-content")

	a := &osEnvAdapter{}
	if err := a.writePOSIXProfileNodeCA(caPath); err != nil {
		t.Fatalf("writePOSIXProfileNodeCA: %v", err)
	}

	fishDir := filepath.Join(home, ".config", "fish")
	content, err := os.ReadFile(filepath.Join(fishDir, "config.fish"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)
	if !contains(s, "set -gx") {
		t.Errorf("expected fish set -gx syntax, got: %s", s)
	}
	// 确保不含裸 set -x（后跟空格）——防止 set -gx 被降级回归
	if strings.Contains(s, "set -x ") {
		t.Errorf("should use set -gx (global), not set -x: %s", s)
	}
	if !contains(s, "NODE_EXTRA_CA_CERTS") {
		t.Errorf("expected NODE_EXTRA_CA_CERTS, got: %s", s)
	}
}

// --- Integration: transparent ready + NodeCA failure prints warning ---

func TestGenerateInstructions_TransparentSuccess_NodeCAFailure_PrintsWarning(t *testing.T) {
	r := Result{
		SelectedMode: ModeTransparent,
		HostsResult:  StepResult{Attempted: true, Success: true},
		TrustResult:  StepResult{Attempted: true, Success: true},
		EnvResult:    StepResult{Attempted: true, Success: true},
		NodeCAResult: StepResult{Attempted: true, Success: false, Err: &testError{"setx failed"}},
	}
	for _, locale := range []string{"zh", "en"} {
		lines := generateInstructions(r, locale)
		found := false
		for _, l := range lines {
			if contains(l, "NODE_EXTRA_CA_CERTS") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("[%s] expected NODE_EXTRA_CA_CERTS failure warning, got: %v", locale, lines)
		}
	}
}

func TestGenerateInstructions_TransparentSuccess_NodeCASuccess_NoWarning(t *testing.T) {
	r := Result{
		SelectedMode: ModeTransparent,
		HostsResult:  StepResult{Attempted: true, Success: true},
		TrustResult:  StepResult{Attempted: true, Success: true},
		EnvResult:    StepResult{Attempted: true, Success: true},
		NodeCAResult: StepResult{Attempted: true, Success: true},
	}
	lines := generateInstructions(r, "en")
	for _, l := range lines {
		if contains(l, "NODE_EXTRA_CA_CERTS") && contains(l, "failed") {
			t.Errorf("should NOT have NodeCA failure warning when success, got: %v", lines)
			break
		}
	}
}

func TestGenerateInstructions_TransparentEnvironmentRefreshGuidance(t *testing.T) {
	tests := []struct {
		name   string
		locale string
		nodeCA StepResult
		want   string
	}{
		{
			name:   "Chinese success asks for Orca restart",
			locale: "zh",
			nodeCA: StepResult{Attempted: true, Success: true},
			want:   "完全退出并重新启动 Orca",
		},
		{
			name:   "English success asks for Orca restart",
			locale: "en",
			nodeCA: StepResult{Attempted: true, Success: true},
			want:   "fully quit and restart Orca",
		},
		{
			name:   "Chinese refresh failure asks for sign in",
			locale: "zh",
			nodeCA: StepResult{
				Attempted: true,
				Partial:   true,
				Err:       fmt.Errorf("%w: %w: timeout", ErrPartialSuccess, ErrEnvironmentRefresh),
			},
			want: "注销并重新登录",
		},
		{
			name:   "English refresh failure asks for sign in",
			locale: "en",
			nodeCA: StepResult{
				Attempted: true,
				Partial:   true,
				Err:       fmt.Errorf("%w: %w: timeout", ErrPartialSuccess, ErrEnvironmentRefresh),
			},
			want: "sign out and sign back in",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Result{
				SelectedMode: ModeTransparent,
				HostsResult:  StepResult{Success: true},
				TrustResult:  StepResult{Success: true},
				EnvResult:    StepResult{Success: true},
				NodeCAResult: tt.nodeCA,
			}
			lines := generateInstructions(r, tt.locale)
			if !contains(strings.Join(lines, "\n"), tt.want) {
				t.Fatalf("instructions missing %q: %v", tt.want, lines)
			}
		})
	}
}

// --- P0-1: writePwshProfileNodeCA path tests ---

// withPwshHooks overrides pwshDetected and pwshProfileCandidates for testing,
// and sets USERPROFILE to home so os.UserHomeDir() is redirected.
func withPwshHooks(t *testing.T, home string, extraCandidates ...string) {
	t.Helper()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home) // Linux/Unix: os.UserHomeDir() reads $HOME, not %USERPROFILE%

	origDetected := pwshDetected
	origCandidates := pwshProfileCandidates
	pwshDetected = func() bool { return true }
	pwshProfileCandidates = func(h string) []string {
		candidates := []string{
			filepath.Join(h, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
			filepath.Join(h, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		}
		return append(candidates, extraCandidates...)
	}
	t.Cleanup(func() {
		pwshDetected = origDetected
		pwshProfileCandidates = origCandidates
	})
}

func TestWritePwshProfileNodeCA_CertOutsideHome_UsesAbsolutePath(t *testing.T) {
	home := t.TempDir()
	otherDir := t.TempDir()
	caPath := writeFile(t, filepath.Join(otherDir, "mcc", "ca.crt"), "cert")
	withPwshHooks(t, home)

	a := &osEnvAdapter{}
	if err := a.writePwshProfileNodeCA(caPath); err != nil {
		t.Fatalf("writePwshProfileNodeCA: %v", err)
	}

	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	content, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	s := string(content)
	// 应含绝对路径原值，不含 $env:USERPROFILE\ + 绝对路径的拼接形态
	if !contains(s, caPath) {
		t.Errorf("profile should contain absolute caCertPath %q, got: %s", caPath, s)
	}
	if strings.Contains(s, "$env:USERPROFILE\\"+caPath) {
		t.Errorf("profile should NOT have $env:USERPROFILE prepended to absolute path outside home, got: %s", s)
	}
}

func TestWritePwshProfileNodeCA_CertInsideHome_UsesUserProfileRef(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "data", "ca.crt"), "cert")
	withPwshHooks(t, home)

	a := &osEnvAdapter{}
	if err := a.writePwshProfileNodeCA(caPath); err != nil {
		t.Fatalf("writePwshProfileNodeCA: %v", err)
	}

	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	content, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	s := string(content)
	// home 内仍引用 $env:USERPROFILE（保留可移植性），且不写死 home 绝对路径。
	// P2-1 后渲染为 Join-Path $env:USERPROFILE '<single-quoted-rel>'。
	if !contains(s, "$env:USERPROFILE") {
		t.Errorf("profile should reference $env:USERPROFILE for cert inside home, got: %s", s)
	}
	if strings.Contains(s, home) {
		t.Errorf("profile should NOT contain literal home path %q, got: %s", home, s)
	}
}

// --- P0-2: pwsh profile idempotent and path-changed tests ---

func TestWritePwshProfileNodeCA_Idempotent(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")
	withPwshHooks(t, home)

	a := &osEnvAdapter{}
	if err := a.writePwshProfileNodeCA(caPath); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := a.writePwshProfileNodeCA(caPath); err != nil {
		t.Fatalf("second call: %v", err)
	}

	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	content, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)
	if got := strings.Count(s, pwshProfileMarkerBegin); got != 1 {
		t.Errorf("expected 1 marker block, got %d in: %s", got, s)
	}
	if got := strings.Count(s, "NODE_EXTRA_CA_CERTS"); got != 1 {
		t.Errorf("expected 1 NODE_EXTRA_CA_CERTS, got %d in: %s", got, s)
	}
}

func TestWritePwshProfileNodeCA_CAPathChanged_UpdatesBlock(t *testing.T) {
	home := t.TempDir()
	oldPath := writeFile(t, filepath.Join(home, "old-ca.crt"), "old")
	newPath := writeFile(t, filepath.Join(home, "new-ca.crt"), "new")
	withPwshHooks(t, home)

	a := &osEnvAdapter{}
	if err := a.writePwshProfileNodeCA(oldPath); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := a.writePwshProfileNodeCA(newPath); err != nil {
		t.Fatalf("second call: %v", err)
	}

	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	content, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)
	if got := strings.Count(s, pwshProfileMarkerBegin); got != 1 {
		t.Errorf("expected 1 marker block after path change, got %d in: %s", got, s)
	}
	if !contains(s, "new-ca") {
		t.Errorf("profile should contain new CA path, got: %s", s)
	}
	if contains(s, "old-ca") {
		t.Errorf("profile should NOT contain old CA path, got: %s", s)
	}
}

// --- P1-1: write to both pwsh 7 and 5.1 profiles ---

func TestWritePwshProfileNodeCA_WritesBothPwsh7AndWindowsPsh5_IfApplicable(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")
	withPwshHooks(t, home)

	a := &osEnvAdapter{}
	if err := a.writePwshProfileNodeCA(caPath); err != nil {
		t.Fatalf("writePwshProfileNodeCA: %v", err)
	}

	// Both candidate profiles should be written
	pwsh7Profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	winPs5Profile := filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1")

	for _, profile := range []string{pwsh7Profile, winPs5Profile} {
		content, err := os.ReadFile(profile)
		if err != nil {
			t.Fatalf("read %s: %v", profile, err)
		}
		s := string(content)
		if !contains(s, pwshProfileMarkerBegin) {
			t.Errorf("%s should contain mcc marker block, got: %s", profile, s)
		}
		if !contains(s, "NODE_EXTRA_CA_CERTS") {
			t.Errorf("%s should contain NODE_EXTRA_CA_CERTS, got: %s", profile, s)
		}
	}
}

// --- P0-2: persistNodeCACertWindows setx + profile ---

func TestPersistNodeCACert_Windows_SetxAndProfile(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")

	// Capture setx call
	var setxKey, setxValue string
	origSetx := setxEnvVar
	setxEnvVar = func(key, value string) error {
		setxKey = key
		setxValue = value
		return nil
	}
	t.Cleanup(func() { setxEnvVar = origSetx })

	withPwshHooks(t, home)

	a := &osEnvAdapter{}
	if err := a.persistNodeCACertWindows(caPath); err != nil {
		t.Fatalf("persistNodeCACertWindows: %v", err)
	}

	if setxKey != "NODE_EXTRA_CA_CERTS" {
		t.Errorf("setx key = %q, want NODE_EXTRA_CA_CERTS", setxKey)
	}
	if setxValue != caPath {
		t.Errorf("setx value = %q, want %q", setxValue, caPath)
	}

	// Profile should also be written
	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	content, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if !contains(string(content), pwshProfileMarkerBegin) {
		t.Error("profile should contain mcc marker block")
	}
}

func TestPersistNodeCACert_Windows_SetxSuccess_BroadcastsEnvironmentChange(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")
	withPwshHooks(t, home)

	origSetx := setxEnvVar
	setxEnvVar = func(string, string) error { return nil }
	t.Cleanup(func() { setxEnvVar = origSetx })

	broadcastCalls := 0
	origBroadcast := broadcastEnvironmentChange
	broadcastEnvironmentChange = func() error {
		broadcastCalls++
		return nil
	}
	t.Cleanup(func() { broadcastEnvironmentChange = origBroadcast })

	a := &osEnvAdapter{}
	if err := a.persistNodeCACertWindows(caPath); err != nil {
		t.Fatalf("persistNodeCACertWindows: %v", err)
	}
	if broadcastCalls != 1 {
		t.Fatalf("broadcast calls = %d, want 1", broadcastCalls)
	}
}

func TestPersistNodeCACert_Windows_SetxFailure_DoesNotBroadcast(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")
	withPwshHooks(t, home)

	origSetx := setxEnvVar
	setxEnvVar = func(string, string) error { return errors.New("setx denied") }
	t.Cleanup(func() { setxEnvVar = origSetx })

	broadcastCalls := 0
	origBroadcast := broadcastEnvironmentChange
	broadcastEnvironmentChange = func() error {
		broadcastCalls++
		return nil
	}
	t.Cleanup(func() { broadcastEnvironmentChange = origBroadcast })

	a := &osEnvAdapter{}
	err := a.persistNodeCACertWindows(caPath)
	if !errors.Is(err, ErrPartialSuccess) {
		t.Fatalf("expected ErrPartialSuccess, got %v", err)
	}
	if broadcastCalls != 0 {
		t.Fatalf("broadcast calls = %d, want 0 after setx failure", broadcastCalls)
	}
}

func TestPersistNodeCACert_Windows_BroadcastFailure_WritesProfileAndReturnsPartial(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")
	withPwshHooks(t, home)

	origSetx := setxEnvVar
	setxEnvVar = func(string, string) error { return nil }
	t.Cleanup(func() { setxEnvVar = origSetx })

	origBroadcast := broadcastEnvironmentChange
	broadcastEnvironmentChange = func() error { return errors.New("send timeout") }
	t.Cleanup(func() { broadcastEnvironmentChange = origBroadcast })

	a := &osEnvAdapter{}
	err := a.persistNodeCACertWindows(caPath)
	if !errors.Is(err, ErrPartialSuccess) {
		t.Fatalf("expected ErrPartialSuccess, got %v", err)
	}
	if !errors.Is(err, ErrEnvironmentRefresh) {
		t.Fatalf("expected ErrEnvironmentRefresh, got %v", err)
	}

	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	content, readErr := os.ReadFile(profile)
	if readErr != nil {
		t.Fatalf("read profile: %v", readErr)
	}
	if !contains(string(content), pwshProfileMarkerBegin) {
		t.Fatal("profile should still be written after broadcast failure")
	}
}

// --- P1-2: partial success ---

func TestPersistNodeCACert_PartialSuccess_NoMarkerWritten(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")

	// mock 直接返回 ErrPartialSuccess（模拟 setx 失败 + profile 成功的场景）
	env := &mockEnv{nodeCAErr: ErrPartialSuccess}
	e := New(dir, caPath, "en", WithEnvAdapter(env))

	r := e.tryPersistNodeCA()
	if !r.Attempted {
		t.Error("expected Attempted=true")
	}
	if r.Success {
		t.Error("expected Success=false for partial success")
	}
	if !r.Partial {
		t.Error("expected Partial=true when PersistNodeCACert returns ErrPartialSuccess")
	}
	if r.Err == nil {
		t.Error("expected non-nil Err")
	}
	// Marker should NOT be written (need to retry failed part next launch)
	if hasNodeCAMarker(dir, caPath) {
		t.Error("marker should NOT be written on partial success")
	}
}

func TestStateHash_NodeCAPartial_DiffersFromSuccess(t *testing.T) {
	base := Result{
		SelectedMode: ModeTransparent,
		HostsResult:  StepResult{Success: true},
		TrustResult:  StepResult{Success: true},
		EnvResult:    StepResult{Attempted: true, Success: true},
	}

	rSuccess := base
	rSuccess.NodeCAResult = StepResult{Attempted: true, Success: true}

	rPartial := base
	rPartial.NodeCAResult = StepResult{Attempted: true, Success: false, Partial: true, Err: &testError{"setx failed"}}

	h1 := stateHash(rSuccess)
	h2 := stateHash(rPartial)
	if h1 == h2 {
		t.Error("stateHash should differ between Success and Partial")
	}

	// Also verify partial differs from full failure
	rFail := base
	rFail.NodeCAResult = StepResult{Attempted: true, Success: false, Err: &testError{"total failure"}}
	h3 := stateHash(rFail)
	if h2 == h3 {
		t.Error("stateHash should differ between Partial and full failure")
	}
}

// --- P2-1: user custom value detection ---

func TestProfileHasNodeCAKeyOutsideMCCBlock(t *testing.T) {
	tests := []struct {
		name    string
		shell   string
		content string
		want    bool
	}{
		{
			name:    "user hand-written export detected",
			shell:   "/bin/bash",
			content: "export NODE_EXTRA_CA_CERTS=/some/path\n",
			want:    true,
		},
		{
			name:    "mcc block not detected as user custom",
			shell:   "/bin/bash",
			content: "# >>> mcc: Node.js CA trust >>>\nexport NODE_EXTRA_CA_CERTS=/path\n# <<< mcc <<<\n",
			want:    false,
		},
		{
			name:    "user export outside mcc block detected",
			shell:   "/bin/bash",
			content: "export FOO=bar\n# >>> mcc: Node.js CA trust >>>\nexport NODE_EXTRA_CA_CERTS=/mcc/path\n# <<< mcc <<<\nexport NODE_EXTRA_CA_CERTS=/user/path\n",
			want:    true,
		},
		{
			name:    "no NODE_EXTRA_CA_CERTS at all",
			shell:   "/bin/bash",
			content: "export MCC_ROOT=/opt/mcc\n",
			want:    false,
		},
		{
			name:    "commented line not detected",
			shell:   "/bin/bash",
			content: "# export NODE_EXTRA_CA_CERTS=/old/path\n",
			want:    false,
		},
		{
			name:    "fish set -gx detected",
			shell:   "/usr/bin/fish",
			content: "set -gx NODE_EXTRA_CA_CERTS /some/path\n",
			want:    true,
		},
		{
			name:    "fish inside mcc block not detected",
			shell:   "/usr/bin/fish",
			content: "# >>> mcc: Node.js CA trust >>>\nset -gx NODE_EXTRA_CA_CERTS /path\n# <<< mcc <<<\n",
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := profileHasNodeCAKeyOutsideMCCBlock(tt.shell, tt.content)
			if got != tt.want {
				t.Errorf("profileHasNodeCAKeyOutsideMCCBlock(%q, %q) = %v, want %v",
					tt.shell, tt.content, got, tt.want)
			}
		})
	}
}

func TestPwshProfileHasNodeCAVarOutsideMCCBlock(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "assignment with $env: detected",
			content: "$env:NODE_EXTRA_CA_CERTS = 'C:\\ca.crt'\n",
			want:    true,
		},
		{
			name:    "assignment with $ syntax detected",
			content: "$NODE_EXTRA_CA_CERTS = 'C:\\ca.crt'\n",
			want:    true,
		},
		{
			name:    "inside mcc block not detected",
			content: "# >>> mcc: Node.js CA trust (auto-managed, do not edit) >>>\n$env:NODE_EXTRA_CA_CERTS = $mccCa\n# <<< mcc <<<\n",
			want:    false,
		},
		{
			name:    "no NODE_EXTRA_CA_CERTS",
			content: "$env:PATH += ';C:\\tools'\n",
			want:    false,
		},
		{
			name:    "comment line not detected",
			content: "# $env:NODE_EXTRA_CA_CERTS = 'old'\n",
			want:    false,
		},
		// N4 负样本：读取/检查/后缀变量名不应误报为赋值
		{
			name:    "Write-Host read (not assignment)",
			content: `Write-Host "$env:NODE_EXTRA_CA_CERTS"`,
			want:    false,
		},
		{
			name:    "if comparison (not assignment)",
			content: "if ($null -eq $env:NODE_EXTRA_CA_CERTS) { Write-Host 'unset' }",
			want:    false,
		},
		{
			name:    "suffix variable name NODE_EXTRA_CA_CERTS_BACKUP",
			content: "$env:NODE_EXTRA_CA_CERTS_BACKUP = 'backup'\n",
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pwshProfileHasNodeCAVarOutsideMCCBlock(tt.content)
			if got != tt.want {
				t.Errorf("pwshProfileHasNodeCAVarOutsideMCCBlock(%q) = %v, want %v",
					tt.content, got, tt.want)
			}
		})
	}
}

func TestWritePwshProfileNodeCA_UserCustomValue_NotOverwritten(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")
	withPwshHooks(t, home)

	// Pre-write a user-custom NODE_EXTRA_CA_CERTS in pwsh 7 profile
	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	writeFile(t, profile, "$env:NODE_EXTRA_CA_CERTS = 'C:\\user\\custom\\ca.crt'\n")

	a := &osEnvAdapter{}
	err := a.writePwshProfileNodeCA(caPath)
	if !errors.Is(err, ErrUserCustomValue) {
		t.Fatalf("expected ErrUserCustomValue, got: %v", err)
	}

	// Verify the user's custom value is untouched
	content, _ := os.ReadFile(profile)
	if !contains(string(content), "C:\\user\\custom\\ca.crt") {
		t.Error("user custom value should be preserved")
	}
	if strings.Contains(string(content), pwshProfileMarkerBegin) {
		t.Error("mcc block should NOT be added when user custom value exists")
	}
}

func TestWritePOSIXProfileNodeCA_UserCustomValue_NotOverwritten(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")

	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")

	// Pre-write a user-custom NODE_EXTRA_CA_CERTS in .bashrc
	bashrc := filepath.Join(home, ".bashrc")
	writeFile(t, bashrc, "export NODE_EXTRA_CA_CERTS='/user/custom/ca.crt'\n")

	a := &osEnvAdapter{}
	err := a.writePOSIXProfileNodeCA(caPath)
	if !errors.Is(err, ErrUserCustomValue) {
		t.Fatalf("expected ErrUserCustomValue, got: %v", err)
	}

	// Verify the user's custom value is untouched
	content, _ := os.ReadFile(bashrc)
	if !contains(string(content), "/user/custom/ca.crt") {
		t.Error("user custom value should be preserved")
	}
	if strings.Contains(string(content), posixCABlockBegin) {
		t.Error("mcc block should NOT be added when user custom value exists")
	}
}

// --- N1: Windows/macOS main path error handling ---

func TestPersistNodeCACert_Windows_SetxSuccess_ProfileUserCustom_ReturnsUserCustom(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")

	// setx succeeds
	origSetx := setxEnvVar
	setxEnvVar = func(key, value string) error { return nil }
	t.Cleanup(func() { setxEnvVar = origSetx })

	// pwsh profile has user custom value
	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	writeFile(t, profile, "$env:NODE_EXTRA_CA_CERTS = 'C:\\user\\custom\\ca.crt'\n")
	withPwshHooks(t, home)

	a := &osEnvAdapter{}
	err := a.persistNodeCACertWindows(caPath)
	if !errors.Is(err, ErrUserCustomValue) {
		t.Fatalf("expected errors.Is(err, ErrUserCustomValue), got: %v", err)
	}
	// 关键：ErrUserCustomValue 不应被包装为 ErrPartialSuccess
	if errors.Is(err, ErrPartialSuccess) {
		t.Error("ErrUserCustomValue must NOT be wrapped in ErrPartialSuccess")
	}
}

func TestPersistNodeCACert_Windows_SetxSuccess_ProfileFails_ReturnsPartial(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")

	// setx succeeds
	origSetx := setxEnvVar
	setxEnvVar = func(key, value string) error { return nil }
	t.Cleanup(func() { setxEnvVar = origSetx })

	// 注入 WriteFile 故障：readProfile 通过（profile 不存在、父链有效），setx 成功，
	// 但 writeFileSync 失败 → partial。用 hook 而非 file-as-parent，因为后者现在
	// 会被 validateParentChain 在 scan 阶段挡住（见 FileAsParent_NoSetx）。
	origWrite := writeFileSync
	writeFileSync = func(path string, data []byte, perm os.FileMode) error {
		return errors.New("simulated write failure")
	}
	t.Cleanup(func() { writeFileSync = origWrite })

	withPwshHooks(t, home)

	a := &osEnvAdapter{}
	err := a.persistNodeCACertWindows(caPath)
	if !errors.Is(err, ErrPartialSuccess) {
		t.Fatalf("expected ErrPartialSuccess when profile write fails, got: %v", err)
	}
}

// F-1: 父路径是文件（Windows ERROR_PATH_NOT_FOUND / Linux ENOTDIR）时，readProfile
// 的 validateParentChain 必须在 setx 前挡住，不调用 setx。跨平台覆盖两条路径：
// Linux 上 ReadFile 返 ENOTDIR（非 NotExist 直接上抛）；Windows 上 ReadFile 返
// ERROR_PATH_NOT_FOUND（IsNotExist=true，validateParentChain 检测到祖先是文件挡住）。
func TestPersistNodeCACert_Windows_FileAsParent_NoSetx(t *testing.T) {
	caPath := writeFile(t, filepath.Join(t.TempDir(), "ca.crt"), "cert")

	// fakeHome 的父路径是文件 → profile 父链无效
	blocked := filepath.Join(t.TempDir(), "file-as-dir")
	writeFile(t, blocked, "x")
	fakeHome := filepath.Join(blocked, "home")
	withPwshHooks(t, fakeHome)

	calls := 0
	origSetx := setxEnvVar
	setxEnvVar = func(k, v string) error { calls++; return nil }
	t.Cleanup(func() { setxEnvVar = origSetx })

	a := &osEnvAdapter{}
	err := a.persistNodeCACertWindows(caPath)
	if err == nil {
		t.Error("expected error from scan (file-as-parent)")
	}
	if calls != 0 {
		t.Errorf("expected setx 0 calls (file-as-parent blocks scan), got %d", calls)
	}
}

func TestPersistNodeCACert_Windows_BothSucceed_ReturnsNil(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")

	origSetx := setxEnvVar
	setxEnvVar = func(key, value string) error { return nil }
	t.Cleanup(func() { setxEnvVar = origSetx })

	withPwshHooks(t, home)

	a := &osEnvAdapter{}
	err := a.persistNodeCACertWindows(caPath)
	if err != nil {
		t.Fatalf("expected nil when both succeed, got: %v", err)
	}
}

func TestPersistNodeCACert_Darwin_LaunchctlSuccess_ProfileFails_ReturnsPartial(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")

	// launchctl succeeds
	origLaunchctl := launchctlSetenv
	origHas := hasLaunchctl
	launchctlSetenv = func(key, value string) error { return nil }
	hasLaunchctl = func() bool { return true }
	t.Cleanup(func() {
		launchctlSetenv = origLaunchctl
		hasLaunchctl = origHas
	})

	// 注入 WriteFile 故障：readProfile 通过（profile 不存在、父链有效），
	// launchctl 成功，writeFileSync 失败 → partial。
	origWrite := writeFileSync
	writeFileSync = func(path string, data []byte, perm os.FileMode) error {
		return errors.New("simulated write failure")
	}
	t.Cleanup(func() { writeFileSync = origWrite })

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")

	a := &osEnvAdapter{}
	err := a.persistNodeCACertDarwin(caPath)
	if !errors.Is(err, ErrPartialSuccess) {
		t.Fatalf("expected ErrPartialSuccess when profile fails, got: %v", err)
	}
}

// F-2: 多 profile 部分写入失败必须返回 ErrPartialSuccess，不能被首个成功掩盖。
func TestWritePwshProfileNodeCA_PartialFailure_ReturnsPartial(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")

	// withPwshHooks 默认两个候选：pwsh 7 + Windows PowerShell 5.1
	profile2 := filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1")
	withPwshHooks(t, home)

	// 注入：profile1（pwsh 7）写成功，profile2（5.1）写失败 → partial。
	// 用 hook 精确控制，避免 file-as-parent 被 validateParentChain 在 scan 挡住。
	origWrite := writeFileSync
	writeFileSync = func(path string, data []byte, perm os.FileMode) error {
		if path == profile2 {
			return errors.New("simulated write failure for profile2")
		}
		return os.WriteFile(path, data, perm)
	}
	t.Cleanup(func() { writeFileSync = origWrite })

	a := &osEnvAdapter{}
	err := a.writePwshProfileNodeCA(caPath)
	if !errors.Is(err, ErrPartialSuccess) {
		t.Fatalf("expected ErrPartialSuccess when some profiles fail, got: %v", err)
	}
}

// F-1: profile 存在用户自定义值时，setx 不应被调用（先检查再覆盖）。
func TestPersistNodeCACert_Windows_ProfileUserCustom_SkipsSetx(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")

	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	writeFile(t, profile, "$env:NODE_EXTRA_CA_CERTS = 'C:\\user\\custom\\ca.crt'\n")
	withPwshHooks(t, home)

	setxCalled := false
	origSetx := setxEnvVar
	setxEnvVar = func(key, value string) error {
		setxCalled = true
		return nil
	}
	t.Cleanup(func() { setxEnvVar = origSetx })

	a := &osEnvAdapter{}
	err := a.persistNodeCACertWindows(caPath)
	if !errors.Is(err, ErrUserCustomValue) {
		t.Fatalf("expected ErrUserCustomValue, got: %v", err)
	}
	if setxCalled {
		t.Error("setx must NOT be called when pwsh profile has user custom value")
	}
}

// F-1 (Darwin): POSIX profile 存在用户自定义值时，launchctl 不应被调用。
func TestPersistNodeCACert_Darwin_ProfileUserCustom_SkipsLaunchctl(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")

	bashrc := filepath.Join(home, ".bashrc")
	writeFile(t, bashrc, "export NODE_EXTRA_CA_CERTS='/user/custom/ca.crt'\n")

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")

	launchctlCalled := false
	origLaunchctl := launchctlSetenv
	origHas := hasLaunchctl
	launchctlSetenv = func(key, value string) error {
		launchctlCalled = true
		return nil
	}
	hasLaunchctl = func() bool { return true }
	t.Cleanup(func() {
		launchctlSetenv = origLaunchctl
		hasLaunchctl = origHas
	})

	a := &osEnvAdapter{}
	err := a.persistNodeCACertDarwin(caPath)
	if !errors.Is(err, ErrUserCustomValue) {
		t.Fatalf("expected ErrUserCustomValue, got: %v", err)
	}
	if launchctlCalled {
		t.Error("launchctl must NOT be called when POSIX profile has user custom value")
	}
}

// P2-1: 路径含 PowerShell 注入字符（$()、反引号、撇号）必须渲染为单引号字面量，
// 不能放进双引号（双引号会展开 $() 和反引号）。
func TestWritePwshProfileNodeCA_PathInjection_IsSingleQuoteLiteral(t *testing.T) {
	tests := []struct {
		name       string
		insideHome bool
		component  string
	}{
		{"home内含子表达式", true, "data$(Write-Output PWNED)"},
		{"home外含子表达式", false, "data$(Write-Output PWNED)"},
		{"home内含反引号", true, "dir`whoami"},
		{"home内含撇号", true, "dir'name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			var caPath string
			if tt.insideHome {
				caPath = filepath.Join(home, tt.component, "ca.crt")
			} else {
				caPath = filepath.Join(t.TempDir(), tt.component, "ca.crt")
			}
			withPwshHooks(t, home)

			a := &osEnvAdapter{}
			if err := a.writePwshProfileNodeCA(caPath); err != nil {
				t.Fatalf("writePwshProfileNodeCA: %v", err)
			}
			profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
			content, _ := os.ReadFile(profile)
			body := string(content)

			found := false
			for _, line := range strings.Split(body, "\n") {
				ts := strings.TrimSpace(line)
				if !strings.HasPrefix(ts, "$mccCa = ") {
					continue
				}
				found = true
				if strings.HasPrefix(ts, "$mccCa = \"") {
					t.Errorf("$mccCa uses double quotes (injectable): %s", ts)
				}
			}
			if !found {
				t.Fatalf("missing $mccCa assignment in profile:\n%s", body)
			}
		})
	}
}

// P2-1: 路径含 CR/LF 应被拒绝（防止换行断开字面量注入新命令）。
func TestWritePwshProfileNodeCA_NewlineRejected(t *testing.T) {
	home := t.TempDir()
	withPwshHooks(t, home)

	badPath := filepath.Join(home, "dir\nmalicious", "ca.crt")
	a := &osEnvAdapter{}
	err := a.writePwshProfileNodeCA(badPath)
	if err == nil {
		t.Fatal("expected error for path containing newline, got nil")
	}
}

// --- N2: User custom value does not write marker ---

func TestTryPersistNodeCA_UserCustomValue_DoesNotWriteMarker(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")

	env := &mockEnv{nodeCAErr: ErrUserCustomValue}
	e := New(dir, caPath, "en", WithEnvAdapter(env))

	r := e.tryPersistNodeCA()
	if !r.Attempted || r.Success {
		t.Errorf("expected Attempted=true Success=false, got %+v", r)
	}
	if !errors.Is(r.Err, ErrUserCustomValue) {
		t.Errorf("expected ErrUserCustomValue, got: %v", r.Err)
	}
	if hasNodeCAMarker(dir, caPath) {
		t.Error("marker should NOT be written for ErrUserCustomValue (user may clear custom value later)")
	}
}

func TestTryPersistNodeCA_UserCustomValue_StateHashStable(t *testing.T) {
	// Verify that consecutive ErrUserCustomValue results produce the same stateHash,
	// so shouldSuppress works to avoid repeated warnings.
	r := Result{
		SelectedMode: ModeTransparent,
		HostsResult:  StepResult{Success: true},
		TrustResult:  StepResult{Success: true},
		EnvResult:    StepResult{Attempted: true, Success: true},
		NodeCAResult: StepResult{Attempted: true, Success: false, Err: ErrUserCustomValue},
	}
	h1 := stateHash(r)
	h2 := stateHash(r)
	if h1 != h2 {
		t.Errorf("stateHash should be stable for same ErrUserCustomValue result: %s != %s", h1, h2)
	}

	// Also verify it differs from a different error
	r2 := r
	r2.NodeCAResult = StepResult{Attempted: true, Success: false, Err: &testError{"other error"}}
	h3 := stateHash(r2)
	if h1 == h3 {
		t.Error("stateHash should differ between ErrUserCustomValue and other errors")
	}
}

// --- N3: Two-phase atomicity ---

func TestWritePwshProfileNodeCA_SecondCandidateUserCustom_NeitherWritten(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")

	// Inject two candidates; second has user custom value
	pwsh7Profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	winPs5Profile := filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1")
	writeFile(t, winPs5Profile, "$env:NODE_EXTRA_CA_CERTS = 'C:\\user\\custom\\ca.crt'\n")

	origDetected := pwshDetected
	origCandidates := pwshProfileCandidates
	pwshDetected = func() bool { return true }
	pwshProfileCandidates = func(h string) []string {
		return []string{pwsh7Profile, winPs5Profile}
	}
	t.Cleanup(func() {
		pwshDetected = origDetected
		pwshProfileCandidates = origCandidates
	})
	t.Setenv("USERPROFILE", home)

	a := &osEnvAdapter{}
	err := a.writePwshProfileNodeCA(caPath)
	if !errors.Is(err, ErrUserCustomValue) {
		t.Fatalf("expected ErrUserCustomValue, got: %v", err)
	}

	// 关键：第一个候选也不应被写入 mcc 块
	content7, _ := os.ReadFile(pwsh7Profile)
	if strings.Contains(string(content7), pwshProfileMarkerBegin) {
		t.Error("pwsh 7 profile should NOT have mcc block when second candidate has user custom value")
	}
	content5, _ := os.ReadFile(winPs5Profile)
	if strings.Contains(string(content5), pwshProfileMarkerBegin) {
		t.Error("Windows PowerShell 5.1 profile should NOT have mcc block")
	}
	// 用户自定义值应保持原样
	if !contains(string(content5), "C:\\user\\custom\\ca.crt") {
		t.Error("user custom value in 5.1 profile should be preserved")
	}
}
