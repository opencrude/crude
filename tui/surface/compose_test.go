package surface

import (
	"testing"

	"crude/tui/cell"
)

func opaqueLetter(ch rune) cell.Cell {
	return cell.NewCell(ch, cell.Red, cell.Blue, 0)
}

func TestBlit_TransparentSkipped(t *testing.T) {
	dst := NewCellBuffer(3, 1)
	dst.Fill(opaqueLetter('D'))
	src := NewCellBuffer(3, 1)
	src.Set(0, 0, opaqueLetter('S'))
	src.Set(1, 0, cell.TransparentBlank())
	src.Set(2, 0, opaqueLetter('T'))

	Blit(dst, 0, 0, src, nil)

	if dst.Get(0, 0).Char != 'S' {
		t.Fatalf("0: %+v", dst.Get(0, 0))
	}
	if dst.Get(1, 0).Char != 'D' {
		t.Fatalf("transparent should preserve D, got %+v", dst.Get(1, 0))
	}
	if dst.Get(2, 0).Char != 'T' {
		t.Fatalf("2: %+v", dst.Get(2, 0))
	}
}

func TestBlitBorderAtomicallyOccludesUnderlyingWideGrapheme(t *testing.T) {
	for _, overlayX := range []int{1, 2} {
		t.Run(string(rune('0'+overlayX)), func(t *testing.T) {
			dst := NewCellBuffer(5, 1)
			dst.Fill(cell.NewCell(' ', cell.White, cell.Black, 0))
			dst.WriteString(1, 0, "😀", cell.White, cell.Black, 0)

			border := NewCellBuffer(1, 1)
			border.Set(0, 0, cell.NewCell('│', cell.Yellow, cell.Black, 0))
			Blit(dst, overlayX, 0, border, nil)

			if got := dst.Get(overlayX, 0); got.Char != '│' || got.Width != 1 {
				t.Fatalf("overlay cell = %+v", got)
			}
			for column := 1; column <= 2; column++ {
				if column == overlayX {
					continue
				}
				got := dst.Get(column, 0)
				if got.Char != ' ' || got.Width != 1 {
					t.Fatalf("old emoji column %d survived as %+v", column, got)
				}
			}
		})
	}
}

func TestBlitNormalizesOrphanContinuation(t *testing.T) {
	dst := NewCellBuffer(3, 1)
	dst.Fill(cell.NewCell(' ', cell.White, cell.Black, 0))
	dst.Set(1, 0, cell.Cell{Width: 0, BG: cell.Black})
	src := NewCellBuffer(1, 1)
	src.Set(0, 0, cell.NewCell('│', cell.Yellow, cell.Black, 0))
	Blit(dst, 1, 0, src, nil)
	if got := dst.Get(1, 0); got.Char != '│' || got.Width != 1 {
		t.Fatalf("orphan continuation was not replaced: %+v", got)
	}
}

func TestBlit_BGInheritance(t *testing.T) {
	dst := NewCellBuffer(2, 1)
	dst.Set(0, 0, cell.BlankCell(cell.Green))
	src := NewCellBuffer(1, 1)
	// Explicit FG, default BG
	src.Set(0, 0, cell.NewCell('x', cell.Red, cell.NoColor, 0))
	Blit(dst, 0, 0, src, nil)
	got := dst.Get(0, 0)
	if got.Char != 'x' || !got.BG.Equal(cell.Green) {
		t.Fatalf("expected inherit Green BG, got %+v", got)
	}
}

func TestBlit_WideChar(t *testing.T) {
	dst := NewCellBuffer(4, 1)
	dst.Fill(cell.BlankCell(cell.Black))
	src := NewCellBuffer(2, 1)
	src.Set(0, 0, cell.NewWideCell('中', cell.Red, cell.Blue, 0))
	src.Set(1, 0, cell.Cell{Char: 0, Width: 0, FG: cell.Red, BG: cell.Blue})

	Blit(dst, 1, 0, src, nil)

	c0 := dst.Get(1, 0)
	if c0.Char != '中' || c0.Width != 2 {
		t.Fatalf("primary: %+v", c0)
	}
	c1 := dst.Get(2, 0)
	if c1.Width != 0 || c1.Char != 0 {
		t.Fatalf("continuation: %+v", c1)
	}
}

func TestBlit_ClipStack(t *testing.T) {
	dst := NewCellBuffer(5, 1)
	dst.Fill(opaqueLetter('.'))
	src := NewCellBuffer(5, 1)
	for x := 0; x < 5; x++ {
		src.Set(x, 0, opaqueLetter('X'))
	}
	cs := NewClipStack()
	cs.Push(Rect{X: 2, Y: 0, W: 2, H: 1})
	Blit(dst, 0, 0, src, cs)
	if dst.Get(0, 0).Char != '.' || dst.Get(1, 0).Char != '.' {
		t.Fatal("outside clip should remain")
	}
	if dst.Get(2, 0).Char != 'X' || dst.Get(3, 0).Char != 'X' {
		t.Fatal("inside clip should blit")
	}
	if dst.Get(4, 0).Char != '.' {
		t.Fatal("outside right should remain")
	}
}

