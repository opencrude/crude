// Package imagepreview prepares bounded raster images for native and cell-based
// previews. Call Decode on an owned worker, never on the UI Update goroutine.
package imagepreview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

const (
	// MaxInputBytes is the maximum compressed input size, in bytes.
	MaxInputBytes = 64 << 20
	// MaxPixels is the maximum decoded area, independent of color depth.
	MaxPixels = 32 * 1024 * 1024
)

var errPNGTooLarge = errors.New("prepared PNG exceeds the native preview limit")

// SupportedPath reports whether path has a supported raster image extension.
// It does not read the path. SVG remains editable source, not a raster preview.
func SupportedPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return true
	default:
		return false
	}
}

// Decoded contains one immutable source for both native and cell previews.
// Bytes charges its pixel backing and any copied native PNG, not transient
// decoder scratch or the original compressed input.
type Decoded struct {
	Pixels   image.Image
	Resource *tea.TerminalImage
	Bytes    int64
}

// CheckHeader checks the input and dimensions without allocating the decoded
// raster. The pixel payload is checked by Prepare when it is needed.
func CheckHeader(ctx context.Context, data []byte) error {
	_, err := imageFormat(ctx, data)
	return err
}

// Decode validates dimensions before decoding PNG, JPEG, GIF, WebP or BMP bytes.
// Animated GIFs intentionally use only their first frame. A successful decode
// returns pixels even when the optional terminal resource cannot be prepared.
//
// A confirmed Sixel terminal uses decoded pixels directly. Kitty (or a pending
// probe) retains original PNG bytes or normalizes other formats once here.
// The caller must not change data until Decode returns.
//
// Cancellation is checked between codec steps and on memory reads/writes.
// Codec CPU work between those checks cannot be forcibly interrupted. A
// cancelled call returns no pixels or resource; callers must still fence stale
// results with their own owner and generation.
func Decode(ctx context.Context, data []byte, protocol tea.ImageProtocol) (image.Image, *tea.TerminalImage, error) {
	decoded, err := Prepare(ctx, data, protocol)
	return decoded.Pixels, decoded.Resource, err
}

// Prepare decodes the same raster as Decode and accounts for its retained bytes.
func Prepare(ctx context.Context, data []byte, protocol tea.ImageProtocol) (Decoded, error) {
	format, err := imageFormat(ctx, data)
	if err != nil {
		return Decoded{}, err
	}
	img, _, err := image.Decode(contextReader{ctx, bytes.NewReader(data)})
	if ctx.Err() != nil {
		return Decoded{}, ctx.Err()
	}
	if err != nil {
		return Decoded{}, fmt.Errorf("cannot decode image: %w", err)
	}
	decoded := Decoded{Pixels: img, Bytes: pixelBytes(img)}
	var pngData []byte
	if protocol != tea.ImageSixel {
		pngData, err = preparePNG(ctx, img, format, data)
		if ctx.Err() != nil {
			return Decoded{}, ctx.Err()
		}
		if err != nil {
			// If PNG preparation fails, keep the cell preview.
			return decoded, nil
		}
	}
	resource := tea.NewTerminalImage(pngData, img)
	if err := ctx.Err(); err != nil {
		return Decoded{}, err
	}
	decoded.Resource = resource
	if resource.Supports(tea.ImageKitty) {
		decoded.Bytes += int64(len(pngData))
	}
	return decoded, nil
}

func imageFormat(ctx context.Context, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) > MaxInputBytes {
		return "", fmt.Errorf("image exceeds the 64 MiB preview limit; open a smaller image")
	}
	config, format, err := image.DecodeConfig(contextReader{ctx, bytes.NewReader(data)})
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("cannot read image header: %w", err)
	}
	switch format {
	case "png", "jpeg", "gif", "webp", "bmp":
	default:
		return "", fmt.Errorf("unsupported image format %q", format)
	}
	// Divide before comparing to avoid overflow, including on 32-bit systems.
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxPixels/config.Height {
		return "", fmt.Errorf("image dimensions exceed the preview limit; open a smaller image")
	}
	return format, nil
}

func pixelBytes(img image.Image) int64 {
	// Charge capacity, not visible bounds: JPEG can retain padded rows and
	// 16-bit PNGs can use twice the backing of 8-bit colour.
	switch img := img.(type) {
	case *image.RGBA:
		return int64(cap(img.Pix))
	case *image.RGBA64:
		return int64(cap(img.Pix))
	case *image.NRGBA:
		return int64(cap(img.Pix))
	case *image.NRGBA64:
		return int64(cap(img.Pix))
	case *image.Gray:
		return int64(cap(img.Pix))
	case *image.Gray16:
		return int64(cap(img.Pix))
	case *image.Paletted:
		// At most 256 palette entries. Include interface and colour backing.
		return int64(cap(img.Pix)) + int64(cap(img.Palette))*24
	case *image.YCbCr:
		return int64(cap(img.Y) + cap(img.Cb) + cap(img.Cr))
	case *image.NYCbCrA:
		return pixelBytes(&img.YCbCr) + int64(cap(img.A))
	case *image.CMYK:
		return int64(cap(img.Pix))
	default:
		// Do not claim that an unknown backing fits the warm cache.
		return 1 << 60
	}
}

func preparePNG(ctx context.Context, img image.Image, format string, data []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if format == "png" {
		return data, nil
	}
	writer := pngBuffer{ctx: ctx, remaining: MaxInputBytes}
	if err := png.Encode(&writer, img); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

type contextReader struct {
	ctx context.Context
	*bytes.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func (r contextReader) ReadByte() (byte, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.ReadByte()
}

// Bound the encoded resource as well as the source; a compact input may expand
// past Kitty's 64 MiB limit after conversion to PNG.
type pngBuffer struct {
	bytes.Buffer
	ctx       context.Context
	remaining int
}

func (w *pngBuffer) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > w.remaining {
		return 0, errPNGTooLarge
	}
	n, err := w.Buffer.Write(p)
	w.remaining -= n
	return n, err
}
