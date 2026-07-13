import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const viewSource = readFileSync(join(here, 'FailoverEventsView.vue'), 'utf8')

test('FailoverEventsView states the global, JSONL-independent disclaimer', () => {
  assert.match(viewSource, /t\('failover\.disclaimer'\)/)
  assert.match(viewSource, /t\('failover\.global_tag'\)/)
})

test('FailoverEventsView renders source→target, model, signal, reason, outcome, disabled-until', () => {
  assert.match(viewSource, /routeLabel\(event\)/)
  assert.match(viewSource, /modelLabel\(event\)/)
  assert.match(viewSource, /signalLabel\(event\)/)
  assert.match(viewSource, /reasonLabel\(event\)/)
  assert.match(viewSource, /outcomeLabel\(event\.outcome\)/)
  assert.match(viewSource, /disabledUntilLabel\(event\)/)
  assert.match(viewSource, /formatTime\(event\.occurred_at\)/)
})

test('FailoverEventsView fetches events on mount and on refresh', () => {
  assert.match(viewSource, /onMounted\(loadEvents\)/)
  assert.match(viewSource, /api\.getFailoverEvents\(100\)/)
  assert.match(viewSource, /@click="loadEvents"/)
})

test('FailoverEventsView never mutates SessionDetail/export/JSONL', () => {
  // 组件不引用会话详情、导出、JSONL；事件仅用于只读展示。
  assert.doesNotMatch(viewSource, /SessionDetail|SessionBrowser|exportSession|\.jsonl/i)
  // 错误信息不把原始错误对象塞进 DOM，避免泄露上游细节。
  assert.match(viewSource, /void err/)
})

test('FailoverEventsView surfaces business_code in the signal column', () => {
  // signalLabel 同时展示 HTTP 状态码与业务码；组件引用 event.business_code。
  assert.match(viewSource, /event\.business_code/)
  assert.match(viewSource, /upstream_code/)
  // 原因列对已知码走 i18n。
  assert.match(viewSource, /reason_/)
})

test('FailoverEventsView outcome labels cover switched/exhausted/retry_failed/recovered', () => {
  for (const o of ['switched', 'exhausted', 'retry_failed', 'recovered']) {
    const pattern = new RegExp("case '" + o + "': return t\\('failover\\.outcome_" + o + "'\\)")
    assert.match(viewSource, pattern)
  }
})
