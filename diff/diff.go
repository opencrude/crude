package diff

import (
	"strings"

	"github.com/aymanbagabas/go-udiff"
)

func GenerateDiff(beforeContent, afterContent, fileName string) (string, int, int) {
	fileName = strings.TrimPrefix(fileName, "/")

	var (
		unified   = udiff.Unified("a/"+fileName, "b/"+fileName, beforeContent, afterContent)
		additions = 0
		removals  = 0
	)

	for line := range strings.SplitSeq(unified, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			additions++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			removals++
		}
	}

	return unified, additions, removals
}

// IsEmptyDiff returns true if the unified diff contains no actual changes.
func IsEmptyDiff(unified string) bool {
	for line := range strings.SplitSeq(unified, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			return false
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			return false
		}
	}
	return true
}
