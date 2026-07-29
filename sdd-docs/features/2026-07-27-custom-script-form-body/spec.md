# Custom Script Form Body & Additional Secret Spec (Qianwen Token Plan Usage)

Local page: admin provider card "Usage" modal (`ProviderUsageModal.vue`) / Proxy entry: no change to model proxy chain; new fields reuse existing `/api/providers/{id}/usage*` admin APIs / Reference sources: qianwen AI platform console `platform.qianwenai.com` private gateway `cs-data.qianwenai.com` (no official public API docs; this spec is based on a live capture from 2026-07-27) / Stack: Go 1.26 stdlib + `github.com/dop251/goja` + Vue 3 + TypeScript + Tailwind / Last updated: 2026-07-27 / Status: validating / Progress: 7 / 7 implemented

## Overall Analysis (Source Analysis)

### 1. Goal & Background

The user purchased a Token Plan Individual subscription on the qianwen AI platform (Alibaba Bailian / qianwenai) and wants the mcc provider card's "Usage" area to show the 5-hour / 7-day used percentage and reset countdown, just like Kimi, Zhipu and MiniMax.

Qianwen Token Plan usage has **no official public API**. The console page `platform.qianwenai.com/home/billing/subscription/token-plan-individual` pulls usage via the private gateway `cs-data.qianwenai.com`. Based on a live capture of that gateway on 2026-07-27 and its auth model, this spec decides **not to add a native adapter for qianwen**, but instead to **enhance the existing `custom` script mechanism** so it can express private endpoints of the shape "POST form-urlencoded body + dual credentials (Cookie + session token)". Qianwen is the first configuration example.

Relationship to existing features: this is a capability enhancement inside the `internal/providerquota` package only. It adds no new package, changes no unified result model, and does not touch `internal/usage`.

### 2. Qianwen Endpoint Source Analysis (2026-07-27 live capture)

#### 2.1 Request shape

```text
POST https://cs-data.qianwenai.com/data/api.json?product=sfm_bailian&action=BroadScopeAspnGateway&api=zeldaHttp.apikeyMgr.%2Ftokenplan%2Fpersonal%2Fapi%2Fv2%2Fusage
Content-Type: application/x-www-form-urlencoded
Cookie: login_qianwenai_ticket=...; login_aliyunid_pk=1758748083928576; cna=...; isg=...; tfstk=...; xlly_s=1; account_info_switch=close
Origin: https://platform.qianwenai.com
Referer: https://platform.qianwenai.com/home/billing/subscription/token-plan-individual?...

# body (form-urlencoded, fields shown decoded):
product=sfm_bailian
action=BroadScopeAspnGateway
sec_token=WmFrs6jduM1ff0WGARDqN
region=cn-beijing
params={"Api":"zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage","Data":{"cornerstoneParam":{"domain":"platform.qianwenai.com","consoleSite":"QIANWENAI","console":"ONE_CONSOLE","xsp_lang":"zh-CN","protocol":"V2","productCode":"p_efm"}},"V":"1.0"}
```

#### 2.2 Auth model (key)

| Credential | Location | Source | Lifetime |
| --- | --- | --- | --- |
| `Cookie` header (core: `login_qianwenai_ticket`) | HTTP request header | Browser login state | Bound to Alibaba Cloud console session; hours to days |
| `sec_token` | form body field | Console global `window.ALIYUN_CONSOLE_CONFIG.SEC_TOKEN` | Bound to the same login session; expires together with Cookie |
| `login_aliyunid_pk` (account id) | Inside Cookie | Set after login | Follows Cookie |

**Conclusion: qianwen usage auth = Cookie + sec_token, two independent secrets bound to the same login session, expiring together.** This is the same class as mcc spec `2026-06-27-provider-quota-query` §6 "Xiaomi MiMo Investigation & Deferral" (depends on browser session Cookie, not a stable API Key). This spec therefore follows that precedent: **no native adapter for qianwen**; users configure it via the `custom` template and accept "manually refresh Cookie + sec_token every few days". The user explicitly accepted this trade-off (confirmed 2026-07-27).

#### 2.3 Response shape & field mapping

```json
{
  "code": "200",
  "data": {
    "DataV2": {
      "ret": ["SUCCESS::接口调用成功"],
      "data": {
        "msg": "Success.",
        "code": "SUCCESS",
        "data": {
          "per5HourPercentage": 0.0,
          "per1WeekResetTime": 1785462900000,
          "per1WeekPercentage": 1.0
        },
        "requestId": "...",
        "success": true
      }
    },
    "success": true,
    "httpStatus": 200,
    "errorCode": "",
    "api": "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage",
    "errorMsg": ""
  },
  "httpStatusCode": "200",
  "requestId": "...",
  "successResponse": true
}
```

The page shows "5h 剩余量 100.0%" for `per5HourPercentage: 0.0` and "7d 剩余量 0.0%" for `per1WeekPercentage: 1.0` (confirmed by the 2026-07-27 end-to-end run). **`perXxxPercentage` is a 0–1 "used ratio"** (0.0 = used 0% = remaining 100%; 1.0 = used 100% = remaining 0%), **not a 0–100 used percentage** — `per5HourPercentage:0.0` alone cannot distinguish the two semantics; it is `per1WeekPercentage:1.0` together with the page's "remaining 0%" that settles it. mcc's `utilization` is always a 0–100 used percentage, so the extractor must do `percentage * 100`. Mapping to mcc unified result:

| Qianwen field | Meaning | mcc normalized field |
| --- | --- | --- |
| `data.DataV2.data.data.per5HourPercentage` | 5h used ratio (0–1) | `tiers[].window="five_hour"`, `utilization = value × 100` |
| `data.DataV2.data.data.per1WeekPercentage` | 7d used ratio (0–1) | `tiers[].window="seven_day"`, `utilization = value × 100` |
| `data.DataV2.data.data.per1WeekResetTime` | ms epoch | `tiers[].resets_at` |

`parseResetTime` (`internal/providerquota/normalize.go:202-229`) already handles millisecond timestamps (`t > 1e12` → `time.UnixMilli`); no change needed.

### 3. Three limitations of the current custom script mechanism

Comparing the live request with the current `custom` script executor, three required changes emerge (all in `internal/providerquota/script.go` and friends):

#### Limitation A: body can only be JSON-serialized, not form-urlencoded

`script.go:241-252` `doHTTPRequest`:

```go
if req.Body != nil {
    bodyBytes, err := json.Marshal(req.Body)   // always JSON
    ...
    bodyReader = bytes.NewReader(bodyBytes)
}
```

Qianwen requires `application/x-www-form-urlencoded`. A JSON body parsed as form by the gateway will fail (verified 2026-07-27: GET rejected by gateway; original form returns 200; JSON body could not be directly tested from the browser due to CORS preflight but is rejected by protocol analysis).

#### Limitation B: placeholder substitution does not cover body

`script.go:69-73`:

```go
reqConfig.URL = substitutePlaceholders(reqConfig.URL, placeholderValues)
for k, v := range reqConfig.Headers {
    reqConfig.Headers[k] = substitutePlaceholders(v, placeholderValues)
}
// body is never substituted
```

