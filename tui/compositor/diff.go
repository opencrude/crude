package compositor

import (
	"crude/tui/cell"
	"crude/tui/surface"
)

// DiffResult contains the cells that changed between two buffers.
type DiffResult struct {
	Cells []CellChange
}

// CellChange represents a single changed cell and its new value.
type CellChange struct {
	X, Y int
	Cell cell.Cell
}

// Diff compares two cell buffers within the damage region and returns
// the cells that differ. This is used to generate minimal ANSI output.
//
// Optimization: only rows within the damage region bounds are examined.
// Within each row, only columns within the damage region are compared.
// Continuation cells (Width==0) are skipped; they are handled by their
// primary wide cell.
func Diff(front, back *surface.CellBuffer, damage *DamageRegion) *DiffResult {
	result := &DiffResult{}

	if !damage.HasDamage() {
		return result
	}

	w := front.Width()
	h := front.Height()

	minY := damage.MinY
	maxY := damage.MaxY
	minX := damage.MinX
	maxX := damage.MaxX

	// Clamp to buffer bounds
	if minY < 0 {
		minY = 0
	}
	if maxY > h {
		maxY = h
	}
	if minX < 0 {
		minX = 0
	}
	if maxX > w {
		maxX = w
	}

	for y := minY; y < maxY; y++ {
		for x := minX; x < maxX; x++ {
			fc := front.Get(x, y)
			bc := back.Get(x, y)

			// Skip continuation cells; they're handled by their primary wide cell
			if bc.Width == 0 {
				continue
			}

			if !fc.Equal(bc) {
				result.Cells = append(result.Cells, CellChange{X: x, Y: y, Cell: bc})
			}
		}
	}

	return result
}

// DiffRow compares a single row between two buffers.
// Returns the changed cells for that row within the column range [minX, maxX).
func DiffRow(front, back *surface.CellBuffer, y int, minX, maxX int) []CellChange {
	var changes []CellChange

	w := front.Width()
	h := front.Height()

	if y < 0 || y >= h {
		return changes
	}

	// Clamp column range
	if minX < 0 {
		minX = 0
	}
	if maxX > w {
		maxX = w
	}

	for x := minX; x < maxX; x++ {
		fc := front.Get(x, y)
		bc := back.Get(x, y)

		// Skip continuation cells
		if bc.Width == 0 {
			continue
		}

		if !fc.Equal(bc) {
			changes = append(changes, CellChange{X: x, Y: y, Cell: bc})
		}
	}

	return changes
}

// DiffCount returns the number of changed cells without building the full list.
// Useful for deciding whether full-redraw or diff-based output is more efficient.
func DiffCount(front, back *surface.CellBuffer, damage *DamageRegion) int {
	if !damage.HasDamage() {
		return 0
	}

	w := front.Width()
	h := front.Height()
	count := 0

	minY := damage.MinY
	maxY := damage.MaxY
	minX := damage.MinX
	maxX := damage.MaxX

	if minY < 0 {
		minY = 0
	}
	if maxY > h {
		maxY = h
	}
	if minX < 0 {
		minX = 0
	}
	if maxX > w {
		maxX = w
	}

	for y := minY; y < maxY; y++ {
		for x := minX; x < maxX; x++ {
			fc := front.Get(x, y)
			bc := back.Get(x, y)

			// Skip continuation cells
			if bc.Width == 0 {
				continue
			}

			if !fc.Equal(bc) {
				count++
			}
		}
	}

	return count
}
