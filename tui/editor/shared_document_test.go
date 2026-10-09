package editor

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"math/rand"
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
	if n < 3 || e.DocumentState().PendingEdits != 1 {
		t.Fatalf("missed producers %d %+v", n, e.DocumentState())
	}
	old := e.buf
	e.Close()
	old.ReplaceAll("detached")
	if n != 3 || old.mutationObserver != nil || e.DocumentState().DocumentID != "" {
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
	e.Undo()
	if !e.DocumentState().UndoPending || intent.ExpectedRevision != 2 || intent.Owner != projectionOwner || intent.Redo {
		t.Fatalf("bad undo intent %+v", intent)
	}
	undo := document.Change{DocumentID: s.ID, BaseRevision: 2, Edits: []document.Edit{{Start: 0, End: 1}}}
	s.Revision = 3
	if err = e.ApplyDocumentDelta(undo, s); err != nil {
		t.Fatal(err)
	}
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
	if !errors.Is(e.DocumentState().Conflict, document.ErrOverlap) || e.Value() != "abc" {
		t.Fatal("failed undo changed bytes")
	}
	// Rejected undo remains explicitly conflicted until the client resolves it
	// through a canonical delta; metadata-only saves still retain local text.
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
