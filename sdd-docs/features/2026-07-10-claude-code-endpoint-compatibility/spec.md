# Claude Code Endpoint Compatibility Spec

Local page: N/A  
Proxy entry: `internal/proxy/handler.go`, `internal/proxy/hardcoded.go`  
Reference sources: Claude Code extracted JS `2.1.196` and `2.1.206`, Docker `mcc` logs, local Claude config/plugins/skills  
Stack: Go 1.26 stdlib proxy, existing MCC provider/config packages  
Last updated: 2026-07-10  
Progress: 7 / 7 validated

## Overall Analysis (Source Analysis)

### Current Project State

MCC currently terminates TLS for `api.anthropic.com`, handles a fixed set of Claude Code client endpoints locally, and forwards all remaining requests to the configured upstream provider. This was acceptable when the local hardcoded list tracked Claude Code `2.1.88`, but it is now too permissive for newer clients.

The relevant flow is:

```text
internal/proxy/handler.go ServeHTTP
  1. GET / -> OK
  2. handleHardcodedEndpoint(w, r) -> local response if matched
  3. load config
  4. read request body
  5. resolve provider/model
  6. transform request
  7. build upstream URL
  8. forward to provider
```

The risk is step 3 onward: an unrecognized non-model endpoint such as `/v1/logs`, `/api/frame/contract/latest`, `/api/ws/speech_to_text/voice_stream`, or even `/favicon.ico` is sent to GLM, MiniMax, DeepSeek, Kimi, Qwen, or another configured model vendor. These vendors do not implement Claude Code control-plane endpoints, telemetry endpoints, artifact endpoints, plugin search, or design services. Forwarding them wastes tokens/requests, creates noisy errors, and may leak client metadata to a model supplier.

The new architecture must be fail-closed:

```text
known local endpoint      -> respond inside MCC
known model endpoint      -> forward to configured provider
unknown non-model endpoint -> block locally, log method/path/query only
```

### Evidence From Local Docker Logs

`docker logs --since 24h mcc` did not show the new `2.1.206` endpoints being called yet. It did show:

```text
3 x GET /api/claude_cli/bootstrap     handled locally
4 x GET localhost/favicon.ico         forwarded to upstream
```

This confirms two facts:

1. The new endpoints may not be exercised in normal startup, so absence from logs is not evidence that forwarding is safe.
2. The current default-forward behavior is already observable for a harmless browser probe (`/favicon.ico`), proving that unknown paths can escape to the model provider.

### Claude Code 2.1.196 vs 2.1.206 Endpoint Delta

The extracted sources are:

```text
/home/www/workspace/open-software/claude_code/073_claude_spy/claude_code_src.js
/home/www/workspace/open-software/claude_code/073_claude_spy/claude_code_src_2.1.206.js
```

New double-quoted endpoint literals found in `2.1.206` compared with `2.1.196`:

| Endpoint | Category | Current MCC status | Required handling |
| --- | --- | --- | --- |
| `/api/frame/contract/latest` | Frame artifact contract | forwarded | local unavailable response |
| `/api/frame/frames?limit=200` | Frame artifact list | forwarded | local empty list |
| `/api/oauth/organizations/:orgUUID/mcp/connectors/list` | MCP connector discovery | matched by broad prefix, weak `{}` response | local `{"results":[]}` |
| `/api/oauth/organizations/:orgUUID/mcp/connectors/search` | MCP connector search | matched by broad prefix, weak `{}` response | local `{"results":[]}` |
| `/api/oauth/organizations/:orgUUID/mcp/connectors/suggest` | MCP connector suggest | matched by broad prefix, weak `{}` response | local `{"results":[]}` |
| `/api/oauth/organizations/:orgUUID/plugins/search` | Plugin search | matched by broad prefix, weak `{}` response | local search results from config, fallback empty |
| `/api/oauth/organizations/:orgUUID/skills/search` | Skill search | matched by broad prefix, weak `{}` response | local search results from config, fallback empty |
| `/v1/design/consent` | Claude Design consent | forwarded | local consent state |
| `/v1/design/mcp` | Claude Design MCP bridge | forwarded | local unsupported response |

Additional `2.1.206` endpoint literals that are present and currently not hardcoded include:

```text
/api/claude_code/discovery/team_usage
/api/claude_code/notification/preferences
/api/claude_code/skills
/api/frame/deploy/complete
/api/frame/deploy/direct
/api/frame/deploy/init
/api/frame/track
/api/organizations/:orgUUID/claude_code/onboarding
/api/ws/speech_to_text/voice_stream
/v1/agents?beta=true
/v1/code/
/v1/code/agent-proxy
/v1/code/github/import-token
/v1/code/sessions
/v1/code/triggers
/v1/complete
/v1/design/
/v1/environments?beta=true
/v1/files?beta=true
/v1/filestore/fs/readFile
/v1/logs
/v1/mcp/{server_id}
/v1/memory_stores?beta=true
/v1/messages/batches
/v1/metrics
/v1/models
/v1/oauth/token
/v1/organizations/spend_limits
/v1/sessions
/v1/skills?beta=true
/v1/token
/v1/toolbox/shttp/mcp/{server_id}
/v1/traces
/v1/ultrareview/preflight
/v1/user_profiles?beta=true
/v1/vaults?beta=true
```

Only `POST /v1/messages` and `POST /anthropic/v1/messages` are model inference endpoints that should continue to be forwarded. `POST /v1/messages/count_tokens` is already handled locally and should remain local. `/v1/models` should become local model discovery using MCC config, not upstream forwarding.

### Client Parsing and Compatibility Notes

The `2.1.206` client source shows the following response expectations:

