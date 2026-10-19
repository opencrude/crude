package editor

import "testing"

func TestTypingPreservesDiagnosticPaintOnUnchangedSourceOnly(t *testing.T) {
	ed := loadEditorText(t, "diagnostic.go", "package main\nvar value = 1\nvar bad = missing\n")
	ed.SetCursorPosition(1, len("var value"))
	ed.SetDiagnostics([]LSPDiagnostic{{Line: 2, Col: 10, EndLine: 2, EndCol: 17, Severity: DiagError, Message: "undefined: missing"}})
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
	ed.SetDiagnostics([]LSPDiagnostic{{Line: 1, Col: 10, EndLine: 1, EndCol: 17, Severity: DiagError}})
	ed.SetCursorPosition(0, 0)
	ed.InsertRune('\n')
	if len(ed.lineDiagnostics(2)) != 1 || len(ed.lineDiagnostics(1)) != 0 {
		t.Fatal("line insertion did not move diagnostic paint with unchanged source")
	}
	if len(ed.Diagnostics()) != 0 {
		t.Fatal("mapped paint escaped as current diagnostic data")
	}
	ed.SetDiagnostics(nil)
	if len(ed.lineDiagnostics(2)) != 0 {
		t.Fatal("empty new publication retained provisional underline")
	}
	ed.SetDiagnostics([]LSPDiagnostic{{Line: 2, Col: 10, EndLine: 2, EndCol: 17, Severity: DiagError}})
	ed.ApplyDisk([]byte("package replacement\n"))
	if len(ed.lineDiagnostics(2)) != 0 {
		t.Fatal("replacement retained unrelated diagnostic paint")
	}
}

func TestLSPDiagnosticsUTF16ColumnsReachVisualCharacter(t *testing.T) {
	ed := editorWithTheme()
	ed.buf = NewPieceTable()
	ed.buf.ReplaceAll("a😀bc")
	ed.SetDiagnostics([]LSPDiagnostic{{Line: 0, Col: 3, EndLine: 0, EndCol: 4}})
	if got := ed.DiagnosticsAt(0, 3); len(got) != 1 || got[0].Col != 3 || got[0].EndCol != 4 {
		t.Fatalf("diagnostic did not reach visual b: %+v", got)
	}
}

func TestLSPDiagnosticsCannotHoverOrPaintOldTextAfterEditAndUndo(t *testing.T) {
	ed := opsEditor(t, "main.go", "bad")
	diagnostics := []LSPDiagnostic{{Line: 0, EndLine: 0, EndCol: 3, Severity: DiagError, Message: "old source error"}}
	ed.SetDiagnostics(diagnostics)
	if len(ed.DiagnosticsAt(0, 0)) != 1 || len(ed.lineDiagnostics(0)) != 1 {
		t.Fatal("fresh diagnostics missing")
	}
	ed.InsertRune('x')
	if len(ed.DiagnosticsAt(0, 0)) != 0 || len(ed.lineDiagnostics(0)) != 0 {
		t.Fatal("old diagnostics attached to changed source")
	}
	ed.SetDiagnostics(diagnostics)
	ed.Undo()
	if len(ed.DiagnosticsAt(0, 0)) != 0 || len(ed.lineDiagnostics(0)) != 0 {
		t.Fatal("undo reused diagnostics for a different source revision")
	}
	ed.SetDiagnostics(diagnostics)
	if len(ed.DiagnosticsAt(0, 0)) != 1 {
		t.Fatal("new diagnostics never became available after undo")
	}
}
