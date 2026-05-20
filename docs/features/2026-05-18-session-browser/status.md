# Claude Code Session Browser Status

**Feature:** Claude Code 会话记录浏览器
**Current state:** draft
**Created:** 2026-05-18
**Last updated:** 2026-05-18
**Owner:** local project maintainer

## Lifecycle

```text
draft -> approved -> planned -> implementing -> implemented -> validating -> validated -> shipped
```

Current position:

```text
draft
```

## Transition Checklist

Move to `approved` when:

1. `requirements.md` has been reviewed.
2. The privacy default `session_capture_enabled=false` is accepted.
3. The three-column UI scope is accepted.
4. The storage limits and deletion behavior are accepted.

Move to `planned` when:

1. `plan.md` has been reviewed.
2. `validation.md` has been reviewed.
3. All implementation tasks name concrete files and commands.

Move to `implementing` when:

1. A work branch or worktree is selected.
2. The usage statistics work dependency is either merged or consciously decoupled.

## Blockers

1. This feature should not be implemented before the usage statistics schema/store shape is stable, because it links conversation requests back to request usage metadata.

## Revisit Trigger

Revisit this feature before implementation if Claude Code exposes a stable session ID in headers or request metadata, because that would simplify session grouping.
