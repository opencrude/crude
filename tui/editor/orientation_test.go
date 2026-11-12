package editor

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/theme"
	"crude/types"
	"fmt"
	"strings"
	"testing"
)

func TestMinimapPreferencePaintAndPointerUseOneScrollModel(t *testing.T) {
	source := strings.Repeat("    alpha 界é = beta;\r\n", 400)
	e := loadEditorText(t, "map.txt", source)
	paintEditor(e, 100, 20)
	width := e.scrollbarColWidth()
	e.SetSelection(0, 4, 0, 9)
	revision := e.Revision()
	e.SetPresentation(types.EditorSettings{LineNumbers: true, TabSize: 4, Minimap: true})
	b := paintEditor(e, 100, 20)
	if e.scrollbarColWidth() <= width {
		t.Fatal("Minimap preference has no visible consumer")
	}
	dots := 0
	for y := 0; y < e.bodyHeight(); y++ {
		for x := 100 - e.scrollbarColWidth(); x < 99; x++ {
			r := b.Get(x, y).Char
			if r > 0x2800 && r <= 0x28ff {
				dots++
			}
		}
	}
	if dots == 0 {
		t.Fatal("source overview contains no source ink")
	}
	e.HandleEvent(tea.MouseClickMsg{X: 100 - e.scrollbarColWidth() + 1, Y: 10, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: 100 - e.scrollbarColWidth() + 1, Y: 10, Button: tea.MouseLeft})
	if row := e.ScrollModel().Offset(); row < 150 || row > 250 {
		t.Fatal("overview click did not jump to the indicated source", row)
	}
	if e.Value() != source || e.Revision() != revision || e.SelectedText() != "alpha" || e.Modified() {
		t.Fatal("overview navigation changed source or selection")
	}
}
func TestWrappedScrollbarMovesVisibleRowsNotSourceLines(t *testing.T) {
	e := loadEditorText(t, "rows.txt", strings.Repeat(strings.Repeat("word ", 60)+"\n", 20))
	e.SetPresentation(wrapSettings(4))
	paintWrapped(t, e, 40, 12)
	e.HandleEvent(tea.MouseClickMsg{X: 39, Y: 0, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseMotionMsg{X: 39, Y: 6, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: 39, Y: 6, Button: tea.MouseLeft})
	if e.scrollRow() != e.scrollModel.Offset() {
		t.Fatal("shared scrollbar offset was reinterpreted as a source line", e.scrollRow(), e.scrollModel.Offset())
	}
	if e.scrollY >= e.LineCount() {
		t.Fatal("scrollbar jumped past the file", e.scrollY)
	}
}
func BenchmarkEditorOrientation(b *testing.B) {
	for _, lines := range []int{200, 20000} {
		for _, enabled := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/%t", lines, enabled), func(b *testing.B) {
				e := NewPreview(theme.DefaultDark(), "dense.txt", strings.Repeat("    word alpha 界 é internal spaces\n", lines), 1)
				defer e.Close()
				e.SetPresentation(types.EditorSettings{LineNumbers: true, Minimap: enabled, IndentGuides: enabled, TabSize: 4})
				paintEditor(e, 120, 40)
				settleSyntax(b, e)
				paintEditor(e, 120, 40) // isolate projection from the separately gated source worker
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					paintEditor(e, 120, 40)
				}
			})
		}
	}
}

