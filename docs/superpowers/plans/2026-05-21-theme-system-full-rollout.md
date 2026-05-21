# Theme System Full Rollout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote the validated session-browser Light/Dark pilot into a full-stack, app-wide theme system with a header switch, backend-persisted admin preference, and local fallback.

**Architecture:** The backend stores `admin_theme_mode` in the existing config store and exposes authenticated preference endpoints. The frontend keeps a single global `useTheme` state, applies `data-theme="light|dark"` at the app/document root, initializes from `localStorage`, then reconciles with the backend after login. The final switch lives in `AppHeader.vue`; page components consume shared semantic tokens instead of owning local theme state.

**Tech Stack:** Go `net/http`, existing `config.ConfigStore`/SQLite settings table, Vue 3, TypeScript, Tailwind CSS v4, Node test runner, Vite, Docker Compose.

---

## File Map

Backend:

- Modify `internal/config/config.go`: add `AdminThemeMode`, defaulting, and normalization helper.
- Modify `internal/config/sqlite_store.go`: load/save `admin_theme_mode` from `settings`.
- Modify `internal/config/store.go`: preserve JSON config compatibility through the new field.
- Modify `internal/admin/server.go`: register authenticated `/api/preferences`.
- Create `internal/admin/preferences_handler.go`: focused handler for preference GET/PUT.
- Modify tests in `internal/config/config_test.go`, `internal/config/sqlite_store_test.go`, `internal/config/store_test.go`.
- Create `internal/admin/preferences_handler_test.go`.

Frontend:

- Modify `internal/frontend/src/composables/useApi.ts`: add preference API types and methods.
- Modify `internal/frontend/src/composables/useTheme.ts`: promote to app-wide state with backend sync.
- Modify `internal/frontend/src/composables/useTheme.test.ts`: cover local fallback and backend reconciliation helpers.
- Modify `internal/frontend/src/components/AppHeader.vue`: move final theme switch next to language/logout.
- Modify `internal/frontend/src/components/SessionBrowser.vue`: remove page-local switch and consume global theme.
- Modify `internal/frontend/src/views/DashboardView.vue`: initialize backend theme sync after authenticated dashboard load.
- Modify `internal/frontend/src/views/LoginView.vue`: use global theme tokens.
- Modify `internal/frontend/src/styles/main.css`: move session scoped tokens to global theme tokens and style all dashboard surfaces.
- Modify or add frontend source tests for header switch and session-browser local-switch removal.

Docs:

- Update `docs/features/2026-05-21-theme-system-redesign/status.md`.
- Update `docs/features/2026-05-21-theme-system-redesign/status_ZH.md`.
- Update `docs/features/2026-05-21-theme-system-redesign/validation.md`.
- Update `docs/features/2026-05-21-theme-system-redesign/validation_ZH.md`.

---