This is the security design of spec `2026-06-27` §4.1 ("secrets are substituted only by Go in the final HTTP request, never enter the script runtime") — but it only covers URL and headers. Qianwen's `sec_token` must appear in the body, so placeholder substitution must be extended to body (still in the Go layer; the security model is unchanged).

#### Limitation C: custom/general template has only one secret slot

`manager.go:237-247` (custom/general branch):

```go
placeholders := map[string]string{
    "baseUrl": plan.scriptURL,
    "apiKey":  plan.token,   // only ScriptAPIKey or fallback card APIToken
}
```

`resolve.go:184-193` (custom/general branch) resolves only `ScriptAPIKey`. Qianwen needs two independent secrets (Cookie + sec_token); a second symmetric secret slot `ScriptAPIKey2` + placeholder `{{apiKey2}}` is required.

### 4. Core Design

#### 4.1 Three enhancements (minimal, general, not qianwen-specific)

1. **`ScriptRequest.BodyType`** (`script.go`): a script may declare `request.bodyType: "form"`; default `""`/`"json"` keeps the current JSON serialization (fully backward compatible). When `bodyType:"form"`, `body` must be an object and Go encodes it with `url.Values`: scalar values become strings directly; object/array values are first `json.Marshal`-ed then used as the field value (so a script may write `params: {...}` equivalently to `params: JSON.stringify({...})`).
2. **Body placeholder substitution** (`script.go`): a new `substitutePlaceholdersInBody(body, values)` recursively walks the body before encoding and applies the same placeholder substitution to every string value. Applies to both JSON and form bodies. Security model unchanged (substitution stays in the Go layer; the script runtime never sees real values).
3. **`ScriptAPIKey2` + `{{apiKey2}}`** (`types.go`/`resolve.go`/`manager.go`/admin handler/frontend): symmetric to `ScriptAPIKey`; available under custom/general as a second independent secret slot.

#### 4.2 Qianwen configuration shape (first application; the end-to-end verification target in Task 6)

| Field | Value |
| --- | --- |
| Template | `custom` |
| Base URL | `https://cs-data.qianwenai.com` |
| Script API Key (`apiKey`) | Full Cookie string |
| Additional secret (`apiKey2`) | `sec_token` value (e.g. `WmFrs6jduM1ff0WGARDqN`) |
| Script | see Task 6 |
| Timeout | 10s (default) |
| Auto Query Interval | user's choice (default 5min) |

Origin check: request URL `https://cs-data.qianwenai.com/data/api.json?...` shares scheme+host+port with Base URL `https://cs-data.qianwenai.com`, satisfying `validateScriptRequest` (`script.go:330-358`). `Origin`/`Referer` are set explicitly by the script (not in the forbidden-header list `script.go:369-375`); the gateway does not enforce same-origin strictly, but setting `Referer` maximizes compatibility (see Task 6 script).

#### 4.3 Why no native adapter for qianwen

Following the MiMo precedent in `2026-06-27-provider-quota-query` §6:

1. Auth depends on a browser login session Cookie + session-level `sec_token`, not a stable API Key.
2. `cs-data.qianwenai.com` is a console private gateway with no official public protocol guarantee; it may change on frontend release.
3. Native adapters (`token_plan.go`) target "stable API Key + official endpoint" providers (Kimi/Zhipu/MiniMax/ZenMux/Volcengine); qianwen does not meet that bar.

Qianwen therefore remains a `custom` template configuration example with a recommended script and credential-gathering steps, and is **not** added to `DetectTokenPlanProvider` host detection.

### 5. Risk Summary

1. **Credential expiry**: when Cookie + sec_token expire together, the query returns `upstream_business_error` (qianwen `code!="200"`) or `invalid_credentials` (HTTP 401/403). The frontend translates existing error codes; `last_success_json` retains the last success (existing mechanism, no change). The user re-acquires Cookie + sec_token and updates both secret slots. No auto-renewal inside mcc (would require Alibaba account password/OAuth, out of scope).
2. **Backward compatibility of body substitution**: existing `general` default script and all stock custom scripts do not contain `{{...}}` in body (because body was never substituted before); introducing body substitution is a no-op for them (`strings.ReplaceAll` on a string without the placeholder is a no-op). Task 3 must cover "JSON body without placeholders is byte-identical".
3. **Form body encoding determinism**: `url.Values.Encode()` outputs keys in sorted order; form-urlencoded does not care about field order, so the gateway tolerates it. Task 3 asserts the encoded string verbatim.
4. **Second secret slot semantics**: `ScriptAPIKey2` has no domain name (not `sec_token`/`cookie`) because it serves any custom script's second-secret need. The frontend label is generic "Additional secret (apiKey2)" with a tooltip explaining usage.
5. **`NormalizeForTemplate` field cleanup**: `resolve.go:36-72` clears irrelevant fields per template. `ScriptAPIKey2` belongs to the same "independent security domain" as `ScriptAPIKey`/`ZenMuxAPIKey` (`resolve.go:33-35` comment) and is **preserved for all templates, never cleared**; safety is guaranteed by `resolveQueryPlan` — only the custom/general branch reads it, so residue is never misused (same as `ScriptAPIKey` residue in newapi/token_plan that is never read).
6. **Frontend modal field density**: the custom template already has Base URL + Script API Key + script editor; adding "Additional secret" increases vertical height. Task 5 places it directly under script_api_key in the same grid; mobile layout must not break.
7. **Public API must not leak secrets**: `PublicQuotaConfig` (`types.go:366-382`) must expose only `script_api_key_2_configured: bool`, never the plaintext. Tasks 1 and 4 together guarantee this.

## Development Checklist

| # | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | config: `ScriptAPIKey2` field + public DTO + validation | `internal/providerquota/types.go` | JSON round-trip; `ToPublicConfig` does not leak plaintext |
| 2 | Planned | resolve + manager: `token2` resolution + `{{apiKey2}}` placeholder + field cleanup | `internal/providerquota/resolve.go`, `internal/providerquota/manager.go` | `resolveQueryPlan` custom/general carries token2; placeholders contain apiKey2 |
| 3 | Planned | script.go: `bodyType:"form"` encoding + body placeholder substitution | `internal/providerquota/script.go` | form encoding assertions; body substitution; JSON backward compatibility |
| 4 | Planned | admin handler: `script_api_key_2` secret update semantics | `internal/admin/provider_quota_handler.go` | preserve/replace/clear three states; public response configured flag |
| 5 | Planned | frontend: custom/general "Additional secret" input + i18n + API types | `ProviderUsageModal.vue`, `quotaForm.ts`, `useApi.ts`, `useI18n.ts` | bilingual; save/test payloads; npm test + build |
| 6 | Planned | qianwen config example + end-to-end verification + doc write-back | Task 6 script in this spec; verification evidence | real Cookie+sec_token produces 5h/7d tiers |

## Requirements

### 1. Scope

#### 1.1 Must deliver

