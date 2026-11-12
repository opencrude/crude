package editor

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/textencoding"
	"crude/tui/ui"
	"crude/types"
)

var ErrDocumentPending = errors.New("document command pending; retain edits and retry after acknowledgement")

// DocumentProjection belongs to one Editor lifetime on the UI goroutine.
// Pending mutations use sequential byte coordinates; wire commands use one
// immutable pre-change coordinate system.
type DocumentProjection struct {
	snapshot         document.Snapshot
	pending          []document.Edit
	pendingGroups    []string
	editGroup        string
	editGroupBefore  editorViewState
	editor           *Editor
	views            map[string]documentGroupView
	viewOrder        []string
	inflight         *document.Change
	owner            document.Owner
	conflict         error
	onEdited         func()
	onUndo           func(DocumentUndoIntent)
	undoPending      bool
	undoSequence     uint64
	historyQueue     []documentHistoryAction
	historySync      bool
	historyRedo      bool
	historyRequestID string
	historyViewID    uint64 // view that invoked the in-flight undo/redo
	replaying        bool
	restoreView      *editorViewState
	// restoreEditor is the linked view whose edit group restoreView belongs
	// to; nil means the coordinator's own editor.
	restoreEditor *Editor
	suppressed    bool
	// hold orders input behind one guarded edit; see HoldDocumentInput.
	hold         *documentHold
	holdSequence uint64
	// heldRefused reports, once, that a guarded edit left this view because
	// canonical text advanced before it could commit.
	heldRefused bool
}

// documentHold is one guarded edit and the input waiting behind it. The edit
// may commit only at the canonical revision observed when the hold began. It
// is never rebased over another author's change: if canonical text advances
// first, the edit leaves this view and the held input replays.
type documentHold struct {
	sequence, revision uint64
	applied            bool
	before             editorViewState
	editor             *Editor
}

// DocumentHold identifies one exact hold for the caller that began it.
type DocumentHold struct {
	projection *DocumentProjection
	sequence   uint64
}

// ErrHeldEditRefused reports that the document owner refused a guarded edit.
// The edit has already left the view; held input replays normally.
var ErrHeldEditRefused = errors.New("the file changed before the guarded edit was committed")

// documentGroupView retains the edit's originating view and its private caret.
// Its snapshot is restored only when that same view invokes undo or redo.
type documentGroupView struct {
	before, after editorViewState
	editor        *Editor
}
type documentHistoryAction struct {
	edit     func()
	redo     bool
	viewID   uint64
	evidence DocumentHistoryIntent
}

// DocumentHistoryIntent is private recovery evidence for ordered UI input, not
// permission to publish it against a different canonical document.
type DocumentHistoryIntent struct {
	Action        ui.ActionID
	Key           *tea.KeyPressMsg
	Paste         *tea.PasteMsg
	Redo          bool
	Unrecoverable bool
}

func (e *Editor) beginDocumentEditGroup(id string) {
	if p := e.documentProjection; p != nil {
		p.editor = e
		p.beginEditGroup(id)
	}
}

func (e *Editor) endDocumentEditGroup() {
	if p := e.documentProjection; p != nil {
		p.endEditGroup()
	}
}

func (p *DocumentProjection) beginEditGroup(id string) {
	p.editGroup = id
	p.editGroupBefore = p.editor.captureViewState()
}

func (p *DocumentProjection) noteEditGroup() {
	id := p.editGroup
	if id == "" {
		return
	}
	if p.views == nil {
		p.views = make(map[string]documentGroupView)
	}
	if _, exists := p.views[id]; !exists {
		if len(p.viewOrder) >= p.editor.maxHistory && len(p.viewOrder) > 0 {
			delete(p.views, p.viewOrder[0])
			p.viewOrder = p.viewOrder[1:]
		}
		p.viewOrder = append(p.viewOrder, id)
		p.views[id] = documentGroupView{before: p.editGroupBefore, editor: p.editor}
	}
}

func (p *DocumentProjection) endEditGroup() {
	if view, ok := p.views[p.editGroup]; p.editGroup != "" && ok {
		view.after = p.editor.captureViewState()
		p.views[p.editGroup] = view
	}
	p.editGroup = ""
}

// DeferDocumentEdit retains an editing intent behind earlier history gestures.
// Replay happens on Update after canonical sync, at the resulting caret rather
// than at coordinates from the text that is about to be undone.
func (e *Editor) DeferDocumentEdit(edit func()) bool {
	p := e.documentProjection
	if p == nil || p.replaying || (p.hold == nil && !p.undoPending && !p.historySync && len(p.historyQueue) == 0) || (p.conflict != nil && !errors.Is(p.conflict, ErrDocumentPending)) {
		return false
	}
	p.historyQueue = append(p.historyQueue, documentHistoryAction{edit: edit, evidence: DocumentHistoryIntent{Unrecoverable: true}})
	return true
}

