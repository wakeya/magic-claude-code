# Auto-Update Spec

Local page: Admin dashboard header / Status endpoint  
Proxy entry: N/A (admin server :8442)  
Reference sources: GitHub Releases API, GitCode (gitcode.com) Releases API  
Stack: Go 1.26 stdlib (`net/http`, `archive/tar`, `compress/gzip`, `crypto/sha256`) + Vue 3 + embedded frontend  
Last updated: 2026-06-13  
Progress: 0 / 7 planned

## Overall Analysis (Source Analysis)

### Current Project State

The project is a Go single-binary transparent proxy (`mcc`) deployed either bare-metal or in Docker. Releases are built by GitHub Actions (`.github/workflows/release.yml`) and GitLab CI (`.gitlab-ci.yml`), producing platform-specific archives:

```
Magic-Claude-Code-{tag}-{Platform}-{Arch}.tar.gz   (Linux, macOS)
Magic-Claude-Code-{tag}-{Platform}-{Arch}.zip      (Windows)
SHA256SUMS.txt                                     (checksums for all archives)
```

The binary name is `mcc`. Archives contain a directory with `mcc` + `README.md`.

Currently there is no version tracking in the binary itself — `go build` uses `-ldflags="-s -w"` without version injection. Users have no way to know which version they are running, and updates require manually downloading and replacing the binary.

### GitHub Releases API

GitHub provides a REST API for release metadata:

```
GET https://api.github.com/repos/{owner}/{repo}//releases/latest
```

Response includes `tag_name`, `html_url`, and an `assets` array with each asset's `name` and `browser_download_url`. The API is well-documented, stable, and returns JSON. Unauthenticated requests are rate-limited to 60/hour, which is sufficient for startup + on-demand checks.

### GitCode Releases API and Binary Distribution

GitCode (operated by CSDN) is a Chinese open-source code platform. Verification confirmed:

- **API base URL**: `https://api.gitcode.com/api/v5` (also accessible via `https://gitcode.com/api/v5`)
- **Auth header**: `PRIVATE-TOKEN` (not `Authorization: token`)
- **Releases API**: `GET /repos/{owner}/{repo}/releases/latest` — returns `tag_name`, `assets` (auto-generated source archives only)
- **Custom asset upload**: **Not supported** — GitCode releases only auto-generate source archives (zip, tar.gz of the repo source code)

Since GitCode does not support uploading custom binary assets to releases, pre-compiled binaries are stored in the repository under `dist/release/{tag}/`. The download URL is constructed via the GitCode raw file API:

```
https://gitcode.com/{owner}/{repo}/raw/main/dist/release/{tag}/{asset_name}
```

For example:
```
https://gitcode.com/wakeya/magic-claude-code/raw/main/dist/release/v0.2.0/Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz
```

SHA256SUMS.txt is stored alongside the binaries:
```
https://gitcode.com/wakeya/magic-claude-code/raw/main/dist/release/v0.2.0/SHA256SUMS.txt
```

Local `dist/release/` directory only keeps the latest version's binaries; older versions retain empty directories with `.gitkeep` as placeholders.

### Connectivity Detection Strategy

Instead of a separate connectivity probe, the updater tries sources sequentially: GitHub first, GitCode on failure. Each source request has a 30-second timeout (inherited from the HTTP client). If the first source times out or returns a network error, the next source is tried automatically. No caching is needed — the check is infrequent (startup + manual trigger).

### Binary Self-Update Constraints

| Platform | Can overwrite running binary? | Notes |
| --- | --- | --- |
| Linux | Yes | `os.Rename` + `os.WriteFile`; atomic via backup + rename |
| macOS | Yes | Same as Linux |
| Windows | No | Running `.exe` is locked; self-update returns an error |
| Docker | N/A | Container filesystem is ephemeral; guide users to image updates |

### Risk Summary

