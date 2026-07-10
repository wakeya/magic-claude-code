# NPM Audit Fix Spec

Local page: Admin dashboard → Usage overview tab (echarts chart) / frontend build toolchain (vite)  
Proxy entry: N/A (frontend dependency upgrade only, no proxy/backend change)  
Reference sources: `npm audit`, GHSA-fgmj-fm8m-jvvx, GHSA-v6wh-96g9-6wx3, GHSA-fx2h-pf6j-xcff  
Stack: npm dependencies (`echarts`, `vite`) + existing Vue 3 + Vite frontend  
Last updated: 2026-07-10  
Progress: 3 / 4 done (Task 3 deferred to release; see Implementation Outcome)

## Overall Analysis (Source Analysis)

### Vulnerability Inventory

`npm audit` in `internal/frontend/` reports exactly two advisories, confirmed against `package-lock.json`:

| Package | Locked version | Vulnerable range | Severity | Advisory | Fix target |
| --- | --- | --- | --- | --- | --- |
| `echarts` | 6.0.0 | `< 6.1.0` | moderate | GHSA-fgmj-fm8m-jvvx (XSS) | 6.1.0 |
| `vite` | 8.0.8 | `8.0.0 - 8.0.15` | high | GHSA-v6wh-96g9-6wx3 + GHSA-fx2h-pf6j-xcff | 8.0.16+ (resolved to 8.1.4) |

`npm audit fix --dry-run` confirms both fixes resolve **within the existing caret ranges** (`^6.0.0` and `^8.0.8`), so no `--force` and no major-version bump is required.

### echarts (moderate XSS, GHSA-fgmj-fm8m-jvvx)

Apache ECharts prior to 6.1.0 has a cross-site scripting (XSS) vulnerability. The advisory affects rendering paths that consume untrusted data into chart options.

Project exposure (`grep -rn echarts src/`):

