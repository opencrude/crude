package compositor

import (
	"fmt"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func assertCellGrids(t testing.TB, got, want uv.ScreenBuffer) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatalf("bounds %v != %v", got.Bounds(), want.Bounds())
	}
	for y := 0; y < got.Height(); y++ {
		for x := 0; x < got.Width(); x++ {
			if !got.CellAt(x, y).Equal(want.CellAt(x, y)) {
				t.Fatalf("cell (%d,%d): got %#v want %#v", x, y, *got.CellAt(x, y), *want.CellAt(x, y))
			}
		}
	}
}

func TestDirectSurfaceMatchesANSIReference(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {19, 7}, {1, 1}, {120, 40}, {0, 0}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			c := NewCompositor(size[0], size[1])
			c.BeginFrame()
			root := c.Root().Buffer()
			root.Fill(cell.BlankCell(cell.NewTrueColor(12, 34, 56)))
			for y := 0; y < size[1]; y++ {
				root.WriteString(0, y, "Aé 界 ♻️ 👨‍👩‍👧‍👦 🇯🇵 END", cell.NewPaletteColor(uint8(y)), cell.NoColor, cell.Attrs(y%128))
			}
			popup := surface.NewSurface("popup", min(12, size[0]), min(3, size[1]), cell.TransparentBlank())
			popup.SetPosition(2, 1)
			popup.Buffer().WriteString(0, 0, "overlay 界", cell.White, cell.Blue, cell.AttrReverse|cell.AttrBold)
			c.Root().AddChild(popup)
			c.Composite()
			text := GenerateANSI(c.back)
			direct, reference := uv.NewScreenBuffer(size[0], size[1]), uv.NewScreenBuffer(size[0], size[1])
			reference.Method = ansi.GraphemeWidth
			uv.NewStyledString(text).Draw(reference, reference.Bounds())
			c.PresentSurface().Draw(direct, direct.Bounds())
			assertCellGrids(t, direct, reference)
		})
	}
}

func TestDirectPublicationTokenFollowsFrameAndGeometry(t *testing.T) {
	c := NewCompositor(8, 2)
	c.BeginFrame()
	c.Composite()
	first := c.PresentSurface().(uv.SnapshotSurface).SnapshotToken()
	if first.Owner == nil || first.Generation == 0 || c.CurrentSurface().(uv.SnapshotSurface).SnapshotToken() != first {
		t.Fatal("current completed frame has no stable publication identity")
	}
	c.BeginFrame()
	c.Composite()
	next := c.PresentSurface().(uv.SnapshotSurface).SnapshotToken()
	if next.Owner != first.Owner || next.Generation == first.Generation {
		t.Fatal("new completed frame retained its predecessor generation")
	}
	c.Resize(4, 1)
	resized := c.CurrentSurface().(uv.SnapshotSurface).SnapshotToken()
	if resized.Owner != first.Owner || resized.Generation == next.Generation {
		t.Fatal("resize reused a snapshot with old geometry")
	}
	other := NewCompositor(4, 1)
	other.finishFrame()
	if other.CurrentSurface().(uv.SnapshotSurface).SnapshotToken().Owner == first.Owner {
		t.Fatal("independent producer shared a publication identity")
	}
}

func TestDirectSurfacePreservesMeasuredWidthsAndUnderlineVariants(t *testing.T) {
	c := NewCompositor(10, 1)
	c.BeginFrame()
	c.Root().Buffer().Set(0, 0, cell.Cell{Char: '🧑', Content: "🧑‍🌾", Width: 4, Attr: cell.AttrUndercurl})
	c.Root().Buffer().Set(4, 0, cell.Cell{Char: '|', Width: 1, Attr: cell.AttrUnderlineDotted})
	c.Composite()
	buf := uv.NewScreenBuffer(10, 1)
	c.PresentSurface().Draw(buf, buf.Bounds())
	if got := buf.CellAt(0, 0); got.Content != "🧑‍🌾" || got.Width != 4 || got.Style.Underline != uv.UnderlineCurly {
		t.Fatalf("lost measured grapheme: %#v", got)
	}
	if got := buf.CellAt(4, 0); got.Content != "|" || got.Style.Underline != uv.UnderlineDotted {
		t.Fatalf("sentinel shifted or underline lost: %#v", got)
	}
}

type countedRowScreen struct {
	benchmarkSnapshot
	rows, cells int
}

func (s *countedRowScreen) SetCellRow(y int, row []uv.Cell) {
	s.rows++
	copy(s.Lines[y], row)
}

func (s *countedRowScreen) SetCell(x, y int, c *uv.Cell) {
	s.cells++
	s.Buffer.SetCell(x, y, c)
}

