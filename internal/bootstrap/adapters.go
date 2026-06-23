package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const externalCmdTimeout = 30 * time.Second

func execWithTimeout(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), externalCmdTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

var isDockerEnvFn = isDockerEnv

// osHostsAdapter handles real hosts-file modification.
type osHostsAdapter struct{}

func newOSHostsAdapter() HostsAdapter { return &osHostsAdapter{} }

func (a *osHostsAdapter) EnsureHostMapping(domain, ip string) error {
	if isDockerEnvFn() {
		if helperPath := os.Getenv("MCC_HOST_HELPER"); helperPath != "" {
			if err := runHostHelper(helperPath, "hosts", "add", domain, ip); err != nil {
				return fmt.Errorf("host helper hosts add: %w", err)
			}
			return nil
		}
		return fmt.Errorf("docker host helper not configured")
	}

	hostsPath := getHostsPath()
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return fmt.Errorf("read hosts: %w", err)
	}

	newContent, changed := processHostsContent(string(content), domain, ip)
	if !changed {
		return nil
	}

	if err := os.WriteFile(hostsPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("write hosts: %w", err)
	}
	return nil
}

// HasMapping reports whether the hosts file already maps domain to ip.
// Read-only — no elevated privileges required.
// On Docker, the container /etc/hosts is isolated from the host's, so we
// delegate to the host helper (which checks the data-dir marker written by
// setup-host.sh). This keeps HasMapping symmetric with EnsureHostMapping:
// both reflect host state, not container state.
func (a *osHostsAdapter) HasMapping(domain, ip string) bool {
	if isDockerEnvFn() {
		helperPath := os.Getenv("MCC_HOST_HELPER")
		if helperPath == "" {
			return false
		}
		return runHostHelper(helperPath, "hosts", "add", domain, ip) == nil
	}
	content, err := os.ReadFile(getHostsPath())
	if err != nil {
		return false
	}
	_, changed := processHostsContent(string(content), domain, ip)
	return !changed
}

// processHostsContent removes all existing mappings for domain that point to a
// different IP, then appends the correct ip→domain mapping if it was missing.
// Returns the new content and whether it changed (by comparing final vs original).
func processHostsContent(content, domain, ip string) (string, bool) {
	if strings.TrimSpace(content) == "" {
		return ip + " " + domain + "\n", true
	}

	normalized := strings.TrimRight(content, "\n")
	lines := strings.Split(normalized, "\n")
	var result []string
	correctExists := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(result) == 0 {
				continue
			}
			result = append(result, line)
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			result = append(result, line)
			continue
		}

		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			result = append(result, line)
			continue
		}

		lineIP := fields[0]
		var remaining []string
		domainInLine := false
		for _, h := range fields[1:] {
			if h == domain {
				domainInLine = true
				if lineIP == ip {
					correctExists = true
				}
			} else {
				remaining = append(remaining, h)
			}
		}

		if domainInLine && lineIP != ip {
			if len(remaining) > 0 {
				result = append(result, lineIP+" "+strings.Join(remaining, " "))
			}
		} else {
			result = append(result, line)
		}
	}

	if !correctExists {
		result = append(result, ip+" "+domain)
	}

	output := strings.Join(result, "\n") + "\n"
	changed := output != normalized+"\n"
	return output, changed
}

// osTrustAdapter handles real CA trust-store installation.
type osTrustAdapter struct{}

func newOSTrustAdapter() TrustAdapter { return &osTrustAdapter{} }

// caTrustMarkerName is the marker file written to dataDir after successful CA
// installation. Bootstrap reads it to skip re-installation on subsequent
// non-privileged launches (first run as admin, later runs as normal user).
// docker-host-helper.sh also checks this marker for Docker transparent mode.
const caTrustMarkerName = ".ca-trust-installed"

