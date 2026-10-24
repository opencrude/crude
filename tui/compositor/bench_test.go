package compositor

import (
	"fmt"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Match Bubble Tea's producer snapshot: no damage tracking on the destination.
type benchmarkSnapshot struct{ *uv.Buffer }

func (benchmarkSnapshot) WidthMethod() uv.WidthMethod { return ansi.GraphemeWidth }

func (s benchmarkSnapshot) SetCellRow(y int, row []uv.Cell) { copy(s.Lines[y], row) }

func BenchmarkFramePipeline(b *testing.B) {
	for _, size := range [][2]int{{80, 24}, {160, 48}, {200, 60}} {
		b.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(b *testing.B) {
			c := NewCompositor(size[0], size[1])
			bg := cell.BlankCell(cell.NewTrueColor(13, 17, 23))
			layer := surface.NewSurface("main", size[0], size[1], bg)
			for y := 0; y < size[1]; y++ {
				layer.Buffer().WriteString(0, y, "Dense styled text 界 é ♻️ 🧑‍🌾 sentinel", cell.White, bg.BG, cell.AttrBold)
			}
			dst := benchmarkSnapshot{uv.NewBuffer(size[0], size[1])}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.BeginFrame()
				c.Root().Buffer().Fill(bg)
				c.Root().AddChild(layer)
				c.Composite()
				c.PresentSurface().Draw(dst, dst.Bounds())
			}
		})
	}
}

func BenchmarkFrameBufferFill(b *testing.B) {
	buf := surface.NewCellBuffer(160, 48)
	bg := cell.BlankCell(cell.NewTrueColor(13, 17, 23))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Clear()
		buf.Fill(bg)
	}
}

func BenchmarkFullRender_80x24(b *testing.B) {
	c := NewCompositor(80, 24)
	// The layer surface is created ONCE and re-attached each frame, exactly
	// how persistent layer surfaces behave in production. BeginFrame detaches
	// children but does not destroy their buffers.
	layer := surface.NewSurface("main", 80, 24, cell.BlankCell(cell.NewTrueColor(13, 17, 23)))
	layer.Buffer().WriteString(0, 0, "crude compositor benchmark line", cell.NewTrueColor(230, 237, 243), cell.NoColor, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.BeginFrame()
		c.Root().AddChild(layer)
		c.Composite()
		_ = c.Present()
	}
}

func BenchmarkGenerateANSI_80x24(b *testing.B) {
	buf := surface.NewCellBuffer(80, 24)
	for y := 0; y < 24; y++ {
		buf.WriteString(0, y, "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ----------", cell.Red, cell.Blue, cell.AttrBold)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = GenerateANSI(buf)
	}
}
