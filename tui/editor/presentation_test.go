package editor

import (
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"strings"
	"testing"
)

func TestPresentationWhitespaceIsPaintOnlyAndCached(t *testing.T) {
	source := "\t foo bar \t\r\n"
	e := loadEditorText(t, "spaces.txt", source)
	e.SetSelection(0, 5, 0, 8)
	selected := e.SelectedText()
	revision := e.Revision()
	for _, mode := range []string{"all", "boundary", "none", "boundary"} {
		e.SetPresentation(types.EditorSettings{LineNumbers: true, RenderWhitespace: mode})
		b := paintEditor(e, 80, 8)
		x := e.GutterWidth()
		for col, want := range map[int]rune{0: '→', 1: ' ', 2: ' ', 3: ' ', 4: '·', 8: '·', 12: '·', 13: '→', 14: ' ', 15: ' '} {
			if mode == "none" || mode == "boundary" && col == 8 {
				want = ' '
			}
			if got := b.Get(x+col, 0).Char; got != want {
				t.Fatalf("%s column %d: %q, want %q", mode, col, got, want)
			}
		}
		info := e.lineGeomCache[0].whitespace
		paintEditor(e, 80, 8)
		if e.lineGeomCache[0].whitespace != info {
			t.Fatal("paint rebuilt unchanged whitespace geometry")
		}
		if e.Value() != source || e.Revision() != revision || e.SelectedText() != selected || e.Modified() {
			t.Fatal("whitespace presentation changed source/selection")
		}
	}
	e.SetQuery("foo")
	e.SetPresentation(types.EditorSettings{LineNumbers: true, RenderWhitespace: "all"})
	if e.Search().Matches[0].Start.Col != 5 {
		t.Fatal("markers entered search geometry")
	}
}
func TestPresentationThemeAndPaletteDoNotRestartSourceAnalysis(t *testing.T) {
	e := loadEditorText(t, "source.go", "package p\n// comment\nvar v=1\n")
	paintEditor(e, 80, 10)
	parsed, revision, generation := e.syntaxDocument, e.syntaxRevision, e.syntaxGeneration
	for _, name := range []string{"dracula", "github", "monokai", "solarized", "catppuccin"} {
		e.SetPresentation(types.EditorSettings{LineNumbers: true, SyntaxTheme: name})
		paintEditor(e, 80, 10)
		if e.syntaxDocument != parsed || e.syntaxRevision != revision || e.syntaxGeneration != generation || e.syntaxJob != nil {
			t.Fatal("palette change reparsed source", name)
		}
	}
	e.SetTheme(theme.DefaultLight())
	paintEditor(e, 80, 10)
	if e.syntaxDocument != parsed || e.syntaxGeneration != generation {
		t.Fatal("theme change reparsed source")
	}
}
func BenchmarkPresentationWhitespacePaint(b *testing.B) {
	for _, mode := range []string{"none", "boundary", "all"} {
		b.Run(mode, func(b *testing.B) {
			e := NewPreview(theme.DefaultDark(), "plain.txt", strings.Repeat("\twide 界 é internal spaces \t \n", 10000), 1)
			defer e.Close()
			e.SetPresentation(types.EditorSettings{LineNumbers: true, RenderWhitespace: mode})
			paintEditor(e, 120, 40)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				paintEditor(e, 120, 40)
			}
		})
	}
}

func TestPresentationKeepsInlaysLiteralAndDiagnosticLaneVisible(t *testing.T) {
	e := newInlayTestEditor(t, "\tfoo ")
	installTestInlays(t, e, types.LSPInlayHint{Index: 0, Position: types.LSPInlayPosition{Line: 0, Character: 1}, SourceByte: 1, Label: "hint text", PaddingRight: true})
	e.SetDiagnostics(testDiagnosticFacts(e, []LSPDiagnostic{{Line: 0, Col: 1, EndLine: 0, EndCol: 4, Severity: DiagError, Message: "source error"}}))
	e.SetPresentation(types.EditorSettings{LineNumbers: false, RenderWhitespace: "all"})
	b := renderTestInlays(e, 80, 8, false)
	x := e.GutterWidth()
	if e.gutterGeometry().numberW != 0 || b.Get(0, 0).Content != ui.DiagnosticGlyph(DiagError) {
		t.Fatal("line-number setting hid the diagnostic lane")
	}
	for col, want := range map[int]rune{0: '→', 4: 'h', 8: ' ', 13: ' ', 14: 'f', 17: '·'} {
		if got := b.Get(x+col, 0).Char; got != want {
			t.Fatalf("inlay/source column %d: %q want %q", col, got, want)
		}
	}
	e.SetSelection(0, 0, 0, 8)
	if e.SelectedText() != "\tfoo " || e.Value() != "\tfoo " {
		t.Fatal("markers or inlays entered source copy")
	}
}
