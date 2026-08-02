package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

type stopCondition func(steps []StepRecord) bool

func isStopConditionMet(conditions []stopCondition, steps []StepRecord) bool {
	for _, cond := range conditions {
		if cond(steps) {
			return true
		}
	}
	return false
}

func hasRepeatedToolCalls(steps []StepRecord, windowSize, maxRepeats int) bool {
	if len(steps) < windowSize {
		return false
	}
	window := steps[len(steps)-windowSize:]
	sigCount := make(map[string]int)
	for _, s := range window {
		if s.ToolName == "" {
			continue
		}
		h := sha256.New()
		_, _ = io.WriteString(h, s.ToolName)
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, s.ToolInput)
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, s.ToolResult)
		_, _ = io.WriteString(h, "\x00")
		sig := hex.EncodeToString(h.Sum(nil))
		sigCount[sig]++
		if sigCount[sig] > maxRepeats {
			return true
		}
	}
	return false
}