// HoldDocumentInput begins ordering later input behind one guarded edit. Only
// a settled projection can hold, so the edit describes exactly its canonical
// text. Input typed meanwhile waits in the ordinary history queue.
func (e *Editor) HoldDocumentInput() (DocumentHold, bool) {
	p := e.documentProjection
	if p == nil || p.hold != nil || p.replaying || len(p.pending) != 0 || p.inflight != nil || p.undoPending ||
		p.historySync || len(p.historyQueue) != 0 || p.conflict != nil {
		return DocumentHold{}, false
	}
	p.holdSequence++
	p.hold = &documentHold{sequence: p.holdSequence, revision: p.snapshot.Revision}
	return DocumentHold{projection: p, sequence: p.holdSequence}, true
}

// HoldsDocumentInput reports that input to this document waits behind a
// guarded edit.
func (e *Editor) HoldsDocumentInput() bool {
	p := e.documentProjection
	return p != nil && p.hold != nil
}

// ReleaseDocumentHold ends a hold whose guarded edit was not applied. Held
// input replays at the next drain. An old, foreign or applied hold is ignored:
// the document owner's receipt settles an applied edit.
func (e *Editor) ReleaseDocumentHold(h DocumentHold) {
	if p := e.documentProjection; p != nil && h.projection == p && p.hold != nil && p.hold.sequence == h.sequence && !p.hold.applied {
		p.hold = nil
	}
}

// ConsumeRefusedHeldEdit reports, once, that a guarded edit left this view
// because another change reached the document first.
func (e *Editor) ConsumeRefusedHeldEdit() bool {
	p := e.documentProjection
	if p == nil || !p.heldRefused {
		return false
	}
	p.heldRefused = false
	return true
}

// heldEditPending is an applied guarded edit that is not yet in flight.
func (p *DocumentProjection) heldEditPending() bool {
	return p.hold != nil && p.hold.applied && p.inflight == nil && len(p.pending) != 0
}

// retireHeldEdit removes a guarded edit that never became canonical text.
// edits are its simultaneous changes against the canonical snapshot; input was
// held, so no later local edit depends on them.
func (e *Editor) retireHeldEdit(edits []document.Edit) {
	p := e.documentProjection
	hold := p.hold
	p.hold, p.heldRefused = nil, true
	_, inverse, err := document.Apply(p.snapshot.Text, edits)
	if err != nil {
		p.conflict = err
		return
	}
	target := hold.editor
	if target == nil || !target.open || target.documentProjection != p {
		target = e
	}
	target.CancelSnippet()
	target.DismissGhostText()
	e.commitDocumentDelta(document.Change{}, inverse, nil)
	p.pendingGroups = nil
	// The text is again the text the edit replaced, so the private view from
	// before the edit is exact.
	target.restoreViewState(hold.before)
	target.ensureCursorVisible()
}

// DeferDocumentEvent preserves the event payload as well as its live replay, so
// a crash cannot silently discard typing queued behind an undo acknowledgement.
func (e *Editor) DeferDocumentEvent(msg tea.Msg) bool {
	if !e.DeferDocumentEdit(func() { e.HandleEvent(msg) }) {
		return false
	}
	p := e.documentProjection
	intent := DocumentHistoryIntent{}
	switch v := msg.(type) {
	case tea.KeyPressMsg:
		intent.Key = &v
	case tea.PasteMsg:
		intent.Paste = &v
	case ActionMsg:
		intent.Action = v.ID
	default:
		intent.Unrecoverable = true
	}
	p.historyQueue[len(p.historyQueue)-1].evidence = intent
	return true
}

type DocumentUndoIntent struct {
	DocumentID       string
	Owner            document.Owner
	ExpectedRevision uint64
	Redo             bool
	Sequence         uint64
	Projection       *DocumentProjection
}

type DocumentState struct {
	DocumentID              string
	Revision                uint64
	Encoding, SavedEncoding textencoding.Encoding
	PendingEdits            int
	InFlight, UndoPending   bool
	HistoryQueued           bool
	Conflict                error
}

func (e *Editor) detachDocument() {
	e.ClearSemanticTokens()
	e.clearSecondary()
	e.DismissGhostText()
	e.CancelSnippet()
	if e.unlink() {
		e.releaseLinkedDocument()
		return
	}
	e.diagMu.Lock()
	e.diagnosticFacts = types.InsightDiagnostics{}
	e.diagnosticProjectedID = ""
	e.diagnostics, e.diagByLine = nil, nil
	e.diagMu.Unlock()
	if e.documentProjection != nil && e.buf != nil {
		e.buf.mutationObserver = nil
	}
	e.documentProjection = nil
	e.canonicalInsight = nil
}

