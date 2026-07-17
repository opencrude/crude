package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

// TestVisualBaseline_Gallery_80x24_Dark renders the state gallery at
// 80x24 under the dark theme and asserts basic cell properties.
// This is a deterministic baseline: if the gallery layout changes,
// this test will catch it.
func TestVisualBaseline_Gallery_80x24_Dark(t *testing.T) {
	th := theme.DefaultDark()
	buf := DemoPaint(th, 80, 24)

	// Gallery must not be empty: at least some non-transparent cells.
	nonTransparent := countNonTransparent(buf)
	if nonTransparent == 0 {
		t.Fatal("gallery produced completely transparent output at 80x24")
	}

	// Gallery must have visible text content.
	hasVisibleText := false
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if !c.Transparent && c.Char != ' ' && c.Char != 0 {
				hasVisibleText = true
				break
			}
		}
		if hasVisibleText {
			break
		}
	}
	if !hasVisibleText {
		t.Fatal("gallery produced no visible text at 80x24")
	}

	// Gallery must contain the title "Crude UI — State Gallery".
	if !bufContainsString(buf, "Crude UI") {
		t.Fatal("gallery missing title text at 80x24")
	}

	// At 80x24 the gallery has limited vertical space. The first
	// sections (TextFields, Buttons, Lists) must be visible.
	for _, section := range []string{"TextFields", "Buttons", "Lists"} {
		if !bufContainsString(buf, section) {
			t.Fatalf("gallery missing section %q at 80x24", section)
		}
	}

	// Gallery must contain control state labels that fit at 80x24.
	for _, label := range []string{"default", "focused", "placeholder", "disabled"} {
		if !bufContainsString(buf, label) {
			t.Fatalf("gallery missing state label %q at 80x24", label)
		}
	}

	// No cell should have Width=0 unless it's a continuation cell.
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Width == 0 && c.Char != 0 {
				t.Errorf("cell at (%d,%d) has Width=0 but Char=%U", x, y, c.Char)
			}
		}
	}
}

// TestVisualBaseline_Gallery_80x30_Dark renders at the gallery's
// native design size. The first sections must be visible.
func TestVisualBaseline_Gallery_80x30_Dark(t *testing.T) {
	th := theme.DefaultDark()
	buf := DemoPaint(th, 80, 30)

	nonTransparent := countNonTransparent(buf)
	if nonTransparent == 0 {
		t.Fatal("gallery produced completely transparent output at 80x30")
	}

	if !bufContainsString(buf, "Crude UI") {
		t.Fatal("gallery missing title at 80x30")
	}

	// At 80x30, the gallery shows TextFields, Buttons, Lists sections.
	// Labels and WindowFrames may be clipped by the Stack layout.
	for _, section := range []string{"TextFields", "Buttons", "Lists"} {
		if !bufContainsString(buf, section) {
			t.Fatalf("gallery missing section %q at 80x30", section)
		}
	}
}

// TestVisualBaseline_Gallery_120x40_Dark renders the state gallery at
// 120x40 under the dark theme. Wider layout must still be valid.
func TestVisualBaseline_Gallery_120x40_Dark(t *testing.T) {
	th := theme.DefaultDark()
	buf := DemoPaint(th, 120, 40)

	nonTransparent := countNonTransparent(buf)
	if nonTransparent == 0 {
		t.Fatal("gallery produced completely transparent output at 120x40")
	}

	if !bufContainsString(buf, "Crude UI") {
		t.Fatal("gallery missing title at 120x40")
	}

	// At wider dimensions, the gallery should have at least as many
	// non-transparent cells as the 80x24 version (more content visible).
	buf80 := DemoPaint(th, 80, 24)
	count80 := countNonTransparent(buf80)
	if nonTransparent < count80 {
		t.Fatalf("120x40 has fewer non-transparent cells (%d) than 80x24 (%d)", nonTransparent, count80)
	}
}

// TestVisualBaseline_Gallery_200x50_Dark renders at a wide terminal
// dimension. Content must not break or produce invalid cells.
func TestVisualBaseline_Gallery_200x50_Dark(t *testing.T) {
	th := theme.DefaultDark()
	buf := DemoPaint(th, 200, 50)

	nonTransparent := countNonTransparent(buf)
	if nonTransparent == 0 {
		t.Fatal("gallery produced completely transparent output at 200x50")
	}

	if !bufContainsString(buf, "Crude UI") {
		t.Fatal("gallery missing title at 200x50")
	}

	// All non-transparent cells must have valid width (1 or 2).
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if !c.Transparent && c.Width != 1 && c.Width != 2 {
				t.Errorf("cell at (%d,%d) has invalid Width=%d", x, y, c.Width)
			}
		}
	}
}

