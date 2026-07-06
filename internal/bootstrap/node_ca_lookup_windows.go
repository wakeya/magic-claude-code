//go:build windows

package bootstrap

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

func lookupPersistedNodeCACertOS() (string, bool, error) {
	if value, exists := os.LookupEnv("NODE_EXTRA_CA_CERTS"); exists && value != "" {
		return value, true, nil
	}

	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("open HKCU Environment: %w", err)
	}
	defer key.Close()

	value, _, err := key.GetStringValue("NODE_EXTRA_CA_CERTS")
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read HKCU Environment NODE_EXTRA_CA_CERTS: %w", err)
	}
	return value, value != "", nil
}
