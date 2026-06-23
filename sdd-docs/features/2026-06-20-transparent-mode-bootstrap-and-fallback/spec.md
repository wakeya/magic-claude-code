# Transparent Mode Bootstrap and Fallback Spec

Local page: startup logs, top header mode area, README quickstart
Proxy entry: `cmd/server/main.go`, `internal/cert/*`, `internal/i18n/i18n.go`, new `internal/bootstrap/*`, `README.md`, `docker-compose.yml`, `Dockerfile`
Reference sources: current README quickstart, `internal/i18n/i18n.go`, `temp/claude-code-proxy/spec.md`
Stack: Go 1.26 stdlib, OS certificate tooling, shell environment variables, Docker
Last updated: 2026-06-20
Progress: 8 / 8 completed

Implementation plan note: keep all execution steps inside the task `Plan` sections below. Do not split this feature into a separate implementation-plan markdown file, otherwise the spec and the work plan will drift apart.

## Overall Analysis (Source Analysis)

### Current Project State

The project already generates a local CA and server certificate at startup, binds the proxy to `443`, and prints manual setup instructions. The current flow is intentionally transparent-mode first:

- `api.anthropic.com` is expected to resolve to `127.0.0.1`.
- The local CA is written to `data/ca.crt`.
- Users are told to install the CA and export `NODE_EXTRA_CA_CERTS` manually.

This is functional but too manual for the deployment experience the user wants. The current startup path does not attempt to change host routing or trust stores automatically, and it does not provide a structured fallback when those operations are blocked by permissions or platform policy.

### Why This Feature Exists

The user wants the runtime to perform the previously manual steps automatically when possible:

1. Add `127.0.0.1 api.anthropic.com`.
2. Trust the generated local CA.
3. Start the proxy immediately.
4. If the host cannot be modified, fall back to lower-privilege connection modes instead of leaving the user at a dead end.

The feature must also print human-readable, localized instructions when automation fails. The language must follow the current system language:

- Chinese locales default to Chinese output.
- All other locales default to English output.

### Required Mode Priority

This feature introduces an explicit entry-mode priority:

| Priority | Mode | Entry | Can intercept hardcoded `api.anthropic.com` traffic? | Privilege requirement | Purpose |
| --- | --- | --- | --- | --- | --- |
| 1 | Transparent Mode | `hosts` + `443` TLS | Yes | Host mutation and local CA trust | Highest compatibility, current default behavior |
| 2 | Tunnel Mode | `HTTPS_PROXY` + CONNECT MITM | Mostly yes | No host mutation; runtime must trust local CA | Best fallback when hosts/CA installation is blocked |
| 3 | Gateway Mode | `ANTHROPIC_BASE_URL=http://127.0.0.1:17487` | No | No host mutation; no system CA install; no `443` | Lowest-privilege fallback for model-path-only usage |

The order is important:

- Transparent Mode must be attempted first because it has the best compatibility and covers hardcoded official endpoints.
- Tunnel Mode must be second because it still has a chance to catch hardcoded requests without requiring host mutation.
- Gateway Mode must be last because it cannot intercept hardcoded endpoints and only covers clients that honor `ANTHROPIC_BASE_URL`.

### Source Reference: Existing Low-Privilege Entry Design

The temporary `claude-code-proxy` spec already defines a compatible low-privilege split:

- `ANTHROPIC_BASE_URL` mode for the model path only.
- `HTTPS_PROXY + CONNECT MITM` mode for broader interception.

This feature should reuse that concept rather than inventing a separate one-off fallback. The difference is that the new feature is about **automatic bootstrap and fallback selection** for MCC at startup, not about replacing the transparent mode.

### Logging and Fallback Requirements

When automatic bootstrap fails, the program must do all of the following:

1. Log the exact failure reason.
2. Print the current missing capability, such as:
   - cannot edit hosts
   - cannot trust the CA store
   - cannot persist environment changes
3. Print the next recommended mode and its startup command.
4. Print the current `ca.crt` path dynamically.
5. Avoid repeating the same full instruction block on every startup once the failure state is unchanged.

### First-Run Environment Bootstrap