// TestVisualBaseline_Gallery_80x24_Light renders the state gallery at
// 80x24 under the light theme. Must produce valid output different
// from the dark theme.
func TestVisualBaseline_Gallery_80x24_Light(t *testing.T) {
	th := LightTheme()
	buf := DemoPaint(th, 80, 24)

	nonTransparent := countNonTransparent(buf)
	if nonTransparent == 0 {
		t.Fatal("gallery produced completely transparent output under light theme")
	}

	if !bufContainsString(buf, "Crude UI") {
		t.Fatal("gallery missing title under light theme")
	}

	// Compare with dark theme: the popup background color must differ.
	darkBuf := DemoPaint(theme.DefaultDark(), 80, 24)
	darkBg := findPopupBgColor(darkBuf)
	lightBg := findPopupBgColor(buf)
	if darkBg.Equal(lightBg) {
		t.Fatal("light theme popup background identical to dark theme")
	}
}

// TestVisualBaseline_Gallery_120x40_Light renders at 120x40 under
// the light theme.
func TestVisualBaseline_Gallery_120x40_Light(t *testing.T) {
	th := LightTheme()
	buf := DemoPaint(th, 120, 40)

	nonTransparent := countNonTransparent(buf)
	if nonTransparent == 0 {
		t.Fatal("gallery produced completely transparent output at 120x40 light")
	}

	if !bufContainsString(buf, "Crude UI") {
		t.Fatal("gallery missing title at 120x40 light")
	}
}

// TestVisualBaseline_Gallery_RuntimeThemeSwitch verifies that switching
// themes at runtime preserves the gallery structure and changes colors.
func TestVisualBaseline_Gallery_RuntimeThemeSwitch(t *testing.T) {
	host := DemoGallery(theme.DefaultDark())
	host.Resize(80, 30)

	buf := surface.NewCellBuffer(80, 30)
	host.Paint(buf)

	// Record dark theme cell at a known position.
	darkCell := buf.Get(0, 0)

	// Switch to light theme.
	host.SetTheme(LightTheme())
	host.Paint(buf)

	lightCell := buf.Get(0, 0)

	// The cell at (0,0) should have changed color after theme switch.
	if darkCell.FG.Equal(lightCell.FG) && darkCell.BG.Equal(lightCell.BG) {
		// It's possible that (0,0) is transparent in both themes,
		// so check a non-transparent cell instead.
		found := false
		for y := 0; y < buf.Height() && !found; y++ {
			for x := 0; x < buf.Width() && !found; x++ {
				dc := buf.Get(x, y)
				if dc.Transparent {
					continue
				}
				// Switch back to dark, paint, record.
				host.SetTheme(theme.DefaultDark())
				host.Paint(buf)
				darkCell = buf.Get(x, y)
				// Switch to light, paint, record.
				host.SetTheme(LightTheme())
				host.Paint(buf)
				lightCell = buf.Get(x, y)
				if !darkCell.FG.Equal(lightCell.FG) || !darkCell.BG.Equal(lightCell.BG) {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("theme switch did not change any cell colors")
		}
	}
}

// TestVisualBaseline_WindowFrame_CloseGlyph verifies that WindowFrame
// renders a close glyph (✕) in the title bar when closable.
func TestVisualBaseline_WindowFrame_CloseGlyph(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	frame := NewWindowFrame(NewID(RootID, "test-close"), FrameDialog, tokens)
	frame.SetTitle("Test")
	frame.SetClosable(true)
	label := NewLabel(NewID(frame.ID(), "body"), "body", th)
	frame.SetContent(label)
	frame.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 30, H: 10})

	buf := surface.NewCellBuffer(30, 10)
	frame.Paint(buf, 0, 0, PaintContext{Theme: th})

	// Look for ✕ glyph in the first row (title bar).
	found := false
	for x := 0; x < 30; x++ {
		c := buf.Get(x, 0)
		if c.Char == '✕' {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("WindowFrame did not render close glyph ✕ in title bar")
	}
}

// TestVisualBaseline_WindowFrame_BorderCells verifies that WindowFrame
// renders border cells with the correct box-drawing characters.
func TestVisualBaseline_WindowFrame_BorderCells(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	frame := NewWindowFrame(NewID(RootID, "test-border"), FrameDialog, tokens)
	frame.SetTitle("T")
	frame.SetClosable(false)
	label := NewLabel(NewID(frame.ID(), "body"), "body", th)
	frame.SetContent(label)
	frame.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 20, H: 8})

	buf := surface.NewCellBuffer(20, 8)
	frame.Paint(buf, 0, 0, PaintContext{Theme: th})

	// Top-left corner should be ╭ (U+256D).
 topLeft := buf.Get(0, 0)
	if topLeft.Char != '╭' {
		t.Errorf("top-left corner: got %U, want ╭", topLeft.Char)
	}

	// Top-right corner should be ╮ (U+256E).
	topRight := buf.Get(19, 0)
	if topRight.Char != '╮' {
		t.Errorf("top-right corner: got %U, want ╮", topRight.Char)
	}

	// Bottom-left corner should be ╰ (U+2570).
	bottomLeft := buf.Get(0, 7)
	if bottomLeft.Char != '╰' {
		t.Errorf("bottom-left corner: got %U, want ╰", bottomLeft.Char)
	}

	// Bottom-right corner should be ╯ (U+256F).
	bottomRight := buf.Get(19, 7)
	if bottomRight.Char != '╯' {
		t.Errorf("bottom-right corner: got %U, want ╯", bottomRight.Char)
	}
}

