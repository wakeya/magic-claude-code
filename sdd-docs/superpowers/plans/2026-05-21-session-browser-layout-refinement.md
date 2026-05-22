# Session Browser Layout Refinement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refine the existing Claude Code session browser so the left panel contains a project dropdown plus session list, the right panel stays focused on session detail, user messages are full light-green blocks, exported HTML matches that highlight, and cleanup commands render as dark editor-style code blocks.

**Architecture:** Keep the existing API and data model unchanged. Update frontend component state/layout in `SessionBrowser.vue`, extract small command-token rendering utilities for testable syntax highlighting, update `SessionDetail.vue` styling, and update the Go export template. No proxy or backend API behavior changes are required.

**Tech Stack:** Vue 3, TypeScript, node:test, Go `html/template`, Go testing.

---

## File Map

Modify:

1. `docs/features/2026-05-18-session-browser/requirements.md`: English v1.1 requirements.
2. `docs/features/2026-05-18-session-browser/decisions.md`: English D-006 layout decision.
3. `docs/features/2026-05-18-session-browser/plan.md`: add v1.1 refinement task summary.
4. `docs/features/2026-05-18-session-browser/plan_ZH.md`: add v1.1 refinement task summary.
5. `internal/session/export_test.go`: assert exported user messages use full green block styling.
6. `internal/session/export.go`: change exported user message CSS.
7. `internal/frontend/src/utils/sessionCommands.ts`: create command-token helper for cleanup command highlighting.
8. `internal/frontend/src/utils/sessionCommands.test.ts`: test command-token helper.
9. `internal/frontend/src/components/SessionBrowser.vue`: project dropdown plus left session list; dark highlighted command editor blocks.
10. `internal/frontend/src/components/SessionDetail.vue`: full green user message blocks.
11. `internal/frontend/src/composables/useI18n.ts`: add missing labels for the project dropdown if needed.

## Task 1: Sync English Specs and Plan Docs

- [ ] **Step 1: Update English requirements**

Edit `docs/features/2026-05-18-session-browser/requirements.md` so:

```markdown
**Version:** 1.1
**Date:** 2026-05-21
```

and section 8.2 specifies:

```markdown
**Left panel (recommended 360px):**

1. Project dropdown at the top, sorted by `lastActiveAt DESC`.
2. Dropdown includes `"All Projects"` and concrete projects; each option shows project name and session count.
3. Selecting a project refreshes the left panel body with that project's sessions.
4. Session list scrolls vertically inside the left panel and does not affect the right detail scroll position.
5. Session cards show title, relative time, and message count; in `"All Projects"` mode, they also show a project name or path summary.
6. The currently selected session is highlighted in the left list.
```

- [ ] **Step 2: Update English decisions**

Edit `docs/features/2026-05-18-session-browser/decisions.md` to rename D-002 to project filtering and add D-006 documenting the selected layout.

- [ ] **Step 3: Add plan addendum**

Append a v1.1 refinement section to both `docs/features/2026-05-18-session-browser/plan.md` and `docs/features/2026-05-18-session-browser/plan_ZH.md` listing the four implementation units: layout, user-message highlight, exported HTML highlight, cleanup command editor.

## Task 2: Exported HTML User Highlight

**Files:**

- Modify: `internal/session/export_test.go`
- Modify: `internal/session/export.go`

- [ ] **Step 1: Write the failing export test**

Add this test to `internal/session/export_test.go`:

```go
func TestExportHTMLHighlightsUserMessagesAsGreenBlocks(t *testing.T) {
	html := exportTestHTML(t)
	for _, want := range []string{".message.user{", "background:#dcfce7", "border:1px solid #86efac", "color:#166534"} {
		if !strings.Contains(html, want) {
			t.Fatalf("exported user message CSS missing %q: %s", want, html)
		}
	}
}
```

- [ ] **Step 2: Run the test and verify RED**

Run: `rtk go test ./internal/session -run TestExportHTMLHighlightsUserMessagesAsGreenBlocks -v`

Expected: FAIL because current export CSS uses only a user left border and dark user block background.

- [ ] **Step 3: Implement minimal export CSS change**

In `internal/session/export.go`, replace the user CSS:

```css
.message.user{border-left:4px solid #38bdf8}
```

with:

```css
.message.user{background:#dcfce7;border:1px solid #86efac;color:#14532d}
.message.user .role{color:#166534}
```

- [ ] **Step 4: Verify GREEN**

Run: `rtk go test ./internal/session -run TestExport -v`

Expected: PASS.

## Task 3: Cleanup Command Highlight Helper

**Files:**

- Create: `internal/frontend/src/utils/sessionCommands.ts`
- Create: `internal/frontend/src/utils/sessionCommands.test.ts`