The first successful run on a local binary installation must try to make the binary's own directory discoverable for future launches from any working directory.

Required behavior:

- Resolve the real executable location, not the current shell working directory.
- Treat the executable directory as the canonical MCC installation root for that machine.
- Persist a user-appropriate environment entry or launch profile entry that lets future runs locate the MCC root automatically.
- Use that root to locate the bundled CA certificate without requiring the user to manually export or copy certificate paths.
- If the runtime cannot persist the environment change, log a localized failure and fall back to the manual instructions or lower-privilege modes.

The environment bootstrap is a convenience layer. It must not be treated as the only way to find the CA, and it must not break startup if the machine policy blocks persistence.

### Docker Boundary

Docker is a special case:

- The container can generate certificates and start the proxy.
- The container cannot reliably modify the host machine's hosts file or system CA store.
- If an external host-side helper exists, the runtime may call it.
- If no helper exists, the runtime must not pretend the host was modified; it must log the failure and print the fallback instructions.

That boundary must remain explicit in the design. Docker should not become a false promise that the container alone can mutate the host OS.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Done | Locale-aware bootstrap message system | `internal/i18n/*`, `internal/bootstrap/*` | Locale unit tests for Chinese vs English defaulting |
| 2 | Done | Transparent-mode bootstrap executor | `cmd/server/main.go`, `internal/bootstrap/*`, `internal/cert/*` | Adapter tests for hosts/CA success and permission failures |
| 3 | Done | Fallback mode resolver and instruction generator | `internal/bootstrap/*`, `README.md` | Priority-order tests for Transparent > Tunnel > Gateway |
| 4 | Done | Docker and helper boundary handling | `cmd/server/main.go`, `docker-compose.yml`, docs | Manual verification in Docker and non-Docker runs |
| 5 | Done | Header mode entry + detailed mode modal | `internal/frontend/src/components/AppHeader.vue`, `internal/frontend/src/views/DashboardView.vue`, `internal/frontend/src/composables/useI18n.ts` | UI review of visible mode entry, mode cards, and priority text |
| 6 | Done | First-run environment bootstrap and cert root discovery | `cmd/server/main.go`, `internal/bootstrap/*`, `internal/cert/*`, docs | Manual verification from a different working directory |
| 7 | Done | Documentation and localized operator guidance | `README.md`, maybe admin certificate/status copy | Reviewed startup logs and manual command output |
| 8 | Done | Preferred connection mode persistence | `internal/config/*`, `internal/admin/*`, `internal/frontend/*`, `README.md` | API round-trip tests, header mode display tests |

## Requirements

### Deliverables

1. On startup, the server must automatically attempt Transparent Mode setup before starting the proxy:
   - ensure the local CA exists
   - attempt to make `api.anthropic.com` resolve to `127.0.0.1`
   - attempt to install or trust the generated CA using platform-appropriate mechanisms
   - continue to start the proxy even if one of those steps fails
2. The bootstrap and fallback logs must follow the current system language:
   - Chinese locales produce Chinese logs and instructions
   - all other locales default to English logs and instructions
3. If Transparent Mode setup fails due to missing privileges or OS policy, the runtime must select the next available mode in priority order:
   - Tunnel Mode first
   - Gateway Mode second
4. If all automatic attempts fail, the runtime must print a localized manual fallback guide that includes:
   - the failure reason
   - the missing capability
   - the current `ca.crt` path
   - the next recommended mode and command
5. The runtime must not block proxy startup just because bootstrap failed.
6. Docker runs must not claim host mutation success unless a host-side helper actually succeeded.
7. The fallback guide must be stable and stateful enough to avoid spamming the same long instruction block on every launch when nothing changed.
8. The top header must expose a prominent mode entry area so users can immediately see and inspect Transparent, Tunnel, and Gateway modes.
9. On a first run from a local binary installation, the runtime must try to persist a discoverable MCC root so future launches from any working directory can resolve the bundled certificate path automatically.
10. The design must reuse the current certificate generation logic and current locale resolution pattern rather than introducing a separate localization system.

### Mode Semantics

#### Transparent Mode

Transparent Mode is the existing full-compatibility path. It must remain the first attempt because it is the only mode that fully preserves the current `api.anthropic.com` illusion.