| Client area | Request | Client behavior | Compatible MCC response |
| --- | --- | --- | --- |
| MCP connectors | `POST /api/oauth/organizations/:orgUUID/mcp/connectors/search`, `suggest`, `list` | Parses `{results: array, opt_in_required?: bool, message?: string}`. Throws on non-2xx or schema mismatch. | `200 {"results":[]}` |
| Plugin search | `POST /api/oauth/organizations/:orgUUID/plugins/search` | Parses `{results: array}` where each item is loose but normally has `id`, `name`, `description`, `enabled`. | `200 {"results":[...]}` from local marketplace cache, fallback `[]` |
| Skill search | `POST /api/oauth/organizations/:orgUUID/skills/search` | Same parser as plugin search. | `200 {"results":[...]}` from local skills/plugin manifests, fallback `[]` |
| Installed skill health | `GET /api/claude_code/skills` | If status is non-2xx, client skips. If 2xx, reads `data.skills`; each item expects `skill_name` and `health` in `good|warn|poor`. | `200 {"skills":[...]}` or `{"skills":[]}` |
| Frame list | `GET /api/frame/frames?limit=200` | Parses `{frames: array|null}`. Empty array is accepted. | `200 {"frames":[]}` |
| Frame tracking | `POST /api/frame/track` | Expects status `204`; logs otherwise. | `204` no body |
| Frame deploy complete | `POST /api/frame/deploy/complete` | Expects status `204`; logs otherwise. | `204` no body |
| Frame deploy init/direct | `POST /api/frame/deploy/init`, `direct` | Publish path handles 403 reasons including `write_gate_disabled`. | `403 {"error":"Frame publishing is unavailable in MCC local mode","reason":"write_gate_disabled"}` |
| Frame contract | `GET /api/frame/contract/latest` and contract assets | Client requires a precise schema/version if successful. Returning fake data risks parser failures. | `404 {"error":"Frame contract service is unavailable in MCC local mode","reason":"local_unavailable"}` |
| Design consent | `GET /v1/design/consent` | Reads booleans such as `agent_design_projects`; non-200 returns empty state. | `200 {"agent_design_projects":false}` |
| Design consent mutate | `POST`/`DELETE /v1/design/consent` | Expects success status. | `204` no body |
| Design MCP | `POST /v1/design/mcp` | JSON-RPC bridge; non-2xx becomes a clear feature error. | `403 {"error":{"type":"unsupported_local_endpoint","message":"Claude Design is unavailable in MCC local mode"}}` |
| OTLP telemetry | `POST /v1/metrics`, `/v1/logs`, `/v1/traces`, `/api/event_logging/*` | Client does not need returned data. | `204` for `/v1/*`; existing `{}` is acceptable for `/api/event_logging/*` |
| Voice stream | `/api/ws/speech_to_text/voice_stream` | WebSocket/audio path, not a model request. | `501 {"error":{"type":"unsupported_local_endpoint","message":"Speech-to-text streaming is unavailable in MCC local mode"}}` |

### Local Config, Plugins, and Skills

Local inspection confirms the following useful data sources:

```text
/home/www/.claude/settings.json
/home/www/.claude.json
/home/www/.claude/plugins/marketplaces/*/.claude-plugin/marketplace.json
/home/www/.claude/plugins/marketplaces/*/.claude-plugin/plugin.json
/home/www/.claude/skills/*/SKILL.md
```

`~/.claude/settings.json` contains keys such as `enabledPlugins`, `extraKnownMarketplaces`, and `env`. `~/.claude.json` contains caches such as `additionalModelOptionsCache`, `pluginUsage`, and `skillUsage`.

Implementation must treat these files as optional best-effort inputs. MCC runs in Docker for many users, so host `~/.claude` may not be mounted. Missing or unreadable files must produce valid empty responses, never startup failure.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | ✅ Done | Endpoint policy and fail-closed guard | `endpoint_policy.go`, `blocked.go`, handler guard | `TestClassifyForwardingEndpoint`/`TestEndpointPolicy`/`TestServeHTTPFailClosed` pass; blocked-endpoint upstream counter stays 0 |
| 2 | ✅ Done | Local responders for telemetry, probes, models, and low-risk Claude Code APIs | hardcoded handlers, `handleModels` (config-derived) | `TestStaticProbeEndpoints`/`TestHardcodedTelemetry`/`TestHardcodedModels`/`TestHardcodedLowRiskClaudeCode` pass |
| 3 | ✅ Done | Plugin, skill, and MCP connector compatibility | `local_catalog.go`, org-scoped search handlers | `TestLocalCatalog`/`TestPluginSkillSearch`/`TestMCPConnectorEndpoints` pass; specific handlers precede broad fallback |
| 4 | ✅ Done | Frame artifact compatibility | `frame.go` | `TestFrameEndpointCompatibility` passes |
| 5 | ✅ Done | Design and unsupported streaming compatibility | `design_streaming.go` | `TestDesignEndpointCompatibility`/`TestUnsupportedStreamingEndpoints` pass |
| 6 | ✅ Done | Logging and diagnostics | `logBlockedEndpoint` (with control-char sanitization) | `TestBlockedEndpointLogging`/`TestBlockedEndpointLogInjectionGuard`/`TestOrgEndpointsSingleHandlingLog` pass |
| 7 | ✅ Done | Regression verification and endpoint matrix | full test suite, no frontend change | `go test ./internal/proxy` 415 pass; `go test ./...` 1311 pass |

## Requirements

### Deliverables

1. A fail-closed endpoint policy before provider selection and request transformation.
2. An explicit model-forward allowlist:
   - `POST /v1/messages`
   - `POST /anthropic/v1/messages`
3. Existing local `POST /v1/messages/count_tokens` behavior preserved.
4. All known Claude Code control-plane, telemetry, Frame, Design, plugin, skill, MCP connector, browser probe, and voice-stream endpoints handled locally.
5. Unknown non-model endpoints blocked locally with a stable JSON error and a safe log line.
6. `/v1/models` handled locally using MCC configured provider/model data.
7. Local plugin and skill search implemented as best-effort from `CLAUDE_CONFIG_DIR` or `~/.claude`, with empty compatible responses when data is unavailable.
8. Unit tests prove that unknown endpoints do not reach provider forwarding.
9. The implementation updates this spec's checklist/progress and records verification evidence after completion.

### Endpoint Policy

The proxy must classify every request using the normalized `r.URL.Path` only. Query strings must not decide whether a path is forwardable, except handler code may read query values after a local match.

| Decision | Match | Action |
| --- | --- | --- |
| Root probe | `GET /` | Existing `OK\n` |
| Static/browser probe | `/favicon.ico`, `/robots.txt`, `/apple-touch-icon.png`, `/apple-touch-icon-precomposed.png` | Local `404` empty body |
| Local hardcoded | Exact or prefix match in the local endpoint registry | Run local handler |
| Forwardable model | `POST /v1/messages`, `POST /anthropic/v1/messages` | Forward to configured provider |
| Known but wrong method | Same path as forwardable model but non-POST | Local `405` |
| Unknown | Anything else | Local block, no upstream call |

Forwarding `GET /v1/models`, `POST /v1/messages/batches`, `/v1/complete`, `/v1/logs`, `/v1/traces`, or `/api/*` catch-all is forbidden unless this spec is amended with a concrete local handler or model-forward justification.

### Local Response Contract

