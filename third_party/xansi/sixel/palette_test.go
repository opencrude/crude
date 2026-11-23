package sixel

import (
	"context"
	"image"
	"image/color"
	"slices"
	"testing"
)

type testCase struct {
	maxColors       int
	expectedPalette []sixelColor
}

func TestPaletteCreationRedGreen(t *testing.T) {
	redGreen := image.NewRGBA(image.Rect(0, 0, 2, 2))
	redGreen.Set(0, 0, color.RGBA{255, 0, 0, 255})
	redGreen.Set(0, 1, color.RGBA{128, 0, 0, 255})
	redGreen.Set(1, 0, color.RGBA{0, 255, 0, 255})
	redGreen.Set(1, 1, color.RGBA{0, 128, 0, 255})

	testCases := map[string]testCase{
		"way too many colors": {
			maxColors: 16,
			expectedPalette: []sixelColor{
				{100, 0, 0, 100},
				{50, 0, 0, 100},
				{0, 100, 0, 100},
				{0, 50, 0, 100},
			},
		},
		"just the right number of colors": {
			maxColors: 4,
			expectedPalette: []sixelColor{
				{100, 0, 0, 100},
				{50, 0, 0, 100},
				{0, 100, 0, 100},
				{0, 50, 0, 100},
			},
		},
		"color reduction": {
			maxColors: 2,
			expectedPalette: []sixelColor{
				{75, 0, 0, 100},
				{0, 75, 0, 100},
			},
		},
	}

	runTests(t, redGreen, testCases)
}

func TestPaletteWithSemiTransparency(t *testing.T) {
	blueAlpha := image.NewRGBA(image.Rect(0, 0, 2, 2))
	blueAlpha.Set(0, 0, color.RGBA{0, 0, 255, 255})
	blueAlpha.Set(0, 1, color.RGBA{0, 0, 128, 255})
	blueAlpha.Set(1, 0, color.RGBA{0, 0, 255, 128})
	blueAlpha.Set(1, 1, color.RGBA{0, 0, 255, 0})

	testCases := map[string]testCase{
		"just the right number of colors": {
			maxColors: 4,
			expectedPalette: []sixelColor{
				{0, 0, 100, 100},
				{0, 0, 50, 100},
				{0, 0, 100, 50},
				{0, 0, 100, 0},
			},
		},
		"color reduction": {
			maxColors: 2,
			expectedPalette: []sixelColor{
				{0, 0, 75, 100},
				{0, 0, 100, 25},
			},
		},
	}
	runTests(t, blueAlpha, testCases)
}

func runTests(t *testing.T, img image.Image, testCases map[string]testCase) {
	for testName, test := range testCases {
		t.Run(testName, func(t *testing.T) {
			palette, _, err := newSixelPalette(context.Background(), img, test.maxColors, false)
			if err != nil {
				t.Fatal(err)
			}
			defer palette.release()
			if len(palette.PaletteColors) != len(test.expectedPalette) {
				t.Errorf("Expected colors %+v in palette, but got %+v", test.expectedPalette, palette.PaletteColors)
				return
			}

			for _, c := range test.expectedPalette {
				var foundColor bool
				if slices.Contains(palette.PaletteColors, c) {
					foundColor = true
				}

				if !foundColor {
					t.Errorf("Expected colors %+v in palette, but got %+v", test.expectedPalette, palette.PaletteColors)
					return
				}
			}

			bounds := img.Bounds()
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					c := sixelConvertColor(img.At(x, y).RGBA())
					i, ok := palette.lookup(c)
					if !ok || int(i) >= len(palette.PaletteColors) {
						t.Fatalf("no valid palette index for %+v", c)
					}
					for _, alternative := range palette.PaletteColors {
						if colorDistance(c, alternative) < colorDistance(c, palette.PaletteColors[i]) {
							t.Fatalf("palette index %d is not nearest to %+v", i, c)
						}
					}
				}
			}
		})
	}
}

func colorDistance(a, b sixelColor) int {
	r, g, bl, al := int(a.Red)-int(b.Red), int(a.Green)-int(b.Green), int(a.Blue)-int(b.Blue), int(a.Alpha)-int(b.Alpha)
	return r*r + g*g + bl*bl + al*al
}
