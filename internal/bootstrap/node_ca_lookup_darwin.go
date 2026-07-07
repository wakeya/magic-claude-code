//go:build darwin

package bootstrap

import (
	"fmt"
	"os"
	"strings"
)

func lookupPersistedNodeCACertOS() (string, bool, error) {
	if value, exists := os.LookupEnv("NODE_EXTRA_CA_CERTS"); exists && value != "" {
		return value, true, nil
	}
	if !hasLaunchctl() {
		return "", false, nil
	}

	out, err := execWithTimeout("launchctl", "getenv", "NODE_EXTRA_CA_CERTS")
	if err != nil {
		return "", false, fmt.Errorf("launchctl getenv NODE_EXTRA_CA_CERTS: %w: %s", err, decodeCmdOutput(out))
	}
	value := strings.TrimRight(string(out), "\r\n")
	return value, value != "", nil
}
