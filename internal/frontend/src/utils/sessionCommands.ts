export type CommandTokenKind = 'command' | 'keyword' | 'flag' | 'path' | 'space' | 'text'

export interface CommandToken {
  text: string
  kind: CommandTokenKind
}

const keywordSet = new Set(['project', 'purge', 'session', 'delete', 'rm'])

export function tokenizeCommand(command: string): CommandToken[] {
  const parts = command.match(/\s+|\S+/g) || []
  let seenCommand = false

  return parts.map((part) => {
    if (/^\s+$/.test(part)) return { text: part, kind: 'space' }
    if (!seenCommand) {
      seenCommand = true
      return { text: part, kind: 'command' }
    }
    if (part.startsWith('--') || part.startsWith('-')) return { text: part, kind: 'flag' }
    if (part.startsWith('/') || part.startsWith('~') || /^[A-Za-z]:[\\/]/.test(part)) return { text: part, kind: 'path' }
    if (keywordSet.has(part)) return { text: part, kind: 'keyword' }
    return { text: part, kind: 'text' }
  })
}
