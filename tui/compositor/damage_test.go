package compositor

import (
	"testing"

	"crude/tui/surface"
)

func TestDamageRegion_MarkAndBounds(t *testing.T) {
	d := NewDamageRegion()
	if d.HasDamage() {
		t.Fatal("new empty")
	}
	if !d.Bounds().Empty() {
		t.Fatal("empty bounds")
	}
	d.Mark(3, 4)
	if !d.HasDamage() {
		t.Fatal("has damage")
	}
	b := d.Bounds()
	if b != (surface.Rect{X: 3, Y: 4, W: 1, H: 1}) {
		t.Fatalf("bounds %+v", b)
	}
	d.Mark(1, 6)
	b = d.Bounds()
	// minX=1, minY=4, maxX=4, maxY=7 → W=3 H=3
	if b.X != 1 || b.Y != 4 || b.W != 3 || b.H != 3 {
		t.Fatalf("expanded %+v", b)
	}
}

func TestDamageRegion_MarkRect(t *testing.T) {
	d := NewDamageRegion()
	d.MarkRect(surface.Rect{}) // empty no-op
	if d.HasDamage() {
		t.Fatal("empty rect")
	}
	d.MarkRect(surface.Rect{X: 2, Y: 2, W: 3, H: 4})
	b := d.Bounds()
	if b != (surface.Rect{X: 2, Y: 2, W: 3, H: 4}) {
		t.Fatalf("%+v", b)
	}
	d.MarkRect(surface.Rect{X: 0, Y: 0, W: 1, H: 1})
	b = d.Bounds()
	if b.X != 0 || b.Y != 0 || b.W != 5 || b.H != 6 {
		t.Fatalf("union rect %+v", b)
	}
}

func TestDamageRegion_Contains(t *testing.T) {
	d := NewDamageRegion()
	if d.ContainsRow(0) || d.ContainsCell(0, 0) {
		t.Fatal("empty contains")
	}
	d.MarkRect(surface.Rect{X: 2, Y: 3, W: 4, H: 5})
	if !d.ContainsRow(3) || !d.ContainsRow(7) {
		t.Fatal("row inside")
	}
	if d.ContainsRow(2) || d.ContainsRow(8) {
		t.Fatal("row outside")
	}
	if !d.ContainsCell(2, 3) || !d.ContainsCell(5, 7) {
		t.Fatal("cell inside")
	}
	if d.ContainsCell(6, 3) || d.ContainsCell(2, 8) {
		t.Fatal("cell outside exclusive")
	}
}

func TestDamageRegion_Reset(t *testing.T) {
	d := NewDamageRegion()
	d.Mark(1, 1)
	d.Reset()
	if d.HasDamage() {
		t.Fatal("reset")
	}
	if d.MinX != 0 || d.MaxX != 0 {
		t.Fatal("coords cleared")
	}
}

func TestDamageRegion_Union(t *testing.T) {
	a := NewDamageRegion()
	b := NewDamageRegion()
	a.Union(b) // no-op
	if a.HasDamage() {
		t.Fatal("union empty")
	}
	b.MarkRect(surface.Rect{X: 5, Y: 5, W: 2, H: 2})
	a.Union(b)
	if a.Bounds() != b.Bounds() {
		t.Fatal("union into empty")
	}
	c := NewDamageRegion()
	c.Mark(0, 0)
	a.Union(c)
	if a.Bounds().X != 0 || a.Bounds().Y != 0 {
		t.Fatalf("%+v", a.Bounds())
	}
}
