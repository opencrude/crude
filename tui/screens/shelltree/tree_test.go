package shelltree_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/shelltree"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/uitest"
)

func TestPaintGoldenCellBufferContract(t *testing.T) {
	// Fixed workspace name so the root label is deterministic for the golden.
	dir := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	th := theme.DefaultDark()
	model := filetree.NewFileTreeModel(dir)
	screen := shelltree.New(th, model)
	screen.ResizeLayout(40, 8)

	host := ui.NewHost(th, screen)
	host.Resize(40, 8)
	host.Layout()
	buf := surface.NewCellBuffer(40, 8)
	host.Paint(buf)

	if nonEmpty := countNonEmptyCells(buf); nonEmpty == 0 {
		t.Fatal("shell tree paint produced no non-empty cells")
	}
	plain := bufferPlain(buf)
	if !strings.Contains(plain, "main.go") {
		t.Fatalf("missing tree entry main.go:\n%s", plain)
	}
	wantAccent := ui.NewTokenSet(th).Color(ui.ColorAccent)
	if !bufferHasFG(buf, wantAccent) {
		t.Fatal("shell tree paint missing theme accent foreground")
	}

	// Byte-identical regression: catches a shifted indent/divider or wrong
	// git-status glyph that property checks cannot see.
	uitest.CheckGolden(t, "shelltree_dark_40x8", buf)
}

// TestScreenDividerConsumesBorderVGlyph verifies the sidebar divider chrome
// paints the theme BorderV glyph: overriding the token changes the divider.
func TestScreenDividerConsumesBorderVGlyph(t *testing.T) {
	dir := t.TempDir()
	model := filetree.NewFileTreeModel(dir)
	th := theme.DefaultDark()
	th.Glyphs.BorderV = "!"
	screen := shelltree.New(th, model)
	buf := screen.PaintTestBuffer(30, 6)
	for y := 0; y < 6; y++ {
		if c := buf.Get(29, y).Char; c != '!' {
			t.Fatalf("divider row %d char=%q want '!' (BorderV token)", y, string(c))
		}
	}
}

// TestScreenDirIconConsumesTreeGlyphs verifies the file-tree directory icons
// flow from the theme's TreeCollapsed/TreeExpanded glyph tokens through the
// IconResolver into the painted rows.
func TestScreenDirIconConsumesTreeGlyphs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	model := filetree.NewFileTreeModel(dir)
	th := theme.DefaultDark()
	th.Glyphs.TreeCollapsed = "c"
	th.Glyphs.TreeExpanded = "v"
	screen := shelltree.New(th, model)
	buf := screen.PaintTestBuffer(30, 6)
	row0 := shelltree.PlainRow(buf, 0, 30)
	if !strings.Contains(row0, "v") {
		t.Fatalf("expanded root row %q missing TreeExpanded glyph 'v'", row0)
	}
	row1 := shelltree.PlainRow(buf, 1, 30)
	if !strings.Contains(row1, "c") {
		t.Fatalf("collapsed subdir row %q missing TreeCollapsed glyph 'c'", row1)
	}
}

func countNonEmptyCells(buf *surface.CellBuffer) int {
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 0 && c.Width != 0 {
				n++
			}
		}
	}
	return n
}

func bufferHasFG(buf *surface.CellBuffer, want cell.Color) bool {
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if fg := buf.Get(x, y).FG; !fg.IsDefault() && fg.Equal(want) {
				return true
			}
		}
	}
	return false
}

func bufferPlain(buf *surface.CellBuffer) string {
	var rows []string
	for y := 0; y < buf.Height(); y++ {
		rows = append(rows, shelltree.PlainRow(buf, y, buf.Width()))
	}
	return strings.Join(rows, "\n")
}

