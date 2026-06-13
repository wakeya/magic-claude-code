# Auto-Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Feature Spec:** `sdd-docs/features/2026-06-13-auto-update/spec.md` | `spec_ZH.md`

**Goal:** Add automatic version detection and self-update capability — checks GitHub/GitCode for new releases, downloads the correct platform binary, verifies SHA256, replaces the running binary, and triggers graceful restart.

**Architecture:** New `internal/updater/` package encapsulates all update logic (source abstraction, version checking, download, verify, apply). Admin server gains `/api/update/*` endpoints. Version is injected at build time via ldflags. Frontend shows update notification in the header. On startup, a background goroutine checks for updates non-blocking. Docker environments detect `/.dockerenv` and disable self-update (guide users to image-based updates instead).

**Tech Stack:** Go stdlib (`net/http`, `archive/tar`, `compress/gzip`, `crypto/sha256`, `runtime`, `os`), Vue 3 + Tailwind (frontend), GitHub Releases API + GitCode API

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/version/version.go` | Build-time version variable |
| `internal/updater/source.go` | `ReleaseSource` interface + `GitHubSource`, `GitCodeSource` |
| `internal/updater/source_test.go` | Source layer tests |
| `internal/updater/updater.go` | `Updater` struct: Check, Download, Verify, Apply |
| `internal/updater/updater_test.go` | Core logic tests |
| `internal/admin/update_handler.go` | HTTP handlers: `GET /api/update/check`, `POST /api/update/apply` |
| `internal/admin/update_handler_test.go` | Handler tests |
| `internal/admin/server.go` | Wire updater + routes |
| `internal/admin/handler.go` | Add version to status response |
| `cmd/server/main.go` | Instantiate updater, startup auto-check |
| `.github/workflows/release.yml` | Inject version via ldflags |
| `.gitlab-ci.yml` | Inject version via ldflags |
| `internal/frontend/src/composables/useApi.ts` | Add update API methods |
| `internal/frontend/src/composables/useI18n.ts` | Add update-related i18n strings |
| `internal/frontend/src/components/AppHeader.vue` | Update notification badge |

---

### Task 1: Version Package & CI Injection

**Files:**
- Create: `internal/version/version.go`
- Modify: `.github/workflows/release.yml`
- Modify: `.gitlab-ci.yml`
- Modify: `internal/admin/handler.go` (add version to status response)

- [ ] **Step 1: Create version package**

```go
// internal/version/version.go
package version

// Version is set at build time via -ldflags "-X magic-claude-code/internal/version.Version=v0.1.0".
// Defaults to "dev" for local builds.
var Version = "dev"
```

- [ ] **Step 2: Add version to admin status response**

In `internal/admin/handler.go`, modify `handleStatus` — add `version` import and field:

```go
import (
	// ...existing imports...
	"magic-claude-code/internal/version"
)
```

In the `json.NewEncoder(w).Encode(map[string]interface{}{...})` call inside `handleStatus`, add:

```go
		"version": version.Version,
```

- [ ] **Step 3: Update GitHub Actions release workflow**

In `.github/workflows/release.yml`, find the `go build` line inside `build_target()` and add version injection:

```bash
            GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
              go build -trimpath -ldflags="-s -w -X magic-claude-code/internal/version.Version=${RELEASE_TAG}" \
              -o "${package_dir}/${BINARY_NAME}${exe_suffix}" ./cmd/server
```

- [ ] **Step 4: Update GitLab CI release pipeline**

In `.gitlab-ci.yml`, find the `GOOS=... go build` line inside `build_target()` and add version injection:

```bash
        GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
          go build -trimpath -ldflags="-s -w -X magic-claude-code/internal/version.Version=${CI_COMMIT_TAG}" \
          -o "${package_dir}/${BINARY_NAME}${exe_suffix}" ./cmd/server
```

- [ ] **Step 5: Verify build and status endpoint**

Run:
```bash
go build -ldflags="-X magic-claude-code/internal/version.Version=v0.0.1-test" ./cmd/server && echo "OK"
```
Expected: `OK`

- [ ] **Step 6: Commit**

```bash
git add internal/version/version.go internal/admin/handler.go .github/workflows/release.yml .gitlab-ci.yml
git commit -m "feat: add build-time version injection via ldflags"
```

---

### Task 2: Updater Package — Source Layer

**Files:**
- Create: `internal/updater/source.go`
- Create: `internal/updater/source_test.go`

This task builds the source abstraction for fetching latest release info from GitHub and GitCode.

`★ Design note:` The `ReleaseSource` interface lets us try multiple sources in order (GitHub first, GitCode fallback). Each source maps its native API response into a common `ReleaseInfo` struct.

- [ ] **Step 1: Write the failing test for GitHubSource**

```go
// internal/updater/source_test.go
package updater

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubSource_FetchLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/wakeya/magic-claude-code/releases/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"tag_name": "v0.2.0",
			"html_url": "https://github.com/wakeya/magic-claude-code/releases/tag/v0.2.0",
			"assets": [
				{
					"name": "Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz",
					"browser_download_url": "https://github.com/wakeya/magic-claude-code/releases/download/v0.2.0/Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz"
				},
				{
					"name": "SHA256SUMS.txt",
					"browser_download_url": "https://github.com/wakeya/magic-claude-code/releases/download/v0.2.0/SHA256SUMS.txt"
				}
			]
		}`))
	}))
	defer server.Close()

	src := &GitHubSource{
		owner:    "wakeya",
		repo:     "magic-claude-code",
		baseURL:  server.URL,
	}

	info, err := src.FetchLatestRelease(t.Context(), server.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.TagName != "v0.2.0" {
		t.Errorf("expected tag v0.2.0, got %s", info.TagName)
	}
	if info.HTMLURL != "https://github.com/wakeya/magic-claude-code/releases/tag/v0.2.0" {
		t.Errorf("unexpected html_url: %s", info.HTMLURL)
	}
	if len(info.Assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(info.Assets))
	}

	asset := info.findAsset("Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz")
	if asset == nil {
		t.Fatal("expected to find Linux x86_64 asset")
	}
	expectedURL := "https://github.com/wakeya/magic-claude-code/releases/download/v0.2.0/Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz"
	if asset.DownloadURL != expectedURL {
		t.Errorf("unexpected download URL: %s", asset.DownloadURL)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/updater/ -run TestGitHubSource -v`
