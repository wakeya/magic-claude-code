package version

// Version is set at build time via -ldflags "-X magic-claude-code/internal/version.Version=v0.1.0".
// Defaults to "dev" for local builds.
var Version = "dev"
