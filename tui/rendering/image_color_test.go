package rendering

import (
	"image/color"
	"testing"

	"crude/tui/cell"
)

func TestImagePixelToCellRetainsContentColorAndAlpha(t *testing.T) {
	background := cell.NewTrueColor(20, 40, 60)
	for _, tc := range []struct {
		name  string
		pixel color.Color
		want  cell.Color
	}{
		{"missing", nil, background},
		{"transparent", color.NRGBA{R: 255}, background},
		{"opaque", color.NRGBA{R: 255, G: 100, B: 50, A: 255}, cell.NewTrueColor(255, 100, 50)},
		{"straight alpha", color.NRGBA{R: 255, A: 128}, cell.NewTrueColor(137, 19, 29)},
		{"premultiplied alpha", color.RGBA{R: 128, A: 128}, cell.NewTrueColor(137, 19, 29)},
		{"gray", color.Gray{Y: 123}, cell.NewTrueColor(123, 123, 123)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ImagePixelToCell(tc.pixel, background); got != tc.want {
				t.Fatalf("source pixel %v became %v, want %v", tc.pixel, got, tc.want)
			}
		})
	}
}
