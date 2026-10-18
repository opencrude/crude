package editor

import (
	"errors"
	"testing"

	"crude/document"
)

func acknowledgeHistoryEdit(t *testing.T, e *Editor, request string) document.Change {
	t.Helper()
	c, err := e.CaptureDocumentEdits(projectionOwner, request, "fallback-"+request)
	if err != nil || c.DocumentID == "" {
		t.Fatalf("capture: %+v %v", c, err)
	}
	if err := e.AcceptDocumentReceipt(document.Receipt{DocumentID: c.DocumentID, RequestID: c.RequestID, Before: c.BaseRevision, Revision: c.BaseRevision + 1, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSharedHistoryCapturesLogicalGroupsNotDrainBatches(t *testing.T) {
	e, _ := projectionEditor(t, "base")
	e.beginUndoGroup()
	e.InsertRune('x')
	e.InsertRune('y')
	e.endUndoGroup()
	e.InsertRune('z')
	first := acknowledgeHistoryEdit(t, e, "first")
	if first.GroupID == "fallback-first" || len(first.Edits) != 1 || first.Edits[0].Text != "xy" || e.DocumentState().PendingEdits != 1 {
		t.Fatalf("first logical transaction: %+v %+v", first, e.DocumentState())
	}
	second := acknowledgeHistoryEdit(t, e, "second")
	if second.GroupID == first.GroupID || second.Edits[0].Text != "z" || second.Edits[0].Start != 2 || e.DocumentSnapshot().Revision != 3 || string(e.DocumentSnapshot().Text) != "xyzbase" {
		t.Fatalf("second logical transaction: %+v", second)
	}
}

func TestSharedHistoryRejectedReceiptRetainsGroupBoundaries(t *testing.T) {
	e, _ := projectionEditor(t, "base")
	e.InsertRune('x')
	first, err := e.CaptureDocumentEdits(projectionOwner, "rejected", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	e.InsertRune('y')
	if err = e.AcceptDocumentReceipt(document.Receipt{DocumentID: first.DocumentID, RequestID: first.RequestID, Error: document.ErrRevision.Error()}); err == nil {
		t.Fatal("rejection accepted")
	}
	e.ReconcileDocumentRevision()
	retry := acknowledgeHistoryEdit(t, e, "retry")
	if retry.GroupID != first.GroupID || retry.Edits[0].Text != "x" || e.DocumentState().PendingEdits != 1 {
		t.Fatalf("retry crossed transaction: %+v", retry)
	}
	second := acknowledgeHistoryEdit(t, e, "second")
	if second.GroupID == retry.GroupID || second.Edits[0].Text != "y" {
		t.Fatalf("second group lost %+v", second)
	}
}

func TestSharedHistoryQueuesEveryGestureAndFencesTypingUntilSync(t *testing.T) {
	e, s := projectionEditor(t, "base")
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	var intents []DocumentUndoIntent
	e.SetOnDocumentUndo(func(i DocumentUndoIntent) { intents = append(intents, i) })
	e.InsertRune('x')
	e.Undo()
	e.Undo()
	e.Redo()
	e.InsertRune('z')
	if e.Value() != "xbase" || len(intents) != 0 {
		t.Fatal("typing crossed queued undo")
	}
	edit := acknowledgeHistoryEdit(t, e, "edit")
	e.DrainDocumentUndo()
	if len(intents) != 1 || intents[0].Redo {
		t.Fatal("first undo lost")
	}
	if err := e.CompleteDocumentUndoReceipt(intents[0], document.Receipt{RequestID: "undo-1"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CaptureDocumentEdits(projectionOwner, "forbidden", "group"); !errors.Is(err, ErrDocumentPending) {
		t.Fatalf("capture before sync: %v", err)
	}
	s.Revision = 3
	delta := document.Change{DocumentID: s.ID, RequestID: "undo-1", Owner: projectionOwner, BaseRevision: 2, GroupID: edit.GroupID, Edits: []document.Edit{{Start: 0, End: 1}}}
	if err := e.ApplyDocumentDelta(delta, s); err != nil {
		t.Fatal(err)
	}
	if e.Value() != "base" || len(intents) != 1 {
		t.Fatal("queue drained before entire sync")
	}
	e.CompleteDocumentHistorySync()
	if len(intents) != 2 || intents[1].Redo {
		t.Fatal("repeated undo lost")
	}
	if err := e.CompleteDocumentUndo(intents[1], errors.New("empty history")); err != nil {
		t.Fatal(err)
	}
	e.CompleteDocumentHistorySync()
	if len(intents) != 3 || !intents[2].Redo {
		t.Fatal("redo order lost")
	}
	if err := e.CompleteDocumentUndoReceipt(intents[2], document.Receipt{RequestID: "redo-1"}, nil); err != nil {
		t.Fatal(err)
	}
	s.Revision = 4
	s.Text = []byte("xbase")
	delta.BaseRevision = 3
	delta.RequestID = "redo-1"
	delta.Edits = []document.Edit{{Start: 0, End: 0, Text: "x"}}
	if err := e.ApplyDocumentDelta(delta, s); err != nil {
		t.Fatal(err)
	}
	e.CompleteDocumentHistorySync()
	if e.Value() != "xzbase" || e.CursorCol() != 2 || e.DocumentState().PendingEdits != 1 || e.DocumentState().HistoryQueued {
		t.Fatalf("post-redo intent: %q %+v", e.Value(), e.DocumentState())
	}
}

func TestSharedHistoryTypingBetweenGesturesPreservesOrder(t *testing.T) {
	e, _ := projectionEditor(t, "base")
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	var intents []DocumentUndoIntent
	e.SetOnDocumentUndo(func(i DocumentUndoIntent) { intents = append(intents, i) })
	e.Undo()
	e.InsertRune('x')
	e.Undo()
	if err := e.CompleteDocumentUndo(intents[0], errors.New("empty")); err != nil {
		t.Fatal(err)
	}
	e.CompleteDocumentHistorySync()
	if e.Value() != "xbase" || len(intents) != 1 {
		t.Fatal("second undo preceded its edit")
	}
	acknowledgeHistoryEdit(t, e, "middle")
	e.DrainDocumentUndo()
	if len(intents) != 2 || intents[1].ExpectedRevision != 2 {
		t.Fatalf("second undo missing %+v", intents)
	}
}

func TestSharedHistoryRetainsViewsOnlyForBoundedActualTransactions(t *testing.T) {
	e, _ := projectionEditor(t, "base")
	e.maxHistory = 2
	for range 10 {
		e.beginUndoGroup()
		e.endUndoGroup()
	}
	if len(e.documentProjection.views) != 0 {
		t.Fatal("navigation retained history views")
	}
	for range 3 {
		e.InsertRune('x')
	}
	if len(e.documentProjection.views) != 2 || len(e.documentProjection.viewOrder) != 2 {
		t.Fatal("private history view retention unbounded")
	}
}
