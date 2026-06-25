import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'SessionBrowser.vue'), 'utf8')
const i18nSource = readFileSync(join(here, '..', 'composables', 'useI18n.ts'), 'utf8')

test('desktop session outline panel scrolls independently when many user messages exist', () => {
  assert.match(source, /sticky top-4[^"]*max-h-\[calc\(100vh-2rem\)\][^"]*overflow-y-auto/)
  assert.match(source, /SessionOutline :messages="detail\.messages"/)
})

test('session browser no longer owns global theme switch', () => {
  assert.doesNotMatch(source, /session-theme-toggle/)
  assert.doesNotMatch(source, /setTheme\('light'\)/)
  assert.doesNotMatch(source, /setTheme\('dark'\)/)
})

test('session browser does not render the redundant selected-session hero block', () => {
  assert.doesNotMatch(source, /session-theme-hero/)
  assert.match(source, /v-if="!detail" class="session-empty-state"/)
})

test('session outline has back to top button in desktop and mobile views', () => {
  assert.match(source, /ArrowUp/)
  assert.match(source, /scrollToTop/)
  assert.match(source, /sessions\.back_to_top/)
  assert.match(source, /sticky bottom-0/)
  assert.match(source, /scrollToTop\(\); showOutline = false/)
})

test('cleanup hint note is localized and includes Windows terminal guidance', () => {
  assert.match(source, /\{\{\s*t\('sessions\.cleanup_note'\)\s*\}\}/)
  assert.doesNotMatch(source, /cleanupHint\.note/)

  assert.match(i18nSource, /'sessions\.cleanup_note': '管理面板不会删除 JSONL 文件。.*Windows.*PowerShell.*CMD.*--dry-run/)
  assert.match(i18nSource, /'sessions\.cleanup_note': 'The admin panel does not delete JSONL files\..*Windows.*PowerShell.*CMD.*--dry-run/)
})

test('cleanup hint shows Windows preview and interactive commands', () => {
  assert.match(source, /t\('sessions\.preview_command_windows'\)/)
  assert.match(source, /cleanupHint\.windows_preview_command/)
  assert.match(source, /t\('sessions\.interactive_command_windows'\)/)
  assert.match(source, /cleanupHint\.windows_interactive_command/)

  assert.match(i18nSource, /'sessions\.preview_command_windows': 'Windows 预览命令'/)
  assert.match(i18nSource, /'sessions\.interactive_command_windows': 'Windows 交互清理'/)
  assert.match(i18nSource, /'sessions\.preview_command_windows': 'Windows Preview Command'/)
  assert.match(i18nSource, /'sessions\.interactive_command_windows': 'Windows Interactive Cleanup'/)
})

test('session browser accepts preloaded list data from dashboard props', () => {
  assert.match(source, /defineProps<\{[\s\S]*projects: SessionProject\[\][\s\S]*sessions: SessionItem\[\][\s\S]*loading: boolean[\s\S]*\}>/)
  assert.match(source, /defineEmits<\{[\s\S]*refreshed[\s\S]*projects: SessionProject\[\][\s\S]*sessions: SessionItem\[\]/)
  assert.doesNotMatch(source, /onMounted\(\(\)\s*=>\s*\{\s*void reload\(\)\s*\}\)/)
})

test('session list loading state reserves space with a skeleton', () => {
  assert.match(source, /animate-pulse/)
  assert.match(source, /min-h-\[[^\]]+\]/)
  assert.doesNotMatch(source, /v-if="loading" class="session-empty-compact"/)
})

test('session list shows load errors before empty states', () => {
  assert.match(source, /v-if="loading"[\s\S]*v-else-if="error"[\s\S]*\{\{\s*error\s*\}\}[\s\S]*v-else-if="sessions\.length === 0"/)
})

test('session browser no longer has its own fixed back-to-top button', () => {
  // The global back-to-top is now in DashboardView; SessionBrowser keeps only
  // the outline-modal back-to-top (inside a modal backdrop context).
  assert.doesNotMatch(source, /fixed bottom-5 right-5 z-50/)
  // Outline modal's own back-to-top is preserved
  assert.match(source, /scrollToTop\(\); showOutline = false/)
})
