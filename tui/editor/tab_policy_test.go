package editor

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/theme"
	"crude/types"
)

func TestTabPolicyPreservesLinkedSourceAndHistory(t *testing.T) {
	source := "\t界é\tname\r\n\tother\r\n"
	e := loadEditorText(t, "width.txt", source)
	paintEditor(e, 80, 12)
	e.SetCursorPosition(0, 12)
	e.SetSelection(0, 8, 0, 12)
	peer := e.NewLinkedView()
	t.Cleanup(peer.Close)
	peer.SetCursorPosition(1, 6)
	before := e.Revision()
	for _, width := range []int{2, 8, 4, 2} {
		e.SetPresentation(types.EditorSettings{TabSize: width, LineNumbers: true})
		wantStart, wantEnd := 6, 10
		if width == 8 {
			wantStart, wantEnd = 16, 20
		}
		if width == 4 {
			wantStart, wantEnd = 8, 12
		}
		sl, sc, el, ec := e.SelectionRange()
		if sl != 0 || el != 0 || sc != wantStart || ec != wantEnd || e.SelectedText() != "name" || e.Value() != source || e.Revision() != before || peer.CursorCol() != width+2 {
			t.Fatalf("width %d moved source: selection=%d:%d-%d:%d %q peer=%d", width, sl, sc, el, ec, e.SelectedText(), peer.CursorCol())
		}
		for _, view := range []*Editor{e, peer} {
			b := paintEditor(view, 80, 12)
			if got := b.Get(view.GutterWidth()+width, 0).Char; got != '界' {
				t.Fatalf("width %d painted %q instead of source", width, got)
			}
		}
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	e.SetPresentation(types.EditorSettings{TabSize: 8, LineNumbers: true})
	e.Undo()
	if e.Value() != source || e.SelectedText() != "name" || e.CursorCol() != 20 {
		t.Fatal("Undo restored old display columns", e.Value(), e.SelectedText(), e.CursorCol())
	}
	e.SetPresentation(types.EditorSettings{TabSize: 2, LineNumbers: true})
	e.Redo()
	if e.Value() != "\t界é\tZ\r\n\tother\r\n" || e.CursorCol() != 7 {
		t.Fatal("Redo did not restore the same source caret", e.Value(), e.CursorCol())
	}
	e.Undo()
	if e.Value() != source || e.Modified() {
		t.Fatal("width changes entered document history")
	}
}

func TestTabPolicyReprojectsSemanticAndInlineGeometry(t *testing.T) {
	e := newInlayTestEditor(t, "\tfoo \t界")
	e.SetCursorPosition(0, 5)
	installTestInlays(t, e, types.LSPInlayHint{Index: 0, Position: types.LSPInlayPosition{Character: 1}, SourceByte: 1, Label: "hint", PaddingRight: true})
	e.SetDiagnostics(testDiagnosticFacts(e, []LSPDiagnostic{{Line: 0, Col: 1, EndLine: 0, EndCol: 4, Severity: DiagError, Message: "source"}}))
	e.SetQuery("foo")
	for _, width := range []int{2, 8} {
		e.SetPresentation(types.EditorSettings{TabSize: width, LineNumbers: true, RenderWhitespace: "all"})
		if e.CursorCol() != width+1 {
			t.Fatal("caret source changed", e.CursorCol())
		}
		line, col := e.LSPCursorPosition()
		if line != 0 || col != 2 {
			t.Fatal("wire coordinate used tab width", line, col)
		}
		match := e.Search().Matches[0]
		if match.Start.Col != width || match.End.Col != width+3 {
			t.Fatal("search retained old display columns", match)
		}
		ds := e.Diagnostics()
		if len(ds) != 1 || ds[0].Col != width || ds[0].EndCol != width+3 {
			t.Fatal("diagnostics retained old display columns", ds)
		}
		b := renderTestInlays(e, 80, 8, false)
		x := e.GutterWidth()
		if b.Get(x, 0).Char != '→' || b.Get(x+width, 0).Char != 'h' || b.Get(x+width+5, 0).Char != 'f' {
			t.Fatal("hint/source geometry drifted")
		}
	}
	e.SetSearchOpen(false)
	start, _, _ := e.SourceByteRange()
	if err := e.SetGhostText(e.Revision(), start, "Extra"); err != nil {
		t.Fatal(err)
	}
	e.SetPresentation(types.EditorSettings{TabSize: 2, LineNumbers: true})
	if anchor, text, ok := e.GhostText(); !ok || anchor != start || text != "Extra" {
		t.Fatal("geometry lost the current ghost", anchor, text, ok)
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "\tfExtraoo \t界" {
		t.Fatal("ghost accepted at wrong source", e.Value())
	}
}

func TestTabPolicyRecoveryUsesSourceAnchors(t *testing.T) {
	e, _ := projectionEditor(t, "\tword")
	e.SetCursorPosition(0, 5)
	e.InsertRune('X')
	e.SetCursorPosition(0, 9)
	e.SetSelection(0, 4, 0, 9)
	e.SetPresentation(types.EditorSettings{TabSize: 8, LineNumbers: true})
	r, ok := e.ExportDocumentRecovery()
	if !ok {
		t.Fatal("missing unresolved draft")
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DocumentRecovery
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	next, _ := projectionEditor(t, "\tword")
	next.SetPresentation(types.EditorSettings{TabSize: 2, LineNumbers: true})
	if err = next.RestoreDocumentRecovery(decoded); err != nil {
		t.Fatal(err)
	}
	if next.Value() != "\twXord" || next.SelectedText() != "wXord" || next.CursorCol() != 7 || next.DocumentState().Conflict == nil {
		t.Fatal("recovery reinterpreted old columns", next.Value(), next.SelectedText(), next.CursorCol())
	}
}

func TestTabPolicyNavigationSnapshotUsesSource(t *testing.T) {
	e := loadEditorText(t, "navigation.txt", "\t界é\tname\n")
	e.SetCursorPosition(0, 10)
	place, scroll := e.Caret()
	e.SetPresentation(types.EditorSettings{TabSize: 8, LineNumbers: true})
	e.SetCursorPosition(0, 0)
	e.RestoreCaret(place, scroll)
	if e.CursorCol() != 18 {
		t.Fatal("history location used old columns", e.CursorCol())
	}
}

func BenchmarkTabPolicyStableNavigation(b *testing.B) {
	e := NewPreview(theme.DefaultDark(), "line.txt", "\t"+strings.Repeat("x", 2<<20), 1)
	defer e.Close()
	e.SetCursorPosition(0, 1000000)
	e.Caret()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		e.Caret()
	}
}

func TestTabPolicyExternalHighlightsAndPointerUseSameGeometry(t *testing.T) {
	e := loadEditorText(t, "pointer.txt", "\t界é\tword")
	e.Search().Open = true
	e.Search().Matches = []SearchMatch{{Start: Position{Col: 8}, End: Position{Col: 12}}}
	e.SetPresentation(types.EditorSettings{TabSize: 2, LineNumbers: true})
	if m := e.Search().Matches[0]; m.Start.Col != 6 || m.End.Col != 10 {
		t.Fatal("preview match was not reprojected", m)
	}
	paintEditor(e, 80, 10)
	e.HandleEvent(tea.MouseClickMsg{X: e.GutterWidth() + 7, Y: 0, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: e.GutterWidth() + 7, Y: 0, Button: tea.MouseLeft})
	if e.CursorCol() != 7 {
		t.Fatal("pointer missed tab-qualified source", e.CursorCol())
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if e.Value() != "\t界é\twXord" {
		t.Fatal("pointer edit changed the wrong bytes", e.Value())
	}
}

func TestTabPolicyNativeIndentFallbackAndDetectedStyleStayDistinct(t *testing.T) {
	for _, width := range []int{2, 8, 1, 32} {
		e := loadEditorText(t, "new.txt", "x\r\n")
		e.SetPresentation(types.EditorSettings{TabSize: width, LineNumbers: true})
		revision := e.Revision()
		e.SetCursorPosition(1, 0)
		e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
		if e.Value() != "x\r\n"+strings.Repeat(" ", width) || e.CursorCol() != width || e.Revision() == revision {
			t.Fatal("Tab ignored configured default", width, e.Value(), e.CursorCol())
		}
		e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyBackspace})
		if e.Value() != "x\r\n" || e.CursorCol() != 0 {
			t.Fatal("Backspace ignored the same indent stop", width, e.Value())
		}
		e.Undo()
		if e.Value() != "x\r\n"+strings.Repeat(" ", width) {
			t.Fatal("Backspace did not form one Undo transaction")
		}
	}
	e := loadEditorText(t, "detected.txt", "{\n  a\n}\n")
	e.SetPresentation(types.EditorSettings{TabSize: 8, LineNumbers: true})
	e.SetCursorPosition(1, 1)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "{\n   a\n}\n" {
		t.Fatal("display width replaced detected two-space indentation", e.Value())
	}
}
