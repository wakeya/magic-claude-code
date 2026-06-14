package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	baseURL string
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
	if body.TagName == "" {
		return nil, fmt.Errorf("github: release tag_name is empty")
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

func (s *GitHubSource) AssetURL(tag, assetName string) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", s.owner, s.repo, tag, assetName)
}

// GitCodeSource queries the GitCode Releases API for tag detection.
// Pre-compiled binaries are stored in the repo under dist/release/{tag}/
// and downloaded via the GitCode raw file API.
type GitCodeSource struct {
	owner   string
	repo    string
	apiBase string
	token   string
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
	if body.TagName == "" {
		return nil, fmt.Errorf("gitcode: release tag_name is empty")
	}

	if body.HTMLURL == "" {
		body.HTMLURL = fmt.Sprintf("https://gitcode.com/%s/%s/releases/tag/%s", s.owner, s.repo, body.TagName)
	}

	return &ReleaseInfo{
		TagName: body.TagName,
		HTMLURL: body.HTMLURL,
	}, nil
}

func (s *GitCodeSource) AssetURL(tag, assetName string) string {
	return fmt.Sprintf("%s/repos/%s/%s/raw/dist/release/%s/%s", s.apiBase, s.owner, s.repo, tag, assetName)
}

func (s *GitCodeSource) AssetHeaders() map[string]string {
	if s.token == "" {
		return nil
	}
	return map[string]string{"PRIVATE-TOKEN": s.token}
}

// GiteeSource queries the Gitee Releases API for tag detection.
// Pre-compiled binaries are stored in the repo under dist/release/{tag}/
// and downloaded via the Gitee raw file URL.
type GiteeSource struct {
	owner   string
	repo    string
	apiBase string
	token   string
}

func NewGiteeSource(owner, repo, token string) *GiteeSource {
	return &GiteeSource{
		owner:   owner,
		repo:    repo,
		apiBase: "https://gitee.com/api/v5",
		token:   token,
	}
}

func (s *GiteeSource) Name() string { return "gitee" }

func (s *GiteeSource) FetchLatestRelease(ctx context.Context, client *http.Client) (*ReleaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?page=1&per_page=1", s.apiBase, s.owner, s.repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("gitee: build request: %w", err)
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitee: fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitee: unexpected status %d", resp.StatusCode)
	}

	var body []struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("gitee: decode response: %w", err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("gitee: no releases found")
	}
	if body[0].TagName == "" {
		return nil, fmt.Errorf("gitee: release tag_name is empty")
	}

	htmlURL := body[0].HTMLURL
	if htmlURL == "" {
		htmlURL = fmt.Sprintf("https://gitee.com/%s/%s/releases/tag/%s", s.owner, s.repo, body[0].TagName)
	}

	return &ReleaseInfo{
		TagName: body[0].TagName,
		HTMLURL: htmlURL,
	}, nil
}

func (s *GiteeSource) AssetURL(tag, assetName string) string {
	return fmt.Sprintf("https://gitee.com/%s/%s/raw/main/dist/release/%s/%s", s.owner, s.repo, tag, assetName)
}

func (s *GiteeSource) AssetHeaders() map[string]string {
	if s.token == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + s.token}
}