1. Version injection requires CI changes (`-ldflags -X`); a missed CI update means the binary reports `dev` and always appears to need an update.
2. SHA256 verification is mandatory — a missing or mismatched checksum must block the update.
3. Binary replacement must be atomic with automatic rollback to avoid bricking the installation.
4. Docker detection (`/.dockerenv`) must disable self-update to avoid confusing users with a failed update that gets lost on container recreation.
5. GitCode API format verified: releases API works for tag detection, but custom binary assets are distributed via repo raw URLs (`dist/release/{tag}/`), not release attachments.
6. Restart after update is not automatic in v1; the server binary is replaced on disk but the running process stays on the old binary until restarted.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | Version package and CI ldflags injection | `internal/version/version.go`, CI updates, status endpoint | Build with test version; verify status returns it |
| 2 | Planned | Updater source layer (GitHub + GitCode) | `internal/updater/source.go` | Source unit tests with mock HTTP server |
| 3 | Planned | Updater core logic (check, download, verify, apply) | `internal/updater/updater.go` | Core unit tests (SHA256, extract, asset mapping) |
| 4 | Planned | Admin update API endpoints | `internal/admin/update_handler.go`, server wiring | Handler tests (503 without updater, check, apply) |
| 5 | Planned | Frontend version label + update notification UI | `AppHeader.vue`, `useApi.ts`, `useI18n.ts` | Frontend build; manual check of label + dialog |
| 6 | Planned | Startup auto-check and Docker detection | `cmd/server/main.go` wiring | Build; verify startup log message |
| 7 | Planned | End-to-end manual verification | Verification record | Mock release server full-chain test |

## Requirements

### Deliverables

1. The binary reports its version via `internal/version.Version`, injected at build time via `-ldflags "-X magic-claude-code/internal/version.Version={tag}"`.
2. The `/api/status` endpoint includes a `version` field in the response.
3. A new `internal/updater` package provides:
   - `ReleaseSource` interface with `GitHubSource` and `GitCodeSource` implementations.
   - `GitHubSource` queries the GitHub Releases API and returns asset download URLs from release attachments.
   - `GitCodeSource` queries the GitCode Releases API for tag detection but constructs binary download URLs from repo raw file paths (`dist/release/{tag}/`).
   - `CheckForUpdate` that queries sources sequentially and returns `UpdateInfo` (current vs. latest version, download URL, release page URL).
   - `DownloadAndApply` that downloads the platform-specific archive, verifies SHA256 against `SHA256SUMS.txt`, extracts the `mcc` binary, and replaces the running binary with atomic backup + rollback.
4. Admin API endpoints:
   - `GET /api/update/check` — returns current version, latest version, whether an update is available, and the source used.
   - `POST /api/update/apply` — downloads and applies the update; returns success or error.
5. Frontend shows the current version as a label next to the title in the header. When an update is available, the label becomes a highlighted clickable element showing "vX.Y.Z → vA.B.C" with an arrow icon; clicking opens a dialog with version details and an "Update Now" button.
6. On startup, a non-blocking goroutine checks for updates after a 10-second delay and logs the result.
7. Docker environments (`/.dockerenv` exists) disable self-update and log a guidance message.
8. Unit tests cover source parsing, SHA256 verification, binary extraction, asset name mapping, and version comparison logic.
9. The detailed execution plan is maintained at `sdd-docs/superpowers/plans/2026-06-13-auto-update.md`.

### Directory Structure

```text
internal/
  version/
    version.go
  updater/
    source.go
    source_test.go
    updater.go
    updater_test.go
  admin/
    update_handler.go
    update_handler_test.go
    server.go           (modify: add updater field, routes, setter)
    handler.go          (modify: add version to status response)
cmd/
  server/
    main.go             (modify: wire updater, startup check)
dist/
  release/              (GitCode binary distribution)
    v0.1.0/.gitkeep     (empty placeholder)
    v0.2.0/             (latest: actual binaries + SHA256SUMS.txt)
internal/frontend/src/
  composables/
    useApi.ts           (modify: add update API methods)
    useI18n.ts          (modify: add update i18n strings)
  components/
    AppHeader.vue        (modify: add version label + update dialog)
```