Expected: FAIL — package not found / types not defined

- [ ] **Step 3: Implement source.go**

```go
// internal/updater/source.go
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ReleaseAsset represents a downloadable file in a release.
type ReleaseAsset struct {
	Name        string
	DownloadURL string
}

// ReleaseInfo holds parsed release metadata from any source.
type ReleaseInfo struct {
	TagName string
	HTMLURL string
	Assets  []ReleaseAsset
}

// findAsset returns the asset with the given name, or nil if not found.
func (r *ReleaseInfo) findAsset(name string) *ReleaseAsset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

// ReleaseSource abstracts a release-lookup provider.
type ReleaseSource interface {
	Name() string
	FetchLatestRelease(ctx context.Context, client *http.Client) (*ReleaseInfo, error)
	AssetURL(tag, assetName string) string
}

// GitHubSource queries the GitHub Releases API.
type GitHubSource struct {
	owner   string
	repo    string
	baseURL string // overrideable for testing; defaults to "https://api.github.com"
}

func NewGitHubSource(owner, repo string) *GitHubSource {
	return &GitHubSource{owner: owner, repo: repo, baseURL: "https://api.github.com"}
}

func (s *GitHubSource) Name() string { return "github" }

func (s *GitHubSource) FetchLatestRelease(ctx context.Context, client *http.Client) (*ReleaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", s.baseURL, s.owner, s.repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("github: decode response: %w", err)
	}

	info := &ReleaseInfo{
		TagName: body.TagName,
		HTMLURL: body.HTMLURL,
	}
	for _, a := range body.Assets {
		info.Assets = append(info.Assets, ReleaseAsset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
		})
	}
	return info, nil
}

// AssetURL satisfies the ReleaseSource interface.
// GitHub release assets follow a predictable download URL pattern.
func (s *GitHubSource) AssetURL(tag, assetName string) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", s.owner, s.repo, tag, assetName)
}

// GitCodeSource queries the GitCode Releases API for tag detection.
// GitCode (gitcode.com) is a CSDN-operated open-source code platform.
// API: /api/v5/repos/{owner}/{repo}/releases/latest (auth: PRIVATE-TOKEN header)
//
// NOTE: GitCode releases do NOT support custom binary asset uploads.
// Only auto-generated source archives are available. Pre-compiled binaries
// are stored in the repo under dist/release/{tag}/ and downloaded via the
// GitCode raw API endpoint:
//   GET {apiBase}/repos/{owner}/{repo}/raw/dist/release/{tag}/{asset_name}
//
// This source queries the releases API for the latest tag, then constructs
// download URLs for the expected asset names using the raw file API.
type GitCodeSource struct {
	owner   string
	repo    string
	apiBase string // defaults to "https://api.gitcode.com/api/v5"
	token   string // optional PRIVATE-TOKEN for authenticated requests
}

func NewGitCodeSource(owner, repo, token string) *GitCodeSource {
	return &GitCodeSource{
		owner:   owner,
		repo:    repo,
		apiBase: "https://api.gitcode.com/api/v5",
		token:   token,
	}
}

func (s *GitCodeSource) Name() string { return "gitcode" }

func (s *GitCodeSource) FetchLatestRelease(ctx context.Context, client *http.Client) (*ReleaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", s.apiBase, s.owner, s.repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("gitcode: build request: %w", err)
	}
	if s.token != "" {
		req.Header.Set("PRIVATE-TOKEN", s.token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitcode: fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitcode: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("gitcode: decode response: %w", err)
	}

	if body.HTMLURL == "" {
		body.HTMLURL = fmt.Sprintf("https://gitcode.com/%s/%s/releases/tag/%s", s.owner, s.repo, body.TagName)
	}

	// GitCode release assets are source archives only.
	// We construct synthetic assets pointing to repo raw URLs for binary distribution.
	info := &ReleaseInfo{
		TagName: body.TagName,
		HTMLURL: body.HTMLURL,
	}
	// Assets are constructed dynamically by the Updater via constructRawURL().
	// We store the raw base URL pattern for later use.
	return info, nil
}

// constructRawURL builds a download URL via the GitCode raw file API.
func (s *GitCodeSource) constructRawURL(tag, assetName string) string {
	return fmt.Sprintf("%s/repos/%s/%s/raw/dist/release/%s/%s", s.apiBase, s.owner, s.repo, tag, assetName)
}

// AssetURL satisfies the ReleaseSource interface.
// GitCode downloads binary assets via repo raw URLs (方案 B).
func (s *GitCodeSource) AssetURL(tag, assetName string) string {
	return s.constructRawURL(tag, assetName)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/updater/ -run TestGitHubSource -v`
