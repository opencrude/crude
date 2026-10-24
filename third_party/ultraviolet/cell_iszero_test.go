package uv

import (
	"image/color"
	"testing"
)

// External colors may contain arbitrary, non-comparable interface state. Only
// their terminal value crosses the input boundary; cells never compare or retain
// the external object. Normalization also makes scalar IsZero checks dependable.
type recursiveColor struct {
	color.Color
	r, g, b, a uint8
}

func (c *recursiveColor) RGBA() (uint32, uint32, uint32, uint32) {
	return uint32(c.r) * 257, uint32(c.g) * 257, uint32(c.b) * 257, uint32(c.a) * 257
}

func TestCellIsZero_RecursiveColorPanicsOrHangs(t *testing.T) {
	external := &recursiveColor{r: 255, a: 255}
	c := Cell{Content: "x", Width: 1, Style: Style{Fg: NewColor(external)}}
	external.r = 0
	if c.IsZero() || c.Style.Fg != RGBColor(255, 0, 0) {
		t.Fatal("cell lost normalized immutable color")
	}
}

func TestStyleIsZero_RecursiveColorPanicsOrHangs(t *testing.T) {
	s := Style{Fg: NewColor(&recursiveColor{r: 255, a: 255})}
	if s.IsZero() || s.Fg != RGBColor(255, 0, 0) {
		t.Fatal("nondefault external color became a zero style")
	}
}

func TestCellIsZero_NilColorDoesNotHang(t *testing.T) {
	for _, tt := range []struct {
		name string
		cell Cell
		want bool
	}{
		{"zero cell", Cell{}, true},
		{"default style colors", Cell{Content: "x", Width: 1}, false},
		{"empty content with default colors", Cell{Content: " ", Width: 1}, false},
		{"explicit RGB black", Cell{Style: Style{Fg: RGBColor(0, 0, 0)}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cell.IsZero(); got != tt.want {
				t.Fatalf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}
