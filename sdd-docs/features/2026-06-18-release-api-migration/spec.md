# Release API Migration Spec

Local page: `internal/updater/source.go`, `scripts/release.sh`
Proxy entry: none
Reference sources: GitHub Releases API, Gitee Releases API, GitCode Releases API, GitLab Releases API
Stack: Go 1.26, Bash, GitHub Actions CI
Last updated: 2026-06-18
Progress: 5 / 5 completed

## Overall Analysis (Source Analysis)

### Background

The project distributes pre-compiled binaries across four Git remotes: GitHub, Gitee, GitCode, and a self-hosted GitLab. Currently, binaries are stored as tracked files in `dist/release/{tag}/` and downloaded via platform-specific raw file URLs:

- GitHub: already uses Release API (`github.com/.../releases/download/{tag}/{file}`)
- Gitee: raw URL (`gitee.com/.../raw/main/dist/release/{tag}/{file}`)
- GitCode: raw API URL (`api.gitcode.com/api/v5/repos/.../raw/dist/release/{tag}/{file}`)

The raw URL approach causes repository bloat (each release adds ~35 MB of tracked binaries) and complicates retention (requires `--skip-worktree` hacks and dual cleanup strategies).

### API Verification Results (2026-06-18)

All three domestic/international platforms were tested with real API calls:

| Platform | Upload | Anonymous Download | Delete | Download URL Format |
|----------|--------|-------------------|--------|---------------------|
| GitHub | CI `gh release upload` | Yes | Yes (API) | `github.com/{owner}/{repo}/releases/download/{tag}/{file}` |
| Gitee | `POST .../releases/{id}/attach_files` (multipart) | Yes (200) | Yes (204) | `gitee.com/{owner}/{repo}/releases/download/{tag}/{file}` |
| GitCode | Two-step: `GET upload_url` → `PUT` to OBS (200) | Yes (200) | No API (404) | `gitcode.com/{owner}/{repo}/releases/download/{tag}/{file}` |
| GitLab | Release Links only (no binary upload) | N/A | N/A | Links point to GitHub |

Key findings:

1. **GitCode**: API-uploaded files get a `browser_download_url` on the `api.gitcode.com` domain (404), but the `gitcode.com` domain works for anonymous download. The constructed URL `https://gitcode.com/{owner}/{repo}/releases/download/{tag}/{file}` returns the correct file content.
2. **Gitee**: Full CRUD — upload, anonymous download, and delete all work correctly.
3. **GitLab** (self-hosted, HTTP port 56080): No direct binary upload API. Releases use external links pointing to GitHub download URLs.
4. **GitCode limitation**: No API endpoint to delete attachments. Old attachments persist until manually removed via web UI.

### Current Auto-Updater Code

The `ReleaseSource` interface (`internal/updater/source.go`) has three implementations:

- `GitHubSource`: already uses Release download URLs — no change needed.
- `GiteeSource`: `AssetURL()` returns raw URL; `AssetHeaders()` returns Bearer token (for raw API auth).
- `GitCodeSource`: `AssetURL()` returns raw API URL; `AssetHeaders()` returns PRIVATE-TOKEN.

The `checkSource()` method in `updater.go` calls `AssetURL()` as the default download URL, then optionally overrides with `asset.DownloadURL` if the release has parsed assets. Since neither Gitee nor GitCode parse assets in `FetchLatestRelease()`, `AssetURL()` is always used.

### Current Release Script

`scripts/release.sh` currently:
1. Builds 6 platform binaries into `dist/release/{tag}/`
2. Applies a remote retention policy (keep 10 versions in git)
3. Commits and pushes `dist/release/` to Gitee/GitCode/GitLab
4. Creates Gitee/GitCode Releases (text only, no attachments)
5. Applies a local retention policy (keep 3 versions via `--skip-worktree`)

### Migration Impact

After migration:
- `dist/release/` in all repos contains only `.gitkeep` — no tracked binaries.
- `scripts/release.sh` uploads binaries as Release attachments (Gitee/GitCode) instead of committing to git.
- Auto-updater constructs Release download URLs instead of raw URLs.
- No retention策略 needed — binaries live in Release storage, not git.
- GitLab gets Release with links to GitHub download URLs.

## Development Checklist

| # | Status | Task | Output | Verification |
|---|--------|------|--------|-------------|
| 1 | Done | Update Gitee/GitCode `AssetURL()` to Release download pattern | `internal/updater/source.go` | Unit tests pass with new URL format |
| 2 | Done | Remove `AssetHeaders()` from Gitee/GitCode sources | `internal/updater/source.go` | Downloads work without auth headers |
| 3 | Done | Rewrite `scripts/release.sh` for Release API upload | `scripts/release.sh` | Dry-run build + upload test on v0.5.0 |
| 4 | Done | Clean `dist/release/` to `.gitkeep` only | `dist/release/` | `git status` shows only `.gitkeep` files |
| 5 | Done | Update CLAUDE.md and AGENT.md | `CLAUDE.md`, `AGENT.md` | Docs reflect Release API workflow |

