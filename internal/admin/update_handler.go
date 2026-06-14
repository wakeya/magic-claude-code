package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type updateCheckResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	Source          string `json:"source,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	Error           string `json:"error,omitempty"`
}

type updateApplyResponse struct {
	Success    bool   `json:"success"`
	NewVersion string `json:"new_version,omitempty"`
	Message    string `json:"message,omitempty"`
	Restarting bool   `json:"restarting"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

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

	if s.updateApplyDisabledMessage != "" {
		writeUpdateApplyJSON(w, http.StatusOK, updateApplyResponse{
			Error:      s.updateApplyDisabledMessage,
			Restarting: false,
		})
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
		Restarting: result.Restarting,
	})

	if result.Restarting {
		go func() {
			time.Sleep(500 * time.Millisecond)
			s.updater.SignalRestart()
		}()
	}
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
