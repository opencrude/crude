package editor

import "testing"

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
