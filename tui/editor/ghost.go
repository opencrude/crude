package editor

import (
	"errors"
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
// This first projection supports literal, insertion-only single-line candidates.
type editorGhost struct {
	buffer     *PieceTable
	document   *DocumentProjection
	revision   uint64
	caret      Position
	anchor     int
	text       string
	owner      string // which producer offered it; type-through keeps it
	base, line *editorInlineLine
}

type ghostEdit struct {
	offer          *editorGhost
	next, consumed int
	valid, edited  bool
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
	if !e.open || e.readOnly || e.buf == nil || e.Revision() != revision ||
		e.HasSelection() || e.IsMouseDragging() || !types.ValidEditorPredictionInsertion(text) {
		return errors.New("prediction does not identify a usable insertion caret")
	}
	if p := e.documentProjection; p != nil && (p.conflict != nil || p.snapshot.Conflict ||
		p.undoPending || p.historySync || len(p.historyQueue) != 0) {
		return errors.New("prediction cannot cross pending history or recovery")
	}
	start, end, ok := e.SourceByteRange()
	if !ok || start != anchor || end != anchor {
		return errors.New("prediction anchor is not this exact source boundary")
	}
	g := &editorGhost{buffer: e.buf, document: e.documentProjection,
		revision: revision, caret: e.cursor, anchor: anchor, text: text, owner: owner}
	var base *editorInlineLine
	if hints := e.currentInlays(); hints != nil {
		base = hints.lines[g.caret.Line]
	}
	line, err := e.prepareGhostLine(g, base)
	if err != nil {
		return err
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

// GhostText returns immutable private presentation metadata, not edit authority.
func (e *Editor) GhostText() (int, string, bool) {
	if g := e.currentGhost(); g != nil {
		return g.anchor, g.text, true
	}
	return 0, "", false
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
	if g != nil && g.buffer != nil && (!e.SnippetActive() || g.buffer != e.buf) {
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

func (e *Editor) prepareGhostLine(g *editorGhost, base *editorInlineLine) (*editorInlineLine, error) {
	raw, err := e.buf.lineView(g.caret.Line)
	if err != nil {
		return nil, err
	}
	var hints []InlineInsertion
	if base != nil {
		hints = base.insertions
	}
	p, err := buildInlineProjectionWithGhost(e.TabWidth(), string(raw), hints, &InlineInsertion{
		ByteOffset: g.anchor - e.buf.lineOffsets[g.caret.Line], Index: -1, Label: g.text,
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
	if g := e.currentGhost(); g != nil {
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
	if !tx.valid || !tx.edited || tx.consumed == len(old.text) || e.buf != old.buffer ||
		e.documentProjection != old.document || e.Revision() == old.revision {
		return
	}
	start, end, ok := e.SourceByteRange()
	if !ok || start != tx.next || end != tx.next {
		return // auto-closing a pair can leave the caret before an inserted suffix
	}
	// A typed-through candidate can follow an exact local prefix before its
	// canonical ACK. This does not authorize a new provider request or edit.
	_ = e.SetGhostTextFor(old.owner, e.Revision(), tx.next, old.text[tx.consumed:])
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
	if all || word {
		if e.DeferDocumentEvent(key) {
			e.DismissGhostText()
			return true
		}
		text := g.text
		if word {
			text = text[:ghostWordEnd(text)]
		}
		e.stopPointerSelection()
		e.landing = nil
		e.cursorBlink.MarkActivity()
		e.BreakUndoGroup()
		e.insertText(text) // exact insertion, not automatic pair/snippet expansion
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