// InstallDocument is a watcher-free open, not a reload or a save acknowledgement.
// Callers must explicitly resolve any existing local edits before replacing it.
func (e *Editor) InstallDocument(s document.Snapshot) error {
	if e.documentProjection != nil && (len(e.documentProjection.pending) > 0 || e.documentProjection.inflight != nil || e.documentProjection.undoPending || e.documentProjection.historySync || len(e.documentProjection.historyQueue) > 0) {
		return ErrDocumentPending
	}
	if s.ID == "" || s.Revision == 0 {
		return document.ErrRevision
	}
	if len(s.Text) > document.MaxBytes || len(s.Saved) > document.MaxBytes || !utf8.Valid(s.Text) || !utf8.Valid(s.Saved) {
		return document.ErrLimit
	}
	e.cancelRows()
	e.folds = foldState{}
	e.rowsStopped = false
	width := e.TabWidth()
	e.detachDocument()
	e.stopWatching()
	pt := NewPieceTable()
	pt.tabSize = width
	pt.ReplaceAll(string(s.Text))
	pt.filePath = s.Path
	e.buf = pt
	e.buffer = newBuffer(pt, s.Path, s.DiskExists)
	e.filePath, e.title = s.Path, filepath.Base(s.Path)
	if s.Path == "" && document.IsUntitledID(s.ID) && s.Name != "" {
		e.title, e.buffer.Title = s.Name, s.Name
	}
	e.buffer.ID = s.ID
	e.installDocumentBaseline(s)
	s.Text = bytes.Clone(s.Text)
	s.Saved = bytes.Clone(s.Saved)
	p := &DocumentProjection{snapshot: s, editor: e}
	e.documentProjection = p
	pt.mutationObserver = func(edit document.Edit) {
		if p.suppressed {
			return
		}
		p.noteEditGroup()
		p.pending = append(p.pending, edit)
		// Recovered evidence may lack grouping for its original pending edits.
		for len(p.pendingGroups) < len(p.pending)-1 {
			p.pendingGroups = append(p.pendingGroups, "")
		}
		p.pendingGroups = append(p.pendingGroups, p.editGroup)
		e.buffer.NoteEdit()
		if p.onEdited != nil {
			p.onEdited()
		}
	}
	e.open = true
	e.cursor = Position{}
	e.scrollX, e.scrollY = 0, 0
	e.ClearSelection()
	e.ClearHover()
	e.ClearGitAnnotation()
	e.invalidateEditorCaches()
	e.markDocumentWidthAll()
	e.initChroma()
	return nil
}

func (e *Editor) installDocumentBaseline(s document.Snapshot) {
	e.buffer.encoding, e.buffer.savedEncoding = s.Encoding, s.SavedEncoding
	savepoint := snapshotBytes(s.Saved)
	// The private Save receipt and ordered topic can describe the same baseline.
	// Re-observing it is not another disk mutation: that would invalidate a
	// workspace-switch preimage captured between the two deliveries.
	if e.buffer.diskHash != s.DiskHash || e.buffer.diskExists != s.DiskExists || !e.buffer.savepoint.equal(savepoint) {
		e.buffer.savepoint = savepoint
		e.buffer.diskHash, e.buffer.diskExists = s.DiskHash, s.DiskExists
		e.buffer.diskGeneration++
	}
	// A text-only canonical delta still needs dirty-state derivation even when
	// no savepoint changed.
	e.buffer.NoteEdit()
}

func (e *Editor) SetOnDocumentEdited(fn func()) {
	if p := e.documentProjection; p != nil {
		p.onEdited = fn
	}
}
func (e *Editor) SetOnDocumentUndo(fn func(DocumentUndoIntent)) {
	if p := e.documentProjection; p != nil {
		p.onUndo = fn
	}
}
func (e *Editor) DocumentState() DocumentState {
	p := e.documentProjection
	if p == nil {
		if e.buffer != nil {
			return DocumentState{Encoding: e.buffer.encoding, SavedEncoding: e.buffer.savedEncoding}
		}
		return DocumentState{}
	}
	return DocumentState{DocumentID: p.snapshot.ID, Revision: p.snapshot.Revision, Encoding: p.snapshot.Encoding, SavedEncoding: p.snapshot.SavedEncoding, PendingEdits: len(p.pending), InFlight: p.inflight != nil, UndoPending: p.undoPending, HistoryQueued: p.historySync || len(p.historyQueue) > 0, Conflict: p.conflict}
}

// DocumentSnapshot is the detached canonical baseline, excluding optimistic edits.
func (e *Editor) DocumentSnapshot() document.Snapshot {
	p := e.documentProjection
	if p == nil {
		return document.Snapshot{}
	}
	s := p.snapshot
	s.Text, s.Saved = bytes.Clone(s.Text), bytes.Clone(s.Saved)
	return s
}

// DocumentBuffer exposes the one live buffer for nonvisual editor consumers.
func (e *Editor) DocumentBuffer() *Buffer { return e.buffer }

// ReconcileDocumentRevision allows a definitively rejected stale command to
// rebase its retained draft. Unknown transport outcomes must never use this.
func (e *Editor) ReconcileDocumentRevision() {
	if p := e.documentProjection; p != nil && p.inflight == nil {
		p.conflict = nil
	}
}

// ResetDocument accepts an explicit history gap only for a settled projection.
func (e *Editor) ResetDocument(s document.Snapshot) error {
	p := e.documentProjection
	if p == nil || len(p.pending) != 0 || p.inflight != nil || p.undoPending {
		return ErrDocumentPending
	}
	if s.ID != p.snapshot.ID || s.Revision == 0 || !utf8.Valid(s.Text) || !utf8.Valid(s.Saved) || len(s.Text) > document.MaxBytes || len(s.Saved) > document.MaxBytes {
		return document.ErrRevision
	}
	// Keep the managed buffer and mutation observer: all existing consumers
	// must continue to see the same live text authority after a history gap.
	e.CancelSnippet()
	e.CancelPointerCapture()
	p.suppressed = true
	e.buf.ReplaceAll(string(s.Text))
	p.suppressed = false
	s.Text, s.Saved = bytes.Clone(s.Text), bytes.Clone(s.Saved)
	p.snapshot, p.conflict = s, nil
	p.views, p.viewOrder, p.restoreView = nil, nil, nil
	e.installDocumentBaseline(s)
	e.invalidateEditorCaches()
	e.markDocumentWidthAll()
	return nil
}

