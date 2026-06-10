# Multimodal Model Switch Decisions

## D-001: Use One Multimodal Model ID Per Provider

**Date:** 2026-06-09
**Status:** accepted

**Context:** The switch is intended for Providers whose normal text model rejects requests containing non-text content. Per-original-model multimodal mappings would add UI and configuration complexity.

**Decision:** Each Provider configures a single `multimodal_model`. When the request contains images, PDFs, audio, or video, the proxy switches to that single model.

**Impact:** Configuration is simple and troubleshooting is clear. The tradeoff is that one Provider cannot route different original models to different multimodal models.

**Re-evaluation trigger:** Revisit this if one Provider needs multiple multimodal tiers, such as a premium vision model for Opus requests and a lower-cost vision model for Sonnet requests.

## D-002: Label the Switch "多模态切换"

**Date:** 2026-06-09
**Status:** accepted

**Context:** "Switch models when non-text content is present" is accurate but too long for the UI. "Supports multimodal" would be misleading because this is a proxy routing strategy, not a Provider capability claim.

**Decision:** Use `多模态切换` as the switch label and `多模态模型 ID` as the input label.

**Impact:** The UI remains compact while preserving the idea that the proxy switches models under specific conditions.

**Re-evaluation trigger:** Revisit this if the Provider settings UI later adds a broader capability matrix that separates Provider capabilities from proxy strategies.

## D-003: Keep the Switch Disabled by Default

**Date:** 2026-06-09
**Status:** accepted

**Context:** Some Providers, such as GLM-compatible endpoints, may already accept image-bearing requests even when the configured model looks text-oriented. Global automatic switching could break those Providers.

**Decision:** The switch is disabled by default. The proxy overrides the mapped model only when the user explicitly enables the switch and provides a model ID.

**Impact:** Existing Provider behavior remains stable. Providers such as Mimo that reject image input require explicit configuration.

**Re-evaluation trigger:** Revisit this if reliable Provider/model capability discovery becomes available.

## D-004: Recursively Detect Non-Text Content

**Date:** 2026-06-09
**Status:** accepted

**Context:** The observed image was not in the top-level user message. It was nested inside a screenshot tool result under `tool_result.content`. A shallow scan would miss this case.

**Decision:** Recursively scan arrays and objects under `messages` and `system`, matching explicit content block types or `source.media_type`.

**Impact:** The implementation covers screenshots, PDFs, and other tool-returned non-text content. The scan is linear in request size and reuses the existing JSON parse, so normal latency impact is negligible.

**Re-evaluation trigger:** Revisit this if a new non-text content format appears outside the current matching rules.

## D-005: Do Not Modify Protocol Headers

**Date:** 2026-06-09
**Status:** accepted

**Context:** The observed failure was caused by an upstream model that did not support image input, not by incompatible Anthropic Beta headers. Changing headers too early could break other Providers.

**Decision:** Version 1 only changes the request `model` field. It does not modify `Anthropic-Beta`, `Anthropic-Version`, or other request headers.

**Impact:** Scope and risk remain low. If a multimodal model still fails because of protocol headers, a separate Provider-level compatibility strategy should be designed.

**Re-evaluation trigger:** Revisit this if a configured multimodal model still returns compatibility errors related to Beta headers or protocol fields.