1. `custom`/`general` supports `request.bodyType: "form"`; Go encodes an object body with `url.Values`; missing or `"json"` `bodyType` preserves the current JSON serialization.
2. Placeholder substitution (`{{baseUrl}}` `{{apiKey}}` `{{apiKey2}}` `{{accessToken}}` `{{userId}}`) is extended to all string values inside `request.body`, for both JSON and form bodies; substitution happens before encoding, in the Go layer.
3. `ProviderQuotaConfig` gains `ScriptAPIKey2 string`, symmetric to `ScriptAPIKey`: under custom/general it is the second independent secret, referenced via `{{apiKey2}}`; it shares `ScriptAPIKey`'s independent security domain and is not cleared by `NormalizeForTemplate` (other template branches do not read it).
4. `PublicQuotaConfig` gains `ScriptAPIKey2Configured bool`; `ToPublicConfig` outputs only that boolean; admin public responses (GET config, batch snapshots) never return `script_api_key_2` plaintext.
5. admin `PUT /api/providers/{id}/usage` secret-update semantics supports `script_api_key_2` (non-empty replaces; `clear_script_api_key_2=true` clears; omitted preserves), symmetric to `script_api_key`; `POST /usage/test` draft supports it too.
6. Frontend `ProviderUsageModal.vue` adds an "Additional secret (apiKey2)" input (password type + "clear" button when configured) directly under "Script API Key", visible under custom/general, visually consistent with script_api_key.
7. Task 6 provides the complete qianwen Token Plan custom script and config parameters, and records one real end-to-end query (5h/7d tier values matching the page).

#### 1.2 Non-goals

1. No native adapter for qianwen; not added to `DetectTokenPlanProvider`.
2. No auto-renewal of Cookie / sec_token.
3. No change to `ProviderQuotaResult`/`QuotaTier`/`BalanceItem`.
4. No change to `internal/usage` or the model proxy chain.
5. No new `fetch`/file/env/process capability exposed to scripts (spec `2026-06-27` §4.1 unchanged).
6. No encryption of `ScriptAPIKey2` (existing local-config security model).
7. `bodyType:"form"` does not accept non-object body (string/array/number) — form body is by definition a `key=value` map.

### 2. Data model changes

#### 2.1 `ProviderQuotaConfig` (`internal/providerquota/types.go:34-53`)

Add after `ScriptAPIKey`:

```go
type ProviderQuotaConfig struct {
    // ...existing fields...
    BaseURL            string `json:"base_url,omitempty"`
    ScriptAPIKey       string `json:"script_api_key,omitempty"`
    ScriptAPIKey2      string `json:"script_api_key_2,omitempty"` // NEW: second secret for custom/general
    ZenMuxBaseURL      string `json:"zenmux_base_url,omitempty"`
    // ...
}
```

`HasSecrets` (`types.go:159-165`) appends `|| c.ScriptAPIKey2 != ""`.

#### 2.2 `PublicQuotaConfig` (`types.go:366-382`)

Add after `ScriptAPIKeyConfigured`:

```go
type PublicQuotaConfig struct {
    // ...
    ScriptAPIKeyConfigured    bool   `json:"script_api_key_configured"`
    ScriptAPIKey2Configured   bool   `json:"script_api_key_2_configured"` // NEW
    // ...
}
```

`ToPublicConfig` (`types.go:386-406`) appends `ScriptAPIKey2Configured: c.ScriptAPIKey2 != ""`.

#### 2.3 `queryPlan` (`resolve.go:21-31`)

Add `token2 string` (custom/general second secret).

#### 2.4 `ScriptRequest` (`script.go:27-32`)

Add `BodyType string`:

```go
type ScriptRequest struct {
    URL      string            `json:"url"`
    Method   string            `json:"method"`
    Headers  map[string]string `json:"headers,omitempty"`
    Body     any               `json:"body,omitempty"`
    BodyType string            `json:"bodyType,omitempty"` // NEW: "form" | "" | "json"
}
```

### 3. Form body encoding rules (Task 3 contract)

When `ScriptRequest.BodyType == "form"`:

1. `Body` must be `map[string]any` (object). Other types → `script_error` ("form body must be an object").
2. Body is recursively placeholder-substituted (before encoding; see §4).
3. Build `url.Values`; for each key/value:
   - `string`: used directly.
   - `float64`/`int`/`int64`/`bool`: `fmt.Sprintf("%v", v)`.
   - `nil`: skip the key (not emitted).
   - `map`/`[]any` (object/array): `json.Marshal` to a JSON string (supports qianwen `params` nesting).
   - other types: `json.Marshal` to a string.
4. If the script did not set `Content-Type`, Go sets `application/x-www-form-urlencoded`; if the script set it, the script value wins (allow override).
5. Final body bytes = `url.Values.Encode()` (key-sorted, URL-encoded).

When `BodyType` is missing, empty or `"json"`: keep the existing `json.Marshal(req.Body)` behavior, but apply recursive placeholder substitution to body before marshal (see §4).

### 4. Placeholder substitution extension (Task 3 contract)

New function `substitutePlaceholdersInBody(body any, values map[string]string) any`:

- `string`: returns `substitutePlaceholders(s, values)`.
- `map[string]any`: returns a new map with each value recursively substituted (input not mutated).
- `[]any`: returns a new slice with each element recursively substituted.
- other types (number/bool/nil): returned as-is.

Call site (`script.go` `ExecuteScript`, after URL/headers substitution, before `validateScriptRequest`):

```go
reqConfig.URL = substitutePlaceholders(reqConfig.URL, placeholderValues)
for k, v := range reqConfig.Headers {
    reqConfig.Headers[k] = substitutePlaceholders(v, placeholderValues)
}
reqConfig.Body = substitutePlaceholdersInBody(reqConfig.Body, placeholderValues) // NEW
```

`placeholderValues` is built by `manager.go`; the custom/general branch adds `"apiKey2": plan.token2`.

### 5. Qianwen configuration example (Task 6 deliverable, end-to-end target)

#### 5.1 Credential acquisition steps (written into the spec for user operation)

1. Log in to `platform.qianwenai.com` and open the "Token Plan" page.
2. **Cookie**: DevTools → Network → any `cs-data.qianwenai.com/data/api.json` request → Request Headers → copy the full `Cookie:` value (including `login_qianwenai_ticket=...; login_aliyunid_pk=...; cna=...; isg=...; tfstk=...` etc.).
3. **sec_token**: DevTools → Console → run `ALIYUN_CONSOLE_CONFIG.SEC_TOKEN` → copy the returned string (e.g. `WmFrs6jduM1ff0WGARDqN`).

#### 5.2 Recommended custom script