- [ ] **Step 1: Write the failing utility test**

Create `internal/frontend/src/utils/sessionCommands.test.ts`:

```ts
import test from 'node:test'
import assert from 'node:assert/strict'

import { tokenizeCommand } from './sessionCommands.ts'

test('tokenizeCommand classifies claude command keywords flags and paths', () => {
  const tokens = tokenizeCommand('claude project purge --dry-run /tmp/project-a')
  assert.deepEqual(tokens.map((token) => token.kind), ['command', 'keyword', 'keyword', 'flag', 'path'])
  assert.deepEqual(tokens.map((token) => token.text), ['claude', 'project', 'purge', '--dry-run', '/tmp/project-a'])
})

test('tokenizeCommand preserves spaces as separate tokens', () => {
  const tokens = tokenizeCommand('claude  project')
  assert.deepEqual(tokens.map((token) => token.kind), ['command', 'space', 'keyword'])
  assert.equal(tokens[1].text, '  ')
})
```

- [ ] **Step 2: Run the test and verify RED**

Run: `rtk npm --prefix internal/frontend test -- --test-name-pattern tokenizeCommand`

Expected: FAIL because `sessionCommands.ts` does not exist.

- [ ] **Step 3: Implement the helper**

Create `internal/frontend/src/utils/sessionCommands.ts`:

```ts
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
```

- [ ] **Step 4: Verify GREEN**

Run: `rtk npm --prefix internal/frontend test -- --test-name-pattern tokenizeCommand`

Expected: PASS.

## Task 4: Frontend Layout and Styling

**Files:**

- Modify: `internal/frontend/src/components/SessionBrowser.vue`
- Modify: `internal/frontend/src/components/SessionDetail.vue`
- Modify: `internal/frontend/src/composables/useI18n.ts`

- [ ] **Step 1: Update `SessionBrowser.vue` layout**

Change the left panel from project buttons plus nested sessions to:

```vue
<aside class="rounded-lg border-2 border-border bg-white">
  <div class="border-b border-border p-4">
    <div class="mb-2 flex items-center justify-between gap-2">
      <label for="session-project-select" class="flex items-center gap-2 text-sm font-bold">
        <Folder class="h-4 w-4" />
        {{ t('sessions.projects') }}
      </label>
      <button class="rounded-md p-2 text-text-secondary hover:bg-muted hover:text-fg" :title="t('sessions.refresh')" @click="reload">
        <RefreshCw class="h-4 w-4" />
      </button>
    </div>
    <select id="session-project-select" v-model="selectedProject" class="w-full rounded-lg border-2 border-border bg-white px-3 py-2 text-sm font-semibold text-fg" @change="selectProject(selectedProject)">
      <option value="">{{ t('sessions.all_projects') }} ({{ totalSessions }})</option>
      <option v-for="project in projects" :key="project.path" :value="project.path">
        {{ project.name }} ({{ project.session_count }})
      </option>
    </select>
  </div>
  <div class="max-h-[calc(100vh-260px)] overflow-y-auto p-3">
    <!-- session cards only -->
  </div>
</aside>
```

Remove `projectButtonClass()` after it is unused.

- [ ] **Step 2: Keep details on the right**

Ensure clicking a session only updates `selectedSession` and `detail`; no back-button state is introduced.

- [ ] **Step 3: Update cleanup command rendering**

Import `tokenizeCommand` and render command tokens inside a dark code block:

```ts
import { tokenizeCommand, type CommandTokenKind } from '@/utils/sessionCommands'
```

Use `commandTokenClass(kind)` for token colors:

```ts
function commandTokenClass(kind: CommandTokenKind): string {
  const classes: Record<CommandTokenKind, string> = {
    command: 'text-emerald-300',
    keyword: 'text-sky-300',
    flag: 'text-amber-300',
    path: 'text-fuchsia-200',
    space: 'text-slate-300',
    text: 'text-slate-100',
  }
  return classes[kind]
}
```

- [ ] **Step 4: Update user message block styling**

In `SessionDetail.vue`, make user messages use:

```ts
message.role === 'user' ? 'border-green-300 bg-green-100 text-green-950' : ''
```

and use a dark-green role label for user messages.

- [ ] **Step 5: Verify frontend tests and build**

Run:

```bash
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
```

Expected: PASS.

## Task 5: Full Verification

- [ ] **Step 1: Run Go tests**

Run: `rtk go test ./...`

Expected: PASS.

- [ ] **Step 2: Run frontend tests and build**

Run:

```bash
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
```

Expected: PASS.

- [ ] **Step 3: Review changed files**

Run: `rtk git diff --stat`

Expected: Changes are limited to session browser docs, frontend session browser components/utilities, and session export tests/template.
