package updater

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
		owner:   "wakeya",
		repo:    "magic-claude-code",
		baseURL: server.URL,
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

func TestGitHubSourceRejectsEmptyTagName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"","html_url":"https://example.com/release"}`))
	}))
	defer server.Close()

	src := &GitHubSource{
		owner:   "wakeya",
		repo:    "magic-claude-code",
		baseURL: server.URL,
	}

	_, err := src.FetchLatestRelease(t.Context(), server.Client())
	if err == nil {
		t.Fatal("expected empty tag_name error")
	}
	if !strings.Contains(err.Error(), "tag_name is empty") {
		t.Fatalf("error = %v, want empty tag_name error", err)
	}
}

func TestGitCodeSource_FetchLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/api/v5/repos/wakeya/magic-claude-code/releases/latest"
		if r.URL.Path != expectedPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
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

	assetURL := src.AssetURL("v0.3.0", "Magic-Claude-Code-v0.3.0-Linux-x86_64.tar.gz")
	expected := server.URL + "/api/v5/repos/wakeya/magic-claude-code/raw/dist/release/v0.3.0/Magic-Claude-Code-v0.3.0-Linux-x86_64.tar.gz"
	if assetURL != expected {
		t.Errorf("AssetURL: got %q, want %q", assetURL, expected)
	}
}

func TestGitCodeSourceRejectsEmptyTagName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"","html_url":"https://example.com/release"}`))
	}))
	defer server.Close()

	src := &GitCodeSource{
		owner:   "wakeya",
		repo:    "magic-claude-code",
		apiBase: server.URL,
	}

	_, err := src.FetchLatestRelease(t.Context(), server.Client())
	if err == nil {
		t.Fatal("expected empty tag_name error")
	}
	if !strings.Contains(err.Error(), "tag_name is empty") {
		t.Fatalf("error = %v, want empty tag_name error", err)
	}
}

func TestGiteeSource_FetchLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/repos/wakeya/magic-claude-code/releases"
		if r.URL.Path != expectedPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Authorization Bearer header, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{
			"tag_name": "v0.3.0",
			"html_url": "https://gitee.com/wakeya/magic-claude-code/releases/tag/v0.3.0"
		}]`))
	}))
	defer server.Close()

	src := &GiteeSource{
		owner:   "wakeya",
		repo:    "magic-claude-code",
		apiBase: server.URL,
		token:   "test-token",
	}

	info, err := src.FetchLatestRelease(t.Context(), server.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.TagName != "v0.3.0" {
		t.Errorf("expected tag v0.3.0, got %s", info.TagName)
	}

	assetURL := src.AssetURL("v0.3.0", "Magic-Claude-Code-v0.3.0-Linux-x86_64.tar.gz")
	expected := "https://gitee.com/wakeya/magic-claude-code/raw/main/dist/release/v0.3.0/Magic-Claude-Code-v0.3.0-Linux-x86_64.tar.gz"
	if assetURL != expected {
		t.Errorf("AssetURL: got %q, want %q", assetURL, expected)
	}

	headers := src.AssetHeaders()
	if headers["Authorization"] != "Bearer test-token" {
		t.Errorf("AssetHeaders: expected Bearer test-token, got %v", headers)
	}
}

func TestGiteeSourceRejectsEmptyReleaseList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer server.Close()

	src := &GiteeSource{
		owner:   "wakeya",
		repo:    "magic-claude-code",
		apiBase: server.URL,
	}

	_, err := src.FetchLatestRelease(t.Context(), server.Client())
	if err == nil {
		t.Fatal("expected no releases error")
	}
	if !strings.Contains(err.Error(), "no releases found") {
		t.Fatalf("error = %v, want no releases found", err)
	}
}

func TestGiteeSourceRejectsEmptyTagName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"tag_name":"","html_url":"https://example.com"}]`))
	}))
	defer server.Close()

	src := &GiteeSource{
		owner:   "wakeya",
		repo:    "magic-claude-code",
		apiBase: server.URL,
	}

	_, err := src.FetchLatestRelease(t.Context(), server.Client())
	if err == nil {
		t.Fatal("expected empty tag_name error")
	}
	if !strings.Contains(err.Error(), "tag_name is empty") {
		t.Fatalf("error = %v, want empty tag_name error", err)
	}
}
