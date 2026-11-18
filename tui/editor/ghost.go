package editor

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/types"
)

// Ghost text is a private display projection, never source or provider authority.
// The coordinator must qualify the producing result before offering it here.
// One ghost shows one suggested change of the exact local buffer revision:
//
//   - an insertion at the caret: the first line as faded text after the caret
//     and the following lines as faded rows below it;
//   - a replacement near the caret: the replaced text struck through and the
//     new text faded after it;
//   - a marker: a display-only label (the next place to edit) with no text.
type editorGhost struct {
	buffer   *PieceTable
	document *DocumentProjection
	revision uint64
	caret    Position
	caretAt  int    // byte offset of the caret
	anchor   int    // byte offset where the label shows and Text is inserted
	start    int    // byte offset where the replaced text starts (== anchor for an insertion)
	text     string // the complete new text
	label    string // display of the first line, or the marker
	rows     []string
	more     int // lines of text that the rows do not show
	marker   bool
	jumpTo   int // marker: byte offset the first Tab moves the caret to
	atLine   int // line of anchor
	owner    string
	base     *editorInlineLine
	line     *editorInlineLine
}

type ghostEdit struct {
	offer          *editorGhost
	next, consumed int
	valid, edited  bool
}

// Suggestion is one change for SetSuggestion: replace source bytes
// [Start, End) with Text. A Marker suggestion has no text: it shows the label
// at End and the first Tab moves the caret to JumpTo.
type Suggestion struct {
	Start, End int
	Text       string
	Marker     string
	JumpTo     int
}

// SetGhostText prepares an exact local buffer revision and byte anchor at the
// current insertion caret. It changes no source, canonical command or savepoint.
// Failed preparation is atomic. Invalid/control-bearing/multiline output is
// refused rather than trimmed or transformed into different source code.
func (e *Editor) SetGhostText(revision uint64, anchor int, text string) error {
	return e.SetGhostTextFor("", revision, anchor, text)
}

// SetGhostTextFor is SetGhostText with a producer name that GhostOwner reports,
// so a producer can tell its own candidate from another producer's. A candidate
// may sit at the caret of an active snippet field: accepting it is an ordinary
// edit of that field, and the next Tab moves to the next field.
func (e *Editor) SetGhostTextFor(owner string, revision uint64, anchor int, text string) error {
	if !types.ValidEditorPredictionInsertion(text) {
		return errors.New("prediction does not identify a usable insertion caret")
	}
	return e.SetSuggestion(owner, revision, Suggestion{Start: anchor, End: anchor, Text: text})
}

// SetSuggestion shows one suggested change of the exact local revision. The
// caret must be a single empty caret; an insertion must be at the caret.
func (e *Editor) SetSuggestion(owner string, revision uint64, s Suggestion) error {
	if !e.open || e.readOnly || e.buf == nil || e.Revision() != revision ||
		e.HasMultipleCarets() || e.HasSelection() || e.IsMouseDragging() {
		return errors.New("prediction does not identify a usable insertion caret")
	}
	if p := e.documentProjection; p != nil && (p.conflict != nil || p.snapshot.Conflict ||
		p.undoPending || p.historySync || len(p.historyQueue) != 0) {
		return errors.New("prediction cannot cross pending history or recovery")
	}
	caretStart, caretEnd, ok := e.SourceByteRange()
	size := e.buf.positionToOffset(Position{Line: e.buf.LineCount()})
	if !ok || caretStart != caretEnd || s.Start < 0 || s.End < s.Start || s.End > size {
		return errors.New("prediction anchor is not this exact source boundary")
	}
	if e.rangeFolded(s.Start, s.End) {
		return errors.New("prediction is inside a folded region")
	}
	g := &editorGhost{buffer: e.buf, document: e.documentProjection, revision: revision, caret: e.cursor,
		caretAt: caretStart, anchor: s.End, start: s.Start, text: s.Text, owner: owner}
	switch {
	case s.Marker != "":
		if s.Text != "" || s.Start != s.End || s.JumpTo < 0 || s.JumpTo > size || !types.ValidEditorPredictionInsertion(s.Marker) {
			return errors.New("prediction marker must be a label without text")
		}
		g.label, g.marker, g.jumpTo = s.Marker, true, s.JumpTo
	case s.Start == s.End:
		if s.Start != caretStart || s.Text == "" || !types.ValidEditorPredictionText(s.Text) {
			return errors.New("prediction anchor is not this exact source boundary")
		}
	default:
		if !types.ValidEditorPredictionText(s.Text) {
			return errors.New("prediction replacement is not literal text")
		}
	}
	if !g.marker {
		g.label, g.rows, g.more = ghostDisplay(s.Text, e.TabWidth())
	}
	g.atLine = e.buf.positionAtOffset(g.anchor).Line
	var base *editorInlineLine
	if hints := e.currentInlays(); hints != nil {
		base = hints.lines[g.atLine]
	}
	var line *editorInlineLine
	if g.label != "" {
		var err error
		if line, err = e.prepareGhostLine(g, base); err != nil {
			return err
		}
	}
	visible := e.inlayCaretVisible()
	e.DismissGhostText()
	g.base, g.line = base, line
	e.ghost = g
	if g.buffer.interactionViews == nil {
		g.buffer.interactionViews = make(map[*Editor]struct{})
	}
	g.buffer.interactionViews[e] = struct{}{}
	e.clearGoal()
	e.pendingReveal = e.pendingReveal || visible
	return nil
}

