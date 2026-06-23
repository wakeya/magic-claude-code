package main

import (
	"os"
	"path/filepath"
	"testing"

	"magic-claude-code/internal/i18n"
)

func TestResolveAdminPasswordUsesProvidedPassword(t *testing.T) {
	got := resolveAdminPassword("secret", i18n.Messages{}, func(i18n.Messages) string {
		t.Fatal("generate should not be called")
		return ""
	})

	if got.Value != "secret" || got.RandomGenerated {
		t.Fatalf("resolveAdminPassword() = %#v", got)
	}
}

func TestResolveAdminPasswordGeneratesWhenEmpty(t *testing.T) {
	got := resolveAdminPassword("", i18n.Messages{}, func(i18n.Messages) string {
		return "generated-secret"
	})

	if got.Value != "generated-secret" || !got.RandomGenerated {
		t.Fatalf("resolveAdminPassword() = %#v", got)
	}
}

func TestResolveDataDirFromExecutablePathUsesExecutableDirectory(t *testing.T) {
	exePath := filepath.Join(string(os.PathSeparator), "opt", "mcc", "mcc")
	got := resolveDataDirFromExecutablePath(exePath)
	want := filepath.Join(string(os.PathSeparator), "opt", "mcc", "data")
	if got != want {
		t.Fatalf("resolveDataDirFromExecutablePath() = %q, want %q", got, want)
	}
}

func TestResolveDataDirPrefersMCCRoot(t *testing.T) {
	t.Setenv("MCC_ROOT", filepath.Join(string(os.PathSeparator), "srv", "mcc"))
	got := resolveDataDir("")
	want := filepath.Join(string(os.PathSeparator), "srv", "mcc", "data")
	if got != want {
		t.Fatalf("resolveDataDir() = %q, want %q", got, want)
	}
}