Expected automation targets:

- hosts file update
- CA trust installation or trust promotion
- `443` binding readiness

If any of those cannot be completed automatically, the runtime should record the failure and fall back.

#### Tunnel Mode

Tunnel Mode is the compatibility fallback for environments that cannot be modified at the hosts level. It should rely on:

- process-level proxy environment variables
- CONNECT MITM support
- trust of the local CA at the runtime level

Tunnel Mode is still stronger than Gateway Mode because it has a chance to catch hardcoded official endpoints. It should be recommended before Gateway Mode whenever the environment can support proxy variables and runtime CA trust.

#### Gateway Mode

Gateway Mode is the lowest-privilege fallback. It should only be recommended when:

- Transparent Mode cannot be set up
- Tunnel Mode cannot be used or would not be meaningful in the current runtime

Gateway Mode only covers clients that honor `ANTHROPIC_BASE_URL`. It must be presented as a limited fallback, not as a replacement for Transparent Mode or Tunnel Mode.

### Header Mode Entry

The top header must include a visible mode entry section that acts as the primary discovery surface for the three modes.

Required behavior:

- Show the current recommended priority inline as `Transparent Mode > Tunnel Mode > Gateway Mode`.
- Render three clearly distinguishable mode buttons or chips in the header.
- Clicking the header entry opens the detailed mode explanation modal.
- The header entry is for explanation and discovery, not for immediately rewriting runtime configuration.
- The header must be visually prominent enough that users notice it before drilling into cert details.

The header area should not contain the long startup command block. That content must remain separate so the header stays compact and scannable.

### Manual Guidance Rules

The manual guidance printed on failure must be dynamically composed from the real runtime state:

- The `ca.crt` path must be taken from the current data directory.
- The host mapping line must target `api.anthropic.com`.
- The suggested environment variable commands must be platform-specific.
- The guidance must reflect the selected fallback mode and not present irrelevant steps.

### Executable-Directory Cert Resolution

The runtime must prefer the directory containing the `mcc` executable as the source of truth for certificate discovery on local binary installs.

Implications:

- The bundled CA is discovered relative to the executable directory, not the caller's current working directory.
- Running `mcc` from another directory must still resolve the correct CA location after the first-run environment bootstrap has succeeded.
- The README must tell users to run the binary with administrator privileges the first time so the environment persistence step has the best chance of succeeding.
- If admin privileges are not available, the runtime must fall back to manual certificate import instructions or to Tunnel/Gateway modes instead of blocking startup.

### Non-Goals

1. Do not replace Transparent Mode with Gateway Mode.
2. Do not add kernel-level interception, WFP, TUN/TAP, WinDivert, Npcap, or DLL injection.
3. Do not silently mutate the host OS from Docker without a helper or equivalent privileged pathway.
4. Do not add a new GUI wizard in this phase.
5. Do not claim that Gateway Mode can intercept hardcoded `api.anthropic.com` traffic.
6. Do not make the startup fail just because bootstrap cannot finish.
7. Do not require the user to manually copy certificate paths after first-run environment bootstrap succeeds.

## Task Details

### Task 1: Locale-Aware Bootstrap Messaging

#### Requirements

**Objective** - Make all bootstrap success, failure, and fallback text follow the current system language.

**Outcomes** - Chinese systems default to Chinese output; all other systems default to English output; message templates support the bootstrap workflow and its fallback modes.

**Evidence** - Unit tests prove that Chinese locale inputs produce Chinese messages and that non-Chinese locale inputs produce English messages.

**Constraints** - Reuse the existing locale pattern rather than creating a parallel localization system; add a system-locale fallback when shell locale variables are absent.

**Edge Cases** - `LANG` is unset; Windows has no shell locale variable; locale value is `zh_CN.UTF-8`, `zh-Hans`, or another Chinese variant.

**Verification** - Unit tests for locale normalization and message selection.

#### Plan

