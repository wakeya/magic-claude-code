import test from 'node:test'
import assert from 'node:assert/strict'
import { localizeExposedModelError } from './providerError.ts'

// 记录型 t：返回选中的键，并记录插值参数，便于断言「映射到哪个键 + 提取了哪些值」。
function recordingT() {
  const calls: { key: string; params?: Record<string, string | number> }[] = []
  const t = (key: string, params?: Record<string, string | number>): string => {
    calls.push({ key, params })
    return key
  }
  return { t, calls }
}

test('maps spaces/control-char error and echoes the display name', () => {
  const { t, calls } = recordingT()
  const raw = 'provider[12]: exposed_models[0]: display name (id) "智谱　GLM" must not contain spaces or control characters (check for invisible/full-width spaces)'
  assert.equal(localizeExposedModelError(raw, t), 'modal.error_exposed_spaces')
  assert.deepEqual(calls[0].params, { name: '智谱　GLM' })
})

test('maps claude- prefix error', () => {
  const { t, calls } = recordingT()
  const raw = 'provider[0]: exposed_models[0]: display name (id) "claude-opus" must not start with "claude-" (conflicts with built-in menu items)'
  assert.equal(localizeExposedModelError(raw, t), 'modal.error_exposed_claude_prefix')
  assert.deepEqual(calls[0].params, { name: 'claude-opus' })
})

test('maps [1m] reserved error', () => {
  const { t, calls } = recordingT()
  const raw = 'provider[0]: exposed_models[0]: display name (id) "glm[1m]" must not contain "[1m]" (reserved by Claude Code 1M-context handling)'
  assert.equal(localizeExposedModelError(raw, t), 'modal.error_exposed_1m')
  assert.deepEqual(calls[0].params, { name: 'glm[1m]' })
})

test('maps reserved alias error', () => {
  const { t, calls } = recordingT()
  const raw = 'provider[0]: exposed_models[0]: display name (id) "sonnet" is reserved by Claude Code model aliases'
  assert.equal(localizeExposedModelError(raw, t), 'modal.error_exposed_alias')
  assert.deepEqual(calls[0].params, { name: 'sonnet' })
})

test('maps within-provider duplicate error', () => {
  const { t, calls } = recordingT()
  const raw = 'provider[0]: exposed_models[1]: duplicate display name (id) "GLM-4.6" within provider'
  assert.equal(localizeExposedModelError(raw, t), 'modal.error_exposed_dup_provider')
  assert.deepEqual(calls[0].params, { name: 'GLM-4.6' })
})

test('maps cross-provider duplicate error with both provider names', () => {
  const { t, calls } = recordingT()
  const raw = 'exposed model display name (id) "GLM-4.6" is duplicated between provider "智谱" and "备份"'
  assert.equal(localizeExposedModelError(raw, t), 'modal.error_exposed_dup_global')
  assert.deepEqual(calls[0].params, { name: 'GLM-4.6', p1: '智谱', p2: '备份' })
})

test('maps label-required error with 1-based index', () => {
  const { t, calls } = recordingT()
  const raw = 'provider[3]: exposed_models[0]: label is required'
  assert.equal(localizeExposedModelError(raw, t), 'modal.error_exposed_label_required')
  assert.deepEqual(calls[0].params, { index: 1 })
})

test('maps backend-required error with 1-based index', () => {
  const { t, calls } = recordingT()
  const raw = 'provider[3]: exposed_models[2]: backend_model is required'
  assert.equal(localizeExposedModelError(raw, t), 'modal.error_exposed_backend_required')
  assert.deepEqual(calls[0].params, { index: 3 })
})

test('unknown error passes through unchanged', () => {
  const { t, calls } = recordingT()
  const raw = 'api_url must use http or https scheme'
  assert.equal(localizeExposedModelError(raw, t), raw)
  assert.equal(calls.length, 0)
})

test('empty error passes through unchanged', () => {
  const { t } = recordingT()
  assert.equal(localizeExposedModelError('', t), '')
})