Expected: PASS

- [ ] **Step 5: Write test for GitCodeSource**

Add to `internal/updater/source_test.go`:

```go
func TestGitCodeSource_FetchLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/api/v5/repos/wakeya/magic-claude-code/releases/latest"
		if r.URL.Path != expectedPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Verify PRIVATE-TOKEN header is sent when token is set
		if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Errorf("expected PRIVATE-TOKEN header, got %q", r.Header.Get("PRIVATE-TOKEN"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"tag_name": "v0.3.0",
			"html_url": "https://gitcode.com/wakeya/magic-claude-code/releases/tag/v0.3.0"
		}`))
	}))
	defer server.Close()

	src := &GitCodeSource{
		owner:   "wakeya",
		repo:    "magic-claude-code",
		apiBase: server.URL + "/api/v5",
		token:   "test-token",
	}

	info, err := src.FetchLatestRelease(t.Context(), server.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.TagName != "v0.3.0" {
		t.Errorf("expected tag v0.3.0, got %s", info.TagName)
	}

	// GitCodeSource constructs download URLs via the raw file API (方案 B)
	assetURL := src.AssetURL("v0.3.0", "Magic-Claude-Code-v0.3.0-Linux-x86_64.tar.gz")
	expected := server.URL + "/api/v5/repos/wakeya/magic-claude-code/raw/dist/release/v0.3.0/Magic-Claude-Code-v0.3.0-Linux-x86_64.tar.gz"
	if assetURL != expected {
		t.Errorf("AssetURL: got %q, want %q", assetURL, expected)
	}
}
```

- [ ] **Step 6: Run all source tests**

Run: `go test ./internal/updater/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/updater/source.go internal/updater/source_test.go
git commit -m "feat: add updater source layer (GitHub + GitCode)"
```

---

### Task 3: Updater Package — Core Logic

**Files:**
- Create: `internal/updater/updater.go`
- Create: `internal/updater/updater_test.go`

This task implements the core update workflow: platform detection, asset selection, SHA256 verification, and binary replacement.

`★ Design note:` The Updater tries sources sequentially with a short connectivity timeout. SHA256 verification is mandatory — if checksums file is missing or doesn't match, the update fails. Binary replacement uses backup + rename for atomicity and automatic rollback on failure.

- [ ] **Step 1: Write failing test for asset name mapping**

```go
// internal/updater/updater_test.go
package updater

import "testing"

