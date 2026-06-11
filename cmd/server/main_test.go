package main

import "testing"

func TestResolveAdminPasswordUsesProvidedPassword(t *testing.T) {
	got := resolveAdminPassword("secret", func() string {
		t.Fatal("generate should not be called")
		return ""
	})

	if got.Value != "secret" || got.RandomGenerated {
		t.Fatalf("resolveAdminPassword() = %#v", got)
	}
}

func TestResolveAdminPasswordGeneratesWhenEmpty(t *testing.T) {
	got := resolveAdminPassword("", func() string {
		return "generated-secret"
	})

	if got.Value != "generated-secret" || !got.RandomGenerated {
		t.Fatalf("resolveAdminPassword() = %#v", got)
	}
}
