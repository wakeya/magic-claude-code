import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')
const lazySessionSource = readFileSync(join(here, '..', 'composables', 'useLazySessionData.ts'), 'utf8')
const cssSource = readFileSync(join(here, '..', 'styles', 'main.css'), 'utf8')

test('dashboard reserves a stable vertical scrollbar gutter', () => {
  assert.match(cssSource, /html\s*\{[\s\S]*scrollbar-gutter:\s*stable;[\s\S]*\}/)
  assert.match(cssSource, /html\s*\{[\s\S]*overflow-y:\s*auto;[\s\S]*\}/)
  assert.doesNotMatch(cssSource, /overflow-y:\s*scroll/)
})

test('dashboard keeps session state without preloading it on unrelated tabs', () => {
  assert.match(lazySessionSource, /const projects = ref<SessionProject\[\]>\(\[\]\)/)
  assert.match(lazySessionSource, /const sessions = ref<SessionItem\[\]>\(\[\]\)/)
  assert.match(dashboardSource, /useLazySessionData/)
  const mounted = dashboardSource.match(/onMounted\(async \(\) => \{[\s\S]*?\n}\)/)?.[0] || ''
  const initialLoads = mounted.match(/await Promise\.all\(\[[\s\S]*?\]\)/)?.[0] || ''
  assert.doesNotMatch(initialLoads, /loadSessionsList\(/)
  assert.doesNotMatch(initialLoads, /getSessionProjects|getSessionList/)
})

test('dashboard loads sessions on tab entry and direct sessions navigation', () => {
  assert.match(
    dashboardSource,
    /watch\([\s\S]*?\(\) => activeTab\.value[\s\S]*?tab === 'sessions'[\s\S]*?loadSessionsList\(\)/,
  )
  const mounted = dashboardSource.match(/onMounted\(async \(\) => \{[\s\S]*?\n}\)/)?.[0] || ''
  assert.match(
    mounted,
    /activeTab\.value = urlTab as MainTab[\s\S]*?if \(activeTab\.value === 'sessions'\) void loadSessionsList\(\)/,
  )
})

test('dashboard passes lazy-loaded sessions data into SessionBrowser', () => {
  assert.match(dashboardSource, /<SessionBrowser[\s\S]*:projects="sessionProjects"[\s\S]*:sessions="sessionList"[\s\S]*:loading="sessionsLoading"[\s\S]*@refreshed="handleSessionsRefreshed"[\s\S]*\/>/)
})

test('dashboard invalidates session work at authentication and component boundaries', () => {
  const logout = dashboardSource.match(/async function handleLogout\(\)[\s\S]*?\n}/)?.[0] || ''
  const unmount = dashboardSource.match(/onBeforeUnmount\(\(\) => \{[\s\S]*?\n}\)/)?.[0] || ''
  assert.match(logout, /invalidateSessionsLoad\(\)[\s\S]*?await api\.logout\(\)/)
  assert.match(unmount, /invalidateSessionsLoad\(\)/)
})
