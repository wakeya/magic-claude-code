# Listen Address Configuration Spec

Local page: admin dashboard configuration page (read-only status display)
Proxy entry: `cmd/server/main.go`, `internal/config/config.go`, `internal/config/sqlite_store.go`, `internal/admin/handler.go`, `internal/frontend/src/views/DashboardView.vue`, `internal/frontend/src/composables/useI18n.ts`
Reference sources: `sdd-docs/features/2026-06-13-auto-update/spec.md` (spec template and config/status exposure pattern), `sdd-docs/features/2026-06-20-transparent-mode-bootstrap-and-fallback/spec.md` (Gateway listen-config precedent)
Stack: Go 1.26 standard library (`net`, `net/http`, `flag`, `os`) + Vue 3 + embedded frontend
Last updated: 2026-06-23
Progress: 0 / 7 planned

## Overall Analysis (Source Analysis)

### Current Project State

The proxy, admin, and gateway services listen on fixed address-port pairs at startup:

- The proxy (`proxy.Server.Start`) is hardcoded to `:443` at [cmd/server/main.go:222](../../../../cmd/server/main.go), equivalent to `0.0.0.0:443` (all IPv4 interfaces) + `[::]:443` (IPv6).
- The admin server (`admin.Server.Start`) is hardcoded to `:8442` at [cmd/server/main.go:228](../../../../cmd/server/main.go).
- The gateway already has full `GatewayListenAddr` + `GatewayListenPort` config fields, assembles its address via `fmt.Sprintf("%s:%d", ...)`, and has a `RestartGateway` mechanism to restart the listener after a change.

### Existing Configuration Inconsistency

`internal/config/config.go` already defines `ProxyPort` (default 443) and `AdminPort` (default 8442). `sqlite_store.go` persists them and `main.go` prints them in the startup banner — **but the `Start` calls ignore these fields**, and the ports remain hardcoded to 443/8442. This is a half-finished "fields-defined-but-not-wired" state and must be fixed in this same change, otherwise the configuration page would show port values that do not match the actual listening ports.

### Why Listen Addresses Cannot Hot-Reload Like Providers

provider / model mapping are hot-reload configs: the proxy reads the latest value when handling the next request; saving in the frontend takes effect immediately.

Listen addresses are fundamentally different: they are bound by `net.Listen` at process startup, and a bound listener does not change address when config changes. Making a change effective requires restarting the listener or the whole process. The gateway achieves single-listener hot restart via a dedicated `RestartGateway`; but the proxy on 443 and admin on 8442 are `Start`-ed once in top-level `main.go` goroutines with no equivalent restart API, and restarting the proxy would interrupt all in-flight requests.

### Why the Frontend Will Not Offer an Edit Entry

Editing listen addresses from the frontend carries two real risks:

1. **Admin listen change severs frontend access**: if a user saves a changed `admin_listen_addr` or `admin_listen_port` and restarts the mcc process, the admin server listens on the new address, but the browser is still on the old URL — a page refresh fails to load, and the user may think the service is down.
2. **Proxy port change breaks hosts**: hosts pins `api.anthropic.com` to `127.0.0.1:443`. If the proxy port changes to 8443, client requests to 443 go unanswered. Changing the proxy port only makes sense with iptables forwarding; changing config alone is useless.

Listen addresses are **infrastructure-layer (decided at deploy time)**, not business-layer (adjusted at runtime). Most users decide once at deployment (default `0.0.0.0`, or tightened to `127.0.0.1`) and never touch it again. This feature therefore adopts **Option B**: config file + CLI flag + env-var overrides at three layers, with a read-only display of the actual listen state in the frontend — no edit entry.

### Configuration Priority

Three-layer override, highest first:

