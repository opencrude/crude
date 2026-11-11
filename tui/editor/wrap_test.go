package editor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func wrapSettings(width int) types.EditorSettings {
	return types.EditorSettings{TabSize: width, WordWrap: true, LineNumbers: true}
}

// Expectations use literal source and cell positions. The gutter + scrollbar
// leaves ten cells: nine text cells and one continuation/caret cell.
func TestWrapPaintMotionPointerAndUndoShareSourceRows(t *testing.T) {
	source := "abcdefghijklmno\nend"
	e := loadEditorText(t, "wrap.txt", source)
	e.SetPresentation(wrapSettings(4))
	width := e.GutterWidth() + 11
	b := paintWrapped(t, e, width, 10)
	x := e.GutterWidth()
	if b.Get(x, 1).Char != 'j' || b.Get(x, 2).Char != 'e' || e.IsHorizontalScrollbarDragging() || e.hScrollbarVisible {
		t.Fatal("Word wrap did not project long source onto continuation rows")
	}
	if e.ScrollModel().Content() != 3 {
		t.Fatal("scrollbar does not count the same visible rows", e.ScrollModel().Content())
	}
	revision := e.Revision()
	e.SetCursorPosition(0, 1)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	if e.CursorLine() != 0 || e.CursorCol() != 10 || e.SelectedText() != "bcdefghij" {
		t.Fatal("Down used a logical line instead of the next wrapped row", e.CursorLine(), e.CursorCol(), e.SelectedText())
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if e.Value() != "aXklmno\nend" {
		t.Fatal("wrapped selection changed wrong source", e.Value())
	}
	e.Undo()
	if e.Value() != source || e.SelectedText() != "bcdefghij" {
		t.Fatal("Undo lost wrapped source selection")
	}
	e.ClearSelection()
	e.SetCursorPosition(0, 10)
	paintWrapped(t, e, width, 10)
	sx, sy, ok := e.PositionScreenPosition(Position{Line: 0, Col: 10}, 0, 0)
	if !ok || sx != x+1 || sy != 1 {
		t.Fatal("popover/caret projection missed continuation", sx, sy, ok)
	}
	e.HandleEvent(tea.MouseClickMsg{X: x + 2, Y: 1, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: x + 2, Y: 1, Button: tea.MouseLeft})
	if e.CursorLine() != 0 || e.CursorCol() != 11 {
		t.Fatal("pointer did not hit the painted source", e.cursor)
	}
	if e.Value() != source || e.Revision() == revision {
		t.Fatal("Undo did not retain canonical source history")
	}
}

func TestWrapWideClustersTabsInlaysAndResizingStayLiteral(t *testing.T) {
	e := newInlayTestEditor(t, "abc\t界éXYZq\nend")
	e.SetPresentation(wrapSettings(4))
	width := e.GutterWidth() + 7
	b := paintWrappedInlays(t, e, width, 10, false)
	x := e.GutterWidth()
	if b.Get(x, 1).Content != "界" || b.Get(x+2, 1).Content != "é" || b.Get(x, 2).Char != 'Z' {
		t.Fatal("wrap split a grapheme or changed tab stops")
	}
	e.SetCursorPosition(0, 9)
	before := e.captureViewState()
	revision := e.Revision()
	paintWrappedInlays(t, e, width+3, 10, false)
	if e.captureViewState().Cursor != before.Cursor || e.Revision() != revision || e.Value() != "abc\t界éXYZq\nend" {
		t.Fatal("resize changed source or caret")
	}
	e = newInlayTestEditor(t, "\tfoo bar")
	e.SetPresentation(wrapSettings(4))
	installTestInlays(t, e, types.LSPInlayHint{Index: 0, SourceByte: 1, Position: types.LSPInlayPosition{Character: 1}, Label: "HINT", PaddingRight: true})
	b = paintWrappedInlays(t, e, e.GutterWidth()+9, 10, false)
	x = e.GutterWidth()
	if b.Get(x+2, 1).Char != 'f' {
		t.Fatal("inlay and source were wrapped by different owners")
	}
	if index, ok := e.InlayHintAt(x+5, 0); !ok || index != 0 {
		t.Fatal("wrapped hint hit lost original identity", index, ok)
	}
	if _, ok := e.InlayHintAt(x+2, 1); ok {
		t.Fatal("source is falsely an inlay")
	}
	sx, sy, ok := e.PositionScreenPosition(Position{Col: 4}, 0, 0)
	if !ok || sx != x+2 || sy != 1 {
		t.Fatal("source anchor skipped wrapping after inlay", sx, sy, ok)
	}
	e.SetSelection(0, 0, 0, 11)
	if e.SelectedText() != "\tfoo bar" {
		t.Fatal("wrap or hints entered copied text")
	}
}

func TestWrapLinkedViewsKeepIndependentRowsAndToggleAnchors(t *testing.T) {
	source := strings.Repeat("abcdefghij", 8) + "\nlast"
	e := loadEditorText(t, "split.txt", source)
	e.SetPresentation(wrapSettings(4))
	peer := e.NewLinkedView()
	t.Cleanup(peer.Close)
	paintWrapped(t, e, e.GutterWidth()+11, 8)
	paintWrapped(t, peer, peer.GutterWidth()+21, 8)
	e.SetCursorPosition(0, 1)
	peer.SetCursorPosition(0, 1)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	peer.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	if e.CursorCol() != 10 || peer.CursorCol() != 20 {
		t.Fatal("linked panes shared wrapping width", e.cursor, peer.cursor)
	}
	before := e.captureViewState().Cursor
	e.SetPresentation(types.EditorSettings{TabSize: 4, LineNumbers: true, WordWrap: false})
	paintWrapped(t, e, e.GutterWidth()+11, 8)
	if e.captureViewState().Cursor != before || e.Value() != source || e.Modified() || !e.hScrollbarVisible {
		t.Fatal("turning wrap off moved source or kept wrapped chrome")
	}
}

func BenchmarkWrappedEditorWorkload(b *testing.B) {
	for _, size := range []int{200, 20000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			e := NewPreview(theme.DefaultDark(), "wrap.txt", strings.Repeat("    "+strings.Repeat("wide text ", 8)+"界 é\n", size), 1)
			defer e.Close()
			e.readOnly = false
			e.SetPresentation(wrapSettings(4))
			surf := surface.NewSurface("wrap-benchmark", 80, 24, cell.TransparentBlank())
			e.Render(surf, ui.PaintContext{Theme: e.th})
			settleSyntax(b, e)
			settleRows(b, e)
			e.SetCursorPosition(size/2, 4)
			e.ScrollToCursor()
			e.Render(surf, ui.PaintContext{Theme: e.th})
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				e.Render(surf, ui.PaintContext{Theme: e.th})
			}
		})
	}
}