// caTrustMarker is the on-disk JSON shape of the marker file.
type caTrustMarker struct {
	Action      string `json:"action"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
}

// caFingerprint returns the hex-encoded SHA256 of the raw cert file bytes.
// Used to detect CA regeneration: if the cert changes but the marker still
// references the old fingerprint, the marker is stale and CA must be reinstalled.
func caFingerprint(certPath string) (string, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// hasCATrustMarker reports whether the CA-installation marker exists in dataDir
// AND its recorded fingerprint matches the current CA cert. Fingerprint mismatch
// (e.g. after cert regeneration) yields false so the caller reinstalls.
// On Docker, setup-host.sh writes the marker without a fingerprint; in that case
// hasCATrustMarker returns false and the caller falls through to the host helper.
func hasCATrustMarker(dataDir, caCertPath string) bool {
	if dataDir == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, caTrustMarkerName))
	if err != nil {
		return false
	}
	var m caTrustMarker
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	if m.Fingerprint == "" {
		return false // legacy marker without fingerprint — fall through to install/helper
	}
	current, err := caFingerprint(caCertPath)
	if err != nil {
		return false // cert unreadable — cannot confirm, fall through
	}
	return m.Fingerprint == current
}

// writeCATrustMarker records that CA installation succeeded, embedding the
// current cert fingerprint so future launches can detect staleness.
// Best-effort — failure to write the marker does not block the current run.
func writeCATrustMarker(dataDir, caCertPath string) {
	if dataDir == "" {
		return
	}
	fp, err := caFingerprint(caCertPath)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return
	}
	m := caTrustMarker{
		Action:      "ca-trust-installed",
		Fingerprint: fp,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dataDir, caTrustMarkerName), data, 0644)
}

func (a *osTrustAdapter) InstallCA(certPath string) error {
	if isDockerEnvFn() {
		if helperPath := os.Getenv("MCC_HOST_HELPER"); helperPath != "" {
			if err := runHostHelper(helperPath, "trust", "install", certPath); err != nil {
				return fmt.Errorf("host helper trust install: %w", err)
			}
			return nil
		}
		return fmt.Errorf("docker host helper not configured")
	}

	switch runtime.GOOS {
	case "darwin":
		return a.installDarwin(certPath)
	case "windows":
		return a.installWindows(certPath)
	default:
		return a.installLinux(certPath)
	}
}

func (a *osTrustAdapter) installDarwin(certPath string) error {
	if _, err := exec.LookPath("security"); err != nil {
		return fmt.Errorf("security command not found: %w", err)
	}
	out, err := execWithTimeout("security", "add-trusted-cert", "-d",
		"-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", certPath)
	if err != nil {
		return fmt.Errorf("security add-trusted-cert (permission denied or not root): %w: %s", err, string(out))
	}
	return nil
}

func (a *osTrustAdapter) installWindows(certPath string) error {
	out, err := execWithTimeout("certutil", "-addstore", "-f", "ROOT", certPath)
	if err != nil {
		return fmt.Errorf("certutil -addstore: %w: %s", err, decodeCmdOutput(out))
	}
	return nil
}

func (a *osTrustAdapter) installLinux(certPath string) error {
	if _, err := exec.LookPath("update-ca-certificates"); err == nil {
		destDir := "/usr/local/share/ca-certificates"
		dest := destDir + "/mcc-proxy-ca.crt"
		data, err := os.ReadFile(certPath)
		if err != nil {
			return fmt.Errorf("read ca cert: %w", err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("write ca cert to %s (permission denied?): %w", dest, err)
		}
		out, err := execWithTimeout("update-ca-certificates")
		if err != nil {
			return fmt.Errorf("update-ca-certificates (permission denied or not root): %w: %s", err, string(out))
		}
		return nil
	}
	if _, err := exec.LookPath("update-ca-trust"); err == nil {
		dest := "/etc/pki/ca-trust/source/anchors/mcc-proxy-ca.pem"
		data, err := os.ReadFile(certPath)
		if err != nil {
			return fmt.Errorf("read ca cert: %w", err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("write ca cert to %s (permission denied?): %w", dest, err)
		}
		out, err := execWithTimeout("update-ca-trust", "extract")
		if err != nil {
			return fmt.Errorf("update-ca-trust (permission denied or not root): %w: %s", err, string(out))
		}
		return nil
	}
	return fmt.Errorf("no supported CA trust tool found (tried update-ca-certificates, update-ca-trust)")
}

// osEnvAdapter handles real environment persistence.
type osEnvAdapter struct{}

func newOSEnvAdapter() EnvAdapter { return &osEnvAdapter{} }

func (a *osEnvAdapter) PersistRoot(rootDir string) error {
	switch runtime.GOOS {
	case "windows":
		out, err := execWithTimeout("setx", "MCC_ROOT", rootDir)
		if err != nil {
			return fmt.Errorf("setx MCC_ROOT: %w: %s", err, decodeCmdOutput(out))
		}
		return nil
	default:
		shell := os.Getenv("SHELL")
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("user home dir: %w", err)
		}

		entry := shellExportEntry(shell, "MCC_ROOT", rootDir)
		profiles := resolveShellProfiles(shell, home)
		openProfile := func(p string) (writeCloser, error) {
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
			}
			return os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		}
		var lastErr error
		for _, profile := range profiles {
			if existing, rErr := os.ReadFile(profile); rErr == nil {
				content := string(existing)
				if profileHasEquivalentEntry(shell, content, "MCC_ROOT", rootDir) ||
					profileHasExactEntry(content, entry) {
					return nil
				}
			}
			if err := writeProfileEntry(openProfile, profile, entry); err != nil {
				lastErr = err
				continue
			}
			return nil
		}
		if lastErr != nil {
			return lastErr
		}
		return fmt.Errorf("no profile file writable (tried %v)", profiles)
	}
}

// writeCloser is the minimal interface writeProfileEntry needs from a profile
// file. *os.File satisfies it; tests inject a fake to simulate Close errors.
type writeCloser interface {
	Write(p []byte) (n int, err error)
	Close() error
}

// writeProfileEntry opens profile via open, appends entry, and checks both the
// Write and Close errors so a failed flush is never silently treated as success.
func writeProfileEntry(open func(string) (writeCloser, error), profile, entry string) error {
	f, err := open(profile)
	if err != nil {
		return fmt.Errorf("open %s: %w", profile, err)
	}
	if _, err := f.Write([]byte(entry)); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", profile, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", profile, err)
	}
	return nil
}

// profileHasExactEntry reports whether content already contains a full line
// that equals entry after both sides are TrimSpace'd. A commented-out line
// (e.g. "# export MCC_ROOT=...") is intentionally NOT treated as a match.
func profileHasExactEntry(content, entry string) bool {
	target := strings.TrimSpace(entry)
	if target == "" {
		return false
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}

// profileHasEquivalentEntry reports whether content already contains a
// shell-appropriate line that exports key with the given value, treating
// `export KEY=v`, `export KEY="v"`, `KEY=v`, `KEY="v"`, `KEY='v'` (and the
// fish equivalents `set -x KEY v`, `set -gx KEY v`, `set --export KEY v`) as
// duplicates of each other. Comment lines and blank lines are skipped.
func profileHasEquivalentEntry(shell, content, key, value string) bool {
	if key == "" {
		return false
	}
	isFish := strings.Contains(shell, "fish")
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isFish {
			if fishLineMatches(line, key, value) {
				return true
			}
			continue
		}
		if shLineMatches(line, key, value) {
			return true
		}
	}
	return false
}

// shLineMatches handles POSIX-shell (bash/zsh/unknown) assignments:
//
//	export KEY=value
//	export KEY="value"
//	export KEY='value'
//	KEY=value
//	KEY="value"
func shLineMatches(line, key, value string) bool {
	rest := strings.TrimPrefix(line, "export ")
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, key+"=") {
		return false
	}
	return unquoteValue(strings.TrimPrefix(rest, key+"=")) == value
}

// fishLineMatches handles fish shell exports such as:
//
//	set -x KEY value
//	set -gx KEY value
//	set --export KEY value
//
// A small scanner is used instead of strings.Fields so quoted values, escaped
// characters, and trailing comments can be handled without losing value
// boundaries. Explicit export flags are required; local / erase / unexport
// forms are rejected. Ambiguous fish list syntax is still treated as non-match.
func fishLineMatches(line, key, value string) bool {
	parsed, ok := parseFishExportLine(line)
	if !ok || !parsed.hasExport || parsed.key != key {
		return false
	}
	if parsed.valueTokenCount != 1 {
		return false
	}
	return parsed.value == value
}

type fishToken struct {
	text   string
	quoted bool
}

type fishExportLine struct {
	hasExport       bool
	key             string
	value           string
	valueQuoted     bool
	valueTokenCount int
}

// parseFishExportLine scans the subset of fish syntax that matters for MCC
// profile de-duplication. It preserves quoted values and escape characters,
// strips trailing inline comments, and fails closed on malformed input.
func parseFishExportLine(line string) (fishExportLine, bool) {
	line = stripFishComment(line)
	tokens := scanFishTokens(line)
	if len(tokens) < 4 || tokens[0].text != "set" {
		return fishExportLine{}, false
	}

	out := fishExportLine{}
	idx := 1
	for idx < len(tokens) {
		switch tokens[idx].text {
		case "-x", "-gx", "--export":
			out.hasExport = true
			idx++
		default:
			if strings.HasPrefix(tokens[idx].text, "-") {
				return fishExportLine{}, false
			}
			goto key
		}
	}

key:
	if !out.hasExport || idx >= len(tokens) {
		return fishExportLine{}, false
	}
	out.key = tokens[idx].text
	idx++
	if idx >= len(tokens) {
		return fishExportLine{}, false
	}

	out.valueTokenCount = len(tokens) - idx
	valueTokens := tokens[idx:]
	var b strings.Builder
	for i, tok := range valueTokens {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(tok.text)
		if tok.quoted {
			out.valueQuoted = true
		}
	}
	out.value = b.String()
	return out, true
}

// scanFishTokens tokenizes a fish line while preserving quoted spans and
// backslash escapes inside the returned token text.
func scanFishTokens(line string) []fishToken {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var tokens []fishToken
	var buf strings.Builder
	inSingle := false
	inDouble := false
	tokenQuoted := false
	tokenStarted := false

	flush := func() {
		if !tokenStarted {
			tokenQuoted = false
			return
		}
		tokens = append(tokens, fishToken{text: buf.String(), quoted: tokenQuoted})
		buf.Reset()
		tokenQuoted = false
		tokenStarted = false
	}

	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inSingle:
			if c == '\\' && i+1 < len(line) && (line[i+1] == '\'' || line[i+1] == '\\') {
				tokenStarted = true
				buf.WriteByte(line[i+1])
				i++
				continue
			}
			if c == '\'' {
				inSingle = false
				continue
			}
			tokenStarted = true
			buf.WriteByte(c)
		case inDouble:
			if c == '\\' && i+1 < len(line) {
				next := line[i+1]
				if next == '\\' || next == '"' || next == '$' {
					tokenStarted = true
					buf.WriteByte(next)
					i++
					tokenQuoted = true
					continue
				}
				tokenStarted = true
				buf.WriteByte(c)
				continue
			}
			if c == '"' {
				inDouble = false
				continue
			}
			tokenStarted = true
			buf.WriteByte(c)
		default:
			if c == ' ' || c == '\t' {
				flush()
				continue
			}
			if c == '\'' {
				inSingle = true
				tokenQuoted = true
				tokenStarted = true
				continue
			}
			if c == '"' {
				inDouble = true
				tokenQuoted = true
				tokenStarted = true
				continue
			}
			if c == '\\' && i+1 < len(line) {
				tokenStarted = true
				i++
				buf.WriteByte(line[i])
				continue
			}
			tokenStarted = true
			buf.WriteByte(c)
		}
	}

	if inSingle || inDouble {
		return nil
	}
	flush()
	return tokens
}

// stripFishComment removes a trailing fish inline comment from line. A `#`
// starts a comment only when it is at the beginning of the line or preceded
// by whitespace, and is not inside a single- or double-quoted span.
//
// Escape handling models fish's three quoting modes:
//   - Single quotes: `\'` and `\\` are escape pairs.
//   - Double quotes: only `\\` and `\"` are escape pairs; other `\x` are
//     preserved verbatim by fish, so we leave them for the normal loop.
//   - Unquoted: `\` escapes (consumes) the next character, so `\#` is not
//     treated as a comment start.
//
// This is still a conservative approximation (no variable expansion, command
// substitution, or line continuation), but it is enough for the simple export
// lines we generate and need to dedup.
func stripFishComment(line string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' {
			if inSingle {
				if i+1 < len(line) && (line[i+1] == '\'' || line[i+1] == '\\') {
					i++
				}
				continue
			}
			if inDouble {
				if i+1 < len(line) && (line[i+1] == '\\' || line[i+1] == '"') {
					i++
				}
				continue
			}
			i++
			continue
		}
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') && !isEscapedWhitespace(line, i-1) {
				return line[:i]
			}
		}
	}
	return line
}

