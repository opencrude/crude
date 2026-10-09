package editor

import (
	"bytes"
	"errors"
	"path/filepath"
	"unicode/utf8"

	"crude/document"
)

var ErrDocumentPending = errors.New("document command pending; retain edits and retry after acknowledgement")

// DocumentProjection belongs to one Editor lifetime on the UI goroutine.
// Pending mutations use sequential byte coordinates; wire commands use one
// immutable pre-change coordinate system.
type DocumentProjection struct {
	snapshot     document.Snapshot
	pending      []document.Edit
	inflight     *document.Change
	owner        document.Owner
	conflict     error
	onEdited     func()
	onUndo       func(DocumentUndoIntent)
	undoPending  bool
	undoSequence uint64
	queuedUndo   *bool
	suppressed   bool
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
	if e.documentProjection != nil && (len(e.documentProjection.pending) > 0 || e.documentProjection.inflight != nil || e.documentProjection.undoPending) {
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
	p := &DocumentProjection{snapshot: s}
	e.documentProjection = p
	pt.mutationObserver = func(edit document.Edit) {
		if p.suppressed {
			return
		}
		p.pending = append(p.pending, edit)
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
	return DocumentState{p.snapshot.ID, p.snapshot.Revision, len(p.pending), p.inflight != nil, p.undoPending, p.conflict}
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
	p.suppressed = true
	e.buf.ReplaceAll(string(s.Text))
	p.suppressed = false
	s.Text, s.Saved = bytes.Clone(s.Text), bytes.Clone(s.Saved)
	p.snapshot, p.conflict = s, nil
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
	p.pending, p.queuedUndo = nil, nil
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
	if p.inflight != nil || p.undoPending {
		return document.Change{}, ErrDocumentPending
	}
	if owner.ClientID == "" || owner.Epoch == 0 || requestID == "" || (p.owner.ClientID != "" && p.owner != owner) {
		return document.Change{}, document.ErrOwner
	}
	edits := composeDocumentEdits(len(p.snapshot.Text), p.pending)
	if len(edits) == 0 {
		p.pending = nil
		return document.Change{}, nil
	}
	if len(edits) > document.MaxEdits {
		return document.Change{}, document.ErrLimit
	}
	c := document.Change{DocumentID: p.snapshot.ID, Owner: owner, RequestID: requestID, BaseRevision: p.snapshot.Revision, GroupID: groupID, Edits: edits}
	p.owner = owner
	p.pending = nil
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
	if len(p.pending) > 0 || p.inflight != nil || p.undoPending || p.onUndo == nil || p.owner.ClientID == "" {
		if p.onUndo != nil && p.owner.ClientID != "" && !p.undoPending {
			p.queuedUndo = &redo
		}
		p.conflict = ErrDocumentPending
		return
	}
	p.undoPending = true
	p.undoSequence++
	p.conflict = nil
	p.onUndo(DocumentUndoIntent{p.snapshot.ID, p.owner, p.snapshot.Revision, redo, p.undoSequence, p})
}

// DrainDocumentUndo admits a queued keyboard gesture only after its preceding
// local edits have been acknowledged and the canonical revision is current.
func (e *Editor) DrainDocumentUndo() {
	p := e.documentProjection
	if p == nil || p.queuedUndo == nil || len(p.pending) != 0 || p.inflight != nil || p.undoPending {
		return
	}
	redo := *p.queuedUndo
	p.queuedUndo = nil
	e.requestDocumentUndo(redo)
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
		// A definitive refusal is not a projection conflict. Sync must remain
		// available so another author's newer revision can be installed.
		p.conflict = nil
		return nil
	}
	return document.ErrOwner
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
	offsets := make([]int, len(anchors))
	for i, a := range anchors {
		offsets[i] = e.buf.positionToOffset(a)
	}
	p.suppressed = true
	for i := len(remote) - 1; i >= 0; i-- {
		r := remote[i]
		e.buf.ensureCache()
		text := string(e.buf.cachedBytes)
		a, b := offsetToLineCol(text, r.Start, r.End)
		_, _ = e.buf.Delete(a, b)
		_, _ = e.buf.Insert(a, []byte(r.Text))
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
	e.buf.ensureCache()
	text := string(e.buf.cachedBytes)
	for i, off := range offsets {
		line, col := ByteOffsetToLineCol(text, off, tabWidth)
		anchors[i] = Position{Line: line, Col: col}
	}
	e.cursor, e.selAnchor, e.selEnd = anchors[0], anchors[1], anchors[2]
	e.scrollY = anchors[3].Line
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