### Data Model

No persistent data model changes. The updater operates on in-memory state only:

```go
// internal/version/version.go
package version

var Version = "dev" // overridden by ldflags

// internal/updater/updater.go
type UpdateInfo struct {
    CurrentVersion string
    LatestVersion  string
    SourceName     string
    ReleaseURL     string
    AssetName      string
    DownloadURL    string
}

type ApplyResult struct {
    NewVersion string
    Message    string
}
```

### Platform Asset Naming

| `runtime.GOOS` | `runtime.GOARCH` | Asset suffix |
| --- | --- | --- |
| `linux` | `amd64` | `Linux-x86_64.tar.gz` |
| `linux` | `arm64` | `Linux-arm64.tar.gz` |
| `darwin` | `amd64` | `macOS-x86_64.tar.gz` |
| `darwin` | `arm64` | `macOS-arm64.tar.gz` |
| `windows` | `amd64` | `Windows-x86_64.zip` |
| `windows` | `arm64` | `Windows-arm64.zip` |

Full asset name format: `Magic-Claude-Code-{tag}-{Platform}-{Arch}.{ext}`

### Constraints

1. SHA256 verification is mandatory — if `SHA256SUMS.txt` is missing or the checksum does not match, the update must fail with a clear error.
2. Binary replacement must use a backup + rename strategy; on write failure, the backup is restored automatically.
3. Docker environments (`/.dockerenv` exists) must not instantiate the updater; the admin API must return HTTP 503 with a guidance message.
4. Windows self-update is not supported in v1; the updater must return a clear error guiding manual update.
5. The updater must not run as a background polling loop — only on startup (once) and on manual API trigger.
6. The HTTP client used for release checks must have a timeout (30 seconds) to avoid blocking the admin server.
7. Version comparison uses simple string ordering (`latest > current`); a `dev` version is always considered older than any tag.
8. The startup auto-check goroutine must be non-blocking and must not delay server startup.
9. The admin API `apply` endpoint must use a longer timeout (5 minutes) to accommodate large binary downloads on slow connections.
10. The frontend update notification must be best-effort — a failed check must not show an error badge or block normal usage.

### Edge Cases

1. The binary is built without ldflags injection (version is `dev`) — always reports an update is available.
2. GitHub API rate limit exceeded — falls back to GitCode automatically.
3. GitCode release assets are not published — the source returns an error; if both sources fail, the check endpoint returns an error message.
4. The running binary path contains symlinks — `filepath.EvalSymlinks` resolves the real path before replacement.
5. The archive does not contain the expected `mcc` binary — `extractBinary` returns a clear error.
6. SHA256SUMS.txt format has extra whitespace or blank lines — `parseSHA256Sums` handles `strings.Fields` parsing robustly.
7. The update is triggered but the version is already up to date — returns a clear "already up to date" message.
8. Network is completely unreachable — both sources fail; the check endpoint returns a clear error; the startup log message records the failure.
9. The admin server has no updater configured (Docker environment) — update endpoints return HTTP 503.
10. The download is interrupted or corrupt — `io.ReadAll` returns a partial body; SHA256 verification fails; the update is rejected.

### Non-Goals

1. Do not implement automatic process restart after binary replacement (future enhancement via `syscall.Exec` or systemd notify).
2. Do not implement a background polling loop for update checks.
3. Do not implement Windows self-update in this phase.
4. Do not implement rollback to a previous version (only automatic rollback if the write step fails).
5. Do not implement release notes rendering in the frontend.
6. Do not implement GitCode release publishing automation in CI (documented as a manual or future CI step).

## Task Details

### Task 1: Version Package and CI Injection

#### Requirements

**Objective** - Embed the release version into the binary at build time so the application can report which version is running.

**Outcomes** - `internal/version/version.go` exposes a `Version` variable; GitHub Actions, GitLab CI, and Dockerfile inject the version via `-ldflags "-X"`; the `/api/status` endpoint includes a `version` field.

