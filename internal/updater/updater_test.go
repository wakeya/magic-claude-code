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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

	raw := server.URL + "/" + markerPath + "?" + markerQueryKey + "=" + markerQuery + "#" + markerFragment
	_, err := u.downloadFile(t.Context(), raw)
	if err == nil {
		t.Fatal("expected oversized download error")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("error = %v, want maximum size error", err)
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error = %v, want server origin retained", err)
	}
	assertNoSensitiveMarkers(t, err.Error(), allSensitiveMarkers()...)
}

func TestDownloadFileWithLimitRejectsCustomLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("123456"))
	}))
	defer server.Close()

	u := New()
	u.client = server.Client()

	raw := server.URL + "/" + markerPath + "?" + markerQueryKey + "=" + markerQuery
	_, err := u.downloadFileWithLimit(t.Context(), raw, 5)
	if err == nil {
		t.Fatal("expected custom limit error")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size of 5 bytes") {
		t.Fatalf("error = %v, want custom byte limit", err)
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error = %v, want server origin retained", err)
	}
	assertNoSensitiveMarkers(t, err.Error(), allSensitiveMarkers()...)
}

// Distinct sensitive markers used across the URL-redaction tests. Every marker
// must be absent from each redacted error string. Relying only on "user:pass"
// would be unsafe because Go's http client masks the password as "***" while
// still exposing username, query, fragment, path, and redirect text.
const (
	markerUsername  = "username-secret"
	markerPassword  = "password-secret"
	markerPath      = "path-secret"
	markerQueryKey  = "query-key"
	markerQuery     = "query-secret"
	markerFragment  = "fragment-secret"
	markerRedirect  = "redirect-secret"
	markerTransport = "transport-secret"
	markerBody      = "body-secret"
)

func allSensitiveMarkers() []string {
	return []string{
		markerUsername, markerPassword, markerPath,
		markerQueryKey, markerQuery, markerFragment,
		markerRedirect, markerTransport, markerBody,
	}
}

// validURLWithMarkers is a valid https URL carrying every URL-side marker; its
// safe origin is https://example.com and it reaches the transport when used.
func validURLWithMarkers() string {
	return "https://" + markerUsername + ":" + markerPassword +
		"@example.com/" + markerPath +
		"?" + markerQueryKey + "=" + markerQuery +
		"#" + markerFragment
}

// roundTripFunc adapts a function into an http.RoundTripper for hermetic tests.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// errorReadCloser is an io.ReadCloser whose Read always fails with err.
type errorReadCloser struct{ err error }

func (r errorReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (errorReadCloser) Close() error               { return nil }

func assertNoSensitiveMarkers(t *testing.T, got string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(got, marker) {
			t.Fatalf("output leaked %q: %s", marker, got)
		}
	}
}

func TestParseDownloadURL(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		ok     bool
		origin string
	}{
		{"simple https", "https://example.com/a", true, "https://example.com"},
		{"uppercase scheme with port", "HTTP://example.com:8080/a?x=y", true, "http://example.com:8080"},
		{"full credentials and markers", validURLWithMarkers(), true, "https://example.com"},
		{"opaque payload", "user:password-secret@example.com/path?query-key=query-secret", false, ""},
		{"unsupported scheme", "ftp://example.com/file", false, ""},
		{"relative path", "/relative/path?query-key=query-secret", false, ""},
		{"missing host", "https:///missing-host", false, ""},
		{"malformed escape", "https://example.com/%zz?query-key=query-secret", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, ok := parseDownloadURL(tt.raw)
			if ok != tt.ok {
				t.Fatalf("parseDownloadURL(%q) ok = %v, want %v", tt.raw, ok, tt.ok)
			}
			if !ok {
				if parsed != nil {
					t.Fatalf("parseDownloadURL(%q) returned non-nil URL on rejection", tt.raw)
				}
				return
			}
			if got := safeURLOrigin(parsed); got != tt.origin {
				t.Fatalf("safeURLOrigin = %q, want %q", got, tt.origin)
			}
			if parsed.Scheme != strings.ToLower(parsed.Scheme) {
				t.Fatalf("scheme not normalized to lowercase: %q", parsed.Scheme)
			}
		})
	}
}

