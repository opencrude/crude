package sixel

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

func quantizedFixture(w, h int, alpha bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(5, 9, 5+w, 9+h))
	var sample uint32 = 7
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			sample = sample*1664525 + 1013904223
			c := color.RGBA{R: uint8(x * 3), G: uint8(y * 5), B: uint8(sample >> 24), A: 255}
			if alpha && (x+y)%7 == 0 {
				c = color.RGBA{}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func decodeSixelData(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := new(Decoder).Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// Every crop of one quantized image must show exactly the pixels the whole
// image shows at that place, for dithered and transparent images, and for
// crops large enough to be encoded on several goroutines.
func TestQuantizedCropsMatchTheWholeImage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		w, h  int
		alpha bool
	}{
		{"dithered", 131, 47, false},
		{"transparent", 97, 31, true},
		{"parallel", 700, 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := quantizedFixture(tc.w, tc.h, tc.alpha)
			e := &Encoder{Dither: true}
			q, err := e.Quantize(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			var stream bytes.Buffer
			if err := e.Encode(&stream, src); err != nil {
				t.Fatal(err)
			}
			whole := decodeSixelData(t, stream.Bytes())
			full, err := q.AppendEncode(context.Background(), nil, q.Bounds())
			if err != nil {
				t.Fatal(err)
			}
			b := src.Bounds()
			crops := []image.Rectangle{
				b,
				image.Rect(b.Min.X+3, b.Min.Y+1, b.Min.X+40, b.Min.Y+20), // unaligned to bands
				image.Rect(b.Max.X-9, b.Max.Y-13, b.Max.X, b.Max.Y),
				image.Rect(b.Min.X, b.Min.Y+7, b.Max.X, b.Max.Y-2),
			}
			for i, crop := range crops {
				data := full
				if i > 0 {
					if data, err = q.AppendEncode(context.Background(), []byte("prefix"), crop); err != nil {
						t.Fatal(err)
					}
					if !bytes.HasPrefix(data, []byte("prefix")) {
						t.Fatal("AppendEncode did not append")
					}
					data = data[len("prefix"):]
				}
				got := decodeSixelData(t, data)
				if got.Bounds().Size() != crop.Size() {
					t.Fatalf("crop %v decoded as %v", crop, got.Bounds())
				}
				for y := range crop.Dy() {
					for x := range crop.Dx() {
						want := whole.At(crop.Min.X-b.Min.X+x, crop.Min.Y-b.Min.Y+y)
						if have := got.At(x, y); color.NRGBAModel.Convert(have) != color.NRGBAModel.Convert(want) {
							t.Fatalf("crop %v pixel %d,%d: %v, whole image shows %v", crop, x, y, have, want)
						}
					}
				}
			}
		})
	}
}

func TestQuantizedEncodeHonorsCancellation(t *testing.T) {
	q, err := (&Encoder{Dither: true}).Quantize(context.Background(), quantizedFixture(64, 64, false))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dst := []byte("kept")
	got, err := q.AppendEncode(ctx, dst, q.Bounds())
	if !errors.Is(err, context.Canceled) || string(got) != "kept" {
		t.Fatalf("canceled encode: %q, %v", got, err)
	}
	if _, err := (&Encoder{Dither: true}).Quantize(ctx, quantizedFixture(8, 8, false)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled quantize: %v", err)
	}
}

func BenchmarkQuantizedEncode(b *testing.B) {
	src := quantizedFixture(810, 560, false)
	e := &Encoder{Dither: true}
	b.Run("Quantize", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := e.Quantize(context.Background(), src); err != nil {
				b.Fatal(err)
			}
		}
	})
	q, err := e.Quantize(context.Background(), src)
	if err != nil {
		b.Fatal(err)
	}
	for _, bench := range []struct {
		name string
		r    image.Rectangle
	}{
		{"Whole", q.Bounds()},
		{"Tooltip", image.Rect(100, 40, 280, 100)},
	} {
		b.Run(bench.name, func(b *testing.B) {
			b.ReportAllocs()
			var buf []byte
			for range b.N {
				if buf, err = q.AppendEncode(context.Background(), buf[:0], bench.r); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(buf)), "wire-B/op")
		})
	}
}
