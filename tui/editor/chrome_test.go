package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestRenderPaintsSharedScrollbarChrome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "many.go")
	var b []byte
	for i := 0; i < 40; i++ {
		b = append(b, []byte("line\n")...)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	ed.SetStatusChrome(false)

	surf := surface.NewSurface("ed", 40, 10, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})

	sm := ed.ScrollModel()
	if sm == nil || sm.MaxOffset() <= 0 {
		t.Fatal("expected scrollable content")
	}
	foundThumb := false
	for y := 0; y < 10; y++ {
		c := surf.Buffer().Get(39, y)
		if c.Char == '┃' || c.Char == '█' {
			foundThumb = true
		}
		if c.Char != '│' && c.Char != '┃' && c.Char != '█' && c.Char != ' ' {
			t.Fatalf("scrollbar col unexpected rune %q at y=%d", c.Char, y)
		}
	}
	if !foundThumb {
		t.Fatal("expected scrollbar thumb")
	}
	ed.HandleEvent(tea.MouseMotionMsg{X: 39, Y: 0})
	if !ed.vScrollbarHovered {
		t.Fatal("vertical scrollbar did not enter hover state")
	}
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	if got := surf.Buffer().Get(39, 0).Char; got != '█' {
		t.Fatalf("hovered vertical thumb=%q want block", got)
	}
	ed.HandleEvent(tea.MouseMotionMsg{X: 10, Y: 5})
	if ed.vScrollbarHovered {
		t.Fatal("vertical scrollbar hover stuck after pointer left")
	}
}

func TestHorizontalScrollbarAppearsOnDemandAndDrags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wide.go")
	ed := NewPreview(theme.DefaultDark(), path, strings.Repeat("wide", 80)+"\nshort", 1)
	surf := surface.NewSurface("wide", 50, 6, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	if !ed.hScrollbarVisible || ed.hScrollModel == nil || ed.hScrollModel.MaxOffset() <= 0 {
		t.Fatal("wide line did not expose horizontal scrollbar")
	}
	y := ed.bodyHeight()
	if !ed.horizontalScrollbarHit(ed.gutterWidth(), y) {
		t.Fatal("painted horizontal scrollbar row must be interactive")
	}
	if ed.horizontalScrollbarHit(ed.gutterWidth(), y-1) {
		t.Fatal("row above horizontal scrollbar must remain editor content")
	}
	foundThumb := false
	for x := ed.gutterWidth(); x < 50-ed.scrollbarColWidth(); x++ {
		if ch := surf.Buffer().Get(x, y).Char; ch == '▁' || ch == '▄' {
			foundThumb = true
			break
		}
	}
	if !foundThumb {
		t.Fatal("horizontal thumb was not painted")
	}
	startX := ed.gutterWidth()
	ed.HandleEvent(tea.MouseMotionMsg{X: startX, Y: y})
	if !ed.hScrollbarHovered {
		t.Fatal("horizontal scrollbar did not enter hover state")
	}
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	if got := surf.Buffer().Get(startX, y).Char; got != '▄' {
		t.Fatalf("hovered horizontal thumb=%q want lower block", got)
	}
	before := make([]rune, 0, 20)
	for x := startX; x < startX+20; x++ {
		before = append(before, surf.Buffer().Get(x, 0).Char)
	}
	ed.HandleEvent(tea.MouseClickMsg{X: startX, Y: y, Button: tea.MouseLeft})
	ed.HandleEvent(tea.MouseMotionMsg{X: startX + 20, Y: y})
	ed.HandleEvent(tea.MouseReleaseMsg{X: startX + 20, Y: y, Button: tea.MouseLeft})
	if ed.scrollX == 0 {
		t.Fatal("horizontal thumb drag did not update scrollX")
	}
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	changed := false
	for i, x := range before {
		if surf.Buffer().Get(startX+i, 0).Char != x {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("horizontal offset changed but painted editor content stayed behind")
	}
}

func TestEditorCursorIsBlinkingReverseOverlay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cursor.go")
	ed := NewPreview(theme.DefaultDark(), path, "abc", 1)
	ed.cursor = Position{Line: 0, Col: 1}
	ed.cursorBlink.ResetAt(0)
	surf := surface.NewSurface("cursor", 20, 5, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true, Frame: 0})
	x := ed.gutterWidth() + 1
	if c := surf.Buffer().Get(x, 0); c.Char != 'b' || !c.Attr.Has(cell.AttrReverse) {
		t.Fatalf("visible cursor cell=%+v want preserved b with reverse", c)
	}

	surf = surface.NewSurface("cursor-off", 20, 5, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true, Frame: 1200})
	if c := surf.Buffer().Get(x, 0); c.Char != 'b' || c.Attr.Has(cell.AttrReverse) {
		t.Fatalf("blink-off cursor cell=%+v want plain b", c)
	}
}

func TestHorizontalExtentTracksWholeDocumentNotViewport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wide.txt")
	lines := make([]string, 80)
	for i := range lines {
		lines[i] = "short"
	}
	lines[70] = strings.Repeat("w", 120)
	ed := NewPreview(theme.DefaultDark(), path, strings.Join(lines, "\n"), 1)
	surf := surface.NewSurface("wide", 40, 10, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true, Frame: 1})
	if !ed.hScrollbarVisible || ed.hScrollModel.Content() < 120 {
		t.Fatalf("initial horizontal content=%d visible=%v", ed.hScrollModel.Content(), ed.hScrollbarVisible)
	}
	extent := ed.hScrollModel.Content()
	ed.scrollX = ed.hScrollModel.MaxOffset()
	ed.scrollY = 60
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true, Frame: 2})
	if ed.hScrollModel.Content() != extent {
		t.Fatalf("vertical viewport changed extent %d -> %d", extent, ed.hScrollModel.Content())
	}
	for i := range lines {
		lines[i] = "short"
	}
	ed.ReplaceDocument(strings.Join(lines, "\n"))
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true, Frame: 3})
	if ed.hScrollbarVisible || ed.hScrollModel.MaxOffset() != 0 || ed.scrollX != 0 {
		t.Fatalf("shortened widest line left horizontal scroll visible: %+v", ed.hScrollModel)
	}
	ed.InvalidateWidthProfile()
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true, Frame: 4})
	if ed.hScrollModel.Content() != 5 {
		t.Fatalf("profile rebuild extent=%d want 5", ed.hScrollModel.Content())
	}
}