// UseSharedDocument discards only this settled private projection, never writes
// shared text. The coordinator must durably back up the draft and check the
// shared preimage first. A changed local revision refuses the decision.
func (e *Editor) UseSharedDocument(s document.Snapshot, localRevision uint64) error {
	p := e.documentProjection
	if p == nil || e.Revision() != localRevision || p.inflight != nil || p.undoPending {
		return ErrDocumentPending
	}
	if s.ID != p.snapshot.ID || s.Revision == 0 || !utf8.Valid(s.Text) || !utf8.Valid(s.Saved) || len(s.Text) > document.MaxBytes || len(s.Saved) > document.MaxBytes {
		return document.ErrRevision
	}
	p.pending, p.pendingGroups, p.historyQueue = nil, nil, nil
	p.historySync, p.restoreView = false, nil
	if err := e.ResetDocument(s); err != nil {
		return err
	}
	// Reviewed private discard retires the recovered command owner too. The
	// current attachment will bind only after its fresh canonical publication.
	p.owner = document.Owner{}
	e.ClearSelection()
	e.cursor.Line = min(e.cursor.Line, max(0, e.buf.LineCount()-1))
	e.clampCursorToLine()
	return nil
}

// composeDocumentEdits uses symbolic base spans: no whole-file export per edit.
type documentSpan struct {
	start, end int
	text       string
	inserted   bool
}

func composeDocumentEdits(size int, sequential []document.Edit) []document.Edit {
	spans := []documentSpan{{start: 0, end: size}}
	for _, edit := range sequential {
		cut := func(offset int) int {
			pos := 0
			for i, s := range spans {
				length := s.end - s.start
				if s.inserted {
					length = len(s.text)
				}
				if offset == pos {
					return i
				}
				if offset < pos+length {
					left, right := s, s
					n := offset - pos
					if s.inserted {
						left.text = s.text[:n]
						right.text = s.text[n:]
					} else {
						left.end = s.start + n
						right.start = s.start + n
					}
					spans = append(spans, documentSpan{})
					copy(spans[i+2:], spans[i+1:])
					spans[i], spans[i+1] = left, right
					return i + 1
				}
				pos += length
			}
			return len(spans)
		}
		a := cut(edit.Start)
		b := cut(edit.End)
		next := append([]documentSpan(nil), spans[:a]...)
		if edit.Text != "" {
			next = append(next, documentSpan{text: edit.Text, inserted: true})
		}
		spans = append(next, spans[b:]...)
	}
	var result []document.Edit
	pos := 0
	text := ""
	for _, s := range spans {
		if s.inserted {
			text += s.text
			continue
		}
		if s.start != pos || text != "" {
			result = append(result, document.Edit{Start: pos, End: s.start, Text: text})
		}
		pos = s.end
		text = ""
	}
	if pos != size || text != "" {
		result = append(result, document.Edit{Start: pos, End: size, Text: text})
	}
	return result
}

func (e *Editor) CaptureDocumentEdits(owner document.Owner, requestID, groupID string) (document.Change, error) {
	p := e.documentProjection
	if p == nil {
		return document.Change{}, document.ErrOwner
	}
	if p.conflict != nil && !errors.Is(p.conflict, ErrDocumentPending) {
		return document.Change{}, p.conflict
	}
	if p.inflight != nil || p.undoPending || p.historySync {
		return document.Change{}, ErrDocumentPending
	}
	if owner.ClientID == "" || owner.Epoch == 0 || requestID == "" || (p.owner.ClientID != "" && p.owner != owner) {
		return document.Change{}, document.ErrOwner
	}
	for len(p.pendingGroups) < len(p.pending) {
		p.pendingGroups = append(p.pendingGroups, "")
	}
	n := len(p.pending)
	if n > 0 {
		logicalGroup := p.pendingGroups[0]
		for i := 1; i < n; i++ {
			if p.pendingGroups[i] != logicalGroup {
				n = i
				break
			}
		}
		if logicalGroup != "" {
			groupID = logicalGroup
		}
	}
	edits := composeDocumentEdits(len(p.snapshot.Text), p.pending[:n])
	if len(edits) == 0 {
		p.pending, p.pendingGroups = p.pending[n:], p.pendingGroups[n:]
		return document.Change{}, nil
	}
	if len(edits) > document.MaxEdits {
		return document.Change{}, document.ErrLimit
	}
	c := document.Change{DocumentID: p.snapshot.ID, Owner: owner, RequestID: requestID, BaseRevision: p.snapshot.Revision, GroupID: groupID, Edits: edits}
	p.owner = owner
	p.pending, p.pendingGroups = p.pending[n:], p.pendingGroups[n:]
	stored := c
	stored.Edits = append([]document.Edit(nil), edits...)
	p.inflight = &stored
	return c, nil
}