func TestAssetName(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		goarch  string
		tag     string
		want    string
		wantErr bool
	}{
		{"linux amd64", "linux", "amd64", "v0.2.0", "Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz", false},
		{"linux arm64", "linux", "arm64", "v0.2.0", "Magic-Claude-Code-v0.2.0-Linux-arm64.tar.gz", false},
		{"darwin arm64", "darwin", "arm64", "v0.2.0", "Magic-Claude-Code-v0.2.0-macOS-arm64.tar.gz", false},
		{"windows amd64", "windows", "amd64", "v0.2.0", "Magic-Claude-Code-v0.2.0-Windows-x86_64.zip", false},
		{"unsupported os", "freebsd", "amd64", "v0.2.0", "", true},
		{"unsupported arch", "linux", "386", "v0.2.0", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := assetNameFor(tt.goos, tt.goarch, tt.tag)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/updater/ -run TestAssetName -v`
Expected: FAIL — function not defined

- [ ] **Step 3: Implement asset name mapping**

Add to `internal/updater/updater.go`:

```go
package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"magic-claude-code/internal/version"
)

// assetNameFor maps GOOS/GOARCH to the release asset naming convention.
func assetNameFor(goos, goarch, tag string) (string, error) {
	var platform string
	switch goos {
	case "linux":
		platform = "Linux"
	case "darwin":
		platform = "macOS"
	case "windows":
		platform = "Windows"
	default:
		return "", fmt.Errorf("unsupported OS: %s", goos)
	}

	var arch string
	switch goarch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("unsupported arch: %s", goarch)
	}

	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}

	return fmt.Sprintf("Magic-Claude-Code-%s-%s-%s.%s", tag, platform, arch, ext), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/updater/ -run TestAssetName -v`
Expected: PASS

- [ ] **Step 5: Write failing test for SHA256SUMS parsing**

Add to `internal/updater/updater_test.go`:

```go
func TestParseSHA256Sums(t *testing.T) {
	input := "abc123def456  Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz\n" +
		"789abcdef012  Magic-Claude-Code-v0.2.0-Linux-arm64.tar.gz\n" +
		"fedcba987654  SHA256SUMS.txt\n"

	sums, err := parseSHA256Sums(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sums) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(sums))
	}

	got, ok := sums["Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz"]
	if !ok {
		t.Fatal("expected key not found")
	}
	if got != "abc123def456" {
		t.Errorf("expected abc123def456, got %s", got)
	}
}
```

- [ ] **Step 6: Implement SHA256SUMS parsing**

Add to `internal/updater/updater.go`:

```go
// parseSHA256Sums parses the output of `sha256sum *` (the SHA256SUMS.txt file).
// Format: "<hash>  <filename>" per line.
func parseSHA256Sums(r io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read sha256sums: %w", err)
	}

	sums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// sha256sum format: "<hash>  <filename>" (two spaces)
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		sums[parts[1]] = parts[0]
	}
	return sums, nil
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./internal/updater/ -run TestParseSHA256Sums -v`
Expected: PASS

- [ ] **Step 8: Write failing test for verifyChecksum**

Add to `internal/updater/updater_test.go`:

```go
func TestVerifyChecksum(t *testing.T) {
	data := []byte("hello world")
	hash := sha256.Sum256(data)
	expected := hex.EncodeToString(hash[:])

	if err := verifyChecksum(data, expected); err != nil {
		t.Fatalf("expected match, got error: %v", err)
	}

	if err := verifyChecksum(data, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
}
```

- [ ] **Step 9: Implement verifyChecksum**

Add to `internal/updater/updater.go`:

```go
// verifyChecksum verifies that data matches the expected SHA256 hex string.
func verifyChecksum(data []byte, expectedHex string) error {
	hash := sha256.Sum256(data)
	actual := hex.EncodeToString(hash[:])
	if actual != expectedHex {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHex, actual)
	}
	return nil
}
```

- [ ] **Step 10: Run test to verify it passes**

Run: `go test ./internal/updater/ -run TestVerifyChecksum -v`
Expected: PASS

- [ ] **Step 11: Write failing test for extractBinary**

Add to `internal/updater/updater_test.go`:

```go
func TestExtractBinary(t *testing.T) {
	// Build a tar.gz in memory containing: testdir/mcc
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	binaryContent := []byte("#!/bin/sh\necho hello")
	hdr := &tar.Header{
		Name: "testdir/mcc",
		Mode: 0755,
		Size: int64(len(binaryContent)),
	}
	tw.WriteHeader(hdr)
	tw.Write(binaryContent)
	tw.Close()
	gw.Close()

	got, err := extractBinary(&buf, "mcc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(binaryContent) {
		t.Errorf("extracted content mismatch")
	}
}
```

- [ ] **Step 12: Implement extractBinary**

Add to `internal/updater/updater.go`:

```go
// extractBinary extracts the named binary from a tar.gz archive.
func extractBinary(r io.Reader, binaryName string) ([]byte, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if filepath.Base(hdr.Name) == binaryName {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read %s from archive: %w", binaryName, err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("binary %s not found in archive", binaryName)
}
```

- [ ] **Step 13: Run test to verify it passes**

Run: `go test ./internal/updater/ -run TestExtractBinary -v`
Expected: PASS

Add `bytes` to the test file imports.

- [ ] **Step 14: Implement the Updater struct and CheckForUpdate**

Add to `internal/updater/updater.go`:

```go
// UpdateInfo describes an available update.
type UpdateInfo struct {
	CurrentVersion string
	LatestVersion  string
	SourceName     string
	ReleaseURL     string
	AssetName      string
	DownloadURL    string
}

// Updater orchestrates the update workflow.
type Updater struct {
	sources []ReleaseSource
	client  *http.Client
}

// New creates an Updater that tries sources in order.
func New(sources ...ReleaseSource) *Updater {
	return &Updater{
		sources: sources,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CheckForUpdate queries sources sequentially and returns update info if a newer version exists.
func (u *Updater) CheckForUpdate(ctx context.Context) (*UpdateInfo, error) {
	var lastErr error
	for _, src := range u.sources {
		info, err := u.checkSource(ctx, src)
		if err == nil {
			return info, nil
		}
		lastErr = fmt.Errorf("%s: %w", src.Name(), err)
	}
	return nil, fmt.Errorf("all sources failed: %w", lastErr)
}

func (u *Updater) checkSource(ctx context.Context, src ReleaseSource) (*UpdateInfo, error) {
	release, err := src.FetchLatestRelease(ctx, u.client)
	if err != nil {
		return nil, err
	}

	current := version.Version
	latest := release.TagName
	if !isNewer(current, latest) {
		return &UpdateInfo{
			CurrentVersion: current,
			LatestVersion:  latest,
			SourceName:     src.Name(),
			ReleaseURL:     release.HTMLURL,
		}, nil
	}

	assetName, err := assetNameFor(runtime.GOOS, runtime.GOARCH, latest)
	if err != nil {
		return nil, fmt.Errorf("determine asset: %w", err)
	}

	// Each source knows how to construct its asset download URL:
	// - GitHubSource → releases/download/{tag}/{asset}
	// - GitCodeSource → raw URL to dist/release/{tag}/{asset} (方案 B)
	downloadURL := src.AssetURL(latest, assetName)

	return &UpdateInfo{
		CurrentVersion: current,
		LatestVersion:  latest,
		SourceName:     src.Name(),
		ReleaseURL:     release.HTMLURL,
		AssetName:      assetName,
		DownloadURL:    downloadURL,
	}, nil
}

// isNewer returns true if latest is a newer semver tag than current.
// Tags are expected in "vX.Y.Z" format. "dev" is always older.
func isNewer(current, latest string) bool {
	if current == "dev" {
		return true
	}
	return latest != current && strings.Compare(latest, current) > 0
}
```

- [ ] **Step 15: Write test for isNewer**

Add to `internal/updater/updater_test.go`:

```go
func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"dev", "v0.1.0", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", true},
		{"v0.2.0", "v0.1.0", false},
		{"v1.0.0", "v1.0.1", true},
	}
	for _, tt := range tests {
		got := isNewer(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}
```

- [ ] **Step 16: Run test to verify it passes**

Run: `go test ./internal/updater/ -run TestIsNewer -v`
Expected: PASS

- [ ] **Step 17: Implement DownloadAndApply**

Add to `internal/updater/updater.go`:

```go
// ApplyResult holds the outcome of a successful update.
type ApplyResult struct {
	NewVersion string
	Message    string
}

// DownloadAndApply downloads the update, verifies SHA256, extracts the binary, and replaces the running process.
func (u *Updater) DownloadAndApply(ctx context.Context, info *UpdateInfo) (*ApplyResult, error) {
	if info.DownloadURL == "" {
		return nil, fmt.Errorf("no download URL — already up to date?")
	}

	// 1. Download the asset archive
	archiveData, err := u.downloadFile(ctx, info.DownloadURL)
	if err != nil {
		return nil, fmt.Errorf("download asset: %w", err)
	}

	// 2. Download SHA256SUMS.txt from the same release directory
	sumsURL := info.DownloadURL[:strings.LastIndex(info.DownloadURL, "/")+1] + "SHA256SUMS.txt"
	sumsData, err := u.downloadFile(ctx, sumsURL)
	if err != nil {
		return nil, fmt.Errorf("download sha256sums: %w", err)
	}

	// 3. Verify checksum
	sums, err := parseSHA256Sums(bytes.NewReader(sumsData))
	if err != nil {
		return nil, fmt.Errorf("parse sha256sums: %w", err)
	}
	expected, ok := sums[info.AssetName]
	if !ok {
		return nil, fmt.Errorf("asset %s not found in SHA256SUMS", info.AssetName)
	}
	if err := verifyChecksum(archiveData, expected); err != nil {
		return nil, fmt.Errorf("checksum verification failed: %w", err)
	}

	// 4. Extract binary from archive
	binaryData, err := extractBinary(bytes.NewReader(archiveData), "mcc")
	if err != nil {
		return nil, fmt.Errorf("extract binary: %w", err)
	}

	// 5. Replace running binary
	if err := replaceBinary(binaryData); err != nil {
		return nil, fmt.Errorf("replace binary: %w", err)
	}

	return &ApplyResult{
		NewVersion: info.LatestVersion,
		Message:    "Update applied successfully. Restart required.",
	}, nil
}

func (u *Updater) downloadFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// replaceBinary swaps the running binary with newBinary.
// On Unix: backup → write → rollback on failure. On Windows: not supported (return error).
func replaceBinary(newBinary []byte) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("self-update is not supported on Windows; please update manually")
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	backupPath := exePath + ".bak"
	if err := os.Rename(exePath, backupPath); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}

	if err := os.WriteFile(exePath, newBinary, 0755); err != nil {
		os.Rename(backupPath, exePath)
		return fmt.Errorf("write new binary: %w", err)
	}

	os.Remove(backupPath)
	return nil
}
```

Add `bytes` to the imports of `updater.go`.

- [ ] **Step 18: Run all updater tests**

Run: `go test ./internal/updater/ -v`
Expected: PASS

- [ ] **Step 19: Commit**

```bash
git add internal/updater/updater.go internal/updater/updater_test.go
git commit -m "feat: add updater core logic (check, download, verify, apply)"
```

---

### Task 4: Admin Update API

**Files:**
- Create: `internal/admin/update_handler.go`
- Create: `internal/admin/update_handler_test.go`
- Modify: `internal/admin/server.go` — add updater field, routes, setter

`★ Design note:` The admin server gets an optional `*updater.Updater` via a setter method (`SetUpdater`), keeping the existing `NewServer` signature unchanged for backward compatibility. When no updater is configured, update endpoints return 503 Service Unavailable.

- [ ] **Step 1: Add updater field and routes to server.go**

In `internal/admin/server.go`, add import and field:

```go
import (
	// ...existing imports...
	"magic-claude-code/internal/updater"
)

type Server struct {
	// ...existing fields...
	updater *updater.Updater
}

func (s *Server) SetUpdater(u *updater.Updater) {
	s.updater = u
}
```

In `Start()` method, after existing `mux.HandleFunc` lines, add:

```go
	mux.HandleFunc("/api/update/check", s.authMiddlewareFunc(s.handleUpdateCheck))
	mux.HandleFunc("/api/update/apply", s.authMiddlewareFunc(s.handleUpdateApply))
```

- [ ] **Step 2: Write failing test for update check handler**

```go
// internal/admin/update_handler_test.go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleUpdateCheck_NoUpdater(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/update/check", nil)
	rr := httptest.NewRecorder()
	s.handleUpdateCheck(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/admin/ -run TestHandleUpdateCheck -v`
Expected: FAIL — method not defined

- [ ] **Step 4: Implement update handlers**

```go
// internal/admin/update_handler.go
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"magic-claude-code/internal/updater"
)

type updateCheckResponse struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	UpdateAvailable bool  `json:"update_available"`
	Source         string `json:"source,omitempty"`
	ReleaseURL     string `json:"release_url,omitempty"`
	Error          string `json:"error,omitempty"`
}

