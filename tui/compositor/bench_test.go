package compositor

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
)

func BenchmarkFullRender_80x24(b *testing.B) {
	c := NewCompositor(80, 24)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.BeginFrame()
		layer := surface.NewSurface("main", 80, 24, cell.BlankCell(cell.NewTrueColor(13, 17, 23)))
		layer.Buffer().WriteString(0, 0, "crude compositor benchmark line", cell.NewTrueColor(230, 237, 243), cell.NoColor, 0)
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