func (e *Editor) AcceptDocumentReceipt(r document.Receipt) error {
	p := e.documentProjection
	if p == nil || p.inflight == nil {
		return document.ErrRequest
	}
	c := p.inflight
	if r.DocumentID != c.DocumentID || r.RequestID != c.RequestID {
		return document.ErrRequest
	}
	if !r.Accepted && p.hold != nil && p.hold.applied {
		p.inflight = nil
		e.retireHeldEdit(c.Edits)
		if errors.Is(p.conflict, ErrDocumentPending) {
			p.conflict = nil
		}
		return fmt.Errorf("%w: %s", ErrHeldEditRefused, r.Error)
	}
	if !r.Accepted {
		groups := make([]string, len(c.Edits))
		for i := range groups {
			groups[i] = c.GroupID
		}
		p.pendingGroups = append(groups, p.pendingGroups...)
		p.pending = append(append([]document.Edit(nil), c.Edits...), p.pending...)
		// Captured edits have simultaneous coordinates; restore as sequential.
		delta := 0
		for i := range c.Edits {
			p.pending[i].Start += delta
			p.pending[i].End += delta
			delta += len(c.Edits[i].Text) - (c.Edits[i].End - c.Edits[i].Start)
		}
		p.inflight = nil
		p.conflict = errors.New(r.Error)
		e.CancelSnippet()
		return p.conflict
	}
	if r.Before != c.BaseRevision || r.Revision != r.Before+1 {
		return document.ErrRevision
	}
	text, _, err := document.Apply(p.snapshot.Text, c.Edits)
	if err != nil {
		return err
	}
	p.snapshot.Text = text
	p.snapshot.Revision = r.Revision
	p.inflight = nil
	if p.hold != nil && p.hold.applied {
		// The guarded edit committed at exactly the revision it described.
		p.hold = nil
	}
	if errors.Is(p.conflict, ErrDocumentPending) {
		p.conflict = nil
	}
	if p.onEdited != nil && len(p.pending) > 0 {
		p.onEdited()
	}
	return nil
}

func (e *Editor) requestDocumentUndo(redo bool) {
	p := e.documentProjection
	if p.conflict != nil && !errors.Is(p.conflict, ErrDocumentPending) {
		return
	}
	p.historyQueue = append(p.historyQueue, documentHistoryAction{redo: redo, viewID: e.historyID, evidence: DocumentHistoryIntent{Redo: redo}})
	e.DrainDocumentUndo()
	if len(p.historyQueue) > 0 {
		p.conflict = ErrDocumentPending
	}
}

// DrainDocumentUndo admits a queued keyboard gesture only after its preceding
// local edits have been acknowledged and the canonical revision is current.
func (e *Editor) DrainDocumentUndo() {
	p := e.documentProjection
	if p == nil || p.hold != nil || p.inflight != nil || p.undoPending || p.historySync || (p.conflict != nil && !errors.Is(p.conflict, ErrDocumentPending)) {
		return
	}
	for len(p.historyQueue) > 0 {
		action := p.historyQueue[0]
		if action.edit != nil {
			p.historyQueue = p.historyQueue[1:]
			p.replaying = true
			action.edit()
			p.replaying = false
			continue
		}
		if len(p.pending) > 0 || p.onUndo == nil || p.owner.ClientID == "" {
			return
		}
		p.historyQueue = p.historyQueue[1:]
		p.undoPending = true
		p.historyViewID = action.viewID
		p.undoSequence++
		p.conflict = nil
		p.onUndo(DocumentUndoIntent{p.snapshot.ID, p.owner, p.snapshot.Revision, action.redo, p.undoSequence, p})
		return
	}
}

// CompleteDocumentHistorySync is called only after the entire canonical sync,
// not after individual deltas of a multi-operation undo group.
func (e *Editor) CompleteDocumentHistorySync() {
	p := e.documentProjection
	if p == nil || !p.historySync {
		return
	}
	p.historySync = false
	if p.restoreView != nil {
		target := p.restoreEditor
		// A snapshot from a closed view must never move a surviving sibling.
		// An undo elsewhere only transforms each pane's current anchors.
		if target != nil && target.open && target.documentProjection == p &&
			(p.historyViewID == 0 || target.historyID == p.historyViewID) {
			target.restoreViewState(*p.restoreView)
			target.ensureCursorVisible()
		}
		p.restoreView, p.restoreEditor = nil, nil
	}
	p.historyViewID = 0
	e.DrainDocumentUndo()
}

