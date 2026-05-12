package rendering

import "strings"

func IsDiffContent(text string) bool {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "```diff") {
		return true
	}
	hasMinus := false
	hasPlus := false
	hasHunk := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "--- ") {
			hasMinus = true
		}
		if strings.HasPrefix(line, "+++ ") {
			hasPlus = true
		}
		if strings.HasPrefix(line, "@@") {
			hasHunk = true
		}
	}
	return (hasMinus && hasPlus) || hasHunk
}