**Evidence** - A local build with `-ldflags "-X magic-claude-code/internal/version.Version=v0.0.1-test"` succeeds; the status endpoint returns `"version": "v0.0.1-test"`.

**Constraints** - Default value is `"dev"` for local builds; do not change the existing CI build pipeline beyond adding the `-X` flag.

**Edge Cases** - Building without ldflags (version stays `"dev"`); CI tag does not match semver format (the pipeline already validates this).

**Verification** - Build locally with a test version and verify the status endpoint.

#### Plan

1. Create `internal/version/version.go` with `var Version = "dev"`.
2. Update `.github/workflows/release.yml` build command to add `-X magic-claude-code/internal/version.Version=${RELEASE_TAG}`.
3. Update `.gitlab-ci.yml` build command to add `-X magic-claude-code/internal/version.Version=${CI_COMMIT_TAG}`.
4. Update `Dockerfile` to accept `ARG APP_VERSION=dev` and pass it to the build command.
5. Modify `internal/admin/handler.go` `handleStatus` to include `"version": version.Version` in the JSON response.

#### Verification

- [ ] Local build with test version reports correctly.
- [ ] Status endpoint includes `version` field.
- [ ] CI ldflags syntax matches the existing `-ldflags="-s -w"` pattern.

### Task 2: Updater Source Layer

#### Requirements

**Objective** - Create a source abstraction that can fetch the latest release metadata from GitHub and GitCode.

**Outcomes** - `ReleaseSource` interface with `GitHubSource` and `GitCodeSource` implementations; both return `ReleaseInfo` containing tag name, HTML URL, and downloadable assets.

**Evidence** - Unit tests use `httptest.Server` to mock API responses; GitHub source correctly parses the standard releases JSON; GitCode source correctly parses the GitCode API response.

**Constraints** - Sources must be tried sequentially (GitHub first); each source must have its own timeout via the shared HTTP client; the interface must be extensible for future sources without modifying the updater core.

**Edge Cases** - API returns non-200 status; response JSON is missing expected fields; asset list is empty; network timeout.

**Verification** - Unit tests with mock HTTP servers for both sources.

#### Plan

1. Define `ReleaseAsset`, `ReleaseInfo`, and `ReleaseSource` interface in `internal/updater/source.go`.
2. Implement `GitHubSource` with configurable `baseURL` (defaults to `https://api.github.com`).
3. Implement `GitCodeSource` with configurable `baseURL` (defaults to `https://gitcode.com`).
4. Add `findAsset` helper method on `ReleaseInfo`.
5. Write tests with `httptest.Server` returning mock release JSON.

#### Verification

- [ ] `GitHubSource.FetchLatestRelease` correctly parses tag, URL, and assets.
- [ ] `GitCodeSource.FetchLatestRelease` correctly parses tag, URL, and assets.
- [ ] Non-200 status returns an error.
- [ ] `findAsset` returns the correct asset or nil.

### Task 3: Updater Core Logic

#### Requirements

**Objective** - Implement the complete update workflow: version comparison, asset selection, download, SHA256 verification, archive extraction, and atomic binary replacement.

**Outcomes** - `Updater` struct with `CheckForUpdate` and `DownloadAndApply` methods; helper functions for asset name mapping, SHA256 parsing/verification, tar.gz extraction, and binary replacement.

**Evidence** - Unit tests cover `assetNameFor` (all 6 platform/arch combinations + invalid inputs), `parseSHA256Sums` (standard format + edge cases), `verifyChecksum` (match + mismatch), `extractBinary` (tar.gz with nested binary), `isNewer` (version comparison logic).

**Constraints** - Binary replacement is Linux/macOS only; Windows returns an error; backup + rename strategy for atomicity with automatic rollback.

**Edge Cases** - Unsupported OS/arch; missing binary in archive; SHA256 mismatch; symlinked executable path; write failure after backup (must rollback).