```javascript
({
  request: {
    url: "{{baseUrl}}/data/api.json?product=sfm_bailian&action=BroadScopeAspnGateway&api=zeldaHttp.apikeyMgr.%2Ftokenplan%2Fpersonal%2Fapi%2Fv2%2Fusage",
    method: "POST",
    bodyType: "form",
    headers: {
      "Cookie": "{{apiKey}}",
      "Content-Type": "application/x-www-form-urlencoded",
      "Accept": "application/json, text/plain, */*",
      "Referer": "https://platform.qianwenai.com/home/billing/subscription/token-plan-individual"
    },
    body: {
      product: "sfm_bailian",
      action: "BroadScopeAspnGateway",
      sec_token: "{{apiKey2}}",
      region: "cn-beijing",
      params: {
        Api: "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage",
        Data: {
          cornerstoneParam: {
            domain: "platform.qianwenai.com",
            consoleSite: "QIANWENAI",
            console: "ONE_CONSOLE",
            xsp_lang: "zh-CN",
            protocol: "V2",
            productCode: "p_efm"
          }
        },
        V: "1.0"
      }
    }
  },
  extractor: function (response) {
    if (response.code !== "200" || response.successResponse !== true) {
      return {
        __error_code: "upstream_business_error",
        __error_message: (response.data && response.data.errorMsg) || "qianwen usage query failed"
      };
    }
    var inner = response.data && response.data.DataV2 && response.data.DataV2.data && response.data.DataV2.data.data;
    if (!inner) {
      return {
        __error_code: "invalid_response",
        __error_message: "qianwen usage: missing data.DataV2.data.data"
      };
    }
    var tiers = [];
    if (typeof inner.per5HourPercentage === "number") {
      tiers.push({ window: "five_hour", utilization: inner.per5HourPercentage * 100 });
    }
    if (typeof inner.per1WeekPercentage === "number") {
      tiers.push({
        window: "seven_day",
        utilization: inner.per1WeekPercentage * 100,
        resetsAt: inner.per1WeekResetTime
      });
    }
    if (tiers.length === 0) {
      return {
        __error_code: "empty_result",
        __error_message: "qianwen usage: no percentage fields in response"
      };
    }
    return tiers;
  }
})
```

#### 5.3 Field mapping & expected result

- `per5HourPercentage` (5h used ratio 0–1) → `tier{window:"five_hour", utilization:<value × 100>}`.
- `per1WeekPercentage` (7d used ratio 0–1) → `tier{window:"seven_day", utilization:<value × 100>, resetsAt:<ms epoch>}`.
- Card displays e.g. "5h: 0%", "7d: 1% ◷ 6d23h" (countdown computed by the existing `QuotaResultDisplay.vue` logic).

### 6. Lifecycle & edge cases

1. **New field persistence**: `ProviderQuotaConfig` is JSON-pass-through (`EncodeQuotaConfig`/`DecodeQuotaConfig`, `types.go:419-467`); `ScriptAPIKey2` persists automatically via the JSON column — no SQLite schema change (unlike `ExposedModels`, `providerquota` config is stored as a single JSON text column).
2. **Old config compatibility**: a missing `script_api_key_2` field deserializes to empty string — equivalent to "second secret not configured".
3. **Template switching**: `NormalizeForTemplate` (`resolve.go:36-72`) **does not clear `ScriptAPIKey2`** (same independent security domain as `ScriptAPIKey`/`ZenMuxAPIKey`); after switching to newapi/token_plan the residue is never misused because `resolveQueryPlan` branches read only their own credentials.
4. **Import/export/duplicate**: `ScriptAPIKey2` is exported with `ProviderQuotaConfig` (existing secret-export warning applies); duplicate copies config but not snapshot (existing rule; the new field follows automatically).
5. **Credential-expiry degradation**: query failure → `result.success=false` + error code; `last_success_json` retains the last success (existing mechanism); card shows a warning icon + the old value.
6. **Origin check**: the qianwen request URL must share origin with Base URL `https://cs-data.qianwenai.com`; a mistyped Base URL is rejected by `validateScriptRequest` (`invalid_config`).
7. **Redirects**: qianwen returns 200 directly, no redirects; the existing redirect origin check (`script.go:272-310`) is unchanged.

## Task Details

### Task 1: config layer — `ScriptAPIKey2` field & public DTO

#### Requirements

**Objective** — Add `ScriptAPIKey2` secret field to `ProviderQuotaConfig`, add a read-only `ScriptAPIKey2Configured` flag to `PublicQuotaConfig`, guarantee JSON round-trip and "public response never leaks plaintext".

**Outcomes** — `internal/providerquota/types.go` changes; `go test ./internal/providerquota/ -run 'Config|Public|Secrets'` passes new tests.

**Evidence** — new tests: `ScriptAPIKey2` JSON marshal includes the field, unmarshal restores it, `ToPublicConfig` outputs only `ScriptAPIKey2Configured` boolean, `HasSecrets` returns true when only `ScriptAPIKey2` is set.

**Constraints** — no change to `ProviderQuotaConfig.Validate` (`ScriptAPIKey2` is optional, no required validation); JSON tag `script_api_key_2,omitempty`; field placed immediately after `ScriptAPIKey` (keep secret fields grouped).

**Edge Cases** — empty string; old config missing the field; both `ScriptAPIKey` and `ScriptAPIKey2` set; `ToPublicConfig(nil)` returns zero value.

**Verification** — `go test -v -race ./internal/providerquota/ -run 'Config|Public|Secrets'` green.

#### Plan

**File: `internal/providerquota/types.go`**

1. After `ScriptAPIKey` (around line 44) add:
   ```go
   // ScriptAPIKey2 is an optional second secret for custom/general templates
   // (e.g. a session token that must appear in the request body). It maps to
   // the {{apiKey2}} placeholder and is never returned in PublicQuotaConfig.
   ScriptAPIKey2 string `json:"script_api_key_2,omitempty"`
   ```
2. In `HasSecrets` (lines 159-165) append `|| c.ScriptAPIKey2 != ""` to the return expression.
3. In `PublicQuotaConfig` (lines 366-382), after `ScriptAPIKeyConfigured`, add `ScriptAPIKey2Configured bool \`json:"script_api_key_2_configured"\``.
4. In `ToPublicConfig` (lines 386-406), after `ScriptAPIKeyConfigured`, add `ScriptAPIKey2Configured: c.ScriptAPIKey2 != ""`.

**Test file: `internal/providerquota/types_test.go`**

5. Add `TestScriptAPIKey2RoundTrip`: build `ProviderQuotaConfig{ScriptAPIKey2: "sec-token-abc"}`, `EncodeQuotaConfig` → `DecodeQuotaConfig`, assert `ScriptAPIKey2 == "sec-token-abc"`; `json.Marshal` output contains `"script_api_key_2":"sec-token-abc"`.
6. Add `TestPublicQuotaConfigRedactsScriptAPIKey2`: `ToPublicConfig(&ProviderQuotaConfig{ScriptAPIKey2: "sec-token-abc"})` → assert `ScriptAPIKey2Configured == true`, and the marshaled JSON does not contain the string `sec-token-abc`.
7. Extend `TestHasSecrets` (if present) or add `TestHasSecretsScriptAPIKey2`: only `ScriptAPIKey2` set → `HasSecrets() == true`.

#### Verification

- [ ] `go test -v -race ./internal/providerquota/ -run 'Config|Public|Secrets|HasSecrets'` green.
- [ ] Manual: `ToPublicConfig` output JSON has no `script_api_key_2` plaintext (only `script_api_key_2_configured`).

---

### Task 2: resolve + manager — `token2` resolution & `{{apiKey2}}` placeholder

#### Requirements

**Objective** — Make the custom/general query plan carry the second secret `token2` and inject `apiKey2` when `manager.go` builds placeholders. `ScriptAPIKey2` shares `ScriptAPIKey`'s independent security domain and is not cleared by `NormalizeForTemplate`.

**Outcomes** — `internal/providerquota/resolve.go`, `internal/providerquota/manager.go` changes; targeted tests pass.

**Evidence** — `resolveQueryPlan` returns `queryPlan.token2 == cfg.ScriptAPIKey2` for custom/general, `token2==""` for other templates (branch does not read it); `NormalizeForTemplate` preserves `ScriptAPIKey2` (same as `ScriptAPIKey`, independent security domain); manager placeholders contain `apiKey2`.

