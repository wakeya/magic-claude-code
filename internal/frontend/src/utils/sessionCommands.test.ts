import test from 'node:test'
import assert from 'node:assert/strict'

import { tokenizeCommand } from './sessionCommands.ts'

test('tokenizeCommand classifies claude command keywords flags and paths', () => {
  const tokens = tokenizeCommand('claude project purge --dry-run /tmp/project-a').filter((token) => token.kind !== 'space')
  assert.deepEqual(tokens.map((token) => token.kind), ['command', 'keyword', 'keyword', 'flag', 'path'])
  assert.deepEqual(tokens.map((token) => token.text), ['claude', 'project', 'purge', '--dry-run', '/tmp/project-a'])
})

test('tokenizeCommand preserves spaces as separate tokens', () => {
  const tokens = tokenizeCommand('claude  project')
  assert.deepEqual(tokens.map((token) => token.kind), ['command', 'space', 'keyword'])
  assert.equal(tokens[1].text, '  ')
})