func TestOrientationGuidesAndMinimapPreserveSourceAndGeometry(t *testing.T) {
	source := "\t  alpha 界é\r\n        nested\r\nplain\r\n"
	e := loadEditorText(t, "guides.txt", source)
	e.SetSelection(0, 6, 0, 11)
	revision := e.Revision()
	settings := types.EditorSettings{LineNumbers: true, TabSize: 4, IndentSize: 2, IndentGuides: true, Minimap: true}
	e.SetPresentation(settings)
	peer := e.NewLinkedView()
	defer peer.Close()
	b := paintEditor(e, 100, 12)
	paintEditor(peer, 80, 12)
	x := e.GutterWidth()
	for _, col := range []int{0, 2, 4} {
		if b.Get(x+col, 0).Char != '│' {
			t.Fatal("guide missed source indentation", col, b.Get(x+col, 0))
		}
	}
	if b.Get(x+6, 0).Char != 'a' || b.Get(x, 2).Char != 'p' {
		t.Fatal("guide overwrote source content")
	}
	if !peer.minimap || !peer.indentGuides || e.Revision() != revision || e.SelectedText() != "alpha" || e.Modified() {
		t.Fatal("presentation lost linked preference or source selection")
	}
	image, count := e.mini.image, e.mini.samples
	e.SetTheme(theme.DefaultLight())
	paintEditor(e, 100, 12)
	if e.mini.image != image || e.mini.samples != count {
		t.Fatal("theme/focus repainted source geometry")
	}
	e.SetPresentation(types.EditorSettings{LineNumbers: true, TabSize: 8, IndentSize: 4, IndentGuides: true, Minimap: true, RenderWhitespace: "all"})
	b = paintEditor(e, 100, 12)
	if b.Get(x, 0).Char != '│' || b.Get(x+4, 0).Char != '│' || b.Get(x+8, 0).Char != '│' || b.Get(x+9, 0).Char != '·' || e.SelectedText() != "alpha" {
		t.Fatal("guides and whitespace did not share tab/source columns")
	}
	paintEditor(e, 35, 12)
	if e.minimapWidth() != 0 {
		t.Fatal("minimap crowds narrow source")
	}
	paintEditor(e, 100, 12)
	if e.minimapWidth() == 0 || e.Value() != source || e.Revision() != revision {
		t.Fatal("resize lost map or changed source")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	e.Undo()
	if e.Value() != source || e.SelectedText() != "alpha" {
		t.Fatal("display data entered Undo")
	}
}
func TestMinimapWorkIsBoundedAndIndependentOfScrolling(t *testing.T) {
	e := loadEditorText(t, "dense.txt", strings.Repeat("    word 界é\n", 20000))
	e.SetPresentation(types.EditorSettings{LineNumbers: true, Minimap: true, TabSize: 4})
	paintEditor(e, 120, 40)
	count, image := e.mini.samples, e.mini.image
	if count > 160 || len(image.Cells) > 8*40 {
		t.Fatal("minimap scanned/stored the file instead of bounded samples", count, len(image.Cells))
	}
	e.setScrollRow(9000)
	paintEditor(e, 120, 40)
	if e.mini.image != image || e.mini.samples != count {
		t.Fatal("scroll resampled unchanged source")
	}
	e.SetCursorPosition(10000, 4)
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	paintEditor(e, 120, 40)
	if e.mini.image == image || e.mini.samples-count > 160 {
		t.Fatal("source change kept stale or unbounded overview")
	}
}
func TestMinimapWrappedAndFoldedScrollCapture(t *testing.T) {
	e := loadEditorText(t, "fold.go", foldFixture+strings.Repeat("var message = \"long 界 é words repeated here and repeated again across several visible rows\"\n", 300))
	settings := wrapSettings(4)
	settings.Minimap = true
	e.SetPresentation(settings)
	paintWrapped(t, e, 60, 16)
	e.SetCursorPosition(1, 0)
	e.HandleEvent(ActionMsg{ID: "view.fold"})
	paintWrapped(t, e, 60, 16)
	x := 60 - e.scrollbarColWidth()
	e.HandleEvent(tea.MouseClickMsg{X: x, Y: 8, Button: tea.MouseLeft})
	if !e.IsScrollbarDragging() {
		t.Fatal("overview track jump did not capture")
	}
	e.HandleEvent(tea.MouseMotionMsg{X: -5, Y: 12, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: -5, Y: 12, Button: tea.MouseLeft})
	if e.IsScrollbarDragging() || e.scrollRow() != e.scrollModel.Offset() || e.scrollRow() < 300 {
		t.Fatal("captured map drag lost wrapped source offset", e.scrollRow(), e.scrollModel.Offset())
	}
	paintWrapped(t, e, 60, 16)
	first, _, ok := e.VisibleSourceLines()
	if !ok || first != e.scrollY {
		t.Fatal("miniature pointer and rendered source disagree", first, e.scrollY)
	}
}

func TestMinimapWrappedContinuationsDoNotLookEmpty(t *testing.T) {
	e := loadEditorText(t, "wrapped.txt", strings.Repeat("word 界é ", 40))
	settings := wrapSettings(4)
	settings.Minimap = true
	e.SetPresentation(settings)
	paintWrapped(t, e, 60, 16)
	image := e.mini.image
	samples := min(e.visualRowCount(), image.Height*4)
	for sample := 0; sample < samples; sample++ {
		y := sample * (image.Height*4 - 1) / max(1, samples-1) / 4
		ink := false
		for x := 0; x < image.Width; x++ {
			c := image.Cells[y*image.Width+x]
			ink = ink || c != ' ' && c != 0
		}
		if !ink {
			t.Fatal("wrapped source beyond first map columns looks empty", y)
		}
	}
}
func BenchmarkMinimapRebuild(b *testing.B) {
	for _, lines := range []int{200, 20000} {
		b.Run(fmt.Sprint(lines), func(b *testing.B) {
			e := NewPreview(theme.DefaultDark(), "dense.txt", strings.Repeat("    word alpha 界 é internal spaces\n", lines), 1)
			defer e.Close()
			e.SetPresentation(types.EditorSettings{LineNumbers: true, Minimap: true, IndentGuides: true, TabSize: 4})
			paintEditor(e, 120, 40)
			settleSyntax(b, e)
			paintEditor(e, 120, 40) // isolate projection from the separately gated source worker
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				e.mini.image = nil
				e.prepareMinimap(40)
			}
		})
	}
}

