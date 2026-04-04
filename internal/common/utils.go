package common

import (
	"strings"
)

// ReplacePlaceholders replaces all occurrences of placeholders with their values.
func ReplacePlaceholders(text string, placeholders map[string]string) string {
	for p, v := range placeholders {
		text = strings.ReplaceAll(text, p, v)
	}
	return text
}

// EstimateTokenCount estimates the number of tokens in text.
// Uses a simple approximation: 1 token ≈ 3 words.
func EstimateTokenCount(text string) int {
	if text == "" {
		return 0
	}
	words := len(strings.Fields(text))
	if words == 0 {
		return 0
	}
	return max(1, words/3)
}
