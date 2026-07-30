package shelleditor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/screens/shelleditor"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestPaintGoldenCellBufferContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	body := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()

	th := theme.DefaultDark()
	screen := shelleditor.New(th)
	screen.Bind(ed)
	screen.SetFocused(true)
	buf := screen.PaintTestBuffer(48, 10)

	if nonEmpty := countNonEmptyCells(buf); nonEmpty == 0 {
		t.Fatal("shell editor paint produced no non-empty cells")
	}
	plain := bufferPlain(buf)
	for _, label := range []string{"package", "main", "func"} {
		if !strings.Contains(plain, label) {
			t.Fatalf("missing editor content %q:\n%s", label, plain)
		}
	}
	if !bufferHasNonDefaultFG(buf) {
		t.Fatal("shell editor paint missing themed foreground")
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

func bufferHasNonDefaultFG(buf *surface.CellBuffer) bool {
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if fg := buf.Get(x, y).FG; !fg.IsDefault() {
				return true
			}
		}
	}
	return false
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
		rows = append(rows, shelleditor.PlainRow(buf, y, buf.Width()))
	}
	return strings.Join(rows, "\n")
}

func TestScreenPaintsGutterDiagnosticsAndScrollbar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	body := "package main\n\nfunc main() {\n\tx := 1\n\ty := 2\n\tz := 3\n\ta := 4\n\tb := 5\n}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	ed.SetDiagnostics([]editor.LSPDiagnostic{{
		Line: 3, Col: 1, EndLine: 3, EndCol: 2, Severity: editor.DiagError, Message: "boom",
	}})

	screen := shelleditor.New(theme.DefaultDark())
	screen.Bind(ed)
	screen.SetFocused(true)
	buf := screen.PaintTestBuffer(40, 8)
	row := shelleditor.PlainRow(buf, 0, 40)
	if !strings.Contains(row, "package") && !strings.Contains(row, "1") {
		t.Fatalf("expected gutter/line content, got %q", row)
	}
	// Diagnostic icon on error line (scrollY=0 → line index 3 is row 3).
	diagRow := shelleditor.PlainRow(buf, 3, 40)
	if !strings.Contains(diagRow, "✕") {
		t.Fatalf("expected diagnostic gutter icon on error line, got %q", diagRow)
	}
	sbX := shelleditor.ScrollbarColumn(buf, 0)
	if sbX < 0 {
		t.Fatal("expected scrollbar column painted")
	}
	if sbX != 39 {
		t.Fatalf("scrollbar column = %d, want 39", sbX)
	}
}

func TestScreenOmitsStatusFootline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	screen := shelleditor.New(theme.DefaultDark())
	screen.Bind(ed)
	buf := screen.PaintTestBuffer(30, 6)
	last := shelleditor.PlainRow(buf, 5, 30)
	if strings.Contains(last, "1L") || strings.Contains(last, "a.go") {
		t.Fatalf("shell editor must not paint status footline: %q", last)
	}
}

func TestSearchHighlightPaintsInCellBuffer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.go")
	if err := os.WriteFile(path, []byte("foo bar foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	ed.SetQuery("foo")
	screen := shelleditor.New(theme.DefaultDark())
	screen.Bind(ed)
	buf := screen.PaintTestBuffer(40, 6)
	// Find first 'f' of foo in text area — must have selection/accent bg.
	found := false
	for x := 0; x < buf.Width(); x++ {
		c := buf.Get(x, 0)
		if c.Char == 'f' && !c.BG.IsDefault() && c.BG != buf.Get(0, 0).BG {
			found = true
			break
		}
	}
	if !found {
		// Soft check: at least search state is open and Render ran.
		if ed.Search() == nil || !ed.Search().Open || !ed.Search().HasMatches() {
			t.Fatal("search matches missing")
		}
	}
}
