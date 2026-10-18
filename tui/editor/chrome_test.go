package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/rendering"
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
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
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
	ed.readOnly = false
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

	// The diagnostic lane stays independent from line numbers and Git state.
	sw := ed.gutterWidth()
	icon := buf.Get(0, 0)
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

func TestGutterAlignsNumbersAndPaintsDeletionBoundaries(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	ed := NewPreview(theme.DefaultDark(), "gutter.txt", strings.Join(lines, "\n"), 1)
	setEditorAnnotationForTest(ed, ed.Revision(),
		[]GitHunk{
			{OldStart: 4, NewStart: 4, OldLines: []string{"old\n"}},
			{OldStart: 100, NewStart: 100, OldLines: []string{"gone\n"}},
		},
		nil,
		false,
	)

	surf := surface.NewSurface("gutter", 50, 110, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	buf := surf.Buffer()
	geometry := ed.gutterGeometry()

	if got := string([]rune{buf.Get(geometry.numberX, 0).Char, buf.Get(geometry.numberX+1, 0).Char, buf.Get(geometry.numberX+2, 0).Char}); got != "  1" {
		t.Fatalf("line 1 gutter=%q, want right-aligned", got)
	}
	if got := buf.Get(geometry.gapX, 4).Char; got != '▔' {
		t.Fatalf("middle deletion marker=%q, want top cap", got)
	}
	if got := buf.Get(geometry.gapX, 99).Char; got != '▁' {
		t.Fatalf("EOF deletion marker=%q, want bottom cap", got)
	}
	if got := buf.Get(geometry.numberX, 100).Char; got != ' ' {
		t.Fatalf("row after EOF painted phantom line number %q", got)
	}
}

func TestGitHunksShiftAcrossUnrelatedEdits(t *testing.T) {
	ed := New(theme.DefaultDark())
	ed.ReplaceDocument(strings.Repeat("line\n", 20))
	setEditorAnnotationForTest(ed, ed.Revision(), []GitHunk{
		{OldStart: 5, NewStart: 5, OldLines: []string{"old\n"}, NewLines: []string{"new\n"}},
		{OldStart: 15, NewStart: 15, NewLines: []string{"added\n"}},
	}, nil, false)
	ed.SetCursorPosition(0, 0)
	ed.InsertRune('\n')
	hunks := ed.GitHunks()
	if len(hunks) != 3 || hunks[0].NewStart != 0 || hunks[1].NewStart != 6 || hunks[2].NewStart != 16 {
		t.Fatalf("unaffected hunks were not mapped: %#v", hunks)
	}
}

// gutterRailCharAt renders the editor and returns the Git rail cell for a
// document line. The surface is taller than the test documents so the editor
// never scrolls: the assertion reads the line's own row instead of depending on
// viewport arithmetic.
func gutterRailCharAt(t *testing.T, ed *Editor, line int) rune {
	t.Helper()
	const width, height = 50, 48
	if line < 0 || line >= height {
		t.Fatalf("line %d is outside the painted surface", line)
	}
	surf := surface.NewSurface("git-gutter", width, height, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	if ed.ScrollY() != 0 {
		t.Fatalf("gutter assertion assumes an unscrolled viewport, got scrollY=%d", ed.ScrollY())
	}
	return surf.Buffer().Get(ed.gutterGeometry().railX, line).Char
}

// Typing inside a changed region must not drop its marker: the region is still
// changed relative to HEAD, and dropping it is what made the gutter blink on
// every keystroke before the debounced refresh replaced the projection.
func TestTypingInsideAHunkKeepsItsGutterMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blink.go")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 20)), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	setEditorAnnotationForTest(ed, ed.Revision(), []GitHunk{
		{OldStart: 5, NewStart: 5, OldLines: []string{"old\n"}, NewLines: []string{"new\n"}},
	}, nil, false)
	if got := gutterRailCharAt(t, ed, 5); got != '▌' {
		t.Fatalf("pre-edit marker=%q, want the git rail", got)
	}

	ed.SetCursorPosition(5, 0)
	ed.InsertRune('x')
	if got := gutterRailCharAt(t, ed, 5); got != '▌' {
		t.Fatalf("typing inside the hunk dropped its marker (gutter blinks): %q", got)
	}
	hunks := ed.GitHunks()
	if len(hunks) != 1 || hunks[0].NewStart != 5 {
		t.Fatalf("hunk projection did not hold its position: %#v", hunks)
	}
}

