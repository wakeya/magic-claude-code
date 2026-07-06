# Node CA Persistence Correctness Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist only absolute MCC CA paths while preserving any non-MCC-managed `NODE_EXTRA_CA_CERTS` value.

**Architecture:** `Executor.tryPersistNodeCA` owns path normalization and overwrite policy. `EnvAdapter` exposes a read-only lookup implemented with HKCU on Windows, process/launchctl state on macOS, and the process environment elsewhere; the existing user-bound marker authorizes migration from an older MCC-managed path.

**Tech Stack:** Go 1.26, `path/filepath`, `golang.org/x/sys/windows/registry`, build tags, Go tests, Make, npm.

---

## File Structure

- Modify `internal/bootstrap/bootstrap.go`: extend `EnvAdapter`; normalize paths; enforce custom-value preservation; expose safe prior-marker path evidence.
- Modify `internal/bootstrap/adapters.go`: implement the adapter lookup method through an injectable OS hook.
- Create `internal/bootstrap/node_ca_lookup_windows.go`: read `HKCU\Environment\NODE_EXTRA_CA_CERTS`.
- Create `internal/bootstrap/node_ca_lookup_darwin.go`: read the current environment and `launchctl getenv`.
- Create `internal/bootstrap/node_ca_lookup_other.go`: read the process environment on non-Windows, non-macOS systems.
- Modify `internal/bootstrap/bootstrap_test.go`: add red-green coverage and update the mock adapter.
- Modify the bilingual feature review notes to archive resolutions and the remaining native-platform gate.

### Task 1: Normalize The Persisted CA Path

**Files:**
- Modify: `internal/bootstrap/bootstrap.go:261-295`
- Test: `internal/bootstrap/bootstrap_test.go:1420-1540`

- [ ] **Step 1: Write the failing relative-path test**

```go
func TestTryPersistNodeCA_RelativePath_UsesAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	absCA := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")
	relCA, err := filepath.Rel(".", absCA)
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
```

- [ ] **Step 2: Run the test and verify RED**

Run: `go test ./internal/bootstrap -run '^TestTryPersistNodeCA_RelativePath_UsesAbsolutePath$' -count=1`

Expected: FAIL because `PersistNodeCACert` receives the relative path.

- [ ] **Step 3: Implement absolute-path normalization**

Resolve once at the start of `tryPersistNodeCA` and use `caCertPath` for stat, marker lookup, persistence, and marker writes:

```go
caCertPath, err := filepath.Abs(e.caCertPath)
if err != nil {
	return StepResult{Attempted: true, Success: false, Err: fmt.Errorf("absolute CA cert path: %w", err)}
}
if _, err := os.Stat(caCertPath); err != nil {
	return StepResult{Attempted: true, Success: false, Err: err}
}
if hasNodeCAMarker(e.dataDir, caCertPath) {
	return StepResult{Success: true}
}
```

- [ ] **Step 4: Run focused and existing marker tests**

Run: `go test ./internal/bootstrap -run '^(TestTryPersistNodeCA_RelativePath_UsesAbsolutePath|TestTryPersistNodeCA_CertExists_CallsPersistAndWritesMarker|TestTryPersistNodeCA_MarkerStaleCertChanged_Repersists)$' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the isolated path fix**

```bash
git add internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go
git commit -m "fix(bootstrap): persist absolute Node CA path"
```

### Task 2: Define Custom-Value Policy At The Executor Boundary

**Files:**
- Modify: `internal/bootstrap/bootstrap.go:90-96,261-409`
- Modify: `internal/bootstrap/bootstrap_test.go:30-44,1420-1600`

- [ ] **Step 1: Extend the mock and write four failing policy tests**

Extend `mockEnv`:

```go
type mockEnv struct {
	err             error
	nodeCAErr       error
	caCertArg       string
	nodeCAValue     string
	nodeCAValueSet  bool
	nodeCALookupErr error
	nodeCALookupCalls int
}

func (m *mockEnv) LookupNodeCACert() (string, bool, error) {
	m.nodeCALookupCalls++
	return m.nodeCAValue, m.nodeCAValueSet, m.nodeCALookupErr
}
```

Add tests with these assertions:

```go
func TestTryPersistNodeCA_CustomPersistedValue_IsPreserved(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")
	setPrivileged(t, false)
	env := &mockEnv{nodeCAValue: filepath.Join(dir, "corporate-ca.pem"), nodeCAValueSet: true}
	r := New(dir, caPath, "en", WithEnvAdapter(env)).tryPersistNodeCA()
	if !errors.Is(r.Err, ErrUserCustomValue) || env.caCertArg != "" || env.nodeCALookupCalls != 1 {
		t.Fatalf("custom value was not preserved: result=%+v arg=%q", r, env.caCertArg)
	}
}