### Task 1: Backend Theme Mode Model and Config Persistence

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/sqlite_store.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/sqlite_store_test.go`
- Modify: `internal/config/store_test.go`

- [ ] **Step 1: Write config normalization tests**

Add tests to `internal/config/config_test.go`:

```go
func TestNormalizeThemeMode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults light", in: "", want: ThemeModeLight},
		{name: "light accepted", in: "light", want: ThemeModeLight},
		{name: "dark accepted", in: "dark", want: ThemeModeDark},
		{name: "invalid defaults light", in: "system", want: ThemeModeLight},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeThemeMode(tt.in); got != tt.want {
				t.Fatalf("NormalizeThemeMode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDefaultConfigThemeMode(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.AdminThemeMode != ThemeModeLight {
		t.Fatalf("AdminThemeMode = %q, want %q", cfg.AdminThemeMode, ThemeModeLight)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
rtk go test ./internal/config -run 'TestNormalizeThemeMode|TestDefaultConfigThemeMode' -count=1
```

Expected: fail with undefined `ThemeModeLight` or `NormalizeThemeMode`.

- [ ] **Step 3: Implement config theme fields and helper**

Update `internal/config/config.go`:

```go
const (
	ThemeModeLight = "light"
	ThemeModeDark  = "dark"
)

// NormalizeThemeMode returns a supported admin theme mode.
func NormalizeThemeMode(mode string) string {
	switch mode {
	case ThemeModeDark:
		return ThemeModeDark
	case ThemeModeLight:
		return ThemeModeLight
	default:
		return ThemeModeLight
	}
}

```

Add this field to the existing `Config` struct after `ActiveProviderID`:

```go
// AdminThemeMode 管理端主题模式: light 或 dark
AdminThemeMode string `json:"admin_theme_mode"`
```

Also set the default in `DefaultConfig()`:

```go
return &Config{
	BackendURL:      "https://open.bigmodel.cn/api/anthropic",
	ProxyPort:       443,
	AdminPort:       8442,
	DataDir:         "./data",
	AdminThemeMode: ThemeModeLight,
}
```

In `Store.Load()` in `internal/config/store.go`, normalize after defaults are filled:

```go
cfg.AdminThemeMode = NormalizeThemeMode(cfg.AdminThemeMode)
```

- [ ] **Step 4: Verify config tests pass**

Run:

```bash
rtk go test ./internal/config -run 'TestNormalizeThemeMode|TestDefaultConfigThemeMode' -count=1
```

Expected: pass.

- [ ] **Step 5: Write SQLite persistence test**

Add to `internal/config/sqlite_store_test.go`:

```go
func TestSQLiteStorePersistsAdminThemeMode(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(tmpDir, "config.db"), filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	cfg := DefaultConfig()
	cfg.AdminThemeMode = ThemeModeDark
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.AdminThemeMode != ThemeModeDark {
		t.Fatalf("AdminThemeMode = %q, want %q", loaded.AdminThemeMode, ThemeModeDark)
	}
}
```

Add to `internal/config/store_test.go`:

```go
func TestJSONStorePersistsAdminThemeMode(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(filepath.Join(tmpDir, "config.json"))

	cfg := DefaultConfig()
	cfg.AdminThemeMode = ThemeModeDark
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.AdminThemeMode != ThemeModeDark {
		t.Fatalf("AdminThemeMode = %q, want %q", loaded.AdminThemeMode, ThemeModeDark)
	}
}
```

- [ ] **Step 6: Run persistence tests to verify SQLite fails first**

Run:

```bash
rtk go test ./internal/config -run 'TestSQLiteStorePersistsAdminThemeMode|TestJSONStorePersistsAdminThemeMode' -count=1
```

Expected before SQLite implementation: JSON test may pass, SQLite test fails with `AdminThemeMode = "light"`.

- [ ] **Step 7: Implement SQLite load/save**

Update `SQLiteStore.Load()` in `internal/config/sqlite_store.go` after `cfg.ActiveProviderID = settings["active_provider_id"]`:

```go
cfg.AdminThemeMode = NormalizeThemeMode(settings["admin_theme_mode"])
```

Update `saveSettings()` map:

```go
settings := map[string]string{
	"backend_url":          cfg.BackendURL,
	"proxy_port":           strconv.Itoa(cfg.ProxyPort),
	"admin_port":           strconv.Itoa(cfg.AdminPort),
	"admin_password_hash":  cfg.AdminPasswordHash,
	"data_dir":             cfg.DataDir,
	"active_provider_id":   cfg.ActiveProviderID,
	"admin_theme_mode":     NormalizeThemeMode(cfg.AdminThemeMode),
}
```

- [ ] **Step 8: Run config package tests**

Run:

```bash
rtk go test ./internal/config -count=1
```

Expected: pass.

- [ ] **Step 9: Commit backend config persistence**

```bash
rtk git add internal/config/config.go internal/config/sqlite_store.go internal/config/config_test.go internal/config/sqlite_store_test.go internal/config/store_test.go
rtk git commit -m "feat: persist admin theme preference in config"
```

---

### Task 2: Backend Preferences API

**Files:**
- Create: `internal/admin/preferences_handler.go`
- Create: `internal/admin/preferences_handler_test.go`
- Modify: `internal/admin/server.go`

- [ ] **Step 1: Write preferences handler tests**

Create `internal/admin/preferences_handler_test.go`:

```go
package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"claude_code_proxy_dns/internal/config"
)

func TestPreferencesRequiresAuth(t *testing.T) {
	server := NewServer(&AdminConfig{Password: "secret"}, config.NewMockStore(config.DefaultConfig()), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/preferences", nil)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handlePreferences)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestGetPreferencesReturnsThemeMode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AdminThemeMode = config.ThemeModeDark
	server := NewServer(&AdminConfig{Password: "secret"}, config.NewMockStore(cfg), nil)
	req := authenticatedRequest(t, server, http.MethodGet, "/api/preferences", nil)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handlePreferences)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ThemeMode string `json:"theme_mode"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.ThemeMode != config.ThemeModeDark {
		t.Fatalf("theme_mode = %q, want %q", got.ThemeMode, config.ThemeModeDark)
	}
}

func TestPutPreferencesPersistsThemeMode(t *testing.T) {
	store := config.NewMockStore(config.DefaultConfig())
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)
	body := bytes.NewBufferString(`{"theme_mode":"dark"}`)
	req := authenticatedRequest(t, server, http.MethodPut, "/api/preferences", body)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handlePreferences)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.AdminThemeMode != config.ThemeModeDark {
		t.Fatalf("AdminThemeMode = %q, want %q", loaded.AdminThemeMode, config.ThemeModeDark)
	}
}

func TestPutPreferencesRejectsInvalidThemeMode(t *testing.T) {
	server := NewServer(&AdminConfig{Password: "secret"}, config.NewMockStore(config.DefaultConfig()), nil)
	body := bytes.NewBufferString(`{"theme_mode":"system"}`)
	req := authenticatedRequest(t, server, http.MethodPut, "/api/preferences", body)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handlePreferences)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func authenticatedRequest(t *testing.T, server *Server, method string, target string, body *bytes.Buffer) *http.Request {
	t.Helper()
	if body == nil {
		body = bytes.NewBuffer(nil)
	}
	token := server.auth.GenerateToken()
	req := httptest.NewRequest(method, target, body)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	return req
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
rtk go test ./internal/admin -run Preferences -count=1
```

Expected: fail with undefined `handlePreferences`.

- [ ] **Step 3: Implement preferences handler**

Create `internal/admin/preferences_handler.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"

	"claude_code_proxy_dns/internal/config"
)

type preferencesResponse struct {
	ThemeMode string `json:"theme_mode"`
	Success   bool   `json:"success,omitempty"`
}

func (s *Server) handlePreferences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getPreferences(w, r)
	case http.MethodPut:
		s.updatePreferences(w, r)
	default:
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) getPreferences(w http.ResponseWriter, _ *http.Request) {
	if s.configStore == nil {
		http.Error(w, `{"error": "config store not available"}`, http.StatusInternalServerError)
		return
	}
	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load preferences"}`, http.StatusInternalServerError)
		return
	}
	writePreferencesJSON(w, preferencesResponse{ThemeMode: config.NormalizeThemeMode(cfg.AdminThemeMode)})
}