// rangeFolded reports whether any line of [start, end] is hidden by a fold:
// a change the user cannot see must not be shown or applied.
func (e *Editor) rangeFolded(start, end int) bool {
	first, last := e.buf.positionAtOffset(start).Line, e.buf.positionAtOffset(end).Line
	for line := first; line <= last; line++ {
		if _, hidden := e.hiddenFold(line); hidden {
			return true
		}
	}
	return false
}

// ghostDisplay splits text into the inline label (first line) and the rows
// below it. Tabs show as spaces; the text itself is never changed.
func ghostDisplay(text string, tabWidth int) (string, []string, int) {
	lines := strings.Split(text, "\n")
	expand := func(line string) string {
		if !strings.Contains(line, "\t") {
			return line
		}
		return strings.ReplaceAll(line, "\t", strings.Repeat(" ", max(1, tabWidth)))
	}
	label := expand(lines[0])
	var rows []string
	for _, line := range lines[1:] {
		if len(rows) == types.EditorPredictionShownLines {
			break
		}
		rows = append(rows, expand(line))
	}
	return label, rows, max(0, len(lines)-1-len(rows))
}

// GhostText returns immutable private presentation metadata, not edit authority.
func (e *Editor) GhostText() (int, string, bool) {
	if g := e.currentGhost(); g != nil {
		return g.anchor, g.text, true
	}
	return 0, "", false
}

// GhostSuggestion returns the shown suggestion, if any.
func (e *Editor) GhostSuggestion() (Suggestion, bool) {
	if g := e.currentGhost(); g != nil {
		s := Suggestion{Start: g.start, End: g.anchor, Text: g.text}
		if g.marker {
			s.Marker, s.JumpTo = g.label, g.jumpTo
		}
		return s, true
	}
	return Suggestion{}, false
}

// SourceLineBounds returns the 0-based line that holds a source byte offset
// and the byte range of that line without its line break.
func (e *Editor) SourceLineBounds(offset int) (line, start, end int) {
	if e.buf == nil {
		return 0, 0, 0
	}
	pos := e.buf.positionAtOffset(max(0, offset))
	raw, err := e.buf.lineView(pos.Line)
	if err != nil {
		return pos.Line, 0, 0
	}
	start = e.buf.lineOffsets[pos.Line]
	return pos.Line, start, start + len(raw)
}

// HasLocalEdits reports edits made in this editor since it opened: its own
// undo history for a private buffer, or edits it sent to a shared document
// (whose history the core owns).
func (e *Editor) HasLocalEdits() bool {
	if p := e.documentProjection; p != nil && p.localEdits {
		return true
	}
	return e.buf != nil && e.buf.changeIndex > 0
}

// SourceSlice returns source bytes [start, end) as text.
func (e *Editor) SourceSlice(start, end int) string {
	if e.buf == nil || start >= end {
		return ""
	}
	return e.buf.GetText(e.buf.positionAtOffset(start), e.buf.positionAtOffset(end))
}

// GhostOwner names the producer of the current candidate ("" when none).
func (e *Editor) GhostOwner() string {
	if g := e.currentGhost(); g != nil {
		return g.owner
	}
	return ""
}

// DismissGhostText retires even a temporarily suspended transaction. The shell
// uses this synchronous fence before closing a tab or ending an invocation.
func (e *Editor) DismissGhostText() bool {
	g, transaction := e.ghost, e.ghostEdit
	if g == nil && transaction == nil {
		return false
	}
	if g == nil {
		g = transaction.offer
	}
	e.ghost, e.ghostEdit = nil, nil
	if g != nil && g.buffer != nil && (!e.SnippetActive() || g.buffer != e.buf) && e.secondary == nil {
		delete(g.buffer.interactionViews, e)
	}
	return true
}

