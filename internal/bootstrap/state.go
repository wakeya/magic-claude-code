package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

type persistedState struct {
	Hash         string `json:"hash"`
	SelectedMode Mode   `json:"selected_mode"`
}

func stateHash(r Result) string {
	h := sha256.New()
	data := struct {
		HostsOK  bool   `json:"h"`
		TrustOK  bool   `json:"t"`
		EnvOK    bool   `json:"e"`
		Mode     Mode   `json:"m"`
		PrefMode Mode   `json:"pm"`
		Docker   bool   `json:"d"`
		Helper   bool   `json:"helper"`
		HostsErr string `json:"he"`
		TrustErr string `json:"te"`
		EnvErr   string `json:"ee"`
	}{
		HostsOK:  r.HostsResult.Success,
		TrustOK:  r.TrustResult.Success,
		EnvOK:    r.EnvResult.Success,
		Mode:     r.SelectedMode,
		PrefMode: r.PreferredMode,
		Docker:   r.Caps.IsDocker,
		Helper:   r.Caps.HasHostHelper,
		HostsErr: errString(r.HostsResult.Err),
		TrustErr: errString(r.TrustResult.Err),
		EnvErr:   errString(r.EnvResult.Err),
	}
	jsonData, _ := json.Marshal(data)
	h.Write(jsonData)
	return hex.EncodeToString(h.Sum(nil))
}

// shouldSuppress returns true if the current result matches the previously persisted state,
// so the same long instruction block is not printed on every launch.
func shouldSuppress(statePath string, r Result) bool {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return false
	}
	var prev persistedState
	if err := json.Unmarshal(data, &prev); err != nil {
		return false
	}
	return prev.Hash == stateHash(r)
}

// saveState writes the current bootstrap state so the next launch can compare.
func saveState(statePath string, r Result) {
	state := persistedState{
		Hash:         stateHash(r),
		SelectedMode: r.SelectedMode,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("bootstrap saveState: marshal error: %v", err)
		return
	}
	dir := filepath.Dir(statePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("bootstrap saveState: mkdir %s: %v", dir, err)
		return
	}
	if err := os.WriteFile(statePath, data, 0644); err != nil {
		log.Printf("bootstrap saveState: write %s: %v", statePath, err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
