package admin

import (
	"bytes"
	"encoding/json"
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
