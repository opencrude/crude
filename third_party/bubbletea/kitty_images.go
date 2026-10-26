package tea

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"strconv"

	"github.com/charmbracelet/x/ansi"
)

const maxKittyImageBytes = 64 << 20

// KittyImage is an immutable PNG resource. Construct it off the Update goroutine
// and retain it across frames; the terminal writer uploads it only when needed.
// Its zero value is not a valid resource.
type KittyImage struct {
	id  uint32
	png string
}

// NewKittyImage copies PNG data and assigns a random nonzero terminal image ID.
// It returns nil for empty data, data larger than 64 MiB, or unavailable entropy.
// The caller is responsible for supplying a valid, bounded-dimension PNG.
func NewKittyImage(pngData []byte) *KittyImage {
	if len(pngData) == 0 || len(pngData) > maxKittyImageBytes {
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
	return &KittyImage{id: id, png: string(pngData)}
}

// ID returns the immutable terminal image ID, or zero for a nil resource.
func (i *KittyImage) ID() uint32 {
	if i == nil {
		return 0
	}
	return i.id
}

// KittyImagePlacement describes a Unicode-placeholder virtual placement.
// Nonpositive geometry and nil/zero-value resources are ignored. Include each
// resource at most once per view; the first valid descriptor wins.
type KittyImagePlacement struct {
	Image         *KittyImage
	Columns, Rows int
}

func (p KittyImagePlacement) valid() bool {
	return p.Image != nil && p.Image.id != 0 && len(p.Image.png) > 0 && p.Columns > 0 && p.Rows > 0
}

func kittyImagesEqual(a, b []KittyImagePlacement) bool {
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

// All methods below run only at the single terminal writer, with mu held.
// Reconciliation does not mutate kittyImages until the entire write succeeds.
func (s *cursedRenderer) kittySequence(seq string) {
	if s.kittyTmux {
		seq = ansi.TmuxPassthrough(seq)
	}
	_, _ = s.scr.WriteString(seq)
}

func (s *cursedRenderer) deleteKittyImage(id uint32) {
	s.kittySequence("\x1b_Ga=d,d=I,i=" + strconv.FormatUint(uint64(id), 10) + ",q=2\x1b\\")
}

func (s *cursedRenderer) deleteKittyImages() {
	for id := range s.kittyImages {
		s.deleteKittyImage(id)
	}
	for _, p := range s.kittyUncertain {
		s.deleteKittyImage(p.Image.id)
	}
}

func (s *cursedRenderer) uploadKittyImage(image *KittyImage) {
	// Encode a single 3072-byte input chunk into at most 4096 base64 bytes.
	// Never allocate a maximum-image-sized staging buffer, or encode on Update.
	var encoded [4096]byte
	for offset := 0; offset < len(image.png); {
		end := min(offset+3072, len(image.png))
		n := base64.StdEncoding.EncodedLen(end - offset)
		base64.StdEncoding.Encode(encoded[:n], []byte(image.png[offset:end]))
		opts := "m=0"
		if end < len(image.png) {
			opts = "m=1"
		}
		if offset == 0 {
			opts = "a=t,f=100,q=2,i=" + strconv.FormatUint(uint64(image.id), 10) + "," + opts
		} else {
			opts += ",q=2"
		}
		s.kittySequence(ansi.KittyGraphics(encoded[:n], opts))
		offset = end
	}
}

// firstKittyPlacement avoids a transient map on warm frames. Image sets are
// small; no-image and unchanged frames allocate no reconciliation state.
func firstKittyPlacement(placements []KittyImagePlacement, index int) bool {
	p := placements[index]
	if !p.valid() {
		return false
	}
	for _, earlier := range placements[:index] {
		if earlier.valid() && earlier.Image.id == p.Image.id {
			return false
		}
	}
	return true
}

func (s *cursedRenderer) reconcileKittyImages(placements []KittyImagePlacement, invalid, deleted bool) {
	if invalid {
		if !deleted {
			s.deleteKittyImages()
		}
	} else {
		for id := range s.kittyImages {
			found := false
			for _, p := range placements {
				if p.valid() && p.Image.id == id {
					found = true
					break
				}
			}
			if !found {
				s.deleteKittyImage(id)
			}
		}
	}
	for i, p := range placements {
		if !firstKittyPlacement(placements, i) {
			continue
		}
		old, uploaded := s.kittyImages[p.Image.id]
		if invalid || !uploaded {
			s.uploadKittyImage(p.Image)
		}
		if invalid || !uploaded || old != p {
			// A nonzero stable placement ID replaces geometry instead of adding
			// ambiguous virtual prototypes. Placeholder underline zero selects
			// this sole placement; no additional public descriptor is needed.
			s.kittySequence("\x1b_Ga=p,U=1,i=" + strconv.FormatUint(uint64(p.Image.id), 10) +
				",c=" + strconv.Itoa(p.Columns) + ",r=" + strconv.Itoa(p.Rows) + ",q=2,p=1\x1b\\")
		}
	}
}

func (s *cursedRenderer) commitKittyImages(placements []KittyImagePlacement) {
	if !s.kittyInvalid && len(s.kittyUncertain) == 0 && s.lastView != nil &&
		(len(s.kittyImages) > 0 || len(placements) == 0) && kittyImagesEqual(placements, s.lastView.Images) {
		return
	}
	if len(placements) == 0 && len(s.kittyImages) == 0 {
		s.kittyUncertain = nil
		s.kittyInvalid = false
		return
	}
	if s.kittyImages == nil {
		s.kittyImages = make(map[uint32]KittyImagePlacement, len(placements))
	} else {
		clear(s.kittyImages)
	}
	for i, p := range placements {
		if firstKittyPlacement(placements, i) {
			s.kittyImages[p.Image.id] = p
		}
	}
	s.kittyUncertain = nil
	s.kittyInvalid = false
}

func (s *cursedRenderer) failKittyImages(placements []KittyImagePlacement) {
	// A short/failed write may have transmitted some new resources. Retain their
	// exact IDs for deletion on retry/close, without claiming a successful flush.
	for i, p := range placements {
		if firstKittyPlacement(placements, i) {
			if _, owned := s.kittyImages[p.Image.id]; !owned {
				found := false
				for _, uncertain := range s.kittyUncertain {
					if uncertain.Image.id == p.Image.id {
						found = true
						break
					}
				}
				if !found {
					s.kittyUncertain = append(s.kittyUncertain, p)
				}
			}
		}
	}
	s.kittyInvalid = true
}