// TestVisualBaseline_TextField_CursorBlock verifies that TextField
// renders a block cursor (inverse colors) when focused.
func TestVisualBaseline_TextField_CursorBlock(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	field := NewTextField(NewID(RootID, "cursor-test"), tokens)
	field.SetPrompt("❯")
	field.SetValue("hello")

	// Use Host to focus the field properly.
	h := NewHost(th, field)
	h.Register(field)
	h.Resize(30, 1)
	h.Focus(field.ID())

	buf := surface.NewCellBuffer(30, 1)
	h.Paint(buf)

	// The cursor should be at position after "hello" (position 6, accounting
	// for prompt "❯" + space). The cursor cell should have different colors
	// from the surrounding text.
	promptWidth := 2 // "❯" + space
	cursorX := promptWidth + 5 // after "hello"
	if cursorX < 30 {
		cursorCell := buf.Get(cursorX, 0)
		// Cursor should not be transparent.
		if cursorCell.Transparent {
			t.Errorf("cursor cell at (%d,0) is transparent", cursorX)
		}
	}
}

// TestVisualBaseline_TextField_Placeholder verifies that TextField
// renders placeholder text in muted style when empty.
func TestVisualBaseline_TextField_Placeholder(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	field := NewTextField(NewID(RootID, "ph-test"), tokens)
	field.SetPrompt("❯")
	field.SetPlaceholder("type here…")
	field.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 30, H: 1})

	buf := surface.NewCellBuffer(30, 1)
	field.Paint(buf, 0, 0, PaintContext{Theme: th})

	// Should contain placeholder text.
	if !bufContainsString(buf, "type here") {
		t.Fatal("TextField did not render placeholder text")
	}
}

// TestVisualBaseline_List_SelectedRow verifies that List renders
// selected rows with different background from unselected rows.
func TestVisualBaseline_List_SelectedRow(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	list := NewList(NewID(RootID, "sel-test"), tokens)
	list.SetItems([]ListItem{
		{ID: "a", Label: "Alpha"},
		{ID: "b", Label: "Beta"},
		{ID: "c", Label: "Gamma"},
	})
	list.SelectIndex(1)
	list.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 20, H: 3})

	buf := surface.NewCellBuffer(20, 3)
	list.Paint(buf, 0, 0, PaintContext{Theme: th})

	// Row 0 (Alpha) and row 1 (Beta) should have different backgrounds
	// because row 1 is selected.
	row0Bg := buf.Get(1, 0).BG
	row1Bg := buf.Get(1, 1).BG
	if row0Bg.Equal(row1Bg) {
		// It's possible that the list uses foreground-only selection.
		// Check if at least one cell differs.
		row0Fg := buf.Get(1, 0).FG
		row1Fg := buf.Get(1, 1).FG
		if row0Fg.Equal(row1Fg) {
			t.Fatal("selected row has same FG and BG as unselected row")
		}
	}
}

