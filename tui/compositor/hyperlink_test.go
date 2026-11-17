package compositor

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

func TestFileLinksNeverReachTheOuterTerminal(t *testing.T) {
	c := NewCompositor(32, 1)
	c.BeginFrame()
	c.Root().Buffer().WriteString(0, 0, "src/main.go", cell.White, cell.NoColor, cell.AttrUnderline, cell.NewLink("src/main.go:12", ""))
	c.Composite()
	if strings.Contains(GenerateANSI(c.back), "]8;") {
		t.Fatal("a native file link escaped as an outer-terminal action")
	}
	screen := uv.NewScreenBuffer(32, 1)
	c.PresentSurface().Draw(screen, screen.Bounds())
	if screen.CellAt(0, 0).Link.URL != "" {
		t.Fatal("a native file link escaped through direct output")
	}
	if !c.front.Get(0, 0).Link.File() {
		t.Fatal("native metadata was removed before internal interaction")
	}
}

func TestHyperlinkTargetsReachBothOutputPathsAndChangeWithoutNewGlyphs(t *testing.T) {
	c := NewCompositor(12, 2)
	target := "https://example.test/complete"
	for _, href := range []string{target, target + "?new=target", ""} {
		c.BeginFrame()
		raw := "\x1b]8;;" + href + "\x1b\\\x1b[1mRead 界\x1b[0m\x1b]8;;\x1b\\ plain"
		rendering.RenderToBufferAt(c.Root().Buffer(), 0, 0, raw)
		c.Composite()
		text := GenerateANSI(c.back)
		reference := uv.NewScreenBuffer(12, 2)
		reference.Method = ansi.GraphemeWidth
		uv.NewStyledString(text).Draw(reference, reference.Bounds())
		direct := uv.NewScreenBuffer(12, 2)
		c.PresentSurface().Draw(direct, direct.Bounds())
		assertCellGrids(t, direct, reference)
		if got := direct.CellAt(0, 0).Link.URL; got != href {
			t.Fatalf("terminal target=%q want %q", got, href)
		}
		if direct.CellAt(7, 0).Link.URL != "" {
			t.Fatal("hyperlink leaked into following text")
		}
		// Also exercise the whole-row producer fast path, not only SetCell.
		fast := &countedRowScreen{benchmarkSnapshot: benchmarkSnapshot{uv.NewBuffer(12, 2)}}
		c.CurrentSurface().Draw(fast, fast.Bounds())
		if fast.CellAt(0, 0).Link.URL != href {
			t.Fatal("row publication lost target")
		}
	}
}