func TestTryPersistNodeCA_ExistingDesiredValue_RepairsProfiles(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")
	setPrivileged(t, false)
	env := &mockEnv{nodeCAValue: caPath, nodeCAValueSet: true}
	r := New(dir, caPath, "en", WithEnvAdapter(env)).tryPersistNodeCA()
	if !r.Success || env.caCertArg != caPath || env.nodeCALookupCalls != 1 {
		t.Fatalf("existing value did not repair profiles: result=%+v arg=%q", r, env.caCertArg)
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
		t.Fatalf("managed value did not migrate: result=%+v arg=%q", r, env.caCertArg)
	}
}

func TestTryPersistNodeCA_LookupFailure_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	caPath := writeFile(t, filepath.Join(dir, "ca.crt"), "cert")
	setPrivileged(t, false)
	env := &mockEnv{nodeCALookupErr: errors.New("lookup failed")}
	r := New(dir, caPath, "en", WithEnvAdapter(env)).tryPersistNodeCA()
	if r.Err == nil || !strings.Contains(r.Err.Error(), "lookup failed") || env.caCertArg != "" || env.nodeCALookupCalls != 1 {
		t.Fatalf("lookup did not fail closed: result=%+v arg=%q", r, env.caCertArg)
	}
}
```

- [ ] **Step 2: Run the tests and verify RED**

Run: `go test ./internal/bootstrap -run '^TestTryPersistNodeCA_(CustomPersistedValue_IsPreserved|ExistingDesiredValue_RepairsProfiles|PreviousManagedValue_IsMigrated|LookupFailure_FailsClosed)$' -count=1`

Expected: build or behavioral failure because lookup policy is absent.

- [ ] **Step 3: Extend `EnvAdapter` and add marker/path helpers**

```go
type EnvAdapter interface {
	PersistRoot(rootDir string) error
	LookupNodeCACert() (value string, exists bool, err error)
	PersistNodeCACert(caCertPath string) error
}
```

Add the minimal production method needed to keep every implementation compiling; Task 3 replaces its process-only lookup with the OS-specific hook:

```go
func (a *osEnvAdapter) LookupNodeCACert() (string, bool, error) {
	value, exists := os.LookupEnv("NODE_EXTRA_CA_CERTS")
	return value, exists && value != "", nil
}
```

```go
func previousManagedNodeCAPath(dataDir string) (string, bool) {
	if dataDir == "" {
		return "", false
	}
	markerPath := filepath.Join(dataDir, nodeCAMarkerName)
	if err := isSafeForWrite(markerPath); err != nil {
		return "", false
	}
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return "", false
	}
	var m nodeCAMarker
	if json.Unmarshal(raw, &m) != nil || m.Fingerprint == "" || m.CertPath == "" || !nodeCAMarkerUserMatches(m) {
		return "", false
	}
	return m.CertPath, true
}

