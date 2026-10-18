package editor

import (
	"bytes"
	"errors"
	"unicode/utf8"

	"crude/document"
)

// DocumentRecovery is detached evidence, never permission to replay a command.
// Pending coordinates are sequential; InFlight retains its original owner and
// request identity even when a new shell attachment has a different epoch.
type DocumentRecovery struct {
	Version                               int
	Path                                  string
	Canonical                             document.Snapshot
	OptimisticText                        []byte
	Pending                               []document.Edit
	PendingGroups                         []string
	HistoryQueue                          []DocumentHistoryIntent
	HistorySync                           bool
	InFlight                              *document.Change
	Owner                                 document.Owner
	Cursor, SelectionAnchor, SelectionEnd Position
	ScrollX, ScrollY                      int
	Selecting, UndoPending                bool
	Conflict                              string
	CommandKind, CommandRequestID         string
	CommandRedo                           bool
}

// PendingDocumentUndoIntent restores only correlation for receipt observation;
// it does not issue an undo or change optimistic bytes.
func (e *Editor) PendingDocumentUndoIntent(redo bool) (DocumentUndoIntent, bool) {
	p := e.documentProjection
	if p == nil || !p.undoPending {
		return DocumentUndoIntent{}, false
	}
	return DocumentUndoIntent{DocumentID: p.snapshot.ID, Owner: p.owner, ExpectedRevision: p.snapshot.Revision, Redo: redo, Sequence: p.undoSequence, Projection: p}, true
}

var ErrRecoveredDocument = errors.New("recovered unresolved private draft; reconcile original receipt and canonical text before publishing edits")

func (e *Editor) ExportDocumentRecovery() (DocumentRecovery, bool) {
	p := e.documentProjection
	if p == nil {
		return DocumentRecovery{}, false
	}
	r := DocumentRecovery{Version: 1, Path: e.filePath, Canonical: e.DocumentSnapshot(), OptimisticText: []byte(e.Value()), Pending: append([]document.Edit(nil), p.pending...), Owner: p.owner, Cursor: e.cursor, SelectionAnchor: e.selAnchor, SelectionEnd: e.selEnd, ScrollX: e.scrollX, ScrollY: e.scrollY, Selecting: e.selecting, UndoPending: p.undoPending}
	r.PendingGroups = append([]string(nil), p.pendingGroups...)
	r.HistorySync = p.historySync
	for _, action := range p.historyQueue {
		intent := action.evidence
		if intent.Key != nil {
			key := *intent.Key
			intent.Key = &key
		}
		if intent.Paste != nil {
			paste := *intent.Paste
			intent.Paste = &paste
		}
		r.HistoryQueue = append(r.HistoryQueue, intent)
	}
	if p.inflight != nil {
		c := *p.inflight
		c.Edits = append([]document.Edit(nil), c.Edits...)
		r.InFlight = &c
	}
	if p.conflict != nil {
		r.Conflict = p.conflict.Error()
	}
	return r, len(p.pending) > 0 || p.inflight != nil || p.undoPending || p.historySync || len(p.historyQueue) > 0 || p.conflict != nil
}

