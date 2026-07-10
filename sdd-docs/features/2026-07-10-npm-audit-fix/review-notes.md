# NPM Audit Fix Review Notes

Date: 2026-07-10  
Reviewers: Codex and Claude Code

## Scope

Reviewed the amended `fix/npm-audit` branch tip against `main`, including the lockfile-only dependency resolution, the bilingual spec, the tracked embedded frontend artifacts, the ECharts integration, the Vite/release build paths, and the reported updater test failure.

## Key Findings And Resolutions

1. The original embedded-frontend mismatch is resolved.
   - Evidence: `internal/frontend/embed.go` embeds `dist/*`, while `make build` and `make run` invoke Go directly. The amended branch tip deletes the old hashed assets and commits the Vite 8.1.4/ECharts 6.1.0 asset set. After a fresh `npm ci`, `npm run build` produced no Git diff, proving that the committed assets are reproducible from the lockfile.
   - Resolution: resolved. A clean-checkout `make build`/`make run` now embeds the upgraded frontend.

2. The lockfile upgrade itself resolves the reported advisories and remains reproducible.
   - Evidence: official advisory ranges are ECharts `<6.1.0` and Vite `8.0.0` through `8.0.15`; the lockfile resolves `echarts@6.1.0`, `zrender@6.1.0`, and `vite@8.1.4`. Fresh `npm ci` and `npm audit --json` reported zero vulnerabilities. `npm test` passed 158/158 tests, `npm run build` completed with Vite 8.1.4, and `go test ./...` passed.
   - Resolution: acceptable. Leaving the existing `package.json` caret ranges unchanged is valid because `package-lock.json` pins the installed versions and `npm ci` reproduced them.

3. The spec was only partially synchronized and still contained contradictory instructions.
   - Evidence: the checklist, musl binding row, and implementation outcome were corrected, but the risk summary, non-goals, and Tasks 1/2/4 still said not to commit `dist/` and expected `package.json` plus `package-lock.json` only. These statements directly contradicted the corrected constraint that the tracked embedded `dist/` must be committed.
   - Resolution: resolved and confirmed on re-review. Both specs were rewritten in full on 2026-07-10 so every operative section now states `dist/` must be committed and `package.json` is unchanged; mechanical anti-pattern scanning found no contradictory instruction.

4. Runtime chart rendering remains an explicit verification gap.
   - Evidence: unit tests assert the lazy-import source shape and the production build succeeds, but no browser check covered the seven-series chart, tooltip/legend interaction, or empty-state graphic after the ECharts minor upgrade.
   - Resolution: complete the recorded browser runtime check before release. This is a residual verification item, not a reproduced functional defect.

5. The reported updater failure is both a flaky test and a real pre-existing URL-redaction defect, not a harmless flaky result.
   - Evidence: `main...fix/npm-audit` has no `internal/updater` diff. The test performs a real request to `https://user:pass@example.com?token=secret` before validating the URL. A normal full `go test ./... -count=1` run passed, but forcing an unreachable HTTPS proxy made the test fail deterministically because `http.Client.Do` returned an error containing `?token=secret`.
   - Resolution: non-blocking for this dependency-only change because the code is identical on `main`, but it requires a separate security fix and a hermetic test. Do not record it merely as a flaky test to ignore; network-layer errors must be redacted before being returned or logged.

## Final Review Conclusion

The dependency and embedded-asset implementation is correct: the audit is clean, the frontend tests and build pass, and a clean rebuild exactly reproduces the committed ECharts 6.1 assets. No new functional or security defect was found in the npm-audit implementation. Both specs are now internally consistent on `dist/` being committed and `package.json` being unchanged, so the earlier documentation blocker is resolved. The branch is ready to push and open for review. The browser runtime check remains a release gate. The updater URL leak is pre-existing and out of this diff, but must be tracked as a separate security defect.

## Residual Notes

- The current application registers `LineChart`, not the advisory's `LinesChart`, so the reported ECharts XSS path is not reachable through the present chart configuration; upgrading remains correct defense in depth.
- The lockfile changes the registry host of the upgraded Vite/Rolldown subtree from `registry.npmmirror.com` to `registry.npmjs.org`. Installation succeeded; this is a distribution/maintenance consideration, not a security finding.
- Avoid recording the branch tip's literal commit hash inside a commit that will be amended, because the amend necessarily changes that hash; use "the current branch tip" instead.
