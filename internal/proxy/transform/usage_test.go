package transform

import (
	"math"
	"testing"
)

func TestNormalizeOpenAIUsageUsesPromptCacheMissTokensWithoutTotalInput(t *testing.T) {
	usage := normalizeOpenAIUsage(
		map[string]any{
			"completion_tokens":           float64(50),
			"prompt_cache_hit_tokens":     float64(600),
			"prompt_cache_miss_tokens":    float64(400),
			"cache_creation_input_tokens": float64(0),
		},
		[]string{"prompt_tokens"},
		[]string{"completion_tokens"},
		[]string{"prompt_cache_hit_tokens"},
		[]string{"cache_creation_input_tokens"},
	)

	if usage["input_tokens"] != float64(400) || usage["cache_read_input_tokens"] != float64(600) {
		t.Fatalf("normalized usage = %#v", usage)
	}
}

func TestNormalizeOpenAIUsageClampsInputAtZeroWhenTotalIsLessThanCache(t *testing.T) {
	usage := normalizeOpenAIUsage(
		map[string]any{
			"prompt_tokens":            float64(100),
			"completion_tokens":        float64(50),
			"prompt_cache_hit_tokens":  float64(600),
			"prompt_cache_miss_tokens": float64(400),
		},
		[]string{"prompt_tokens"},
		[]string{"completion_tokens"},
		[]string{"prompt_cache_hit_tokens"},
		nil,
	)

	if usage["input_tokens"] != float64(0) || usage["cache_read_input_tokens"] != float64(600) {
		t.Fatalf("clamped usage = %#v", usage)
	}
}

func TestNormalizeOpenAIUsageFallsThroughUnparseableStandardCacheField(t *testing.T) {
	usage := normalizeOpenAIUsage(
		map[string]any{
			"prompt_tokens":            float64(1000),
			"prompt_tokens_details":    map[string]any{"cached_tokens": "not-a-number"},
			"prompt_cache_hit_tokens":  float64(600),
			"prompt_cache_miss_tokens": float64(400),
		},
		[]string{"prompt_tokens"},
		nil,
		[]string{"prompt_tokens_details.cached_tokens", "cache_read_input_tokens", "prompt_cache_hit_tokens"},
		nil,
	)

	if usage["input_tokens"] != float64(400) || usage["cache_read_input_tokens"] != float64(600) {
		t.Fatalf("unparseable fallback usage = %#v", usage)
	}
}

func TestNormalizeOpenAIUsageTreatsNegativeZeroCacheAsAbsent(t *testing.T) {
	usage := normalizeOpenAIUsage(
		map[string]any{
			"prompt_tokens":            float64(1000),
			"prompt_tokens_details":    map[string]any{"cached_tokens": math.Copysign(0, -1)},
			"prompt_cache_hit_tokens":  float64(600),
			"prompt_cache_miss_tokens": float64(400),
		},
		[]string{"prompt_tokens"},
		nil,
		[]string{"prompt_tokens_details.cached_tokens", "cache_read_input_tokens", "prompt_cache_hit_tokens"},
		nil,
	)

	if usage["input_tokens"] != float64(400) || usage["cache_read_input_tokens"] != float64(600) {
		t.Fatalf("negative zero cache usage = %#v", usage)
	}
}

func TestNormalizeOpenAIUsageTreatsNegativeCacheValuesAsAbsent(t *testing.T) {
	usage := normalizeOpenAIUsage(
		map[string]any{
			"prompt_tokens":         float64(1000),
			"prompt_tokens_details": map[string]any{"cached_tokens": float64(-5)},
		},
		[]string{"prompt_tokens"},
		nil,
		[]string{"prompt_tokens_details.cached_tokens", "prompt_cache_hit_tokens"},
		nil,
	)

	if usage["input_tokens"] != float64(1000) {
		t.Fatalf("negative cache usage = %#v", usage)
	}
	if _, ok := usage["cache_read_input_tokens"]; ok {
		t.Fatalf("negative cache must not emit cache_read_input_tokens = %#v", usage)
	}
}

func TestNormalizeOpenAIUsageSubtractsCacheCreationFromInput(t *testing.T) {
	usage := normalizeOpenAIUsage(
		map[string]any{
			"prompt_tokens":               float64(1000),
			"completion_tokens":           float64(50),
			"cache_creation_input_tokens": float64(300),
		},
		[]string{"prompt_tokens"},
		[]string{"completion_tokens"},
		nil,
		[]string{"cache_creation_input_tokens"},
	)

	if usage["input_tokens"] != float64(700) || usage["cache_creation_input_tokens"] != float64(300) {
		t.Fatalf("cache creation usage = %#v", usage)
	}
}
