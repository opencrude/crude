package editor

import (
	"crude/textdiff"
	"strings"
)

func setEditorAnnotationForTest(ed *Editor, revision uint64, hunks []GitHunk, blame []GitBlameLine, conflicted bool) {
	lines := textdiff.Lines(ed.Value())
	for i := len(hunks) - 1; i >= 0; i-- {
		h := hunks[i]
		start := min(h.NewStart, len(lines))
		end := min(start+len(h.NewLines), len(lines))
		next := append([]string(nil), lines[:start]...)
		next = append(next, h.OldLines...)
		lines = append(next, lines[end:]...)
	}
	ed.SetGitAnnotation(revision, strings.Join(lines, ""), hunks, blame, conflicted)
}