func (e *Editor) currentGhost() *editorGhost {
	g := e.ghost
	if g == nil {
		return nil
	}
	if !e.open || e.readOnly || e.buf != g.buffer || e.documentProjection != g.document ||
		e.Revision() != g.revision || e.cursor != g.caret || e.HasSelection() {
		e.DismissGhostText()
		return nil
	}
	if p := e.documentProjection; p != nil && (p.conflict != nil || p.snapshot.Conflict ||
		p.undoPending || p.historySync || len(p.historyQueue) != 0) {
		e.DismissGhostText()
		return nil
	}
	return g
}

// hasGhostRows reports a suggestion with faded rows below its line.
func (e *Editor) hasGhostRows() bool {
	g := e.currentGhost()
	return g != nil && (len(g.rows) > 0 || g.more > 0)
}

// ghostRows reports the faded rows below line, including the "+N lines" row.
func (e *Editor) ghostRows(line int) int {
	g := e.currentGhost()
	if g == nil || g.atLine != line {
		return 0
	}
	n := len(g.rows)
	if g.more > 0 {
		n++
	}
	return n
}

// ghostRowText returns the display text of one faded row below the anchor
// line and whether it is the muted "+N lines" marker.
func (e *Editor) ghostRowText(index int) (string, bool) {
	g := e.currentGhost()
	if g == nil || index < 0 {
		return "", false
	}
	if index < len(g.rows) {
		return g.rows[index], false
	}
	if g.more == 1 {
		return "+1 line", true
	}
	return "+" + strconv.Itoa(g.more) + " lines", true
}

// ghostRemoval returns the replaced source range for paint, if any.
func (e *Editor) ghostRemoval() (from, to Position, ok bool) {
	g := e.currentGhost()
	if g == nil || g.start == g.anchor {
		return Position{}, Position{}, false
	}
	return e.buf.positionAtOffset(g.start), e.buf.positionAtOffset(g.anchor), true
}

func (e *Editor) prepareGhostLine(g *editorGhost, base *editorInlineLine) (*editorInlineLine, error) {
	raw, err := e.buf.lineView(g.atLine)
	if err != nil {
		return nil, err
	}
	var hints []InlineInsertion
	if base != nil {
		hints = base.insertions
	}
	p, err := buildInlineProjectionWithGhost(e.TabWidth(), string(raw), hints, &InlineInsertion{
		ByteOffset: g.anchor - e.buf.lineOffsets[g.atLine], Index: -1, Label: g.label,
	})
	if err != nil {
		return nil, err
	}
	runs := len(p.Runs)
	if current := e.currentInlays(); current != nil {
		runs += current.runs
		if base != nil {
			runs -= len(base.Runs)
		}
	}
	if runs > inlineMaxRuns {
		return nil, errors.New("combined display-only projection exceeds run bound")
	}
	line := &editorInlineLine{inlineProjection: p, clusters: make([]string, len(p.Runs)), widths: make([]int, len(p.Runs))}
	for i, run := range p.Runs {
		line.clusters[i], line.widths[i] = run.Cluster, run.Width
	}
	return line, nil
}

func (e *Editor) ghostProjection(g *editorGhost, base *editorInlineLine) *editorInlineLine {
	if g.line != nil && g.base == base {
		return g.line
	}
	line, err := e.prepareGhostLine(g, base)
	if err != nil {
		e.DismissGhostText()
		return base
	}
	g.base, g.line = base, line
	return line
}

func (e *Editor) beginGhostEdit() {
	if g := e.currentGhost(); g != nil && g.start == g.anchor && !g.marker {
		e.ghostEdit = &ghostEdit{offer: g, next: g.anchor, valid: true}
		e.ghost = nil // never paint an old revision during the transaction
	}
}

// PieceTable's existing post-mutation dispatcher supplies actual source deltas,
// including pair edits, mirrors and peers. KeyPressMsg.Text is not the oracle.
func (e *Editor) observeGhostMutation(edit document.Edit, owner *Editor) {
	tx := e.ghostEdit
	if tx == nil {
		if e.ghost != nil {
			e.DismissGhostText()
		}
		return
	}
	if !tx.valid {
		return
	}
	if owner != e || edit.Start != tx.next || edit.End != edit.Start || edit.Text == "" ||
		!strings.HasPrefix(tx.offer.text[tx.consumed:], edit.Text) {
		tx.valid = false
		return
	}
	tx.next += len(edit.Text)
	tx.consumed += len(edit.Text)
	tx.edited = true
}