**Verification** - All unit tests pass; `go test ./internal/updater/ -v` shows green.

#### Plan

1. Implement `assetNameFor(goos, goarch, tag)` mapping `runtime` values to asset naming convention.
2. Implement `parseSHA256Sums(r io.Reader)` parsing `sha256sum` output format.
3. Implement `verifyChecksum(data []byte, expectedHex string)` using `crypto/sha256`.
4. Implement `extractBinary(r io.Reader, binaryName string)` using `archive/tar` + `compress/gzip`.
5. Implement `isNewer(current, latest string)` version comparison.
6. Implement `Updater` struct with `CheckForUpdate` (tries sources sequentially) and `DownloadAndApply` (download → verify → extract → replace).
7. Implement `replaceBinary(newBinary []byte)` with backup + rename + rollback.

#### Verification

- [ ] `assetNameFor` returns correct names for all supported platforms.
- [ ] `parseSHA256Sums` handles standard format with multiple entries.
- [ ] `verifyChecksum` accepts correct hash and rejects incorrect hash.
- [ ] `extractBinary` extracts the correct binary from a tar.gz archive.
- [ ] `isNewer` correctly compares `dev`, equal versions, and standard semver.
- [ ] `replaceBinary` creates backup, writes new binary, removes backup on success.

### Task 4: Admin Update API Endpoints

#### Requirements

**Objective** - Expose update check and apply functionality via authenticated admin API endpoints.

**Outcomes** - `GET /api/update/check` returns version comparison info; `POST /api/update/apply` triggers download and apply; both are behind `authMiddlewareFunc`; when no updater is configured (Docker), endpoints return HTTP 503.

**Evidence** - Handler tests verify 503 response when updater is nil; check endpoint returns correct JSON structure; apply endpoint rejects non-POST methods.

**Constraints** - Keep the existing `NewServer` signature unchanged; use a `SetUpdater` setter for backward compatibility; check timeout is 15 seconds; apply timeout is 5 minutes.

**Edge Cases** - Updater not configured; all sources fail; already up to date; download or verification fails; apply called with GET method.

**Verification** - Admin handler unit tests pass; manual curl test shows correct responses.

#### Plan

1. Add `updater *updater.Updater` field and `SetUpdater` method to `Server`.
2. Register `/api/update/check` and `/api/update/apply` routes in `Start()`.
3. Implement `handleUpdateCheck` with 15-second context timeout.
4. Implement `handleUpdateApply` with 5-minute context timeout.
5. Write handler tests.

#### Verification

- [ ] `GET /api/update/check` without updater returns 503.
- [ ] `GET /api/update/check` with updater returns version info.
- [ ] `POST /api/update/apply` without updater returns 503.
- [ ] `POST /api/update/apply` with GET method returns 405.

### Task 5: Frontend Version Label and Update Notification UI

#### Requirements

**Objective** - Show the current version as a label next to the title in the header. When a new version is available, the label transforms into a highlighted clickable element that opens an update dialog.

**Outcomes** - `AppHeader.vue` displays a version label next to the "Magic Claude Code" title at all times. When no update is available, the label is static gray text (e.g., `v0.1.0`). When an update is available, the label becomes a highlighted clickable element with a subtle pulse animation showing the version transition (e.g., `v0.1.0 → v0.2.0` with an up-arrow icon); clicking opens a dialog with current vs. latest version details and an "Update Now" button. i18n strings added for zh and en.

**Evidence** - Frontend builds without errors; version label is always visible next to the title; label changes appearance when an update is available; clicking the highlighted label opens the dialog; dialog shows correct version info; clicking "Update Now" calls the apply API.

**Constraints** - Failed update check must be silent (label stays as static version text, no error indicator); the label must not shift the header layout when transitioning between states; the dialog must show the confirm message about service interruption; the apply button must show a loading state during update.

**Edge Cases** - Version is `dev` (local build without ldflags) — label shows `dev` and always shows update prompt; update check fails silently — label stays as static version text; apply fails with an error message; apply succeeds and server restarts (UI shows "restarting" message); user dismisses the dialog.

