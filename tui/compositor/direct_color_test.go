package compositor

import (
	"fmt"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Every cell changes colors on every frame: neither runs nor a bounded color
// cache can hide the cost of publishing arbitrary terminal video colors.
func BenchmarkDirectTrueColorFrame(b *testing.B) {
	const width, height = 200, 50
	src := surface.NewCellBuffer(width, height)
	dst := benchmarkSnapshot{uv.NewBuffer(width, height)}
	s := cellSurface{buffer: src, row: make([]uv.Cell, width)}
	b.ReportAllocs()
	b.ResetTimer()
	for frame := 0; frame < b.N; frame++ {
		for y := 0; y < height; y++ {
			for x := range src.Row(y) {
				n := uint32(frame*width*height + y*width + x)
				src.Row(y)[x] = cell.NewCell('X', cell.NewTrueColor(uint8(n>>16), uint8(n>>8), uint8(n)), cell.NewTrueColor(uint8(n), uint8(n>>16), uint8(n>>8)), 0)
			}
		}
		s.Draw(dst, dst.Bounds())
	}
}

func TestDirectNewTrueColorsPreserveSnapshotsAndCrops(t *testing.T) {
	for _, area := range []uv.Rectangle{uv.Rect(0, 0, 12, 3), uv.Rect(2, 0, 8, 3), uv.Rect(-2, 0, 12, 3)} {
		t.Run(fmt.Sprint(area), func(t *testing.T) {
			c := NewCompositor(12, 3)
			var saved benchmarkSnapshot
			var savedCell uv.Cell
			for frame := 0; frame < 4; frame++ {
				c.BeginFrame()
				root := c.Root().Buffer()
				for y := 0; y < 3; y++ {
					for x := 0; x < 12; x++ {
						root.Set(x, y, cell.NewCell('X', cell.NewTrueColor(uint8(frame*31+x), uint8(y+32), 253), cell.NewTrueColor(uint8(255-x), uint8(frame*71+y), uint8(x*21)), 0))
					}
				}
				root.SetSpan(3, 1, cell.Cell{Content: "界", Width: 2, FG: cell.NewTrueColor(0, 0, 0), BG: cell.NewTrueColor(255, 254, uint8(frame)), Attr: cell.AttrUndercurl})
				popup := surface.NewSurface("transparent", 2, 1, cell.TransparentBlank())
				popup.SetPosition(8, 2)
				popup.Buffer().Set(1, 0, cell.NewCell('Z', cell.NewTrueColor(12, 34, 56), cell.NoColor, cell.AttrBold))
				c.Root().AddChild(popup)
				c.Composite()
				want := uv.NewScreenBuffer(12, 3)
				want.Method = ansi.GraphemeWidth
				uv.NewStyledString(GenerateANSI(c.back)).Draw(want, area)
				got := benchmarkSnapshot{uv.NewBuffer(12, 3)}
				c.PresentSurface().Draw(got, area)
				assertCellGrids(t, uv.ScreenBuffer{RenderBuffer: &uv.RenderBuffer{Buffer: got.Buffer}}, want)
				if frame == 0 {
					saved, savedCell = got, *got.CellAt(max(0, area.Min.X), 0)
				} else if *saved.CellAt(max(0, area.Min.X), 0) != savedCell {
					t.Fatal("later producer frames mutated a retained color snapshot")
				}
				// Mutate the same front-buffer pointer, then draw again. Old
				// snapshot colors must remain independent of row scratch reuse.
				c.front.Set(0, 0, cell.NewCell('Y', cell.NewTrueColor(238, 54, uint8(frame)), cell.NewTrueColor(9, 8, 7), 0))
				latest := benchmarkSnapshot{uv.NewBuffer(12, 3)}
				c.CurrentSurface().Draw(latest, latest.Bounds())
				if latest.CellAt(0, 0).Style.Fg != uv.RGBColor(238, 54, uint8(frame)) {
					t.Fatal("same pointer cached stale color")
				}
			}
		})
	}
}

func TestDirectTrueColorsDoNotAllocatePerCell(t *testing.T) {
	const width, height = 200, 50
	src := surface.NewCellBuffer(width, height)
	dst := benchmarkSnapshot{uv.NewBuffer(width, height)}
	s := cellSurface{buffer: src, row: make([]uv.Cell, width)}
	frame := 0
	if got := testing.AllocsPerRun(10, func() {
		for y := 0; y < height; y++ {
			for x := range src.Row(y) {
				n := uint32(frame*width*height + y*width + x)
				src.Row(y)[x] = cell.NewCell('X', cell.NewTrueColor(uint8(n>>16), uint8(n>>8), uint8(n)), cell.NewTrueColor(uint8(n), uint8(n>>16), uint8(n>>8)), 0)
			}
		}
		frame++
		s.Draw(dst, dst.Bounds())
	}); got > 1 {
		t.Fatalf("truecolor snapshot allocates per cell: %v (one reusable SetCell escape permitted)", got)
	}
}

func TestDirectAttributesMatchParsedANSI(t *testing.T) {
	// The string renderer is an independent oracle for every combination,
	// including underline precedence. The no-attribute video path must not
	// alter default colors or hide a nonzero attribute combination.
	const all = cell.AttrBold | cell.AttrItalic | cell.AttrUnderline |
		cell.AttrStrikethrough | cell.AttrReverse | cell.AttrBlink | cell.AttrDim |
		cell.AttrUndercurl | cell.AttrUnderlineDotted
	src := surface.NewCellBuffer(1, 1)
	for attr := cell.Attrs(0); attr <= all; attr++ {
		for _, fg := range []cell.Color{cell.NoColor, cell.NewPaletteColor(0), cell.NewTrueColor(17, 251, 0)} {
			c := cell.NewCell('X', fg, cell.NewTrueColor(0, 0, 0), attr)
			src.Set(0, 0, c)
			want := uv.NewScreenBuffer(1, 1)
			uv.NewStyledString(GenerateANSI(src)).Draw(want, want.Bounds())
			got := directStyle(c)
			if !got.Equal(&want.CellAt(0, 0).Style) {
				t.Fatalf("attributes %d color %+v: direct %+v != parsed %+v", attr, fg, got, want.CellAt(0, 0).Style)
			}
		}
	}
}