func (r DocumentRecovery) Validate() error {
	if len(r.PendingGroups) != 0 && len(r.PendingGroups) != len(r.Pending) {
		return document.ErrRevision
	}
	for _, intent := range r.HistoryQueue {
		if intent.Key != nil && intent.Paste != nil {
			return document.ErrRequest
		}
		if intent.Key != nil && (len(intent.Key.Text) > document.MaxBytes || !utf8.ValidString(intent.Key.Text)) {
			return document.ErrLimit
		}
		if intent.Paste != nil && (len(intent.Paste.Content) > document.MaxBytes || !utf8.ValidString(intent.Paste.Content)) {
			return document.ErrLimit
		}
	}
	if r.Version != 1 || r.Path == "" || r.Path != r.Canonical.Path || r.Canonical.ID == "" || r.Canonical.Revision == 0 || r.ScrollX < 0 || r.ScrollY < 0 {
		return document.ErrRevision
	}
	for _, text := range [][]byte{r.Canonical.Text, r.Canonical.Saved, r.OptimisticText} {
		if len(text) > document.MaxBytes || !utf8.Valid(text) {
			return document.ErrLimit
		}
	}
	text := bytes.Clone(r.Canonical.Text)
	var err error
	if r.InFlight != nil {
		c := r.InFlight
		if c.DocumentID != r.Canonical.ID || c.BaseRevision != r.Canonical.Revision || c.Owner != r.Owner || c.RequestID == "" {
			return document.ErrRequest
		}
		text, _, err = document.Apply(text, c.Edits)
		if err != nil {
			return err
		}
	}
	for _, edit := range r.Pending {
		text, _, err = document.Apply(text, []document.Edit{edit})
		if err != nil {
			return err
		}
	}
	if !bytes.Equal(text, r.OptimisticText) {
		return document.ErrRevision
	}
	for _, pos := range []Position{r.Cursor, r.SelectionAnchor, r.SelectionEnd} {
		if pos.Line < 0 || pos.Col < 0 {
			return document.ErrRevision
		}
	}
	return nil
}

// RestoreDocumentRecovery retains text for editing/copying but deliberately
// blocks automatic publication, including when today's canonical text matches.
// Receipt resolution belongs to the coordinator, not this value installation.
func (e *Editor) RestoreDocumentRecovery(r DocumentRecovery) error {
	if err := r.Validate(); err != nil {
		return err
	}
	for _, intent := range r.HistoryQueue {
		if intent.Unrecoverable {
			return errors.New("recovered editor contains an unavailable deferred API edit; retain the recovery evidence")
		}
	}
	p := e.documentProjection
	if p == nil || p.snapshot.Path != r.Path || p.snapshot.ID != r.Canonical.ID || len(p.pending) != 0 || p.inflight != nil {
		return ErrDocumentPending
	}
	matching := p.snapshot.Revision == r.Canonical.Revision && bytes.Equal(p.snapshot.Text, r.Canonical.Text)
	p.suppressed = true
	e.buf.ReplaceAll(string(r.OptimisticText))
	p.suppressed = false
	p.snapshot = r.Canonical
	p.snapshot.Text, p.snapshot.Saved = bytes.Clone(r.Canonical.Text), bytes.Clone(r.Canonical.Saved)
	p.pending = append([]document.Edit(nil), r.Pending...)
	p.pendingGroups = append([]string(nil), r.PendingGroups...)
	p.historySync = r.HistorySync
	p.historyQueue = nil
	for _, intent := range r.HistoryQueue {
		action := documentHistoryAction{redo: intent.Redo, evidence: intent}
		switch {
		case intent.Key != nil:
			key := *intent.Key
			action.evidence.Key = &key
			action.edit = func() { e.HandleEvent(key) }
		case intent.Paste != nil:
			paste := *intent.Paste
			action.evidence.Paste = &paste
			action.edit = func() { e.HandleEvent(paste) }
		}
		p.historyQueue = append(p.historyQueue, action)
	}
	p.owner, p.undoPending = r.Owner, r.UndoPending
	if r.InFlight != nil {
		c := *r.InFlight
		c.Edits = append([]document.Edit(nil), c.Edits...)
		p.inflight = &c
	}
	p.conflict = ErrRecoveredDocument
	if !matching {
		p.conflict = errors.New(ErrRecoveredDocument.Error() + "; canonical revision/text changed")
	}
	e.cursor, e.selAnchor, e.selEnd, e.selecting = r.Cursor, r.SelectionAnchor, r.SelectionEnd, r.Selecting
	e.scrollX, e.scrollY = r.ScrollX, r.ScrollY
	e.installDocumentBaseline(r.Canonical)
	e.invalidateEditorCaches()
	e.markDocumentWidthAll()
	return nil
}
