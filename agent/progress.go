package agent

import (
	"strings"
)

func CountLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

type ToolProgress struct {
	Phase        string
	Message      string
	Path         string
	LinesRead    int
	LinesWritten int
	LinesAdded   int
	LinesRemoved int
}

func NewToolProgress(phase, message, path string) ToolProgress {
	return ToolProgress{
		Phase:   phase,
		Message: message,
		Path:    path,
	}
}
