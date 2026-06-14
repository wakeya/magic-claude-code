import test from 'node:test'
import assert from 'node:assert/strict'
import {
  isOpenAICompatibleFormat,
  shouldDefaultClaudeCodeCompatHint,
} from './providerForm.ts'

test('detects OpenAI-Compatible provider formats', () => {
  assert.equal(isOpenAICompatibleFormat('anthropic'), false)
  assert.equal(isOpenAICompatibleFormat('openai_chat'), true)
  assert.equal(isOpenAICompatibleFormat('openai_responses'), true)
})

test('defaults Claude Code compatibility hint only when entering OpenAI-Compatible mode', () => {
  assert.equal(shouldDefaultClaudeCodeCompatHint('anthropic', 'openai_chat'), true)
  assert.equal(shouldDefaultClaudeCodeCompatHint('anthropic', 'openai_responses'), true)

  assert.equal(shouldDefaultClaudeCodeCompatHint('openai_chat', 'openai_responses'), false)
  assert.equal(shouldDefaultClaudeCodeCompatHint('openai_responses', 'openai_chat'), false)
  assert.equal(shouldDefaultClaudeCodeCompatHint('openai_chat', 'anthropic'), false)
  assert.equal(shouldDefaultClaudeCodeCompatHint('anthropic', 'anthropic'), false)
})
