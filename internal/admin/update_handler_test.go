package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"magic-claude-code/internal/updater"
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

func TestHandleUpdateCheck_DockerEnv(t *testing.T) {
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

func TestHandleUpdateCheck_WrongMethod(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/update/check", nil)
	rr := httptest.NewRecorder()
	s.handleUpdateCheck(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleUpdateApply_NoUpdater(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", nil)
	rr := httptest.NewRecorder()
	s.handleUpdateApply(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}

func TestHandleUpdateApply_WrongMethod(t *testing.T) {
	s := &Server{updater: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/update/apply", nil)
	rr := httptest.NewRecorder()
	s.handleUpdateApply(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleUpdateApply_DisabledReturnsBusinessError(t *testing.T) {
	s := &Server{}
	s.DisableUpdateApply("Docker 环境不支持应用内自更新，请通过更新镜像并重新创建容器完成升级。")

	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", nil)
	rr := httptest.NewRecorder()
	s.handleUpdateApply(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 business error, got %d", rr.Code)
	}

	var resp updateApplyResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if resp.Restarting {
		t.Fatal("expected restarting=false")
	}
	if resp.Error == "" {
		t.Fatal("expected disabled update message")
	}
}

func TestWriteUpdateApplyJSONIncludesExplicitRestartingFalse(t *testing.T) {
	rr := httptest.NewRecorder()
	writeUpdateApplyJSON(rr, http.StatusOK, updateApplyResponse{
		Success:    true,
		NewVersion: "v0.2.0",
		Message:    "restart manually",
		Restarting: false,
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"restarting":false`)) {
		t.Fatalf("response %s does not include explicit restarting=false", rr.Body.String())
	}
}

// fakeUpdateReleaseSource is a package-local updater.ReleaseSource whose asset
// URL points at a local httptest server, so the admin handler exercises the
// updater download path without any public-network access.
type fakeUpdateReleaseSource struct {
	assetURL string
}

func (s fakeUpdateReleaseSource) Name() string { return "fake" }

func (s fakeUpdateReleaseSource) FetchLatestRelease(context.Context, *http.Client) (*updater.ReleaseInfo, error) {
	return &updater.ReleaseInfo{
		TagName: "v9.9.9",
		HTMLURL: "https://example.com/releases/v9.9.9",
	}, nil
}

func (s fakeUpdateReleaseSource) AssetURL(tag, assetName string) string {
	return s.assetURL
}

// TestHandleUpdateApplyRedactsMalformedRedirect drives the actual security sink:
// the JSON error returned by POST /api/update/apply must not contain the raw
// redirect Location, userinfo, path, query, fragment, or any underlying-error
// marker after a malformed redirect forces a nested client.Do failure.
func TestHandleUpdateApplyRedactsMalformedRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// %zz is an invalid escape; Go nests the raw Location into client.Do's
		// error, which is the leak vector this handler must close.
		w.Header().Set("Location", "https://redirect.example/%zz?query-key=redirect-secret")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	// The asset URL carries userinfo, path, query and fragment markers so the
	// sink test exercises every URL-side leak vector, not just path/query.
	host := strings.TrimPrefix(server.URL, "http://")
	assetURL := "http://username-secret:password-secret@" + host +
		"/path-secret?query-key=query-secret#fragment-secret"

	src := fakeUpdateReleaseSource{assetURL: assetURL}
	u := updater.New(src)
	s := &Server{}
	s.SetUpdater(u)

	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", nil)
	rr := httptest.NewRecorder()
	s.handleUpdateApply(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 business error, got %d", rr.Code)
	}

	// Capture the body BEFORE decoding: json.Decoder consumes the buffer, so a
	// later rr.Body.String() would return "" and make the marker checks below
	// vacuously pass even if the response leaked secrets.
	body := rr.Body.String()
	if body == "" || !strings.Contains(body, "download asset:") {
		t.Fatalf("response body missing expected redacted error: %q", body)
	}
	var resp updateApplyResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if resp.Error == "" || !strings.Contains(resp.Error, "download asset:") {
		t.Fatalf("error = %q, want non-empty download asset: context", resp.Error)
	}

	// The full JSON body is the actual security sink; none of the userinfo,
	// path, query, fragment, redirect, or underlying-error markers may appear.
	for _, marker := range []string{
		"username-secret", "password-secret", "path-secret",
		"query-key", "query-secret", "fragment-secret",
		"redirect-secret", "redirect.example", "%zz",
		"transport-secret", "body-secret", "token=",
	} {
		if strings.Contains(body, marker) {
			t.Fatalf("response body leaked %q: %s", marker, body)
		}
	}
}
