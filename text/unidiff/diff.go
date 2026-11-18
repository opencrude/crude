package diff

import (
	"strings"
	"sync"
	"time"

	"crude/logger"

	"github.com/aymanbagabas/go-udiff"
)

var (
	diffLogger   *logger.Logger
	diffLoggerMu sync.RWMutex
)

func SetLogger(lg *logger.Logger) {
	diffLoggerMu.Lock()
	defer diffLoggerMu.Unlock()
	diffLogger = lg
}

func diffLogIf(fn func(lg *logger.Logger)) {
	diffLoggerMu.RLock()
	lg := diffLogger
	diffLoggerMu.RUnlock()
	if lg != nil {
		fn(lg)
	}
}

func GenerateDiff(beforeContent, afterContent, fileName string) (string, int, int) {
	start := time.Now()
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

	diffLogIf(func(lg *logger.Logger) {
		lg.Debug("DIFF", "generate", "file", fileName, "additions", additions, "removals", removals, "diff_len", len(unified), "latency_ms", time.Since(start).Milliseconds())
	})
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