func TestMinimapMarksUnsampledLongWrappedSource(t *testing.T) {
	e := loadEditorText(t, "long.txt", strings.Repeat("a", 20000))
	settings := wrapSettings(4)
	settings.Minimap = true
	e.SetPresentation(settings)
	paintWrapped(t, e, 60, 16)
	image := e.mini.image
	if image.Cells[(image.Height-1)*image.Width] != '…' {
		t.Fatal("unsampled long source looks empty")
	}
	e.Close()
	if e.mini.key.buffer != nil || e.mini.image != nil {
		t.Fatal("closed view retained source geometry")
	}
}

func TestSourceGeometryDoesNotCopyLineForCaret(t *testing.T) {
	e := loadEditorText(t, "geometry.txt", strings.Repeat("word alpha 界 é\n", 100))
	e.buf.ensureCache()
	pos := Position{Line: 50, Col: 4}
	off := e.buf.positionToOffset(pos)
	if allocations := testing.AllocsPerRun(100, func() {
		if got := e.buf.positionAtOffset(off); got != pos {
			panic("wrong source position")
		}
		if got := e.buf.positionToOffset(pos); got != off {
			panic("wrong source offset")
		}
	}); allocations != 0 {
		t.Fatal("caret conversion copied the source line", allocations)
	}
}

func TestByteGeometryUsesTheSameUnicodeBoundaries(t *testing.T) {
	text := "a\t界é👩‍💻Z"
	raw := []byte(text)
	for _, test := range [][2]int{{0, 0}, {1, 1}, {2, 4}, {3, 4}, {5, 6}, {6, 6}, {8, 7}, {9, 7}, {19, 9}, {20, 10}} {
		if got := ByteOffsetToVisualCol(raw, test[0], 4); got != test[1] || got != ByteOffsetToVisualCol(text, test[0], 4) {
			t.Fatal("byte/string grapheme boundary mismatch", test, got)
		}
	}
	for col, off := range []int{0, 1, 1, 1, 2, 2, 5, 8, 8, 19, 20} {
		if got := VisualColToByteOffset(raw, col, 4); got != off || got != VisualColToByteOffset(text, col, 4) {
			t.Fatal("byte/string column mapping mismatch", col, got, off)
		}
	}
	if LineVisualWidth(raw, 4) != 10 || string(raw) != text {
		t.Fatal("byte geometry changed width or source")
	}
}
