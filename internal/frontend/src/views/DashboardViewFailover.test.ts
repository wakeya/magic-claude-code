import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')

test("MainTab includes 'failover' and tab.failover immediately follows tab.sessions", () => {
  // MainTab 联合类型包含 failover。
  assert.match(dashboardSource, /type MainTab = '[\s\S']*'failover'[\s\S']*'/)
  // tabs 数组中 failover 紧邻 sessions。
  assert.match(
    dashboardSource,
    /\{ key: 'sessions', labelKey: 'tab\.sessions' \},\s*\n\s*\/\/ 切换事件紧邻会话记录[\s\S]*\n\s*\{ key: 'failover', labelKey: 'tab\.failover' \},/,
  )
})

test('FailoverEventsView is imported and rendered only for activeTab === failover', () => {
  assert.match(dashboardSource, /const FailoverEventsView = defineAsyncComponent\(\(\) => import\('@\/views\/FailoverEventsView\.vue'\)\)/)
  assert.match(dashboardSource, /<div v-if="activeTab === 'failover'">\s*<FailoverEventsView\s*\/>\s*<\/div>/)
})

test('sessions branch stays unchanged (SessionBrowser props/children intact)', () => {
  // SessionBrowser 仍只在 sessions 分支渲染，props 与事件保持原样。
  assert.match(
    dashboardSource,
    /<div v-if="activeTab === 'sessions'">\s*<SessionBrowser[\s\S]*:projects="sessionProjects"[\s\S]*:sessions="sessionList"[\s\S]*:loading="sessionsLoading"[\s\S]*:error-message="sessionsError"[\s\S]*@refreshed="handleSessionsRefreshed"[\s\S]*\/>/,
  )
  // FailoverEventsView 只在 failover 分支出现，绝不嵌在 sessions 分支内。
  const sessionsBlock = dashboardSource.slice(
    dashboardSource.indexOf("v-if=\"activeTab === 'sessions'\""),
    dashboardSource.indexOf("v-if=\"activeTab === 'failover'\""),
  )
  assert.doesNotMatch(sessionsBlock, /FailoverEventsView/)
})

test('failover switch sits adjacent to the providers title with accessible label + save state + rollback', () => {
  // 开关紧邻 providers.title，有 aria-label、disabled 保存态、PUT 失败回滚文案。
  const switchBlock = dashboardSource.slice(
    dashboardSource.indexOf("{{ t('providers.title') }}"),
    dashboardSource.indexOf("{{ t('providers.title') }}") + 900,
  )
  assert.match(switchBlock, /aria-label="t\('failover\.switch_label'\)"/)
  assert.match(switchBlock, /:checked="failoverEnabled"/)
  assert.match(switchBlock, /:disabled="failoverSaving"/)
  assert.match(switchBlock, /@change="toggleFailover"/)
  assert.match(switchBlock, /failover\.switch_save_failed/)
  // 回滚逻辑：toggleFailover 在 catch 中恢复 previous。
  assert.match(dashboardSource, /async function toggleFailover[\s\S]*const previous = failoverEnabled\.value[\s\S]*failoverEnabled\.value = previous/)
})

test('provider cards refresh every 15s only while Providers tab is active', () => {
  assert.match(dashboardSource, /providersRefreshTimer = window\.setInterval\([\s\S]*15000\)/)
  assert.match(dashboardSource, /if \(activeTab\.value === 'providers'\)[\s\S]*void loadProviders\(\)/)
  // 离开 Providers tab 时清理定时器。
  assert.match(dashboardSource, /window\.clearInterval\(providersRefreshTimer\)/)
  assert.match(dashboardSource, /watch\(activeTab, \(\) => ensureProvidersRefresh\(\)\)/)
})

test('failover tab is accepted in the URL-tab whitelist', () => {
  assert.match(dashboardSource, /\['status', 'providers', 'connection', 'certs', 'usage', 'sessions', 'failover'\]/)
})

test('DashboardView renders FailoverEventsView only as an independent tab', () => {
  // FailoverEventsView 仅作为独立 tab 渲染（导入 + 模板各一处），不与会话导出/详情耦合。
  // 会话导出代码在 SessionBrowser.vue 内（本任务不修改），DashboardView 不直接持有导出逻辑。
  assert.match(dashboardSource, /const FailoverEventsView = defineAsyncComponent/)
  assert.match(dashboardSource, /<FailoverEventsView\s*\/>/)
})