func TestScreenPaintsFileTreeNative(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := filetree.NewFileTreeModel(dir)
	screen := shelltree.New(theme.DefaultDark(), model)
	screen.ResizeLayout(32, 6)
	buf := screen.PaintTestBuffer(32, 6)
	row := shelltree.PlainRow(buf, 0, 28)
	if row == "" {
		t.Fatal("expected painted tree row")
	}
}

func TestScreenAlignsGitAndDiagnosticsAsTrailingBlock(t *testing.T) {
	dir := t.TempDir()
	paths := map[string]string{
		"both.go": filepath.Join(dir, "both.go"),
		"diag.go": filepath.Join(dir, "diag.go"),
		"git.go":  filepath.Join(dir, "git.go"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("package test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	model := filetree.NewFileTreeModel(dir)
	model.NodeByPath(paths["git.go"]).GitStatus = filetree.GitModified
	model.NodeByPath(paths["both.go"]).GitStatus = filetree.GitModified
	model.SetDiagnostics(paths["diag.go"], 2, 0, 0)
	model.SetDiagnostics(paths["both.go"], 3, 2, 0)

	screen := shelltree.New(theme.DefaultDark(), model)
	const width = 42
	buf := screen.PaintTestBuffer(width, 8)
	rows := map[string]string{}
	for y := 0; y < 8; y++ {
		row := shelltree.PlainRow(buf, y, width-1)
		for name := range paths {
			if strings.Contains(row, name) {
				rows[name] = row
			}
		}
	}

	if !strings.HasSuffix(rows["git.go"], "M") {
		t.Fatalf("git-only row has misplaced indicator: %q", rows["git.go"])
	}
	if !strings.HasSuffix(rows["diag.go"], "✕2") {
		t.Fatalf("diagnostic-only row has misplaced indicator: %q", rows["diag.go"])
	}
	if !strings.HasSuffix(rows["both.go"], "M ✕3 ⚠2") {
		t.Fatalf("combined row has wrong indicator order: %q", rows["both.go"])
	}
	wantRightEdge := rendering.VisibleWidth(rows["git.go"])
	for _, name := range []string{"diag.go", "both.go"} {
		if got := rendering.VisibleWidth(rows[name]); got != wantRightEdge {
			t.Fatalf("%s trailing edge=%d want %d: %q", name, got, wantRightEdge, rows[name])
		}
	}
}

// TestSetTheme_PreservesPaintedTreeAcrossPresets guards the blank-sidebar
// regression: SetTheme used to rebuildTree() without Host.MarkLayout, leaving
// zero-bound children after color-only theme switches.
func TestSetTheme_PreservesPaintedTreeAcrossPresets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := filetree.NewFileTreeModel(dir)
	th := theme.DefaultDark()
	screen := shelltree.New(th, model)
	host := ui.NewHost(th, screen)
	host.Resize(40, 10)
	host.Layout()

	paint := func() *surface.CellBuffer {
		buf := surface.NewCellBuffer(40, 10)
		host.Paint(buf)
		return buf
	}

	darkBuf := paint()
	darkPlain := bufferPlain(darkBuf)
	if !strings.Contains(darkPlain, "main.go") {
		t.Fatalf("dark tree missing main.go:\n%s", darkPlain)
	}
	darkChars := countNonEmptyCells(darkBuf)
	if darkChars < 10 {
		t.Fatalf("dark tree too sparse: %d cells", darkChars)
	}

	for _, preset := range []string{"light", "high-contrast", "warm", "midnight", "dark"} {
		next := theme.FromPreset(preset)
		host.SetTheme(next)
		screen.SetTheme(next)
		// Color-only switches must NOT require a size change to keep painting.
		buf := paint()
		plain := bufferPlain(buf)
		if !strings.Contains(plain, "main.go") {
			t.Fatalf("preset %q blanked tree (no main.go):\n%s", preset, plain)
		}
		if n := countNonEmptyCells(buf); n < 10 {
			t.Fatalf("preset %q tree too sparse: %d cells", preset, n)
		}
	}
}
