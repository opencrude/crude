package compositor

import (
	"crude/tui/surface"
)

// DamageRegion tracks which cells changed during a frame.
//
// For efficiency, we track a bounding box rather than individual cells.
// The diff only examines rows within the bounding box.
// On a typical streaming frame, only 10-20 rows change out of 50+,
// giving a 3-5x reduction in diff work.
//
// For future optimization, we can track multiple non-overlapping rectangles
// instead of a single bounding box.
type DamageRegion struct {
	MinX, MinY int // Top-left corner of the damage box
	MaxX, MaxY int // Bottom-right corner (exclusive)
	hasDamage  bool
}

// NewDamageRegion creates an empty damage region.
func NewDamageRegion() *DamageRegion {
	return &DamageRegion{}
}

// Mark records that cell (x, y) was written this frame.
// Expands the bounding box to include this point.
func (d *DamageRegion) Mark(x, y int) {
	if !d.hasDamage {
		d.MinX = x
		d.MinY = y
		d.MaxX = x + 1
		d.MaxY = y + 1
		d.hasDamage = true
		return
	}
	if x < d.MinX {
		d.MinX = x
	}
	if y < d.MinY {
		d.MinY = y
	}
	if x+1 > d.MaxX {
		d.MaxX = x + 1
	}
	if y+1 > d.MaxY {
		d.MaxY = y + 1
	}
}

// MarkRect records that a rectangular region was modified.
func (d *DamageRegion) MarkRect(r surface.Rect) {
	if r.Empty() {
		return
	}
	right := r.X + r.W
	bottom := r.Y + r.H
	if !d.hasDamage {
		d.MinX = r.X
		d.MinY = r.Y
		d.MaxX = right
		d.MaxY = bottom
		d.hasDamage = true
		return
	}
	if r.X < d.MinX {
		d.MinX = r.X
	}
	if r.Y < d.MinY {
		d.MinY = r.Y
	}
	if right > d.MaxX {
		d.MaxX = right
	}
	if bottom > d.MaxY {
		d.MaxY = bottom
	}
}

// Bounds returns the bounding box of all damaged cells.
// Returns an empty rect if no damage has been recorded.
func (d *DamageRegion) Bounds() surface.Rect {
	if !d.hasDamage {
		return surface.Rect{}
	}
	return surface.Rect{
		X: d.MinX,
		Y: d.MinY,
		W: d.MaxX - d.MinX,
		H: d.MaxY - d.MinY,
	}
}

// HasDamage reports whether any cells were damaged.
func (d *DamageRegion) HasDamage() bool {
	return d.hasDamage
}

// Reset clears the damage region for a new frame.
func (d *DamageRegion) Reset() {
	d.hasDamage = false
	d.MinX = 0
	d.MinY = 0
	d.MaxX = 0
	d.MaxY = 0
}

// ContainsRow reports whether a row is within the damage region.
// Used by the diff to skip unchanged rows.
func (d *DamageRegion) ContainsRow(y int) bool {
	if !d.hasDamage {
		return false
	}
	return y >= d.MinY && y < d.MaxY
}

// ContainsCell reports whether a specific cell is within the damage region.
func (d *DamageRegion) ContainsCell(x, y int) bool {
	if !d.hasDamage {
		return false
	}
	return x >= d.MinX && x < d.MaxX && y >= d.MinY && y < d.MaxY
}

// Union merges another damage region into this one.
// The resulting region is the bounding box of both regions.
func (d *DamageRegion) Union(other *DamageRegion) {
	if !other.hasDamage {
		return
	}
	d.MarkRect(other.Bounds())
}
