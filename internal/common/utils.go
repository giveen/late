package common

import "strings"

// ReplacePlaceholders replaces all occurrences of placeholders with their values.
func ReplacePlaceholders(text string, placeholders map[string]string) string {
	for p, v := range placeholders {
		text = strings.ReplaceAll(text, p, v)
	}
	return text
}