func TestWrapCacheReflowsOnlyChangedSourceAndRetainsPeers(t *testing.T) {
	e := loadEditorText(t, "cache.txt", strings.Repeat("long words with 界 é and tabs\tend\n", 200))
	e.SetPresentation(wrapSettings(4))
	paintWrapped(t, e, 40, 12)
	before := e.rows.measures
	for range 3 {
		paintWrapped(t, e, 40, 12)
		e.HandleEvent(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	}
	paintWrapped(t, e, 50, 12)
	if e.rows.measures != before {
		t.Fatal("paint, scroll or resize remeasured unchanged source", before, e.rows.measures)
	}
	e.SetCursorPosition(100, 4)
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	paintWrapped(t, e, 50, 12)
	if e.rows.measures-before != 1 {
		t.Fatal("typing reflowed offscreen source", e.rows.measures-before)
	}
	for _, width := range []int{15, 40, 60, 13, 50} {
		e.rows.prepare(e, width)
		reference := coldRowsForTest(t, e)
		reference.prepare(e, width)
		for i := range reference.lines {
			if e.rows.lines[i].count != reference.lines[i].count {
				t.Fatal("resize aliased neighbouring row geometry", i, width)
			}
			for row := 0; row < reference.lines[i].count; row++ {
				if e.rows.lines[i].start(row) != reference.lines[i].start(row) {
					t.Fatal("resize overwrote a retained break run", i, row, width)
				}
			}
		}
	}
	// Several source changes can precede the next render. An insertion before a
	// previously touched line must relocate its invalidation, not forget it.
	if _, err := e.buf.insertAtOffset(e.buf.positionToOffset(Position{Line: 150, Col: 4}), []byte(" more words")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.buf.insertAtOffset(0, []byte("inserted header\n")); err != nil {
		t.Fatal(err)
	}
	paintWrapped(t, e, 50, 12)
	fresh := coldRowsForTest(t, e)
	fresh.prepare(e, e.rows.width)
	if e.rows.total() != fresh.total() {
		t.Fatal("batched line shifts retained stale row counts", e.rows.total(), fresh.total())
	}
	for i := range fresh.lines {
		if fresh.lines[i].count != e.rows.lines[i].count || fresh.lines[i].width != e.rows.lines[i].width {
			t.Fatal("source line shift lost wrapping", i)
		}
	}
}

func TestWrapHomeEndAndBoundaryAffinityUseNoFakeBytes(t *testing.T) {
	e := loadEditorText(t, "keys.txt", "abcdefghijklmno")
	e.SetPresentation(wrapSettings(4))
	paintWrapped(t, e, e.GutterWidth()+11, 10)
	e.SetCursorPosition(0, 3)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnd})
	row, col := e.viewPosition(e.cursor)
	if e.CursorCol() != 9 || row != 0 || col != 9 {
		t.Fatal("End did not land at this row's insertion boundary", e.cursor, row, col)
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyRight})
	row, col = e.viewPosition(e.cursor)
	if e.CursorCol() != 9 || row != 1 || col != 0 {
		t.Fatal("soft-wrap gap fabricated source movement")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyLeft})
	row, col = e.viewPosition(e.cursor)
	if e.CursorCol() != 9 || row != 0 || col != 9 {
		t.Fatal("Left did not cross the visual gap")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnd})
	if e.CursorCol() != 15 {
		t.Fatal("repeated End did not reach source EOL")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyHome})
	if e.CursorCol() != 9 {
		t.Fatal("Home skipped continuation start")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyHome})
	if e.CursorCol() != 0 || e.Modified() {
		t.Fatal("repeated Home missed logical start or changed bytes")
	}
}