func TestRedactURLForError(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		markers []string
	}{
		{"simple https", "https://example.com/a", "https://example.com", nil},
		{"uppercase scheme with port", "HTTP://example.com:8080/a?x=y", "http://example.com:8080", nil},
		{"full credentials", validURLWithMarkers(), "https://example.com",
			[]string{markerUsername, markerPassword, markerPath, markerQueryKey, markerQuery, markerFragment}},
		{"opaque payload", "user:password-secret@example.com/path?query-key=query-secret", "<invalid-url>",
			[]string{markerPassword, markerPath, markerQueryKey, markerQuery, "example.com"}},
		{"unsupported scheme", "ftp://example.com/file", "<invalid-url>", nil},
		{"relative path", "/relative/path?query-key=query-secret", "<invalid-url>",
			[]string{markerQueryKey, markerQuery}},
		{"missing host", "https:///missing-host", "<invalid-url>", nil},
		{"malformed escape", "https://example.com/%zz?query-key=query-secret", "<invalid-url>",
			[]string{markerQueryKey, markerQuery, "%zz"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactURLForError(tt.raw)
			if got != tt.want {
				t.Fatalf("redactURLForError(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			assertNoSensitiveMarkers(t, got, tt.markers...)
		})
	}
}

func TestDownloadFileRejectsUnsafeURL(t *testing.T) {
	invalidURLs := []string{
		"user:password-secret@example.com/path?query-key=query-secret",
		"ftp://example.com/file",
		"/relative/path?query-key=query-secret",
		"https:///missing-host",
		"https://example.com/%zz?query-key=query-secret",
	}
	for _, raw := range invalidURLs {
		t.Run(raw, func(t *testing.T) {
			var calls atomic.Int32
			u := New()
			u.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				t.Fatalf("transport must not be called for invalid URL %q", raw)
				return nil, nil
			})}
			_, err := u.downloadFileWithLimit(t.Context(), raw, maxDownloadSize)
			if err == nil {
				t.Fatal("expected invalid URL error")
			}
			if err.Error() != "invalid download URL: <invalid-url>" {
				t.Fatalf("error = %q, want exact invalid URL message", err.Error())
			}
			if got := calls.Load(); got != 0 {
				t.Fatalf("transport called %d times for invalid URL", got)
			}
			assertNoSensitiveMarkers(t, err.Error(), allSensitiveMarkers()...)
		})
	}
}

func TestChecksumURLForAsset(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
		want string
	}{
		{
			name: "credentials query and fragment",
			raw:  "https://user:pass@example.com/releases/v1/asset.tar.gz?token=secret#fragment",
			ok:   true,
			want: "https://user:pass@example.com/releases/v1/SHA256SUMS.txt",
		},
		{
			name: "simple asset",
			raw:  "https://example.com/asset.tar.gz",
			ok:   true,
			want: "https://example.com/SHA256SUMS.txt",
		},
		{name: "opaque", raw: "user:password-secret@example.com/path", ok: false},
		{name: "relative", raw: "/relative/asset.tar.gz", ok: false},
		{name: "unsupported scheme", raw: "ftp://example.com/asset.tar.gz", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := checksumURLForAsset(tt.raw)
			if ok != tt.ok {
				t.Fatalf("checksumURLForAsset(%q) ok = %v, want %v", tt.raw, ok, tt.ok)
			}
			if tt.ok {
				if got != tt.want {
					t.Fatalf("checksumURLForAsset(%q) = %q, want %q", tt.raw, got, tt.want)
				}
				return
			}
			if got != "" {
				t.Fatalf("checksumURLForAsset(%q) = %q on rejection, want empty", tt.raw, got)
			}
		})
	}
}

func TestRequestFailureCategory(t *testing.T) {
	ctx := context.Background()
	if got := requestFailureCategory(ctx, context.Canceled); got != "was canceled" {
		t.Fatalf("canceled: got %q, want %q", got, "was canceled")
	}
	if got := requestFailureCategory(ctx, context.DeadlineExceeded); got != "timed out" {
		t.Fatalf("deadline: got %q, want %q", got, "timed out")
	}
	if got := requestFailureCategory(ctx, errors.New(markerTransport)); got != "failed" {
		t.Fatalf("other: got %q, want %q", got, "failed")
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := requestFailureCategory(canceledCtx, errors.New("any")); got != "was canceled" {
		t.Fatalf("ctx canceled: got %q, want %q", got, "was canceled")
	}

	deadlineCtx, dcancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer dcancel()
	if got := requestFailureCategory(deadlineCtx, errors.New("any")); got != "timed out" {
		t.Fatalf("ctx deadline: got %q, want %q", got, "timed out")
	}
}

func TestDownloadFileTransportErrorDiscardsRawCause(t *testing.T) {
	u := New()
	u.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New(markerTransport + ": " + req.URL.String())
	})}

	_, err := u.downloadFile(t.Context(), validURLWithMarkers())
	if err == nil {
		t.Fatal("expected transport error")
	}
	want := "download request to https://example.com failed"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	assertNoSensitiveMarkers(t, err.Error(), allSensitiveMarkers()...)
	if strings.Contains(err.Error(), "token=") {
		t.Fatalf("error leaked token= marker: %s", err.Error())
	}
	if errors.Unwrap(err) != nil {
		t.Fatalf("error must not expose an unwrap chain: %v", errors.Unwrap(err))
	}
}