## Requirements

### Deliverables

1. `GiteeSource.AssetURL()` returns `https://gitee.com/{owner}/{repo}/releases/download/{tag}/{file}`.
2. `GitCodeSource.AssetURL()` returns `https://gitcode.com/{owner}/{repo}/releases/download/{tag}/{file}`.
3. `AssetHeaders()` removed from both Gitee and GitCode sources (downloads are anonymous).
4. `scripts/release.sh` uploads all 6 platform archives + `SHA256SUMS.txt` as Release attachments to Gitee and GitCode.
5. `scripts/release.sh` no longer commits binaries to git; `dist/release/` stays local-only during build.
6. `dist/release/` in git contains only `.gitkeep` files across all version directories.
7. GitLab Release created via API with links to GitHub download URLs.
8. CLAUDE.md and AGENT.md updated to reflect the new workflow.
9. Unit tests updated to match new URL patterns.

### Constraints

1. GitCode download URL must use `gitcode.com` domain (not `api.gitcode.com` which returns 404).
2. GitCode has no delete API — uploaded attachments cannot be removed programmatically.
3. Gitee upload requires `release_id` (numeric), obtained by first fetching the release by tag.
4. GitCode upload is two-step: `GET upload_url?file_name=xxx` → `PUT` to OBS pre-signed URL with custom headers.
5. SHA256SUMS.txt must be uploaded as a Release attachment alongside the binaries.
6. The auto-updater's `checkSource()` constructs SHA256SUMS URL by replacing the filename in the download URL — this pattern must work with Release download URLs.
7. GitLab API is HTTP (not HTTPS) on port 56080 with `-k` (self-signed cert).

### Edge Cases

1. Gitee Release already has attachments with the same name — use `--clobber` or delete-then-upload.
2. GitCode Release already has attachments — cannot delete via API; skip upload if file already exists.
3. Gitee API rate limiting — add retry with backoff.
4. Network timeout during OBS upload to GitCode — retry the two-step upload.
5. `SHA256SUMS.txt` generation must happen before upload, not before git commit.

### Non-Goals

1. Not migrating GitHub CI workflow — it already uses Release API.
2. Not adding GitLab as an auto-updater source.
3. Not implementing GitCode attachment deletion (no API).
4. Not changing the frontend update UI.

## Task Details

### Task 1: Update GiteeSource and GitCodeSource AssetURL

#### Requirements

**Objective** — Change the download URL pattern from raw file URLs to Release download URLs for both Gitee and GitCode.

**Outcomes** — `GiteeSource.AssetURL()` returns `https://gitee.com/{owner}/{repo}/releases/download/{tag}/{file}`; `GitCodeSource.AssetURL()` returns `https://gitcode.com/{owner}/{repo}/releases/download/{tag}/{file}`.

**Evidence** — Unit tests assert the new URL patterns.

**Constraints** — GitCode must use `gitcode.com` domain, not `api.gitcode.com`.

**Edge Cases** — Tag or filename contains special characters (URL-encode if needed, though current naming convention avoids this).

**Verification** — `go test ./internal/updater/ -run TestGiteeSource -v` and `go test ./internal/updater/ -run TestGitCodeSource -v`.

#### Plan

1. Edit `GiteeSource.AssetURL()` in `internal/updater/source.go` line 244-246.
2. Edit `GitCodeSource.AssetURL()` in `internal/updater/source.go` line 166-168.
3. Update assertions in `internal/updater/source_test.go` lines 118-119 and 178-179.

#### Verification

- [x] `GiteeSource.AssetURL("v0.5.0", "test.tar.gz")` returns `https://gitee.com/wakeya/magic-claude-code/releases/download/v0.5.0/test.tar.gz`.
- [x] `GitCodeSource.AssetURL("v0.5.0", "test.tar.gz")` returns `https://gitcode.com/wakeya/magic-claude-code/releases/download/v0.5.0/test.tar.gz`.

### Task 2: Remove AssetHeaders from Gitee and GitCode Sources

#### Requirements

**Objective** — Remove the `AssetHeaders()` method from both sources since Release downloads are anonymous.

**Outcomes** — `GiteeSource` and `GitCodeSource` no longer implement `AssetHeaders()`; the interface assertion in `checkSource()` silently returns nil headers.

**Evidence** — Downloads from Gitee and GitCode Release URLs succeed without auth headers.

**Constraints** — The `checkSource()` method uses an optional interface assertion (`interface{ AssetHeaders() map[string]string }`), so removing the method does not break compilation.