func BenchmarkWrappedRowPreparation(b *testing.B) {
	for _, stage := range []string{"cold", "resize", "typing_and_paint"} {
		b.Run(stage, func(b *testing.B) {
			e := NewPreview(theme.DefaultDark(), "wrap.txt", strings.Repeat("    "+strings.Repeat("wide text ", 8)+"界 é\n", 20000), 1)
			defer e.Close()
			e.readOnly = false
			e.SetPresentation(wrapSettings(4))
			surf := surface.NewSurface("wrap-benchmark", 80, 24, cell.TransparentBlank())
			e.Render(surf, ui.PaintContext{Theme: e.th})
			settleSyntax(b, e)
			settleRows(b, e)
			e.SetCursorPosition(10000, 4)
			e.Render(surf, ui.PaintContext{Theme: e.th})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				switch stage {
				case "cold":
					p := coldRowsForTest(b, e)
					p.prepare(e, 70)
				case "resize":
					e.rows.prepare(e, 70+i%2)
				case "typing_and_paint":
					key := tea.KeyPressMsg{Code: tea.KeyBackspace}
					if i%2 == 0 {
						key = tea.KeyPressMsg{Code: 'x', Text: "x"}
					}
					e.HandleEvent(key)
					e.Render(surf, ui.PaintContext{Theme: e.th})
				}
			}
		})
	}
}

func settleRows(t testing.TB, e *Editor) {
	t.Helper()
	for range 5 {
		cmd := e.RowsCommand()
		if cmd == nil {
			if e.rowJob == nil {
				return
			}
			t.Fatal("row job completion claimed twice")
		}
		select {
		case <-e.RowsDone():
		case <-time.After(5 * time.Second):
			t.Fatal("row preparation did not finish")
		}
		cmd().(RowsReadyMsg).Apply()
	}
	t.Fatal("row preparation did not settle")
}
func paintWrapped(t testing.TB, e *Editor, w, h int) *surface.CellBuffer {
	t.Helper()
	paintEditor(e, w, h)
	settleRows(t, e)
	return paintEditor(e, w, h)
}
func paintWrappedInlays(t testing.TB, e *Editor, w, h int, focused bool) *surface.CellBuffer {
	t.Helper()
	renderTestInlays(e, w, h, focused)
	settleRows(t, e)
	return renderTestInlays(e, w, h, focused)
}
func coldRowsForTest(t testing.TB, e *Editor) editorRows {
	t.Helper()
	document, err := syntaxSource(context.Background(), e.buf.snapshotText())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := buildSourceRows(context.Background(), document.text, document.lines, e.TabWidth())
	if err != nil {
		t.Fatal(err)
	}
	rows.buffer, rows.revision, rows.tabWidth = e.buf, e.Revision(), e.TabWidth()
	return rows
}