func TestDownloadFileCanceledRequest(t *testing.T) {
	u := New()
	u.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})}

	_, err := u.downloadFile(t.Context(), validURLWithMarkers())
	if err == nil {
		t.Fatal("expected canceled error")
	}
	want := "download request to https://example.com was canceled"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	assertNoSensitiveMarkers(t, err.Error(), allSensitiveMarkers()...)
}

func TestDownloadFileDeadlineExceeded(t *testing.T) {
	u := New()
	u.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}

	_, err := u.downloadFile(t.Context(), validURLWithMarkers())
	if err == nil {
		t.Fatal("expected deadline error")
	}
	want := "download request to https://example.com timed out"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	assertNoSensitiveMarkers(t, err.Error(), allSensitiveMarkers()...)
}

func TestDownloadFileMalformedRedirectDiscardsLocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Malformed redirect Location: %zz is an invalid escape, so Go places the
		// raw Location into the nested client.Do error unless we discard it.
		w.Header().Set("Location", "https://redirect.example/%zz?"+markerQueryKey+"="+markerRedirect)
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	u := New()
	u.client = server.Client()

	raw := server.URL + "/" + markerPath + "?" + markerQueryKey + "=" + markerQuery
	_, err := u.downloadFile(t.Context(), raw)
	if err == nil {
		t.Fatal("expected redirect error")
	}
	// The original server origin (host:port) is retained; the redirect target is not.
	if !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error = %q, want original server origin %q retained", err.Error(), server.URL)
	}
	if strings.Contains(err.Error(), "redirect.example") || strings.Contains(err.Error(), "%zz") {
		t.Fatalf("error leaked redirect location: %s", err.Error())
	}
	assertNoSensitiveMarkers(t, err.Error(), allSensitiveMarkers()...)
}

func TestDownloadFileBodyReadErrorDiscardsRawCause(t *testing.T) {
	u := New()
	u.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       errorReadCloser{err: errors.New(markerBody)},
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	_, err := u.downloadFile(t.Context(), validURLWithMarkers())
	if err == nil {
		t.Fatal("expected body read error")
	}
	want := "read download response from https://example.com failed"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	assertNoSensitiveMarkers(t, err.Error(), allSensitiveMarkers()...)
}

func TestDownloadFileRedactsURLInStatusErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	u := New()
	u.client = server.Client()

	raw := server.URL + "/" + markerPath + "?" + markerQueryKey + "=" + markerQuery
	_, err := u.downloadFile(t.Context(), raw)
	if err == nil {
		t.Fatal("expected status error")
	}
	if !strings.HasPrefix(err.Error(), "unexpected status 403 from ") {
		t.Fatalf("error = %q, want unexpected status prefix", err.Error())
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error = %q, want server origin retained", err.Error())
	}
	assertNoSensitiveMarkers(t, err.Error(), allSensitiveMarkers()...)
	if strings.Contains(err.Error(), "token=") {
		t.Fatalf("error leaked token=: %s", err.Error())
	}
}

func TestDownloadAndApplyRedactsInvalidDownloadURL(t *testing.T) {
	u := New()
	u.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New(markerTransport + ": " + req.URL.String())
	})}

	_, err := u.DownloadAndApply(t.Context(), &UpdateInfo{
		LatestVersion: "v0.2.0",
		AssetName:     "asset.tar.gz",
		DownloadURL:   validURLWithMarkers(),
	})
	if err == nil {
		t.Fatal("expected download error")
	}
	if !strings.HasPrefix(err.Error(), "download asset:") {
		t.Fatalf("error = %q, want download asset: prefix", err.Error())
	}
	// The only URL detail retained is the safe origin.
	if !strings.Contains(err.Error(), "https://example.com") {
		t.Fatalf("error = %q, want safe origin https://example.com", err.Error())
	}
	assertNoSensitiveMarkers(t, err.Error(), allSensitiveMarkers()...)
	if strings.Contains(err.Error(), "token=") {
		t.Fatalf("error leaked token=: %s", err.Error())
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
