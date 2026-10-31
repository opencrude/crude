package editor

import (
	"fmt"
	"slices"

	"crude/document"
	"crude/snippet"
	"crude/tui/rendering"
)

// SnippetChoices is a detached, exact interaction token, not edit authority.
// Selection, source, history or interaction changes invalidate it.
type SnippetChoices struct {
	Session, Revision uint64
	Stop              int
	Choices           []string
	editor            *Editor
	view              editorViewState
}

func (e *Editor) SnippetActive() bool { return e.snippet != nil }

// CancelSnippet discards only private interaction. Accepted bytes and the
// canonical edit/history log are never undone or consumed.
func (e *Editor) CancelSnippet() {
	if e.snippet != nil && e.buf != nil {
		delete(e.buf.snippetViews, e)
	}
	e.snippet, e.snippetDirty = nil, false
}

func (e *Editor) installSnippet(state *snippetSession) {
	e.CancelSnippet()
	if state == nil || e.snippetSequence == ^uint64(0) {
		return
	}
	e.snippetSequence++
	e.snippet = state
	if state.active < 0 {
		e.finishSnippet()
		return
	}
	if !e.selectSnippetStop() {
		e.CancelSnippet()
		return
	}
	if e.buf.snippetViews == nil {
		e.buf.snippetViews = make(map[*Editor]struct{})
	}
	e.buf.snippetViews[e] = struct{}{}
}

func (e *Editor) snippetPosition(offset int) (Position, bool) {
	if offset < 0 {
		return Position{}, false
	}
	pos := e.buf.positionAtOffset(offset)
	return pos, e.buf.positionToOffset(pos) == offset
}

func (e *Editor) selectSnippetStop() bool {
	state := e.snippet
	slot := state.occurrences[state.active]
	start, valid := e.snippetPosition(slot.start)
	end, endValid := e.snippetPosition(slot.end)
	if !valid || !endValid {
		return false
	}
	e.BreakUndoGroup()
	e.cursor = end
	e.selAnchor, e.selEnd = start, end
	e.selecting = slot.start != slot.end
	e.stopPointerSelection()
	e.clearGoal()
	e.ensureCursorVisible()
	return true
}

func (e *Editor) finishSnippet() {
	state := e.snippet
	landing := state.end
	for _, slot := range state.occurrences {
		if slot.live && slot.stop == 0 {
			landing = slot.end
			break
		}
	}
	pos, valid := e.snippetPosition(landing)
	e.CancelSnippet()
	e.ClearSelection()
	if valid {
		e.cursor = pos
		e.clearGoal()
		e.ensureCursorVisible()
	}
	e.BreakUndoGroup()
}

