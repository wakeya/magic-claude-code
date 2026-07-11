package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"magic-claude-code/internal/version"
)

const (
	maxDownloadSize         = 200 << 20 // 200 MB safety limit for release archives
	maxChecksumDownloadSize = 1 << 20   // SHA256SUMS.txt should stay tiny.
)

// UpdateInfo describes an available update.
type UpdateInfo struct {
	CurrentVersion string
	LatestVersion  string
	SourceName     string
	ReleaseURL     string
	AssetName      string
	DownloadURL    string
}

// ApplyResult holds the outcome of a successful update.
type ApplyResult struct {
	NewVersion string
	Message    string
	Restarting bool
}

// Updater orchestrates the update workflow.
type Updater struct {
	sources     []ReleaseSource
	client      *http.Client
	mu          sync.Mutex
	restartSig  chan struct{}
	restartFlag atomic.Bool
}

// New creates an Updater that tries sources in order.
func New(sources ...ReleaseSource) *Updater {
	return &Updater{
		sources: sources,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		restartSig: make(chan struct{}, 1),
	}
}

// RestartSignal returns a channel that receives a value when a restart is needed.
func (u *Updater) RestartSignal() <-chan struct{} {
	return u.restartSig
}

// ShouldRestart reports whether the process should re-exec after shutdown.
func (u *Updater) ShouldRestart() bool {
	return u.restartFlag.Load()
}

// SignalRestart sets the restart flag and notifies the restart channel.
func (u *Updater) SignalRestart() {
	u.restartFlag.Store(true)
	select {
	case u.restartSig <- struct{}{}:
	default:
	}
}

// CheckForUpdate queries sources sequentially and returns update info.
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
	if parseSemver(latest) == nil {
		return nil, fmt.Errorf("invalid release tag: %s", latest)
	}
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

	downloadURL := src.AssetURL(latest, assetName)
	if len(release.Assets) > 0 {
		asset := release.findAsset(assetName)
		if asset == nil {
			return nil, fmt.Errorf("release asset %s not found", assetName)
		}
		if asset.DownloadURL == "" {
			return nil, fmt.Errorf("release asset %s has no download URL", assetName)
		}
		downloadURL = asset.DownloadURL
	}

	return &UpdateInfo{
		CurrentVersion: current,
		LatestVersion:  latest,
		SourceName:     src.Name(),
		ReleaseURL:     release.HTMLURL,
		AssetName:      assetName,
		DownloadURL:    downloadURL,
	}, nil
}

// DownloadAndApply downloads the update, verifies SHA256, extracts the binary, and replaces the running process.
// Works on all platforms: Linux/macOS auto-restart via syscall.Exec; Windows requires manual restart.
func (u *Updater) DownloadAndApply(ctx context.Context, info *UpdateInfo) (*ApplyResult, error) {
	if info.DownloadURL == "" {
		return nil, fmt.Errorf("no download URL — already up to date?")
	}

	archiveData, err := u.downloadFile(ctx, info.DownloadURL)
	if err != nil {
		return nil, fmt.Errorf("download asset: %w", err)
	}

	sumsURL, ok := checksumURLForAsset(info.DownloadURL)
	if !ok {
		return nil, errors.New("invalid download URL: <invalid-url>")
	}
	sumsData, err := u.downloadFileWithLimit(ctx, sumsURL, maxChecksumDownloadSize)
	if err != nil {
		return nil, fmt.Errorf("download sha256sums: %w", err)
	}

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

	binaryName := "mcc"
	if runtime.GOOS == "windows" {
		binaryName = "mcc.exe"
	}
	binaryData, err := extractBinaryFromArchive(archiveData, binaryName, info.AssetName)
	if err != nil {
		return nil, fmt.Errorf("extract binary: %w", err)
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if err := replaceBinary(binaryData); err != nil {
		return nil, fmt.Errorf("replace binary: %w", err)
	}

	restarting := shouldRestartAfterApply(runtime.GOOS)
	msg := "Update applied successfully. Please restart the service to use the new version."
	if restarting {
		msg = "Update applied successfully. Restarting..."
	}

	return &ApplyResult{
		NewVersion: info.LatestVersion,
		Message:    msg,
		Restarting: restarting,
	}, nil
}

func shouldRestartAfterApply(goos string) bool {
	return goos != "windows"
}

// requestFailureCategory maps a download failure to a fixed public category
// string. It may inspect the error with errors.Is, but it must never place the
// error text in the returned string.
func requestFailureCategory(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		return "was canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timed out"
	default:
		return "failed"
	}
}

func (u *Updater) downloadFile(ctx context.Context, url string) ([]byte, error) {
	return u.downloadFileWithLimit(ctx, url, maxDownloadSize)
}

func (u *Updater) downloadFileWithLimit(ctx context.Context, raw string, maxSize int) ([]byte, error) {
	parsed, ok := parseDownloadURL(raw)
	if !ok {
		return nil, errors.New("invalid download URL: <invalid-url>")
	}
	target := safeURLOrigin(parsed)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create download request for %s failed", target)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request to %s %s", target, requestFailureCategory(ctx, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, target)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxSize)+1))
	if err != nil {
		return nil, fmt.Errorf("read download response from %s failed", target)
	}
	if len(data) > maxSize {
		return nil, fmt.Errorf("download from %s exceeds maximum size of %d bytes", target, maxSize)
	}
	return data, nil
}

