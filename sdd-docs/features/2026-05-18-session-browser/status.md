# Claude Code Session Browser Status

**Feature:** Claude Code 会话记录浏览器
**Current state:** shipped
**Created:** 2026-05-18
**Last updated:** 2026-05-21
**Owner:** local project maintainer

## Lifecycle

```text
draft -> approved -> planned -> implementing -> implemented -> validating -> validated -> shipped
```

Current position:

```text
approved
```

Specs review completed on 2026-05-20; this feature is now `approved`.

## Transition Checklist

Move to `approved` when:

1. `requirements.md` v1.0 has been reviewed (file-based approach).
2. The project-directory navigation design is accepted.
3. The HTML export scope is accepted.
4. The deployment model (local mount or Docker volume) is confirmed.

Move to `planned` when:

1. `plan.md` has been reviewed.
2. `validation.md` has been reviewed.
3. All implementation tasks name concrete files and commands.

Move to `implementing` when:

1. A work branch or worktree is selected.
2. The `CLAUDE_PROJECTS_DIR` path is confirmed accessible.

## Blockers

None. This feature is independent of proxy logic and usage statistics.

## Revisit Trigger

None. The file-based approach uses Claude Code's native sessionId, eliminating the grouping uncertainty from the previous proxy-capture approach.