| Endpoint pattern | Methods | Status | Body |
| --- | --- | --- | --- |
| `/v1/messages/count_tokens` | `POST` | `200` | Existing token estimate response |
| `/v1/models` | `GET` | `200` | `{"data":[{"id":"...","type":"model","display_name":"..."}],"has_more":false}` |
| `/v1/metrics`, `/v1/logs`, `/v1/traces` | `POST` | `204` | empty |
| `/api/event_logging/batch`, `/api/event_logging/v2/batch` | any existing supported method | `200` | `{}` |
| `/api/claude_code/skills` | `GET` | `200` | `{"skills":[{"skill_name":"...","health":"good","source":"local"}]}` or `{"skills":[]}` |
| `/api/claude_code/discovery/team_usage` | `GET` | `200` | `{"teams":[],"usage":[],"data":[]}` |
| `/api/claude_code/notification/preferences` | `GET` | `200` | `{"preferences":{},"notifications_enabled":false}` |
| `/api/organizations/{orgUUID}/claude_code/onboarding` | `GET`, `POST`, `PUT`, `PATCH` | `200` | `{}` |
| `/api/oauth/organizations/{orgUUID}/mcp/connectors/list` | `POST` | `200` | `{"results":[]}` |
| `/api/oauth/organizations/{orgUUID}/mcp/connectors/search` | `POST` | `200` | `{"results":[]}` |
| `/api/oauth/organizations/{orgUUID}/mcp/connectors/suggest` | `POST` | `200` | `{"results":[]}` |
| `/api/oauth/organizations/{orgUUID}/plugins/search` | `POST` | `200` | `{"results":[{"id":"...","name":"...","description":"...","enabled":false}]}` |
| `/api/oauth/organizations/{orgUUID}/skills/search` | `POST` | `200` | Same shape as plugin search |
| `/api/frame/frames` | `GET` | `200` | `{"frames":[]}` |
| `/api/frame/track` | `POST` | `204` | empty |
| `/api/frame/deploy/complete` | `POST` | `204` | empty |
| `/api/frame/deploy/init`, `/api/frame/deploy/direct` | `POST` | `403` | `{"error":"Frame publishing is unavailable in MCC local mode","reason":"write_gate_disabled"}` |
| `/api/frame/contract/*` | `GET` | `404` | `{"error":"Frame contract service is unavailable in MCC local mode","reason":"local_unavailable"}` |
| `/api/frame/{slug}` | `GET`, `DELETE` | `404` | `{"error":"Artifact not found in MCC local mode","reason":"not_found"}` |
| `/v1/design/consent` | `GET` | `200` | `{"agent_design_projects":false}` |
| `/v1/design/consent` | `POST`, `DELETE` | `204` | empty |
| `/v1/design/mcp` | `POST` | `403` | `{"error":{"type":"unsupported_local_endpoint","message":"Claude Design is unavailable in MCC local mode"}}` |
| `/api/ws/*` | any | `501` | `{"error":{"type":"unsupported_local_endpoint","message":"Streaming endpoint is unavailable in MCC local mode"}}` |
| unknown endpoint | any | `404` | `{"error":{"type":"mcc_blocked_unknown_endpoint","message":"MCC blocked an unrecognized non-model endpoint"},"path":"/..."}` |

All local handlers must drain or close the request body before returning, except handlers that already read the body for local calculation.

### Local Catalog Rules

Plugin/skill search must use a small internal package or helper file under `internal/proxy` with these rules:

1. Config directory resolution:
   - Use `CLAUDE_CONFIG_DIR` when set.
   - Else use `os.UserHomeDir()` + `.claude`.
   - If no home directory is available, return an empty catalog.
2. Candidate files (**revised: marketplace.json plugins[] is the primary plugin source**):
   - Plugin primary source: the `plugins[]` array of each `plugins/marketplaces/*/.claude-plugin/marketplace.json` (fields `name`/`displayName`/`description`/`category`/`tags`/`keywords`). The official marketplace aggregates all plugins in this array; scanning individual `plugin.json` files misses most.
   - Skill sources: `skills/*/SKILL.md` and `plugins/marketplaces/*/skills/*/SKILL.md` (covers plugin-provided skills).
   - When file parsing yields nothing, optionally fall back to `~/.claude.json` (parent of configDir) `pluginUsage`/`skillUsage` keys for name-only entries (compound key `name@market` split).
   - `settings.json` provides enabledPlugins (compound-key format, see below).
3. Search request parsing:
   - Accept `{"keywords":["foo","bar"]}`.
   - Also tolerate `{"keywords":"foo bar"}` and missing/invalid bodies by returning the unfiltered first page or empty list.
4. Matching:
   - Case-insensitive substring match against `id`, `name`, `displayName`, `description`, `category`, `tags`, `keywords`, and source marketplace name.
   - No fuzzy dependency or external library.
5. Result limits:
   - Return at most 50 results.
   - Stable sort by enabled first, then lowercase name, then id.
6. Enabled detection (**revised: compound-key support**):
   - Read `settings.json.enabledPlugins`.
   - Real keys are compound `plugin@market` (e.g. `agent-sdk-dev@claude-plugins-official`), so try both `enabled[id]` and `enabled[id+"@"+source]`; either `true` yields `enabled:true`.
7. Failure behavior:
   - Malformed JSON, unreadable directories, or missing files must not return HTTP 500.
   - Log a concise debug/warn line if useful, but still return an empty compatible response.
8. Method enforcement (**revised: enforce per contract**):
   - Organization search endpoints (connectors/plugins/skills search) accept `POST` only; non-POST bounded-drains/closes the body then returns `405`.
   - `/api/claude_code/discovery/team_usage`, `/notification/preferences`, `/api/claude_code/skills` accept `GET` only; non-GET returns `405`.

### Constraints

