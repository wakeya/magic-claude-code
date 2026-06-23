package main

import (
	"flag"
	"fmt"
	"os"

	"magic-claude-code/internal/version"
)

// versionOutput returns the "mcc <version>" string for --version output.
// The version is injected at build time via ldflags (see internal/version).
func versionOutput() string {
	return fmt.Sprintf("mcc %s", version.Version)
}

// setupFlagUsage replaces the default flag.Usage with a localized version
// that prints the program name + version header followed by all registered
// flags (whose descriptions come from the i18n Messages struct).
func setupFlagUsage() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "%s\n\n", versionOutput())
		flag.PrintDefaults()
	}
}