1. Update `internal/i18n/i18n.go` so any `zh*` locale token resolves to Chinese and shell-locale absence falls back to the OS/system locale.
2. Add bootstrap-specific message entries in `internal/i18n/i18n.go` for success, failure, missing capability, fallback mode labels, and localized command snippets.
3. Extend `internal/i18n/i18n_test.go` with cases for `zh_CN`, `zh-Hans`, empty env vars, and non-Chinese defaults.
4. Add one formatter-focused test that injects `ca.crt` path and host mapping values and verifies the rendered text contains both values.
5. Keep the locale code path shared with the existing startup messages so bootstrap output and current CLI output cannot drift apart.

#### Verification

- [x] Chinese locale inputs produce Chinese messages.
- [x] Non-Chinese locale inputs default to English.
- [x] Dynamic values are rendered into the output correctly.
- [x] `internal/i18n/i18n_test.go` covers locale fallback without env vars.

### Task 2: Transparent-Mode Bootstrap Executor

#### Requirements

**Objective** - Automatically attempt the current manual Transparent Mode setup steps at startup.

**Outcomes** - The runtime can create or load the CA, attempt host mapping, and attempt CA trust installation without stopping proxy startup when a step fails.

**Evidence** - Tests with mocked OS adapters show the success path and the permission-denied path.

**Constraints** - The executor must be explicit about what it could and could not do. It must not fabricate success.

**Edge Cases** - Hosts file is read-only; CA store is locked by policy; a step succeeds but later fallback is still required; startup runs under Docker where host mutation is impossible.

**Verification** - Unit tests for success, partial failure, and total failure.

#### Plan

1. Add a new `internal/bootstrap/` package with a result model that records hosts, CA trust, and environment persistence independently.
2. Split the package into small units such as `executor`, `capabilities`, `instructions`, and `state` so startup, reporting, and formatting stay isolated.
3. In `cmd/server/main.go`, call bootstrap after `certManager.EnsureCA()` and `EnsureServerCert()` and before starting the proxy/admin goroutines.
4. Add OS adapters for hosts mutation, CA trust installation, and optional environment persistence.
5. Make each adapter return typed success/failure details instead of collapsing everything into one error string.
6. Persist the bootstrap state in the data directory so the same failure does not print a full instruction block on every launch.
7. Preserve proxy/admin startup even when bootstrap fails.

#### Verification

- [x] Successful bootstrap reports success.
- [x] Permission failures are captured and reported.
- [x] Proxy startup continues after failure.
- [x] Bootstrap state is persisted and reused to suppress duplicate long failure logs.
- [x] `go test ./...` covers the new bootstrap package.

### Task 3: Fallback Mode Resolver

#### Requirements

**Objective** - Select and announce the next best mode when Transparent Mode cannot be completed.

**Outcomes** - The runtime chooses Transparent > Tunnel > Gateway and prints the right startup command or environment snippet for the selected mode.

**Evidence** - Tests prove that when Transparent Mode is impossible, the resolver prefers Tunnel Mode; when Tunnel Mode is impossible, it falls back to Gateway Mode.

**Constraints** - Gateway Mode must remain last because it cannot intercept hardcoded endpoints.

**Edge Cases** - The runtime supports proxy environment variables but not CA persistence; the runtime can use `HTTPS_PROXY` but not `ANTHROPIC_BASE_URL`; the user launches Claude Code through a GUI app that does not inherit shell variables.

**Verification** - Priority-order unit tests and manual command inspection.

#### Plan

1. Model capability detection explicitly in `internal/bootstrap/` instead of using a single yes/no decision.
2. Add a resolver that ranks Transparent Mode, Tunnel Mode, and Gateway Mode from the current capability snapshot.
3. Generate localized startup instructions and fallback commands from the resolved mode, current OS, and current `ca.crt` path.
4. Make the resolver return both the selected mode and a human-readable rationale so logs can explain why the fallback happened.
5. Add tests proving that Tunnel is chosen before Gateway when Transparent is unavailable, and that Gateway only appears when proxy variables are also unavailable or meaningless.

#### Verification

- [x] Transparent Mode is always preferred when available.
- [x] Tunnel Mode is selected before Gateway Mode.
- [x] Gateway Mode is only selected as the last fallback.
- [x] The resolver exposes a rationale string for logs.

### Task 4: Docker and Helper Boundary Handling