1. **CLI flag** (most common at deploy time; overrides everything)
2. **Environment variable** (most common for Docker)
3. **Config file** (SQLite / JSON; persistent defaults)
4. **Hardcoded defaults** (proxy `0.0.0.0:443`, admin `0.0.0.0:8442`, gateway unchanged)

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | Config fields and defaults | `internal/config/config.go`, `config_test.go` | Default-value and normalize unit tests |
| 2 | Planned | Persistence layer for new fields | `internal/config/sqlite_store.go`, `sqlite_store_test.go` | Old/new DB migration tests |
| 3 | Planned | CLI flag + env var override | `cmd/server/main.go` | Flag parse and env override unit tests (`main_test.go`) |
| 4 | Planned | Wire startup, remove `:443`/`:8442` hardcode | `cmd/server/main.go` | Startup listens on configured address; banner prints the real address |
| 5 | Planned | `/api/status` exposes proxy and admin listen fields | `internal/admin/handler.go`, related tests | Status response field assertion tests |
| 6 | Planned | Frontend read-only listen status + i18n | `internal/frontend/src/views/DashboardView.vue`, `useI18n.ts`, `useApi.ts` | Frontend build + component assertion tests |
| 7 | Planned | CLI help i18n + version flag | `cmd/server/main.go`, `internal/i18n/i18n.go` | `mcc -h` / `mcc -v` zh/en output verification |

## Requirements

### Deliverables

1. `internal/config/config.go` adds two fields and completes the semantics:
   - `ProxyListenAddr string` (json `proxy_listen_addr`, default `"0.0.0.0"`)
   - `AdminListenAddr string` (json `admin_listen_addr`, default `"0.0.0.0"`)
   - `ProxyPort`, `AdminPort` (already exist; this change makes them actually take effect at startup).
2. `NormalizeConfig` fills defaults for the new fields; empty string and port 0 fall back to defaults; proxy/admin port range is validated to 1–65535.
3. SQLite store persists `proxy_listen_addr` / `admin_listen_addr`; reading an old database (column absent) falls back to defaults without error.
4. `cmd/server/main.go` adds CLI flags:
   - `-proxy-listen` (default empty; empty means use the config-file value)
   - `-proxy-port`
   - `-admin-listen`
   - `-admin-port`
   and supports env vars `MCC_PROXY_LISTEN_ADDR`, `MCC_PROXY_PORT`, `MCC_ADMIN_LISTEN_ADDR`, `MCC_ADMIN_PORT`. A non-empty flag overrides env var and config file.
5. `main.go` assembles the proxy and admin addresses via `net.JoinHostPort(cfg.ProxyListenAddr, strconv.Itoa(cfg.ProxyPort))` at startup, removing the hardcoded `:443` / `:8442`; gateway keeps its existing logic.
6. `internal/admin/handler.go` `handleStatus` adds `proxy_listen_addr` / `proxy_port` / `admin_listen_addr` / `admin_port` alongside the existing `gateway_listen_addr` / `gateway_listen_port`, reflecting the **actually-effective** values.
7. The frontend configuration page adds a read-only "Listen Status" block (next to the gateway config area or the status overview), showing proxy / admin / gateway `address:port`; the block **has no save/edit button**, and the copy clearly states "Listen addresses are changed via startup flags or the config file; restart mcc after changing."
8. The three ports printed in the startup banner must match the actually-listening address (today the banner prints `cfg.ProxyPort` but the proxy listens on hardcoded 443; this change unifies them).
9. i18n covers all labels, hints, and explanatory copy for the new block in both zh and en.
10. Add `-v` / `-version` flag: prints the current binary version (`internal/version.Version`, injected via ldflags) and exits immediately without starting any service.
11. Localize CLI help: set a custom `flag.Usage` so `mcc -h` / `mcc --help` output follows the language chosen by `i18n.ResolveLocale()` (zh / simplified Chinese → Chinese, others → English). The new `-proxy-listen` / `-proxy-port` / `-admin-listen` / `-admin-port` / `-v` / `-version` and the existing `-data` / `-password` all use this i18n mechanism.

### Data Model

```go
// internal/config/config.go
type Config struct {
    // ...existing fields...
    ProxyPort        int    `json:"proxy_port"`         // existing; now takes effect
    AdminPort        int    `json:"admin_port"`         // existing; now takes effect
    ProxyListenAddr  string `json:"proxy_listen_addr"`  // new; default "0.0.0.0"
    AdminListenAddr  string `json:"admin_listen_addr"`  // new; default "0.0.0.0"
    // Gateway fields unchanged
}
```