type updateApplyResponse struct {
	Success   bool   `json:"success"`
	NewVersion string `json:"new_version,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeUpdateJSON(w, http.StatusServiceUnavailable, updateCheckResponse{
			Error: "self-update is not available (running in Docker or updater not configured)",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	info, err := s.updater.CheckForUpdate(ctx)
	if err != nil {
		writeUpdateJSON(w, http.StatusOK, updateCheckResponse{
			Error: err.Error(),
		})
		return
	}

	writeUpdateJSON(w, http.StatusOK, updateCheckResponse{
		CurrentVersion:  info.CurrentVersion,
		LatestVersion:   info.LatestVersion,
		UpdateAvailable: info.DownloadURL != "",
		Source:          info.SourceName,
		ReleaseURL:      info.ReleaseURL,
	})
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if s.updater == nil {
		writeUpdateApplyJSON(w, http.StatusServiceUnavailable, updateApplyResponse{
			Error: "self-update is not available",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	info, err := s.updater.CheckForUpdate(ctx)
	if err != nil {
		writeUpdateApplyJSON(w, http.StatusOK, updateApplyResponse{
			Error: "failed to check for update: " + err.Error(),
		})
		return
	}

	if info.DownloadURL == "" {
		writeUpdateApplyJSON(w, http.StatusOK, updateApplyResponse{
			Error: "already up to date",
		})
		return
	}

	result, err := s.updater.DownloadAndApply(ctx, info)
	if err != nil {
		writeUpdateApplyJSON(w, http.StatusOK, updateApplyResponse{
			Error: err.Error(),
		})
		return
	}

	writeUpdateApplyJSON(w, http.StatusOK, updateApplyResponse{
		Success:    true,
		NewVersion: result.NewVersion,
		Message:    result.Message,
	})
}

func writeUpdateJSON(w http.ResponseWriter, status int, payload updateCheckResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeUpdateApplyJSON(w http.ResponseWriter, status int, payload updateApplyResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// Ensure updater package is referenced (avoids "imported and not used" if only used in server.go).
var _ = updater.New
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/admin/ -run TestHandleUpdateCheck -v`
Expected: PASS

- [ ] **Step 6: Write test for Docker detection**

Add to `internal/admin/update_handler_test.go`:

```go
func TestHandleUpdateCheck_DockerEnv(t *testing.T) {
	// When updater is nil, should return 503 regardless
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/update/check", nil)
	rr := httptest.NewRecorder()
	s.handleUpdateCheck(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}

	var resp updateCheckResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Error == "" {
		t.Error("expected error message for missing updater")
	}
}
```

- [ ] **Step 7: Run all admin tests**

Run: `go test ./internal/admin/ -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/admin/update_handler.go internal/admin/update_handler_test.go internal/admin/server.go
git commit -m "feat: add admin API endpoints for update check and apply"
```

---

### Task 5: CI/CD — Add Version Injection & GitCode Mirror Note

**Files:**
- Modify: `.github/workflows/release.yml`
- Modify: `.gitlab-ci.yml`

The ldflags changes were already done in Task 1 Step 3-4. This task ensures the Docker build also injects version and documents the GitCode mirror requirement.

- [ ] **Step 1: Update Dockerfile to accept version arg**

In `Dockerfile`, modify the build stage:

```dockerfile
# 阶段2: 构建 Go 二进制
FROM golang:1.26-alpine AS builder

ARG APP_VERSION=dev

WORKDIR /app

# ...existing COPY and build steps...

# 构建
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X magic-claude-code/internal/version.Version=${APP_VERSION}" -o mcc ./cmd/server
```

- [ ] **Step 2: Commit**

```bash
git add Dockerfile
git commit -m "ci: inject version into Docker builds"
```

- [ ] **Step 3: Document GitCode release mirroring requirement**

Add a note to `CLAUDE.md` under "常见问题":

```markdown
### Q: 自动更新使用哪个源？

A: 优先尝试 GitHub Releases API，如果不可达则回退到 GitCode。
   GitCode (gitcode.com) 需要同步发布 Release 产物，包含与 GitHub 相同的资产文件和 SHA256SUMS.txt。
```

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document auto-update source strategy"
```

---

### Task 6: Frontend — Version Label and Update Notification UI

**Files:**
- Modify: `internal/frontend/src/composables/useApi.ts`
- Modify: `internal/frontend/src/composables/useI18n.ts`
- Modify: `internal/frontend/src/components/AppHeader.vue`
- Modify: `internal/frontend/src/styles/main.css`

`★ Design note:` The version label sits next to the title — always visible. When no update is available, it's static gray text (e.g., `v0.1.0`). When an update is available, it becomes a highlighted clickable element showing `v0.1.0 → v0.2.0` with an up-arrow icon. Clicking opens a confirmation dialog with version details and an "立即更新" / "Update Now" button. After applying, the UI shows "更新成功，正在重启..." and the server shuts down for restart.

- [ ] **Step 1: Add update API methods to useApi.ts**

Add types and methods to `internal/frontend/src/composables/useApi.ts`:

After the existing interface definitions (before the `useApi()` function), add:

```typescript
export interface UpdateCheckResult {
  current_version: string
  latest_version: string
  update_available: boolean
  source?: string
  release_url?: string
  error?: string
}

export interface UpdateApplyResult {
  success: boolean
  new_version?: string
  message?: string
  error?: string
}
```

Inside the `useApi()` function, after the last method (`getSessionCleanupHint`), add:

```typescript
  async function checkForUpdate(): Promise<UpdateCheckResult> {
    const res = await fetch('/api/update/check')
    if (!res.ok) throw new Error('Failed to check for update')
    return res.json()
  }

  async function applyUpdate(): Promise<UpdateApplyResult> {
    const res = await fetch('/api/update/apply', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    })
    if (!res.ok) throw new Error('Failed to apply update')
    return res.json()
  }