**Constraints** — `token2` does not participate in `ValidateForCard` "missing_credentials" validation (second secret is optional); the custom/general first-secret fallback (`ScriptAPIKey` else card APIToken) is unchanged.

**Edge Cases** — `ScriptAPIKey2` empty → `token2=""`, placeholder substituted to empty string (same semantics as `apiKey`); switching template from custom to newapi leaves `ScriptAPIKey2` residue but `token2` is never read (newapi branch uses only AccessToken).

**Verification** — `go test -v -race ./internal/providerquota/ -run 'Resolve|Normalize|QueryPlan'` green.

#### Plan

**File: `internal/providerquota/resolve.go`**

1. Add field `token2 string` to the `queryPlan` struct (lines 21-31), commented "custom/general second secret (ScriptAPIKey2); zero for other templates".
2. In `resolveQueryPlan` `case TemplateGeneral, TemplateCustom:` (lines 184-193), append `token2: cfg.ScriptAPIKey2` to the returned `queryPlan`:
   ```go
   return &queryPlan{
       template:  cfg.TemplateType,
       scriptURL: baseURL,
       token:     token,
       token2:    cfg.ScriptAPIKey2, // NEW
   }, nil
   ```
3. `NormalizeForTemplate` (lines 36-72) **needs no cleanup logic for `ScriptAPIKey2`** — `ScriptAPIKey` and `ZenMuxAPIKey` are "independent security domains" in the current design (`resolve.go:33-35` comment), preserved for all templates, never cleared on switch; `ScriptAPIKey2` follows the same principle. Safety is guaranteed by `resolveQueryPlan`: only the custom/general branch reads `ScriptAPIKey2` (as `token2`), other template branches do not, so residue is never misused.

**File: `internal/providerquota/manager.go`**

4. In `executeQuery` `case plan.template == TemplateCustom || plan.template == TemplateGeneral:` (lines 237-253), append `"apiKey2": plan.token2` to the placeholders map:
   ```go
   placeholders := map[string]string{
       "baseUrl": plan.scriptURL,
       "apiKey":  plan.token,
       "apiKey2": plan.token2, // NEW
   }
   ```

**Test file: `internal/providerquota/resolve_test.go`**

5. Add `TestResolveQueryPlanCustomToken2`: `resolveQueryPlan(&ProviderQuotaConfig{Enabled:true, TemplateType:"custom", BaseURL:"https://h.example", ScriptAPIKey2:"sec-xyz"}, "", "")` → assert `plan.token2 == "sec-xyz"`, `plan.token == ""` (no ScriptAPIKey and no card token).
6. Add `TestResolveQueryPlanCustomToken2Empty`: `ScriptAPIKey2` omitted → `plan.token2 == ""`.
7. Extend `TestNormalizeForTemplate` (if present) or add `TestNormalizeForTemplateClearsScriptAPIKey2`:
   - newapi config with `ScriptAPIKey2:"x"` → after normalize, `ScriptAPIKey2 == ""`.
   - custom config with `ScriptAPIKey2:"x"` → preserved.

**Test file: `internal/providerquota/manager_test.go`**

8. Add or extend a manager → script integration test (injecting an `httptest.Server` via `adapterHTTPClient`) asserting that a custom script's `{{apiKey2}}` is replaced by `plan.token2` and reaches the upstream form body's `sec_token` field. This depends on Task 3 form-body support; the test may be written first and run after Task 3, or covered directly in Task 3 step 8.

#### Verification

- [ ] `go test -v -race ./internal/providerquota/ -run 'Resolve|Normalize'` green.
- [ ] `go test -v -race ./internal/providerquota/ -run 'QueryPlan'` green.
- [ ] manager → script integration test (after Task 3) proves `apiKey2` reaches upstream body.

---

### Task 3: script.go — `bodyType:"form"` encoding & body placeholder substitution

#### Requirements

**Objective** — Let `custom`/`general` scripts produce `application/x-www-form-urlencoded` body (via `request.bodyType:"form"`), extend placeholder substitution from URL/headers to all body string values (for both JSON and form bodies), and keep existing JSON body behavior strictly backward compatible.

**Outcomes** — `internal/providerquota/script.go` changes; targeted tests cover form encoding, body substitution, JSON backward compatibility, qianwen fixture.

**Evidence** — form body uses `url.Values`, object values JSON-marshaled; `{{apiKey2}}` inside body is substituted; existing JSON body without placeholders is byte-identical; qianwen fixture (fixture response + form body + dual placeholders) returns two tiers end-to-end.

**Constraints** — placeholder substitution stays in the Go layer (`ExecuteScript`); the goja runtime never sees real values (spec `2026-06-27` §4.1 unchanged); `bodyType:"form"` requires body to be an object, otherwise `script_error`; no new HTTP methods or forbidden-header changes; `substitutePlaceholdersInBody` must not mutate the input map (returns new values).

**Edge Cases** — body is nil (no body); body is object/array/string/number; form body with object value (qianwen `params`); placeholder in nested object; JSON body containing `{{apiKey}}` (previously not substituted, now substituted — confirm no impact on stock scripts); form body without script-set Content-Type (Go auto-fills).

**Verification** — `go test -v -race ./internal/providerquota/ -run 'Script|Form|Body|Placeholder'` green.

#### Plan

**File: `internal/providerquota/script.go`**

1. Add `BodyType string \`json:"bodyType,omitempty"\`` to `ScriptRequest` (lines 27-32).
2. Add `substitutePlaceholdersInBody` (after `substitutePlaceholders`, around line 390):
   ```go
   // substitutePlaceholdersInBody recursively replaces placeholders in all
   // string values within body. It returns a new value; the input is not
   // mutated. Non-string scalars (numbers, bools, nil) are returned as-is.
   func substitutePlaceholdersInBody(body any, values map[string]string) any {
       switch v := body.(type) {
       case string:
           return substitutePlaceholders(v, values)
       case map[string]any:
           out := make(map[string]any, len(v))
           for k, val := range v {
               out[k] = substitutePlaceholdersInBody(val, values)
           }
           return out
       case []any:
           out := make([]any, len(v))
           for i, val := range v {
               out[i] = substitutePlaceholdersInBody(val, values)
           }
           return out
       default:
           return body
       }
   }
   ```
3. In `ExecuteScript` (lines 69-73), after the headers loop, add body substitution:
   ```go
   reqConfig.Body = substitutePlaceholdersInBody(reqConfig.Body, placeholderValues)
   ```
