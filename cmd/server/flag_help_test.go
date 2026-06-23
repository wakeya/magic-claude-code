package main

import (
	"strings"
	"testing"

	"magic-claude-code/internal/i18n"
	"magic-claude-code/internal/version"
)

func TestVersionOutputFormat(t *testing.T) {
	// versionOutput returns "mcc <version>" — the program name plus the
	// build-injected version, e.g. "mcc v0.8.1".
	// During test the version is the default "dev" (no ldflags), so we
	// just verify the "mcc <version>" shape.
	got := versionOutput()
	want := "mcc " + version.Version
	if got != want {
		t.Errorf("versionOutput() = %q, want %q", got, want)
	}
}

func TestVersionOutputContainsMCCPrefix(t *testing.T) {
	if !strings.HasPrefix(versionOutput(), "mcc ") {
		t.Errorf("versionOutput() = %q, want it to start with 'mcc '", versionOutput())
	}
}

func TestEnMessagesFlagFieldsNonEmpty(t *testing.T) {
	msg := i18n.Load("en")
	for _, field := range []struct {
		name, val string
	}{
		{"FlagDataDir", msg.FlagDataDir},
		{"FlagPassword", msg.FlagPassword},
		{"FlagProxyListen", msg.FlagProxyListen},
		{"FlagProxyPort", msg.FlagProxyPort},
		{"FlagAdminListen", msg.FlagAdminListen},
		{"FlagAdminPort", msg.FlagAdminPort},
	} {
		if strings.TrimSpace(field.val) == "" {
			t.Errorf("en: %s is empty", field.name)
		}
	}
}

func TestZhMessagesFlagFieldsNonEmpty(t *testing.T) {
	msg := i18n.Load("zh")
	for _, field := range []struct {
		name, val string
	}{
		{"FlagDataDir", msg.FlagDataDir},
		{"FlagPassword", msg.FlagPassword},
		{"FlagProxyListen", msg.FlagProxyListen},
		{"FlagProxyPort", msg.FlagProxyPort},
		{"FlagAdminListen", msg.FlagAdminListen},
		{"FlagAdminPort", msg.FlagAdminPort},
	} {
		if strings.TrimSpace(field.val) == "" {
			t.Errorf("zh: %s is empty", field.name)
		}
	}
}