// isEscapedWhitespace reports whether the whitespace byte at idx is escaped by
// an odd-length backslash run immediately before it. This keeps `\ #` and
// similar fish tokens from being misclassified as inline comments.
func isEscapedWhitespace(line string, idx int) bool {
	if idx <= 0 {
		return false
	}
	count := 0
	for j := idx - 1; j >= 0 && line[j] == '\\'; j-- {
		count++
	}
	return count%2 == 1
}

// unquoteValue strips a single layer of surrounding double or single quotes
// and trims surrounding whitespace.
func unquoteValue(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func resolveShellProfile(shell, home string) string {
	return resolveShellProfiles(shell, home)[0]
}

// resolveShellProfiles returns the ordered list of shell profile candidates
// that PersistRoot should try. Known shells keep their single dedicated file;
// for unknown shells we fall back to ~/.profile first, then ~/.bashrc, since
// many login shells source one of these two.
func resolveShellProfiles(shell, home string) []string {
	switch {
	case strings.Contains(shell, "zsh"):
		return []string{home + "/.zshrc"}
	case strings.Contains(shell, "fish"):
		return []string{home + "/.config/fish/config.fish"}
	case strings.Contains(shell, "bash"):
		return []string{home + "/.bashrc"}
	default:
		return []string{home + "/.profile", home + "/.bashrc"}
	}
}

func shellExportEntry(shell, key, value string) string {
	if strings.Contains(shell, "fish") {
		return fmt.Sprintf("\nset -x %s %s\n", key, shellQuote(value))
	}
	return fmt.Sprintf("\nexport %s=%s\n", key, shellQuote(value))
}

func runHostHelper(helperPath string, args ...string) error {
	if err := validateHostHelperPath(helperPath); err != nil {
		return err
	}
	out, err := execWithTimeout(helperPath, args...)
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", helperPath, args, err, truncateHelperOutput(out))
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validateHostHelperPath(helperPath string) error {
	if !filepath.IsAbs(helperPath) {
		return fmt.Errorf("helper path must be absolute: %q", helperPath)
	}
	info, err := os.Lstat(helperPath)
	if err != nil {
		return fmt.Errorf("stat helper %q: %w", helperPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("helper path must not be a symlink: %q", helperPath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("helper path is not a regular file: %q", helperPath)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("helper path is writable by group or others: %q (mode %04o)", helperPath, info.Mode().Perm())
	}
	return nil
}

const maxHelperOutputBytes = 512

func truncateHelperOutput(out []byte) string {
	if len(out) <= maxHelperOutputBytes {
		return string(out)
	}
	return fmt.Sprintf("%s... (truncated to %d bytes)", string(out[:maxHelperOutputBytes]), maxHelperOutputBytes)
}