// DeferDocumentUndoUntilSync returns an exact, not-yet-submitted intent to the
// front of the input queue. The coordinator owns command admission: it may use
// this only while awaiting an earlier publication, never for an active or unknown
// command. No receipt or history request identity has been assigned yet.
func (e *Editor) DeferDocumentUndoUntilSync(intent DocumentUndoIntent) error {
	p := e.documentProjection
	if p == nil || intent.Projection != p || !p.undoPending || intent.DocumentID != p.snapshot.ID || intent.Owner != p.owner || intent.ExpectedRevision != p.snapshot.Revision || intent.Sequence != p.undoSequence {
		return document.ErrRequest
	}
	action := documentHistoryAction{redo: intent.Redo, viewID: p.historyViewID, evidence: DocumentHistoryIntent{Redo: intent.Redo}}
	p.historyQueue = append([]documentHistoryAction{action}, p.historyQueue...)
	p.undoPending = false
	p.historySync = true
	p.historyRequestID = ""
	return nil
}

// BindDocumentOwner enables undo even before this view's first edit.
func (p *DocumentProjection) validateOwner(owner document.Owner) error {
	if p == nil || owner.ClientID == "" || owner.Epoch == 0 || (p.owner.ClientID != "" && (p.owner.ClientID != owner.ClientID || p.owner.Epoch > owner.Epoch || p.owner != owner && (p.inflight != nil || p.undoPending))) {
		return document.ErrOwner
	}
	return nil
}

func (e *Editor) BindDocumentOwner(owner document.Owner) error {
	if err := e.documentProjection.validateOwner(owner); err != nil {
		return err
	}
	if e.documentProjection.owner != owner {
		e.DismissGhostText()
	}
	if previous := e.documentProjection.owner; previous.ClientID != "" && previous != owner {
		e.CancelSnippet()
	}
	e.documentProjection.owner = owner
	return nil
}

// CompleteDocumentUndo reports a rejected undo without changing local bytes.
// Successful undo is installed through ApplyDocumentDelta.
func (e *Editor) CompleteDocumentUndo(intent DocumentUndoIntent, err error) error {
	if p := e.documentProjection; p != nil {
		if intent.Projection != p || !p.undoPending || intent.DocumentID != p.snapshot.ID || intent.Owner != p.owner || intent.ExpectedRevision != p.snapshot.Revision || intent.Sequence != p.undoSequence {
			return document.ErrRequest
		}
		p.undoPending = false
		p.historySync = true
		p.historyRedo = intent.Redo
		p.historyRequestID = ""
		// A definitive refusal is not a projection conflict. Sync must remain
		// available so another author's newer revision can be installed.
		p.conflict = nil
		return nil
	}
	return document.ErrOwner
}

// CompleteDocumentUndoReceipt retains exact command correlation for private
// caret restoration while the authoritative deltas are fetched.
func (e *Editor) CompleteDocumentUndoReceipt(intent DocumentUndoIntent, receipt document.Receipt, err error) error {
	if complete := e.CompleteDocumentUndo(intent, err); complete != nil {
		return complete
	}
	if err == nil {
		e.documentProjection.historyRequestID = receipt.RequestID
	}
	return nil
}

// AcceptDocumentSnapshot advances only the saved/disk metadata for a current
// canonical revision, retaining optimistic typing and all view state.
func (e *Editor) AcceptDocumentSnapshot(s document.Snapshot) error {
	p := e.documentProjection
	if p == nil || s.ID != p.snapshot.ID {
		return document.ErrOwner
	}
	if s.Revision != p.snapshot.Revision || !bytes.Equal(s.Text, p.snapshot.Text) {
		return document.ErrRevision
	}
	s.Text = bytes.Clone(s.Text)
	s.Saved = bytes.Clone(s.Saved)
	p.snapshot = s
	e.installDocumentBaseline(s)
	return nil
}

// ApplyDocumentDelta installs a committed command and its exact resulting
// snapshot. Overlap or an outstanding edit command fails closed without touching
// local bytes. Clients reconcile their own edit receipts before foreign deltas.
func (e *Editor) ApplyDocumentDelta(c document.Change, s document.Snapshot) error {
	p := e.documentProjection
	if p == nil || c.DocumentID != p.snapshot.ID || s.ID != p.snapshot.ID {
		return document.ErrOwner
	}
	if c.BaseRevision != p.snapshot.Revision || s.Revision != c.BaseRevision+1 {
		return document.ErrRevision
	}
	if p.inflight != nil {
		return ErrDocumentPending
	}
	canonical, _, err := document.Apply(p.snapshot.Text, c.Edits)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, s.Text) {
		return document.ErrRevision
	}
	if p.heldEditPending() {
		e.retireHeldEdit(composeDocumentEdits(len(p.snapshot.Text), p.pending))
	}
	remote := append([]document.Edit(nil), c.Edits...)
	pending := make([]document.Edit, 0, len(p.pending))
	for _, local := range p.pending {
		l, err := document.Rebase([]document.Edit{local}, remote)
		if err != nil {
			p.conflict = err
			return err
		}
		r, err := document.Rebase(remote, []document.Edit{local})
		if err != nil {
			p.conflict = err
			return err
		}
		pending = append(pending, l[0])
		remote = r
	}
	nextSnippet := prepareSnippetPeer(e.snippet, remote)
	e.snippetApplying = true
	e.commitDocumentDelta(c, remote, pending)
	e.snippetApplying = false
	e.commitSnippetPeer(nextSnippet)
	s.Text, s.Saved = bytes.Clone(s.Text), bytes.Clone(s.Saved)
	p.snapshot = s
	e.installDocumentBaseline(s)
	return nil
}