func TestDirectSnapshotRowsMatchIndividualWrites(t *testing.T) {
	for _, area := range []uv.Rectangle{uv.Rect(0, 0, 12, 3), uv.Rect(2, 0, 8, 3), uv.Rect(-2, 0, 12, 3)} {
		for _, malformed := range []bool{false, true} {
			c := NewCompositor(12, 3)
			c.BeginFrame()
			c.Root().Buffer().Fill(cell.BlankCell(cell.Blue))
			c.Root().Buffer().WriteString(0, 0, "A界 é end", cell.White, cell.Blue, cell.AttrUndercurl)
			c.Root().Buffer().SetSpan(0, 1, cell.Cell{Content: "🧑‍🌾", Width: 4, BG: cell.Blue})
			c.Composite()
			c.PresentSurface()
			if malformed {
				c.front.Row(0)[0].Width = 0  // orphan
				c.front.Row(1)[2].Width = 1  // primary inside a measured span
				c.front.Row(2)[11].Width = 4 // crosses the edge
			}
			got := &countedRowScreen{benchmarkSnapshot: benchmarkSnapshot{uv.NewBuffer(12, 3)}}
			want := uv.NewScreenBuffer(12, 3)
			// Start with old wide cells and links: whole-row replacement must
			// erase them, while clipped writes retain the same UV behavior.
			for y := 0; y < 3; y++ {
				old := &uv.Cell{Content: "界", Width: 2, Link: uv.Link{URL: "https://example.org"}}
				got.Buffer.SetCell(3, y, old)
				want.Buffer.SetCell(3, y, old)
			}
			cellSurface{buffer: c.front}.Draw(want, area)
			c.CurrentSurface().Draw(got, area)
			assertCellGrids(t, uv.ScreenBuffer{RenderBuffer: &uv.RenderBuffer{Buffer: got.Buffer}}, want)
			if !malformed && area == uv.Rect(0, 0, 12, 3) && (got.rows != 3 || got.cells != 0) {
				t.Fatalf("full snapshot must submit one row, not per-cell writes: rows=%d cells=%d", got.rows, got.cells)
			}
			if malformed && got.rows != 0 {
				t.Fatalf("malformed rows bypassed UV normalization: %d", got.rows)
			}
		}
	}
}

func TestDirectSnapshotReadsLatestCellsAndDoesNotAliasScratch(t *testing.T) {
	c := NewCompositor(8, 2)
	c.BeginFrame()
	c.Root().Buffer().Fill(cell.BlankCell(cell.Blue))
	c.Root().Buffer().WriteString(0, 0, "first", cell.White, cell.Blue, 0)
	c.Root().Buffer().WriteString(0, 1, "second", cell.Red, cell.Blue, 0)
	c.Composite()
	src := c.PresentSurface()
	dst := benchmarkSnapshot{uv.NewBuffer(8, 2)}
	src.Draw(dst, dst.Bounds())
	if dst.CellAt(0, 0).Content != "f" || dst.CellAt(0, 1).Content != "s" {
		t.Fatal("snapshot retained reused row scratch")
	}
	c.front.Set(0, 0, cell.NewCell('Z', cell.Green, cell.Blue, 0))
	if dst.CellAt(0, 0).Content != "f" {
		t.Fatal("snapshot aliased producer cells")
	}
	src.Draw(dst, dst.Bounds())
	if dst.CellAt(0, 0).Content != "Z" {
		t.Fatal("same source pointer hid a content mutation")
	}
}

// Random resize, clearing, overlap and frame-reuse sequences compare the direct
// output against an independently parsed full repaint. A skipped presentation
// does not change the state being painted by either route.
func FuzzDirectSurfaceResize(f *testing.F) {
	f.Add([]byte{80, 24, 1, 4, 40, 12, 0, 2, 1, 1, 1, 3})
	f.Add([]byte{0, 0, 0, 0, 32, 8, 1, 255})
	f.Fuzz(func(t *testing.T, sequence []byte) {
		if len(sequence) > 256 {
			sequence = sequence[:256]
		}
		c := NewCompositor(1, 1)
		for i := 0; i+3 < len(sequence); i += 4 {
			w, h := int(sequence[i]%81), int(sequence[i+1]%25)
			c.Resize(w, h)
			c.BeginFrame()
			c.Root().Buffer().Fill(cell.BlankCell(cell.NoColor))
			if h > 0 && sequence[i+2]&1 != 0 {
				c.Root().Buffer().WriteString(0, int(sequence[i+3])%h, "é 界 🇯🇵 end", cell.Red, cell.Blue, cell.AttrBold)
			}
			c.Composite()
			reference := uv.NewScreenBuffer(w, h)
			reference.Method = ansi.GraphemeWidth
			uv.NewStyledString(GenerateANSI(c.back)).Draw(reference, reference.Bounds())
			c.PresentSurface()
			for repeat := 0; repeat < 2; repeat++ {
				direct := uv.NewScreenBuffer(w, h)
				c.CurrentSurface().Draw(direct, direct.Bounds())
				assertCellGrids(t, direct, reference)
			}
		}
	})
}

func BenchmarkDirectCellPresentation(b *testing.B) {
	for _, size := range [][2]int{{80, 24}, {200, 60}} {
		for _, direct := range []bool{false, true} {
			b.Run(fmt.Sprintf("%dx%d/direct=%v", size[0], size[1], direct), func(b *testing.B) {
				src := surface.NewCellBuffer(size[0], size[1])
				src.Fill(cell.BlankCell(cell.Blue))
				for y := 0; y < size[1]; y++ {
					src.WriteString(0, y, "Dense styled text 界 é ♻️ 🧑‍🌾 sentinel", cell.White, cell.Blue, cell.AttrBold)
				}
				dst := uv.NewScreenBuffer(size[0], size[1])
				dst.Method = ansi.GraphemeWidth
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					dst.Clear()
					if direct {
						cellSurface{buffer: src}.Draw(dst, dst.Bounds())
					} else {
						uv.NewStyledString(GenerateANSI(src)).Draw(dst, dst.Bounds())
					}
				}
			})
		}
	}
}