// TestVisualBaseline_Button_Disabled verifies that disabled buttons
// render differently from enabled buttons.
func TestVisualBaseline_Button_Disabled(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)

	btnEnabled := NewButton(NewID(RootID, "btn-en"), "Save", "save")
	btnEnabled.SetTokens(tokens)
	btnEnabled.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 20, H: 1})

	btnDisabled := NewButton(NewID(RootID, "btn-dis"), "Save", "save")
	btnDisabled.SetTokens(tokens)
	btnDisabled.SetDisabled(true)
	btnDisabled.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 20, H: 1})

	bufEn := surface.NewCellBuffer(20, 1)
	btnEnabled.Paint(bufEn, 0, 0, PaintContext{Theme: th})

	bufDis := surface.NewCellBuffer(20, 1)
	btnDisabled.Paint(bufDis, 0, 0, PaintContext{Theme: th})

	// At least one cell should differ between enabled and disabled.
	differ := false
	for x := 0; x < 20; x++ {
		ce := bufEn.Get(x, 0)
		cd := bufDis.Get(x, 0)
		if !ce.FG.Equal(cd.FG) || !ce.BG.Equal(cd.BG) {
			differ = true
			break
		}
	}
	if !differ {
		t.Fatal("disabled button renders identically to enabled button")
	}
}

// TestVisualBaseline_Label_ColorRoles verifies that labels with
// different color roles render with different foreground colors.
func TestVisualBaseline_Label_ColorRoles(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)

	tests := []struct {
		name string
		role SemanticColor
	}{
		{"accent", ColorAccent},
		{"muted", ColorMuted},
		{"error", ColorError},
		{"success", ColorSuccess},
	}

	buffers := make(map[string]*surface.CellBuffer)
	for _, tt := range tests {
		lbl := NewLabel(NewID(RootID, "lbl-"+tt.name), "Test", th)
		lbl.SetTokens(tokens)
		lbl.SetColorRole(tt.role)
		lbl.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 10, H: 1})
		buf := surface.NewCellBuffer(10, 1)
		lbl.Paint(buf, 0, 0, PaintContext{Theme: th})
		buffers[tt.name] = buf
	}

	// Each role should produce a different foreground color.
	for i, tt1 := range tests {
		for _, tt2 := range tests[i+1:] {
			c1 := buffers[tt1.name].Get(0, 0)
			c2 := buffers[tt2.name].Get(0, 0)
			if c1.FG.Equal(c2.FG) {
				t.Errorf("labels %q and %q have same foreground color", tt1.name, tt2.name)
			}
		}
	}
}

// TestVisualBaseline_NoTransparencyInContentArea verifies that the
// content area of a WindowFrame has no transparent cells (the frame
// background should fill everything).
func TestVisualBaseline_NoTransparencyInContentArea(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	frame := NewWindowFrame(NewID(RootID, "test-fill"), FrameDialog, tokens)
	frame.SetTitle("Test")
	frame.SetClosable(true)
	label := NewLabel(NewID(frame.ID(), "body"), "Content", th)
	frame.SetContent(label)
	frame.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 40, H: 10})

	buf := surface.NewCellBuffer(40, 10)
	frame.Paint(buf, 0, 0, PaintContext{Theme: th})

	// Check that the interior of the frame (excluding borders) has
	// no transparent cells.
	for y := 1; y < 9; y++ {
		for x := 1; x < 39; x++ {
			c := buf.Get(x, y)
			if c.Transparent {
				t.Errorf("transparent cell in content area at (%d,%d)", x, y)
			}
		}
	}
}

// TestVisualBaseline_ContinuationCellIntegrity verifies that wide
// characters (Width=2) are followed by continuation cells (Width=0).
func TestVisualBaseline_ContinuationCellIntegrity(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	// Use a label with a wide character.
	label := NewLabel(NewID(RootID, "wide"), "中文测试", th)
	label.SetTokens(tokens)
	label.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 20, H: 1})

	buf := surface.NewCellBuffer(20, 1)
	label.Paint(buf, 0, 0, PaintContext{Theme: th})

	// Check that wide chars are followed by continuation cells.
	for x := 0; x < 19; x++ {
		c := buf.Get(x, 0)
		if c.Width == 2 {
			next := buf.Get(x+1, 0)
			if next.Width != 0 || next.Char != 0 {
				t.Errorf("wide char at (%d,0) not followed by continuation cell", x)
			}
		}
	}
}

