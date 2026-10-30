package editor

import (
	"reflect"
	"strings"
	"testing"

	"crude/document"
	"crude/tui/cell"
)

func TestTypingPreservesDiagnosticPaintOnUnchangedSourceOnly(t *testing.T) {
	ed := loadEditorText(t, "diagnostic.go", "package main\nvar value = 1\nvar bad = missing\n")
	ed.SetCursorPosition(1, len("var value"))
	setTestDiagnostics(ed, []LSPDiagnostic{{Line: 2, Col: 10, EndLine: 2, EndCol: 17, Severity: DiagError, Message: "undefined: missing"}})
	before := paintEditor(ed, 80, 10)
	ed.InsertRune('x')
	after := paintEditor(ed, 80, 10)
	x := ed.gutterWidth() + 10
	if a, b := before.Get(x, 2), after.Get(x, 2); a.Attr != b.Attr || !a.FG.Equal(b.FG) {
		t.Fatalf("typing removed diagnostic ink on unchanged line: %+v -> %+v", a, b)
	}
	if len(ed.DiagnosticsAt(2, 10)) != 0 || ed.FirstError() != nil || ed.NextError(0, 0) != nil || ed.PrevError(99, 0) != nil {
		t.Fatal("provisional paint authorized stale diagnostic interaction")
	}
	ed.SetCursorPosition(2, 10)
	ed.InsertRune('x')
	if len(ed.lineDiagnostics(2)) != 0 {
		t.Fatal("edited line retained stale diagnostic range")
	}
}

func TestDiagnosticPaintMapsLineChangesAndClearsOnPublication(t *testing.T) {
	ed := loadEditorText(t, "mapped.go", "package main\nvar bad = missing\n")
	setTestDiagnostics(ed, []LSPDiagnostic{{Line: 1, Col: 10, EndLine: 1, EndCol: 17, Severity: DiagError}})
	ed.SetCursorPosition(0, 0)
	ed.InsertRune('\n')
	if len(ed.lineDiagnostics(2)) != 1 || len(ed.lineDiagnostics(1)) != 0 {
		t.Fatal("line insertion did not move diagnostic paint with unchanged source")
	}
	if len(ed.Diagnostics()) != 0 {
		t.Fatal("mapped paint escaped as current diagnostic data")
	}
	setTestDiagnostics(ed, nil)
	if len(ed.lineDiagnostics(2)) != 0 {
		t.Fatal("empty new publication retained provisional underline")
	}
	setTestDiagnostics(ed, []LSPDiagnostic{{Line: 2, Col: 10, EndLine: 2, EndCol: 17, Severity: DiagError}})
	ed.ApplyDisk([]byte("package replacement\n"))
	if len(ed.lineDiagnostics(2)) != 0 {
		t.Fatal("replacement retained unrelated diagnostic paint")
	}
}

func TestLSPDiagnosticsUTF16ColumnsReachVisualCharacter(t *testing.T) {
	ed := loadEditorText(t, "unicode.txt", "\t界é😀abc\r\nab\t界é😀XYZ\r\n")
	// First line: tab=4, 界=2, é=1, 😀=2, so UTF-16 6 is visual 9.
	// Second line: ab+tab=4, then 界é😀=5, X=1: UTF-16 9 is visual 10.
	wire := []LSPDiagnostic{{Line: 0, Col: 6, EndLine: 1, EndCol: 9, Severity: DiagError}}
	want := LSPDiagnostic{Line: 0, Col: 9, EndLine: 1, EndCol: 10, Severity: DiagError}
	for range 3 {
		setTestDiagnostics(ed, wire)
		if got := ed.Diagnostics(); len(got) != 1 || got[0] != want {
			t.Fatalf("multiline diagnostic endpoints must use their own source line: %+v", got)
		}
		if !reflect.DeepEqual(ed.buffer.Diagnostics().Diagnostics, wire) || wire[0].Col != 6 || wire[0].EndCol != 9 {
			t.Fatal("projection mutated raw UTF-16 cache")
		}
	}
	if got := ed.DiagnosticsAt(0, 9); len(got) != 1 || got[0] != want {
		t.Fatalf("diagnostic did not reach visual b: %+v", got)
	}
	if len(ed.DiagnosticsAt(1, 9)) != 1 || len(ed.DiagnosticsAt(1, 10)) != 0 {
		t.Fatal("multiline hover did not honor half-open end on destination line")
	}
	paint := paintEditor(ed, 80, 8)
	for _, point := range []Position{{Line: 0, Col: 9}, {Line: 0, Col: 11}, {Line: 1, Col: 4}, {Line: 1, Col: 9}} {
		if paint.Get(ed.gutterWidth()+point.Col, point.Line).Attr&cell.AttrUnderline == 0 {
			t.Fatalf("multiline diagnostic missing rendered underline at %+v", point)
		}
	}
	if paint.Get(ed.gutterWidth()+10, 1).Attr&cell.AttrUnderline != 0 {
		t.Fatal("multiline underline extended beyond destination endpoint")
	}
}

