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

// Hunks compares exact bytes, line terminators included. Merges and edit
// application use it; change presentation uses ContentHunks or ReviewHunks.
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

// ContentHunks matches lines by content, treating CRLF and LF terminators as
// the same line ending. Git stores text in repository form and checks it out
// in working-tree form (core.autocrlf, eol attributes), so the two sides of a
// Git comparison legitimately disagree on terminators; the line-ending policy
// itself is Git's, reported through status. Hunk lines keep their exact bytes.
func ContentHunks(before, after string) []Hunk {
	normalBefore, normalAfter := normalizeLineEndings(before), normalizeLineEndings(after)
	if normalBefore == before && normalAfter == after {
		return Hunks(before, after)
	}
	hunks := Hunks(normalBefore, normalAfter)
	if len(hunks) == 0 {
		return nil
	}
	// CRLF -> LF keeps every line boundary, so indices address the originals.
	oldLines, newLines := Lines(before), Lines(after)
	for i := range hunks {
		h := &hunks[i]
		oldEnd, newEnd := h.OldStart+len(h.OldLines), h.NewStart+len(h.NewLines)
		h.OldLines, h.NewLines = nil, nil
		if oldEnd > h.OldStart {
			h.OldLines = oldLines[h.OldStart:oldEnd:oldEnd]
		}
		if newEnd > h.NewStart {
			h.NewLines = newLines[h.NewStart:newEnd:newEnd]
		}
	}
	return hunks
}

// ReviewHunks is ContentHunks for a comparison already known to differ: when
// the only difference is line endings, it reports those exact lines rather
// than an empty diff, so a real terminator change stays reviewable.
func ReviewHunks(before, after string) []Hunk {
	if hunks := ContentHunks(before, after); len(hunks) != 0 || before == after {
		return hunks
	}
	return Hunks(before, after)
}

func normalizeLineEndings(text string) string {
	if !strings.Contains(text, "\r\n") {
		return text
	}
	return strings.ReplaceAll(text, "\r\n", "\n")
}
