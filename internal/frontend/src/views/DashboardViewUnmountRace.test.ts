import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

// Finding 3：Dashboard 的 onMounted 是异步的。若 await 尚未完成组件即被卸载
// （例如用户快速切走路由），await 之后的代码仍会继续执行，从而：
//   1) 注册 statusRefreshTimer interval 与三个 window listener，却永远不会被清理；
//   2) loadStatus / loadConnectionMode 的在途旧响应继续写入 status / connectionMode 状态。
// 修复方案：引入 mounted/disposed 代际（mountGeneration），onBeforeUnmount 自增；
// onMounted 与各加载函数在 await 之后校验代际，只有仍持有所有权的调用才允许注册或更新状态。
const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')

const mountedBlock = dashboardSource.match(/onMounted\(async \(\) => \{[\s\S]*?\n}\)/)?.[0] || ''
const unmountBlock = dashboardSource.match(/onBeforeUnmount\(\(\) => \{[\s\S]*?\n}\)/)?.[0] || ''
const loadStatusBlock = dashboardSource.match(/async function loadStatus\([\s\S]*?\n\}/)?.[0] || ''
const loadConnectionModeBlock = dashboardSource.match(/async function loadConnectionMode\([\s\S]*?\n\}/)?.[0] || ''

test('dashboard declares a mount/dispose generation ownership token', () => {
  assert.match(dashboardSource, /let mountGeneration = 0/)
})

test('onBeforeUnmount advances the generation so in-flight work loses ownership', () => {
  assert.match(unmountBlock, /mountGeneration \+= 1/)
})

test('onMounted skips interval/listener registration if unmounted during its awaits', () => {
  // 捕获挂载代际，并在 await Promise.all 之后、注册 interval/listener 之前校验所有权。
  assert.match(mountedBlock, /const generation = mountGeneration/)
  assert.match(mountedBlock, /if \(generation !== mountGeneration\) return/)

  const awaitIndex = mountedBlock.indexOf('await Promise.all(')
  const guardIndex = mountedBlock.indexOf('if (generation !== mountGeneration) return')
  const providersRefreshIndex = mountedBlock.indexOf('ensureProvidersRefresh()')
  const intervalIndex = mountedBlock.indexOf('statusRefreshTimer = window.setInterval')
  const listenerIndex = mountedBlock.indexOf("addEventListener('mcc:mode-updated'")

  assert.ok(awaitIndex >= 0, 'onMounted must await the initial loads')
  assert.ok(guardIndex > awaitIndex, 'ownership guard must run after the awaited loads')
  assert.ok(guardIndex < providersRefreshIndex, 'guard must precede providers refresh interval')
  assert.ok(guardIndex < intervalIndex, 'guard must precede status interval registration')
  assert.ok(guardIndex < listenerIndex, 'guard must precede event listener registration')
})

test('in-flight status response does not update state after unmount', () => {
  assert.match(loadStatusBlock, /const generation = mountGeneration/)
  assert.match(loadStatusBlock, /if \(generation !== mountGeneration\) return null/)
  // 所有权校验必须发生在 await 之后、写入 status.value 之前。
  const awaitIndex = loadStatusBlock.indexOf('await request')
  const guardIndex = loadStatusBlock.indexOf('if (generation !== mountGeneration) return null')
  const assignIndex = loadStatusBlock.indexOf('status.value = nextStatus')
  assert.ok(guardIndex > awaitIndex, 'status guard must run after the awaited request')
  assert.ok(guardIndex < assignIndex, 'status guard must precede the state assignment')
})

test('in-flight connection-mode response does not update state after unmount', () => {
  assert.match(loadConnectionModeBlock, /const generation = mountGeneration/)
  assert.match(loadConnectionModeBlock, /if \(generation !== mountGeneration\) return/)
  // 所有权校验必须发生在 await 之后、写入 connectionConfig 之前。
  const awaitIndex = loadConnectionModeBlock.indexOf('await Promise.all(')
  const guardIndex = loadConnectionModeBlock.indexOf('if (generation !== mountGeneration) return')
  const assignIndex = loadConnectionModeBlock.indexOf('connectionConfig = config')
  assert.ok(guardIndex > awaitIndex, 'connection-mode guard must run after the awaited requests')
  assert.ok(guardIndex < assignIndex, 'connection-mode guard must precede the state assignment')
})

test('normal mount/refresh/cleanup behavior remains intact', () => {
  // 既有注册与清理逻辑保持不变（代际校验与之并存，不替换）。
  assert.match(dashboardSource, /statusRefreshTimer = window\.setInterval\(\(\) => \{\s*void loadStatus\(\)/)
  assert.match(dashboardSource, /addEventListener\('mcc:mode-updated', handleModeUpdated\)/)
  assert.match(dashboardSource, /addEventListener\('resize', handleUsageChartResize\)/)
  assert.match(dashboardSource, /addEventListener\('scroll', onScroll, \{ passive: true \}\)/)
  assert.match(unmountBlock, /removeEventListener\('mcc:mode-updated', handleModeUpdated\)/)
  assert.match(unmountBlock, /removeEventListener\('resize', handleUsageChartResize\)/)
  assert.match(unmountBlock, /removeEventListener\('scroll', onScroll\)/)
  // 既有 stale-response 版本校验仍保留。
  assert.match(dashboardSource, /if \(loadVersion !== statusLoadVersion\) return null/)
  assert.match(dashboardSource, /if \(loadVersion !== connectionModeLoadVersion\) return/)
})
