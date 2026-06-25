import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')

test('dashboard has a global fixed back-to-top button', () => {
  assert.match(dashboardSource, /fixed bottom-5 right-5 z-30/)
  assert.match(dashboardSource, /scrollToTop/)
  assert.match(dashboardSource, /ArrowUp/)
})

test('dashboard back-to-top button z-index is below modal overlays', () => {
  // Global button uses z-30 to stay below SessionBrowser modals (z-40 outline, z-50 cleanup)
  // and below DashboardView's own modals (z-50). Verify the button does NOT use z-40 or z-50.
  assert.match(dashboardSource, /fixed bottom-5 right-5 z-30/)
  assert.doesNotMatch(dashboardSource, /fixed bottom-5 right-5 z-40/)
  assert.doesNotMatch(dashboardSource, /fixed bottom-5 right-5 z-50/)
})

test('dashboard tracks scroll position to show/hide the back-to-top button', () => {
  assert.match(dashboardSource, /scrollY/)
  assert.match(dashboardSource, /addEventListener\(.scroll./)
  assert.match(dashboardSource, /removeEventListener\(.scroll./)
})

test('dashboard back-to-top button is placed outside the tab content containers', () => {
  // The button must come AFTER the last v-if tab block (sessions) and BEFORE the modals
  const sessionsTabEnd = dashboardSource.indexOf('</div>', dashboardSource.indexOf("activeTab === 'sessions'"))
  const buttonPos = dashboardSource.indexOf('fixed bottom-5 right-5 z-30')
  const modalPos = dashboardSource.indexOf('showUsageClearModal')
  assert.ok(sessionsTabEnd > 0, 'sessions tab block found')
  assert.ok(buttonPos > sessionsTabEnd, 'button is after sessions tab block')
  assert.ok(modalPos > buttonPos, 'button is before modals')
})