func (e *Editor) navigateSnippet(backward bool) bool {
	e.reconcileSnippetSelection()
	state := e.snippet
	if state == nil {
		return false
	}
	ids := make([]int, 0, len(state.occurrences))
	for _, slot := range state.occurrences {
		if slot.live && slot.stop > 0 {
			ids = append(ids, slot.stop)
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	index, _ := slices.BinarySearch(ids, state.occurrences[state.active].stop)
	if backward {
		if index == 0 {
			return true // first stop owns Shift+Tab, without wrapping/outdent
		}
		index--
	} else {
		index++
		if index == len(ids) {
			e.finishSnippet()
			return true
		}
	}
	state.selectStop(ids[index])
	e.snippetSequence++
	if !e.selectSnippetStop() {
		e.CancelSnippet()
	}
	return true
}

// Only a caret/selection wholly within the chosen occurrence can continue it.
// A pointer/navigation departure retires it without changing that departure.
func (e *Editor) reconcileSnippetSelection() {
	if e.snippet == nil {
		return
	}
	s := e.snippet
	active := s.occurrences[s.active]
	offset := e.buf.positionToOffset(e.cursor)
	valid := e.open && !e.readOnly && active.live && offset >= active.start && offset <= active.end
	if e.HasSelection() {
		start, end := e.buf.positionToOffset(e.selAnchor), e.buf.positionToOffset(e.selEnd)
		valid = valid && min(start, end) >= active.start && max(start, end) <= active.end
	}
	if state := e.DocumentState(); state.Conflict != nil || state.UndoPending || state.HistoryQueued {
		valid = false
	}
	if !valid {
		e.CancelSnippet()
	}
}

func (e *Editor) SnippetChoiceOptions() (SnippetChoices, bool) {
	e.reconcileSnippetSelection()
	s := e.snippet
	if s == nil {
		return SnippetChoices{}, false
	}
	slot := s.occurrences[s.active]
	if len(slot.choices) == 0 {
		return SnippetChoices{}, false
	}
	return SnippetChoices{Session: e.snippetSequence, Revision: e.Revision(),
		Stop: slot.stop, Choices: slices.Clone(slot.choices), editor: e, view: e.captureViewState()}, true
}

func (e *Editor) ApplySnippetChoice(token SnippetChoices, value string) error {
	current, valid := e.SnippetChoiceOptions()
	if !valid || token.Session != current.Session || token.Revision != current.Revision ||
		token.Stop != current.Stop || token.editor != e || token.view != current.view ||
		!slices.Contains(current.Choices, value) {
		return fmt.Errorf("snippet choice is no longer current")
	}
	s := e.snippet.occurrences[e.snippet.active]
	e.beginUndoGroup()
	defer e.endUndoGroup()
	e.BreakUndoGroup()
	e.spliceRecorded(s.start, s.end, value, e.cursor)
	e.cursor = e.buf.positionAtOffset(s.start + len(value))
	e.ClearSelection()
	e.finishEdit()
	return nil
}

func (e *Editor) observeSnippetMutation(edit document.Edit, owner *Editor) {
	if e.snippet == nil || e.snippetApplying {
		return
	}
	if owner != e || e.snippet.observeLocal(snippetSplice{start: edit.Start, end: edit.End, text: edit.Text}) != nil {
		e.CancelSnippet()
		return
	}
	e.snippetDirty = true
}

// Peer edits are simultaneous original-coordinate vectors. Interaction can
// retire, but never reject or mirror an otherwise valid canonical publication.
func prepareSnippetPeer(state *snippetSession, edits []document.Edit) *snippetSession {
	if state == nil || len(edits) == 0 {
		return state
	}
	if len(edits) > snippet.MaxOccurrences {
		return nil
	}
	splices := make([]snippetSplice, len(edits))
	for i, edit := range edits {
		splices[i] = snippetSplice{start: edit.Start, end: edit.End, text: edit.Text}
	}
	next, err := state.preparePeer(splices)
	if err != nil {
		return nil
	}
	return next
}

func (e *Editor) commitSnippetPeer(state *snippetSession) {
	if state == nil {
		e.CancelSnippet()
		return
	}
	e.snippet = state
}

// Called before the OUTER group closes, never inside PieceTable observation.
// All mirrored byte splices use the existing recorder and original group.
func (e *Editor) finishSnippetMirrors() {
	if e.snippet == nil || !e.snippetDirty {
		return
	}
	e.snippetDirty = false
	old := e.snippet
	active := old.occurrences[old.active]
	cursor := e.buf.positionToOffset(e.cursor) - active.start
	anchor, end := e.buf.positionToOffset(e.selAnchor)-active.start, e.buf.positionToOffset(e.selEnd)-active.start
	selected := e.HasSelection()
	read := func(start, end int) (string, error) {
		e.buf.ensureCache()
		if start < 0 || end < start || end > len(e.buf.cachedBytes) || end-start > snippet.MaxExpandedBytes {
			return "", errSnippetRetired
		}
		return string(e.buf.cachedBytes[start:end]), nil
	}
	edits, next, err := old.prepareMirrors(read)
	if err != nil {
		e.CancelSnippet()
		return
	}
	e.buf.ensureCache()
	size := len(e.buf.cachedBytes)
	for _, edit := range edits {
		size += len(edit.text) - (edit.end - edit.start)
		if size > document.MaxBytes {
			e.CancelSnippet()
			return
		}
	}
	e.snippetApplying = true
	batch := make([]document.Edit, len(edits))
	for i, edit := range edits {
		batch[i] = document.Edit{Start: edit.start, End: edit.end, Text: edit.text}
	}
	err = e.spliceRecordedBatch(batch, e.cursor)
	e.snippetApplying = false
	if err != nil {
		e.CancelSnippet()
		return
	}
	e.snippet = next
	nextActive := next.occurrences[next.active]
	e.cursor = e.buf.positionAtOffset(nextActive.start + min(max(0, cursor), nextActive.end-nextActive.start))
	if selected {
		e.selAnchor = e.buf.positionAtOffset(nextActive.start + max(0, anchor))
		e.selEnd = e.buf.positionAtOffset(nextActive.start + max(0, end))
	}
	if len(edits) > 0 {
		e.finishSimpleEdit()
	}
}

// Reject placeholder endpoints that the rendered editor cannot represent.
// This scans the already-prepared completion result once, not the document per
// keystroke/stop, and uses the same grapheme profile as every editor consumer.
func snippetSourceBoundaries(text string, s *snippetSession) bool {
	wanted := make([]int, 0, 2*len(s.occurrences)+1)
	for _, o := range s.occurrences {
		wanted = append(wanted, o.start, o.end)
	}
	wanted = append(wanted, s.end)
	slices.Sort(wanted)
	wanted = slices.Compact(wanted)
	at, index := 0, 0
	for index < len(wanted) {
		if wanted[index] < at {
			return false
		}
		if wanted[index] == at {
			index++
			continue
		}
		if at >= len(text) {
			return false
		}
		cluster, _ := rendering.FirstGrapheme(text[at:])
		if len(cluster) == 0 {
			return false
		}
		at += len(cluster)
	}
	return true
}
