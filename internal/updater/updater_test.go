package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"magic-claude-code/internal/version"
)

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

func TestExtractBinaryFromTarGz(t *testing.T) {
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

	got, err := extractBinaryFromTarGz(&buf, "mcc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(binaryContent) {
		t.Errorf("extracted content mismatch")
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("somedir/mcc")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	binaryContent := []byte("#!/bin/sh\necho hello from zip")
	w.Write(binaryContent)
	zw.Close()

	got, err := extractBinaryFromZip(buf.Bytes(), "mcc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(binaryContent) {
		t.Errorf("extracted content mismatch")
	}
}

func TestExtractBinaryFromArchive_Dispatch(t *testing.T) {
	t.Run("tar.gz dispatch", func(t *testing.T) {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		content := []byte("tar content")
		tw.WriteHeader(&tar.Header{Name: "dir/mcc", Mode: 0755, Size: int64(len(content))})
		tw.Write(content)
		tw.Close()
		gw.Close()

		got, err := extractBinaryFromArchive(buf.Bytes(), "mcc", "Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != "tar content" {
			t.Error("content mismatch")
		}
	})

	t.Run("zip dispatch", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("dir/mcc.exe")
		w.Write([]byte("zip content"))
		zw.Close()

		got, err := extractBinaryFromArchive(buf.Bytes(), "mcc.exe", "Magic-Claude-Code-v0.2.0-Windows-x86_64.zip")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != "zip content" {
			t.Error("content mismatch")
		}
	})
}

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
		{"v0.9.0", "v0.10.0", true},
		{"v0.10.0", "v0.9.0", false},
		{"v1.10.0", "v1.2.0", false},
		{"v0.1.0", "v0.1.0-rc1", false},
	}
	for _, tt := range tests {
		got := isNewer(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

type fakeReleaseSource struct {
	name    string
	release *ReleaseInfo
}

func (s fakeReleaseSource) Name() string { return s.name }

func (s fakeReleaseSource) FetchLatestRelease(context.Context, *http.Client) (*ReleaseInfo, error) {
	return s.release, nil
}

func (s fakeReleaseSource) AssetURL(tag, assetName string) string {
	return "constructed://" + tag + "/" + assetName
}

func TestCheckForUpdateRejectsInvalidReleaseTag(t *testing.T) {
	u := New(fakeReleaseSource{
		name: "fake",
		release: &ReleaseInfo{
			TagName: "latest",
			HTMLURL: "https://example.com/releases/latest",
		},
	})

	_, err := u.CheckForUpdate(t.Context())
	if err == nil {
		t.Fatal("expected invalid release tag error")
	}
	if !strings.Contains(err.Error(), "invalid release tag") {
		t.Fatalf("error = %v, want invalid release tag error", err)
	}
}

func TestCheckForUpdateRequiresPublishedAssetWhenReleaseListsAssets(t *testing.T) {
	oldVersion := version.Version
	version.Version = "v0.1.0"
	defer func() { version.Version = oldVersion }()

	u := New(fakeReleaseSource{
		name: "fake",
		release: &ReleaseInfo{
			TagName: "v0.2.0",
			HTMLURL: "https://example.com/releases/v0.2.0",
			Assets: []ReleaseAsset{
				{Name: "SHA256SUMS.txt", DownloadURL: "https://example.com/SHA256SUMS.txt"},
			},
		},
	})

	_, err := u.CheckForUpdate(t.Context())
	if err == nil {
		t.Fatal("expected missing release asset error")
	}
	if !strings.Contains(err.Error(), "release asset") {
		t.Fatalf("error = %v, want release asset error", err)
	}
}

func TestCheckForUpdateUsesPublishedAssetDownloadURL(t *testing.T) {
	oldVersion := version.Version
	version.Version = "v0.1.0"
	defer func() { version.Version = oldVersion }()

	assetName, err := assetNameFor(runtime.GOOS, runtime.GOARCH, "v0.2.0")
	if err != nil {
		t.Skipf("unsupported test platform: %v", err)
	}

	u := New(fakeReleaseSource{
		name: "fake",
		release: &ReleaseInfo{
			TagName: "v0.2.0",
			HTMLURL: "https://example.com/releases/v0.2.0",
			Assets: []ReleaseAsset{
				{Name: assetName, DownloadURL: "https://cdn.example.com/" + assetName},
			},
		},
	})

	info, err := u.CheckForUpdate(t.Context())
	if err != nil {
		t.Fatalf("CheckForUpdate() error = %v", err)
	}
	if info.DownloadURL != "https://cdn.example.com/"+assetName {
		t.Fatalf("DownloadURL = %q, want published asset URL", info.DownloadURL)
	}
}

func TestCheckForUpdateGitCodeReleaseURL(t *testing.T) {
	oldVersion := version.Version
	version.Version = "v0.1.0"
	defer func() { version.Version = oldVersion }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Fatalf("release request PRIVATE-TOKEN = %q, want test-token", r.Header.Get("PRIVATE-TOKEN"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v0.2.0","html_url":"https://gitcode.com/wakeya/magic-claude-code/releases/tag/v0.2.0"}`))
	}))
	defer server.Close()

	src := &GitCodeSource{
		owner:   "wakeya",
		repo:    "magic-claude-code",
		apiBase: server.URL,
		token:   "test-token",
	}
	u := New(src)
	u.client = server.Client()

	info, err := u.CheckForUpdate(t.Context())
	if err != nil {
		t.Fatalf("CheckForUpdate() error = %v", err)
	}
	expectedURL := "https://gitcode.com/wakeya/magic-claude-code/releases/download/v0.2.0/Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz"
	if info.DownloadURL != expectedURL {
		t.Fatalf("DownloadURL = %q, want %q", info.DownloadURL, expectedURL)
	}
}

func TestDownloadFileRejectsOversizedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxDownloadSize+1))
	}))
	defer server.Close()

	u := New()
	u.client = server.Client()

	_, err := u.downloadFile(t.Context(), server.URL+"/large")
	if err == nil {
		t.Fatal("expected oversized download error")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("error = %v, want maximum size error", err)
	}
}