- Only `src/views/DashboardView.vue` imports echarts, via **tree-shaken dynamic imports** (`echarts/core`, `echarts/charts`, `echarts/components`, `echarts/renderers`).
- Registered modules: `LineChart`, `GridComponent`, `TooltipComponent`, `LegendComponent`, `GraphicComponent`, `CanvasRenderer`.
- Chart is a **dual-Y-axis line chart** (`type: 'line'`, `smooth: true`) fed by `usageTrends` from the project's own admin API (`/api/usage/...`). No user-controllable string is passed into option fields that render HTML.
- The app registers `LineChart`, not the advisory's `LinesChart`, so the reported XSS path is **not reachable** through the current chart configuration. The upgrade is defense-in-depth and clears the advisory.
- Residual real-world exploitability in this project is low (data source is the operator's own backend, not external user input).

### vite (high, Windows-only dev-server)

Two sub-advisories, both gated on Windows and the dev server:

1. **GHSA-v6wh-96g9-6wx3** — `launch-editor` NTLMv2 hash disclosure via UNC path handling, Windows only.
2. **GHSA-fx2h-pf6j-xcff** — `server.fs.deny` bypass on Windows alternate paths.

Project exposure:

- `vite` is a **devDependency**; it only runs `npm run dev` / `npm run build`. The production artifact is the static `dist/` bundle embedded into the Go binary — it never ships the vite dev server.
- Development and CI run on Linux; production deploys to an Alpine Docker image. Neither sub-advisory is reachable on these platforms.
- Effective threat is Windows-developer-only. The upgrade is still warranted: zero-cost within range, clears the high-severity audit flag, protects Windows contributors.

### Fix Strategy and Resolved Versions

`npm audit fix` resolves to the **latest version within each caret range**, confirmed by dry-run:

```
change zrender  6.0.0 => 6.1.0   (echarts transitive)
change echarts  6.0.0 => 6.1.0
change vite     8.0.8 => 8.1.4   (crosses 8.0 -> 8.1 minor, still inside ^8.0.8)
... plus transitive bumps (tinyglobby, rolldown, postcss, picomatch, nanoid, ...)
```

Key decision — vite target:

- `npm audit fix` pulls vite to **8.1.4** (current latest under `^8.0.8`), which is a minor bump from 8.0.x. This is the default, semver-compatible path.
- The minimally-invasive alternative is to pin vite to `^8.0.16` (patch-only fix). This is **not** the default and is reserved as the rollback if 8.1.4 causes a build/runtime regression.
- Default plan: accept `npm audit fix` → vite 8.1.4. Rollback plan: if `npm run build` or the dashboard chart fails, pin `"vite": "^8.0.16"` in `package.json` and re-run `npm install`.

### Embedded Frontend Constraint (why `dist/` must be committed)

`internal/frontend/embed.go` is `//go:embed dist/*`, and `Makefile`'s `make build` / `make run` invoke Go directly (`go build` / `go run`) **without rebuilding the frontend**. Therefore the tracked `internal/frontend/dist/` is exactly what gets embedded into the Go binary on a clean checkout. Release paths (Docker, `scripts/release.sh`, CI) rebuild the frontend independently, but the lightweight `make build` / `make run` path relies on the committed `dist/`.

Consequence: after any frontend dependency or source change, `npm run build` must be re-run and the rebuilt `internal/frontend/dist/` committed, so the embedded frontend matches the upgraded dependency. This supersedes any looser "dist not committed" guidance — for this repo, `dist/` is a tracked, embedded artifact, not a release-only build output.

### Risk Summary

1. **echarts 6.0 → 6.1 is a minor bump.** The project uses only stable, long-standing option APIs (line series + grid/tooltip/legend/graphic + canvas renderer). Breakage is unlikely but unit tests do not render pixels, so a manual dashboard render check is mandatory (Task 3).
2. **vite 8.0 → 8.1 is a minor bump.** As a dev-only build tool, the only project-visible surface is `npm run build` and `npm run dev`. A clean production build is sufficient evidence; rollback to `^8.0.16` exists if needed.
3. **Transitive dependency churn** (zrender, rolldown, postcss, etc.) is expected and benign — they are pulled by echarts/vite's own ranges and recorded in `package-lock.json`. No direct `package.json` change for these.
4. **`dist/` MUST be committed** with the lockfile — see "Embedded Frontend Constraint" above. Failing to commit the rebuilt `dist/` leaves `make build`/`make run` embedding the stale (pre-fix) frontend.
5. **No push without confirmation** — per project convention, commit locally on `fix/npm-audit` and wait for explicit go-ahead before pushing / opening the PR.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Done | Run `npm audit fix` to upgrade echarts + vite | `package-lock.json` only (echarts 6.1.0, vite 8.1.4; `package.json` caret ranges unchanged) | `npm audit` shows 0 vulnerabilities |
| 2 | Done | Build and unit-test verification | Build output, test results | `npm run build` succeeds; `npm test` passes 158/158 (incl. echarts lazy-load assertions) |
| 3 | Deferred | echarts runtime render verification | Manual dashboard render check | Usage overview dual-axis line chart renders correctly; empty-state graphic shows (deferred to release) |
| 4 | Done | Workspace hygiene and local commit | One commit on `fix/npm-audit` | Commit = lockfile + rebuilt `dist/` + bilingual spec + README index + bilingual review-notes; not pushed |

## Requirements

### Deliverables

1. `internal/frontend/package.json` caret ranges `^6.0.0` (echarts) and `^8.0.8` (vite) remain **unchanged** — both resolved versions (6.1.0 / 8.1.4) still satisfy them. The file itself is NOT modified by `npm audit fix`.
2. `internal/frontend/package-lock.json` is regenerated to lock echarts 6.1.0, vite 8.1.4, and their transitive updates (zrender 6.1.0, etc.).
3. `internal/frontend/dist/` is rebuilt by `npm run build` and **committed** (required because `embed.go` embeds it).
4. `npm audit` (run in `internal/frontend/`) reports **0 vulnerabilities**.
5. `npm run build` (in `internal/frontend/`) completes successfully and regenerates `dist/`.
6. `npm test` (in `internal/frontend/`) passes, including the echarts lazy-load assertions in `src/views/DashboardUsageRequests.test.ts`.
7. The Usage Overview dashboard chart renders correctly at runtime after the echarts 6.0 → 6.1 upgrade (manual verification, since unit tests do not render pixels) — deferred to release.
8. Exactly one local git commit on branch `fix/npm-audit` containing: the regenerated `internal/frontend/package-lock.json`, the rebuilt `internal/frontend/dist/`, the bilingual spec, the bilingual review-notes, and the README index line. The commit is **not pushed**.

### Directory Structure

```text
internal/frontend/
  package.json          (UNCHANGED: caret ranges ^6.0.0 / ^8.0.8 still satisfied by the resolved versions)
  package-lock.json     (regenerate: lock echarts 6.1.0, vite 8.1.4, transitive bumps)
  dist/                 (rebuilt by `npm run build` and COMMITTED — embed.go embeds dist/*, so it must match the upgraded echarts)
```

No other files are modified. No Go source, proxy, cert, config, or CI files are touched.

### Upgrade Delta (confirmed via dry-run and final lockfile)

| Package | From | To | Direct? |
| --- | --- | --- | --- |
| echarts | 6.0.0 | 6.1.0 | yes (dependency) |
| zrender | 6.0.0 | 6.1.0 | transitive (echarts) |
| vite | 8.0.8 | 8.1.4 | yes (devDependency) |
| tinyglobby | 0.2.16 | 0.2.17 | transitive (vite) |
| rolldown | 1.0.0-rc.15 | 1.1.5 | transitive (vite) |
| @rolldown/pluginutils | 1.0.0-rc.15 | 1.0.1 | transitive (vite) |
| @rolldown/binding-linux-x64-gnu | 1.0.0-rc.15 | 1.1.5 | transitive (vite, platform) |
| @oxc-project/types | 0.124.0 | 0.139.0 | transitive (vite) |
| postcss | 8.5.10 | 8.5.16 | transitive (vite) |
| picomatch | 4.0.4 | 4.0.5 | transitive (vite) |
| nanoid | 3.3.11 | 3.3.15 | transitive (vite) |
| @tybys/wasm-util | 0.10.1 | 0.10.3 | transitive (rolldown) |
| @napi-rs/wasm-runtime | 1.1.4 | 1.1.6 | transitive |
| @emnapi/wasi-threads | 1.2.1 | 1.2.2 | transitive |
| @emnapi/runtime | 1.9.2 | 1.11.1 | transitive |
| @emnapi/core | 1.9.2 | 1.11.1 | transitive |
| @rolldown/binding-linux-x64-musl | 1.0.0-rc.15 | 1.1.5 | transitive (vite, platform optional dep) |

### Constraints

1. Use `npm audit fix` (no `--force`). Both fixes resolve within existing caret ranges; no major-version bump is permitted.
2. Do not modify the caret ranges in `package.json` unless the rollback path (vite `^8.0.16`) is triggered. The ranges `^6.0.0` and `^8.0.8` remain valid for the resolved versions.
3. Do not run `npm audit fix --force` — it would push echarts/vite across a major boundary and is explicitly out of scope.
4. Commit `package-lock.json` **plus the rebuilt `internal/frontend/dist/`**. `node_modules/` is gitignored. **`dist/` must be committed** — see "Embedded Frontend Constraint" above.
5. Do not push the commit. Commit locally on `fix/npm-audit` and await explicit confirmation (per project convention: commit-only during iteration).
6. The upgrade must not change any source file under `src/`. echarts usage in `DashboardView.vue` stays as-is; if 6.1 introduces an API change, the fix is a minimal source patch, not a rewrite.
7. Run all commands with working directory `internal/frontend/` (or `npm --prefix internal/frontend`).

### Edge Cases

1. `npm run build` fails after vite 8.0 → 8.1 minor bump — rollback: set `"vite": "^8.0.16"` in `package.json`, re-run `npm install`, re-verify build. Document the rollback in the task evidence.
2. echarts 6.1 introduces a breaking option change affecting the dual-axis chart — rollback: pin `"echarts": "^6.0.0"` is insufficient (6.0.0 is still vulnerable); instead pin to the first non-vulnerable 6.1.x only if a patch exists, otherwise keep 6.1.0 and apply a minimal source fix in `DashboardView.vue`. Capture the specific option that changed.
3. `npm test` fails on the echarts lazy-load assertions — these assertions match source text (`import('echarts/core')`, etc.). They will only fail if a source edit changes the import shape; investigate the actual diff before touching the test.
4. `npm audit fix` reports it cannot fix everything (e.g., a peer conflict) — stop, capture the exact message, and do not proceed to `--force`; report the conflict for manual resolution.
5. A transitive bump breaks the build — narrow down via `npm ls <pkg>`; transitive breakage is rare for these packages but rolldown rc → stable is the most likely candidate. If rolldown 1.1.5 breaks, that gates the vite upgrade and triggers the vite `^8.0.16` rollback.
6. `package-lock.json` shows more changes than the dry-run listed — acceptable as long as `npm audit` is clean and the build/test pass; transitive trees can vary by platform.
7. The dashboard chart renders but with a visual regression (color, animation, tooltip) after echarts 6.1 — minor visual changes within the same theme are acceptable; functional breakage (no series, wrong axis, empty canvas) is not.

### Non-Goals

1. Do not upgrade `vue`, `vue-router`, `tailwindcss`, `typescript`, `vue-tsc`, or any other dependency unrelated to the two advisories.
2. Do not switch the build tool away from vite.
3. Do not refactor the echarts integration (lazy-load pattern, module registration) — keep it as-is unless 6.1 forces a minimal compatibility patch.
4. Do not modify any Go source, including `internal/updater` (a pre-existing URL-redaction defect surfaced during review is tracked separately — see Implementation Outcome).
5. Do not push the branch or open the PR automatically — await explicit confirmation.
6. Do not modify CI workflow or release scripts.

## Task Details

### Task 1: Upgrade Dependencies via npm audit fix

#### Requirements

**Objective** - Resolve both security advisories by upgrading echarts to 6.1.0 and vite to 8.1.4 within the existing caret ranges.

**Outcomes** - `internal/frontend/package-lock.json` is regenerated (echarts 6.1.0, vite 8.1.4, transitive deps like zrender 6.1.0 locked); `package.json` caret ranges are unchanged; `npm audit` reports 0 vulnerabilities.

**Evidence** - `npm audit` output shows "found 0 vulnerabilities"; `grep` in `package-lock.json` confirms `echarts@6.1.0` and `vite@8.1.4`.

**Constraints** - Use `npm audit fix` only, never `--force`. Run inside `internal/frontend/`. Do not edit `package.json` ranges by hand unless the rollback path is triggered.

**Edge Cases** - `npm audit fix` cannot fully resolve (peer conflict) — capture the message and stop. Unexpected major bump proposed — reject and inspect manually.

**Verification** - `npm audit` reports 0 vulnerabilities; resolved versions match the delta table.

#### Plan

1. Ensure working directory is `internal/frontend/` (the npm prefix root).
2. Confirm the baseline by recording current state:
   ```bash
   npm audit 2>&1 | tail -20          # expect: 2 vulnerabilities (1 moderate, 1 high)
   ```
3. Run the in-range fix:
   ```bash
   npm audit fix
   ```
4. Re-run audit to confirm resolution:
   ```bash
   npm audit 2>&1 | tail -5           # expect: "found 0 vulnerabilities"
   ```
5. Verify the resolved versions landed in the lockfile:
   ```bash
   grep -A1 '"node_modules/echarts"' package-lock.json   # expect: "version": "6.1.0"
   grep -A1 '"node_modules/vite"' package-lock.json      # expect: "version": "8.1.4"
   ```
6. Inspect the diff scope (expect `package-lock.json` modified; `package.json` NOT modified):
   ```bash
   git status --short
   git diff --stat
   ```

#### Verification

- [ ] `npm audit` reports 0 vulnerabilities.
- [ ] `echarts` resolves to 6.1.0 in `package-lock.json`.
- [ ] `vite` resolves to 8.1.4 in `package-lock.json`.
- [ ] `package.json` is NOT modified (only `package-lock.json` is).

### Task 2: Build and Unit-Test Verification

#### Requirements

**Objective** - Confirm the upgraded echarts/vite do not break the production build or the existing frontend unit tests, and regenerate the embedded `dist/`.

**Outcomes** - `npm run build` completes successfully and regenerates `dist/`; `npm test` passes all cases, in particular the echarts lazy-load assertions.

**Evidence** - Build output ends with a successful bundle summary (no errors); test runner reports all tests passing, including `DashboardUsageRequests.test.ts` cases about lazy-loading echarts.

**Constraints** - Run inside `internal/frontend/`. The regenerated `dist/` is staged for commit in Task 4 (it must be committed because `embed.go` embeds it). If the build fails on the vite minor bump, trigger the rollback in Task 1's edge cases (pin vite `^8.0.16`) before retrying.

**Edge Cases** - TypeScript/Vue type-check fails after upgrade — inspect whether a transitive `@types` or `typescript` behavior changed (typescript is NOT upgraded directly; only if a transitive type package shifted). Test assertion on echarts source text fails — inspect `src/views/DashboardView.vue` diff (should be empty).

**Verification** - Build succeeds; all tests pass.

#### Plan

1. Run the production build:
   ```bash
   npm run build
   ```
2. Confirm `dist/` was regenerated:
   ```bash
   ls -la dist/                       # expect: index.html + assets/ present, recent mtime
   ```
3. Run the unit tests:
   ```bash
   npm test
   ```
4. Specifically confirm the echarts lazy-load assertions pass:
   ```bash
   node --test --experimental-strip-types "src/views/DashboardUsageRequests.test.ts" 2>&1 | tail -20
   ```
   Expected assertions still hold (they match source text that is unchanged):
   - `import type { EChartsType } from 'echarts/core'` present
   - `import('echarts/core')`, `import('echarts/charts')`, `import('echarts/components')`, `import('echarts/renderers')` present
   - `import * as echarts from 'echarts'` (full bundle import) NOT present

#### Verification

- [ ] `npm run build` exits 0 with no errors.
- [ ] `dist/` is regenerated.
- [ ] `npm test` passes all tests.
- [ ] echarts lazy-load assertions in `DashboardUsageRequests.test.ts` pass.

### Task 3: echarts Runtime Render Verification (DEFERRED TO RELEASE)

#### Requirements

**Objective** - Verify the echarts 6.0 → 6.1 minor upgrade does not regress the Usage Overview dual-axis line chart at runtime, since unit tests assert source text but do not render pixels.

**Outcomes** - The Usage Overview chart renders with data present (7 line series across two Y-axes, tooltip, legend) and with data absent (centered empty-state text graphic). No console errors from echarts.

**Evidence** - A running admin dashboard (local backend + frontend) showing the Usage Overview tab; the chart canvas displays the trend lines correctly; switching to an empty data range shows the centered empty-state text; browser console shows no echarts errors.

**Constraints** - This is a manual verification step because the chart is canvas-rendered and not pixel-tested. Functional correctness (series render, axes present, tooltip works, empty state shows) is the bar; minor cosmetic shifts within theme are acceptable.

**Edge Cases** - No usage data available (fresh install) — verify the empty-state graphic path (`graphic: { type: 'text', ... }` in `setOption`). Backend not running — start it locally or skip with a documented note that build + test passed and runtime check is deferred to release.

**Verification** - Chart renders correctly with and without data; no echarts console errors.

#### Plan

1. Start the backend locally (so `/api/usage/...` returns trend data), then run the frontend dev server:
   ```bash
   npm run dev                         # from internal/frontend/
   ```
   (If the backend is already running via the project's run flow, only the dev server is needed. If a full local stack is unavailable, fall back to `npm run preview` after `npm run build`, but note that API data requires the backend.)
2. Open the admin dashboard in a browser and navigate to the **Usage** tab → **Overview** sub-tab.
3. With usage data present, confirm:
   - Dual Y-axis line chart renders all 7 series (provider_requests_total, failed_requests, Input, Output, Cache Create, Cache Read, usage_coverage).
   - Left axis = provider request totals; right axis = usage coverage (0–100%).
   - Hover tooltip shows axis values; legend toggles series; colors match the theme accent palette.
4. Switch the time range / provider filter to produce an empty result set and confirm:
   - Canvas clears and a centered "empty" text graphic appears (`t('usage.empty')`).
5. Open the browser console and confirm **no echarts errors or warnings** are emitted during render or filter changes.
6. Record the verification outcome (pass / deferred with reason) in the task evidence.

#### Verification

- [ ] Dual-axis line chart renders with data (7 series, two axes, tooltip, legend).
- [ ] Empty-state centered text graphic renders when data is absent.
- [ ] No echarts errors/warnings in the browser console.
- [ ] Outcome recorded (pass, or deferred-to-release with justification).

### Task 4: Workspace Hygiene and Local Commit

#### Requirements

**Objective** - Stage the deliverable files (lockfile + rebuilt dist + bilingual spec + bilingual review-notes + README index) and create a single local commit on `fix/npm-audit`, without pushing.

**Outcomes** - One commit on branch `fix/npm-audit` whose diff is: the regenerated `internal/frontend/package-lock.json`, the rebuilt `internal/frontend/dist/` (all chunk hash changes), the bilingual spec, the bilingual review-notes, and the README index line. `package.json` is NOT in the commit. `node_modules/` is not staged. The commit is local only.

**Evidence** - `git show --stat HEAD` lists the lockfile, the dist chunk changes, and the doc files; `git status` is clean after the commit; "no push performed" confirms it is not pushed.

**Constraints** - Per project convention, commit locally and do not push until the user confirms. **Commit the rebuilt `dist/`** (required because `embed.go` embeds it — see Embedded Frontend Constraint). Do not amend or rebase unrelated history. Commit message follows the repo's conventional-commit style (`fix(deps): ...`).

**Edge Cases** - `dist/` shows as modified/untracked — this is expected; stage it (do NOT restore it, or `make build` will embed the stale frontend). Accidental staging of `package.json` — unstage with `git restore --staged internal/frontend/package.json` (it should not be in the commit). Commit accidentally includes unrelated files — `git reset --soft HEAD~1` and re-stage only the intended set.

**Verification** - `git show --stat HEAD` shows the lockfile + dist + docs, and NOT `package.json`; working tree is clean; commit is local (not pushed).

#### Plan

1. Confirm the full set of changes:
   ```bash
   git status --short
   git diff --stat
   ```
   Expect:
   - `M internal/frontend/package-lock.json`
   - `M internal/frontend/dist/...` (rebuilt chunk hashes)
   - `M sdd-docs/features/README.md`
   - spec + review-notes under `sdd-docs/features/2026-07-10-npm-audit-fix/`
   - `package.json` NOT modified
2. Stage the deliverables explicitly (do not `git add -A`, to avoid stray files):
   ```bash
   git add internal/frontend/package-lock.json internal/frontend/dist/ \
       sdd-docs/features/README.md \
       sdd-docs/features/2026-07-10-npm-audit-fix/
   ```
3. Verify the staged set:
   ```bash
   git diff --cached --stat             # expect lockfile + dist + docs; NOT package.json
   ```
4. Create the commit (conventional-commit style; match recent history):
   ```bash
   git commit -m "fix(deps): 升级 echarts 6.1.0、vite 8.1.4 修复 npm audit 漏洞"
   ```
5. Confirm the commit contents and that it is local:
   ```bash
   git show --stat HEAD
   git status --short                   # expect: clean
   ```
6. **Do not push.** Report the commit hash and summary to the user and await confirmation before pushing / opening the PR.

#### Verification

- [ ] `git show --stat HEAD` lists `package-lock.json` + `dist/` + spec + review-notes + README.
- [ ] `package.json` is NOT in the commit.
- [ ] `git status --short` is clean after the commit.
- [ ] Commit is local; no `git push` was run.
- [ ] Branch `fix/npm-audit` is ready for push/PR upon explicit confirmation.

---

## Implementation Outcome

Executed 2026-07-10. Records the actual results and verification evidence.

### Actual Results

- **Task 1 — Done.** `npm audit fix` upgraded echarts 6.0.0 → 6.1.0 (with zrender 6.0.0 → 6.1.0) and vite 8.0.8 → 8.1.4. `npm audit` reports **0 vulnerabilities**. `package.json` was not modified.
- **Task 2 — Done.** `npm run build` succeeded (Vite 8.1.4); `npm test` passed **158/158**, including the echarts lazy-load assertions in `DashboardUsageRequests.test.ts`. A fresh `npm ci` + `npm run build` produced zero git diff, confirming the committed `dist/` is reproducible from the lockfile.
- **Task 3 — Deferred to release.** Not executed this iteration (requires a full local stack). The production build emits the echarts chunks (charts/components/graphic/renderers) normally and the source-shape assertions pass; canvas rendering / tooltip / legend / empty-state remain a release-gate browser check.
- **Task 4 — Done.** The branch-tip commit contains `package-lock.json` + rebuilt `dist/` + bilingual spec + bilingual review-notes + README index. `package.json` is not in the commit. Not pushed.

### Go Test Verification

`go test ./... -count=1` passed (1358 tests), and `make test` (with `-race`) passed. This was run to confirm the newly embedded `dist/` introduced no Go-side regression (since `embed.go` embeds `dist/*`).

One test, `internal/updater.TestDownloadAndApplyRedactsInvalidDownloadURL`, is intermittently failing in the full concurrent `go test ./...` run. Investigation: it is a **pre-existing defect, not introduced by this PR** (`git diff e901d2a..HEAD -- internal/updater/` is empty). It is tracked separately below — it is NOT a regression of this dependency upgrade.

### Separate Tracking: Pre-existing updater URL-redaction Defect

During review, `internal/updater.TestDownloadAndApplyRedactsInvalidDownloadURL` was found to be both a non-hermetic flaky test **and** a real URL-redaction defect: `updater.go` returns the raw `http.Client.Do` error, which can contain the request's query string (`?token=secret`) when a download fails at the network layer. Forcing an unreachable HTTPS proxy reproduces it deterministically. This is out of scope for the dependency-only PR (no `internal/updater` diff), but must be fixed in a **separate** security change: redact network-layer errors before returning/logging them, and make the test hermetic. See `review-notes.md` finding 5. Recorded in memory so it is not mistaken for a harmless flaky test to ignore.

### Additional Notes

- The lockfile changes the download registry of the upgraded Vite/Rolldown subtree from `registry.npmmirror.com` to `registry.npmjs.org`. Installation succeeded; this is a distribution/maintenance note, not a security finding.
- The app registers `LineChart`, not the advisory's `LinesChart`, so the reported ECharts XSS path is not reachable through the current chart configuration — the upgrade is defense-in-depth.
- A bilingual review archive (`review-notes.md` / `review-notes_ZH.md`) is committed alongside the spec.
