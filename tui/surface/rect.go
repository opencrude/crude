package surface

// Rect represents a rectangular region in cell coordinates.
// X and Y are the top-left corner. W and H are the width and height.
// The rectangle is half-open: it includes (X, Y) but excludes
// (X+W, Y+H), similar to Go slice semantics.
type Rect struct {
	X, Y, W, H int
}

// Empty reports whether the rectangle has zero or negative area.
func (r Rect) Empty() bool {
	return r.W <= 0 || r.H <= 0
}

// Contains reports whether a point (x, y) is inside the rectangle.
// The right and bottom edges are exclusive.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Intersect returns the intersection of two rectangles.
// If they don't overlap, returns an empty rect.
func (r Rect) Intersect(other Rect) Rect {
	x1 := max(r.X, other.X)
	y1 := max(r.Y, other.Y)
	x2 := min(r.X+r.W, other.X+other.W)
	y2 := min(r.Y+r.H, other.Y+other.H)
	if x2 <= x1 || y2 <= y1 {
		return Rect{} // No overlap
	}
	return Rect{X: x1, Y: y1, W: x2 - x1, H: y2 - y1}
}

// Union returns the smallest rectangle containing both rectangles.
// If either rectangle is empty, the other is returned.
func (r Rect) Union(other Rect) Rect {
	if r.Empty() {
		return other
	}
	if other.Empty() {
		return r
	}
	x1 := min(r.X, other.X)
	y1 := min(r.Y, other.Y)
	x2 := max(r.X+r.W, other.X+other.W)
	y2 := max(r.Y+r.H, other.Y+other.H)
	return Rect{X: x1, Y: y1, W: x2 - x1, H: y2 - y1}
}

// FullRect returns a rect covering a buffer of the given size,
// starting at (0, 0).
func FullRect(w, h int) Rect {
	return Rect{X: 0, Y: 0, W: w, H: h}
}

// Right returns the right edge (exclusive) of the rectangle.
func (r Rect) Right() int {
	return r.X + r.W
}

// Bottom returns the bottom edge (exclusive) of the rectangle.
func (r Rect) Bottom() int {
	return r.Y + r.H
}

// Area returns the number of cells in the rectangle.
func (r Rect) Area() int {
	return r.W * r.H
}
