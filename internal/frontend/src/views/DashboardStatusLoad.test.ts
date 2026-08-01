import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')
const headerSource = readFileSync(join(here, '..', 'components', 'AppHeader.vue'), 'utf8')
const mainSource = readFileSync(join(here, '..', 'main.ts'), 'utf8')

test('dashboard first entry reuses the router status result for all initial consumers', () => {
  assert.match(mainSource, /router\.beforeEach\(\(to\) => guardDashboardRoute\(to\)\)/)
  assert.match(dashboardSource, /consumeDashboardInitialStatus/)
  assert.match(
    dashboardSource,
    /const initialStatusRequest = stagedStatus\s*\?\s*Promise\.resolve\(stagedStatus\)\s*:\s*api\.getStatus\(browserTimeZone\(\)\)/,
  )
  assert.match(dashboardSource, /const initialStatusLoad = loadStatus\(initialStatusRequest\)/)
  assert.match(
    dashboardSource,
    /loadConnectionMode\(initialStatusLoad,\s*initialConfigRequest\)/,
  )
  assert.doesNotMatch(headerSource, /api\.getStatus\(/)
  assert.doesNotMatch(headerSource, /api\.getConfig\(/)
})

test('dashboard passes the shared version and mode state to AppHeader', () => {
  assert.match(
    dashboardSource,
    /<AppHeader[\s\S]*:version="status\?\.version"[\s\S]*:configured-mode="configuredMode"[\s\S]*:effective-mode="effectiveMode"/,
  )
  assert.match(headerSource, /defineProps/)
  assert.match(headerSource, /version\?: string/)
  assert.match(headerSource, /configuredMode\?: ConnectionMode/)
  assert.match(headerSource, /effectiveMode\?: ConnectionMode/)
})

test('status refresh keeps existing triggers and ignores stale responses', () => {
  assert.match(dashboardSource, /let statusLoadVersion = 0/)
  assert.match(dashboardSource, /const loadVersion = \+\+statusLoadVersion/)
  assert.match(dashboardSource, /if \(loadVersion !== statusLoadVersion\) return null/)
  assert.match(
    dashboardSource,
    /statusRefreshTimer = window\.setInterval\(\(\) => \{\s*void loadStatus\(\)/,
  )
  assert.match(dashboardSource, /addEventListener\('mcc:mode-updated', handleModeUpdated\)/)
  assert.match(dashboardSource, /removeEventListener\('mcc:mode-updated', handleModeUpdated\)/)
  assert.match(dashboardSource, /function handleModeUpdated\(\) \{\s*void loadConnectionMode\(\)\s*}/)
})

test('dashboard clears staged status before logout crosses an authentication boundary', () => {
  const logoutSource = dashboardSource.match(/async function handleLogout\(\)[\s\S]*?\n}/)?.[0] || ''
  assert.match(logoutSource, /clearDashboardInitialStatus\(\)\s+await api\.logout\(\)/)
})
