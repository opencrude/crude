package sixel

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestDitherReducesFlatGradientBands(t *testing.T) {
	img := image.NewRGBA(image.Rect(3, 7, 259, 135))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x - 3), G: uint8((y - 7) * 2), B: 80, A: 255})
		}
	}
	var transitions [2]int
	for i, dither := range []bool{false, true} {
		var output bytes.Buffer
		if err := (&Encoder{Dither: dither}).Encode(&output, img); err != nil {
			t.Fatal(err)
		}
		decoded, err := new(Decoder).Decode(&output)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Bounds().Size() != img.Bounds().Size() {
			t.Fatal("dithering changed the image dimensions")
		}
		for y := 0; y < decoded.Bounds().Dy(); y++ {
			for x := 1; x < decoded.Bounds().Dx(); x++ {
				if decoded.At(x, y) != decoded.At(x-1, y) {
					transitions[i]++
				}
			}
		}
	}
	if transitions[1] < 2*transitions[0] {
		t.Fatalf("dithering retained large flat bands: plain=%d dithered=%d", transitions[0], transitions[1])
	}
}

func TestDitherKeepsExactSmallPaletteAndAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(3, 7, 11, 19))
	for y := 7; y < 19; y++ {
		for x := 3; x < 11; x++ {
			if x%2 == 0 {
				img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
			}
		}
	}
	for _, dither := range []bool{false, true} {
		var output bytes.Buffer
		if err := (&Encoder{Dither: dither}).Encode(&output, img); err != nil {
			t.Fatal(err)
		}
		decoded, err := new(Decoder).Decode(&output)
		if err != nil {
			t.Fatal(err)
		}
		for y := 0; y < 12; y++ {
			for x := 0; x < 8; x++ {
				if color.NRGBAModel.Convert(decoded.At(x, y)) != color.NRGBAModel.Convert(img.At(x+3, y+7)) {
					t.Fatalf("dither=%v: source origin, exact colors or alpha lost at %d,%d", dither, x, y)
				}
			}
		}
	}
}

func TestDecodeRepeatPreservesFollowingPixelCharacters(t *testing.T) {
	img, err := new(Decoder).Decode(bytes.NewBufferString(`"1;1;8;6#1;2;100;0;0#1!4~?~?~`))
	if err != nil {
		t.Fatal(err)
	}
	for y := range 6 {
		for x := range 8 {
			want := color.NRGBA{R: 255, A: 255}
			if x == 4 || x == 6 {
				want = color.NRGBA{}
			}
			if got := color.NRGBAModel.Convert(img.At(x, y)); got != want {
				t.Fatalf("repeat swallowed the following pixel at %d,%d: %v want %v", x, y, got, want)
			}
		}
	}
}
