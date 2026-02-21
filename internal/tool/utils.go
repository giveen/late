package tool

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// getToolParam extracts a string parameter from tool arguments
func getToolParam(args json.RawMessage, key string) string {
	var params map[string]any
	if err := json.Unmarshal(args, &params); err != nil {
		// Fallback for partial JSON during streaming where the unmarshal fails
		re := regexp.MustCompile(fmt.Sprintf(`"%s"\s*:\s*"([^"]*)`, regexp.QuoteMeta(key)))
		matches := re.FindStringSubmatch(string(args))
		if len(matches) > 1 {
			return matches[1]
		}
		return ""
	}
	val, ok := params[key].(string)
	if !ok {
		return ""
	}
	return val
}

// truncate shortens a string to maxLen characters, adding "..." if truncated
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