func (e *Editor) finishGhostEdit() {
	tx := e.ghostEdit
	if tx == nil {
		return
	}
	old := tx.offer
	e.DismissGhostText()
	if !tx.valid || !tx.edited || e.buf != old.buffer ||
		e.documentProjection != old.document || e.Revision() == old.revision {
		return
	}
	if tx.consumed == len(old.text) {
		if !e.ghostAccepting {
			e.ghostOutcome(old.owner, GhostTypedThrough)
		}
		return
	}
	start, end, ok := e.SourceByteRange()
	if !ok || start != tx.next || end != tx.next {
		return // auto-closing a pair can leave the caret before an inserted suffix
	}
	// A typed-through candidate can follow an exact local prefix before its
	// canonical ACK. This does not authorize a new provider request or edit.
	_ = e.SetSuggestion(old.owner, e.Revision(), Suggestion{Start: tx.next, End: tx.next, Text: old.text[tx.consumed:]})
}

func (e *Editor) handleGhostKey(key tea.KeyPressMsg) bool {
	g := e.currentGhost()
	if g == nil {
		return false // Off has no source export, allocation or added editing state
	}
	if key.Code == tea.KeyEsc && key.Mod == 0 {
		e.DismissGhostText()
		return true
	}
	all := key.Code == tea.KeyTab && key.Mod == 0
	word := key.Code == tea.KeyRight && key.Mod == tea.ModCtrl
	line := key.Code == tea.KeyEnd && key.Mod == tea.ModAlt
	if g.marker {
		if !all {
			if key.Text == "" || key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) != 0 {
				e.DismissGhostText()
			}
			return false
		}
		// The first Tab goes to the next place; its change shows there next.
		e.DismissGhostText()
		e.stopPointerSelection()
		e.landing = nil
		e.cursor = e.buf.positionAtOffset(g.jumpTo)
		e.clearGoal()
		e.ensureCursorVisible()
		e.ghostOutcome(g.owner, GhostJumped)
		return true
	}
	if all || word || line {
		if e.DeferDocumentEvent(key) {
			e.DismissGhostText()
			return true
		}
		text := g.text
		switch {
		case word && g.start == g.anchor:
			text = text[:ghostWordEnd(text)]
		case line && g.start == g.anchor:
			if nl := strings.IndexByte(text, '\n'); nl >= 0 {
				text = text[:nl+1]
			}
		case word || line:
			return false // part of a replacement is not a smaller change
		}
		e.stopPointerSelection()
		e.landing = nil
		e.cursorBlink.MarkActivity()
		e.BreakUndoGroup()
		if g.start != g.anchor {
			// A replacement is one edit; the caret goes after the new text.
			end := g.start + len(g.text)
			if e.ApplySourceEdits(e.Revision(), []document.Edit{{Start: g.start, End: g.anchor, Text: g.text}}) == nil {
				e.cursor = e.buf.positionAtOffset(end)
				e.clearGoal()
				e.ensureCursorVisible()
				e.ghostOutcome(g.owner, GhostAccepted)
			}
			return true
		}
		before := e.Revision()
		// The insertion runs through the typing path; it is an accept, not
		// typed-through, so only the accept below reports an outcome.
		e.ghostAccepting = true
		e.insertText(text) // exact insertion, not automatic pair/snippet expansion
		e.ghostAccepting = false
		if e.Revision() == before {
			return true
		}
		if rest := g.text[len(text):]; rest != "" {
			// A word or a line was taken: the rest stays shown at the caret.
			if at, end, ok := e.SourceByteRange(); ok && at == end && at == g.anchor+len(text) {
				_ = e.SetSuggestion(g.owner, e.Revision(), Suggestion{Start: at, End: at, Text: rest})
			}
			e.ghostOutcome(g.owner, GhostAcceptedPart)
			return true
		}
		e.ghostOutcome(g.owner, GhostAccepted)
		return true
	}
	if (key.Text == "" || key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) != 0) && !ui.IsCopyChord(key) {
		e.DismissGhostText()
	}
	return false
}

// Matches the editor's word-right categories while retaining whole graphemes:
// initial spaces plus one identifier or punctuation run, never a partial emoji.
func ghostWordEnd(text string) int {
	end, class := 0, -1
	for end < len(text) {
		cluster, _ := rendering.FirstGrapheme(text[end:])
		if cluster == "" {
			break
		}
		r, _ := utf8.DecodeRuneInString(cluster)
		next := 2
		if unicode.IsSpace(r) {
			next = 0
		} else if isWordChar(r) {
			next = 1
		}
		if class > 0 && next != class {
			break
		}
		class = next
		end += len(cluster)
	}
	return end
}
