package tea

import (
	"crypto/rand"
	"encoding/binary"
	"image"
)

const maxTerminalImageBytes = 64 << 20

// ImageProtocol selects the negotiated output protocol. ImageKitty is the
// default for placement descriptors; screens without support use ImageNone.
type ImageProtocol uint8

const (
	ImageKitty ImageProtocol = iota
	ImageSixel
	ImageNone
)

// TerminalImage is an immutable PNG resource. Construct it off the Update goroutine
// and retain it across frames. The renderer prepares protocol output only when
// needed and its single writer owns publication.
// Its zero value is not a valid resource.
type TerminalImage struct {
	id     uint32
	png    string
	pixels image.Image
}

// NewTerminalImage assigns an immutable image ID and copies any supplied PNG.
// Kitty needs a bounded PNG; Sixel needs only the decoded pixels. At least one
// must be present. Supplied pixels are retained, not copied, and must be immutable.
func NewTerminalImage(pngData []byte, pixels image.Image) *TerminalImage {
	if len(pngData) == 0 && pixels == nil || len(pngData) > maxTerminalImageBytes {
		return nil
	}
	var b [4]byte
	var id uint32
	for id == 0 {
		if _, err := rand.Read(b[:]); err != nil {
			return nil
		}
		id = binary.BigEndian.Uint32(b[:])
	}
	return &TerminalImage{id: id, png: string(pngData), pixels: pixels}
}

// ID returns the immutable terminal image ID, or zero for a nil resource.
func (i *TerminalImage) ID() uint32 {
	if i == nil {
		return 0
	}
	return i.id
}

// ImagePlacement describes an image grid carried by Unicode-placeholder cells.
// Nonpositive geometry and nil/zero-value resources are ignored. Include each
// resource at most once per view; the first valid descriptor wins.
type ImagePlacement struct {
	Image         *TerminalImage
	Columns, Rows int
	Protocol      ImageProtocol
	// Sixel needs measured cell pixels. Kitty scales at the terminal instead.
	CellWidth, CellHeight int

	// SourceX, SourceY, SourceWidth and SourceHeight select a rectangle in
	// original image pixels, relative to the PNG's top-left corner. All zero
	// selects the entire image, preserving the default placement behavior.
	// Otherwise the origin must be nonnegative and both sizes positive. Values
	// must fit the protocol's unsigned 32-bit fields. The terminal intersects
	// the rectangle with the image bounds. Kitty selects it without reupload;
	// Sixel prepares only the visible crop.
	SourceX, SourceY, SourceWidth, SourceHeight int
}

// Supports reports whether this immutable resource can be painted by protocol.
func (i *TerminalImage) Supports(protocol ImageProtocol) bool {
	return i != nil && i.id != 0 && (protocol == ImageKitty && i.png != "" || protocol == ImageSixel && i.pixels != nil)
}

func (p ImagePlacement) valid() bool {
	if !p.Image.Supports(p.Protocol) || p.Columns <= 0 || p.Rows <= 0 {
		return false
	}
	if p.Protocol == ImageSixel && (p.CellWidth <= 0 || p.CellWidth > 1024 || p.CellHeight <= 0 || p.CellHeight > 1024) {
		return false
	}
	if !p.hasSourceRect() {
		return true
	}
	return p.SourceX >= 0 && p.SourceY >= 0 && p.SourceWidth > 0 && p.SourceHeight > 0 &&
		uint64(p.SourceX) <= 1<<32-1 && uint64(p.SourceY) <= 1<<32-1 &&
		uint64(p.SourceWidth) <= 1<<32-1 && uint64(p.SourceHeight) <= 1<<32-1
}

func (p ImagePlacement) hasSourceRect() bool {
	return p.SourceX != 0 || p.SourceY != 0 || p.SourceWidth != 0 || p.SourceHeight != 0
}

func imagePlacementsEqual(a, b []ImagePlacement) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