4. Refactor the body construction in `doHTTPRequest` (lines 241-252) to branch on `BodyType`:
   ```go
   var bodyReader io.Reader
   if req.Body != nil {
       bodyBytes, err := encodeRequestBody(req)
       if err != nil {
           return nil, 0, err
       }
       if len(bodyBytes) > maxRequestBodySize {
           return nil, 0, fmt.Errorf("request body exceeds %d bytes", maxRequestBodySize)
       }
       bodyReader = bytes.NewReader(bodyBytes)
   }
   ```
   And add `encodeRequestBody`:
   ```go
   // encodeRequestBody serializes the script body. BodyType "form" produces
   // application/x-www-form-urlencoded; "" or "json" produces JSON (existing
   // behavior). Form body must be an object; object/array field values are
   // JSON-marshaled to support nested structures like qianwen's params field.
   func encodeRequestBody(req *ScriptRequest) ([]byte, error) {
       if strings.EqualFold(req.BodyType, "form") {
           obj, ok := req.Body.(map[string]any)
           if !ok {
               return nil, fmt.Errorf("form body must be an object, got %T", req.Body)
           }
           v := make(url.Values, len(obj))
           for key, val := range obj {
               s, err := formFieldValue(val)
               if err != nil {
                   return nil, fmt.Errorf("form field %q: %w", key, err)
               }
               if s == nil {
                   continue // nil values skipped
               }
               v.Set(key, *s)
           }
           return []byte(v.Encode()), nil
       }
       // Default: JSON (existing behavior).
       return json.Marshal(req.Body)
   }

   // formFieldValue converts a script body field value to its form-urlencoded
   // string representation. Returns nil for nil values (field skipped).
   func formFieldValue(val any) (*string, error) {
       switch v := val.(type) {
       case nil:
           return nil, nil
       case string:
           s := v
           return &s, nil
       case bool, float64, int, int64:
           s := fmt.Sprintf("%v", v)
           return &s, nil
       default:
           b, err := json.Marshal(v)
           if err != nil {
               return nil, err
           }
           s := string(b)
           return &s, nil
       }
   }
   ```
5. In `doHTTPRequest`, after creating `httpReq` (around lines 254-258), auto-fill Content-Type for form body: if `BodyType=="form"` and the script did not set `Content-Type` (case-insensitive scan of `req.Headers`), call `httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")`.

**Test file: `internal/providerquota/script_test.go`**

6. Add `TestEncodeRequestBodyForm`:
   - `ScriptRequest{BodyType:"form", Body: map[string]any{"a":"1","b":"2"}}` → `"a=1&b=2"` (url.Values.Encode sorted order).
   - Object value: `Body: map[string]any{"params": map[string]any{"x":1}}` → `"params=%7B%22x%22%3A1%7D"`.
   - Non-object body: `Body: "string"` → error `"form body must be an object"`.
   - Nil value: `Body: map[string]any{"skip": nil, "keep": "y"}` → `"keep=y"`.
7. Add `TestSubstitutePlaceholdersInBody`:
   - string: `"{{apiKey2}}"` + `{"apiKey2":"sec"}` → `"sec"`.
   - nested object: `{a:{b:"{{apiKey2}}"}}` → `{a:{b:"sec"}}`.
   - array: `["{{apiKey}}", 1]` → `["k", 1]`.
   - number/bool unchanged.
   - input map not mutated (deep-copy assertion).
8. Add `TestExecuteScriptFormBodyQianwenFixture` (end-to-end, depends on Tasks 1-2):
   - Start an `httptest.Server` that records the received method and body.
   - Use the qianwen script from §5.2 (`bodyType:"form"`, `sec_token:"{{apiKey2}}"`, `Cookie:"{{apiKey}}"`, `params` as object).
   - `placeholderValues = {"baseUrl": server.URL, "apiKey": "cookie-val", "apiKey2": "sec-tok"}`.
   - Server returns the qianwen fixture from §2.3.
   - Assert: server received POST, Content-Type contains `application/x-www-form-urlencoded`, decoded body has `sec_token=="sec-tok"`, `product=="sfm_bailian"`, `params` parses as JSON with the right `Api`; `Cookie` header contains `cookie-val`.
   - Assert result: `Success==true`, `Tiers` length 2, `five_hour` utilization 0, `seven_day` utilization 1 with `ResetsAt` matching `1785462900000` ms.
9. Add `TestExecuteScriptJSONBodyBackwardCompat`:
   - JSON body (no BodyType) without placeholders → upstream body is byte-identical to `json.Marshal` of the original object (`bytes.Equal`).
   - JSON body with `{{apiKey}}` (previously not substituted) → now substituted to the real value (intentional enhancement; test locks the new behavior).
10. Extend `TestValidateScriptRequest` (if present) to confirm form body still enforces same-origin.

#### Verification

- [ ] `go test -v -race ./internal/providerquota/ -run 'Script|Form|Body|Placeholder|EncodeRequest'` green.
- [ ] qianwen fixture test proves `sec_token`/`Cookie`/`params` reach upstream correctly; result has two tiers.
- [ ] JSON body backward-compat test passes byte-identically.

---

### Task 4: admin handler — `script_api_key_2` secret-update semantics

#### Requirements

**Objective** — Let admin `PUT /api/providers/{id}/usage` and `POST /usage/test` support the preserve/replace/clear three-state semantics for `script_api_key_2`, symmetric to `script_api_key`, and expose only `script_api_key_2_configured` in public responses.

**Outcomes** — `internal/admin/provider_quota_handler.go` changes; handler tests cover the three states.

**Evidence** — tests: PUT with `script_api_key_2:"x"` replaces, omitted preserves, `clear_script_api_key_2:true` clears; GET config response contains `script_api_key_2_configured` and no plaintext; test draft carries `script_api_key_2`.

**Constraints** — reuse the two existing symmetric patterns: the explicit `applySecretPatch` call list in `applyQuotaUpdate` (lines 397-400), and the `patches` slice in `validateProviderQuotaSecretPatches` (lines 335-352, which rejects simultaneous replace+clear); do not change other secret fields' semantics; `script_api_key_2` is only accepted under custom/general (other templates are cleared by `NormalizeForTemplate`; no handler special-casing needed).

**Edge Cases** — both `script_api_key_2:"x"` and `clear_script_api_key_2:true` (replace wins, matching `script_api_key`); residue after template switch (handled by `NormalizeForTemplate`); old clients not sending the field (preserved as empty).

**Verification** — `go test -v -race ./internal/admin/ -run 'ProviderQuota'` green.

#### Plan

**File: `internal/admin/provider_quota_handler.go`**

1. Add to the quota config update request struct (around line 319, near `ScriptAPIKey *string`):
   ```go
   ScriptAPIKey2      *string `json:"script_api_key_2"`
   ```
   and near lines 328-329 (`ClearScriptAPIKey`/`ClearZenMuxAPIKey`):
   ```go
   ClearScriptAPIKey2 bool `json:"clear_script_api_key_2"`
   ```
2. Append a row to the `patches` slice in `validateProviderQuotaSecretPatches` (lines 335-352) so the "simultaneous replace+clear" guard covers the new field:
   ```go
   {name: "script_api_key_2", value: req.ScriptAPIKey2, clear: req.ClearScriptAPIKey2},
   ```
3. Append to the explicit `applySecretPatch` call list in `applyQuotaUpdate` (from line 357, at lines 397-400):
   ```go
   applySecretPatch(&c.ScriptAPIKey2, req.ScriptAPIKey2, req.ClearScriptAPIKey2)
   ```
4. Public config response construction (search where `ScriptAPIKeyConfigured` is output): confirm `script_api_key_2_configured` is produced automatically by `ToPublicConfig` (added in Task 1); the handler needs no extra change — **this step is a verification item**. If the handler builds a DTO that overrides `PublicQuotaConfig`, add the field there.

**Test file: `internal/admin/provider_quota_handler_test.go`**