1. Do not forward unknown endpoints to configured model providers.
2. Do not log request bodies, authorization headers, API keys, cookies, or bearer tokens.
3. Do not introduce network calls in local compatibility handlers.
4. Do not require host `~/.claude` to exist in Docker.
5. Preserve existing behavior for `/api/claude_cli/bootstrap`, `/api/feature/*`, `/v1/me`, `/api/oauth/profile`, and other already hardcoded endpoints unless a test proves the response must be improved.
6. Keep endpoint classification deterministic and easy to audit; avoid one large regex that hides intent.
7. Do not add persistence for Frame, Design, or telemetry state in this feature.
8. Maintain existing usage accounting behavior for forwarded `/v1/messages` requests.
9. **Bounded drain (gpt-5.6 review revision)**: request-body drain for all local hardcoded/compat/blocked/telemetry/connector endpoints must use bounded `drainRequestBodyLimited` (cap `maxLocalDrainSize`=1MB), including the shared drain in `handleHardcodedEndpoint` (now uniformly bounded). No unbounded `io.Copy` remains, to avoid DoS (CWE-400). Forwarded requests like POST /v1/messages do not use this path (they use handler.go's maxRequestBodySize). Exceeding the cap may close the connection (keep-alive not guaranteed).
10. **Plugin catalog dedup and response id (gpt-5.6 round 2/3)**: dedup key uses `id@source` so same-named plugins across different marketplaces are treated as distinct and not dropped; **the returned JSON `id` field also uses the compound key `plugin@market`** (Claude Code 2.1.206 uses pluginId as the unique locator and does not read a custom source field), matching the `enabledPlugins` key format; falls back to plain plugin id when no market name.
11. **Two skill data sources separated (gpt-5.6 round 3)**:
    - `/skills/search`: scans the full marketplace catalog (4 globs: `skills/*/SKILL.md`, `plugins/marketplaces/*/skills/*/SKILL.md`, `plugins/marketplaces/*/plugins/*/skills/*/SKILL.md`, `plugins/marketplaces/*/external_plugins/*/skills/*/SKILL.md`).
    - `/api/claude_code/skills`: reports **installed** skills only — reads `plugins/installed_plugins.json` `plugins[plugin@market][].installPath`, scans each installPath's `skills/*/SKILL.md`, plus personal `skills/*/SKILL.md`; does not return the whole marketplace catalog or mark all as `health=good`.

### Implementation Review Hotspots

These points are mandatory review gates for the GLM-5.2 implementation:

1. `/v1/models` must be derived from the existing MCC provider/model config structures after reading the current code. The implementer must not invent field names or a parallel model registry.
2. Specific organization handlers such as `/api/oauth/organizations/{orgUUID}/plugins/search`, `/skills/search`, and `/mcp/connectors/*` must be matched before the existing broad `/api/oauth/organizations/` fallback; otherwise the client will continue receiving weak `{}` responses.
3. The fail-closed guard must be placed after `handleHardcodedEndpoint(w, r)` and before config load/provider resolution/request transformation in `ServeHTTP`; otherwise unknown endpoints can still enter the forwarding path.
4. Tests must prove blocked endpoints do not hit a fake upstream provider. Status-code-only tests are insufficient for this feature.

### Edge Cases

1. A request is `GET /v1/messages` instead of `POST` - return local `405`, do not forward.
2. A request is `POST /v1/messages?beta=true` - forward as a model request; existing provider URL logic may strip `beta=true` for non-Anthropic formats.
3. A request is `/v1/models?beta=true` - handle locally using path `/v1/models`.
4. A request has a huge telemetry body - discard/drain without parsing and return `204`.
5. A malformed plugin search body is received - return `{"results":[]}` or unfiltered local results, never `500`.
6. A Frame route includes a slug under `/api/frame/{slug}` - return local not-found unless it is one of the explicit Frame control routes.
7. A WebSocket upgrade request hits `/api/ws/*` - return `501` without hijacking the connection.
8. Docker has no mounted Claude config - plugin/skill search returns empty arrays.
9. Provider config is empty - `/v1/models` returns `{"data":[],"has_more":false}`.
10. Unknown path is `/anthropic/v1/anything-but-messages` - block locally.

### Non-Goals

1. Do not implement real Frame artifact hosting or publishing.
2. Do not implement real Claude Design MCP service.
3. Do not implement remote plugin marketplace federation.
4. Do not emulate Anthropic account billing, organization spend limits, or OAuth token issuance.
5. Do not forward telemetry anywhere, including user-configured providers.
6. Do not change admin UI behavior in this feature.

## Task Details

### Task 1: Endpoint Policy and Fail-Closed Guard

#### Requirements

**Objective** - Make the proxy deny unknown non-model endpoints before provider selection.

**Outcomes** - `ServeHTTP` only forwards explicit model requests; all other unrecognized paths are local responses.

**Evidence** - A test with an `httptest.Server` upstream verifies that `GET /favicon.ico`, `POST /v1/logs`, and `GET /v1/complete` do not hit the upstream server, while `POST /v1/messages` still does.

**Constraints** - Keep the change small and audit-friendly. Preserve existing hardcoded endpoint behavior.

**Edge Cases** - Wrong method on `/v1/messages`; query strings on model paths; unknown `/anthropic/v1/*`.

**Verification** - `go test ./internal/proxy -run 'TestEndpointPolicy|TestServeHTTPFailClosed'`.

#### Plan

1. Create `internal/proxy/endpoint_policy.go`.
2. Define:
   ```go
   type endpointAction int

   const (
       endpointActionLocal endpointAction = iota
       endpointActionForwardModel
       endpointActionBlock
       endpointActionMethodNotAllowed
   )

   type endpointDecision struct {
       action endpointAction
       reason string
   }
   ```
3. Add `classifyForwardingEndpoint(method, path string) endpointDecision` with these exact rules:
   - `POST /v1/messages` -> `endpointActionForwardModel`
   - `POST /anthropic/v1/messages` -> `endpointActionForwardModel`
   - non-POST `/v1/messages` or `/anthropic/v1/messages` -> `endpointActionMethodNotAllowed`
   - everything else -> `endpointActionBlock`
4. Add tests in `internal/proxy/endpoint_policy_test.go` before implementation:
   - `TestClassifyForwardingEndpointAllowsOnlyMessagePosts`
   - `TestClassifyForwardingEndpointRejectsWrongMethod`
   - `TestClassifyForwardingEndpointIgnoresQueryBecausePathIsNormalized`
5. Run `go test ./internal/proxy -run TestClassifyForwardingEndpoint` and confirm it fails before implementation.
6. Implement the classifier.
7. Modify `internal/proxy/handler.go` after `handleHardcodedEndpoint` and before config load:
   ```go
   decision := classifyForwardingEndpoint(r.Method, r.URL.Path)
   switch decision.action {
   case endpointActionForwardModel:
       // continue existing forwarding path
   case endpointActionMethodNotAllowed:
       h.handleBlockedEndpoint(w, r, http.StatusMethodNotAllowed, "method_not_allowed")
       return
   default:
       h.handleBlockedEndpoint(w, r, http.StatusNotFound, decision.reason)
       return
   }
   ```
8. Add `handleBlockedEndpoint` in `internal/proxy/hardcoded.go` or a new `blocked.go`. It must drain the body, set `Content-Type: application/json`, log a safe line, and write the stable JSON error.
9. Add an integration-style handler test that configures a fake provider and asserts an atomic counter remains zero for blocked paths.
10. Run `go test ./internal/proxy -run 'TestClassifyForwardingEndpoint|TestServeHTTPFailClosed'`.
11. Update this spec checklist and progress after the task is completed.

#### Verification

- [x] Classifier unit tests pass. (`TestClassifyForwardingEndpoint*`, `TestEndpointPolicy`)
- [x] Blocked endpoint integration test proves upstream is not called. (`TestServeHTTPFailClosed` asserts atomic counter delta=0)
- [x] `POST /v1/messages` still reaches the existing forwarding path in tests. (positive control delta>=1)

### Task 2: Telemetry, Probe, Model, and Low-Risk Local Responders

#### Requirements

**Objective** - Expand local hardcoded handling for non-model endpoints that should never reach model providers.

**Outcomes** - Telemetry, browser probes, model discovery, team usage, notification preferences, onboarding, and existing count-token behavior are local.

**Evidence** - Tests assert response status/body for every endpoint listed in this task and verify no upstream calls.

**Constraints** - No external network calls. `/v1/models` must use existing MCC config data rather than Anthropic APIs.

**Edge Cases** - Empty provider config; provider models with duplicate names; telemetry requests with large bodies.

**Verification** - `go test ./internal/proxy -run 'TestHardcodedTelemetry|TestHardcodedModels|TestHardcodedLowRiskClaudeCode|TestStaticProbeEndpoints'`.

#### Plan

1. Add failing tests in `internal/proxy/hardcoded_test.go`:
   - `TestStaticProbeEndpointsAreLocal`
   - `TestHardcodedTelemetryOTLPEndpoints`
   - `TestHardcodedModelsUsesConfiguredProviders`
   - `TestHardcodedLowRiskClaudeCodeEndpoints`
2. Static probes:
   - Add exact local matches for `/favicon.ico`, `/robots.txt`, `/apple-touch-icon.png`, `/apple-touch-icon-precomposed.png`.
   - Response: `404`, empty body.
3. Telemetry:
   - Add exact local matches for `/v1/metrics`, `/v1/logs`, `/v1/traces`.
   - `POST` response: `204`, empty body.
   - Non-POST response: `405`, JSON error.
4. Models:
   - Add exact local match for `/v1/models`.
   - Implement `handleModels(w, r)` with `GET` only.
   - Load current config through `h.configManager.Load()`.
   - Collect model ids from configured providers using the same provider/model structures already used by `handleBootstrap`.
   - Before writing this handler, inspect the existing config/provider structs and tests; do not guess new field names.
   - De-duplicate by `id`.
   - Sort by `id`.
   - `id` is the model selection key (unchanged); `display_name` uses `ExposedModel.Label` (trimmed, falls back to id when empty) for friendlier UI display.
   - Response example:
     ```json
     {"data":[{"id":"glm-4.6","type":"model","display_name":"GLM-4.6"}],"has_more":false}
     ```
   - If config load fails or no models exist, return `{"data":[],"has_more":false}`.
5. Low-risk Claude Code APIs:
   - `/api/claude_code/discovery/team_usage` -> `200 {"teams":[],"usage":[],"data":[]}`
   - `/api/claude_code/notification/preferences` -> `200 {"preferences":{},"notifications_enabled":false}`
   - `/api/organizations/{orgUUID}/claude_code/onboarding` -> `200 {}`
6. Preserve existing `/v1/messages/count_tokens` tests and add one regression assertion that it remains local after the fail-closed guard is added.
7. Run the targeted test command and confirm it fails before implementation.
8. Implement the handlers with helper functions:
   - `writeJSON(w, status, value)`
   - `writeNoContent(w)`
   - `methodAllowed(w, r, allowed ...string) bool`
9. Run targeted tests, then `go test ./internal/proxy`.
10. Update this spec checklist and progress.

#### Verification

- [x] Telemetry endpoints return `204` and do not parse payloads. (`TestHardcodedTelemetryOTLPEndpoints`, 64KB body)
- [x] `/v1/models` returns config-derived data or an empty list. (`TestHardcodedModelsUsesConfiguredProviders`; `collectModelIDs` reuses `cfg.Providers[].ExposedModels`)
- [x] Browser probes no longer forward to upstream. (`TestStaticProbeEndpointsAreLocal`)
- [x] Existing hardcoded endpoints remain green. (full suite 1311 pass)

### Task 3: Plugin, Skill, and MCP Connector Compatibility

#### Requirements

**Objective** - Return schema-compatible local responses for Claude Code plugin, skill, and MCP connector discovery endpoints.

**Outcomes** - Connector endpoints return empty result arrays; plugin and skill search return local best-effort results when local Claude config is available.

**Evidence** - Temp-directory tests create fake Claude config/plugin/skill files and verify search responses match the client parser schema.

**Constraints** - Best-effort only. Missing config must not fail. No remote marketplace requests.

**Edge Cases** - Malformed JSON; missing `enabledPlugins`; `keywords` as string vs array; duplicate plugin ids.

**Verification** - `CLAUDE_CONFIG_DIR=$(mktemp -d) go test ./internal/proxy -run 'TestLocalCatalog|TestPluginSkillSearch|TestMCPConnectorEndpoints'`.

#### Plan

1. Create `internal/proxy/local_catalog.go` and `internal/proxy/local_catalog_test.go`.
2. Define:
   ```go
   type localCatalogItem struct {
       ID          string `json:"id"`
       Name        string `json:"name"`
       Description string `json:"description,omitempty"`
       Enabled     bool   `json:"enabled"`
       Source      string `json:"source,omitempty"`
       Kind        string `json:"kind,omitempty"`
   }
   ```
3. Add failing tests:
   - `TestLocalCatalogDirUsesEnvOverride`
   - `TestLocalCatalogLoadsMarketplacePluginJSON`
   - `TestLocalCatalogLoadsSkillsDirectory`
   - `TestSearchLocalCatalogHandlesArrayAndStringKeywords`
   - `TestPluginSkillSearchReturnsEmptyOnMalformedConfig`
4. Implement config-dir resolution:
   - `CLAUDE_CONFIG_DIR` env var first.
   - `os.UserHomeDir()+"/.claude"` fallback.
   - Empty string if neither is available.
5. Implement `loadLocalCatalog(kind string) []localCatalogItem`:
   - For plugins, scan `plugins/marketplaces/*/.claude-plugin/plugin.json` and `marketplace.json`.
   - For skills, scan `skills/*/SKILL.md` and plugin manifests that expose skills.
   - Use loose JSON structs so unknown fields do not matter.
   - Use directory basename as fallback id/name.
6. Implement `readEnabledPlugins(configDir string) map[string]bool`.
7. Implement `parseSearchKeywords(r *http.Request) []string`:
   - Body `{"keywords":["a","b"]}` -> `[]string{"a","b"}`
   - Body `{"keywords":"a b"}` -> `[]string{"a","b"}`
   - Missing/malformed body -> empty keywords
8. Implement `filterCatalog(items, keywords)`:
   - If no keywords, return sorted first 50.
   - All keywords should be matched across searchable text.
   - Sort enabled first, lowercase name, then id.
9. Add local endpoint matches before the existing broad `/api/oauth/organizations/` fallback:
   - `/api/oauth/organizations/{orgUUID}/mcp/connectors/list`
   - `/api/oauth/organizations/{orgUUID}/mcp/connectors/search`
   - `/api/oauth/organizations/{orgUUID}/mcp/connectors/suggest`
   - `/api/oauth/organizations/{orgUUID}/plugins/search`
   - `/api/oauth/organizations/{orgUUID}/skills/search`
   - Add a regression assertion that these specific handlers are reached before the broad organization fallback.
10. Connector handlers return `200 {"results":[]}`.
11. Plugin/skill handlers return `200 {"results":[...]}`.
12. Add `GET /api/claude_code/skills` handler:
   - Return `{"skills":[]}` when no local skills exist.
   - For local skills, return `{"skills":[{"skill_name":"name","health":"good","source":"local"}]}`.
13. Run targeted tests, then `go test ./internal/proxy`.
14. Update this spec checklist and progress.

#### Verification

- [x] Search endpoints return client-compatible `results`. (`TestPluginSkillSearchReturnsConfigDerivedResults`)
- [x] Missing `~/.claude` returns empty results, not `500`. (`TestPluginSkillSearchReturnsEmptyOnMalformedConfig`)
- [x] Existing broad `/api/oauth/organizations/` fallback does not mask the new specific handlers. (`handleOrgScopedSearch` runs before drain/switch; regression asserts fallback still returns `{}`)

### Task 4: Frame Artifact Compatibility

#### Requirements

**Objective** - Prevent Frame artifact endpoints from reaching model providers while keeping the Claude Code client behavior controlled.

**Outcomes** - Listing and tracking work as harmless no-ops; publishing returns a recognized write-gate denial; contracts and artifact slugs return local not-found/unavailable responses.

**Evidence** - Tests cover every Frame route and status/body contract.

**Constraints** - Do not implement artifact persistence or remote publish. Avoid fake contract data because the client validates contract versions.

**Edge Cases** - `/api/frame/frames?limit=200`; unknown slug; method mismatch; contract subpaths.

**Verification** - `go test ./internal/proxy -run TestFrameEndpointCompatibility`.

#### Plan

1. Add failing tests in `internal/proxy/hardcoded_frame_test.go` or `hardcoded_test.go`:
   - `TestFrameFramesReturnsEmptyList`
   - `TestFrameTrackAndDeployCompleteReturnNoContent`
   - `TestFrameDeployInitDirectReturnWriteGateDenied`
   - `TestFrameContractReturnsUnavailable`
   - `TestFrameSlugReturnsNotFound`
2. Add a prefix local match for `/api/frame/`.
3. Implement `handleFrameEndpoint(w, r)` with route order:
   - `GET /api/frame/frames` -> `200 {"frames":[]}`
   - `POST /api/frame/track` -> `204`
   - `POST /api/frame/deploy/complete` -> `204`
   - `POST /api/frame/deploy/init` -> `403 {"error":"Frame publishing is unavailable in MCC local mode","reason":"write_gate_disabled"}`
   - `POST /api/frame/deploy/direct` -> same 403
   - `GET /api/frame/contract/*` -> `404 {"error":"Frame contract service is unavailable in MCC local mode","reason":"local_unavailable"}`
   - `GET /api/frame/{slug}` -> `404 {"error":"Artifact not found in MCC local mode","reason":"not_found"}`
   - `DELETE /api/frame/{slug}` -> same 404
   - unmatched methods -> `405`
4. Ensure query strings are ignored for matching. `GET /api/frame/frames?limit=200` must match `/api/frame/frames`.
5. Ensure request bodies are drained for POST routes.
6. Run targeted tests and `go test ./internal/proxy`.
7. Update this spec checklist and progress.

#### Verification

- [x] Frame list is an empty array. (`TestFrameEndpointCompatibility`)
- [x] Tracking and deploy completion are no-op `204`.
- [x] Publish attempts fail with `reason:"write_gate_disabled"` recognized by the client.
- [x] No Frame route forwards upstream. (prefix `/api/frame/` registered as hardcoded, intercepted before the guard)

### Task 5: Design and Unsupported Streaming Compatibility

#### Requirements

**Objective** - Handle Claude Design and voice streaming endpoints locally with predictable unsupported behavior.

**Outcomes** - Consent reads/mutations are accepted locally; Design MCP and voice stream are blocked with clear unsupported errors.

**Evidence** - Tests verify status/body for `GET`, `POST`, and `DELETE /v1/design/consent`, `POST /v1/design/mcp`, and `/api/ws/*`.

**Constraints** - Do not implement a JSON-RPC MCP bridge or WebSocket streaming. Do not perform external calls.

**Edge Cases** - JSON-RPC body on `/v1/design/mcp`; WebSocket upgrade headers; DELETE consent.

**Verification** - `go test ./internal/proxy -run 'TestDesignEndpointCompatibility|TestUnsupportedStreamingEndpoints'`.

#### Plan

1. Add failing tests:
   - `TestDesignConsentCompatibility`
   - `TestDesignMCPReturnsUnsupported`
   - `TestUnsupportedStreamingEndpoints`
2. Add exact local matches:
   - `/v1/design/consent`
   - `/v1/design/mcp`
3. Add prefix local match:
   - `/api/ws/`
4. Implement `handleDesignConsent`:
   - `GET` -> `200 {"agent_design_projects":false}`
   - `POST` -> `204`
   - `DELETE` -> `204`
   - other methods -> `405`
5. Implement `handleDesignMCP`:
   - `POST` -> `403 {"error":{"type":"unsupported_local_endpoint","message":"Claude Design is unavailable in MCC local mode"}}`
   - other methods -> `405`
6. Implement `handleUnsupportedStreamingEndpoint`:
   - Any method -> `501 {"error":{"type":"unsupported_local_endpoint","message":"Streaming endpoint is unavailable in MCC local mode"}}`
   - Do not attempt to upgrade or hijack the connection.
7. Drain request bodies before response.
8. Run targeted tests and `go test ./internal/proxy`.
9. Update this spec checklist and progress.

#### Verification

- [x] Design consent no longer forwards. (`TestDesignEndpointCompatibility`: GET 200 / POST·DELETE 204 / other 405)
- [x] Design MCP returns a controlled unsupported error. (`403 unsupported_local_endpoint`)
- [x] WebSocket/audio path is blocked locally. (`TestUnsupportedStreamingEndpoints`: `501`, no Upgrade header, no hijack)

### Task 6: Logging and Diagnostics

#### Requirements

**Objective** - Make blocked endpoints visible in logs without leaking sensitive data.

**Outcomes** - Known local endpoints keep existing hardcoded logs; unknown blocked endpoints produce one structured line with method, host, path, query, user agent, status, and reason.

**Evidence** - Tests capture logger output or inject a logger hook to verify blocked endpoint logs do not contain body/auth content.

**Constraints** - Do not log request bodies or sensitive headers. Keep log volume bounded: one line per blocked request.

**Edge Cases** - Query string contains token-like values; body contains API key; Authorization header present.

**Verification** - `go test ./internal/proxy -run TestBlockedEndpointLogging`.

#### Plan

1. Add failing test `TestBlockedEndpointLogging`:
   - Send blocked endpoint with `Authorization: Bearer secret`, `Cookie: a=b`, body `api_key=secret`, and query `token=secret`.
   - Assert log contains method/path/status/reason.
   - Assert log does not contain body or authorization/cookie header values.
2. Implement log line in `handleBlockedEndpoint`:
   ```text
   [Hardcoded] Blocking unknown endpoint method=GET host=api.anthropic.com path=/v1/complete query_present=true status=404 reason=unknown_non_model_endpoint ua="..."
   ```
3. Log only whether query is present, not the raw query string, unless the existing project logging standard already logs query. Prefer `query_present=true`.
4. Keep existing local handler logs for known endpoints:
   ```text
   [Hardcoded] Handling METHOD /path
   ```
5. Run targeted test and `go test ./internal/proxy`.
6. Update this spec checklist and progress.

#### Verification

- [x] Block logs identify the endpoint and reason. (`TestBlockedEndpointLogging` asserts method/path/status/reason/query_present/ua present)
- [x] Logs do not contain request body, authorization, cookies, or raw query tokens. (asserts `secret`/`Bearer`/`api_key`/`a=b`/`token=secret` absent)
- [x] Logs are not duplicated for a single request. (`TestBlockedEndpointLogInjectionGuard` + `TestOrgEndpointsSingleHandlingLog`; path/UA control chars sanitized against CWE-117 log injection)

### Task 7: Regression Verification and Endpoint Matrix

#### Requirements

**Objective** - Prove the feature is safe across existing proxy behavior and document the endpoint matrix for future Claude Code updates.

**Outcomes** - Full Go tests pass; optional endpoint extraction script or documented command exists; spec records actual verification evidence.

**Evidence** - `go test ./...` passes. If frontend is untouched, frontend tests/build can be skipped with a note; if any admin/frontend files are touched, run frontend validation.

**Constraints** - Do not change generated frontend `dist` unless frontend source changes. Do not commit unless the user requests a commit.

**Edge Cases** - Dirty worktree unrelated to this feature; unavailable Docker daemon; local Claude config missing.

**Verification** - `go test ./...`.

#### Plan

1. Run:
   ```bash
   go test ./internal/proxy
   go test ./...
   ```
2. If any frontend/admin UI source changed, also run:
   ```bash
   npm --prefix internal/frontend test
   npm --prefix internal/frontend run build
   ```
3. Add or document an endpoint extraction command in this spec for future updates:
   ```bash
   rg -o '"/(api|v1|mcp-registry|anthropic)[^"]*"' /path/to/claude_code_src_2.1.206.js | sort -u
   ```
4. Review `git diff --stat` and ensure changes are limited to:
   - `internal/proxy/*`
   - this feature spec progress updates
   - optional tests/helpers under `internal/proxy`
5. Manually test Docker logs after one normal Claude Code startup if a Docker runtime is available:
   ```bash
   docker logs --since 10m mcc | rg 'Hardcoded|Blocking unknown endpoint|Forwarding request'
   ```
6. Record actual command outputs in this spec's task verification checkboxes.
7. Leave the feature in `validated` status only after tests pass and blocked-endpoint behavior is observed or unit-proven.

#### Verification

- [x] `go test ./internal/proxy` passes. (415 tests pass)
- [x] `go test ./...` passes. (1311 tests pass across 16 packages; frontend/admin untouched, so npm verification skipped)
- [x] Endpoint matrix in this spec matches implemented handlers. (every 2.1.206 endpoint is either handled locally or blocked; only `POST /v1/messages` and `POST /anthropic/v1/messages` forward)
- [x] No unknown non-model endpoint can reach provider forwarding in tests. (`TestServeHTTPFailClosed` atomic-counter assertion)

#### Actual verification evidence

```text
# Task 1-6 acceptance commands
go test ./internal/proxy -run 'TestEndpointPolicy|TestServeHTTPFailClosed'                 # 26 passed
go test ./internal/proxy -run 'TestClassifyForwardingEndpoint|TestServeHTTPFailClosed'     # 43 passed
go test ./internal/proxy -run 'TestStaticProbeEndpoints|TestHardcodedTelemetry|TestHardcodedModels|TestHardcodedLowRiskClaudeCode'  # 19 passed
CLAUDE_CONFIG_DIR=$(mktemp -d) go test ./internal/proxy -run 'TestLocalCatalog|TestPluginSkillSearch|TestMCPConnectorEndpoints'      # ok
go test ./internal/proxy -run TestFrameEndpointCompatibility                                # 10 passed
go test ./internal/proxy -run 'TestDesignEndpointCompatibility|TestUnsupportedStreamingEndpoints'  # 12 passed
go test ./internal/proxy -run 'TestBlockedEndpointLogging|TestBlockedEndpointLogInjectionGuard|TestOrgEndpointsSingleHandlingLog'  # passed

# Regression
go test ./internal/proxy   # 415 passed
go test ./...              # 1311 passed
go vet ./...               # No issues
```

#### Implementation file inventory (diff scoped to internal/proxy/)

```text
M internal/proxy/handler.go              # ServeHTTP inserts fail-closed guard
M internal/proxy/hardcoded.go            # registry + new endpoint handlers + handleModels
M internal/proxy/hardcoded_test.go       # task 2 tests
+ internal/proxy/endpoint_policy.go      # classifier
+ internal/proxy/endpoint_policy_test.go # classifier + fail-closed integration tests
+ internal/proxy/blocked.go              # handleBlockedEndpoint + safe logging + sanitization
+ internal/proxy/blocked_test.go         # blocked response body + log safety + injection guard
+ internal/proxy/helpers.go              # writeNoContent / methodAllowed / encodeJSONBody
+ internal/proxy/local_catalog.go        # local catalog load + search + org handlers
+ internal/proxy/local_catalog_test.go   # catalog / search / connector / skills tests
+ internal/proxy/frame.go                # Frame artifact handlers
+ internal/proxy/frame_test.go           # Frame contract tests
+ internal/proxy/design_streaming.go     # Design consent/mcp + ws blocking
+ internal/proxy/design_streaming_test.go# Design/ws tests
```

Endpoint-matrix extraction command for future Claude Code updates:

```bash
rg -o '"/(api|v1|mcp-registry|anthropic)[^"]*"' /path/to/claude_code_src_<VERSION>.js | sort -u
```

## Appendix: gpt-5.6 Review Fixes (2026-07-10)

Based on gpt-5.6's review of the first implementation and cross-checking the real `~/.claude` config, three issue classes were fixed:

1. **Bounded drain (medium / DoS)**: added `drainRequestBodyLimited(r, maxLocalDrainSize)` (1MB cap), used by `handleBlockedEndpoint`, `handleTelemetry`, `handleMCPConnectors`, and the org-search non-POST branch. Telemetry moved from the post-drain switch to the pre-drain section so it no longer hits the shared unbounded `drainRequestBody`. Tests: `TestBlockedEndpointLargeBodyBoundedDrain`, `TestHardcodedTelemetryOTLPEndpoints/POST_with_oversized_body`.
2. **marketplace.json plugins[] parsing (medium / insufficient data)**: `loadPlugins` now parses each marketplace's `marketplace.json` `plugins[]` array (fields `name`/`displayName`/`description`/`category`/`tags`/`keywords`), covering the official marketplace's 255+ plugins; `source` = marketplace name matches the `enabledPlugins` compound-key suffix. Enabled lookup supports compound key `plugin@market` (`isPluginEnabled`). Skill glob expanded to `plugins/marketplaces/*/skills/*/SKILL.md` (plugin-provided skills). When file parsing yields nothing, `~/.claude.json` `pluginUsage`/`skillUsage` keys are used as a name-only fallback. Tests: `TestLocalCatalogLoadsMarketplacePluginJSON`, `TestPluginProvidedSkillsLoaded`, `TestClaudeJSONFallbackWhenNoFiles`.
3. **Method enforcement (low-medium / contract consistency)**: organization search endpoints (connectors/plugins/skills search) are now POST-only (non-POST bounded-drains + 405); `team_usage`/`notification/preferences`/`/api/claude_code/skills` are GET-only (405). Tests: `TestOrgScopedSearchRejectsNonPost`, the 405 subtests in `TestHardcodedLowRiskClaudeCodeEndpoints`.

After fixes: `go test ./internal/proxy` 448 pass, `go test ./...` 1344 pass, `go vet ./...` clean.

## Appendix: gpt-5.6 Round-2 Review Fixes (2026-07-10)

Round 2 found the shared `drainRequestBody` (`handleHardcodedEndpoint` line 144) was still an unbounded `io.Copy`; several new local endpoints (frame/design/ws, models non-GET, favicon with body, etc.) hit it before reaching their handler. Fixes:

1. **Shared drain → bounded**: the shared drain in `handleHardcodedEndpoint` is now uniformly `drainRequestBodyLimited(r, maxLocalDrainSize)`; the unbounded `drainRequestBody` (no remaining callers) was removed as dead code. POST /v1/messages is unaffected (does not use this path). Test: `TestLocalEndpointsBoundedDrainOnOversizedBody` (9 cases: frame track/deploy, design mcp/consent, ws voice_stream, favicon, models non-GET, feedback, bootstrap — all return within contract status with a >2MB body).
2. **Plugin catalog dedup by compound key**: `loadPlugins` internal `seen` key is now `id@source`, so same-named plugins across marketplaces are no longer dropped; the returned JSON `id` field still holds the plugin id. Test: `TestPluginCatalogDedupByCompoundKey`.

Verification: `go test ./internal/proxy -race` 460 pass; `go test ./... -count=1` 1356 pass; `go vet ./...` clean; `display_name` now uses `ExposedModel.Label` (falls back to id).

## Appendix: gpt-5.6 Round-3 Review Fixes (2026-07-10)

Round 3 found incomplete skill scanning, a bounded-drain test that could not prevent regressions, and cross-marketplace same-name plugin identity ambiguity. Fixes:

1. **Complete skill scanning + two data sources separated**: `loadSkills` expanded to 4 globs (market-level + `plugins/<p>/skills/` + `external_plugins/<p>/skills/`), covering the real directory's 56 market-level + 42 plugin-internal + 7 external skills. New `loadInstalledSkills` reads `plugins/installed_plugins.json` installPaths, scanning only installed plugins' cache `skills/*/SKILL.md` + personal `skills/*/SKILL.md`. `/skills/search` uses `loadSkills` (full marketplace catalog); `/api/claude_code/skills` now uses `loadInstalledSkills` (installed only, no longer returns the whole catalog marked good). Tests: the fixture separates a marketplace-only skill (plugin-skill, not installed) from an installed skill (installed-skill, via installed_plugins.json → cache), asserting the former is absent from skills health and the latter present.
2. **Bounded-drain test switched to counting assertions**: added `syntheticCountReader` (records bytes read + Close); `TestDrainRequestBodyLimitedStopsAtCap` asserts `drainRequestBodyLimited` reads exactly `maxLocalDrainSize` bytes, calls Close, and does not over-read. Even with far-more-than-cap data it stays bounded — reverting to unbounded `io.Copy` would make `body.read` equal `total` (not the cap), failing the test.
3. **Plugin response id uses compound key `plugin@market`**: `loadPlugins` sets `item.ID = name@market` (falls back to plain name without a market), so the client can uniquely locate cross-marketplace same-name plugins; matches the `enabledPlugins` key format, so `isPluginEnabled(enabled, item)` hits via `enabled[item.ID]` directly. The `.claude.json` fallback uses the compound id too.

Verification: `go test ./internal/proxy -race` 461 pass; `go test ./... -count=1` 1357 pass; `go vet ./...` clean; `gofmt -d` no output.

**Maintenance hardening (gpt-5.6 non-blocking note)**: plugin-internal/cache skill `source` is now `plugin@market` (includes the marketplace), so same-named skills from same-named plugins across marketplaces get a unique dedup key (`id@source`) and are not merged; market-level skills keep source = market name. Test: `TestSkillDedupDisambiguatesSameNameAcrossMarkets`. Also fixed the stale comment in `TestPluginCatalogDedupByCompoundKey`.

**Marketplace name normalization (gpt-5.6 round-3 follow-up, medium)**: `buildMarketNameMap` builds a "dir name -> canonical marketplace name" map once at the start of `loadSkills`/`loadInstalledSkills` (reading `marketplaces/<dir>/.claude-plugin/marketplace.json`'s `name`, falling back to the dir name). `skillPathSource`/`canonicalMarketName` build the source from the canonical name, preventing duplicate skills when a temp dir (e.g. `temp_1772758330775`) and the canonical dir (`claude-plugins-official`) point to the same marketplace (14 duplicates observed on the real config). Test: `TestSkillDedupNormalizesTempMarketplaceDir` (two dirs with the same manifest name + same plugin/skill -> one entry kept, source = `plugin@claude-plugins-official`). Verification: `go test ./internal/proxy -race` 463; `go test ./... -count=1` 1359.
