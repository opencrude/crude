// Package textdiff derives exact line changes from immutable text, without I/O.
package textdiff

import (
	"github.com/aymanbagabas/go-udiff"
	"strings"
)

type Hunk struct {
	OldStart, NewStart int
	OldLines, NewLines []string
}

func Lines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func Hunks(before, after string) []Hunk {
	if before == after {
		return nil
	}
	if before == "" {
		return []Hunk{{NewLines: Lines(after)}}
	}
	unified, err := udiff.ToUnifiedDiff("a", "b", before, udiff.Lines(before, after), 0)
	if err != nil {
		return nil
	}
	hunks := make([]Hunk, 0, len(unified.Hunks))
	for _, source := range unified.Hunks {
		hunk := Hunk{OldStart: max(0, source.FromLine-1), NewStart: max(0, source.ToLine-1)}
		for _, line := range source.Lines {
			switch line.Kind {
			case udiff.Delete:
				hunk.OldLines = append(hunk.OldLines, line.Content)
			case udiff.Insert:
				hunk.NewLines = append(hunk.NewLines, line.Content)
			}
		}
		hunks = append(hunks, hunk)
	}
	return hunks
}