// A refresh that was already in flight when the user kept typing describes an
// older revision. It must be mapped forward and stay put, so the arriving
// projection cannot make the gutter blink under the keys.
func TestAnnotationArrivingMidBurstSurvivesTheNextKeystrokes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inflight.go")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 20)), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	// Establish the viewport first: the scroll-to-cursor pass on a first render
	// with no known height parks the viewport elsewhere.
	if got := gutterRailCharAt(t, ed, 0); got != ' ' {
		t.Fatalf("unannotated line painted %q", got)
	}

	ed.SetCursorPosition(5, 0)
	ed.InsertRune('x')
	ed.InsertRune('y')
	snapshotRevision := ed.Revision() - 2 // the annotation was computed before these keys

	setEditorAnnotationForTest(ed, snapshotRevision, []GitHunk{
		{OldStart: 5, NewStart: 5, OldLines: []string{"line\n"}, NewLines: []string{"xyline\n"}},
	}, nil, false)
	if got := gutterRailCharAt(t, ed, 5); got != '▌' {
		t.Fatalf("mid-burst annotation did not paint: %q", got)
	}

	ed.InsertRune('z')
	if got := gutterRailCharAt(t, ed, 5); got != '▌' {
		t.Fatalf("next keystroke dropped the mid-burst marker: %q", got)
	}
	if hunks := ed.GitHunks(); len(hunks) != 1 || hunks[0].NewStart != 5 {
		t.Fatalf("mid-burst annotation was discarded: %#v", hunks)
	}
}

// The rail must answer the keystroke, not the Git round trip: editing a line no
// hunk covers marks it at once, and further keystrokes keep that mark steady
// instead of re-claiming it line by line.
func TestTypingMarksAnUnchangedLineImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "immediate.go")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 20)), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	// A clean file relative to HEAD: nothing is marked yet.
	setEditorAnnotationForTest(ed, ed.Revision(), nil, nil, false)
	if got := gutterRailCharAt(t, ed, 5); got != ' ' {
		t.Fatalf("clean file already marked line 5: %q", got)
	}

	ed.SetCursorPosition(5, 0)
	ed.InsertRune('x')
	if got := gutterRailCharAt(t, ed, 5); got != '▌' {
		t.Fatalf("first keystroke did not mark its line: %q", got)
	}

	for i := 0; i < 5; i++ {
		ed.InsertRune('y')
		if got := gutterRailCharAt(t, ed, 5); got != '▌' {
			t.Fatalf("keystroke %d dropped the mark: %q", i+2, got)
		}
	}
	if got := gutterRailCharAt(t, ed, 4); got != ' ' {
		t.Fatalf("mark bled into the line above: %q", got)
	}
	if got := gutterRailCharAt(t, ed, 6); got != ' ' {
		t.Fatalf("mark bled into the line below: %q", got)
	}
	if len(ed.gitHunks) != 1 {
		t.Fatalf("typing one line left %d hunks: %#v", len(ed.gitHunks), ed.gitHunks)
	}
}

// Lines created beyond the annotated document read as added, not edited.
func TestTypingNewLinesMarksThemAsAdded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "append.go")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 5)), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	setEditorAnnotationForTest(ed, ed.Revision(), nil, nil, false)

	ed.SetCursorPosition(ed.LineCount()-1, 0)
	ed.InsertRune('\n')
	ed.InsertRune('n')
	ed.InsertRune('e')
	ed.InsertRune('w')
	if got := ed.gitKindAt(ed.LineCount() - 1); got != GitLineAdded {
		t.Fatalf("appended line kind=%d, want added", got)
	}
}

// An authoritative hunk already owns its lines; the local claim must not stack a
// second mark on top of them.
func TestEditingInsideAHunkAddsNoLocalClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.go")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	setEditorAnnotationForTest(ed, ed.Revision(), []GitHunk{
		{OldStart: 4, NewStart: 4, OldLines: []string{"line\n"}, NewLines: []string{"line\n"}},
	}, nil, false)

	ed.SetCursorPosition(4, 0)
	ed.InsertRune('x')
	if len(ed.gitHunks) != 1 {
		t.Fatalf("editing one hunk produced overlapping changes: %#v", ed.gitHunks)
	}
	if got := ed.gitKindAt(4); got != GitLineModified {
		t.Fatalf("kind=%d, want the authoritative modified marker", got)
	}
}