// commitDocumentDelta is infallible after complete batch admission.
func (e *Editor) commitDocumentDelta(c document.Change, remote, pending []document.Edit) {
	p := e.documentProjection
	if c.Owner != p.owner || !p.historySync || c.RequestID != p.historyRequestID {
		// Historical view positions are private, not canonical anchors. Do not
		// restore stale coordinates after a collaborator or formatter changes it.
		p.views = nil
		p.viewOrder = nil
		p.restoreView, p.restoreEditor = nil, nil
	} else {
		if view, ok := p.views[c.GroupID]; ok {
			state := view.before
			if p.historyRedo {
				state = view.after
			}
			p.restoreView, p.restoreEditor = &state, view.editor
		}
	}
	// Preserve logical view anchors by transforming their byte offsets across
	// precisely the remote edits applied to the optimistic local buffer.
	anchors := []Position{e.cursor, e.selAnchor, e.selEnd, {Line: e.scrollY}}
	if e.IsMouseDragging() {
		anchors = append(anchors, e.drag.start, e.drag.end)
	}
	offsets := make([]int, len(anchors))
	for i, a := range anchors {
		offsets[i] = e.buf.positionToOffset(a)
	}
	p.suppressed = true
	// This editor maps its own anchors below; linked views map theirs per
	// primitive through the table.
	previousActor := e.buf.linkActor
	e.buf.linkActor = e
	_ = e.applySourceOffsets(remote)
	e.buf.linkActor = previousActor
	p.suppressed = false
	selection := remapSourceSelection(SourceSelection{offsets[1], offsets[2]}, remote)
	for i, off := range offsets {
		offsets[i] = remapLinkedOffset(off, remote)
		for _, r := range remote {
			if off == r.Start && r.Start == r.End {
				offsets[i] += len(r.Text)
			}
		}
	}
	if e.primaryHasSelection() {
		offsets[1], offsets[2] = selection.Anchor, selection.Cursor
	}
	for i, off := range offsets {
		anchors[i] = e.buf.positionAtOffset(off)
	}
	e.cursor, e.selAnchor, e.selEnd = anchors[0], anchors[1], anchors[2]
	e.scrollY = anchors[3].Line
	if e.IsMouseDragging() {
		e.drag.start, e.drag.end = anchors[4], anchors[5]
	}
	e.normalizeCarets()
	p.pending = pending
	p.undoPending = false
	p.conflict = nil
	e.invalidateEditorCaches()
	e.remapGitProjection()
}

// DocumentPath reads canonical metadata without exporting text.
func (e *Editor) DocumentPath() string {
	if p := e.documentProjection; p != nil {
		return p.snapshot.Path
	}
	return ""
}

type preparedDocumentDelta struct {
	change          document.Change
	remote, pending []document.Edit
}

// PreparedDocumentUpdate is an Update-owned transaction. Preparation validates
// the entire canonical journal and optimistic rebases without exporting the live
// PieceTable or mutating text, history, metadata, owner, or cursor state.
type PreparedDocumentUpdate struct {
	projection              *DocumentProjection
	localRevision, revision uint64
	owner                   document.Owner
	snapshot                document.Snapshot
	deltas                  []preparedDocumentDelta
	reset, savepoint        bool
	snippetSource, snippet  *snippetSession
	snippetSequence         uint64
	// retire is a guarded edit that newer canonical text refuses.
	retire []document.Edit
}