#### Requirements

**Objective** - Make Docker behavior honest and explicit while still allowing automatic setup when a host-side helper exists.

**Outcomes** - Docker startup can generate the CA and start the service, but host mutation is only reported as successful if a privileged helper actually succeeded.

**Evidence** - Docker runs that lack a helper print localized fallback guidance instead of implying the host was changed.

**Constraints** - Container-local actions are allowed; host mutations must not be faked.

**Edge Cases** - The helper is missing; the helper cannot gain permission; the container can write the CA but the host remains unchanged; a helper succeeds for hosts but not for CA trust.

**Verification** - Manual Docker run with and without helper support.

#### Plan

1. Detect Docker startup in `cmd/server/main.go` and split container-local setup from host-side setup.
2. Allow a host-side helper hook only for host mutation and trust-store operations.
3. If no helper is available, record an explicit Docker-limited bootstrap failure and continue startup.
4. Make Docker logging explicit about what was attempted and what could not be attempted on the host.
5. Update `docker-compose.yml` comments and `README.md` to explain that Docker cannot directly mutate the host OS.

#### Verification

- [x] Docker does not claim host mutation success without a real helper success.
- [x] Docker logs the fallback guide when the host cannot be touched.
- [x] Docker still starts the proxy and admin services.
- [x] Docker helper absence is distinguishable from host permission failure in logs.

### Task 5: Documentation and Operator Guidance

#### Requirements

**Objective** - Make the new behavior discoverable without reading source code.

**Outcomes** - README quickstart and related operator notes explain the new automatic bootstrap behavior, the fallback priority, and the language rule.

**Evidence** - A reviewer can follow the README and reproduce the three modes.

**Constraints** - Documentation must stay aligned with the runtime messages and not promise capabilities that the code does not have.

**Edge Cases** - Users on macOS or Windows need different certificate commands; Chinese and English operators need different message output, but the docs should remain readable in both locales.

**Verification** - README review and one end-to-end startup check per platform family.

#### Plan

1. Update the quickstart section in `README.md` to explain automatic Transparent Mode setup and the first-run admin recommendation.
2. Add a dedicated fallback section that documents Transparent > Tunnel > Gateway and why Gateway is last.
3. Explain the locale rule for startup logs and fallback instructions.
4. Add a short troubleshooting block for permission failures, Docker limitations, and manual import recovery.
5. Add a short first-run checklist that tells users what should happen automatically, what to expect in logs, and when to fall back manually.
6. Add a Docker helper subsection that shows the exact `MCC_HOST_HELPER` path contract, volume mount example, supported helper subcommands, and the failure fallback behavior.

#### Verification

- [x] README matches the runtime behavior.
- [x] Fallback priority is documented as Transparent > Tunnel > Gateway.
- [x] Operator guidance matches the logged instructions.
- [x] README includes a first-run checklist.

### Task 6: Header Mode Entry and Detailed Mode Modal

#### Requirements

**Objective** - Make the mode system obvious from the top header and provide a dedicated modal for the three-mode explanation.

**Outcomes** - The top header exposes a prominent mode entry region; clicking it opens a modal that explains Transparent, Tunnel, and Gateway modes with the required priority order and tradeoffs.

**Evidence** - UI review confirms the header entry is visible without scrolling and the modal contains the three modes with the correct order.

**Constraints** - The header is for mode discovery only; the modal explains the modes; startup commands stay outside the cert page and outside the header body.

**Edge Cases** - Narrow viewport width; long localized strings; no active provider; the user is on a locale that defaults to English.

**Verification** - Manual UI review on desktop and a narrow viewport, plus a component-level content assertion test.

#### Plan

1. Add a prominent mode entry region to `internal/frontend/src/components/AppHeader.vue`.
2. Expose a mode summary bar with `Transparent Mode > Tunnel Mode > Gateway Mode` and make it visually louder than the certificate tab label.
3. Add a modal or popover component for the three-mode explanation, with one card per mode and a shared priority banner.
4. Keep `internal/frontend/src/views/DashboardView.vue` certificate tab focused on certificate data only.
5. Add i18n strings in `internal/frontend/src/composables/useI18n.ts` for the mode summary, modal title, three cards, the current-recommendation text, and the explanatory footer.
6. Add a content-assertion test in `internal/frontend/src/components/AppHeader.test.ts` to confirm the header owns the mode entry surface.
7. If the modal is split into its own component, add a focused test for the modal content order and button labels.

