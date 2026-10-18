package shelleditor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/editor"
	"crude/tui/screens/shelleditor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// buildGoFile creates a synthetic Go file with funcBodyLines distinct
// function bodies so chroma sees a realistic token mix.
func buildGoFile(tb testing.TB, funcCount int) string {
	var b strings.Builder
	b.WriteString("package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\n")
	for i := 0; i < funcCount; i++ {
		b.WriteString("// helper ")
		b.WriteString(strings.Repeat("x", 40))
		b.WriteString("\nfunc helper")
		b.WriteString(strings.Repeat("i", 1+i%5))
		b.WriteString("(s string, n int) (string, error) {\n")
		b.WriteString("\tif n <= 0 {\n\t\treturn \"\", fmt.Errorf(\"bad n: %d\", n)\n\t}\n")
		b.WriteString("\tout := strings.Repeat(s, n)\n")
		b.WriteString("\treturn strings.TrimSpace(out), nil\n}\n\n")
	}
	return b.String()
}

func benchEditorPaint(b *testing.B, w, h, funcCount int) {
	body := buildGoFile(b, funcCount)
	path := filepath.Join(b.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		b.Fatal(err)
	}

	th := theme.DefaultDark()
	ed := editor.New(th)
	if err := ed.Open(path); err != nil {
		b.Fatal(err)
	}
	defer ed.Close()
	settleEditorSyntax(b, ed)

	screen := shelleditor.New(th)
	screen.Bind(ed)
	screen.SetFocused(true)

	// Production keeps a retained surface buffer (mainEditorSurf); paint
	// reuses it every frame. Layout once, then measure steady-state paint.
	buf := surface.NewCellBuffer(w, h)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: h})
	screen.Paint(buf, 0, 0, ui.PaintContext{Focused: true})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		screen.Paint(buf, 0, 0, ui.PaintContext{Focused: true})
	}
}

// BenchmarkEditorPaint_80x24 measures steady-state cell repaint of the
// DocumentEditor viewport at the minimum supported editor size.
func BenchmarkEditorPaint_80x24(b *testing.B) {
	benchEditorPaint(b, 80, 24, 200)
}

// BenchmarkEditorPaint_120x40 measures steady-state cell repaint at the
// standard product size.
func BenchmarkEditorPaint_120x40(b *testing.B) {
	benchEditorPaint(b, 120, 40, 200)
}

// BenchmarkEditorPaint_LargeFile_120x40 stresses chroma cache lookups and
// gutter work with a tall file where only a window is visible.
func BenchmarkEditorPaint_LargeFile_120x40(b *testing.B) {
	benchEditorPaint(b, 120, 40, 2000)
}

// BenchmarkEditorScrollRepaint_120x40 measures repaint after a scroll step
// (new line window, cached chroma entries).
func BenchmarkEditorScrollRepaint_120x40(b *testing.B) {
	body := buildGoFile(b, 1000)
	path := filepath.Join(b.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		b.Fatal(err)
	}

	th := theme.DefaultDark()
	ed := editor.New(th)
	if err := ed.Open(path); err != nil {
		b.Fatal(err)
	}
	defer ed.Close()
	settleEditorSyntax(b, ed)

	screen := shelleditor.New(th)
	screen.Bind(ed)
	screen.SetFocused(true)
	buf := surface.NewCellBuffer(120, 40)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 120, H: 40})
	screen.Paint(buf, 0, 0, ui.PaintContext{Focused: true})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ed.ScrollModel().ScrollBy(1)
		screen.Paint(buf, 0, 0, ui.PaintContext{Focused: true})
	}
}
