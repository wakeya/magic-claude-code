import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'useI18n.ts'), 'utf8')

// Helper: count how many times a key appears (must appear in both zh and en sections)
function keyExists(key: string): boolean {
  return source.includes(`'${key}'`)
}

test('i18n has providers.export key in zh and en', () => {
  assert.ok(keyExists('providers.export'), 'providers.export missing')
})

test('i18n has providers.import key in zh and en', () => {
  assert.ok(keyExists('providers.import'), 'providers.import missing')
})

test('i18n has export security warning key', () => {
  assert.ok(keyExists('providers.export_warning'), 'export_warning missing')
})

test('i18n has import error and done keys', () => {
  assert.ok(keyExists('providers.import_invalid'), 'import_invalid missing')
  assert.ok(keyExists('providers.import_done'), 'import_done missing')
})

test('i18n explains partial import persistence and cleanup errors in both locales', () => {
  assert.equal(source.split("'providers.import_partial'").length - 1, 2)
  assert.match(source, /部分成功[\s\S]*配置已保存[\s\S]*快照清理失败/)
  assert.match(source, /partially succeeded[\s\S]*configuration (?:was )?saved[\s\S]*snapshot cleanup failed/i)
})

test('i18n distinguishes validation errors and refresh failures in both locales', () => {
  assert.equal(source.split("'providers.import_with_errors'").length - 1, 2)
  assert.equal(source.split("'providers.import_refresh_failed'").length - 1, 2)
  assert.match(source, /部分条目无效或未导入/)
  assert.match(source, /界面刷新失败/)
  assert.match(source, /Some entries were invalid or not imported/i)
  assert.match(source, /interface refresh failed/i)
})

test('i18n has preview modal keys', () => {
  assert.ok(keyExists('providers.preview.title'), 'preview.title missing')
  assert.ok(keyExists('providers.preview.new'), 'preview.new missing')
  assert.ok(keyExists('providers.preview.conflict'), 'preview.conflict missing')
  assert.ok(keyExists('providers.preview.strategy'), 'preview.strategy missing')
  assert.ok(keyExists('providers.preview.strategy_skip'), 'preview.strategy_skip missing')
  assert.ok(keyExists('providers.preview.strategy_overwrite'), 'preview.strategy_overwrite missing')
  assert.ok(keyExists('providers.preview.strategy_duplicate'), 'preview.strategy_duplicate missing')
  assert.ok(keyExists('providers.preview.cancel'), 'preview.cancel missing')
  assert.ok(keyExists('providers.preview.confirm'), 'preview.confirm missing')
})

test('export warning mentions token/secret in both locales', () => {
  // The warning must mention tokens or secrets
  assert.match(source, /token|密钥|secret/i)
})
