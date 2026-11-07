package editor

import (
	"crude/document"
	"crude/types"
)

// Linked views show one document in several places at once (a split editor,
// the same file in two panes or windows). They share the PieceTable, Buffer,
// canonical document projection and its history, the Git baseline, the disk
// watcher and the document width model. Each view keeps its own caret,
// selection, scroll, search, hover, snippet/ghost interaction and paint
// caches.
//
// Every text mutation is published synchronously to the other views: their
// anchors are remapped through the exact byte edits and their caches follow
// the same revision, so all views show the same text in the same frame with
// no round trip and no second copy of the document.
//
// The first view in the set is the primary. It owns the shared side effects
// (disk watcher, Git projection, width model, coordinator registration).
// Closing the primary hands them to the next view; the document closes only
// with its last view.

// NewLinkedView opens another view of e's document at e's caret and scroll.
// The new view starts with e's syntax, insight and diagnostics projection, so
// it paints complete content in its first frame.
func (e *Editor) NewLinkedView() *Editor {
	v := New(e.th)
	v.buf, v.buffer, v.documentProjection = e.buf, e.buffer, e.documentProjection
	v.filePath, v.title = e.filePath, e.title
	v.readOnly, v.hideLineNumbers, v.lineNumberOffset = e.readOnly, e.hideLineNumbers, e.lineNumberOffset
	v.statusChrome, v.maxHistory = e.statusChrome, e.maxHistory
	v.open = e.open
	v.cursor, v.scrollX, v.scrollY = e.cursor, e.scrollX, e.scrollY
	v.goalCol, v.goalSet = e.goalCol, e.goalSet
	v.canonicalInsight = e.canonicalInsight
	v.chromaStyle, v.chromaLexer, v.syntaxPlain = e.chromaStyle, e.chromaLexer, e.syntaxPlain
	if e.syntaxBuffer == e.buf && e.syntaxRevision == e.buf.Revision() {
		v.syntaxBuffer, v.syntaxRevision = e.syntaxBuffer, e.syntaxRevision
		v.syntaxDocument, v.canonicalInk = e.syntaxDocument, e.canonicalInk
	}
	e.diagMu.RLock()
	facts, hidden := e.diagnosticFacts, e.diagnosticsHidden
	e.diagMu.RUnlock()
	v.diagnosticsHidden = hidden
	if facts.ID != "" {
		v.setDiagnostics(facts)
	}
	if len(e.buf.views) == 0 {
		e.buf.views = append(e.buf.views, e)
	}
	e.buf.views = append(e.buf.views, v)
	v.cursorBlink.MarkActivity()
	return v
}

// AdoptView takes over another view's caret, selection, scroll and search, so
// a tab keeps its exact presentation when the editor behind it changes.
func (e *Editor) AdoptView(from *Editor) {
	if from == nil || from == e {
		return
	}
	e.restoreViewState(from.captureViewState())
	e.goalCol, e.goalSet = from.goalCol, from.goalSet
	e.search, e.searchRevision = from.search, from.searchRevision
	e.lineRunesCacheValid, e.lineGeomCache = false, nil
	e.ClearHover()
}

// Caret returns the caret and the first visible line, for navigation history.
func (e *Editor) Caret() (Position, int) { return e.cursor, e.scrollY }

// RestoreCaret returns to a recorded place: the caret moves to pos (clamped
// to the current text) and the first visible line to scrollY, then the caret
// is kept visible.
func (e *Editor) RestoreCaret(pos Position, scrollY int) {
	e.DismissGhostText()
	e.BreakUndoGroup()
	e.CancelPointerCapture()
	e.clearGoal()
	e.ClearSelection()
	e.cursor = e.buf.snapPosition(Position{Line: min(max(0, pos.Line), max(0, e.buf.LineCount()-1)), Col: max(0, pos.Col)})
	e.scrollY = min(max(0, scrollY), max(0, e.buf.LineCount()-1))
	e.ensureCursorVisible()
	e.cursorBlink.MarkActivity()
}

// Primary is the view that owns the document's shared side effects. An
// unlinked editor is its own primary.
func (e *Editor) Primary() *Editor {
	if !e.linked() {
		return e
	}
	return e.buf.views[0]
}

// linked reports whether e is one of several views of its document.
func (e *Editor) linked() bool {
	if e == nil || e.buf == nil {
		return false
	}
	for _, v := range e.buf.views {
		if v == e {
			return true
		}
	}
	return false
}

// LinkedViews returns every open view of this document, primary first. An
// unlinked editor returns itself.
func (e *Editor) LinkedViews() []*Editor {
	if !e.linked() {
		return []*Editor{e}
	}
	return append([]*Editor(nil), e.buf.views...)
}

// eachView visits every view of this document without allocating.
func (e *Editor) eachView(visit func(*Editor)) {
	if !e.linked() {
		visit(e)
		return
	}
	for _, v := range e.buf.views {
		visit(v)
	}
}

