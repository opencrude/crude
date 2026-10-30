package editor

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"crude/document"
	"crude/tui/theme"
)

var projectionOwner = document.Owner{ClientID: "editor-test", Epoch: 1}

func projectionEditor(t *testing.T, text string) (*Editor, document.Snapshot) {
	t.Helper()
	s := document.Snapshot{ID: "/virtual/doc", Path: "/virtual/doc", Revision: 1, Text: []byte(text), Saved: []byte(text), DiskExists: true, DiskHash: sha256.Sum256([]byte(text))}
	e := New(theme.DefaultDark())
	t.Cleanup(e.Close)
	if err := e.InstallDocument(s); err != nil {
		t.Fatal(err)
	}
	return e, s
}

func TestDocumentProjectionUnicodeReceiptRetainsNewerTyping(t *testing.T) {
	e, s := projectionEditor(t, "é🙂 end")
	notifications, exports := 0, 0
	e.SetOnDocumentEdited(func() { notifications++ })
	e.SetOnContentChanged(func(string, string) { exports++ })
	e.SetCursorPosition(0, 3)
	e.InsertRune('界')
	c, err := e.CaptureDocumentEdits(projectionOwner, "one", "group")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Edits) != 1 || c.Edits[0].Start != len("é🙂") || c.Edits[0].Text != "界" {
		t.Fatalf("wrong byte edit: %+v", c)
	}
	e.InsertRune('x')
	if _, err = e.CaptureDocumentEdits(projectionOwner, "two", ""); !errors.Is(err, ErrDocumentPending) {
		t.Fatal(err)
	}
	bad := document.Receipt{DocumentID: s.ID, RequestID: "other", Before: 1, Revision: 2, Accepted: true}
	if err = e.AcceptDocumentReceipt(bad); !errors.Is(err, document.ErrRequest) {
		t.Fatal(err)
	}
	// Caller mutation must not change the retained immutable command.
	c.Edits[0].Text = "CORRUPTED"
	if err = e.AcceptDocumentReceipt(document.Receipt{DocumentID: s.ID, RequestID: "one", Before: 1, Revision: 2, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	if e.Value() != "é🙂界x end" || e.DocumentState().PendingEdits != 1 {
		t.Fatalf("lost typing: %q %+v", e.Value(), e.DocumentState())
	}
	second, err := e.CaptureDocumentEdits(projectionOwner, "two", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.BaseRevision != 2 || second.Edits[0].Start != len("é🙂界") || second.Edits[0].Text != "x" {
		t.Fatalf("bad newer edits: %+v", second)
	}
	if exports != 0 || notifications < 2 {
		t.Fatalf("notifications=%d full exports=%d", notifications, exports)
	}
	if e.watcher != nil {
		t.Fatal("canonical install opened a watcher")
	}
}

func TestDocumentProjectionTwoViewsRebaseAndConflict(t *testing.T) {
	a, s := projectionEditor(t, "abc\ndef\nghi")
	b, _ := projectionEditor(t, "abc\ndef\nghi")
	b.cursor = Position{Line: 2, Col: 2}
	b.selAnchor = Position{Line: 2, Col: 1}
	b.selEnd = Position{Line: 2, Col: 3}
	b.selecting = true
	b.scrollY = 2
	b.scrollX = 4
	_, _ = b.buf.Insert(Position{Line: 2, Col: 3}, []byte("!"))
	_, _ = a.buf.Insert(Position{Line: 0, Col: 1}, []byte("X\n"))
	c, err := a.CaptureDocumentEdits(projectionOwner, "foreign", "")
	if err != nil {
		t.Fatal(err)
	}
	s.Text, _, err = document.Apply(s.Text, c.Edits)
	if err != nil {
		t.Fatal(err)
	}
	s.Revision = 2
	if err = b.ApplyDocumentDelta(c, s); err != nil {
		t.Fatal(err)
	}
	if b.Value() != "aX\nbc\ndef\nghi!" || b.cursor != (Position{Line: 3, Col: 2}) || b.selAnchor != (Position{Line: 3, Col: 1}) || b.selEnd != (Position{Line: 3, Col: 3}) || b.scrollY != 3 || b.scrollX != 4 || !b.selecting {
		t.Fatalf("bad rebased view: %q cursor=%+v selection=%+v/%+v scroll=%d,%d", b.Value(), b.cursor, b.selAnchor, b.selEnd, b.scrollX, b.scrollY)
	}
	local, err := b.CaptureDocumentEdits(document.Owner{ClientID: "second", Epoch: 1}, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if local.Edits[0].Start != len("aX\nbc\ndef\nghi") {
		t.Fatalf("bad rebase %+v", local)
	}
	if err = b.AcceptDocumentReceipt(document.Receipt{DocumentID: s.ID, RequestID: "local", Before: 2, Revision: 3, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	s.Text = []byte(b.Value())
	s.Revision = 3
	_, _ = b.buf.Delete(Position{Line: 3, Col: 1}, Position{Line: 3, Col: 2})
	before := b.Value()
	foreign := document.Change{DocumentID: s.ID, BaseRevision: 3, Edits: []document.Edit{{Start: len("aX\nbc\ndef\ng"), End: len("aX\nbc\ndef\ngh"), Text: "Q"}}}
	s.Text, _, err = document.Apply(s.Text, foreign.Edits)
	if err != nil {
		t.Fatal(err)
	}
	s.Revision = 4
	if err = b.ApplyDocumentDelta(foreign, s); !errors.Is(err, document.ErrOverlap) {
		t.Fatal(err)
	}
	if b.Value() != before || !errors.Is(b.DocumentState().Conflict, document.ErrOverlap) {
		t.Fatal("overlap discarded local bytes")
	}
}

func TestDocumentProjectionAllMutationProducersAndDetach(t *testing.T) {
	e, _ := projectionEditor(t, "one")
	n := 0
	e.SetOnDocumentEdited(func() { n++ })
	e.ApplyFormatted("two\n界")
	c, err := e.CaptureDocumentEdits(projectionOwner, "format", "")
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := document.Apply([]byte("one"), c.Edits)
	if err != nil || string(got) != "two\n界" {
		t.Fatalf("%q %v", got, err)
	}
	if err = e.AcceptDocumentReceipt(document.Receipt{DocumentID: c.DocumentID, RequestID: c.RequestID, Before: 1, Revision: 2, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	e.ReplaceDocument("new")
	if n < 3 || e.DocumentState().PendingEdits == 0 {
		t.Fatalf("missed producers %d %+v", n, e.DocumentState())
	}
	replacement, err := e.CaptureDocumentEdits(projectionOwner, "replace", "")
	if err != nil {
		t.Fatal(err)
	}
	got, _, err = document.Apply([]byte("two\n界"), replacement.Edits)
	if err != nil || string(got) != "new" {
		t.Fatalf("replacement not captured exactly: %q %v", got, err)
	}
	beforeClose := n
	old := e.buf
	e.Close()
	old.ReplaceAll("detached")
	if n != beforeClose || old.mutationObserver != nil || e.DocumentState().DocumentID != "" {
		t.Fatalf("detached callback remains %d", n)
	}
	if err = e.AcceptDocumentReceipt(document.Receipt{DocumentID: c.DocumentID, RequestID: c.RequestID, Before: 1, Revision: 2, Accepted: true}); !errors.Is(err, document.ErrRequest) {
		t.Fatal(err)
	}
}

func TestDocumentProjectionUndoIntentAndSavepoint(t *testing.T) {
	e, s := projectionEditor(t, "abc")
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	var intent DocumentUndoIntent
	e.SetOnDocumentUndo(func(i DocumentUndoIntent) { intent = i })
	e.InsertRune('x')
	e.Undo()
	if e.Value() != "xabc" || !errors.Is(e.DocumentState().Conflict, ErrDocumentPending) {
		t.Fatal("pending undo discarded local typing")
	}
	c, err := e.CaptureDocumentEdits(projectionOwner, "edit", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.AcceptDocumentReceipt(document.Receipt{DocumentID: c.DocumentID, RequestID: c.RequestID, Before: 1, Revision: 2, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	e.DrainDocumentUndo()
	if !e.DocumentState().UndoPending || intent.ExpectedRevision != 2 || intent.Owner != projectionOwner || intent.Redo {
		t.Fatalf("bad undo intent %+v", intent)
	}
	undo := document.Change{DocumentID: s.ID, BaseRevision: 2, Edits: []document.Edit{{Start: 0, End: 1}}}
	s.Revision = 3
	if err := e.CompleteDocumentUndo(intent, nil); err != nil {
		t.Fatal(err)
	}
	if err = e.ApplyDocumentDelta(undo, s); err != nil {
		t.Fatal(err)
	}
	e.CompleteDocumentHistorySync()
	if e.Value() != "abc" || e.Modified() || e.DocumentState().UndoPending {
		t.Fatal("undo did not settle")
	}
	e.Redo()
	if !intent.Redo || intent.ExpectedRevision != 3 {
		t.Fatal("redo not routed")
	}
	if err := e.CompleteDocumentUndo(intent, document.ErrOverlap); err != nil {
		t.Fatal(err)
	}
	e.CompleteDocumentHistorySync()
	if e.DocumentState().Conflict != nil || e.Value() != "abc" {
		t.Fatal("failed undo changed bytes")
	}
	// A rejected author-specific undo must not block canonical sync; metadata-only
	// saves still retain local text.
	e.InsertRune('z')
	s.Saved = []byte("abc")
	s.DiskExists = false
	s.DiskHash = [32]byte{}
	if err = e.AcceptDocumentSnapshot(s); err != nil {
		t.Fatal(err)
	}
	if e.Value() != "zabc" || !e.Modified() || e.buffer.diskExists {
		t.Fatal("save metadata lost newer typing")
	}
}

func TestDocumentSaveReceiptAndPublicationShareDiskGeneration(t *testing.T) {
	e, s := projectionEditor(t, "base")
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	e.InsertRune('S')
	change, err := e.CaptureDocumentEdits(projectionOwner, "saved-edit", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AcceptDocumentReceipt(document.Receipt{DocumentID: s.ID, RequestID: change.RequestID, Before: 1, Revision: 2, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	s.Revision, s.Text, s.Saved = 2, []byte("Sbase"), []byte("Sbase")
	s.DiskHash = sha256.Sum256(s.Saved)
	if err := e.AcceptDocumentSnapshot(s); err != nil {
		t.Fatal(err)
	}
	generation, revision, cursor := e.DiskGeneration(), e.Revision(), e.cursor
	publish := func(snapshot document.Snapshot) {
		t.Helper()
		snapshot.Text = nil
		plan, err := e.PrepareDocumentUpdate(projectionOwner, 2, nil, snapshot, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.CommitDocumentUpdate(plan); err != nil {
			t.Fatal(err)
		}
	}
	publish(s) // The ordered topic follows the already-applied Save receipt.
	if e.DiskGeneration() != generation || e.Modified() || e.Value() != "Sbase" || e.Revision() != revision || e.cursor != cursor {
		t.Fatalf("identical save publication changed the editor's switch preimage: disk generation %d -> %d", generation, e.DiskGeneration())
	}
	s.Saved, s.DiskHash = []byte("base"), sha256.Sum256([]byte("base"))
	publish(s)
	if e.DiskGeneration() <= generation || !e.Modified() || e.Value() != "Sbase" {
		t.Fatal("a genuinely changed disk baseline did not invalidate its preimage")
	}
	changed := e.DiskGeneration()
	s.Saved, s.DiskHash = []byte("Sbase"), sha256.Sum256([]byte("Sbase"))
	publish(s)
	if e.DiskGeneration() <= changed || e.Modified() {
		t.Fatal("disk-baseline ABA revived a previously captured generation")
	}
}

func TestDocumentUndoDeferredPublicationRetainsExactGestureOrder(t *testing.T) {
	e, s := projectionEditor(t, "alpha")
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	var intents []DocumentUndoIntent
	e.SetOnDocumentUndo(func(intent DocumentUndoIntent) { intents = append(intents, intent) })
	e.InsertRune('X')
	change, err := e.CaptureDocumentEdits(projectionOwner, "edit", "group")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.AcceptDocumentReceipt(document.Receipt{DocumentID: s.ID, RequestID: "edit", Before: 1, Revision: 2, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	e.Undo()
	e.Redo()
	e.InsertRune('Q')
	first := intents[0]
	for _, field := range []string{"projection", "document", "owner", "revision", "sequence"} {
		wrong := first
		switch field {
		case "projection":
			wrong.Projection = nil
		case "document":
			wrong.DocumentID = "other"
		case "owner":
			wrong.Owner.Epoch++
		case "revision":
			wrong.ExpectedRevision++
		case "sequence":
			wrong.Sequence++
		}
		before, _ := e.ExportDocumentRecovery()
		if err = e.DeferDocumentUndoUntilSync(wrong); !errors.Is(err, document.ErrRequest) {
			t.Fatalf("%s admitted: %v", field, err)
		}
		after, _ := e.ExportDocumentRecovery()
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%s rejection changed private recovery", field)
		}
	}
	if err = e.DeferDocumentUndoUntilSync(first); err != nil {
		t.Fatal(err)
	}
	if err = e.DeferDocumentUndoUntilSync(first); !errors.Is(err, document.ErrRequest) {
		t.Fatal("same gesture deferred twice", err)
	}
	// The earlier publication also contains a peer edit. Admission must use its
	// resulting revision, not the revision captured by the retired intent.
	state := s
	state.Text, state.Saved, state.Revision = nil, nil, 3
	peer := document.Change{DocumentID: s.ID, BaseRevision: 2, Owner: document.Owner{ClientID: "peer", Epoch: 1}, RequestID: "peer", Edits: []document.Edit{{Start: 6, End: 6, Text: "!"}}}
	plan, err := e.PrepareDocumentUpdate(projectionOwner, 1, []document.Change{change, peer}, state, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.CommitDocumentUpdate(plan); err != nil {
		t.Fatal(err)
	}
	if e.Value() != "Xalpha!" || len(intents) != 1 {
		t.Fatal("publication replayed queued input before full commit")
	}
	e.CompleteDocumentHistorySync()
	if len(intents) != 2 || intents[1].Redo || intents[1].ExpectedRevision != 3 || intents[1].Sequence == first.Sequence {
		t.Fatalf("undo not re-admitted against publication: %+v", intents)
	}
	if err = e.CompleteDocumentUndo(first, nil); !errors.Is(err, document.ErrRequest) {
		t.Fatal("retired intent completed a later gesture", err)
	}
	if err = e.CompleteDocumentUndo(intents[1], document.ErrOverlap); err != nil {
		t.Fatal(err)
	}
	e.CompleteDocumentHistorySync()
	if len(intents) != 3 || !intents[2].Redo || e.Value() != "Xalpha!" {
		t.Fatal("redo or typing crossed the deferred undo")
	}
	if err = e.CompleteDocumentUndo(intents[2], document.ErrOverlap); err != nil {
		t.Fatal(err)
	}
	e.CompleteDocumentHistorySync()
	if e.Value() != "XQalpha!" || e.DocumentState().HistoryQueued {
		t.Fatalf("queued typing lost: %q %+v", e.Value(), e.DocumentState())
	}
}

func TestDocumentResetKeepsManagedBufferAndQueuedUndo(t *testing.T) {
	e, s := projectionEditor(t, "abc")
	manager := NewBufferManager()
	buffer := manager.InstallPrepared(e.DocumentBuffer())
	s.Revision, s.Text = 10, []byte("changed")
	if err := e.ResetDocument(s); err != nil {
		t.Fatal(err)
	}
	if manager.Get(s.Path) != e.DocumentBuffer() || buffer != e.DocumentBuffer() || e.Value() != "changed" {
		t.Fatal("reset detached managed live buffer")
	}
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	var intent DocumentUndoIntent
	e.SetOnDocumentUndo(func(i DocumentUndoIntent) { intent = i })
	e.InsertRune('X')
	e.Undo()
	c, err := e.CaptureDocumentEdits(projectionOwner, "queued", "queued")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AcceptDocumentReceipt(document.Receipt{DocumentID: s.ID, RequestID: c.RequestID, Before: 10, Revision: 11, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	e.DrainDocumentUndo()
	if !e.DocumentState().UndoPending || intent.ExpectedRevision != 11 {
		t.Fatal("pending undo gesture was lost")
	}
}

func TestComposeDocumentEditsRandomSequential(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 500; trial++ {
		base := []byte("abcdefghijklmnopqrstuvwxyz")
		value := bytes.Clone(base)
		var sequential []document.Edit
		for i := 0; i < 30; i++ {
			a := rng.Intn(len(value) + 1)
			b := a + rng.Intn(len(value)-a+1)
			text := strings.Repeat("X", rng.Intn(4))
			edit := document.Edit{Start: a, End: b, Text: text}
			sequential = append(sequential, edit)
			var err error
			value, _, err = document.Apply(value, []document.Edit{edit})
			if err != nil {
				t.Fatal(err)
			}
		}
		edits := composeDocumentEdits(len(base), sequential)
		if len(edits) == 0 {
			if !bytes.Equal(base, value) {
				t.Fatal("no-op composition wrong")
			}
			continue
		}
		got, _, err := document.Apply(base, edits)
		if err != nil || !bytes.Equal(got, value) {
			t.Fatalf("trial %d edits=%+v got=%q want=%q err=%v", trial, edits, got, value, err)
		}
	}
}

func TestDocumentProjectionRejectedReceiptAndOpenBufferDetach(t *testing.T) {
	e, _ := projectionEditor(t, "abcdefgh")
	_, _ = e.buf.Insert(Position{Col: 1}, []byte("X"))
	_, _ = e.buf.Insert(Position{Col: 7}, []byte("Y"))
	c, err := e.CaptureDocumentEdits(projectionOwner, "rejected", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.buf.Insert(Position{Col: 4}, []byte("Z"))
	before := e.Value()
	err = e.AcceptDocumentReceipt(document.Receipt{DocumentID: c.DocumentID, RequestID: c.RequestID, Before: 8, Revision: 8, Error: document.ErrRevision.Error()})
	if err == nil || e.Value() != before || e.DocumentState().InFlight {
		t.Fatal("rejection lost local work")
	}
	composed := composeDocumentEdits(8, e.documentProjection.pending)
	text, _, err := document.Apply([]byte("abcdefgh"), composed)
	if err != nil || string(text) != before {
		t.Fatalf("restored pending %q want %q: %v", text, before, err)
	}
	old := e.buf
	e.OpenBuffer(newBuffer(NewPieceTable(), "", false))
	if old.mutationObserver != nil || e.DocumentState().DocumentID != "" {
		t.Fatal("OpenBuffer retained projection")
	}
}

// The scheduling and capture path must not export a dense document. PieceTable
// rendering/position indexing has its own pre-existing caches and benchmarks.
func BenchmarkDocumentProjectionCaptureDense(b *testing.B) {
	for _, size := range []int{1024, 8 << 20} {
		b.Run(strings.Repeat("M", size>>20)+"bytes", func(b *testing.B) {
			e := New(theme.DefaultDark())
			defer e.Close()
			s := document.Snapshot{ID: "dense", Revision: 1, Text: bytes.Repeat([]byte("a"), size)}
			if err := e.InstallDocument(s); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p := e.documentProjection
				p.pending = []document.Edit{{Start: 1, End: 1, Text: "x"}}
				p.inflight = nil
				if _, err := e.CaptureDocumentEdits(projectionOwner, "capture", ""); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestDocumentUpdateAtomicAdmission(t *testing.T) {
	for _, invalid := range []string{"later-edit", "owner", "path", "revision", "overlap"} {
		t.Run(invalid, func(t *testing.T) {
			e, s := projectionEditor(t, "abc def")
			if err := e.BindDocumentOwner(projectionOwner); err != nil {
				t.Fatal(err)
			}
			e.SetCursorPosition(0, 7)
			e.InsertRune('P')
			before, _ := e.ExportDocumentRecovery()
			owner := projectionOwner
			state := s
			state.Text = nil
			state.Saved = nil
			state.Revision = 3
			changes := []document.Change{{DocumentID: s.ID, BaseRevision: 1, Edits: []document.Edit{{Start: 0, End: 0, Text: "X"}}}, {DocumentID: s.ID, BaseRevision: 2, Edits: []document.Edit{{Start: 2, End: 2, Text: "Y"}}}}
			switch invalid {
			case "later-edit":
				changes[1].Edits[0].Start = 99
				changes[1].Edits[0].End = 99
			case "owner":
				owner.ClientID = "foreign"
			case "path":
				state.Path = "relative"
			case "revision":
				changes[1].BaseRevision = 9
			case "overlap":
				changes[1].Edits[0].Start = 8
				changes[1].Edits[0].End = 8
			}
			if _, err := e.PrepareDocumentUpdate(owner, 1, changes, state, nil, false); err == nil {
				t.Fatal("invalid transaction admitted")
			}
			after, _ := e.ExportDocumentRecovery()
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("failed batch changed draft/history/owner: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestDocumentUpdatePendingAndPreparationFence(t *testing.T) {
	e, s := projectionEditor(t, "abc def")
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	e.SetCursorPosition(0, 7)
	e.InsertRune('P')
	state := s
	state.Text = nil
	state.Saved = nil
	state.Revision = 3
	changes := []document.Change{{DocumentID: s.ID, BaseRevision: 1, Edits: []document.Edit{{Start: 0, End: 0, Text: "X"}}}, {DocumentID: s.ID, BaseRevision: 2, Edits: []document.Edit{{Start: 2, End: 2, Text: "Y"}}}}
	plan, err := e.PrepareDocumentUpdate(projectionOwner, 1, changes, state, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	buffer := e.DocumentBuffer()
	if err = e.CommitDocumentUpdate(plan); err != nil {
		t.Fatal(err)
	}
	if e.Value() != "XaYbc defP" || e.DocumentState().PendingEdits != 1 || e.DocumentBuffer() != buffer {
		t.Fatalf("wrong projection: %q %+v", e.Value(), e.DocumentState())
	}
	plan, err = e.PrepareDocumentUpdate(projectionOwner, 3, nil, state, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	e.InsertRune('Q')
	if err = e.CommitDocumentUpdate(plan); !errors.Is(err, ErrDocumentPending) {
		t.Fatal(err)
	}
	if e.Value() != "XaYbc defPQ" {
		t.Fatal("stale plan lost typing")
	}
}

func TestDocumentUpdateOldInitialAndColdResetRetainPrivateWork(t *testing.T) {
	e, s := projectionEditor(t, "abc")
	e.InsertRune('X')
	c, err := e.CaptureDocumentEdits(projectionOwner, "own", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.AcceptDocumentReceipt(document.Receipt{DocumentID: s.ID, RequestID: c.RequestID, Before: 1, Revision: 2, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	e.InsertRune('P')
	state := s
	state.Text = nil
	state.Saved = nil
	plan, err := e.PrepareDocumentUpdate(projectionOwner, 0, nil, state, &s, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.CommitDocumentUpdate(plan); err != nil {
		t.Fatal(err)
	}
	if e.Value() != "XPabc" || e.DocumentState().Revision != 2 || e.DocumentState().PendingEdits != 1 {
		t.Fatal("old initial rolled back own receipt")
	}
	s.Revision = 9
	s.Text = []byte("cold")
	state.Revision = 9
	if _, err = e.PrepareDocumentUpdate(projectionOwner, 0, nil, state, &s, true); !errors.Is(err, ErrDocumentPending) {
		t.Fatal(err)
	}
	if e.Value() != "XPabc" {
		t.Fatal("cold reset lost draft")
	}
}

func FuzzDocumentUpdateAtomicBoundary(f *testing.F) {
	f.Add(0, 0, "x", 2, 2, "y")
	f.Add(1, 1, "x", 99, 99, "bad")
	f.Add(2, 2, "x", 1, 1, "bad")
	f.Fuzz(func(t *testing.T, a, b int, text string, c, d int, later string) {
		if len(text) > 128 || len(later) > 128 {
			t.Skip()
		}
		e, s := projectionEditor(t, "é🙂 abc")
		state := s
		state.Text = nil
		state.Saved = nil
		state.Revision = 3
		changes := []document.Change{{DocumentID: s.ID, BaseRevision: 1, Edits: []document.Edit{{Start: a, End: b, Text: text}}}, {DocumentID: s.ID, BaseRevision: 2, Edits: []document.Edit{{Start: c, End: d, Text: later}}}}
		expected, _, firstErr := document.Apply(s.Text, changes[0].Edits)
		var secondErr error
		if firstErr == nil {
			expected, _, secondErr = document.Apply(expected, changes[1].Edits)
		}
		plan, err := e.PrepareDocumentUpdate(projectionOwner, 1, changes, state, nil, false)
		if firstErr != nil || secondErr != nil {
			if err == nil || e.Value() != "é🙂 abc" || e.DocumentState().Revision != 1 {
				t.Fatal("invalid batch changed projection")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = e.CommitDocumentUpdate(plan); err != nil {
			t.Fatal(err)
		}
		if e.Value() != string(expected) || e.DocumentState().Revision != 3 {
			t.Fatalf("got %q expected %q", e.Value(), expected)
		}
	})
}

func BenchmarkDocumentUpdatePrepareDense(b *testing.B) {
	for _, size := range []int{1024, 8 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			e := New(theme.DefaultDark())
			defer e.Close()
			s := document.Snapshot{ID: "dense", Path: filepath.Join(b.TempDir(), "dense.txt"), Revision: 1, Text: bytes.Repeat([]byte("a"), size)}
			if err := e.InstallDocument(s); err != nil {
				b.Fatal(err)
			}
			if done := e.SyntaxDone(); done != nil {
				<-done
			}
			e.StopSyntax()
			state := s
			state.Text = nil
			state.Revision = 2
			changes := []document.Change{{DocumentID: s.ID, BaseRevision: 1, Edits: []document.Edit{{Start: 1, End: 2, Text: "X"}}}}
			e.documentProjection.pending = []document.Edit{{Start: size, End: size, Text: "P"}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := e.PrepareDocumentUpdate(projectionOwner, 1, changes, state, nil, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestDocumentReviewedRecoveryRebindsOnlyAfterDiscard(t *testing.T) {
	e, s := projectionEditor(t, "abc")
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	e.InsertRune('P')
	current := document.Owner{ClientID: "replacement-shell", Epoch: 1}
	if err := e.BindDocumentOwner(current); !errors.Is(err, document.ErrOwner) {
		t.Fatal("private original provenance rebound before review")
	}
	if err := e.UseSharedDocument(s, e.Revision()); err != nil {
		t.Fatal(err)
	}
	if err := e.BindDocumentOwner(current); err != nil {
		t.Fatal("reviewed recovery cannot resume current attachment", err)
	}
	e.InsertRune('Q')
	change, err := e.CaptureDocumentEdits(current, "after-review", "")
	if err != nil || change.Owner != current || len(change.Edits) != 1 || change.Edits[0].Text != "Q" {
		t.Fatalf("review did not resume fresh edits: %+v %v", change, err)
	}
}
