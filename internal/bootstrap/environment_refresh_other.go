//go:build !windows

package bootstrap

// broadcastEnvironmentChangeOS is a portable no-op used by tests that exercise
// the Windows adapter logic on non-Windows builders.
func broadcastEnvironmentChangeOS() error { return nil }
