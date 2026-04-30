package config

import "fmt"

// ExtractPricing reads pricing_usd_per_1m_tokens from a profile's Info map.
// Returns (inputPer1M, outputPer1M, ok).
func ExtractPricing(info map[string]any) (inputPer1M, outputPer1M float64, ok bool) {
	if info == nil {
		return 0, 0, false
	}
	raw, exists := info["pricing_usd_per_1m_tokens"]
	if !exists {
		return 0, 0, false
	}
	m, isMap := raw.(map[string]any)
	if !isMap {
		return 0, 0, false
	}
	inp, hasInp := toFloat64(m["input"])
	out, hasOut := toFloat64(m["output"])
	if !hasInp || !hasOut {
		return 0, 0, false
	}
	return inp, out, true
}

func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// CalcCost returns the approximate USD cost for given token counts.
func CalcCost(inputTokens, outputTokens int, inputPer1M, outputPer1M float64) float64 {
	return float64(inputTokens)/1_000_000*inputPer1M +
		float64(outputTokens)/1_000_000*outputPer1M
}

// FormatCost formats a dollar amount compactly.
func FormatCost(dollars float64) string {
	if dollars < 0.00005 {
		return "<$0.0001"
	}
	if dollars < 0.01 {
		return fmt.Sprintf("$%.4f", dollars)
	}
	if dollars < 1 {
		return fmt.Sprintf("$%.3f", dollars)
	}
	return fmt.Sprintf("$%.2f", dollars)
}