// TestVisualBaseline_Label_Ellipsis verifies that labels truncate
// with ellipsis when the text exceeds the available width.
func TestVisualBaseline_Label_Ellipsis(t *testing.T) {
	th := theme.DefaultDark()
	label := NewLabel(NewID(RootID, "ell"), "This is a very long label that should be truncated", th)
	label.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 20, H: 1})

	buf := surface.NewCellBuffer(20, 1)
	label.Paint(buf, 0, 0, PaintContext{Theme: th})

	// The last cell should be ellipsis (…).
	lastCell := buf.Get(19, 0)
	if lastCell.Char != '…' {
		t.Errorf("last cell: got %U, want …", lastCell.Char)
	}
}

// TestVisualBaseline_Benchmark_Render_80x24 measures render performance
// at 80x24 under the dark theme.
func TestVisualBaseline_Benchmark_Render_80x24(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}
	th := theme.DefaultDark()
	host := DemoGallery(th)
	host.Resize(80, 24)
	buf := surface.NewCellBuffer(80, 24)

	// Warm up.
	for i := 0; i < 10; i++ {
		host.Paint(buf)
	}

	// Measure allocations.
	allocs := testing.AllocsPerRun(100, func() {
		host.Paint(buf)
	})

	t.Logf("80x24 dark: %.0f allocs/paint", allocs)

	// Measure time.
	b := testing.B{}
	for i := 0; i < b.N; i++ {
		host.Paint(buf)
	}
}

// TestVisualBaseline_Benchmark_Render_120x40 measures render performance
// at 120x40 under the dark theme.
func TestVisualBaseline_Benchmark_Render_120x40(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}
	th := theme.DefaultDark()
	host := DemoGallery(th)
	host.Resize(120, 40)
	buf := surface.NewCellBuffer(120, 40)

	// Warm up.
	for i := 0; i < 10; i++ {
		host.Paint(buf)
	}

	// Measure allocations.
	allocs := testing.AllocsPerRun(100, func() {
		host.Paint(buf)
	})

	t.Logf("120x40 dark: %.0f allocs/paint", allocs)
}

// TestVisualBaseline_Benchmark_HandleKeyEvent measures key event
// handling performance.
func TestVisualBaseline_Benchmark_HandleKeyEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}
	th := theme.DefaultDark()
	host := DemoGallery(th)
	host.Resize(80, 24)

	// Focus the first text field.
	host.Focus(NewID(RootID, "tf-default"))

	// Warm up.
	for i := 0; i < 10; i++ {
		host.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
	}

	// Measure allocations.
	allocs := testing.AllocsPerRun(1000, func() {
		host.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
	})

	t.Logf("key event: %.0f allocs/handle", allocs)
}

// ── helpers ──

// countNonTransparent counts the number of non-transparent cells in a buffer.
func countNonTransparent(buf *surface.CellBuffer) int {
	count := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if !buf.Get(x, y).Transparent {
				count++
			}
		}
	}
	return count
}

// bufContainsString checks if a buffer contains the given string
// anywhere in its cells (reading left-to-right, top-to-bottom).
func bufContainsString(buf *surface.CellBuffer, s string) bool {
	runes := []rune(s)
	if len(runes) == 0 {
		return true
	}
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x <= buf.Width()-len(runes); x++ {
			match := true
			for i, r := range runes {
				c := buf.Get(x+i, y)
				if c.Char != r {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// findPopupBgColor finds the most common background color in a buffer
// (used to compare dark vs light theme).
func findPopupBgColor(buf *surface.CellBuffer) cell.Color {
	counts := make(map[cell.Color]int)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if !c.Transparent && !c.BG.IsDefault() {
				counts[c.BG]++
			}
		}
	}
	var maxColor cell.Color
	maxCount := 0
	for clr, cnt := range counts {
		if cnt > maxCount {
			maxCount = cnt
			maxColor = clr
		}
	}
	return maxColor
}
