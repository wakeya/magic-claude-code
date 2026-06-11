# Multimodal Model Switch Status

**Feature:** Multimodal model switch
**Current status:** validated
**Created:** 2026-06-09
**Last updated:** 2026-06-09
**Owner:** Local project maintainer

## Lifecycle

```text
draft -> approved -> planned -> implementing -> implemented -> validating -> validated -> shipped
```

Current position:

```text
validated
```

## Summary

The Provider configuration now supports an optional multimodal model switch. When a request contains images, PDFs, audio, or video, and the active Provider has the switch enabled with a configured model ID, the proxy replaces the request `model` before forwarding. Disabled Providers and pure text requests continue to use existing model mappings.

## Completed

1. Added `multimodal_switch` and `multimodal_model` to Provider configuration.
2. Added recursive non-text content detection during proxy request transformation.
3. Implemented SQLite persistence and old-database column migration.
4. Updated Admin API create, list, detail, update, and duplicate behavior.
5. Added `多模态切换`, tooltip, and `多模态模型 ID` to the Provider modal.
6. Added Provider card summary for multimodal configuration.
7. Widened the Provider modal on desktop to about three quarters of the providers page content width for more comfortable editing.
8. Verified backend tests, frontend tests, and frontend build.
9. Verified by user against the real Mimo multimodal model `mimo-v2.5`.

## Pending Release

1. The code is currently uncommitted in the `main` worktree.
2. Deployment requires committing and releasing the validated changes.

## Re-evaluation Triggers

1. A Provider needs different multimodal models for different original models.
2. A multimodal model still fails because of Anthropic Beta headers or other protocol fields.
3. New non-text content formats appear outside the current detection rules.
4. Request bodies near the 10MB limit make recursive scanning a measurable latency concern.
