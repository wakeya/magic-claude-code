# Auto Update Review Notes

Date: 2026-06-13  
Reviewers: Codex and Claude Code

## Scope

This note summarizes the temporary `agent-exchange/` review thread for the auto-update feature. The raw exchange contained 13 review rounds and is intentionally not kept in the repository.

## Key Findings And Resolutions

1. Binary replacement must not leave the executable missing or partially written.
   - Resolution: write the new binary to a temporary file first, then rename with backup and rollback handling.

2. Frontend restart behavior needed an explicit backend contract.
   - Resolution: `POST /api/update/apply` returns an explicit `restarting` boolean. Linux/macOS can auto-restart; Windows and Docker do not.

3. GitCode private/raw downloads need source-specific headers.
   - Resolution: `UpdateInfo` carries `DownloadHeaders`, and GitCode downloads use `PRIVATE-TOKEN` when configured.

4. Update API methods needed defensive validation.
   - Resolution: `GET /api/update/check` rejects non-GET requests; `POST /api/update/apply` rejects non-POST requests.

5. Download size limits needed clear failures.
   - Resolution: archives are capped at 200 MB, `SHA256SUMS.txt` is capped at 1 MB, and oversized responses fail with an explicit size-limit error.

6. Download errors could leak sensitive URL parts if future sources put tokens in URLs.
   - Resolution: user-facing download errors redact userinfo, query strings, and fragments.

7. Malformed release metadata could produce confusing downstream asset errors.
   - Resolution: GitHub and GitCode sources reject empty `tag_name`; update checks reject non-semver latest tags.

8. Docker users still need update notifications.
   - Resolution: Docker mode initializes the updater and keeps `/api/update/check` available, but `/api/update/apply` returns a business error instructing users to update the container image.

9. Page-load update checks should not repeatedly hit release sources.
   - Resolution: the frontend records update-check attempts in `localStorage` and only performs page-load checks once every 24 hours per browser.

10. GitCode binary distribution needed CI support.
    - Resolution: `.gitcode/workflows/release.yml` builds release archives, stores them under `dist/release/{tag}/`, commits them to `main`, and creates the GitCode Release required by `releases/latest`.

## Final Review Conclusion

Claude Code confirmed the final Codex review rounds:

- URL redaction is appropriate.
- A 1 MB checksum file limit is sufficient.
- Empty and invalid release tags should be rejected.
- GitCode `html_url` fallback is acceptable.
- Malformed release metadata should be surfaced as source failure.

No remaining issues were identified in the raw exchange at the time it was replaced by this summary.

## Verification Evidence

Representative verification commands run during the review:

```bash
rtk go test ./... -count=1
go vet ./...
go build -o /tmp/mcc-verify ./cmd/server
GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64-verify.exe ./cmd/server
GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64-verify ./cmd/server
npm test
npm run build
git diff --check
```

Latest recorded passing counts during the review:

- Go tests: 421 passed in 11 packages.
- Frontend tests: 49 passed.

## Residual Operational Notes

- GitCode workflow syntax and API payload should still be validated with a real tag push in GitCode.
- GHCR package visibility should be checked after the first image push; GitHub Container Registry packages may require manual public visibility settings.