func (s *Server) updatePreferences(w http.ResponseWriter, r *http.Request) {
	if s.configStore == nil {
		http.Error(w, `{"error": "config store not available"}`, http.StatusInternalServerError)
		return
	}
	var req struct {
		ThemeMode string `json:"theme_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.ThemeMode != config.ThemeModeLight && req.ThemeMode != config.ThemeModeDark {
		http.Error(w, `{"error": "invalid theme_mode"}`, http.StatusBadRequest)
		return
	}
	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load preferences"}`, http.StatusInternalServerError)
		return
	}
	cfg.AdminThemeMode = req.ThemeMode
	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save preferences"}`, http.StatusInternalServerError)
		return
	}
	writePreferencesJSON(w, preferencesResponse{Success: true, ThemeMode: req.ThemeMode})
}

func writePreferencesJSON(w http.ResponseWriter, payload preferencesResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
```

Register the route in `internal/admin/server.go` near the other authenticated API routes:

```go
mux.HandleFunc("/api/preferences", s.authMiddlewareFunc(s.handlePreferences))
```

- [ ] **Step 4: Run admin tests**

Run:

```bash
rtk go test ./internal/admin -run Preferences -count=1
```

Expected: pass.

- [ ] **Step 5: Run backend tests**

Run:

```bash
rtk go test ./internal/config ./internal/admin -count=1
```

Expected: pass.

- [ ] **Step 6: Commit preferences API**

```bash
rtk git add internal/admin/preferences_handler.go internal/admin/preferences_handler_test.go internal/admin/server.go
rtk git commit -m "feat: add admin theme preferences API"
```

---

### Task 3: Frontend API and Global Theme State

**Files:**
- Modify: `internal/frontend/src/composables/useApi.ts`
- Modify: `internal/frontend/src/composables/useTheme.ts`
- Modify: `internal/frontend/src/composables/useTheme.test.ts`

- [ ] **Step 1: Write theme composable tests**

Extend `internal/frontend/src/composables/useTheme.test.ts`:

```ts
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  normalizeThemeMode,
  persistThemeMode,
  readStoredTheme,
  resolveBackendTheme,
  themeStorageKey,
} from './useTheme.ts'

