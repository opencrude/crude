package editor

import (
	"bytes"
	"errors"
	"path/filepath"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/document"
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
	replaying        bool
	restoreView      *editorViewState
	suppressed       bool
}

type documentGroupView struct{ before, after editorViewState }
type documentHistoryAction struct {
	edit     func()
	redo     bool
	evidence DocumentHistoryIntent
}

// DocumentHistoryIntent is private recovery evidence for ordered UI input, not
// permission to publish it against a different canonical document.
type DocumentHistoryIntent struct {
	Key           *tea.KeyPressMsg
	Paste         *tea.PasteMsg
	Redo          bool
	Unrecoverable bool
}

func (e *Editor) beginDocumentEditGroup(id string) {
	if p := e.documentProjection; p != nil {
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
		p.views[id] = documentGroupView{before: p.editGroupBefore}
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
	if p == nil || p.replaying || (!p.undoPending && !p.historySync && len(p.historyQueue) == 0) || (p.conflict != nil && !errors.Is(p.conflict, ErrDocumentPending)) {
		return false
	}
	p.historyQueue = append(p.historyQueue, documentHistoryAction{edit: edit, evidence: DocumentHistoryIntent{Unrecoverable: true}})
	return true
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
	DocumentID            string
	Revision              uint64
	PendingEdits          int
	InFlight, UndoPending bool
	HistoryQueued         bool
	Conflict              error
}

func (e *Editor) detachDocument() {
	if e.documentProjection != nil && e.buf != nil {
		e.buf.mutationObserver = nil
	}
	e.documentProjection = nil
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
	e.detachDocument()
	e.stopWatching()
	pt := NewPieceTable()
	pt.ReplaceAll(string(s.Text))
	pt.filePath = s.Path
	e.buf = pt
	e.buffer = newBuffer(pt, s.Path, s.DiskExists)
	e.filePath, e.title = s.Path, filepath.Base(s.Path)
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
	e.buffer.savepoint = snapshotBytes(s.Saved)
	e.buffer.diskHash, e.buffer.diskExists = s.DiskHash, s.DiskExists
	e.buffer.diskGeneration++
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
		return DocumentState{}
	}
	return DocumentState{DocumentID: p.snapshot.ID, Revision: p.snapshot.Revision, PendingEdits: len(p.pending), InFlight: p.inflight != nil, UndoPending: p.undoPending, HistoryQueued: p.historySync || len(p.historyQueue) > 0, Conflict: p.conflict}
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
	p.historyQueue = append(p.historyQueue, documentHistoryAction{redo: redo, evidence: DocumentHistoryIntent{Redo: redo}})
	e.DrainDocumentUndo()
	if len(p.historyQueue) > 0 {
		p.conflict = ErrDocumentPending
	}
}

// DrainDocumentUndo admits a queued keyboard gesture only after its preceding
// local edits have been acknowledged and the canonical revision is current.
func (e *Editor) DrainDocumentUndo() {
	p := e.documentProjection
	if p == nil || p.inflight != nil || p.undoPending || p.historySync || (p.conflict != nil && !errors.Is(p.conflict, ErrDocumentPending)) {
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
		e.restoreViewState(*p.restoreView)
		p.restoreView = nil
	}
	e.DrainDocumentUndo()
}

// BindDocumentOwner enables undo even before this view's first edit.
func (e *Editor) BindDocumentOwner(owner document.Owner) error {
	p := e.documentProjection
	if p == nil || owner.ClientID == "" || owner.Epoch == 0 || (p.owner.ClientID != "" && (p.owner.ClientID != owner.ClientID || p.owner.Epoch > owner.Epoch || p.owner != owner && (p.inflight != nil || p.undoPending))) {
		return document.ErrOwner
	}
	p.owner = owner
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
	if c.Owner != p.owner || !p.historySync || c.RequestID != p.historyRequestID {
		// Historical view positions are private, not canonical anchors. Do not
		// restore stale coordinates after a collaborator or formatter changes it.
		p.views = nil
		p.viewOrder = nil
		p.restoreView = nil
	} else {
		if view, ok := p.views[c.GroupID]; ok {
			state := view.before
			if p.historyRedo {
				state = view.after
			}
			p.restoreView = &state
		}
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
	for i := len(remote) - 1; i >= 0; i-- {
		r := remote[i]
		_, _ = e.buf.deleteOffsets(r.Start, r.End)
		_, _ = e.buf.insertAtOffset(r.Start, []byte(r.Text))
	}
	p.suppressed = false
	for i, off := range offsets {
		delta := 0
		for _, r := range remote {
			if off >= r.End {
				delta += len(r.Text) - (r.End - r.Start)
			} else if off >= r.Start {
				off = r.Start
				break
			}
		}
		offsets[i] = off + delta
	}
	for i, off := range offsets {
		anchors[i] = e.buf.positionAtOffset(off)
	}
	e.cursor, e.selAnchor, e.selEnd = anchors[0], anchors[1], anchors[2]
	e.scrollY = anchors[3].Line
	if e.IsMouseDragging() {
		e.drag.start, e.drag.end = anchors[4], anchors[5]
	}
	p.pending = pending
	s.Text = bytes.Clone(s.Text)
	s.Saved = bytes.Clone(s.Saved)
	p.snapshot = s
	p.undoPending = false
	p.conflict = nil
	e.installDocumentBaseline(s)
	e.invalidateEditorCaches()
	e.markDocumentWidthAll()
	e.remapGitProjection()
	return nil
}