**Verification** - Frontend build passes; manual verification of label + dialog interaction in both states.

#### Plan

1. Add `UpdateCheckResult` and `UpdateApplyResult` types to `useApi.ts`.
2. Add `checkForUpdate()` and `applyUpdate()` methods to the API composable.
3. Add i18n strings for update UI in both zh and en.
4. Add version label next to the title in `AppHeader.vue`:
   - Static state: gray `text-[11px]` showing current version.
   - Update state: highlighted background + theme color text showing `vX.Y.Z → vA.B.C` with up-arrow icon, clickable to open dialog.
5. Add update confirmation dialog (Teleport to body).
6. Call `checkUpdate()` on component mount (best-effort, silent on failure).

#### Verification

- [ ] Version label is always visible next to the title.
- [ ] Label is static gray when no update is available.
- [ ] Label becomes highlighted and clickable when an update is available, showing `vX.Y.Z → vA.B.C`.
- [ ] Clicking the highlighted label opens the update dialog.
- [ ] Dialog shows current and latest versions.
- [ ] Apply button triggers the update API.
- [ ] Failed check does not change the label appearance (stays static gray).

### Task 6: Startup Auto-Check and Docker Detection

#### Requirements

**Objective** - Wire the updater into `main.go` with non-blocking startup check and Docker environment detection.

**Outcomes** - On startup in a non-Docker environment, a goroutine checks for updates after 10 seconds and logs the result; in Docker, the updater is not instantiated and a guidance message is logged; the admin server receives the updater via `SetUpdater`.

**Evidence** - Application starts successfully; log output shows version check result or Docker detection message; admin update endpoints work in non-Docker environments.

**Constraints** - The startup check must not block server startup; the 10-second delay ensures servers are bound before making outbound requests; the goroutine uses a 15-second context timeout.

**Edge Cases** - Running in Docker (detected via `/.dockerenv`); both sources unreachable; already up to date; new version available.

**Verification** - Build and run locally; verify log messages; verify Docker detection.

#### Plan

1. Add `updater` and `version` imports to `main.go`.
2. Check for `/.dockerenv` to detect Docker environment.
3. Instantiate `Updater` with `GitHubSource` and `GitCodeSource`.
4. Call `adminServer.SetUpdater(updaterInstance)`.
5. Launch goroutine that sleeps 10 seconds, then checks for updates and logs the result.

#### Verification

- [ ] Non-Docker startup logs version check result.
- [ ] Docker startup logs guidance message and does not create updater.
- [ ] Admin update endpoints return 200 (non-Docker) or 503 (Docker).
- [ ] Startup is not delayed by the update check.

### Task 7: End-to-End Manual Verification

#### Requirements

**Objective** - Verify the complete update flow works with a mock or real release.

**Outcomes** - A verification record documenting the test setup, steps, and results.

**Evidence** - Log output showing successful check → download → verify → apply; status endpoint showing the new version after restart.

**Constraints** - Do not leak API keys or sensitive information; use a mock release server when real releases are unavailable; document GitCode API verification status.

**Edge Cases** - Network unreachable during apply; SHA256 mismatch on corrupt download; GitCode API format differs from expectations.

**Verification** - Complete at least one full-chain test (check → apply → restart → verify new version).

#### Plan

1. Set up a mock release server with test archives and SHA256SUMS.txt.
2. Configure the updater to use the mock server as the source.
3. Trigger `GET /api/update/check` and verify the response.
4. Trigger `POST /api/update/apply` and verify success.
5. Restart the server and verify the status endpoint shows the new version.
6. Document the GitCode API endpoint format for future reference.

#### Verification

- [ ] Check endpoint returns correct version comparison.
- [ ] Apply endpoint downloads, verifies, and replaces the binary.
- [ ] SHA256 verification rejects a tampered archive.
- [ ] After restart, the new version is active.
- [ ] Docker detection correctly disables self-update.