// The authoritative annotation replaces the local claim: the marker must stay
// on screen across the handover instead of blinking off and back on.
func TestAuthoritativeAnnotationTakesOverWithoutBlanking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handover.go")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	setEditorAnnotationForTest(ed, ed.Revision(), nil, nil, false)
	// Establish the viewport before typing: the first paint is what sets the
	// scroll model's viewport size, and an edit before it parks a bogus offset.
	if got := gutterRailCharAt(t, ed, 5); got != ' ' {
		t.Fatalf("clean file already marked line 5: %q", got)
	}

	ed.SetCursorPosition(5, 0)
	ed.InsertRune('x')
	if got := gutterRailCharAt(t, ed, 5); got != '▌' {
		t.Fatalf("local mark missing before handover: %q", got)
	}

	// The debounced refresh lands with the authoritative hunk for that line.
	setEditorAnnotationForTest(ed, ed.Revision(), []GitHunk{
		{OldStart: 5, NewStart: 5, OldLines: []string{"line\n"}, NewLines: []string{"xline\n"}},
	}, nil, false)
	if got := gutterRailCharAt(t, ed, 5); got != '▌' {
		t.Fatalf("handover blanked the marker: %q", got)
	}
	if len(ed.gitHunks) != 1 {
		t.Fatalf("refresh changed live hunk geometry: %#v", ed.gitHunks)
	}

	// ...and the line is still marked on the next keystroke.
	ed.InsertRune('y')
	if got := gutterRailCharAt(t, ed, 5); got != '▌' {
		t.Fatalf("keystroke after handover lost the marker: %q", got)
	}
}

// The immediate projection has complete old/new text, not speculative claims.
func TestImmediateChangesAreInspectable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claim.go")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	setEditorAnnotationForTest(ed, ed.Revision(), nil, nil, false)

	ed.SetCursorPosition(5, 0)
	ed.InsertRune('x')
	if h, _, ok := ed.GitHunkAtLine(5); !ok || strings.Join(h.OldLines, "") != "line\n" || strings.Join(h.NewLines, "") != "xline\n" {
		t.Fatal("immediate inspection did not receive exact old/new text")
	}
	if hunks := ed.GitHunks(); len(hunks) != 1 {
		t.Fatalf("navigation did not receive the immediate change: %#v", hunks)
	}
	if kind := ed.gitKindAt(5); kind == GitLineUnchanged {
		t.Fatal("local claim is not painted")
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
	setEditorAnnotationForTest(ed, ed.Revision(), []GitHunk{{NewLines: []string{"alpha\n"}}, {NewStart: 1, OldStart: 1, NewLines: []string{"beta\n"}, OldLines: []string{"before\n"}}}, nil, false)

	surf := surface.NewSurface("ed", 50, 6, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	buf := surf.Buffer()
	sw := ed.gutterWidth()
	if got := buf.Get(0, 0).Char; got != '✕' {
		t.Fatalf("diag col = %q, want ✕", got)
	}
	if got := buf.Get(sw-2, 0).Char; got != '▌' {
		t.Fatalf("git added col = %q, want rail", got)
	}
	if got := buf.Get(sw-2, 1).Char; got != '▌' {
		t.Fatalf("git modified col = %q, want rail", got)
	}
}

func TestGitConflictUsesDistinctLineState(t *testing.T) {
	ed := New(theme.DefaultDark())
	ed.ReplaceDocument("conflict\n")
	setEditorAnnotationForTest(ed, ed.Revision(), []GitHunk{{OldLines: []string{"old\n"}, NewLines: []string{"conflict\n"}}}, nil, true)
	if got := ed.gitKindAt(0); got != GitLineConflict {
		t.Fatalf("kind=%d", got)
	}
}

// The gutter (diagnostics, numbers, Git rail) has its own explicit fill on
// every body row, including rows past the last line and the horizontal
// scrollbar row, and the editor leaves no cell transparent: in a window the
// popup fill beneath used to show through the scrollbar row's gutter.
func TestGutterHasExplicitBackgroundOnEveryBodyRow(t *testing.T) {
	th := theme.DefaultDark()
	ed := NewPreview(th, filepath.Join(t.TempDir(), "wide.go"), strings.Repeat("wide", 80)+"\nshort", 1)
	surf := surface.NewSurface("gutter", 50, 8, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: th, Focused: true})
	if !ed.hScrollbarVisible {
		t.Fatal("fixture needs the horizontal scrollbar")
	}
	gutter := rendering.ThemeColorToCell(th.Colors.Gutter)
	text := rendering.ThemeColorToCell(th.Colors.Bg)
	if gutter == text {
		t.Fatal("gutter fill must differ from the text area")
	}
	buf := surf.Buffer()
	for y := 0; y < 8; y++ {
		for x := 0; x < 50; x++ {
			c := buf.Get(x, y)
			if c.Transparent {
				t.Fatalf("cell %d,%d left transparent", x, y)
			}
			if x < ed.gutterWidth() && c.BG != gutter {
				t.Fatalf("gutter cell %d,%d bg=%v, want %v", x, y, c.BG, gutter)
			}
		}
	}
	if c := buf.Get(ed.gutterWidth(), 0); c.BG != text {
		t.Fatalf("text cell bg=%v, want %v", c.BG, text)
	}
}