test('resolveBackendTheme returns backend value and persists it', () => {
  const storage = new MemoryStorage()
  const got = resolveBackendTheme('dark', storage)
  assert.equal(got, 'dark')
  assert.equal(storage.getItem(themeStorageKey), 'dark')
})

test('resolveBackendTheme falls back to stored theme for invalid backend value', () => {
  const storage = new MemoryStorage()
  storage.setItem(themeStorageKey, 'dark')
  const got = resolveBackendTheme('system', storage)
  assert.equal(got, 'dark')
})

test('resolveBackendTheme defaults to light when backend and storage are invalid', () => {
  const storage = new MemoryStorage()
  storage.setItem(themeStorageKey, 'system')
  const got = resolveBackendTheme('system', storage)
  assert.equal(got, 'light')
})
```

Keep the existing `MemoryStorage` test helper and existing tests.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
rtk npm --prefix internal/frontend test -- --test-name-pattern theme
```

Expected: fail with missing `resolveBackendTheme`.

- [ ] **Step 3: Add frontend API methods**

Update `internal/frontend/src/composables/useApi.ts`:

```ts
export type ThemeMode = 'light' | 'dark'

export interface PreferencesResponse {
  theme_mode: ThemeMode
  success?: boolean
}
```

Inside `useApi()` add:

```ts
async function getPreferences(): Promise<PreferencesResponse> {
  const res = await fetch('/api/preferences')
  if (!res.ok) throw new Error('Failed to fetch preferences')
  return res.json()
}

async function updatePreferences(themeMode: ThemeMode): Promise<PreferencesResponse> {
  const res = await fetch('/api/preferences', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ theme_mode: themeMode }),
  })
  if (!res.ok) throw new Error('Failed to update preferences')
  return res.json()
}
```

Return both methods from `useApi()`:

```ts
return {
  login,
  logout,
  getStatus,
  getPreferences,
  updatePreferences,
  getProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  activateProvider,
  toggleProvider,
  duplicateProvider,
  revealProviderToken,
  testProvider,
  testProviderConnection,
  getCertificates,
  getUsageSummary,
  getUsageTrends,
  getUsageRequests,
  getUsageProviders,
  getUsageModels,
  getUsageCoverage,
  getSessionProjects,
  getSessionList,
  getSessionDetail,
  exportSessionHTML,
  getSessionCleanupHint,
}
```

- [ ] **Step 4: Promote `useTheme` to global state**

Update `internal/frontend/src/composables/useTheme.ts` so it exports:

