package sixel

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestEncoderDitherMatchesStandardDiffusion(t *testing.T) {
	bounds := image.Rect(3, 7, 132, 50) // partial last band; nonzero source origin
	src := image.NewRGBA64(bounds)
	var sample uint32 = 1
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			sample = sample*1664525 + 1013904223
			src.SetRGBA64(x, y, color.RGBA64{R: uint16(sample >> 16), G: uint16(sample >> 8), B: uint16(sample), A: 65535})
		}
	}
	palette, dithered, err := newSixelPalette(t.Context(), src, MaxColors, true)
	if err != nil || !dithered {
		t.Fatalf("fixture did not use adaptive diffusion: %v", err)
	}
	colors := make(color.Palette, len(palette.PaletteColors))
	for i, c := range palette.PaletteColors {
		colors[i] = sixelRGB(int(c.Red), int(c.Green), int(c.Blue))
	}
	want := image.NewPaletted(bounds, colors)
	draw.FloydSteinberg.Draw(want, bounds, src, bounds.Min)

	var output bytes.Buffer
	if err := (&Encoder{Dither: true}).Encode(&output, src); err != nil {
		t.Fatal(err)
	}
	got, err := new(Decoder).Decode(&output)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds() != (image.Rectangle{Max: bounds.Size()}) {
		t.Fatalf("raster dimensions: got %s, want %v", got.Bounds(), bounds.Size())
	}
	for y := range bounds.Dy() {
		for x := range bounds.Dx() {
			actual := color.NRGBAModel.Convert(got.At(x, y))
			if expected := want.At(bounds.Min.X+x, bounds.Min.Y+y); actual != expected {
				t.Fatalf("Sixel pixel %d,%d: got %v, want %v", x, y, actual, expected)
			}
		}
	}
}

type cancellingSixelSource struct {
	image.Image
	cancel context.CancelFunc
	reads  int
}

func (s *cancellingSixelSource) At(x, y int) color.Color {
	s.reads++
	s.cancel()
	return s.Image.At(x, y)
}

func TestEncoderCancelsDuringPaletteConstruction(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pixels := &cancellingSixelSource{Image: image.NewRGBA(image.Rect(0, 0, 100, 100)), cancel: cancel}
	var output bytes.Buffer
	err := (&Encoder{Dither: true}).EncodeContext(ctx, &output, pixels)
	if !errors.Is(err, context.Canceled) || output.Len() != 0 || pixels.reads > pixels.Bounds().Dx() {
		t.Fatalf("palette kept encoding after cancellation: err=%v, bytes=%d, reads=%d", err, output.Len(), pixels.reads)
	}
}

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
