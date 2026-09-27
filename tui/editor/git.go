package editor

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"crude/textdiff"
	"crude/tui/cell"
)

type GitLineKind uint8

const (
	GitLineUnchanged GitLineKind = iota
	GitLineAdded
	GitLineModified
	GitLineConflict
)

type GitBlameLine struct {
	Line                    int
	Hash, ShortHash, Author string
	When                    time.Time
	Subject                 string
	Uncommitted             bool
}

type GitHunk = textdiff.Hunk

// SetGitAnnotation publishes an immutable HEAD baseline and revision-bound blame.
// Background refreshes of the same HEAD never replace the live hunk projection.
func (e *Editor) SetGitAnnotation(sourceRevision uint64, head string, hunks []GitHunk, blame []GitBlameLine, conflicted bool) {
	if e == nil || e.buf == nil {
		return
	}
	e.gitBlame = blame
	e.gitBlameHunks = append([]GitHunk(nil), hunks...)
	e.gitConflicted = conflicted
	if e.gitLoaded && e.gitHead == head {
		e.remapGitProjection()
		return
	}
	e.buf.ensureCache()
	e.gitHead = head
	e.gitHeadLines = textdiff.Lines(head)
	e.gitLoaded = true
	if sourceRevision == e.Revision() {
		e.gitHunks = append([]GitHunk(nil), hunks...)
	} else {
		e.gitHunks = textdiff.Hunks(head, string(e.buf.cachedBytes))
	}
	e.gitSnapshot = e.buf.snapshotText()
	e.gitLineOffsets = e.buf.lineOffsets
	e.gitRevision = e.Revision()
}

func (e *Editor) ClearGitAnnotation() {
	e.gitHead = ""
	e.gitHeadLines = nil
	e.gitHunks = nil
	e.gitBlame = nil
	e.gitBlameHunks = nil
	e.gitSnapshot = textSnapshot{}
	e.gitLineOffsets = nil
	e.gitLoaded = false
	e.gitConflicted = false
	e.gitRevision = 0
}

// remapGitProjection re-diffs only the exact changed line window plus touching
// hunks and one unchanged anchor on either side. Immutable piece spans locate
// the window without scanning the document or replaying a bounded edit log.
// Paint, inspection and navigation consume this same truthful projection.
func (e *Editor) remapGitProjection() {
	if e == nil || !e.gitLoaded || e.gitRevision == e.Revision() {
		return
	}
	e.buf.ensureCache()
	current := e.buf.snapshotText()
	prefix, suffix := e.gitSnapshot.matchingEdges(current)
	defer func() { e.gitSnapshot = current; e.gitLineOffsets = e.buf.lineOffsets; e.gitRevision = e.Revision() }()
	if prefix == current.size && prefix == e.gitSnapshot.size {
		return
	}
	oldOffsets, newOffsets := e.gitLineOffsets, e.buf.lineOffsets
	lineAt := func(offsets []int, offset int) int {
		return max(0, sort.Search(len(offsets), func(i int) bool { return offsets[i] > offset })-1)
	}
	lineCount := func(offsets []int, size int) int {
		count := len(offsets)
		if count > 0 && offsets[count-1] == size {
			count--
		}
		return count
	}
	oldCount, newCount := lineCount(oldOffsets, e.gitSnapshot.size), lineCount(newOffsets, current.size)
	from := max(0, min(oldCount, lineAt(oldOffsets, prefix))-1)
	to := min(oldCount, lineAt(oldOffsets, e.gitSnapshot.size-suffix)+2)
	first, deltaBefore := 0, 0
	for first < len(e.gitHunks) && e.gitHunks[first].NewStart+len(e.gitHunks[first].NewLines) < from {
		h := e.gitHunks[first]
		deltaBefore += len(h.OldLines) - len(h.NewLines)
		first++
	}
	last := first
	for last < len(e.gitHunks) && e.gitHunks[last].NewStart <= to {
		h := e.gitHunks[last]
		from = min(from, h.NewStart)
		to = max(to, h.NewStart+len(h.NewLines))
		last++
	}
	for first > 0 && e.gitHunks[first-1].NewStart+len(e.gitHunks[first-1].NewLines) >= from {
		first--
		h := e.gitHunks[first]
		deltaBefore -= len(h.OldLines) - len(h.NewLines)
		from = min(from, h.NewStart)
	}
	headFrom, headTo := from+deltaBefore, to+deltaBefore
	for i := first; i < last; i++ {
		headTo += len(e.gitHunks[i].OldLines) - len(e.gitHunks[i].NewLines)
	}
	delta := newCount - oldCount
	newTo := to + delta
	if headFrom < 0 || headTo < headFrom || headTo > len(e.gitHeadLines) || from < 0 || newTo < from || newTo > newCount {
		// An externally supplied annotation cannot establish this range. Rebuild
		// from the authoritative baseline rather than inventing a local marker.
		e.gitHunks = textdiff.Hunks(e.gitHead, string(e.buf.cachedBytes))
		return
	}
	byteAt := func(line int) int {
		if line >= len(newOffsets) {
			return current.size
		}
		return newOffsets[line]
	}
	changed := textdiff.Hunks(strings.Join(e.gitHeadLines[headFrom:headTo], ""), string(e.buf.cachedBytes[byteAt(from):byteAt(newTo)]))
	next := make([]GitHunk, 0, first+len(changed)+len(e.gitHunks)-last)
	next = append(next, e.gitHunks[:first]...)
	for _, h := range changed {
		h.OldStart += headFrom
		h.NewStart += from
		next = append(next, h)
	}
	for _, h := range e.gitHunks[last:] {
		h.NewStart += delta
		next = append(next, h)
	}
	e.gitHunks = next
}