```ts
export type ThemeMode = 'light' | 'dark'

export const themeStorageKey = 'claude-proxy-theme'

export interface ThemeStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

export function normalizeThemeMode(value: unknown): ThemeMode {
  return value === 'dark' ? 'dark' : 'light'
}

function browserStorage(): ThemeStorage | undefined {
  return typeof localStorage === 'undefined' ? undefined : localStorage
}

export function readStoredTheme(storage: ThemeStorage | undefined = browserStorage()): ThemeMode {
  if (!storage) return 'light'
  try {
    return normalizeThemeMode(storage.getItem(themeStorageKey))
  } catch {
    return 'light'
  }
}

export function persistThemeMode(mode: ThemeMode, storage: ThemeStorage | undefined = browserStorage()): void {
  if (!storage) return
  try {
    storage.setItem(themeStorageKey, mode)
  } catch {
    // Ignore storage failures; theme state still updates in memory.
  }
}

export function resolveBackendTheme(value: unknown, storage: ThemeStorage | undefined = browserStorage()): ThemeMode {
  if (value === 'light' || value === 'dark') {
    persistThemeMode(value, storage)
    return value
  }
  return readStoredTheme(storage)
}

const themeMode = ref<ThemeMode>(readStoredTheme())
const syncError = ref<string | null>(null)

function applyTheme(mode: ThemeMode): void {
  if (typeof document !== 'undefined') {
    document.documentElement.dataset.theme = mode
  }
}

applyTheme(themeMode.value)

export function useTheme() {
  const isDark = computed(() => themeMode.value === 'dark')

  function setTheme(mode: ThemeMode) {
    themeMode.value = mode
    persistThemeMode(mode)
    applyTheme(mode)
  }

  function toggleTheme() {
    setTheme(themeMode.value === 'dark' ? 'light' : 'dark')
  }

  async function syncTheme(loadPreference: () => Promise<{ theme_mode: ThemeMode }>) {
    try {
      const prefs = await loadPreference()
      syncError.value = null
      setTheme(resolveBackendTheme(prefs.theme_mode))
    } catch (err) {
      syncError.value = err instanceof Error ? err.message : 'Failed to sync theme'
      applyTheme(themeMode.value)
    }
  }

  async function persistTheme(savePreference: (mode: ThemeMode) => Promise<unknown>, mode: ThemeMode) {
    setTheme(mode)
    try {
      await savePreference(mode)
      syncError.value = null
    } catch (err) {
      syncError.value = err instanceof Error ? err.message : 'Failed to save theme'
    }
  }

  return {
    themeMode,
    isDark,
    syncError,
    setTheme,
    toggleTheme,
    syncTheme,
    persistTheme,
  }
}
```

Ensure imports include:

```ts
import { computed, ref } from 'vue'
```

- [ ] **Step 5: Run theme tests**

Run:

```bash
rtk npm --prefix internal/frontend test -- --test-name-pattern theme
```

Expected: pass.

- [ ] **Step 6: Commit frontend API/theme state**

```bash
rtk git add internal/frontend/src/composables/useApi.ts internal/frontend/src/composables/useTheme.ts internal/frontend/src/composables/useTheme.test.ts
rtk git commit -m "feat: add global theme state with backend sync"
```

---

### Task 4: Header Theme Switch and Session Switch Removal

**Files:**
- Modify: `internal/frontend/src/components/AppHeader.vue`
- Modify: `internal/frontend/src/components/SessionBrowser.vue`
- Modify: `internal/frontend/src/components/SessionBrowserLayout.test.ts`
- Modify: `internal/frontend/src/composables/useI18n.ts`

- [ ] **Step 1: Update source-level layout tests**

Update `internal/frontend/src/components/SessionBrowserLayout.test.ts`:

```ts
test('session browser no longer owns the global theme switch', () => {
  assert.doesNotMatch(source, /session-theme-toggle/)
  assert.doesNotMatch(source, /setTheme\('light'\)/)
  assert.doesNotMatch(source, /setTheme\('dark'\)/)
})
```

Create or extend a source test for `AppHeader.vue`, for example `internal/frontend/src/components/AppHeader.test.ts`:

```ts
import { strict as assert } from 'node:assert'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'AppHeader.vue'), 'utf8')

test('app header exposes the global light dark theme switch', () => {
  assert.match(source, /useTheme/)
  assert.match(source, /persistTheme/)
  assert.match(source, /themeMode/)
  assert.match(source, /header\.theme_light/)
  assert.match(source, /header\.theme_dark/)
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
rtk npm --prefix internal/frontend test -- --test-name-pattern "theme switch|session browser"
```

Expected: fail until header switch is added and session-local switch is removed.

- [ ] **Step 3: Add i18n keys**

Update `internal/frontend/src/composables/useI18n.ts` in both locales:

```ts
'header.theme': '主题',
'header.theme_light': '浅色',
'header.theme_dark': '深色',
```

```ts
'header.theme': 'Theme',
'header.theme_light': 'Light',
'header.theme_dark': 'Dark',
```

