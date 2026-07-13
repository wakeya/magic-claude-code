import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const cardSource = readFileSync(join(here, 'ProviderCard.vue'), 'utf8')

test('ProviderCard accepts an orderIndex prop for the priority badge', () => {
  assert.match(cardSource, /orderIndex/)
})

test('ProviderCard renders a circular priority badge left of the quota block', () => {
  // badge 出现在额度显示块之前，蓝底白字圆形。
  const quotaIdx = cardSource.indexOf('quotaDisplay')
  const beforeQuota = cardSource.slice(0, quotaIdx)
  assert.match(
    beforeQuota,
    /rounded-full[^>]*bg-primary[^>]*text-white[\s\S]*?\{\{\s*orderIndex\s*\+\s*1\s*\}\}/,
  )
})

test('priority badge uses i18n aria-label with 1-based index', () => {
  assert.match(cardSource, new RegExp("providers\\.priority_label"))
  assert.match(cardSource, /orderIndex\s*\+\s*1/)
})

test('ProviderCard emits move-up and move-down for keyboard/mobile reorder', () => {
  assert.match(cardSource, /move-up/)
  assert.match(cardSource, /move-down/)
})

test('ProviderCard move buttons exist in the action area', () => {
  // 上移/下移按钮调用 $emit('move-up'/'move-down')。
  assert.match(cardSource, /\$emit\('move-up'\)/)
  assert.match(cardSource, /\$emit\('move-down'\)/)
})

test('disabled provider still renders the badge (no enabled-gate on badge)', () => {
  // badge 不被 provider.enabled 条件包裹。
  const badgeMatch = cardSource.match(/orderIndex[\s\S]{0,400}?\{\{\s*orderIndex\s*\+\s*1\s*\}\}/)
  assert.ok(badgeMatch, 'priority badge rendering not found')
  assert.doesNotMatch(badgeMatch[0], /v-if="provider\.enabled"/)
})
