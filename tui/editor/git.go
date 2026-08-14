package editor

import (
	"fmt"
	"time"

	"crude/tui/cell"
)

// GitLineKind is the HEAD-vs-buffer status of one editor line.
type GitLineKind uint8

const (
	GitLineUnchanged GitLineKind = iota
	GitLineAdded
	GitLineModified
)

// GitBlameLine is blame metadata for one 1-based source line.
type GitBlameLine struct {
	Line        int
	Hash        string
	ShortHash   string
	Author      string
	When        time.Time
	Subject     string
	Uncommitted bool
}

// SetGitAnnotation replaces the gutter Git projection. Passing nil clears it.
func (e *Editor) SetGitAnnotation(edits []GitLineKind, blame []GitBlameLine) {
	if e == nil {
		return
	}
	e.gitEdits = edits
	e.gitBlame = blame
	e.gitLoaded = true
}

func (e *Editor) gitKindAt(line int) GitLineKind {
	if e == nil || line < 0 || line >= len(e.gitEdits) {
		return GitLineUnchanged
	}
	return e.gitEdits[line]
}

// BlameAtLine returns blame for a 0-based editor line.
func (e *Editor) BlameAtLine(line int) (GitBlameLine, bool) {
	if e == nil {
		return GitBlameLine{}, false
	}
	want := line + 1
	for _, b := range e.gitBlame {
		if b.Line == want {
			return b, true
		}
	}
	return GitBlameLine{}, false
}

// CursorBlameSummary is the compact status-bar blame for the cursor line.
func (e *Editor) CursorBlameSummary() string {
	if e == nil {
		return ""
	}
	b, ok := e.BlameAtLine(e.cursor.Line)
	if !ok {
		return ""
	}
	if b.Uncommitted {
		return "Not committed yet"
	}
	if b.Author == "" {
		return ""
	}
	when := relativeTime(b.When)
	if b.ShortHash == "" {
		return fmt.Sprintf("%s  %s", b.Author, when)
	}
	if when == "" {
		return fmt.Sprintf("%s  %s", b.Author, b.ShortHash)
	}
	return fmt.Sprintf("%s  %s  %s", b.Author, when, b.ShortHash)
}

func (e *Editor) gitGutterCell(kind GitLineKind) (rune, cell.Color) {
	if e == nil || e.th == nil {
		return ' ', cell.NoColor
	}
	bg := e.cellColor(e.th.Colors.Bg)
	switch kind {
	case GitLineAdded:
		return '+', e.cellColor(e.th.Tree.Colors.GitAdded)
	case GitLineModified:
		return '~', e.cellColor(e.th.Tree.Colors.GitModified)
	default:
		return ' ', bg
	}
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
