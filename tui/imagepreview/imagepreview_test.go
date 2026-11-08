package imagepreview

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/bmp"
)

func TestSupportedPath(t *testing.T) {
	for _, path := range []string{"a.png", "a.JPG", "a.Jpeg", "dir/a.GIF", "a.WebP", "a.BmP"} {
		if !SupportedPath(path) {
			t.Errorf("SupportedPath(%q) = false", path)
		}
	}
	for _, path := range []string{"", "png", "a.svg", "a.SVG", "a.txt", "a.png.bak", "a.tiff", "dir.png/file"} {
		if SupportedPath(path) {
			t.Errorf("SupportedPath(%q) = true; must remain source or unsupported", path)
		}
	}
}

func testPixels() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	return img
}

func encodedFormats(t *testing.T) map[string][]byte {
	t.Helper()
	formats := make(map[string][]byte)
	for name, encode := range map[string]func(io.Writer) error{
		"png": func(w io.Writer) error { return png.Encode(w, testPixels()) },
		"jpeg": func(w io.Writer) error {
			// Solid color avoids chroma-subsampling ambiguity at the two pixels.
			img := image.NewRGBA(image.Rect(0, 0, 2, 1))
			img.Set(0, 0, color.RGBA{R: 255, A: 255})
			img.Set(1, 0, color.RGBA{R: 255, A: 255})
			return jpeg.Encode(w, img, &jpeg.Options{Quality: 100})
		},
		"gif": func(w io.Writer) error {
			palette := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}}
			first := image.NewPaletted(image.Rect(0, 0, 2, 1), palette)
			first.Pix = []byte{0, 1}
			second := image.NewPaletted(image.Rect(0, 0, 2, 1), palette)
			second.Pix = []byte{1, 0}
			return gif.EncodeAll(w, &gif.GIF{
				Image: []*image.Paletted{first, second},
				Delay: []int{1, 1},
			})
		},
		"bmp": func(w io.Writer) error { return bmp.Encode(w, testPixels()) },
	} {
		var buf bytes.Buffer
		if err := encode(&buf); err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
		formats[name] = buf.Bytes()
	}
	// Independent lossless WebP fixture: ImageMagick 7.1.2-18 encoded a PPM
	// containing a red pixel followed by a green pixel. No WebP encoder is
	// required by this package or by its tests.
	var err error
	formats["webp"], err = base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAQAAAA+w//Mf8x8VMqL/AQA=")
	if err != nil {
		t.Fatal(err)
	}
	return formats
}

func TestDecodeRealFormats(t *testing.T) {
	for format, data := range encodedFormats(t) {
		t.Run(format, func(t *testing.T) {
			img, resource, err := Decode(context.Background(), data)
			if err != nil {
				t.Fatal(err)
			}
			if img == nil || img.Bounds() != image.Rect(0, 0, 2, 1) {
				t.Fatalf("unexpected pixels: %v", img)
			}
			if resource == nil || resource.ID() == 0 {
				t.Fatal("missing prepared Kitty resource")
			}
			r, g, b, a := img.At(0, 0).RGBA()
			if r < 65000 || g > 512 || b > 512 || a != 65535 {
				t.Fatalf("first pixel = %d,%d,%d,%d; want red", r, g, b, a)
			}
			if format != "jpeg" {
				r, g, b, a = img.At(1, 0).RGBA()
				if r != 0 || g != 65535 || b != 0 || a != 65535 {
					t.Fatalf("second pixel = %d,%d,%d,%d; want green (GIF must use first frame)", r, g, b, a)
				}
			}
			prepared, err := preparePNG(context.Background(), img, format, data)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip, err := png.Decode(bytes.NewReader(prepared))
			if err != nil {
				t.Fatalf("Kitty input is not valid PNG: %v", err)
			}
			for x := 0; x < 2; x++ {
				if color.NRGBA64Model.Convert(roundTrip.At(x, 0)) != color.NRGBA64Model.Convert(img.At(x, 0)) {
					t.Fatalf("Kitty PNG pixel %d differs from fallback pixels", x)
				}
			}
		})
	}
}

func TestPreparePNGRetainsOriginalBytes(t *testing.T) {
	data := encodedFormats(t)["png"]
	// Ancillary/trailing bytes must not disappear through an unnecessary encode.
	data = append(data, []byte("original trailing metadata")...)
	img, _, err := Decode(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparePNG(context.Background(), img, "png", data)
	if err != nil || !bytes.Equal(prepared, data) || &prepared[0] != &data[0] {
		t.Fatalf("original PNG was replaced: %v", err)
	}
}

func TestDecodeRejectsInvalidAndTruncatedData(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty": nil, "text": []byte("not an image"), "svg": []byte(`<svg width="2" height="1"></svg>`),
	} {
		t.Run(name, func(t *testing.T) { requireDecodeError(t, data, "") })
	}
	for format, data := range encodedFormats(t) {
		t.Run(format+"-truncated", func(t *testing.T) {
			// Keep headers when possible, but remove the actual pixel payload.
			requireDecodeError(t, data[:len(data)/2], "")
		})
	}
}

