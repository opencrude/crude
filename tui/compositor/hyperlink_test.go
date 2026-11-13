package compositor

import (
	"crude/tui/rendering"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"testing"
)

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
