package util

import (
	"encoding/json"
	"net/http"
	"strings"
)

func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\u2026[truncated]"
}

func StripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) == 2 {
			s = lines[1]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

func IsRetryable(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func RetryBackoffSeconds(attempt int) int {
	if attempt <= 1 {
		return 1
	}
	return 1 << (attempt - 1)
}

func MustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
