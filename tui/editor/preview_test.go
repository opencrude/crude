package editor

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestPreviewUsesSourceLineNumbersAndChroma(t *testing.T) {
	ed := NewPreview(theme.DefaultDark(), "/tmp/main.go", "func main() {\n\treturn\n}", 40)
	ed.SetSelection(0, 0, 0, 4)
	surf := surface.NewSurface("preview", 60, 4, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	buf := surf.Buffer()
	plain := plainRow(buf, 0, 60)
	if !strings.Contains(plain, "40") || !strings.Contains(plain, "func main") {
		t.Fatalf("preview first line=%q", plain)
	}
	foundHL := false
	for x := 0; x < 59; x++ {
		c := buf.Get(x, 0)
		if c.Char == 'f' && !c.FG.IsDefault() {
			foundHL = true
			break
		}
	}
	if !foundHL {
		t.Fatal("preview should contain Chroma/selection styling in cell buffer")
	}
}

func TestRenderWideGlyphsKeepScrollbarColumn(t *testing.T) {
	const width = 60
	ed := NewPreview(
		theme.DefaultDark(),
		"/tmp/width.md",
		"single: ☰\nwide: 😀\ntwo wide: 😀 / ⚡\ncjk: 中文\nplain",
		1,
	)
	surf := surface.NewSurface("preview", width, 4, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	buf := surf.Buffer()
	for y := 0; y < 4; y++ {
		scrollbarAt := -1
		for x := width - 1; x >= 0; x-- {
			ch := buf.Get(x, y).Char
			if ch == '░' || ch == '█' {
				scrollbarAt = x
				break
			}
		}
		if scrollbarAt < 0 {
			t.Fatalf("line %d has no scrollbar column", y)
		}
		if scrollbarAt != width-1 {
			t.Fatalf("line %d scrollbar column = %d, want %d", y, scrollbarAt, width-1)
		}
	}
}

func plainRow(buf *surface.CellBuffer, y, maxW int) string {
	var b strings.Builder
	for x := 0; x < maxW && x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Char != 0 {
			b.WriteRune(c.Char)
		}
	}
	return strings.TrimRight(b.String(), " ")
}
