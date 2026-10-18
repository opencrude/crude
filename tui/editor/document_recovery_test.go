package editor

import (
	"crude/document"
	"encoding/json"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
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

func TestDocumentRecoveryPreservesLogicalPendingGroups(t *testing.T) {
	e, _ := projectionEditor(t, "base")
	e.InsertRune('x')
	e.InsertRune('y')
	r, ok := e.ExportDocumentRecovery()
	if !ok || len(r.PendingGroups) != 2 || r.PendingGroups[0] == r.PendingGroups[1] {
		t.Fatalf("logical grouping missing: %+v", r.PendingGroups)
	}
	next, _ := projectionEditor(t, "base")
	if err := next.RestoreDocumentRecovery(r); err != nil {
		t.Fatal(err)
	}
	r.PendingGroups[0] = "mutated"
	next.ReconcileDocumentRevision()
	first := acknowledgeHistoryEdit(t, next, "recovered-first")
	if first.GroupID == "mutated" || first.Edits[0].Text != "x" || next.DocumentState().PendingEdits != 1 {
		t.Fatalf("restored grouping aliases or crosses commands: %+v", first)
	}
	r.PendingGroups = append(r.PendingGroups, "extra")
	if err := r.Validate(); !errors.Is(err, document.ErrRevision) {
		t.Fatalf("invalid grouping accepted: %v", err)
	}
}

func TestDocumentRecoveryRetainsDeferredUIIntentOrder(t *testing.T) {
	e, _ := projectionEditor(t, "base")
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	e.SetOnDocumentUndo(func(DocumentUndoIntent) {})
	e.Undo()
	e.HandleEvent(tea.KeyPressMsg{Code: 'x', Text: "x"})
	e.HandleEvent(tea.PasteMsg{Content: "界"})
	e.Redo()
	r, ok := e.ExportDocumentRecovery()
	if !ok || len(r.HistoryQueue) != 3 || r.HistoryQueue[0].Key == nil || r.HistoryQueue[1].Paste == nil || !r.HistoryQueue[2].Redo {
		t.Fatalf("intent order missing: %+v", r.HistoryQueue)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DocumentRecovery
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	next, _ := projectionEditor(t, "base")
	if err = next.RestoreDocumentRecovery(decoded); err != nil {
		t.Fatal(err)
	}
	// Recovery does not authorize replay; explicit reconciliation first.
	next.DrainDocumentUndo()
	if next.Value() != "base" {
		t.Fatal("recovered intent replayed before reconciliation")
	}
	var intents []DocumentUndoIntent
	next.SetOnDocumentUndo(func(i DocumentUndoIntent) { intents = append(intents, i) })
	i, ok := next.PendingDocumentUndoIntent(false)
	if !ok {
		t.Fatal("undo correlation missing")
	}
	if err = next.CompleteDocumentUndo(i, errors.New("empty history")); err != nil {
		t.Fatal(err)
	}
	next.CompleteDocumentHistorySync()
	if next.Value() != "x界base" || len(intents) != 0 {
		t.Fatalf("UI replay order lost: %q %+v", next.Value(), intents)
	}
	acknowledgeHistoryEdit(t, next, "key")
	acknowledgeHistoryEdit(t, next, "paste")
	next.DrainDocumentUndo()
	if len(intents) != 1 || !intents[0].Redo {
		t.Fatal("redo crossed deferred typing")
	}
}
