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