func TestDownloadFileWithLimitRejectsCustomLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("123456"))
	}))
	defer server.Close()

	u := New()
	u.client = server.Client()

	_, err := u.downloadFileWithLimit(t.Context(), server.URL+"/small", 5)
	if err == nil {
		t.Fatal("expected custom limit error")
	}
	if !strings.Contains(err.Error(), "5 bytes") {
		t.Fatalf("error = %v, want custom byte limit", err)
	}
}

func TestDownloadFileRedactsURLInStatusErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	u := New()
	u.client = server.Client()

	_, err := u.downloadFile(t.Context(), server.URL+"/asset?token=secret")
	if err == nil {
		t.Fatal("expected status error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token=") {
		t.Fatalf("error leaked sensitive query: %v", err)
	}
	if !strings.Contains(err.Error(), server.URL+"/asset") {
		t.Fatalf("error = %v, want redacted asset URL", err)
	}
}

func TestDownloadAndApplyRedactsInvalidDownloadURL(t *testing.T) {
	u := New()
	_, err := u.DownloadAndApply(t.Context(), &UpdateInfo{
		LatestVersion: "v0.2.0",
		AssetName:     "asset.tar.gz",
		DownloadURL:   "https://user:pass@example.com?token=secret",
	})
	if err == nil {
		t.Fatal("expected invalid download URL error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "user:pass") {
		t.Fatalf("error leaked sensitive URL parts: %v", err)
	}
}

func TestRedactURLForError(t *testing.T) {
	got := redactURLForError("https://user:pass@example.com/path/file.tar.gz?token=secret#fragment")
	want := "https://example.com/path/file.tar.gz"
	if got != want {
		t.Fatalf("redactURLForError() = %q, want %q", got, want)
	}

	if got := redactURLForError("://bad-url"); got != "<invalid-url>" {
		t.Fatalf("redactURLForError(invalid) = %q, want <invalid-url>", got)
	}
}

func TestReplaceBinaryAtWritesTempBeforeSwap(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "mcc")
	if err := os.WriteFile(exePath, []byte("old binary"), 0700); err != nil {
		t.Fatalf("write old binary: %v", err)
	}

	if err := replaceBinaryAt(exePath, []byte("new binary")); err != nil {
		t.Fatalf("replaceBinaryAt() error = %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read new binary: %v", err)
	}
	if string(got) != "new binary" {
		t.Fatalf("binary content = %q, want new binary", got)
	}

	info, err := os.Stat(exePath)
	if err != nil {
		t.Fatalf("stat new binary: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("mode = %v, want 0700", info.Mode().Perm())
	}
	if _, err := os.Stat(exePath + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("backup should be removed, stat error = %v", err)
	}
}

func TestReplaceBinaryAtMissingCurrentBinaryFailsWithoutCreatingTarget(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "missing-mcc")

	err := replaceBinaryAt(exePath, []byte("new binary"))
	if err == nil {
		t.Fatal("expected missing current binary error")
	}
	if _, statErr := os.Stat(exePath); !os.IsNotExist(statErr) {
		t.Fatalf("target should not be created, stat error = %v", statErr)
	}
}

func TestShouldRestartAfterApply(t *testing.T) {
	tests := []struct {
		goos string
		want bool
	}{
		{goos: "linux", want: true},
		{goos: "darwin", want: true},
		{goos: "windows", want: false},
	}

	for _, tt := range tests {
		if got := shouldRestartAfterApply(tt.goos); got != tt.want {
			t.Fatalf("shouldRestartAfterApply(%q) = %v, want %v", tt.goos, got, tt.want)
		}
	}
}