func (e *Editor) gitKindAt(line int) GitLineKind {
	if e == nil || !e.gitLoaded || line < 0 {
		return GitLineUnchanged
	}
	e.remapGitProjection()
	index := sort.Search(len(e.gitHunks), func(i int) bool { return e.gitHunks[i].NewStart > line }) - 1
	if index >= 0 {
		h := e.gitHunks[index]
		offset := line - h.NewStart
		if offset >= 0 && offset < len(h.NewLines) {
			if e.gitConflicted {
				return GitLineConflict
			}
			if offset < len(h.OldLines) {
				return GitLineModified
			}
			return GitLineAdded
		}
	}
	return GitLineUnchanged
}

func (e *Editor) gitDeletionMarkerAt(line, lineCount int) rune {
	e.remapGitProjection()
	if !e.gitLoaded {
		return ' '
	}
	for _, h := range e.gitHunks {
		if len(h.OldLines) <= len(h.NewLines) {
			continue
		}
		boundary := h.NewStart + len(h.NewLines)
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

func (e *Editor) GitHunkAtLine(line int) (GitHunk, int, bool) {
	if e == nil {
		return GitHunk{}, -1, false
	}
	e.remapGitProjection()
	for index, h := range e.gitHunks {
		end := h.NewStart + len(h.NewLines)
		if line >= h.NewStart && line < end {
			return h, index, true
		}
		if len(h.OldLines) > len(h.NewLines) && (line == end || (end >= e.LineCount() && line == e.LineCount()-1)) {
			return h, index, true
		}
	}
	return GitHunk{}, -1, false
}
func (e *Editor) GitHunks() []GitHunk {
	if e == nil {
		return nil
	}
	e.remapGitProjection()
	return e.gitHunks
}

func (e *Editor) NextChange(line, direction int) (int, bool) {
	hunks := e.GitHunks()
	if len(hunks) == 0 {
		return 0, false
	}
	if direction > 0 {
		for _, h := range hunks {
			if h.NewStart > line {
				return min(h.NewStart, e.LineCount()-1), true
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

// Map unchanged live rows through HEAD into the blame snapshot. Edits never
// shift an entire per-line blame array on the UI thread.
func (e *Editor) BlameAtLine(line int) (GitBlameLine, bool) {
	if e == nil || line < 0 {
		return GitBlameLine{}, false
	}
	if e.gitKindAt(line) != GitLineUnchanged {
		return GitBlameLine{Line: line + 1, Uncommitted: true}, true
	}
	headLine := line
	index := sort.Search(len(e.gitHunks), func(i int) bool { return e.gitHunks[i].NewStart > line }) - 1
	if index >= 0 {
		h := e.gitHunks[index]
		headLine = h.OldStart + len(h.OldLines) + line - h.NewStart - len(h.NewLines)
	}
	sourceLine := headLine
	index = sort.Search(len(e.gitBlameHunks), func(i int) bool { return e.gitBlameHunks[i].OldStart > headLine }) - 1
	if index >= 0 {
		h := e.gitBlameHunks[index]
		if headLine < h.OldStart+len(h.OldLines) {
			return GitBlameLine{}, false
		}
		sourceLine = h.NewStart + len(h.NewLines) + headLine - h.OldStart - len(h.OldLines)
	}
	index = sort.Search(len(e.gitBlame), func(i int) bool { return e.gitBlame[i].Line >= sourceLine+1 })
	if index < len(e.gitBlame) && e.gitBlame[index].Line == sourceLine+1 {
		b := e.gitBlame[index]
		b.Line = line + 1
		return b, true
	}
	return GitBlameLine{}, false
}

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
	return fmt.Sprintf("%s  %s  %s", b.Author, b.ShortHash, when)
}

func (e *Editor) gitGutterCell(kind GitLineKind) (rune, cell.Color) {
	if e == nil || e.th == nil {
		return ' ', cell.NoColor
	}
	switch kind {
	case GitLineAdded:
		return '▌', e.cellColor(e.th.Tree.Colors.GitAdded)
	case GitLineModified:
		return '▌', e.cellColor(e.th.Tree.Colors.GitModified)
	case GitLineConflict:
		return '▌', e.cellColor(e.th.Tree.Colors.GitConflicted)
	default:
		return ' ', e.cellColor(e.th.Colors.Bg)
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