func TestWrapColdWorkDoesNotBlockPaintOrAcceptRetiredSource(t *testing.T) {
	e := loadEditorText(t, "plain.txt", strings.Repeat("long source text ", 20000))
	release := pauseSyntaxWorkers(t)
	e.SetPresentation(wrapSettings(4))
	// Paint must remain on exact current source while the shared pool is busy.
	paintEditor(e, 80, 12)
	old := e.rowJob
	if old == nil || e.usesRows() {
		release()
		t.Fatal("cold geometry was not admitted off the UI path")
	}
	e.ReplaceDocument("new source")
	cmd := e.RowsCommand()
	release()
	if cmd != nil {
		cmd().(RowsReadyMsg).Apply()
	}
	settleRows(t, e)
	paintEditor(e, 80, 12)
	if e.rows.buffer != e.buf || e.rows.revision != e.Revision() || e.rows.metrics[0].source.width != 10 || e.Value() != "new source" {
		t.Fatal("old worker overwrote replacement source geometry")
	}
	e.InvalidateWidthProfile()
	paintEditor(e, 80, 12)
	closing := e.RowsCommand()
	done := e.RowsDone()
	e.Close()
	if closing != nil {
		closing().(RowsReadyMsg).Apply()
	}
	if done != nil {
		<-done
	}
	if e.IsOpen() || e.usesRows() {
		t.Fatal("late geometry reopened a closed view")
	}
}

func TestWrapLongLineEditUsesOwnedPreparationRatherThanUIReflow(t *testing.T) {
	e := loadEditorText(t, "long.txt", strings.Repeat("wide 界 ", 20000))
	e.SetPresentation(wrapSettings(4))
	paintWrapped(t, e, 80, 12)
	release := pauseSyntaxWorkers(t)
	e.SetCursorPosition(0, 0)
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	paintEditor(e, 80, 12)
	if e.rowJob == nil || !strings.HasPrefix(e.Value(), "Xwide") {
		release()
		t.Fatal("large source edit did not schedule exact geometry without delaying source")
	}
	cmd := e.RowsCommand()
	release()
	if cmd != nil {
		cmd().(RowsReadyMsg).Apply()
	}
	settleRows(t, e)
	paintEditor(e, 80, 12)
	if e.rows.revision != e.Revision() || e.rows.metrics[0].source.width != 160001 {
		t.Fatal("large source result lost current bytes", e.rows.metrics[0].source.width)
	}
}

func TestWrapCRLFDragScrollbarAndPageKeysUseVisibleRows(t *testing.T) {
	source := strings.Repeat("abcdefghijklmno\r\n", 30)
	e := loadEditorText(t, "drag.txt", source)
	e.SetPresentation(wrapSettings(4))
	width := e.GutterWidth() + 11
	paintWrapped(t, e, width, 8)
	e.HandleEvent(tea.MouseClickMsg{X: e.GutterWidth() + 2, Y: 0, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseMotionMsg{X: e.GutterWidth() + 3, Y: 2, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: e.GutterWidth() + 3, Y: 2, Button: tea.MouseLeft})
	if e.SelectedText() != "cdefghijklmno\r\nabc" {
		t.Fatal("wrapped drag selected display rows, not exact source", e.SelectedText())
	}
	e.ClearSelection()
	e.SetCursorPosition(0, 0)
	e.ScrollToCursor()
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if e.CursorLine() != 3 || e.CursorCol() != 9 {
		t.Fatal("PageDown skipped wrong number of visible rows", e.cursor)
	}
	// Scrollbar end maps to the final source rows, not an out-of-bounds line.
	e.HandleEvent(tea.MouseClickMsg{X: width - 1, Y: 7, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: width - 1, Y: 7, Button: tea.MouseLeft})
	paintWrapped(t, e, width, 8)
	if e.scrollY >= e.LineCount() || e.Value() != source || e.Modified() {
		t.Fatal("scrollbar changed source or invented a source line")
	}
}