### Startup Address Assembly

```go
// cmd/server/main.go (conceptual)
proxyAddr := net.JoinHostPort(cfg.ProxyListenAddr, strconv.Itoa(cfg.ProxyPort))
adminAddr := net.JoinHostPort(cfg.AdminListenAddr, strconv.Itoa(cfg.AdminPort))
proxyServer.Start(proxyAddr, ...)
adminServer.Start(adminAddr, ...)
```

### Override Resolution

A non-empty flag overrides env var and config file; if the flag is empty, the env var is read; if the env var is empty, the config file is read; if all are empty, `NormalizeConfig` fills defaults. `main.go` performs the override and normalization after `flag.Parse` and before `Start`.

### Constraints

1. Changing a listen address does **not** auto-restart the listener, and the frontend offers no edit entry. This feature only does "configurable + read-only display."
2. Default behavior must match the status quo: proxy `0.0.0.0:443`, admin `0.0.0.0:8442`. Existing users see no behavior change after upgrade.
3. Ports must be 1–65535; invalid values fall back to defaults inside `NormalizeConfig` with a logged warning, and must not block startup.
4. An empty CLI flag / 0 means "do not override"; it must not be misread as "bind empty address."
5. SQLite must seamlessly fall back to defaults when reading an old database lacking `proxy_listen_addr` / `admin_listen_addr`, with no migration error.
6. The listen fields returned by `/api/status` must reflect the actually-effective values (after flag/env/file override and normalization), not the raw config-file values.
7. The read-only frontend block must not reuse the gateway's "input + save button" component shape; it must be pure display to avoid implying editability.
8. No new external dependencies; CLI flag parsing stays on the standard library `flag`, env stays on `os.Getenv` (consistent with existing `MCC_ROOT` / `ADMIN_PASSWORD`).
9. `-v` / `-version` must be handled after `flag.Parse` but before any startup logic (data dir, config load, network listen); it prints the version and `os.Exit(0)` and must never start the service or request admin privileges.
10. CLI help i18n reuses the existing `i18n.ResolveLocale()` + `i18n.Load()` mechanism — no second language-detection path; `-h` / `-help` / `--help` all trigger the localized help (the standard library `flag` routes `-h` / `-help` / `--help` to `flag.Usage` automatically).
11. Flag help strings live in the `Messages` struct in `internal/i18n/i18n.go` (en/zh pairs), not hardcoded in main.go, so they stay aligned with the startup-log language rule.
12. The read-only listen block is placed in the page-top "status overview" area (physically separated from the editable Gateway config area) to prevent users from assuming the proxy/admin listen addresses are also editable on the page.

### Edge Cases

1. User sets both flag and env var — flag wins.
2. Port already in use (`net.Listen` fails) — startup fails with a clear error (existing behavior; unchanged here).
3. Config file has port 0 or out-of-range — `NormalizeConfig` falls back to default.
4. Proxy port changed to non-443 — the service starts, but clients using hosts → 443 fail; the read-only block copy warns that the proxy port should stay 443 or be paired with iptables forwarding.
5. Admin listen changed to `127.0.0.1` — after restart only localhost can reach the config page; the copy warns about this consequence.
6. Upgrading an old SQLite DB — missing new columns fall back to defaults.
7. Flag explicitly passed as empty string (`-proxy-listen ""`) — treated as "no override," not a valid address.
8. IPv6 listen address (e.g. `[::1]`) — `fmt.Sprintf("%s:%d", ...)` is not robust for IPv6; address assembly is unified to `net.JoinHostPort` (gateway aligned to the same helper in this change).

### Non-Goals

