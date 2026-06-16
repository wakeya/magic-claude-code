package main

import (
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
