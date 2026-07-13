package admin

import (
	"encoding/json"
	"net/http"

	"magic-claude-code/internal/config"
)

type preferencesResponse struct {
	ThemeMode string `json:"theme_mode"`
	Success   bool   `json:"success,omitempty"`
}

func (s *Server) handlePreferences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getPreferences(w)
	case http.MethodPut:
		s.updatePreferences(w, r)
	default:
		writePreferencesError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) getPreferences(w http.ResponseWriter) {
	if s.configStore == nil {
		writePreferencesError(w, http.StatusInternalServerError, "config store not available")
		return
	}

	cfg, err := s.configStore.Load()
	if err != nil {
		writePreferencesError(w, http.StatusInternalServerError, "failed to load preferences")
		return
	}

	writePreferencesJSON(w, http.StatusOK, preferencesResponse{
		ThemeMode: config.NormalizeThemeMode(cfg.AdminThemeMode),
	})
}

func (s *Server) updatePreferences(w http.ResponseWriter, r *http.Request) {
	if s.configStore == nil {
		writePreferencesError(w, http.StatusInternalServerError, "config store not available")
		return
	}

	var req struct {
		ThemeMode string `json:"theme_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePreferencesError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.ThemeMode != config.ThemeModeLight && req.ThemeMode != config.ThemeModeDark {
		writePreferencesError(w, http.StatusBadRequest, "invalid theme_mode")
		return
	}

	if _, err := s.configStore.Update(func(cfg *config.Config) error {
		cfg.AdminThemeMode = req.ThemeMode
		return nil
	}); err != nil {
		writePreferencesError(w, http.StatusInternalServerError, "failed to save preferences")
		return
	}

	writePreferencesJSON(w, http.StatusOK, preferencesResponse{
		Success:   true,
		ThemeMode: req.ThemeMode,
	})
}

func writePreferencesJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writePreferencesError(w http.ResponseWriter, status int, message string) {
	writePreferencesJSON(w, status, map[string]string{"error": message})
}
