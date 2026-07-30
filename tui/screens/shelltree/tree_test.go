package shelltree_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestPaintGoldenCellBufferContract(t *testing.T) {
	dir := t.TempDir()
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