**Edge Cases** — Private repositories would need auth headers; this project uses public repos.

**Verification** — `go build ./...` succeeds; `go test ./internal/updater/ -v` passes.

#### Plan

1. Remove `AssetHeaders()` method from `GiteeSource` (lines 248-253).
2. Remove `AssetHeaders()` method from `GitCodeSource` (lines 170-175).
3. Remove `AssetHeaders` assertions from `source_test.go`.
4. Update comments on both source types to reflect Release API downloads.

#### Verification

- [x] `go build ./...` succeeds.
- [x] `go test ./internal/updater/ -v` passes.

### Task 3: Rewrite scripts/release.sh for Release API Upload

#### Requirements

**Objective** — Replace the git-commit-and-push binary workflow with Release API attachment uploads.

**Outcomes** — `scripts/release.sh` builds binaries, uploads them as Release attachments to Gitee and GitCode, creates GitLab Release with GitHub links, and no longer commits binaries to git.

**Evidence** — Dry-run on v0.5.0: binaries appear as Release attachments on Gitee/GitCode; anonymous download returns correct file content.

**Constraints** — Gitee upload: `POST /repos/{owner}/{repo}/releases/{release_id}/attach_files` (multipart, needs release_id from tag lookup). GitCode upload: two-step `GET upload_url?file_name=xxx` → `PUT` to OBS with headers. GitLab: `POST /projects/{id}/releases` with `assets.links` pointing to GitHub. Release notes read from `sdd-docs/changes/release-notes/{tag}.md`.

**Edge Cases** — Release already exists (update vs skip); attachment already uploaded (Gitee: delete+re-upload; GitCode: skip); token not set (skip with warning); curl timeout (retry once).

**Verification** — Upload 1-2 test files to v0.5.0 Release on both platforms; verify anonymous download.

#### Plan

1. Remove Steps 5 (remote retention), 6 (git commit+push), 9 (local retention) from current script.
2. Add Gitee attachment upload: fetch release_id by tag → loop upload each file via multipart POST.
3. Add GitCode attachment upload: loop each file → GET upload_url → PUT to OBS with headers.
4. Add GitLab Release creation with GitHub download links via `--noproxy '*'` (HTTP port 56080).
5. Keep Steps 1-4 (sync main, build frontend, test, build binaries).
6. Keep push of code + tag to all remotes (but no `dist/release/` binaries).

#### Verification

- [x] `bash -n scripts/release.sh` passes syntax check.
- [ ] Gitee Release has 7 attachments (6 archives + SHA256SUMS.txt).
- [ ] GitCode Release has 7 attachments.
- [ ] Anonymous download from both platforms returns correct file content.
- [ ] GitLab Release has links to GitHub download URLs.

### Task 4: Clean dist/release/ to .gitkeep Only

#### Requirements

**Objective** — Remove all tracked binary files from `dist/release/` across all version directories.

**Outcomes** — `git ls-files dist/release/` shows only `.gitkeep` files.

**Evidence** — `git status` shows binary file deletions; repo size decreases significantly.

**Constraints** — Must not delete `.gitkeep` files. Previous `--skip-worktree` flags must be cleared.

**Edge Cases** — Some files may have `skip-worktree` set from previous retention策略 — need `git update-index --no-skip-worktree` before deletion.

**Verification** — `git ls-files dist/release/ | grep -v '.gitkeep'` returns empty.

#### Plan

1. `git update-index --no-skip-worktree` on all `dist/release/` files (clear previous flags).
2. `git rm` all binary files in `dist/release/` (keep `.gitkeep`).
3. Verify only `.gitkeep` files remain tracked.

#### Verification

- [x] `git ls-files dist/release/` shows only `*.gitkeep`.
- [x] No `skip-worktree` flags remain on `dist/release/` files.

### Task 5: Update CLAUDE.md and AGENT.md

#### Requirements

**Objective** — Update documentation to reflect the Release API workflow.

**Outcomes** — CLAUDE.md items 5-8 and AGENT.md release flow section describe the new Release API approach.

**Evidence** — Docs mention Release API uploads instead of raw URL storage; retention策略 section removed.

**Constraints** — Keep existing doc structure; only modify release-related sections.

**Edge Cases** — None.

**Verification** — Manual review of updated sections.

#### Plan

1. Update CLAUDE.md item 5: `dist/release` is `.gitkeep` only, binaries are Release attachments.
2. Update CLAUDE.md items 6-8: describe Release API upload flow.
3. Update AGENT.md release flow section: describe upload steps.
4. Remove retention策略 references.

#### Verification

- [x] CLAUDE.md accurately describes the new workflow.
- [x] AGENT.md accurately describes the new workflow.
- [x] No references to raw URL downloads or retention策略 remain.
