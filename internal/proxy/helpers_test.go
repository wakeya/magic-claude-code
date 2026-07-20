package proxy

import "testing"

func TestFormatModelLog(t *testing.T) {
	tests := []struct {
		name          string
		originalModel string
		mappedModel   string
		exposedLabel  string
		want          string
	}{
		{"exposed label replaces em id", "em-fceb31a8", "k3", "Kimi K3", "Kimi K3 -> k3"},
		{"label equals backend collapses to single token", "em-deadbeef", "k3", "k3", "k3"},
		{"default route keeps original with mapping", "claude-opus-4-8", "glm-5.2", "", "claude-opus-4-8 -> glm-5.2"},
		{"unmapped default collapses", "claude-opus-4-8", "claude-opus-4-8", "", "claude-opus-4-8"},
		{"empty label falls back to original", "em-x", "k3", "", "em-x -> k3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatModelLog(tt.originalModel, tt.mappedModel, tt.exposedLabel); got != tt.want {
				t.Fatalf("formatModelLog(%q, %q, %q) = %q, want %q",
					tt.originalModel, tt.mappedModel, tt.exposedLabel, got, tt.want)
			}
		})
	}
}