```

Update the `return { ... }` statement to include:

```typescript
    checkForUpdate,
    applyUpdate,
```

- [ ] **Step 2: Add i18n strings**

In `internal/frontend/src/composables/useI18n.ts`, add to the `zh` messages object:

```typescript
    'update.available': '有新版本',
    'update.checking': '检查更新中...',
    'update.current': '当前版本',
    'update.latest': '最新版本',
    'update.apply': '立即更新',
    'update.applying': '正在更新...',
    'update.success': '更新成功，服务正在重启...',
    'update.error': '更新失败',
    'update.up_to_date': '已是最新版本',
    'update.title': '版本更新',
    'update.confirm': '更新将中断当前服务，确定要继续吗？',
```

Add corresponding entries to the `en` messages object:

```typescript
    'update.available': 'Update available',
    'update.checking': 'Checking...',
    'update.current': 'Current',
    'update.latest': 'Latest',
    'update.apply': 'Update Now',
    'update.applying': 'Updating...',
    'update.success': 'Update successful, restarting...',
    'update.error': 'Update failed',
    'update.up_to_date': 'Up to date',
    'update.title': 'Software Update',
    'update.confirm': 'Updating will restart the service. Continue?',
```

- [ ] **Step 3: Add version label and update dialog to AppHeader.vue**

In `internal/frontend/src/components/AppHeader.vue`, find the title `<h1>` element and add a version label next to it.

Replace:
```html
      <h1 class="text-[17px] font-bold tracking-tight">Magic Claude Code</h1>