Keep the existing `sessions.theme_*` keys until Task 5 cleanup confirms no remaining usage.

- [ ] **Step 4: Add AppHeader switch**

Update `internal/frontend/src/components/AppHeader.vue` script:

```ts
import { ref } from 'vue'
import { Sun, Moon } from 'lucide-vue-next'
import { useApi } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import { useTheme } from '@/composables/useTheme'

const api = useApi()
const { locale, t, setLocale } = useI18n()
const { themeMode, persistTheme } = useTheme()
```

Add a theme switch in the right-side controls before the language switcher:

```vue
<div class="app-theme-toggle" :aria-label="t('header.theme')">
  <button
    :class="['app-theme-toggle-button', themeMode === 'light' ? 'app-theme-toggle-active' : '']"
    :aria-pressed="themeMode === 'light'"
    @click="persistTheme(api.updatePreferences, 'light')"
  >
    <Sun class="h-4 w-4" />
    {{ t('header.theme_light') }}
  </button>
  <button
    :class="['app-theme-toggle-button', themeMode === 'dark' ? 'app-theme-toggle-active' : '']"
    :aria-pressed="themeMode === 'dark'"
    @click="persistTheme(api.updatePreferences, 'dark')"
  >
    <Moon class="h-4 w-4" />
    {{ t('header.theme_dark') }}
  </button>
</div>
```

- [ ] **Step 5: Remove SessionBrowser local switch**

In `internal/frontend/src/components/SessionBrowser.vue`:

1. Remove the `Sun` and `Moon` imports.
2. Remove `setTheme` destructuring.
3. Keep `themeMode` only if the root still needs `:data-theme`; otherwise remove the root `:data-theme` and rely on global `html[data-theme]`.
4. Remove the `.session-theme-hero` toggle markup.

The session browser header should keep title context only:

```vue
<div class="session-theme-hero">
  <div>
    <div class="text-sm font-semibold uppercase tracking-[0.18em] session-muted">{{ t('sessions.title') }}</div>
    <h2 class="mt-1 text-2xl font-bold session-heading">{{ detail?.session.title || t('sessions.select') }}</h2>
  </div>
</div>
```

- [ ] **Step 6: Run frontend tests**

Run:

```bash
rtk npm --prefix internal/frontend test -- --test-name-pattern "theme switch|session browser"
```

Expected: pass.

- [ ] **Step 7: Commit header switch**

```bash
rtk git add internal/frontend/src/components/AppHeader.vue internal/frontend/src/components/AppHeader.test.ts internal/frontend/src/components/SessionBrowser.vue internal/frontend/src/components/SessionBrowserLayout.test.ts internal/frontend/src/composables/useI18n.ts
rtk git commit -m "feat: move theme switch to app header"
```

---

### Task 5: Dashboard Theme Synchronization and Global Tokens

**Files:**
- Modify: `internal/frontend/src/views/DashboardView.vue`
- Modify: `internal/frontend/src/views/LoginView.vue`
- Modify: `internal/frontend/src/styles/main.css`
- Modify: `internal/frontend/src/components/ProviderCard.vue`
- Modify: `internal/frontend/src/components/ProviderModal.vue`
- Modify: `internal/frontend/src/components/SessionDetail.vue`
- Modify: `internal/frontend/src/components/SessionOutline.vue`

- [ ] **Step 1: Add dashboard sync on authenticated load**

Update `internal/frontend/src/views/DashboardView.vue` script imports and setup:

```ts
import { useTheme } from '@/composables/useTheme'
```

After `const api = useApi()`:

```ts
const { syncTheme } = useTheme()
```

In the existing mount/init flow, call:

```ts
onMounted(async () => {
  await syncTheme(api.getPreferences)
  await loadInitialData()
})
```

If the file already has `onMounted(loadInitialData)`, replace it with the wrapped async version while preserving all existing data loads.

- [ ] **Step 2: Move theme variables to global scope**

In `internal/frontend/src/styles/main.css`, introduce global tokens before session-specific classes:

