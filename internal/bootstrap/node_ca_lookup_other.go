//go:build !windows && !darwin

package bootstrap

import "os"

func lookupPersistedNodeCACertOS() (string, bool, error) {
	value, exists := os.LookupEnv("NODE_EXTRA_CA_CERTS")
	return value, exists && value != "", nil
}