func TestLSPDiagnosticMultilinePaintHasBoundedExpansion(t *testing.T) {
	ed := loadEditorText(t, "bounded.txt", strings.Repeat("abc\n", 20_000))
	diags := make([]LSPDiagnostic, 100)
	for i := range diags {
		diags[i] = LSPDiagnostic{Line: i, Col: 1, EndLine: int(^uint(0) >> 1), EndCol: 1, Severity: DiagError}
	}
	setTestDiagnostics(ed, diags)
	total := 0
	for _, spans := range ed.diagByLine {
		total += len(spans)
	}
	if total > len(diags)+4096 {
		t.Fatalf("server range expanded beyond bounded paint budget: %d", total)
	}
	if len(ed.lineDiagnostics(19_999)) != 0 {
		t.Fatal("unbounded range expanded over complete source on Update")
	}
}

func TestLSPDiagnosticsCannotHoverOrPaintOldTextAfterEditAndUndo(t *testing.T) {
	ed := opsEditor(t, "main.go", "bad")
	diagnostics := []LSPDiagnostic{{Line: 0, EndLine: 0, EndCol: 3, Severity: DiagError, Message: "old source error"}}
	setTestDiagnostics(ed, diagnostics)
	old := ed.DiagnosticFacts()
	if len(ed.DiagnosticsAt(0, 0)) != 1 || len(ed.lineDiagnostics(0)) != 1 {
		t.Fatal("fresh diagnostics missing")
	}
	if err := ed.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	var intent DocumentUndoIntent
	ed.SetOnDocumentUndo(func(i DocumentUndoIntent) { intent = i })
	ed.InsertRune('x')
	ed.SetDiagnostics(old) // actual old publication arrives AFTER local mutation
	if len(ed.DiagnosticsAt(0, 0)) != 0 || len(ed.lineDiagnostics(0)) != 0 {
		t.Fatal("late publication attached to private draft")
	}
	change, err := ed.CaptureDocumentEdits(projectionOwner, "edit", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID, Before: 41, Revision: 42, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	ed.Undo()
	if intent.ExpectedRevision != 42 {
		t.Fatal("undo was not admitted at acknowledged revision")
	}
	snapshot := ed.DocumentSnapshot()
	snapshot.Revision, snapshot.Text = 43, []byte("bad")
	if err := ed.CompleteDocumentUndo(intent, nil); err != nil {
		t.Fatal(err)
	}
	if err := ed.ApplyDocumentDelta(document.Change{DocumentID: snapshot.ID, BaseRevision: 42, Edits: []document.Edit{{Start: 0, End: 1}}}, snapshot); err != nil {
		t.Fatal(err)
	}
	ed.CompleteDocumentHistorySync()
	ed.SetDiagnostics(old)
	if ed.Value() != "bad" || len(ed.DiagnosticsAt(0, 0)) != 0 || len(ed.lineDiagnostics(0)) != 0 {
		t.Fatal("equal-text undo revived old canonical revision")
	}
	setTestDiagnostics(ed, diagnostics)
	if len(ed.DiagnosticsAt(0, 0)) != 1 {
		t.Fatal("new exact-source diagnostics unavailable after undo")
	}
}