```

With:
```html
      <h1 class="text-[17px] font-bold tracking-tight">Magic Claude Code</h1>
      <!-- Version Label / Update Trigger -->
      <span v-if="!updateAvailable"
        class="text-[11px] app-muted font-mono"
      >{{ currentVersion }}</span>
      <button v-else
        type="button"
        class="update-pulse text-[12px] font-mono font-semibold px-2 py-0.5 rounded-md cursor-pointer transition-all duration-200 flex items-center gap-1"
        style="background: var(--app-primary-light); color: var(--app-primary);"
        @click="showUpdateDialog = true"
        :title="t('update.title')"
      >
        {{ currentVersion }}
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /><polyline points="7 10 12 15 17 10" /><line x1="12" y1="15" x2="12" y2="3" />
        </svg>
        {{ updateInfo?.latest_version }}
      </button>
```

Add the pulse animation CSS to `internal/frontend/src/styles/main.css`:

```css
@keyframes update-pulse {
  0%, 100% { box-shadow: 0 0 0 0 var(--app-primary-light); }
  50% { box-shadow: 0 0 0 4px transparent; }
}
.update-pulse {
  animation: update-pulse 2s ease-in-out infinite;
}
```

Add the update dialog before the closing `</header>` tag:

```html
    <!-- Update Dialog -->
    <Teleport to="body">
      <div v-if="showUpdateDialog" class="fixed inset-0 z-[100] flex items-center justify-center" style="background: rgba(0,0,0,0.5);">
        <div class="app-panel rounded-xl p-6 max-w-md w-full mx-4 shadow-2xl">
          <h3 class="text-lg font-bold mb-4">{{ t('update.title') }}</h3>
          <div class="space-y-2 mb-4 text-sm">
            <div class="flex justify-between">
              <span class="app-muted">{{ t('update.current') }}:</span>
              <span class="font-mono">{{ updateInfo?.current_version }}</span>
            </div>
            <div class="flex justify-between">
              <span class="app-muted">{{ t('update.latest') }}:</span>
              <span class="font-mono font-bold" style="color: var(--app-primary);">{{ updateInfo?.latest_version }}</span>
            </div>
          </div>
          <p v-if="updateError" class="text-sm mb-3" style="color: var(--app-danger);">{{ updateError }}</p>
          <p class="text-xs app-muted mb-4">{{ t('update.confirm') }}</p>
          <div class="flex gap-3 justify-end">
            <button type="button" class="app-control px-4 py-2 rounded-lg text-sm font-semibold cursor-pointer" :disabled="updating"
              @click="showUpdateDialog = false">
              {{ t('header.logout') === '退出登录' ? '取消' : 'Cancel' }}
            </button>
            <button type="button" class="px-4 py-2 rounded-lg text-sm font-semibold text-white cursor-pointer transition-all duration-200"
              style="background: var(--app-primary);"
              :disabled="updating"
              @click="applyUpdate">
              {{ updating ? t('update.applying') : t('update.apply') }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
```

Update the `<script setup>` section:

```typescript
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Moon, Sun } from 'lucide-vue-next'
import { useApi, type UpdateCheckResult } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import { useTheme } from '@/composables/useTheme'