func TestBlit_OffsetAndOOB(t *testing.T) {
	dst := NewCellBuffer(3, 3)
	src := NewCellBuffer(2, 2)
	src.Fill(opaqueLetter('S'))
	// Partially off-screen top-left
	Blit(dst, -1, -1, src, nil)
	// Only overlapping cell (1,1) of src maps to (0,0) of dst
	if dst.Get(0, 0).Char != 'S' {
		t.Fatalf("overlap cell: %+v", dst.Get(0, 0))
	}
}

func TestBlitOpaque_OverwritesTransparent(t *testing.T) {
	dst := NewCellBuffer(2, 1)
	dst.Fill(opaqueLetter('D'))
	src := NewCellBuffer(2, 1)
	src.Set(0, 0, cell.TransparentBlank())
	src.Set(1, 0, opaqueLetter('S'))
	BlitOpaque(dst, 0, 0, src)
	if !dst.Get(0, 0).Transparent {
		t.Fatalf("opaque blit should overwrite with transparent: %+v", dst.Get(0, 0))
	}
	if dst.Get(1, 0).Char != 'S' {
		t.Fatal("second cell")
	}
}

func TestMergeDown(t *testing.T) {
	dst := NewCellBuffer(3, 1)
	dst.Fill(opaqueLetter('D'))
	src := NewCellBuffer(3, 1)
	src.Set(0, 0, opaqueLetter('S'))
	src.Set(1, 0, cell.TransparentBlank())
	src.Set(2, 0, cell.NewWideCell('中', cell.Red, cell.Blue, 0))
	// Need only 1 cell for wide at end; buffer is 3 wide, so wide at 2 would need col 3
	// Place wide at 0 instead for second test; first check transparent skip
	MergeDown(dst, 0, 0, src)
	if dst.Get(0, 0).Char != 'S' {
		t.Fatal("merge primary")
	}
	if dst.Get(1, 0).Char != 'D' {
		t.Fatal("merge skip transparent")
	}
}

func TestMergeDown_Wide(t *testing.T) {
	dst := NewCellBuffer(4, 1)
	dst.Fill(opaqueLetter('D'))
	src := NewCellBuffer(2, 1)
	src.Set(0, 0, cell.NewWideCell('中', cell.Red, cell.Blue, 0))
	src.Set(1, 0, cell.Cell{Width: 0})
	MergeDown(dst, 0, 0, src)
	if dst.Get(0, 0).Char != '中' || dst.Get(0, 0).Width != 2 {
		t.Fatalf("wide primary: %+v", dst.Get(0, 0))
	}
	if dst.Get(1, 0).Width != 0 {
		t.Fatalf("wide cont: %+v", dst.Get(1, 0))
	}
}

func TestBlendAlpha(t *testing.T) {
	dst := NewCellBuffer(2, 1)
	dst.Set(0, 0, cell.NewCell('D', cell.NewTrueColor(0, 0, 0), cell.NewTrueColor(0, 0, 0), 0))
	src := NewCellBuffer(2, 1)
	src.Set(0, 0, cell.NewCell('S', cell.NewTrueColor(255, 0, 0), cell.NewTrueColor(255, 0, 0), 0))
	src.Set(1, 0, cell.TransparentBlank())

	BlendAlpha(dst, 0, 0, src, 0.5)

	got := dst.Get(0, 0)
	if got.Char != 'S' {
		t.Fatalf("char should come from source: %+v", got)
	}
	if got.FG.Type != cell.ColorTrueColor || got.FG.R < 100 {
		t.Fatalf("blended FG expected mid red, got %+v", got.FG)
	}
	// Transparent source cell skipped
	if dst.Get(1, 0).Char != 0 && dst.Get(1, 0).Char != ' ' {
		t.Fatalf("transparent source overwrote destination: %+v", dst.Get(1, 0))
	}
}

func TestBlendAlpha_Wide(t *testing.T) {
	dst := NewCellBuffer(3, 1)
	dst.Fill(cell.BlankCell(cell.NewTrueColor(0, 0, 0)))
	src := NewCellBuffer(2, 1)
	src.Set(0, 0, cell.NewWideCell('中', cell.NewTrueColor(255, 0, 0), cell.NewTrueColor(0, 0, 255), 0))
	src.Set(1, 0, cell.Cell{Width: 0})
	BlendAlpha(dst, 0, 0, src, 1.0)
	if dst.Get(0, 0).Char != '中' {
		t.Fatal("wide primary")
	}
	if dst.Get(1, 0).Width != 0 {
		t.Fatal("wide cont")
	}
}

func TestClearRegion(t *testing.T) {
	b := NewCellBuffer(4, 4)
	b.Fill(opaqueLetter('X'))
	ClearRegion(b, Rect{X: 1, Y: 1, W: 2, H: 2})
	if !b.Get(1, 1).Transparent {
		t.Fatal("cleared should be transparent")
	}
	if b.Get(0, 0).Char != 'X' {
		t.Fatal("outside clear preserved")
	}
}