func (e *Editor) PrepareDocumentUpdate(owner document.Owner, base uint64, changes []document.Change, state document.Snapshot, reset *document.Snapshot, savepoint bool) (*PreparedDocumentUpdate, error) {
	p := e.documentProjection
	if err := p.validateOwner(owner); err != nil {
		return nil, err
	}
	if p.inflight != nil || p.undoPending {
		return nil, ErrDocumentPending
	}
	if state.ID != p.snapshot.ID {
		return nil, document.ErrOwner
	}
	validPath := state.Path == "" && document.IsUntitledID(state.ID) && state.Name != "" ||
		filepath.IsAbs(state.Path) && filepath.Clean(state.Path) == state.Path
	if state.Revision == 0 || !validPath || len(state.Text) != 0 {
		return nil, document.ErrRevision
	}
	if len(state.Saved) > document.MaxBytes || !utf8.Valid(state.Saved) || !savepoint && len(state.Saved) != 0 {
		return nil, document.ErrLimit
	}
	plan := &PreparedDocumentUpdate{projection: p, localRevision: e.Revision(), revision: p.snapshot.Revision, owner: owner, snapshot: state, savepoint: savepoint}
	plan.snippetSource, plan.snippet, plan.snippetSequence = e.snippet, e.snippet, e.snippetSequence
	if state.Path != p.snapshot.Path || p.owner.ClientID != "" && p.owner != owner {
		plan.snippet = nil
	}
	if reset != nil {
		if len(changes) != 0 || reset.ID != state.ID || reset.Path != state.Path || reset.Revision != state.Revision || len(reset.Text) > document.MaxBytes || len(reset.Saved) > document.MaxBytes || !utf8.Valid(reset.Text) || !utf8.Valid(reset.Saved) {
			return nil, document.ErrRevision
		}
		if reset.Revision < p.snapshot.Revision {
			// An own receipt can advance the baseline before an initial publication.
			// Acknowledge that older publication, retaining today's draft and metadata.
			plan.snapshot = p.snapshot
			plan.savepoint = false
			return plan, nil
		}
		same := reset.Revision == p.snapshot.Revision && bytes.Equal(reset.Text, p.snapshot.Text)
		if !same && (len(p.pending) != 0 || p.historySync || len(p.historyQueue) != 0) {
			return nil, ErrDocumentPending
		}
		plan.snapshot = *reset
		plan.snapshot.Text, plan.snapshot.Saved = bytes.Clone(reset.Text), bytes.Clone(reset.Saved)
		plan.reset, plan.savepoint = !same, true
		if plan.reset {
			plan.snippet = nil
		}
		return plan, nil
	}
	if p.snapshot.Revision < base {
		return nil, document.ErrRevision
	}
	next := base
	text, pending := p.snapshot.Text, p.pending
	if p.heldEditPending() && state.Revision > p.snapshot.Revision {
		// Another author committed first. The guarded edit cannot commit at its
		// revision, so it leaves this view instead of being rebased.
		plan.retire = composeDocumentEdits(len(p.snapshot.Text), p.pending)
		pending, plan.snippet = nil, nil
	}
	for _, c := range changes {
		if c.DocumentID != state.ID || c.BaseRevision != next || next == ^uint64(0) {
			return nil, document.ErrRevision
		}
		next++
		if c.BaseRevision < p.snapshot.Revision {
			continue
		}
		var err error
		text, _, err = document.Apply(text, c.Edits)
		if err != nil {
			return nil, err
		}
		remote := c.Edits
		moved := make([]document.Edit, 0, len(pending))
		for _, local := range pending {
			l, err := document.Rebase([]document.Edit{local}, remote)
			if err != nil {
				return nil, err
			}
			r, err := document.Rebase(remote, []document.Edit{local})
			if err != nil {
				return nil, err
			}
			moved = append(moved, l[0])
			remote = r
		}
		// Own the small operation vectors; text is already immutable, built once.
		c.Edits = append([]document.Edit(nil), c.Edits...)
		plan.deltas = append(plan.deltas, preparedDocumentDelta{c, append([]document.Edit(nil), remote...), moved})
		plan.snippet = prepareSnippetPeer(plan.snippet, remote)
		pending = moved
	}
	if next != state.Revision {
		return nil, document.ErrRevision
	}
	if state.Revision < p.snapshot.Revision {
		// A command receipt may precede an older savepoint/delta publication.
		// Its canonical text is already installed; only the ordered savepoint is new.
		plan.snapshot = p.snapshot
		if savepoint {
			plan.snapshot.DiskHash, plan.snapshot.DiskExists = state.DiskHash, state.DiskExists
		}
		plan.snapshot.Encoding, plan.snapshot.SavedEncoding = state.Encoding, state.SavedEncoding
	}
	plan.snapshot.Text = text
	if savepoint {
		plan.snapshot.Saved = bytes.Clone(state.Saved)
	} else {
		plan.snapshot.Saved = p.snapshot.Saved
	}
	return plan, nil
}

// CommitDocumentUpdate checks the preparation's local generation before any
// mutation. The coordinator preflights every view/path before committing any.
func (e *Editor) CommitDocumentUpdate(plan *PreparedDocumentUpdate) error {
	p := e.documentProjection
	if plan == nil || p == nil || p != plan.projection || e.Revision() != plan.localRevision || p.snapshot.Revision != plan.revision || p.inflight != nil || p.undoPending ||
		e.snippet != plan.snippetSource || e.snippetSequence != plan.snippetSequence {
		return ErrDocumentPending
	}
	if err := p.validateOwner(plan.owner); err != nil {
		return err
	}
	p.owner = plan.owner
	if plan.retire != nil {
		e.retireHeldEdit(plan.retire)
	}
	e.snippetApplying = true
	if plan.reset {
		e.CancelPointerCapture()
		p.suppressed = true
		e.buf.ReplaceAll(string(plan.snapshot.Text))
		p.suppressed = false
		p.views, p.viewOrder, p.restoreView = nil, nil, nil
		p.conflict = nil
		e.invalidateEditorCaches()
		e.markDocumentWidthAll()
	} else {
		for _, delta := range plan.deltas {
			e.commitDocumentDelta(delta.change, delta.remote, delta.pending)
		}
	}
	e.snippetApplying = false
	e.commitSnippetPeer(plan.snippet)
	baselineChanged := p.snapshot.DiskExists != plan.snapshot.DiskExists || p.snapshot.DiskHash != plan.snapshot.DiskHash
	p.snapshot = plan.snapshot
	e.buffer.encoding, e.buffer.savedEncoding = plan.snapshot.Encoding, plan.snapshot.SavedEncoding
	if plan.savepoint || baselineChanged {
		e.installDocumentBaseline(plan.snapshot)
	} else if len(plan.deltas) != 0 {
		e.buffer.NoteEdit()
	}
	plan.projection = nil
	return nil
}