defineEmits<{ logout: [] }>()

const api = useApi()
const { locale, t, setLocale } = useI18n()
const { themeMode, persistTheme, syncError } = useTheme()
const langOpen = ref(false)
const langMenuRef = ref<HTMLElement | null>(null)

const updateAvailable = ref(false)
const updateInfo = ref<UpdateCheckResult | null>(null)
const showUpdateDialog = ref(false)
const updating = ref(false)
const updateError = ref('')

const currentVersion = computed(() => updateInfo.value?.current_version || 'dev')

const langOptions = [
  { value: 'zh' as const, label: '中文' },
  { value: 'en' as const, label: 'English' },
]

async function checkUpdate() {
  try {
    const result = await api.checkForUpdate()
    updateInfo.value = result
    updateAvailable.value = result.update_available
  } catch {
    // Silently ignore — update check is best-effort
  }
}

async function applyUpdate() {
  updating.value = true
  updateError.value = ''
  try {
    const result = await api.applyUpdate()
    if (result.success) {
      updateAvailable.value = false
      showUpdateDialog.value = false
      // Server will restart — show message
      alert(t('update.success'))
    } else {
      updateError.value = result.error || t('update.error')
    }
  } catch (e) {
    updateError.value = String(e)
  } finally {
    updating.value = false
  }
}

onMounted(() => {
  checkUpdate()
})
```

- [ ] **Step 4: Build frontend and verify no errors**

Run:
```bash
cd internal/frontend && npm run build && echo "BUILD OK"
```
Expected: `BUILD OK`

- [ ] **Step 5: Commit**

```bash
git add internal/frontend/src/composables/useApi.ts internal/frontend/src/composables/useI18n.ts internal/frontend/src/components/AppHeader.vue
git commit -m "feat: add version label and update dialog in header"
```

---

### Task 7: Wiring & Startup Auto-Check

**Files:**
- Modify: `cmd/server/main.go`

`★ Design note:` On startup, a non-blocking goroutine checks for updates after a 10-second delay (to let servers bind first). If an update is available, it logs a message. The updater is always instantiated (for the admin API), but Docker detection (`/.dockerenv`) disables self-update in containerized environments.

- [ ] **Step 1: Wire updater into main.go**

In `cmd/server/main.go`, add imports:

```go
import (
	// ...existing imports...
	"magic-claude-code/internal/updater"
	"magic-claude-code/internal/version"
)
```

After the `adminServer := admin.NewServer(...)` block and before the `go func()` goroutines, add:

```go
	// 配置自动更新器
	var updaterInstance *updater.Updater
	if _, err := os.Stat("/.dockerenv"); err == nil {
		log.Println("运行在 Docker 容器中，自动更新已禁用（请通过镜像更新）")
	} else {
		updaterInstance = updater.New(
			updater.NewGitHubSource("wakeya", "magic-claude-code"),
			updater.NewGitCodeSource("wakeya", "magic-claude-code", os.Getenv("GITCODE_TOKEN")),
		)
		adminServer.SetUpdater(updaterInstance)

		// 启动时延迟检查更新（非阻塞）
		go func() {
			time.Sleep(10 * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			info, err := updaterInstance.CheckForUpdate(ctx)
			if err != nil {
				log.Printf("检查更新失败: %v", err)
				return
			}
			if info.DownloadURL != "" {
				log.Printf("发现新版本 %s（当前 %s），请在配置页面手动更新",
					info.LatestVersion, info.CurrentVersion)
			} else {
				log.Printf("当前版本 %s 已是最新", version.Version)
			}
		}()
	}
```

- [ ] **Step 2: Build and smoke test**

Run:
```bash
go build ./cmd/server && echo "BUILD OK"
```
Expected: `BUILD OK`

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: wire updater with startup auto-check and Docker detection"
```

---

## Post-Implementation Notes

### GitCode Release Mirroring

For the GitCode fallback to work, pre-compiled binaries and `SHA256SUMS.txt` must be available in the repo under `dist/release/{tag}/`. Download URLs use the GitCode raw file API: `GET https://api.gitcode.com/api/v5/repos/{owner}/{repo}/raw/dist/release/{tag}/{asset_name}`. Steps after each GitHub release:
1. Download all release assets from GitHub
2. Place them under `dist/release/{tag}/` in the local repo
3. Push to GitCode (the raw URLs become immediately downloadable)
4. Create a GitCode release with the same tag for the releases API tag detection

### Windows Self-Update

Self-update on Windows is not supported in this implementation because the running binary cannot be overwritten or renamed. Windows users should update manually by downloading the new release archive.

### Restart After Update

After a successful update, the server binary is replaced on disk. The running process continues with the old binary in memory. To activate the new version:
- **systemd**: `systemctl restart magic-claude-code` (or rely on watchdog)
- **Docker**: Restart the container: `docker restart <container>`
- **Manual**: Ctrl+C and re-run the binary

A future enhancement could add automatic graceful restart via `syscall.Exec`.
