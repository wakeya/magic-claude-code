export type ProviderAPIFormat = 'anthropic' | 'openai_chat' | 'openai_responses'

export function isOpenAICompatibleFormat(format: ProviderAPIFormat): boolean {
  return format === 'openai_chat' || format === 'openai_responses'
}

export function shouldDefaultClaudeCodeCompatHint(
  previousFormat: ProviderAPIFormat,
  nextFormat: ProviderAPIFormat,
): boolean {
  return previousFormat === 'anthropic' && isOpenAICompatibleFormat(nextFormat)
}
