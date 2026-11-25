package surface

import (
	"testing"

	"crude/tui/cell"
)

func BenchmarkBlit_80x24(b *testing.B) {
	dst := NewCellBuffer(80, 24)
	src := NewCellBuffer(40, 12)
	src.Fill(cell.NewCell('x', cell.Red, cell.Blue, 0))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Blit(dst, 10, 5, src, nil)
	}
}

func BenchmarkWriteString_Line(b *testing.B) {
	buf := NewCellBuffer(120, 1)
	text := "package surface // the quick brown fox jumps over the lazy dog 中文测试"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.WriteString(0, 0, text, cell.NoColor, cell.NoColor, 0)
	}
}

func BenchmarkFlattenZOrder_Nested(b *testing.B) {
	root := NewSurface("root", 80, 24, cell.TransparentBlank())
	for i := 0; i < 20; i++ {
		child := NewSurface("c", 10, 5, cell.TransparentBlank())
		child.SetZOrder(i)
		root.AddChild(child)
		for j := 0; j < 3; j++ {
			g := NewSurface("g", 2, 2, cell.TransparentBlank())
			child.AddChild(g)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = root.FlattenZOrder()
	}
}

// BenchmarkFillRectDenseSpans clears a retained 160x48 pane through both edges
// of wide cells. Restoring the previously painted grid is outside the timer.
func BenchmarkFillRectDenseSpans(b *testing.B) {
	const w, h = 160, 48
	buf, source := NewCellBuffer(w, h), NewCellBuffer(w, h)
	for y := range h {
		for x := 0; x < w; x += 2 {
			source.SetSpan(x, y, cell.Cell{Char: '界', Content: "界", Width: 2})
		}
	}
	r := Rect{X: 1, W: w - 2, H: h}
	fill := cell.BlankCell(cell.Blue)
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		copy(buf.Cells(), source.Cells())
		b.StartTimer()
		buf.FillRect(r, fill)
	}
}