func TestHorizontalWheelAndShiftWheelScrollOnlyX(t *testing.T) {
	ed := NewPreview(theme.DefaultDark(), "wide.txt", strings.Repeat("x", 120)+"\nsecond", 1)
	surf := surface.NewSurface("wheel", 40, 8, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true, Frame: 1})
	startY := ed.scrollY
	ed.HandleEvent(tea.MouseWheelMsg{Button: tea.MouseWheelRight})
	if ed.scrollX == 0 || ed.scrollY != startY {
		t.Fatalf("right wheel scroll=(%d,%d)", ed.scrollX, ed.scrollY)
	}
	right := ed.scrollX
	ed.HandleEvent(tea.MouseWheelMsg{Button: tea.MouseWheelLeft})
	if ed.scrollX >= right || ed.scrollY != startY {
		t.Fatalf("left wheel scroll=(%d,%d)", ed.scrollX, ed.scrollY)
	}
	ed.HandleEvent(tea.MouseWheelMsg{Button: tea.MouseWheelDown, Mod: tea.ModShift})
	if ed.scrollX == 0 || ed.scrollY != startY {
		t.Fatalf("shift wheel scroll=(%d,%d)", ed.scrollX, ed.scrollY)
	}
}

func TestDocumentWidthIndexIsIncrementalForOrdinaryEdit(t *testing.T) {
	content := strings.Repeat("short\n", 10000)
	ed := NewPreview(theme.DefaultDark(), "large.txt", content, 1)
	_ = ed.documentVisualWidth()
	if ed.hWidthMeasures < 10000 {
		t.Fatalf("initial measures=%d", ed.hWidthMeasures)
	}
	ed.hWidthMeasures = 0
	ed.cursor = Position{Line: 5000, Col: 2}
	ed.typeRune('x')
	_ = ed.documentVisualWidth()
	if ed.hWidthMeasures > 1 {
		t.Fatalf("single-line edit measured %d document lines", ed.hWidthMeasures)
	}
	ed.hWidthMeasures = 0
	ed.scrollY = 9000
	_ = ed.documentVisualWidth()
	if ed.hWidthMeasures != 0 {
		t.Fatalf("vertical scroll remeasured %d lines", ed.hWidthMeasures)
	}
}

func TestDocumentWidthIndexReducesFormerMaximum(t *testing.T) {
	ed := NewPreview(theme.DefaultDark(), "width.txt", "12345678901234567890\n1234567890", 1)
	if got := ed.documentVisualWidth(); got != 20 {
		t.Fatalf("initial width=%d", got)
	}
	ed.SetSelection(0, 5, 0, 20)
	ed.DeleteSelection()
	ed.hWidthMeasures = 0
	if got := ed.documentVisualWidth(); got != 10 {
		t.Fatalf("reduced width=%d", got)
	}
	if ed.hWidthMeasures != 1 {
		t.Fatalf("maximum reduction measured %d lines", ed.hWidthMeasures)
	}
}

func TestRenderDiagnosticsAndSearchChrome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.go")
	if err := os.WriteFile(path, []byte("alpha beta alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	ed.SetDiagnostics([]LSPDiagnostic{{
		Line: 0, Col: 0, EndLine: 0, EndCol: 5, Severity: DiagError, Message: "err",
	}})
	ed.SetQuery("alpha")

	surf := surface.NewSurface("ed", 50, 6, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	buf := surf.Buffer()

	// Gutter diagnostic icon in icon column (gutterWidth-3).
	sw := ed.gutterWidth()
	icon := buf.Get(sw-3, 0)
	if icon.Char != '✕' {
		t.Fatalf("diag icon = %q, want ✕", icon.Char)
	}

	// Search highlight: current match uses Accent2 bg.
	foundHL := false
	for x := sw; x < 49; x++ {
		c := buf.Get(x, 0)
		if c.Char == 'a' && c.BG.Equal(ed.searchHighlightBg(true)) {
			foundHL = true
			break
		}
		if c.Char == 'a' && c.BG.Equal(ed.searchHighlightBg(false)) {
			foundHL = true
			break
		}
	}
	if !foundHL {
		t.Fatal("expected search highlight background on match")
	}
}

func TestRenderGitGutterBesideDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	ed.SetDiagnostics([]LSPDiagnostic{{
		Line: 0, Col: 0, EndLine: 0, EndCol: 5, Severity: DiagError, Message: "err",
	}})
	ed.SetGitAnnotation([]GitLineKind{GitLineAdded, GitLineModified}, nil)

	surf := surface.NewSurface("ed", 50, 6, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	buf := surf.Buffer()
	sw := ed.gutterWidth()
	if got := buf.Get(sw-3, 0).Char; got != '✕' {
		t.Fatalf("diag col = %q, want ✕", got)
	}
	if got := buf.Get(sw-2, 0).Char; got != '+' {
		t.Fatalf("git added col = %q, want +", got)
	}
	if got := buf.Get(sw-2, 1).Char; got != '~' {
		t.Fatalf("git modified col = %q, want ~", got)
	}
}
