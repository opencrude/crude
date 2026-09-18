package editor

import (
	"fmt"
	"sort"
	"time"

	"crude/tui/cell"
)

// GitLineKind is the HEAD-vs-buffer status of one editor line.
type GitLineKind uint8

const (
	GitLineUnchanged GitLineKind = iota
	GitLineAdded
	GitLineModified
	GitLineConflict
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

// GitHunk is one exact HEAD-to-buffer change using zero-based line positions.
type GitHunk struct {
	OldStart int
	NewStart int
	OldLines []string
	NewLines []string
}

// gitProvisionalMark is a paint-only claim about lines the user touched since the
// last authoritative annotation: the region is certainly changed (it was just
// edited), so the rail can say so at once instead of waiting for the debounced
// Git refresh. It carries no old text, so it is deliberately invisible to
// inspection and navigation — those need the authoritative hunk.
type gitProvisionalMark struct {
	start int // zero-based first covered line
	lines int // covered line count, at least 1
	// added reports that the whole range lies past the end of the annotated
	// document, so it reads as an appended line rather than an edited one.
	added bool
}

// SetGitAnnotation installs a projection that was computed from the buffer at
// sourceRevision. Edits that landed while the annotation was in flight are
// mapped forward here, so the arriving projection neither blanks the gutter nor
// fights the live edits. Callers that already hold the live buffer pass
// Revision(). Passing no hunks clears the projection.
func (e *Editor) SetGitAnnotation(sourceRevision uint64, hunks []GitHunk, blame []GitBlameLine, conflicted bool) {
	if e == nil {
		return
	}
	e.gitHunks = hunks
	e.gitBlame = blame
	e.gitConflicted = conflicted
	e.gitLoaded = true
	e.gitRevision = sourceRevision
	// The arriving projection is authoritative: whatever the local provisional
	// marks claimed is now either confirmed or resolved away by it, so they are
	// replaced rather than kept alongside.
	e.gitProvisional = e.gitProvisional[:0]
	e.remapGitProjection()
}

// remapGitProjection keeps the projection continuous across edits. An edit
// inside a hunk keeps that hunk in place — the region is still changed relative
// to HEAD — and only the hunks after the edit shift by its line delta. Unknown
// full replacements invalidate the projection.
//
// This runs on every text change. It is the display-continuity half of the
// projection: the asynchronous Git annotation remains the only producer of
// complete truth, while a touched line is marked locally right away so the rail
// answers the keystroke instead of waiting for the debounced refresh. Dropping a
// touched hunk or waiting for Git is what made gutter markers blink or lag.
func (e *Editor) remapGitProjection() {
	if !e.gitLoaded || e.gitRevision == e.Revision() {
		return
	}
	for revision := e.gitRevision + 1; revision <= e.Revision(); revision++ {
		change := e.buf.lineChanges[revision%uint64(len(e.buf.lineChanges))]
		if change.revision != revision {
			e.gitHunks = nil
			e.gitBlame = nil
			e.gitProvisional = nil
			break
		}
		e.shiftGitProjection(change)
		e.noteProvisionalEdit(change)
	}
	e.gitRevision = e.Revision()
}

// shiftGitProjection moves everything that sits below one edit.
func (e *Editor) shiftGitProjection(change bufferLineChange) {
	for i := range e.gitHunks {
		if e.gitHunks[i].NewStart > change.end {
			e.gitHunks[i].NewStart += change.delta
		}
	}
	for i := range e.gitProvisional {
		mark := &e.gitProvisional[i]
		if mark.start > change.end {
			mark.start += change.delta
		}
	}
	// Blame is stored 1-based and follows the same shift rule, so a keystroke
	// does not make the status-bar blame disappear.
	for i := range e.gitBlame {
		if e.gitBlame[i].Line-1 > change.end {
			e.gitBlame[i].Line += change.delta
		}
	}
}

// noteProvisionalEdit claims the lines one edit touched, minus whatever the
// authoritative projection already covers, so the marker is on screen at the
// first keystroke and never duplicated.
//
// The flavour is structural, not a diff: the line the edit started on is an
// edit, and the lines it created with newlines are additions. The authoritative
// refresh refines that into real HEAD-relative hunks; until then the rail shows
// what the keystroke itself proves.
func (e *Editor) noteProvisionalEdit(change bufferLineChange) {
	if e.gitConflicted {
		// A conflicted file already paints every line as conflicted.
		return
	}
	lineCount := e.buf.LineCount()
	start := max(0, change.start)
	if start <= lineCount-1 {
		// Editing a line that is itself only a local addition keeps the added
		// flavour: the whole region reads as new text.
		e.addProvisionalMark(start, 1, e.locallyAddedAt(start))
	}
	if change.delta > 0 {
		addedFrom := start + 1
		addedLines := min(change.delta, lineCount-addedFrom)
		if addedLines > 0 {
			e.addProvisionalMark(addedFrom, addedLines, true)
		}
	}
}

// locallyAddedAt reports whether a line is already claimed as a local addition.
func (e *Editor) locallyAddedAt(line int) bool {
	for _, mark := range e.gitProvisional {
		if mark.added && line >= mark.start && line < mark.start+mark.lines {
			return true
		}
	}
	return false
}

// addProvisionalMark claims a line range, minus the authoritative coverage:
// hunk-owned lines already carry a marker, and a second claim over them is what
// turns a rail into a stutter.
func (e *Editor) addProvisionalMark(start, lines int, added bool) {
	if lines <= 0 {
		return
	}
	gaps := [][2]int{{start, start + lines - 1}}
	for _, hunk := range e.gitHunks {
		hunkStart := hunk.NewStart
		hunkEnd := hunk.NewStart + max(1, len(hunk.NewLines)) - 1
		kept := gaps[:0]
		for _, gap := range gaps {
			if hunkEnd < gap[0] || hunkStart > gap[1] {
				kept = append(kept, gap)
				continue
			}
			if hunkStart > gap[0] {
				kept = append(kept, [2]int{gap[0], hunkStart - 1})
			}
			if hunkEnd < gap[1] {
				kept = append(kept, [2]int{hunkEnd + 1, gap[1]})
			}
		}
		gaps = kept
		if len(gaps) == 0 {
			return
		}
	}
	for _, gap := range gaps {
		e.unionProvisionalRange(gap[0], gap[1]-gap[0]+1, added)
	}
}

// unionProvisionalRange merges a range into the claims of the same flavour, so
// typing line by line grows one continuous mark instead of stacking claims.
func (e *Editor) unionProvisionalRange(start, lines int, added bool) {
	if lines <= 0 {
		return
	}
	for i := range e.gitProvisional {
		mark := &e.gitProvisional[i]
		if mark.added != added {
			continue
		}
		if start > mark.start+mark.lines || mark.start > start+lines {
			continue
		}
		mark.start = min(mark.start, start)
		mark.lines = max(mark.start+mark.lines, start+lines) - mark.start
		return
	}
	e.gitProvisional = append(e.gitProvisional, gitProvisionalMark{start: start, lines: lines, added: added})
}

func (e *Editor) provisionalKindAt(line int) GitLineKind {
	for _, mark := range e.gitProvisional {
		if line < mark.start || line >= mark.start+mark.lines {
			continue
		}
		if mark.added {
			return GitLineAdded
		}
		return GitLineModified
	}
	return GitLineUnchanged
}

func (e *Editor) gitKindAt(line int) GitLineKind {
	if e == nil || e.gitRevision != e.Revision() || line < 0 {
		return GitLineUnchanged
	}
	index := sort.Search(len(e.gitHunks), func(i int) bool { return e.gitHunks[i].NewStart > line }) - 1
	if index >= 0 {
		hunk := e.gitHunks[index]
		offset := line - hunk.NewStart
		if offset >= 0 && offset < len(hunk.NewLines) {
			if e.gitConflicted {
				return GitLineConflict
			}
			if offset < len(hunk.OldLines) {
				return GitLineModified
			}
			return GitLineAdded
		}
	}
	// No authoritative hunk owns the line, but the local edit claim does: the
	// marker appears with the keystroke instead of after the Git round trip.
	return e.provisionalKindAt(line)
}

func (e *Editor) gitDeletionMarkerAt(line, lineCount int) rune {
	if e.gitRevision != e.Revision() {
		return ' '
	}
	for _, hunk := range e.gitHunks {
		if len(hunk.OldLines) <= len(hunk.NewLines) {
			continue
		}
		boundary := hunk.NewStart + len(hunk.NewLines)
		if boundary >= lineCount {
			if lineCount > 0 && line == lineCount-1 {
				return '▁'
			}
			continue
		}
		if line == boundary {
			return '▔'
		}
	}
	return ' '
}

// GitHunkAtLine returns the change owning a current-buffer line or deletion
// boundary. The returned index is stable only for the current annotation.
func (e *Editor) GitHunkAtLine(line int) (GitHunk, int, bool) {
	if e == nil || e.gitRevision != e.Revision() {
		return GitHunk{}, -1, false
	}
	for index, hunk := range e.gitHunks {
		newEnd := hunk.NewStart + len(hunk.NewLines)
		if line >= hunk.NewStart && line < newEnd {
			return hunk, index, true
		}
		if len(hunk.OldLines) > len(hunk.NewLines) {
			boundary := newEnd
			if line == boundary || (boundary >= e.LineCount() && line == e.LineCount()-1) {
				return hunk, index, true
			}
		}
	}
	return GitHunk{}, -1, false
}

func (e *Editor) GitHunks() []GitHunk {
	if e.gitRevision != e.Revision() {
		return nil
	}
	return e.gitHunks
}

func (e *Editor) NextChange(line, direction int) (int, bool) {
	hunks := e.GitHunks()
	if len(hunks) == 0 {
		return 0, false
	}
	if direction > 0 {
		for _, hunk := range hunks {
			if hunk.NewStart > line {
				return min(hunk.NewStart, e.LineCount()-1), true
			}
		}
		return min(hunks[0].NewStart, e.LineCount()-1), true
	}
	for i := len(hunks) - 1; i >= 0; i-- {
		if hunks[i].NewStart < line {
			return min(hunks[i].NewStart, e.LineCount()-1), true
		}
	}
	return min(hunks[len(hunks)-1].NewStart, e.LineCount()-1), true
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
		return '▌', e.cellColor(e.th.Tree.Colors.GitAdded)
	case GitLineModified:
		return '▌', e.cellColor(e.th.Tree.Colors.GitModified)
	case GitLineConflict:
		return '▌', e.cellColor(e.th.Tree.Colors.GitConflicted)
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