func pngHeader(width, height uint32) []byte {
	header := make([]byte, 33)
	copy(header, "\x89PNG\r\n\x1a\n")
	binary.BigEndian.PutUint32(header[8:12], 13)
	copy(header[12:16], "IHDR")
	binary.BigEndian.PutUint32(header[16:20], width)
	binary.BigEndian.PutUint32(header[20:24], height)
	header[24], header[25] = 8, 2 // RGB
	binary.BigEndian.PutUint32(header[29:33], crc32.ChecksumIEEE(header[12:29]))
	return header
}

func TestDecodeRejectsOverlargeHeadersBeforePixels(t *testing.T) {
	for name, data := range map[string][]byte{
		"png-area":    pngHeader(8193, 4096),
		"png-wide":    pngHeader(33554433, 1),
		"png-tall":    pngHeader(1, 33554433),
		"gif-product": append([]byte("GIF89a"), 0xff, 0xff, 0xff, 0xff, 0, 0, 0),
	} {
		t.Run(name, func(t *testing.T) { requireDecodeError(t, data, "dimensions") })
	}
	// Some codecs reject impossible dimensions in DecodeConfig itself, before
	// the shared area guard. Both paths must fail without attempting pixels.
	requireDecodeError(t, pngHeader(0x7fffffff, 0x7fffffff), "header")
	// A real BMP header with huge positive signed dimensions, no pixel payload.
	header := make([]byte, 54)
	copy(header, "BM")
	binary.LittleEndian.PutUint32(header[10:14], 54)
	binary.LittleEndian.PutUint32(header[14:18], 40)
	binary.LittleEndian.PutUint32(header[18:22], 0x7fffffff)
	binary.LittleEndian.PutUint32(header[22:26], 0x7fffffff)
	binary.LittleEndian.PutUint16(header[26:28], 1)
	binary.LittleEndian.PutUint16(header[28:30], 24)
	requireDecodeError(t, header, "header")
	binary.LittleEndian.PutUint32(header[18:22], 8193)
	binary.LittleEndian.PutUint32(header[22:26], 4096)
	requireDecodeError(t, header, "dimensions")
	// Exactly 32*1024*1024 pixels must pass the header guard, then fail because
	// this fixture deliberately has no pixels, not because of its dimensions.
	requireDecodeError(t, pngHeader(8192, 4096), "cannot decode")
}

func TestDecodeInputLimit(t *testing.T) {
	data := make([]byte, (64<<20)+1)
	requireDecodeError(t, data, "64 MiB")
	// At the boundary the header, rather than the size guard, must reject it.
	requireDecodeError(t, data[:64<<20], "header")
}

func requireDecodeError(t *testing.T, data []byte, contains string) {
	t.Helper()
	img, resource, err := Decode(context.Background(), data)
	if err == nil || img != nil || resource != nil {
		t.Fatalf("Decode = (%v, %v, %v), want no pixels/resource and an error", img, resource, err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err, contains)
	}
}

func TestDecodeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer stop()
	for _, tc := range []struct {
		ctx context.Context
		err error
	}{{ctx, context.Canceled}, {expired, context.DeadlineExceeded}} {
		img, resource, err := Decode(tc.ctx, encodedFormats(t)["png"])
		if !errors.Is(err, tc.err) || img != nil || resource != nil {
			t.Fatalf("Decode = (%v, %v, %v), want cancellation %v", img, resource, err, tc.err)
		}
	}
}

func TestCancellationDuringPNGPreparation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	img := cancelImage{Image: testPixels(), cancel: cancel}
	data, err := preparePNG(ctx, img, "bmp", nil)
	if !errors.Is(err, context.Canceled) || data != nil {
		t.Fatalf("preparePNG = (%d bytes, %v), want cancellation with no output", len(data), err)
	}
}

type cancelImage struct {
	image.Image
	cancel context.CancelFunc
}

func (img cancelImage) At(x, y int) color.Color {
	img.cancel()
	return img.Image.At(x, y)
}

func TestBoundedPNGBufferAndCancelledReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := pngBuffer{ctx: ctx, remaining: 4}
	if n, err := w.Write([]byte("1234")); err != nil || n != 4 {
		t.Fatalf("write at limit = %d, %v", n, err)
	}
	if n, err := w.Write([]byte("5")); !errors.Is(err, errPNGTooLarge) || n != 0 || w.String() != "1234" {
		t.Fatalf("over-limit write = %d, %v, data %q", n, err, w.String())
	}
	reader := contextReader{ctx, bytes.NewReader([]byte("pixels"))}
	cancel()
	if n, err := reader.Read(make([]byte, 6)); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read = %d, %v", n, err)
	}
	if _, err := reader.ReadByte(); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled byte read = %v", err)
	}
	if n, err := w.Write([]byte("5")); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write = %d, %v", n, err)
	}
}
