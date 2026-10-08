//go:build cgo

package term

import (
	"testing"

	"crude/tui/cell"
)

func TestSelectionColorsInvalidateOnlyOnChange(t *testing.T) {
	e := requireEmulator(t)
	e.Feed([]byte("abc"))
	e.SelectionPress(0, 0, 1)
	e.SelectionDrag(1, 0)
	e.SetSelectionColors(cell.White, cell.Blue)
	paintGrid(t, e)
	if e.paintDirty {
		t.Fatal("paint cache remained dirty after paint")
	}
	e.SetSelectionColors(cell.White, cell.Blue)
	if e.paintDirty {
		t.Fatal("unchanged selection colors invalidated paint")
	}
	e.SetSelectionColors(cell.Black, cell.Cyan)
	if !e.paintDirty {
		t.Fatal("changed selection colors kept stale paint cache")
	}
	if c := paintGrid(t, e).Get(0, 0); c.FG != cell.Black || c.BG != cell.Cyan {
		t.Fatalf("updated paint colors=%+v", c)
	}
}