func nodeCAPathsEqual(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
```

- [ ] **Step 4: Enforce lookup policy before mutation**

```go
existing, exists, err := e.env.LookupNodeCACert()
if err != nil {
	return StepResult{Attempted: true, Success: false, Err: fmt.Errorf("lookup persisted NODE_EXTRA_CA_CERTS: %w", err)}
}
if exists && existing != "" && !nodeCAPathsEqual(existing, caCertPath) {
	previous, managed := previousManagedNodeCAPath(e.dataDir)
	if !managed || !nodeCAPathsEqual(existing, previous) {
		return StepResult{Attempted: true, Success: false, Err: ErrUserCustomValue}
	}
}
```

- [ ] **Step 5: Run focused and full bootstrap tests**

```bash
go test ./internal/bootstrap -run '^TestTryPersistNodeCA_' -count=1
go test ./internal/bootstrap -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit executor policy**

```bash
git add internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go
git commit -m "fix(bootstrap): preserve custom Node CA environment"
```

### Task 3: Implement Platform-Specific Read-Only Lookup

**Files:**
- Modify: `internal/bootstrap/adapters.go:366-426`
- Create: `internal/bootstrap/node_ca_lookup_windows.go`
- Create: `internal/bootstrap/node_ca_lookup_darwin.go`
- Create: `internal/bootstrap/node_ca_lookup_other.go`
- Test: `internal/bootstrap/bootstrap_test.go`

- [ ] **Step 1: Write the failing OS-hook dispatch test**

```go
func TestOSEnvAdapterLookupNodeCACert_UsesOSLookup(t *testing.T) {
	original := lookupPersistedNodeCACert
	lookupPersistedNodeCACert = func() (string, bool, error) {
		return "/corporate/ca.pem", true, nil
	}
	t.Cleanup(func() { lookupPersistedNodeCACert = original })
	got, exists, err := (&osEnvAdapter{}).LookupNodeCACert()
	if err != nil || !exists || got != "/corporate/ca.pem" {
		t.Fatalf("LookupNodeCACert() = (%q, %v, %v)", got, exists, err)
	}
}
```

- [ ] **Step 2: Run the test and verify RED**

Run: `go test ./internal/bootstrap -run '^TestOSEnvAdapterLookupNodeCACert_UsesOSLookup$' -count=1`

Expected: build failure because the `lookupPersistedNodeCACert` hook is undefined.

- [ ] **Step 3: Add the common adapter hook**

```go
var lookupPersistedNodeCACert = lookupPersistedNodeCACertOS

func (a *osEnvAdapter) LookupNodeCACert() (string, bool, error) {
	return lookupPersistedNodeCACert()
}
```

- [ ] **Step 4: Add `node_ca_lookup_other.go`**

```go
//go:build !windows && !darwin

package bootstrap

import "os"

func lookupPersistedNodeCACertOS() (string, bool, error) {
	value, exists := os.LookupEnv("NODE_EXTRA_CA_CERTS")
	return value, exists && value != "", nil
}
```

- [ ] **Step 5: Add `node_ca_lookup_windows.go`**

```go
//go:build windows

package bootstrap

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

func lookupPersistedNodeCACertOS() (string, bool, error) {
	if value, exists := os.LookupEnv("NODE_EXTRA_CA_CERTS"); exists && value != "" {
		return value, true, nil
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("open HKCU Environment: %w", err)
	}
	defer key.Close()
	value, _, err := key.GetStringValue("NODE_EXTRA_CA_CERTS")
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read HKCU Environment NODE_EXTRA_CA_CERTS: %w", err)
	}
	return value, value != "", nil
}
```

- [ ] **Step 6: Add `node_ca_lookup_darwin.go`**

```go
//go:build darwin

package bootstrap

import (
	"fmt"
	"os"
	"strings"
)

func lookupPersistedNodeCACertOS() (string, bool, error) {
	if value, exists := os.LookupEnv("NODE_EXTRA_CA_CERTS"); exists && value != "" {
		return value, true, nil
	}
	if !hasLaunchctl() {
		return "", false, nil
	}
	out, err := execWithTimeout("launchctl", "getenv", "NODE_EXTRA_CA_CERTS")
	if err != nil {
		return "", false, fmt.Errorf("launchctl getenv NODE_EXTRA_CA_CERTS: %w: %s", err, decodeCmdOutput(out))
	}
	value := strings.TrimRight(string(out), "\r\n")
	return value, value != "", nil
}
```

- [ ] **Step 7: Run lookup tests and cross-compilation**

```bash
go test ./internal/bootstrap -run '^(TestOSEnvAdapterLookupNodeCACert_UsesOSLookup|TestTryPersistNodeCA_)' -count=1
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/mcc-bootstrap-windows-amd64.test.exe ./internal/bootstrap
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/mcc-bootstrap-windows-arm64.test.exe ./internal/bootstrap
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/mcc-bootstrap-darwin-amd64.test ./internal/bootstrap
```

Expected: all commands exit 0.

- [ ] **Step 8: Commit platform lookup**

```bash
git add internal/bootstrap/adapters.go internal/bootstrap/bootstrap_test.go internal/bootstrap/node_ca_lookup_windows.go internal/bootstrap/node_ca_lookup_darwin.go internal/bootstrap/node_ca_lookup_other.go
git commit -m "feat(bootstrap): inspect persisted Node CA value"
```

### Task 4: Verify And Update Review Records

**Files:**
- Modify: `sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/review-notes.md`
- Modify: `sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/review-notes_ZH.md`

- [ ] **Step 1: Format and run backend verification**

```bash
gofmt -w internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go internal/bootstrap/adapters.go internal/bootstrap/node_ca_lookup_windows.go internal/bootstrap/node_ca_lookup_darwin.go internal/bootstrap/node_ca_lookup_other.go
go test ./internal/bootstrap -count=1
go test -race ./internal/bootstrap -count=1
go vet ./internal/bootstrap
make test
go vet ./...
go mod tidy -diff
go mod verify
```

Expected: all commands exit 0.

- [ ] **Step 2: Run frontend verification**

```bash
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

Expected: both commands exit 0 and the frontend test summary has 0 failures.

- [ ] **Step 3: Build all six release targets**

```bash
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
	goos=${target%/*}
	goarch=${target#*/}
	suffix=
	[ "$goos" = windows ] && suffix=.exe
	GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
		go build -trimpath -ldflags='-s -w -X magic-claude-code/internal/version.Version=v0.0.0-review' \
		-o "/tmp/mcc-${goos}-${goarch}${suffix}" ./cmd/server
done
```

Expected: all six builds exit 0.

- [ ] **Step 4: Update bilingual review notes**

Append aligned follow-up sections recording that relative paths are normalized, non-MCC values are preserved, prior marker-owned paths migrate, and all automated checks pass. Keep native Windows tests and the Orca/Node end-to-end flow explicitly open as the final release gate.

- [ ] **Step 5: Verify and commit the review records**

```bash
git diff --check
git status --short
git diff --stat
git add sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/review-notes.md sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/review-notes_ZH.md
git commit -m "docs(review): archive Node CA persistence fixes"
```

Do not mark the feature release-ready until native Windows tests and the Orca/Node end-to-end flow are executed and archived.