#### Verification

- [x] The header contains a visible mode entry area.
- [x] The modal explains all three modes in priority order.
- [x] The cert page stays focused on cert data and does not absorb the mode explanation block.
- [x] The header copy still fits on narrow desktop widths.

### Task 7: First-Run Environment Bootstrap and Cert Root Discovery

#### Requirements

**Objective** - Make local binary installs self-locating so future `mcc` launches from any working directory can resolve the correct certificate path automatically.

**Outcomes** - The first successful run attempts to persist a user-appropriate environment entry or launch profile entry pointing at the executable directory; subsequent runs can discover the bundled CA without manual path setup.

**Evidence** - A binary launched from a non-install directory still finds the same CA after the first-run bootstrap has succeeded.

**Constraints** - The runtime must resolve the executable directory, not the shell working directory; the persistence step is best-effort and must not block startup.

**Edge Cases** - The machine denies environment persistence; the user runs the binary without admin rights; the binary is moved after the bootstrap was written; the current shell session has not reloaded the persisted environment yet.

**Verification** - Manual verification from a different working directory, with and without admin rights.

#### Plan

1. Resolve the executable path at startup in `cmd/server/main.go` and derive the MCC root from the executable directory, not the working directory.
2. Extend the bootstrap layer to try persisting a discoverable environment entry or launch-profile entry on the first successful run.
3. Update certificate resolution in `internal/cert/*` to prefer the executable-relative CA path when present.
4. Add a logging branch for persistence failure that prints the manual import path and the lower-privilege fallback modes.
5. Make the persisted entry point at the MCC root, not the CA file directly, so future layout changes do not require rewriting user configuration.
6. Update `README.md` to instruct first-run execution with administrator privileges and to explain the fallback when that is not possible.

#### Verification

- [x] A subsequent launch from another working directory still resolves the same certificate root.
- [x] Permission failures are logged and do not block startup.
- [x] README tells users to use administrator privileges on first run.
- [x] The persisted root survives relaunch from a different working directory.

### Task 8: Preferred Connection Mode Persistence

#### Requirements

**Objective** - Let the header mode buttons change the next-startup mode and persist that preference in backend config.

**Outcomes** - The UI shows the preferred mode and the effective startup mode; switching a mode saves to backend config; the backend exposes the saved mode in config/status; mode help explains how to configure `~/.claude/settings.json` for Transparent, Tunnel, and Gateway.

**Evidence** - The mode saved from the header survives restart and appears in backend config/status responses.

**Constraints** - Reuse the existing config store and `/api/config` surface; do not introduce a separate mode settings file; keep the current fallback behavior for Transparent Mode.

**Edge Cases** - Saved mode is transparent but bootstrap falls back because of missing privileges; saved mode changes while the server is already running; a user selects tunnel or gateway explicitly.

**Verification** - API tests cover GET/PUT round-tripping; frontend tests cover visible current/effective mode text and save buttons; README and mode help text include the three `settings.json` examples.

#### Plan

1. Add a `connection_mode` field to `internal/config.Config`, normalize it, and persist it in both JSON and SQLite config stores.
2. Extend `/api/config` to return and accept `connection_mode`, and expose the configured/effective mode in `/api/status`.
3. Make bootstrap honor the preferred mode while still allowing Transparent Mode to fall back to lower modes when host setup fails.
4. Update the top header to show the current preferred mode, the effective mode, and buttons for Transparent / Tunnel / Gateway.
5. Expand the mode help modal and startup logs with per-mode `~/.claude/settings.json` examples and restart guidance.
6. Update the README and tests so the new mode persistence flow is documented and verified.

#### Verification

- [x] The selected mode is stored in backend config.
- [x] Header buttons show the current preferred mode and can change it.
- [x] Startup status exposes both the preferred and effective mode.
- [x] Mode help text includes `~/.claude/settings.json` examples for all three modes.
