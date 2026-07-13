import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')

test('DashboardView wraps provider cards in a draggable container', () => {
  // 拖拽：draggable=true + dragstart/dragover/drop 事件绑定。
  assert.match(dashSource, /draggable="true"/)
  assert.match(dashSource, /@dragstart=/)
  assert.match(dashSource, /@dragover\.prevent=/)
  assert.match(dashSource, /@drop\.prevent=/)
})

test('drag starts only from the drag handle, not from card content/buttons', () => {
  // 手柄带 data-provider-drag-handle；dragstart 校验事件来源，非手柄发起则 preventDefault。
  assert.match(dashSource, /data-provider-drag-handle/)
  assert.match(dashSource, /closest\(['"]\[data-provider-drag-handle\]['"]\)/)
  assert.match(dashSource, /preventDefault\(\)/)
})

test('drag handle arms the outer draggable card before dragstart fires', () => {
  // 浏览器 dragstart 的 target 通常是 draggable 外层容器，不是内部手柄。
  // 因此必须在 pointerdown 阶段记录“本次拖拽从手柄发起”，dragstart 再读取该状态。
  assert.match(dashSource, /@pointerdown\.capture="onProviderPointerDown\(\$event,\s*index\)"/)
  assert.match(dashSource, /providerDragHandleIndex/)
  assert.match(dashSource, /function\s+onProviderPointerDown\(event:\s*PointerEvent,\s*index:\s*number\)/)
  assert.match(dashSource, /providerDragHandleIndex\.value\s*=\s*index/)
  assert.match(dashSource, /providerDragHandleIndex\.value\s*!==\s*index/)
})

test('DashboardView passes orderIndex (index) to ProviderCard', () => {
  assert.match(dashSource, /:order-index="index"|:order-index="idx"/)
})

test('DashboardView handles move-up/move-down from ProviderCard', () => {
  assert.match(dashSource, /@move-up=/)
  assert.match(dashSource, /@move-down=/)
})

test('DashboardView has a reorderProviders function that calls the API', () => {
  assert.match(dashSource, /api\.reorderProviders\(/)
})

test('DashboardView rolls back provider order on reorder failure', () => {
  // 失败回滚：保留拖拽前顺序快照 + catch 中恢复。
  assert.match(dashSource, /providers\.reorder_failed/)
  assert.match(dashSource, /reorderProviders[\s\S]{0,400}catch[\s\S]{0,200}回滚|previousProviders|previousOrder|rollback/i)
})

test('DashboardView does not call the API when dropped at the original position', () => {
  // 拖到原位置不发请求：比较 from/to index 相等则跳过。
  assert.match(dashSource, /===|!==[\s\S]{0,80}(reorderProviders|return)/)
})

test('DashboardView disable move-up on the first item and move-down on the last', () => {
  // 上移按钮在第一项禁用、下移在最后一项禁用（通过 :can-move-up / :can-move-down 或 index 比较）。
  assert.match(dashSource, /can-move-up|canMoveUp|index\s*===\s*0|index\s*>\s*0/)
  assert.match(dashSource, /can-move-down|canMoveDown|index\s*===\s*providers\.length\s*-\s*1|index\s*<\s*providers\.length\s*-\s*1/)
})

test('DashboardView adds an accessible tooltip next to the auto-failover switch', () => {
  // 问号图标紧邻自动切换开关；tooltip 用 i18n key failover.switch_help，支持 hover+focus。
  const switchIdx = dashSource.indexOf('failover.switch_label')
  const after = dashSource.slice(switchIdx, switchIdx + 1200)
  assert.match(after, /failover\.switch_help/)
  // focus 支持：tabindex 或 focus-within/group-focus。
  assert.match(after, /tabindex="0"|group-focus-within|focus-within/)
})

test('DashboardView tooltip i18n mentions drag-to-reorder and /model independence', () => {
  // 校验 i18n 文案在 useI18n 里（中英），不直接断言 DashboardView 内联文案。
  const i18n = readFileSync(join(here, '..', 'composables', 'useI18n.ts'), 'utf8')
  assert.match(i18n, /failover\.switch_help'/)
  assert.match(i18n, /拖拽供应商卡片/)
  assert.match(i18n, /不影响会话\n内 \/model|不影响会话内 \/model/)
  assert.match(i18n, /Drag provider cards/)
  assert.match(i18n, /in-session \/model choices/)
})

test('reorder does not modify ActiveProviderID on the client', () => {
  // 客户端重排只动 providers 顺序，不写 activeProviderId。
  const reorderFn = dashSource.match(/function\s+(?:reorderProviders|applyReorder|moveProvider)[\s\S]{0,800}?\n\}/g) || []
  const blob = reorderFn.join('\n')
  // 重排函数里不应出现给 activeProviderId 赋值。
  assert.doesNotMatch(blob, /activeProviderId\.value\s*=/)
})
