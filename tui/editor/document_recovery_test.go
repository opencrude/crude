package editor

import (
	"crude/document"
	"errors"
	"testing"
)

func TestDocumentRecoveryDetachedUnknownReceiptAndChangedCanonical(t *testing.T) {
	e, s := projectionEditor(t, "base")
	e.InsertRune('x')
	c, err := e.CaptureDocumentEdits(projectionOwner, "unknown", "group")
	if err != nil {
		t.Fatal(err)
	}
	e.InsertRune('y')
	e.selecting = true
	e.selAnchor = Position{}
	e.selEnd = Position{Col: 1}
	r, ok := e.ExportDocumentRecovery()
	if !ok {
		t.Fatal("missing recovery")
	}
	if err = r.Validate(); err != nil {
		t.Fatal(err)
	}
	if r.InFlight.RequestID != c.RequestID || string(r.OptimisticText) != "xybase" {
		t.Fatalf("lost evidence %+v", r)
	}
	r.InFlight.Edits[0].Text = "mutated"
	again, _ := e.ExportDocumentRecovery()
	if again.InFlight.Edits[0].Text != "x" {
		t.Fatal("export aliases command")
	}
	restored, _ := projectionEditor(t, "base")
	s.Revision = 9
	s.Text = []byte("other author")
	if err = restored.ResetDocument(s); err != nil {
		t.Fatal(err)
	}
	if err = restored.RestoreDocumentRecovery(again); err != nil {
		t.Fatal(err)
	}
	if restored.Value() != "xybase" || !restored.selecting || restored.DocumentState().Conflict == nil {
		t.Fatalf("lost recovered view: %s %+v", restored.Value(), restored.DocumentState())
	}
	if _, err = restored.CaptureDocumentEdits(projectionOwner, "new", "new"); err == nil || errors.Is(err, ErrDocumentPending) {
		t.Fatalf("replayed unknown outcome: %v", err)
	}
	restored.InsertRune('z')
	next, _ := restored.ExportDocumentRecovery()
	if err = next.Validate(); err != nil {
		t.Fatal(err)
	}
	if restored.Value() != "xyzbase" {
		t.Fatal("recovered draft is not editable")
	}
	if next.Owner != (document.Owner{ClientID: projectionOwner.ClientID, Epoch: projectionOwner.Epoch}) {
		t.Fatal("owner changed")
	}
}

func TestDocumentRecoveryRetainsUndoReceiptCorrelation(t *testing.T) {
	e, s := projectionEditor(t, "base")
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	e.SetOnDocumentUndo(func(DocumentUndoIntent) {})
	e.Undo()
	r, ok := e.ExportDocumentRecovery()
	if !ok || !r.UndoPending {
		t.Fatal("pending undo omitted")
	}
	r.CommandKind, r.CommandRequestID = "undo", "exact-unknown-undo"
	next, _ := projectionEditor(t, "base")
	if err := next.RestoreDocumentRecovery(r); err != nil {
		t.Fatal(err)
	}
	i, ok := next.PendingDocumentUndoIntent(false)
	if !ok || i.ExpectedRevision != s.Revision || i.Owner != projectionOwner {
		t.Fatal("undo correlation lost")
	}
	if err := next.CompleteDocumentUndo(i, nil); err != nil {
		t.Fatal(err)
	}
	if next.Value() != "base" || next.DocumentState().UndoPending {
		t.Fatal("receipt observation mutated text or left pending")
	}
}
