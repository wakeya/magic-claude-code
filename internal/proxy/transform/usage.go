package transform

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

type openAIUsageMetrics struct {
	totalInput       float64
	hasTotalInput    bool
	output           float64
	hasOutput        bool
	cacheRead        float64
	hasCacheRead     bool
	cacheCreation    float64
	hasCacheCreation bool
	cacheMiss        float64
}

func normalizeOpenAIUsage(
	usage map[string]any,
	totalInputPaths []string,
	outputPaths []string,
	cacheReadPaths []string,
	cacheCreationPaths []string,
) map[string]any {
	metrics := openAIUsageMetrics{
		totalInput:    firstUsageNumber(usage, totalInputPaths...),
		output:        firstUsageNumber(usage, outputPaths...),
		cacheRead:     firstUsageNumber(usage, cacheReadPaths...),
		cacheCreation: firstUsageNumber(usage, cacheCreationPaths...),
		cacheMiss:     firstUsageNumber(usage, "prompt_cache_miss_tokens"),
	}
	metrics.hasTotalInput = hasUsageNumber(usage, totalInputPaths...)
	metrics.hasOutput = hasUsageNumber(usage, outputPaths...)
	metrics.hasCacheRead = hasUsageNumber(usage, cacheReadPaths...)
	metrics.hasCacheCreation = hasUsageNumber(usage, cacheCreationPaths...)

	inputTokens := metrics.cacheMiss
	if metrics.hasTotalInput {
		inputTokens = metrics.totalInput
		if metrics.hasCacheRead {
			inputTokens -= metrics.cacheRead
		}
		if metrics.hasCacheCreation {
			inputTokens -= metrics.cacheCreation
		}
	}
	if inputTokens < 0 || math.IsNaN(inputTokens) || math.IsInf(inputTokens, 0) {
		inputTokens = 0
	}

	out := defaultAnthropicUsage()
	out["input_tokens"] = inputTokens
	if metrics.hasOutput {
		out["output_tokens"] = metrics.output
	}
	if metrics.hasCacheRead && metrics.cacheRead > 0 {
		out["cache_read_input_tokens"] = metrics.cacheRead
	}
	if metrics.hasCacheCreation && metrics.cacheCreation > 0 {
		out["cache_creation_input_tokens"] = metrics.cacheCreation
	}
	return out
}

func firstUsageNumber(usage map[string]any, paths ...string) float64 {
	value, _ := firstUsageNumberOK(usage, paths...)
	return value
}

func hasUsageNumber(usage map[string]any, paths ...string) bool {
	_, ok := firstUsageNumberOK(usage, paths...)
	return ok
}

func firstUsageNumberOK(usage map[string]any, paths ...string) (float64, bool) {
	for _, path := range paths {
		current := any(usage)
		for _, key := range strings.Split(path, ".") {
			object, ok := current.(map[string]any)
			if !ok {
				current = nil
				break
			}
			current, ok = object[key]
			if !ok {
				current = nil
				break
			}
		}
		if value, ok := usageNumber(current); ok {
			return value, true
		}
	}
	return 0, false
}

func usageNumber(value any) (float64, bool) {
	var number float64
	switch value := value.(type) {
	case float64:
		number = value
	case float32:
		number = float64(value)
	case int:
		number = float64(value)
	case int64:
		number = float64(value)
	case uint64:
		number = float64(value)
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	if number < 0 || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}