// unlink removes e from its link set. It reports whether other views still
// show the document, in which case they keep every shared resource and e must
// release its references without tearing them down.
func (e *Editor) unlink() bool {
	pt := e.buf
	if pt == nil || len(pt.views) == 0 {
		return false
	}
	index := -1
	for i, v := range pt.views {
		if v == e {
			index = i
			break
		}
	}
	if index < 0 {
		return false
	}
	pt.views = append(pt.views[:index], pt.views[index+1:]...)
	next := pt.views[0]
	if len(pt.views) == 1 {
		pt.views = nil
	}
	if p := e.documentProjection; p != nil {
		if p.editor == e {
			p.editor = next
		}
		if p.restoreEditor == e {
			p.restoreEditor = nil
		}
	}
	if index == 0 {
		// The primary hands over the document-level projections it owns.
		next.gitHunks, next.gitHead, next.gitHeadLines = e.gitHunks, e.gitHead, e.gitHeadLines
		next.gitSnapshot, next.gitLineOffsets = e.gitSnapshot, e.gitLineOffsets
		next.gitBlameHunks, next.gitBlame = e.gitBlameHunks, e.gitBlame
		next.gitLoaded, next.gitConflicted, next.gitRevision = e.gitLoaded, e.gitConflicted, e.gitRevision
		next.markDocumentWidthAll()
		e.watchMu.Lock()
		watching := e.watcher != nil
		e.watchMu.Unlock()
		if watching {
			e.stopWatching()
			next.startWatching()
		}
	}
	return true
}

// releaseLinkedDocument drops e's references to a document other views still
// show. Shared text, history, observers and watchers stay with them.
func (e *Editor) releaseLinkedDocument() {
	e.documentProjection = nil
	e.canonicalInsight, e.canonicalInk = nil, nil
	e.diagMu.Lock()
	e.diagnosticFacts = types.InsightDiagnostics{}
	e.diagnosticProjectedID = ""
	e.diagnostics, e.diagByLine = nil, nil
	e.diagMu.Unlock()
	e.buf = NewPieceTable()
	e.buffer = newBuffer(e.buf, "", false)
	e.ClearGitAnnotation()
}

// linkedAnchorCount is the fixed number of anchors a view remaps: caret,
// selection anchor and end, first visible line, and pointer drag ends.
const linkedAnchorCount = 6

// captureLinked records the anchor offsets of every other open view before a
// mutation. The returned slice is the table's scratch; it is valid until the
// next mutation.
func (pt *PieceTable) captureLinked() []*Editor {
	if len(pt.views) == 0 {
		return nil
	}
	pt.linkScratch = pt.linkScratch[:0]
	for _, v := range pt.views {
		if v == pt.linkActor || !v.open {
			continue
		}
		v.linkAnchors = [linkedAnchorCount]int{
			pt.positionToOffset(v.cursor), pt.positionToOffset(v.selAnchor), pt.positionToOffset(v.selEnd),
			pt.positionToOffset(Position{Line: v.scrollY}), pt.positionToOffset(v.drag.start), pt.positionToOffset(v.drag.end),
		}
		pt.linkScratch = append(pt.linkScratch, v)
	}
	return pt.linkScratch
}

// remapLinked moves captured anchors through one simultaneous edit vector in
// original coordinates (sorted, disjoint). An anchor where another view
// inserts text stays before that text, as in established editors.
func (pt *PieceTable) remapLinked(views []*Editor, edits []document.Edit) {
	for _, v := range views {
		var anchors [linkedAnchorCount]Position
		for i, off := range v.linkAnchors {
			anchors[i] = pt.positionAtOffset(remapLinkedOffset(off, edits))
		}
		v.cursor, v.selAnchor, v.selEnd = anchors[0], anchors[1], anchors[2]
		v.scrollY = anchors[3].Line
		if v.IsMouseDragging() {
			v.drag.start, v.drag.end = anchors[4], anchors[5]
		}
		v.afterLinkedEdit()
	}
}

// resetLinked follows a whole-source replacement: anchors are clamped, not
// mapped, because no line-preserving mutation exists.
func (pt *PieceTable) resetLinked(views []*Editor) {
	for _, v := range views {
		v.CancelPointerCapture()
		v.cursor = pt.snapPosition(v.cursor)
		v.selAnchor, v.selEnd = pt.snapPosition(v.selAnchor), pt.snapPosition(v.selEnd)
		v.scrollY = min(v.scrollY, max(0, pt.LineCount()-1))
		v.afterLinkedEdit()
	}
}

func remapLinkedOffset(off int, edits []document.Edit) int {
	delta := 0
	for _, r := range edits {
		if off < r.Start || off == r.Start && r.Start == r.End {
			break
		}
		if off < r.End {
			return r.Start + delta
		}
		delta += len(r.Text) - (r.End - r.Start)
	}
	return off + delta
}

// afterLinkedEdit is the passive view's share of post-edit bookkeeping. The
// acting view marks the shared width model; syntax colours, diagnostics paint
// and geometry follow the same revision journal as the acting view.
func (e *Editor) afterLinkedEdit() {
	// History is document state (PieceTable.changes or the canonical
	// projection), so a sibling's edit needs only this view's paint caches.
	e.invalidateEditorCaches()
}
