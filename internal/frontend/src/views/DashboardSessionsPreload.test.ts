import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')
const cssSource = readFileSync(join(here, '..', 'styles', 'main.css'), 'utf8')

test('dashboard reserves a stable vertical scrollbar gutter', () => {
  assert.match(cssSource, /html\s*\{[\s\S]*scrollbar-gutter:\s*stable;[\s\S]*\}/)
  assert.match(cssSource, /html\s*\{[\s\S]*overflow-y:\s*auto;[\s\S]*\}/)
  assert.doesNotMatch(cssSource, /overflow-y:\s*scroll/)
})

test('dashboard preloads sessions list with other mount-time data', () => {
  assert.match(dashboardSource, /type SessionItem/)
  assert.match(dashboardSource, /type SessionProject/)
  assert.match(dashboardSource, /const sessionProjects = ref<SessionProject\[\]>\(\[\]\)/)
  assert.match(dashboardSource, /const sessionList = ref<SessionItem\[\]>\(\[\]\)/)
  assert.match(dashboardSource, /const sessionsLoading = ref\(false\)/)
  assert.match(dashboardSource, /async function loadSessionsList\(\)[\s\S]*api\.getSessionProjects\(\)[\s\S]*api\.getSessionList\(\{\s*project: '',\s*page: 1,\s*page_size: 100\s*\}\)/)
  assert.match(dashboardSource, /Promise\.all\(\[[\s\S]*loadStatus\(\)[\s\S]*loadProviders\(\)[\s\S]*loadCerts\(\)[\s\S]*loadConnectionMode\(\)[\s\S]*loadSessionsList\(\)[\s\S]*\]\)/)
})

test('dashboard passes preloaded sessions data into SessionBrowser', () => {
  assert.match(dashboardSource, /<SessionBrowser[\s\S]*:projects="sessionProjects"[\s\S]*:sessions="sessionList"[\s\S]*:loading="sessionsLoading"[\s\S]*@refreshed="handleSessionsRefreshed"[\s\S]*\/>/)
})