4. Add `TestPUTProviderUsageScriptAPIKey2Replace`: PUT with `script_api_key_2:"new-sec"` → GET → `script_api_key_2_configured==true`; read storage directly to confirm plaintext `"new-sec"`.
5. Add `TestPUTProviderUsageScriptAPIKey2Preserve`: existing `ScriptAPIKey2:"old"`, PUT without the field → plaintext still `"old"`.
6. Add `TestPUTProviderUsageScriptAPIKey2Clear`: existing `ScriptAPIKey2:"old"`, PUT with `clear_script_api_key_2:true` → plaintext empty, `script_api_key_2_configured==false`.
7. Add `TestPOSTProviderUsageTestScriptAPIKey2`: test draft with `script_api_key_2` → upstream (mock) receives the substituted value (echoes Task 3 step 8; handler layer verifies draft pass-through).

#### Verification

- [ ] `go test -v -race ./internal/admin/ -run 'ProviderQuota'` green.
- [ ] GET config response JSON contains no `script_api_key_2` plaintext.

---

### Task 5: frontend — custom/general "Additional secret" input + i18n + API types

#### Requirements

**Objective** — Add an "Additional secret (apiKey2)" input in `ProviderUsageModal.vue` directly under "Script API Key" for custom/general, reusing script_api_key's visual and three-state interaction (input replaces, "clear" button when configured); sync types and bilingual copy in `quotaForm.ts`/`useApi.ts`/`useI18n.ts`.

**Outcomes** — changes to `internal/frontend/src/components/ProviderUsageModal.vue`, `internal/frontend/src/utils/quotaForm.ts`, `internal/frontend/src/composables/useApi.ts`, `internal/frontend/src/composables/useI18n.ts`; frontend tests pass; `npm run build` succeeds.

**Evidence** — component test asserts the input is visible under custom and hidden under newapi; quotaForm test asserts buildSavePayload/buildTestPayload carries `script_api_key_2` when filled and `clear_script_api_key_2` when checked; bilingual keys exist with no bare keys.

**Constraints** — do not change the existing script_api_key layout or logic; the additional input appears under the same condition as script_api_key (custom/general, via a symmetric computed `showAPIKey2` or by reusing `showAPIKey`); password-type input; no horizontal overflow on mobile.

**Edge Cases** — configured + no input (show "Configured" placeholder + "clear" button); configured + new input (replace); not configured + empty (do not send the field); template switch custom → newapi (input hidden, form field reset).

**Verification** — `npm --prefix internal/frontend test` green; `npm --prefix internal/frontend run build` succeeds; 360/768/1440px no overflow (manual).

#### Plan

**File: `internal/frontend/src/utils/quotaForm.ts`**

1. In `QuotaFormState` (from line 14, near line 20 `script_api_key`) add:
   ```ts
   script_api_key_2: string
   ```
   and near line 27 (`clear_script_api_key`):
   ```ts
   clear_script_api_key_2: boolean
   ```
2. In `initialQuotaForm`/default form (the `form` reactive object in `ProviderUsageModal.vue` lines 271-284, or the factory in quotaForm.ts) add `script_api_key_2: ''` and `clear_script_api_key_2: false`.
3. Next to `showAPIKeyField` (line 83 `return ['general','custom'].includes(templateType)`) add `showAPIKey2Field` with identical semantics — or simply reuse `showAPIKeyField` (same display condition; one computed is enough).
4. In `buildSavePayload` (from line 107, the `usesScriptAPIKey`/`replacesScriptAPIKey` logic at lines 109/112/136/154) add symmetrically:
   ```ts
   const replacesScriptAPIKey2 = usesScriptAPIKey && !!form.script_api_key_2
   // ...
   if (replacesScriptAPIKey2) data.script_api_key_2 = form.script_api_key_2
   // ...
   if (form.clear_script_api_key_2 && !replacesScriptAPIKey2) data.clear_script_api_key_2 = true
   ```
   `buildTestPayload` (from line 170) likewise: `if (usesScriptAPIKey && form.script_api_key_2) data.script_api_key_2 = form.script_api_key_2`.
5. In the form reset logic (`ProviderUsageModal.vue` lines 363-367 where `form.script_api_key = ''` / `form.clear_script_api_key = false`) add `form.script_api_key_2 = ''` and `form.clear_script_api_key_2 = false`.

**File: `internal/frontend/src/components/ProviderUsageModal.vue`**

6. Directly under the "Script API Key" block (lines 91-94), duplicate a symmetric "Additional secret" block:
   ```vue
   <div v-if="showAPIKey" class="mt-3">
     <label class="block text-sm font-medium mb-1">{{ t('quota.script_api_key_2') }}</label>
     <div class="flex gap-2 items-center">
       <input v-model="form.script_api_key_2" type="password" class="min-w-0 flex-1 app-control rounded-md px-3 py-2 text-sm" :placeholder="savedConfig?.script_api_key_2_configured ? t('quota.script_api_key_configured') : ''" />
       <button v-if="savedConfig?.script_api_key_2_configured" type="button" class="text-xs text-danger hover:underline whitespace-nowrap" @click="form.clear_script_api_key_2 = true">{{ t('quota.clear_script_key') }}</button>
     </div>
     <div class="text-xs text-text-secondary mt-1">{{ t('quota.script_api_key_2_hint') }}</div>
   </div>
   ```
   (The `showAPIKey` computed already exists at line 294 with condition custom/general; reuse it.)

**File: `internal/frontend/src/composables/useApi.ts`**

7. In the quota config TypeScript type (search for `script_api_key_configured`) add `script_api_key_2_configured?: boolean`; in the save/test payload types (search for `script_api_key?: string`) add `script_api_key_2?: string` and `clear_script_api_key_2?: boolean`.

**File: `internal/frontend/src/composables/useI18n.ts`**

8. In the bilingual quota copy (search for `quota.script_api_key` and `quota.clear_script_key`) add three entries:
   - `quota.script_api_key_2`: ZH「附加密钥（apiKey2）」/ EN「Additional secret (apiKey2)」.
   - `quota.script_api_key_2_hint`: ZH「用于 custom 脚本的第二个占位符 {{apiKey2}}，例如千问 Token Plan 的 sec_token」/ EN「Second placeholder {{apiKey2}} for custom scripts, e.g. qianwen Token Plan sec_token」.
   - reuse existing `quota.script_api_key_configured` as the placeholder text; if it does not exist, add ZH「已配置」/ EN「Configured」.

**Test files: `internal/frontend/src/utils/quotaForm.test.ts` and `internal/frontend/src/components/ProviderUsageModal.test.ts`**

9. `quotaForm.test.ts` add:
   - custom + `script_api_key_2:"x"` → save payload contains `script_api_key_2:"x"`.
   - custom + `clear_script_api_key_2:true` with no input → save payload contains `clear_script_api_key_2:true`.
   - newapi → save/test payload does not contain `script_api_key_2`.
10. `ProviderUsageModal.test.ts` add: under custom, the "Additional secret" label is visible; under newapi, it is not.

#### Verification

- [ ] `npm --prefix internal/frontend test` green.
- [ ] `npm --prefix internal/frontend run build` succeeds.
- [ ] `grep -r "quota.script_api_key_2" internal/frontend/src` matches both ZH and EN.
- [ ] Manual: under custom the input appears; after save, GET shows "Configured"; after clear, it resets.

