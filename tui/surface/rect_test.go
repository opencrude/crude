package surface

import "testing"

func TestRectEmpty(t *testing.T) {
	if !(Rect{}.Empty()) {
		t.Fatal("zero rect empty")
	}
	if !(Rect{W: 0, H: 5}.Empty()) {
		t.Fatal("zero width empty")
	}
	if (Rect{W: 1, H: 1}.Empty()) {
		t.Fatal("1x1 not empty")
	}
}

func TestRectContains(t *testing.T) {
	r := Rect{X: 2, Y: 3, W: 4, H: 5}
	// inclusive start, exclusive end
	if !r.Contains(2, 3) {
		t.Fatal("top-left should be inside")
	}
	if r.Contains(6, 3) { // X+W = 6 exclusive
		t.Fatal("right edge exclusive")
	}
	if r.Contains(2, 8) { // Y+H = 8 exclusive
		t.Fatal("bottom edge exclusive")
	}
	if !r.Contains(5, 7) {
		t.Fatal("bottom-right-1 should be inside")
	}
	if r.Contains(1, 3) {
		t.Fatal("left of rect")
	}
}

func TestRectIntersect(t *testing.T) {
	a := Rect{X: 0, Y: 0, W: 10, H: 10}
	b := Rect{X: 5, Y: 5, W: 10, H: 10}
	got := a.Intersect(b)
	want := Rect{X: 5, Y: 5, W: 5, H: 5}
	if got != want {
		t.Fatalf("intersect: got %+v want %+v", got, want)
	}
	c := Rect{X: 100, Y: 100, W: 1, H: 1}
	if !a.Intersect(c).Empty() {
		t.Fatal("no overlap should be empty")
	}
	// Touching edge only
	d := Rect{X: 10, Y: 0, W: 1, H: 1}
	if !a.Intersect(d).Empty() {
		t.Fatal("touching exclusive edge is empty")
	}
}

func TestRectUnion(t *testing.T) {
	a := Rect{X: 0, Y: 0, W: 2, H: 2}
	b := Rect{X: 3, Y: 3, W: 2, H: 2}
	got := a.Union(b)
	want := Rect{X: 0, Y: 0, W: 5, H: 5}
	if got != want {
		t.Fatalf("union: %+v want %+v", got, want)
	}
	if a.Union(Rect{}) != a {
		t.Fatal("union empty other")
	}
	if (Rect{}).Union(b) != b {
		t.Fatal("union empty self")
	}
}

func TestRectHelpers(t *testing.T) {
	r := Rect{X: 1, Y: 2, W: 3, H: 4}
	if r.Right() != 4 || r.Bottom() != 6 {
		t.Fatalf("edges: right=%d bottom=%d", r.Right(), r.Bottom())
	}
	if r.Area() != 12 {
		t.Fatalf("area=%d", r.Area())
	}
	fr := FullRect(80, 24)
	if fr != (Rect{W: 80, H: 24}) {
		t.Fatalf("FullRect: %+v", fr)
	}
}