1. No frontend edit entry for listen addresses (Option C deferred).
2. No hot restart of proxy/admin listeners (no `RestartGateway`-style mechanism introduced).
3. No change to default listen behavior (still `0.0.0.0` all-interfaces).
4. No per-NIC / per-source-IP access control (that is the firewall's job).
5. No automatic process restart or one-click restart button on config change.
6. No TLS port or Unix-socket listening.

## Task Details

### Task 1: Config Fields and Defaults

#### Requirements

**Objective** — Add configurable listen-address fields for the proxy and admin services, and bring the existing-but-inert port fields into the normalization flow.

**Outcomes** — `Config` gains `ProxyListenAddr`, `AdminListenAddr`; `NormalizeConfig` fills empty values with default `"0.0.0.0"`, empty ports with 443/8442, and validates port ranges with fallback.

**Evidence** — Unit tests cover: empty field → default; invalid port → fallback; valid value → preserved.

**Constraints** — Defaults match the status quo; no change to existing field semantics; JSON tags use snake_case.

**Edge Cases** — Port 0, negative, >65535; empty address; surrounding whitespace (trim).

**Verification** — `go test ./internal/config/`.

#### Plan

1. Add `ProxyListenAddr`, `AdminListenAddr` to the `Config` struct (next to existing `ProxyPort`/`AdminPort`, with default-value comments).
2. Set default `"0.0.0.0"` in `defaultConfig()`.
3. In `NormalizeConfig`, trim and fallback-empty the two new fields; range-validate `ProxyPort`/`AdminPort` (1–65535, fallback on out-of-range).
4. Extend `config_test.go` to cover these branches.

#### Verification

- [ ] Empty address fills default `0.0.0.0`.
- [ ] Invalid port falls back to 443/8442.
- [ ] Valid custom values are preserved.
- [ ] Surrounding whitespace is trimmed.

### Task 2: Persistence Layer

#### Requirements

**Objective** — Make the new fields readable and writable in the SQLite store, compatible with old databases.

**Outcomes** — `sqlite_store.go` writes/reads `proxy_listen_addr` / `admin_listen_addr`; old DBs (column absent) fall back to defaults without error.

**Evidence** — Unit tests: new-DB round-trip is consistent; old-DB (simulated missing column) reads without error and falls back.

**Constraints** — Follow the existing SQLite schema-migration style (the project already has "auto-add-column" precedent); do not break existing field reads/writes.

**Edge Cases** — Column absent; column present but empty string; column present but port 0 or non-numeric.

**Verification** — `go test ./internal/config/`.

#### Plan

1. Add `proxy_listen_addr` / `admin_listen_addr` key-value pairs in the write path.
2. Add corresponding parsing in the read path; empty/missing → leave unset, let `NormalizeConfig` fill defaults.
3. Port fields reuse the existing `ProxyPort`/`AdminPort` read/write (already present).
4. Test a simulated old DB (without new columns) for fallback.

#### Verification

- [ ] New DB round-trip preserves values.
- [ ] Old DB missing-column read does not error.
- [ ] Empty-string value normalizes to default.

### Task 3: CLI Flag and Env-Var Override

#### Requirements

**Objective** — Let deployers override the config-file listen address and port via CLI flags and env vars.

**Outcomes** — `main.go` adds four flags `-proxy-listen` / `-proxy-port` / `-admin-listen` / `-admin-port` plus matching `MCC_*` env vars; a non-empty flag overrides env and file, a non-empty env overrides file.

**Evidence** — `main_test.go` verifies override priority: flag > env > file.

**Constraints** — Empty flag / 0 means "do not override"; use the standard library `flag` and `os.Getenv`; no envconfig-style dependency.

**Edge Cases** — Both flag and env set; env only; flag only; neither (file wins); flag explicitly empty string.

**Verification** — `go test ./cmd/server/`.

#### Plan

1. Define four flags with empty defaults (meaning "no override").
2. Add an `applyListenOverrides(cfg *config.Config, flags ..., envs ...)` helper that applies overrides in flag→env order.
3. Call it after `flag.Parse` and before `Start`.
4. Test the combinations of the three sources.

#### Verification

- [ ] Non-empty flag overrides env and file.
- [ ] Empty flag, non-empty env overrides file.
- [ ] Both empty preserves the file value.
- [ ] Explicit empty-string flag is not mistaken for a valid address.

### Task 4: Wire Startup, Remove Hardcode

#### Requirements

**Objective** — Proxy and admin use the configured listen address/port at startup, removing the `:443` / `:8442` hardcode.

**Outcomes** — `main.go` assembles the proxy address via `net.JoinHostPort(cfg.ProxyListenAddr, strconv.Itoa(cfg.ProxyPort))`; admin likewise; gateway is aligned to `net.JoinHostPort` as well. The banner-printed ports match the actually-listening address.

**Evidence** — Startup log shows `Proxy server starting on 0.0.0.0:443` (or a custom value) consistent with `cfg`; changing a flag changes the listen address.

**Constraints** — Default-value behavior is identical to today; address assembly is unified on `net.JoinHostPort` to handle IPv6 correctly.

**Edge Cases** — IPv6 address; port in use (startup fails, existing behavior); address `0.0.0.0` (all interfaces, default).

**Verification** — Start locally with defaults and verify behavior is unchanged; start with `-proxy-listen 127.0.0.1` and verify localhost-only listening.

#### Plan

1. Introduce `net.JoinHostPort` for proxy and admin address assembly.
2. Replace the hardcoded `:443` and `:8442` at [main.go:222](../../../../cmd/server/main.go) and [main.go:228](../../../../cmd/server/main.go).
3. Change the gateway's `fmt.Sprintf("%s:%d", ...)` to `net.JoinHostPort` too.
4. Verify banner-printed values match the actually-listening values.

#### Verification

- [ ] Default-value startup matches the status quo (`0.0.0.0:443`, `0.0.0.0:8442`).
- [ ] After `-proxy-listen 127.0.0.1`, only localhost listens on 443.
- [ ] IPv6 address assembles correctly (`net.JoinHostPort` produces `[::1]:443`).
- [ ] Banner ports match the actual listen ports.

### Task 5: `/api/status` Exposes Listen Fields

#### Requirements

**Objective** — Let the frontend read the actual proxy/admin listen address/port via the status endpoint.

**Outcomes** — `handleStatus` adds `proxy_listen_addr` / `proxy_port` / `admin_listen_addr` / `admin_port` alongside the existing `gateway_listen_addr` / `gateway_listen_port`.

**Evidence** — Handler tests assert the four new fields exist and match `cfg`.

**Constraints** — The returned values are the actually-effective ones (after `NormalizeConfig` + overrides); no sensitive data exposed (address/port are not sensitive).

**Edge Cases** — Default values; custom values; IPv6 addresses.

**Verification** — `go test ./internal/admin/`.

#### Plan

1. Add the four fields to the `handleStatus` JSON response.
2. If `StatusResponse` has a backing Go struct, add fields; otherwise add keys to the map directly (consistent with existing `gateway_*`).
3. Assert the new fields in tests.

#### Verification

- [ ] `GET /api/status` returns the four new fields.
- [ ] Field values match the actual listen address.
- [ ] Default-value case returns `0.0.0.0` / 443 / 8442.

### Task 6: Frontend Read-Only Display and i18n

#### Requirements

**Objective** — Display the listen status of the three services read-only on the configuration page, with clear copy on how to change them.

**Outcomes** — `DashboardView.vue` adds a "Listen Status" block in the page-top "status overview" area, showing proxy / admin / gateway `address:port` as plain text (no inputs, no save button); copy states "Listen addresses are changed via startup flags or the config file; restart after changing"; zh/en i18n completed.

**Evidence** — Frontend builds; component tests assert the three services' address:port are rendered as read-only text and no editable input appears; i18n copy is complete in zh/en; the block sits in the status overview area, not in the editable config area.

**Constraints** — Do not reuse the gateway's input-component shape; **the block must live in the page-top "status overview" area**, physically separated from the editable Gateway config area to prevent the assumption that proxy/admin listen addresses are editable on the page; read from `/api/status` (not `/api/config`) because status returns the actually-effective values.

**Edge Cases** — Address is `0.0.0.0` (display + note "all interfaces"); IPv6 address; default ports; backend has not returned the new fields yet (old-version compatibility — degrade or hide the block).

**Verification** — `npm --prefix internal/frontend test`; manually verify zh/en copy and read-only rendering.

#### Plan

1. Add the four fields to the status response type in `useApi.ts`.
2. Add zh/en i18n strings for the "Listen Status" block labels, hints, "all interfaces" note, change-method hint, and IPv6/port advice.
3. Add the read-only block to `DashboardView.vue`: one row per service showing `address:port`, plus a one-line explanation below.
4. Component tests assert: read-only text rendered, no inputs, includes the change-method note.

#### Verification

- [ ] The three services' address:port are shown as read-only text.
- [ ] No input fields, no save button.
- [ ] zh/en copy is complete.
- [ ] The note clearly states "change via startup flag or config file + restart to take effect."
- [ ] Does not crash when the backend has not returned the new fields (graceful degradation).

### Task 7: CLI Help i18n and Version Flag

#### Requirements

**Objective** — Make `mcc -h` / `--help` show flag descriptions in the system language, and add `mcc -v` / `--version` to query the version, so users can discover available flags and the current version without reading docs.

**Outcomes** — `cmd/server/main.go` sets a custom `flag.Usage` that emits zh/en flag descriptions via `i18n.Load(i18n.ResolveLocale())`; a new `-v` / `-version` flag prints `mcc <version>` (program name + space + version, e.g. `mcc v0.8.1`) and `os.Exit(0)`; `internal/i18n/i18n.go` `Messages` gains zh/en help copy for every flag (including the new `-proxy-listen` / `-proxy-port` / `-admin-listen` / `-admin-port` / `-version`).

**Evidence** — Manual: under a Chinese locale `mcc -h` prints Chinese flag descriptions; under other locales it prints English; `mcc -v` prints `mcc v0.8.1` (program name + version) and exits without starting any service; an unknown flag triggers the localized Usage and exits with code 2 (standard library default).

**Constraints** — Reuse the existing `i18n.ResolveLocale()` + `i18n.Load()` language detection — no second detection path; flag help strings live in the `Messages` struct (en/zh pairs), not hardcoded in main.go; `-version` is handled before any startup side effect (data dir, config load, network listen); `-h` / `-help` / `--help` all trigger the localized help (standard library `flag` routes them to `flag.Usage`).

**Edge Cases** — Local build without ldflags (version is `dev`); `MCC_LANG` forcing a language; locale detection failure (falls back to English); unknown flag (standard library prints Usage by default — this change makes it localized).

**Verification** — `MCC_LANG=zh` → `mcc -h` prints Chinese; `MCC_LANG=en` → English; `mcc -v` prints the version with exit code 0; no listen port is started.

#### Plan

1. Add help-copy fields for each flag to the `Messages` struct in `internal/i18n/i18n.go` (en/zh pairs), following the existing `FlagDataDir` / `FlagPassword` naming (e.g. `FlagProxyListen`, `FlagVersion`).
2. In `cmd/server/main.go`, at the top of `main` (after `i18n.Load`, before `flag.String` calls), set `flag.Usage` to a closure that prints a localized Usage header + `flag.PrintDefaults()` (PrintDefaults automatically uses the `msg.FlagXxx` string each flag was registered with).
3. Add a `-v` / `-version` bool flag; immediately after `flag.Parse`, if it is true, print `mcc <version>` (e.g. `mcc v0.8.1` — program name + space + `version.Version`) and `os.Exit(0)`.
4. Ensure every `flag.String` / `flag.Bool` usage argument passes `msg.FlagXxx` — no hardcoded literals.
5. Tests: construct `msg` for different locales and assert the Usage output contains expected-language flag keywords; assert the `-version` path exits before any startup side effect.

#### Verification

- [ ] `mcc -h`, `mcc -help`, and `mcc --help` all trigger the localized help output.
- [ ] Under a Chinese locale (`zh*`) the help is Chinese; under other locales it is English.
- [ ] Both `mcc -v` and `mcc --version` print `mcc vX.Y.Z` (program name + version) with exit code 0.
- [ ] `-version` does not create the data dir, does not load config, and does not listen on any port.
- [ ] Every flag description comes from the `Messages` struct; no hardcoded literals in main.go.
- [ ] The Usage triggered by an unknown flag is also localized.