---

### Task 6: qianwen Token Plan config example + end-to-end verification + doc write-back

#### Requirements

**Objective** — End-to-end verify the "form body + dual placeholder + dual secret slot" chain with real qianwen credentials, confirm the mcc card correctly shows 5-hour and 7-day used percentages and the reset countdown, and write the verification evidence and final script back into this spec.

**Outcomes** — the "Verification" subsection below filled with measured values; no `internal/frontend/dist` change beyond the rebuild required by Task 5's frontend source changes (commit the rebuilt dist per CLAUDE.md in that case).

**Evidence** — one real query's `ProviderQuotaResult` (success:true, two tiers, values matching the qianwen page); a card screenshot or log snippet.

**Constraints** — the Cookie + sec_token used for verification are private user credentials and **must not be written into the spec/code/commit**; they are only entered in the local mcc config UI; the configuration is retained after verification (the user keeps using it).

**Edge Cases** — credentials expired (re-acquire); `per5HourPercentage` is 0 (used 0%, page shows remaining 100%) or `per1WeekPercentage` is 1.0 (used 100%, page shows remaining 0%); `per1WeekResetTime` in the past (countdown shows "pending refresh", existing logic).

**Verification** — the mcc card, under custom template + qianwen config, shows two tiers after manual refresh, values matching the qianwen page's "5-hour quota / 7-day quota".

#### Plan

1. Complete Tasks 1-5 code and tests; `make test` green.
2. `make build` (or `go run ./cmd/server`) and start mcc.
3. In the mcc admin UI, open the "Usage" modal for the qianwen provider card (or a new dedicated card with BaseURL `https://cs-data.qianwenai.com`) and configure:
   - Template: custom
   - Base URL: `https://cs-data.qianwenai.com`
   - Script API Key: the full Cookie from §5.1
   - Additional secret (apiKey2): the sec_token from §5.1
   - Script: the recommended script from §5.2
4. Click "Test query" and confirm the right panel shows `success:true` and two tiers.
5. Save the config, click the card's "refresh", and confirm the card title row shows "5h: X%", "7d: Y% ◷ <countdown>".
6. Fill the measured `per5HourPercentage`/`per1WeekPercentage` values and the query time into the "Verification" subsection below; if the values disagree with the qianwen page, return to Task 3 and check the extractor.
7. (Optional) Mention "custom script form body + additional secret" in `sdd-docs/changes/changelog.md` or the next release notes.

#### Verification

- [x] Tasks 1-5 all `[x]`; `go test ./...` + `go vet ./...` + race (providerquota/admin/config) + `npm test` (211) + `npm run build` green.
- [x] Qianwen page "5-hour quota remaining 100.0%" matches mcc card `five_hour.utilization = 0` (used 0%).
- [x] Qianwen page "7-day quota remaining 0.0%" matches mcc card `seven_day.utilization = 100` (used 100%).
- [x] Qianwen page "quota reset time 2026-07-30 18:55:00" matches mcc card `seven_day.resetsAt` (`per1WeekResetTime = 1785462900000` ms).
- [x] Measured values written back here (2026-07-27 end-to-end run):
  - Query time: 2026-07-27 (user local end-to-end)
  - `per5HourPercentage: 0.0` → mcc `five_hour.utilization: 0` (× 100; used 0% = page remaining 100%)
  - `per1WeekPercentage: 1.0` → mcc `seven_day.utilization: 100` (× 100; used 100% = page remaining 0%)
  - `per1WeekResetTime: 1785462900000` → mcc `seven_day.resetsAt`, frontend shows 2026-07-30 18:55:00 (matches page)
  - **Fix record**: the first verification revealed a `perXxxPercentage` semantic misjudgment — the first capture only saw `per5HourPercentage:0.0`, where both semantics (0–100 used percentage vs 0–1 used ratio) hold, and was misjudged as the former; the end-to-end run with `per1WeekPercentage:1.0` plus the page's "remaining 0.0%" confirmed it is a **0–1 used ratio**. The extractor was fixed to `utilization = percentage * 100`, and the fixture test assertion updated (`seven_day.utilization == 100`). **The user must replace the old script saved in mcc config with the latest §5.2 script**, otherwise 7-day will show 1% (should be 100%).

---

### Task 7: frontend static asset cache headers (fix frontend-not-updating found in deployment)

#### Requirements

**Objective** — Fix the defect where "after `docker compose up -d --build` the browser still shows the old frontend": the mcc config service sent no `Cache-Control` for any static asset (including `index.html`), so browsers cached a stale `index.html` (referencing stale JS hashes) and the new frontend embedded in the binary stayed invisible.

**Outcomes** — `internal/admin/server.go` gains `cacheHeadersHandler`; `auth_test.go` gains `TestStaticCacheHeaders`.

**Evidence** — tests assert: `GET /` and the SPA route `/providers/{id}/usage` return `Cache-Control: no-cache`; `GET /assets/<hash>.js` returns `public, max-age=31536000, immutable`. Live container: curl of the served index.html already references the new JS hash and the `useI18n` JS contains `script_api_key_2`.

**Constraints** — no routing or auth-logic changes; only wrap the static handler with a header-injection layer; no impact on APIs (`/api/*` is routed away by `authMiddleware` and never reaches the header branches).

**Edge Cases** — extension-less SPA routes (serve index.html → no-cache); `/assets/` (content-hash filename → immutable long cache, safe because the name changes with content); root images (.png/.ico/.svg → no header, default behavior).

**Verification** — `go test -v ./internal/admin/ -run TestStaticCacheHeaders` green.

#### Plan

1. In `internal/admin/server.go` `Start` (line 86) change `s.authMiddleware(fileServer)` to `s.authMiddleware(cacheHeadersHandler(fileServer))`.
2. Before `authMiddleware` add `cacheHeadersHandler(next http.Handler) http.Handler`: `/assets/` prefix → `public, max-age=31536000, immutable`; `/`, `index.html`, and extension-less paths (SPA routes) → `no-cache`; others (images, etc.) → no header.
3. `internal/admin/auth_test.go` add `TestStaticCacheHeaders`: build `fileServer` and `cacheHeadersHandler` over the real `frontend.DistFS`, route through `authMiddleware`, assert `Cache-Control` for the three path classes.
4. `gofmt` + `go test ./internal/admin/ -run TestStaticCacheHeaders`.

#### Verification

- [x] `TestStaticCacheHeaders` green (3 subtests: root / SPA / hashed asset).
- [x] `go test ./internal/admin/` + `go vet` green.
- [x] Live container: `curl -sk https://localhost:8442/` JS hashes match the worktree dist; `curl /assets/useI18n-*.js` contains `script_api_key_2`; index.html response header `Cache-Control: no-cache`.
- [x] Commit `45a859c`.

**Mechanism**: vite-built JS/CSS filenames already carry a content hash (e.g. `useI18n-BzJfoWFA.js`); every frontend change produces a new hash → files are never reused → `/assets/` is safe under an immutable long cache. The only thing a browser must revalidate every time is `index.html` (which references the newest hashes), hence `no-cache` (revalidate each load) rather than `no-store` — the browser may cache index.html but confirms with the server first (304/200), so the new frontend is visible immediately while most requests are cheap 304s.