```css
:root,
:root[data-theme="light"] {
  --app-bg: #f7fbff;
  --app-bg-soft: #eef6ff;
  --app-surface: rgba(255, 255, 255, 0.92);
  --app-surface-raised: #ffffff;
  --app-surface-muted: #f1f7ff;
  --app-border: #dbeafe;
  --app-border-strong: #93c5fd;
  --app-text: #102033;
  --app-text-muted: #64748b;
  --app-accent: #2563eb;
  --app-accent-strong: #1d4ed8;
  --app-accent-soft: #dbeafe;
  --app-danger: #dc2626;
  --app-danger-soft: #fee2e2;
  --app-success: #15803d;
  --app-success-soft: #dcfce7;
  --app-code-bg: #0f172a;
  --app-code-text: #dbeafe;
  --app-shadow: 0 18px 50px rgba(37, 99, 235, 0.12);
}

:root[data-theme="dark"] {
  --app-bg: #070b14;
  --app-bg-soft: #0b1120;
  --app-surface: rgba(15, 23, 42, 0.94);
  --app-surface-raised: #111827;
  --app-surface-muted: #0f172a;
  --app-border: #263449;
  --app-border-strong: #38bdf8;
  --app-text: #e5edf7;
  --app-text-muted: #94a3b8;
  --app-accent: #38bdf8;
  --app-accent-strong: #7dd3fc;
  --app-accent-soft: rgba(56, 189, 248, 0.14);
  --app-danger: #fb7185;
  --app-danger-soft: rgba(251, 113, 133, 0.14);
  --app-success: #86efac;
  --app-success-soft: rgba(22, 101, 52, 0.42);
  --app-code-bg: #020617;
  --app-code-text: #e2e8f0;
  --app-shadow: 0 22px 70px rgba(0, 0, 0, 0.42);
}
```

Map existing session tokens to global tokens:

```css
.session-theme {
  --session-bg: var(--app-bg);
  --session-bg-soft: var(--app-bg-soft);
  --session-surface: var(--app-surface);
  --session-surface-raised: var(--app-surface-raised);
  --session-surface-muted: var(--app-surface-muted);
  --session-border: var(--app-border);
  --session-border-strong: var(--app-border-strong);
  --session-text: var(--app-text);
  --session-text-muted: var(--app-text-muted);
  --session-accent: var(--app-accent);
  --session-accent-strong: var(--app-accent-strong);
  --session-accent-soft: var(--app-accent-soft);
  --session-shadow: var(--app-shadow);
}
```

Remove the separate `.session-theme[data-theme="dark"]` block after confirming all values come from global `:root[data-theme="dark"]`.

- [ ] **Step 3: Add app shell and control classes**

Add CSS classes used by header and dashboard:

```css
body {
  background: var(--app-bg);
  color: var(--app-text);
}

.app-header {
  background: var(--app-surface);
  border-color: var(--app-border);
  color: var(--app-text);
  box-shadow: var(--app-shadow);
}

.app-logo-mark {
  background: var(--app-accent);
}

.app-shell {
  background:
    radial-gradient(circle at 18% 0%, var(--app-accent-soft), transparent 32%),
    var(--app-bg);
  color: var(--app-text);
}

.app-panel {
  background: var(--app-surface);
  border: 1px solid var(--app-border);
  color: var(--app-text);
}

.app-muted {
  color: var(--app-text-muted);
}

.app-control {
  background: var(--app-surface-raised);
  border: 1px solid var(--app-border);
  color: var(--app-text);
}

.app-theme-toggle {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  border: 1px solid var(--app-border);
  border-radius: 0.75rem;
  background: var(--app-surface-muted);
  padding: 0.2rem;
}

.app-theme-toggle-button {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  border: 0;
  border-radius: 0.55rem;
  padding: 0.4rem 0.65rem;
  background: transparent;
  color: var(--app-text-muted);
  cursor: pointer;
  font-size: 0.8125rem;
  font-weight: 700;
}

.app-theme-toggle-active {
  background: var(--app-surface-raised);
  color: var(--app-accent-strong);
  box-shadow: 0 8px 20px rgba(15, 23, 42, 0.12);
}
```

- [ ] **Step 4: Replace high-level dashboard hard-coded theme classes**

