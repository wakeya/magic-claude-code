package main

import (
	"os"
	"strconv"

	"magic-claude-code/internal/config"
)

// listenOverride holds listen address and port values from a single source
// (CLI flag or environment variable). Zero/empty means "not provided."
type listenOverride struct {
	ProxyAddr string
	ProxyPort int
	AdminAddr string
	AdminPort int
}

// overrideString picks the first non-empty override in flag→env→current order.
func overrideString(flag, env, current string) string {
	if flag != "" {
		return flag
	}
	if env != "" {
		return env
	}
	return current
}

// overrideInt picks the first non-zero override in flag→env→current order.
func overrideInt(flag, env, current int) int {
	if flag != 0 {
		return flag
	}
	if env != 0 {
		return env
	}
	return current
}

// applyListenConfig applies CLI flag and environment variable overrides to the
// listen address/port fields of cfg. Priority: CLI flag > env var > config
// file (already populated from SQLite or JSON defaults). A zero/empty value
// in flag or env means "not provided" and is skipped.
func applyListenConfig(cfg *config.Config, flag, env listenOverride) {
	cfg.ProxyListenAddr = overrideString(flag.ProxyAddr, env.ProxyAddr, cfg.ProxyListenAddr)
	cfg.ProxyPort = overrideInt(flag.ProxyPort, env.ProxyPort, cfg.ProxyPort)
	cfg.AdminListenAddr = overrideString(flag.AdminAddr, env.AdminAddr, cfg.AdminListenAddr)
	cfg.AdminPort = overrideInt(flag.AdminPort, env.AdminPort, cfg.AdminPort)
}

// envIntOrZero reads an environment variable and parses it as an int.
// Returns 0 if the variable is unset or cannot be parsed (meaning "not provided").
func envIntOrZero(key string) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return 0
	}
	return v
}
