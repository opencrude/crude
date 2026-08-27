package editor

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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
		"- ~~Normalized UI event types~~ ✅ DONE\nsingle: ☰\nwide: 😀\ntwo wide: 😀 / ⚡\ncjk: 中文\nplain",
		1,
	)
	surf := surface.NewSurface("preview", width, 4, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	buf := surf.Buffer()
	foundCheck := false
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
		for x := 0; x < width; x++ {
			if c := buf.Get(x, y); c.Content == "✅" {
				foundCheck = true
				if c.Width != uint8(ansi.GraphemeWidth.StringWidth("✅")) {
					t.Fatalf("checkmark cell = %+v", c)
				}
			}
		}
	}
	if !foundCheck {
		t.Fatal("editor discarded the full checkmark grapheme")
	}

	// Reparse the exact compositor output through UV, as Bubble Tea does. The
	// scrollbar must remain at the physical right edge on every row.
	emitted := compositor.GenerateANSI(buf)
	uvBuffer := uv.NewScreenBuffer(width, 4)
	uvBuffer.Method = ansi.GraphemeWidth
	uv.NewStyledString(emitted).Draw(uvBuffer, uv.Rect(0, 0, width, 4))
	for y := 0; y < 4; y++ {
		cellAt := uvBuffer.CellAt(width-1, y)
		if cellAt == nil || (cellAt.Content != "░" && cellAt.Content != "█") {
			t.Fatalf("UV scrollbar row %d displaced from right edge: %#v", y, cellAt)
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