Update `DashboardView.vue`, `LoginView.vue`, `AppHeader.vue`, `ProviderCard.vue`, and `ProviderModal.vue` by replacing only high-impact hard-coded color classes with app token classes:

```vue
<header class="app-header px-8 h-16 flex items-center justify-between">
```

```vue
<main class="app-shell min-h-screen">
```

```vue
<section class="app-panel rounded-xl p-5">
```

```vue
<p class="app-muted text-sm">
```

```vue
<input class="app-control w-full rounded-lg px-3 py-2 text-sm">
```

Keep layout, data flow, and component structure unchanged.

- [ ] **Step 5: Run frontend tests and build**

Run:

```bash
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
```

Expected: tests pass and Vite build succeeds.

- [ ] **Step 6: Commit global token rollout**

```bash
rtk git add internal/frontend/src/views/DashboardView.vue internal/frontend/src/views/LoginView.vue internal/frontend/src/styles/main.css internal/frontend/src/components/AppHeader.vue internal/frontend/src/components/ProviderCard.vue internal/frontend/src/components/ProviderModal.vue internal/frontend/src/components/SessionBrowser.vue internal/frontend/src/components/SessionDetail.vue internal/frontend/src/components/SessionOutline.vue
rtk git commit -m "feat: apply global light dark theme tokens"
```

---

### Task 6: End-to-End Verification and Specs Status

**Files:**
- Modify: `docs/features/2026-05-21-theme-system-redesign/status.md`
- Modify: `docs/features/2026-05-21-theme-system-redesign/status_ZH.md`
- Modify: `docs/features/2026-05-21-theme-system-redesign/validation.md`
- Modify: `docs/features/2026-05-21-theme-system-redesign/validation_ZH.md`
- Generated by build: `internal/frontend/dist/**`

- [ ] **Step 1: Run full automated verification**

Run:

```bash
rtk go test ./...
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
```

Expected:

```text
go test ./... passes
frontend tests pass
Vite production build succeeds
```

- [ ] **Step 2: Rebuild and restart container**

Run:

```bash
rtk docker compose up -d --build
```

Expected: `claude_code_proxy_dns` container is rebuilt and started.

- [ ] **Step 3: Browser validation**

Open `https://localhost:8442` and verify:

1. Login succeeds.
2. Header theme switch is visible beside language/logout.
3. Switching to Dark updates the full dashboard.
4. Navigating status/providers/certificates/usage/sessions keeps Dark active.
5. Refresh keeps Dark active.
6. Clearing `localStorage` and reloading after login restores backend preference.
7. Session browser has no local theme switch.
8. Cleanup command modal remains readable.
9. User message blocks remain prominent.
10. Narrow viewport remains usable.

- [ ] **Step 4: Update validation docs**

In `validation.md` and `validation_ZH.md`, mark passed Phase 2 checks and record the exact commands/results. Use this wording shape:

````markdown
2026-05-21 Phase 2 results:

```text
rtk go test ./...
Result: passed

rtk npm --prefix internal/frontend test
Result: passed

rtk npm --prefix internal/frontend run build
Result: Vite production build succeeded

rtk docker compose up -d --build
Result: container rebuilt and started
```
````

- [ ] **Step 5: Update status docs**

Set `status.md` and `status_ZH.md` to:

```text
Phase 2 implemented; validating
```

After browser checks pass, update to:

```text
Phase 2 validated
```

- [ ] **Step 6: Commit docs and dist**

```bash
rtk git add docs/features/2026-05-21-theme-system-redesign internal/frontend/dist
rtk git commit -m "docs: record theme system rollout validation"
```

- [ ] **Step 7: Final working tree check**

Run:

```bash
rtk git status --short
```

Expected: no unexpected untracked or modified files outside the planned feature scope.

---

## Self-Review

Spec coverage:

- Header placement is covered by Task 4.
- Backend persistence and `/api/preferences` are covered by Tasks 1 and 2.
- `localStorage` fallback and backend reconciliation are covered by Task 3.
- Global `data-theme` and token rollout are covered by Task 5.
- Removal of the session-browser-local switch is covered by Task 4.
- Validation and docs updates are covered by Task 6.

No unresolved placeholders are intentionally left in this plan. The only unchecked boxes are execution tracking items.
