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

// Decode validates dimensions before decoding PNG, JPEG, GIF, WebP or BMP bytes.
// Animated GIFs intentionally use only their first frame. A successful decode
// returns pixels even when the optional Kitty resource cannot be prepared.
//
// Original PNG bytes are retained verbatim in the immutable Kitty resource.
// Other formats are encoded to PNG once here, not during painting or resize.
// The caller must not change data until Decode returns.
//
// Cancellation is checked between codec steps and on memory reads/writes.
// Codec CPU work between those checks cannot be forcibly interrupted. A
// cancelled call returns no pixels or resource; callers must still fence stale
// results with their own owner and generation.
func Decode(ctx context.Context, data []byte) (image.Image, *tea.KittyImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(data) > MaxInputBytes {
		return nil, nil, fmt.Errorf("image exceeds the 64 MiB preview limit; open a smaller image")
	}
	config, format, err := image.DecodeConfig(contextReader{ctx, bytes.NewReader(data)})
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read image header: %w", err)
	}
	switch format {
	case "png", "jpeg", "gif", "webp", "bmp":
	default:
		return nil, nil, fmt.Errorf("unsupported image format %q", format)
	}
	// Divide before comparing to avoid overflow, including on 32-bit systems.
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxPixels/config.Height {
		return nil, nil, fmt.Errorf("image dimensions exceed the preview limit; open a smaller image")
	}
	img, _, err := image.Decode(contextReader{ctx, bytes.NewReader(data)})
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("cannot decode image: %w", err)
	}
	pngData, err := preparePNG(ctx, img, format, data)
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		// PNG conversion is only needed for Kitty; cell previews still work.
		return img, nil, nil
	}
	resource := tea.NewKittyImage(pngData)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	// NewKittyImage can return nil (for example, if entropy is unavailable).
	return img, resource, nil
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
