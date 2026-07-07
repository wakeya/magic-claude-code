# Missing Windows Root Validation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reject profile paths whose filesystem volume/share root does not exist before `setx` or `launchctl` can mutate environment state.

**Architecture:** Keep `validateParentChain` as the production wrapper and move traversal into `validateParentChainWithStat`, which receives a stat function for deterministic Linux testing. Root traversal becomes fail-closed when the root itself is absent. A Windows-only integration test independently exercises an unused drive root.

**Tech Stack:** Go 1.26 standard library, `testing`, build tags, Git.

---

### Task 1: Add a failing cross-platform root test

**Files:**
- Modify: `internal/bootstrap/bootstrap_test.go`

- [ ] **Step 1: Add the failing test**

```go
func TestValidateParentChain_MissingRoot_ReturnsError(t *testing.T) {
	statMissing := func(path string) (os.FileInfo, error) {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
	}
	err := validateParentChainWithStat(filepath.Join(string(filepath.Separator), "missing", "profile"), statMissing)
	if err == nil {
		t.Fatal("expected missing filesystem root to fail closed")
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test ./internal/bootstrap -run '^TestValidateParentChain_MissingRoot_ReturnsError$' -count=1`

Expected: build failure because `validateParentChainWithStat` is not defined.

### Task 2: Implement fail-closed root traversal

**Files:**
- Modify: `internal/bootstrap/adapters.go:556-580`

- [ ] **Step 1: Add the minimal implementation**

```go
func validateParentChain(profile string) error {
	return validateParentChainWithStat(profile, os.Stat)
}

func validateParentChainWithStat(profile string, stat func(string) (os.FileInfo, error)) error {
	dir := filepath.Dir(profile)
	for {
		fi, err := stat(dir)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("parent %s is not a directory", dir)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat parent %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("filesystem root %s does not exist: %w", dir, err)
		}
		dir = parent
	}
}
```

- [ ] **Step 2: Run the focused test and verify GREEN**

Run: `go test ./internal/bootstrap -run '^TestValidateParentChain_MissingRoot_ReturnsError$' -count=1`

Expected: PASS.

### Task 3: Add native Windows integration coverage

**Files:**
- Create: `internal/bootstrap/parent_chain_windows_test.go`

- [ ] **Step 1: Add the Windows-only test**

```go
//go:build windows

package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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
	setxEnvVar = func(string, string) error { calls++; return nil }
	t.Cleanup(func() { setxEnvVar = previous })

	err := (&osEnvAdapter{}).persistNodeCACertWindows(filepath.Join(t.TempDir(), "ca.crt"))
	if err == nil {
		t.Fatal("expected missing volume root to fail profile scan")
	}
	if calls != 0 {
		t.Fatalf("setx called %d times; want 0", calls)
	}
}
```

- [ ] **Step 2: Cross-compile Windows tests**

Run: `GOOS=windows GOARCH=amd64 go test -c -o /tmp/mcc-bootstrap-windows.test.exe ./internal/bootstrap`

Expected: exit 0.

### Task 4: Verify, archive, commit, and push

**Files:**
- Modify: `sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/review-notes.md`
- Modify: `sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/review-notes_ZH.md`

- [ ] **Step 1: Run verification**

Run:

```bash
go test ./internal/bootstrap -count=1
go test -race ./internal/bootstrap -count=1
go test ./... -count=1
go vet ./internal/bootstrap
GOOS=windows GOARCH=amd64 go test -c -o /tmp/mcc-bootstrap-windows.test.exe ./internal/bootstrap
GOOS=darwin GOARCH=amd64 go test -c -o /tmp/mcc-bootstrap-darwin.test ./internal/bootstrap
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 2: Update bilingual review notes**

Record that the absent-root branch now returns an error, focused/full/race verification passes, Windows/macOS tests cross-compile, and native Windows execution remains the user's final platform check.

- [ ] **Step 3: Commit and push**

```bash
git add internal/bootstrap/adapters.go internal/bootstrap/bootstrap_test.go internal/bootstrap/parent_chain_windows_test.go \
  sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/review-notes.md \
  sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/review-notes_ZH.md \
  sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/implementation-plan-missing-windows-root.md
git commit -m "fix(bootstrap): reject missing profile filesystem roots"
git push origin feat/node-extra-ca-certs-auto-setup
```

Expected: local and remote branch heads match the new commit.