// parseDownloadURL accepts only absolute hierarchical http/https URLs with a host.
// The parser error is discarded so callers never format untrusted parse diagnostics.
func parseDownloadURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.Host == "" {
		return nil, false
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil, false
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return parsed, true
}

// safeURLOrigin renders only the scheme + host (origin) of an already-validated URL.
func safeURLOrigin(parsed *url.URL) string {
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
}

// redactURLForError returns the safe origin for a valid download URL, or the
// fixed sentinel "<invalid-url>" for every rejected value. It must be the only
// URL-derived data that appears in any error string.
func redactURLForError(raw string) string {
	parsed, ok := parseDownloadURL(raw)
	if !ok {
		return "<invalid-url>"
	}
	return safeURLOrigin(parsed)
}

// checksumURLForAsset derives the SHA256SUMS.txt URL from a valid download URL by
// resolving against the parsed URL (dropping query/fragment) and replacing the
// final path segment. The returned URL is a request URL, never a diagnostic.
func checksumURLForAsset(raw string) (string, bool) {
	base, ok := parseDownloadURL(raw)
	if !ok {
		return "", false
	}
	base.RawQuery = ""
	base.ForceQuery = false
	base.Fragment = ""
	base.RawFragment = ""
	return base.ResolveReference(&url.URL{Path: "SHA256SUMS.txt"}).String(), true
}

// replaceBinary swaps the running binary with newBinary.
func replaceBinary(newBinary []byte) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	return replaceBinaryAt(exePath, newBinary)
}

// replaceBinaryAt writes a complete temporary binary before swapping paths.
// This avoids leaving exePath missing or partially written if the write fails.
func replaceBinaryAt(exePath string, newBinary []byte) error {
	info, err := os.Stat(exePath)
	if err != nil {
		return fmt.Errorf("stat current binary: %w", err)
	}

	dir := filepath.Dir(exePath)
	base := filepath.Base(exePath)
	tmp, err := os.CreateTemp(dir, "."+base+".new-*")
	if err != nil {
		return fmt.Errorf("create temporary binary: %w", err)
	}
	tmpPath := tmp.Name()
	tmpClosed := false
	defer func() {
		if !tmpClosed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(newBinary); err != nil {
		return fmt.Errorf("write temporary binary: %w", err)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod temporary binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		tmpClosed = true
		return fmt.Errorf("close temporary binary: %w", err)
	}
	tmpClosed = true

	backupPath := exePath + ".bak"
	if err := os.Rename(exePath, backupPath); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}

	if err := os.Rename(tmpPath, exePath); err != nil {
		if rbErr := os.Rename(backupPath, exePath); rbErr != nil {
			return fmt.Errorf("install new binary: %w (CRITICAL: rollback also failed: %v — binary may be missing at %s)", err, rbErr, exePath)
		}
		return fmt.Errorf("install new binary: %w", err)
	}

	_ = os.Remove(backupPath)
	return nil
}

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

// parseSHA256Sums parses the output of `sha256sum *` (the SHA256SUMS.txt file).
func parseSHA256Sums(r io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read sha256sums: %w", err)
	}

	sums := make(map[string]string)
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		sums[parts[1]] = parts[0]
	}
	return sums, nil
}

// verifyChecksum verifies that data matches the expected SHA256 hex string.
func verifyChecksum(data []byte, expectedHex string) error {
	hash := sha256.Sum256(data)
	actual := hex.EncodeToString(hash[:])
	if actual != expectedHex {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHex, actual)
	}
	return nil
}

// extractBinaryFromArchive dispatches to the correct extractor based on asset name extension.
func extractBinaryFromArchive(data []byte, binaryName, assetName string) ([]byte, error) {
	if strings.HasSuffix(assetName, ".zip") {
		return extractBinaryFromZip(data, binaryName)
	}
	return extractBinaryFromTarGz(bytes.NewReader(data), binaryName)
}

// extractBinaryFromTarGz extracts the named binary from a tar.gz archive.
func extractBinaryFromTarGz(r io.Reader, binaryName string) ([]byte, error) {
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
		if filepath.Base(hdr.Name) == binaryName && hdr.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read %s from archive: %w", binaryName, err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("binary %s not found in archive", binaryName)
}

// extractBinaryFromZip extracts the named binary from a zip archive.
func extractBinaryFromZip(data []byte, binaryName string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	for _, f := range zr.File {
		if filepath.Base(f.Name) == binaryName && f.Mode().IsRegular() {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open %s in zip: %w", binaryName, err)
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				return nil, fmt.Errorf("read %s from zip: %w", binaryName, err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("binary %s not found in zip archive", binaryName)
}

type semver struct {
	major, minor, patch int
}

func parseSemver(tag string) *semver {
	tag = strings.TrimPrefix(tag, "v")
	tag = strings.SplitN(tag, "-", 2)[0]
	parts := strings.SplitN(tag, ".", 3)
	if len(parts) != 3 {
		return nil
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	patch, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return nil
	}
	return &semver{major, minor, patch}
}

// isNewer returns true if latest is a newer semver tag than current.
// "dev" is always older than any release.
func isNewer(current, latest string) bool {
	if current == "dev" {
		return true
	}
	cv := parseSemver(current)
	lv := parseSemver(latest)
	if cv == nil || lv == nil {
		return latest != current
	}
	if lv.major != cv.major {
		return lv.major > cv.major
	}
	if lv.minor != cv.minor {
		return lv.minor > cv.minor
	}
	return lv.patch > cv.patch
}
