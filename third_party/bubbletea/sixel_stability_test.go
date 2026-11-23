package tea

import (
	"image"
	"image/color"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"
)

// armedSixelPixels blocks the first pixel read after it is armed, so a test
// can observe the frames presented while a replacement raster is pending.
type armedSixelPixels struct {
	image.Image
	armed            atomic.Bool
	entered, release chan struct{}
	once             sync.Once
}

func newArmedSixelPixels(img image.Image) *armedSixelPixels {
	return &armedSixelPixels{Image: img, entered: make(chan struct{}), release: make(chan struct{})}
}

func (p *armedSixelPixels) At(x, y int) color.Color {
	if p.armed.Load() {
		p.once.Do(func() {
			close(p.entered)
			<-p.release
		})
	}
	return p.Image.At(x, y)
}

// requireImageArea fails a frame in which a cell of the image area is blank,
// or in which any other cell still shows image pixels.
func requireImageArea(t *testing.T, area image.Rectangle, except image.Rectangle) func(*sixelScreen) {
	return func(s *sixelScreen) {
		t.Helper()
		for y := range s.height {
			for x := range s.width {
				pt := image.Pt(x, y)
				switch want := pt.In(area) && !pt.In(except); {
				case want && !s.shows(x, y):
					t.Fatalf("frame %d: image cell %d,%d is blank; the image blinked", s.frames, x, y)
				case !want && s.shows(x, y):
					t.Fatalf("frame %d: cell %d,%d still shows stale image pixels", s.frames, x, y)
				}
			}
		}
	}
}

// requireFixturePixels checks the exact source pixels of the unscaled fixture.
func requireFixturePixels(t *testing.T, s *sixelScreen, p ImagePlacement, origin image.Point, area image.Rectangle) {
	t.Helper()
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			for py := range p.CellHeight {
				for px := range p.CellWidth {
					got, ok := s.pixel(x, y, px, py)
					sx := 3 + (x-origin.X)*p.CellWidth + px
					sy := 5 + (y-origin.Y)*p.CellHeight + py
					want := color.NRGBAModel.Convert(p.Image.pixels.At(sx, sy)).(color.NRGBA)
					if !ok || got != want {
						t.Fatalf("cell %d,%d pixel %d,%d: %v want %v", x, y, px, py, got, want)
					}
				}
			}
		}
	}
}

// A tooltip or menu that opens over an image must not erase the rest of it,
// and closing it must restore only the cells it covered. Neither change may
// prepare the image again.
func TestSixelOverlayKeepsUncoveredPixelsAndRestoresOnlyItsCells(t *testing.T) {
	p, source := sixelFixture(t)
	pixels := &countedSixelPixels{Image: p.Image.pixels}
	p.Image.pixels = pixels
	screen := newSixelScreen(t, 12, 8, p.CellWidth, p.CellHeight)
	r := newSixelTestRenderer(t, screen, 12, 8)
	t.Cleanup(func() { screen.check = nil }) // before the renderer's own cleanup
	v := sixelView(p, source)
	area := image.Rect(2, 2, 6, 5)
	r.show(t, v)
	requireImageArea(t, area, image.Rectangle{})(screen)
	requireFixturePixels(t, screen, p, area.Min, area)
	reads := pixels.reads.Load() // includes this test's own reads above

	cover := image.Rect(3, 2, 5, 3)
	screen.check = requireImageArea(t, area, cover)
	source.FillArea(&uv.Cell{Content: "T", Width: 1}, cover)
	source.generation++
	sent := len(screen.payloads)
	r.show(t, v)
	if screen.textAt(3, 2) != "T" || screen.textAt(4, 2) != "T" {
		t.Fatal("the overlay text is not visible over the image")
	}
	if got := screen.payloadSizes(sent); len(got) != 0 {
		t.Fatalf("opening an overlay retransmitted image pixels: %v", got)
	}

	screen.check = requireImageArea(t, area, image.Rectangle{})
	source.Clear()
	setSixelCells(source.Buffer, p, 2, 2)
	source.generation++
	sent = len(screen.payloads)
	r.show(t, v)
	if pixels.reads.Load() != reads {
		t.Fatal("an overlay change prepared the image again")
	}
	requireFixturePixels(t, screen, p, area.Min, area)
	want := []image.Point{{cover.Dx() * p.CellWidth, cover.Dy() * p.CellHeight}}
	if got := screen.payloadSizes(sent); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("closing an overlay sent %v, want only the covered cells %v", got, want)
	}
}

// A zoom step keeps the current pixels on screen until the new raster is
// ready, then replaces them in one frame. The image never goes blank.
func TestSixelZoomKeepsCurrentPixelsUntilReplacementIsReady(t *testing.T) {
	p, source := sixelFixture(t)
	gate := newArmedSixelPixels(p.Image.pixels)
	p.Image.pixels = gate
	release := sync.OnceFunc(func() { close(gate.release) })
	defer release()
	screen := newSixelScreen(t, 12, 8, p.CellWidth, p.CellHeight)
	r := newSixelTestRenderer(t, screen, 12, 8)
	t.Cleanup(func() { screen.check = nil }) // before the renderer's own cleanup
	area := image.Rect(2, 2, 6, 5)
	r.show(t, sixelView(p, source))
	screen.check = requireImageArea(t, area, image.Rectangle{})
	screen.check(screen)

	zoomed := p
	zoomed.SourceWidth, zoomed.SourceHeight = 4, 6
	gate.armed.Store(true)
	sent := len(screen.payloads)
	r.render(sixelView(zoomed, source))
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	awaitSixelStage(t, gate.entered)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	release()
	r.awaitRasters(t)
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if screen.cell(x, y).payload <= sent {
				t.Fatalf("cell %d,%d still shows the previous zoom", x, y)
			}
		}
	}
}

// Switching a pane to another image never shows the old image inside the
// new one: its pixels are cleared at once and the new raster appears when it
// is ready. Switching back to a recent image paints it from the cache in the
// same frame, without preparing it again.
func TestSixelImageSwitchNeverMixesImagesAndRevisitsFromCache(t *testing.T) {
	p, source := sixelFixture(t)
	counted := &countedSixelPixels{Image: p.Image.pixels}
	p.Image.pixels = counted
	screen := newSixelScreen(t, 12, 8, p.CellWidth, p.CellHeight)
	r := newSixelTestRenderer(t, screen, 12, 8)
	t.Cleanup(func() { screen.check = nil }) // before the renderer's own cleanup
	area := image.Rect(2, 2, 6, 5)
	first := sixelView(p, source)
	r.show(t, first)
	screen.check = requireImageArea(t, area, image.Rectangle{})
	screen.check(screen)

	other, otherSource := sixelFixture(t)
	gate := newArmedSixelPixels(other.Image.pixels)
	other.Image.pixels = gate
	release := sync.OnceFunc(func() { close(gate.release) })
	defer release()
	other.Columns, other.Rows = 3, 2
	smaller := image.Rect(2, 2, 5, 4)
	otherSource.Clear()
	setSixelCells(otherSource.Buffer, other, 2, 2)
	otherSource.generation++
	sent := len(screen.payloads)
	screen.check = func(s *sixelScreen) {
		for y := 0; y < s.height; y++ {
			for x := 0; x < s.width; x++ {
				if s.shows(x, y) && s.cell(x, y).payload <= sent {
					t.Fatalf("frame %d: cell %d,%d still shows the previous image", s.frames, x, y)
				}
			}
		}
	}
	gate.armed.Store(true)
	r.render(sixelView(other, otherSource))
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	awaitSixelStage(t, gate.entered)
	release()
	r.awaitRasters(t)
	requireImageArea(t, smaller, image.Rectangle{})(screen)

	// Back to the first image: one frame, no preparation.
	reads := counted.reads.Load()
	source.generation++
	screen.check = nil
	r.render(first)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if r.sixelPending || counted.reads.Load() != reads {
		t.Fatal("a recent image was prepared again instead of painted from the cache")
	}
	requireImageArea(t, area, image.Rectangle{})(screen)
	requireFixturePixels(t, screen, p, area.Min, area)

	// Cell geometry above the raster bound has no native raster; nothing may
	// remain held over its cells.
	huge := other
	huge.CellWidth, huge.CellHeight = 1024, 1024
	source.Clear()
	setSixelCells(source.Buffer, huge, 2, 2)
	source.generation++
	screen.check = requireImageArea(t, image.Rectangle{}, image.Rectangle{})
	r.render(sixelView(huge, source))
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
}

// A motion preview drawn as cells stays on screen until the native raster
// replaces it. The release of a drag never shows an empty pane in between.
func TestSixelCellPreviewStaysUntilNativeRasterIsReady(t *testing.T) {
	p, source := sixelFixture(t)
	gate := newArmedSixelPixels(p.Image.pixels)
	p.Image.pixels = gate
	release := sync.OnceFunc(func() { close(gate.release) })
	defer release()
	area := image.Rect(2, 2, 6, 5)
	screen := newSixelScreen(t, 12, 8, p.CellWidth, p.CellHeight)
	r := newSixelTestRenderer(t, screen, 12, 8)
	t.Cleanup(func() { screen.check = nil })
	// A drag starts from the native image at its old size; the preview then
	// paints the new size and release asks for that size natively.
	before := p
	before.Columns, before.Rows = 3, 2
	beforeSource := source.Clone()
	beforeSource.Clear()
	setSixelCells(beforeSource, before, 2, 2)
	r.show(t, sixelView(before, &generationSurface{Buffer: beforeSource, generation: 1}))
	gate.armed.Store(true)
	preview := source.Clone()
	preview.FillArea(&uv.Cell{Content: "▀", Width: 1}, area)
	v := NewViewFromSurface(&generationSurface{Buffer: preview, generation: 2})
	v.AltScreen = true
	r.render(v)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}

	screen.check = func(s *sixelScreen) {
		for y := area.Min.Y; y < area.Max.Y; y++ {
			for x := area.Min.X; x < area.Max.X; x++ {
				if !s.shows(x, y) && s.textAt(x, y) != "▀" {
					t.Fatalf("frame %d: cell %d,%d went blank between the preview and the image", s.frames, x, y)
				}
			}
		}
	}
	r.render(sixelView(p, source))
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	awaitSixelStage(t, gate.entered)
	if screen.textAt(2, 2) != "▀" {
		t.Fatal("the preview was not held while the native raster was pending")
	}
	release()
	r.awaitRasters(t)
	requireImageArea(t, area, image.Rectangle{})(screen)
}

// setSixelWindow writes the markers of a window into a placement's grid, as a
// zoomed image pane does.
func setSixelWindow(buf *uv.Buffer, p ImagePlacement, window image.Rectangle, ox, oy int) {
	for y := window.Min.Y; y < window.Max.Y; y++ {
		for x := window.Min.X; x < window.Max.X; x++ {
			text := string([]rune{kitty.Placeholder, kitty.Diacritic(y), kitty.Diacritic(x), kitty.Diacritic(int(p.Image.id >> 24))})
			buf.SetCell(ox+x-window.Min.X, oy+y-window.Min.Y, &uv.Cell{Content: text, Width: 1, Style: uv.Style{
				Fg: uv.RGBColor(uint8(p.Image.id>>16), uint8(p.Image.id>>8), uint8(p.Image.id)),
				Bg: uv.RGBColor(20, 40, 60),
			}})
		}
	}
}

// Panning a zoomed image crops a raster prepared once with a margin around the
// visible area: no pan step resamples or quantizes the image again, and the
// image area is never blank.
func TestSixelPanCropsThePreparedMarginWithoutPreparingAgain(t *testing.T) {
	p, source := sixelFixture(t)
	counted := &countedSixelPixels{Image: p.Image.pixels}
	p.Image.pixels = counted
	p.Columns, p.Rows = 8, 6 // zoomed: twice the fitted grid
	screen := newSixelScreen(t, 12, 8, p.CellWidth, p.CellHeight)
	r := newSixelTestRenderer(t, screen, 12, 8)
	t.Cleanup(func() { screen.check = nil })
	area := image.Rect(2, 2, 6, 5)
	show := func(window image.Rectangle) {
		source.Clear()
		setSixelWindow(source.Buffer, p, window, area.Min.X, area.Min.Y)
		source.generation++
		r.render(sixelView(p, source))
		if err := r.flush(false); err != nil {
			t.Fatal(err)
		}
	}
	show(image.Rect(0, 0, 4, 3))
	r.awaitRasters(t)
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := r.sixelWork
		w.mu.Lock()
		margin := len(w.bases) == 2 && w.cancelActive == nil
		w.mu.Unlock()
		if margin || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	reads := counted.reads.Load()
	screen.check = requireImageArea(t, area, image.Rectangle{})
	for _, window := range []image.Rectangle{image.Rect(1, 0, 5, 3), image.Rect(2, 1, 6, 4), image.Rect(4, 3, 8, 6)} {
		sent := len(screen.payloads)
		show(window)
		if r.sixelPending {
			t.Fatalf("pan to %v waited for a new raster", window)
		}
		if got := screen.payloadSizes(sent); len(got) != 1 || got[0] != image.Pt(area.Dx()*p.CellWidth, area.Dy()*p.CellHeight) {
			t.Fatalf("pan to %v sent %v, want one crop of the visible area", window, got)
		}
	}
	if counted.reads.Load() != reads {
		t.Fatal("panning prepared the image again")
	}
}

// A window placement into a larger virtual grid paints exactly the pixels the
// whole grid shows in those cells. This is how a zoomed image larger than the
// 297-cell marker range stays native.
func TestSixelVirtualWindowMatchesTheWholeGrid(t *testing.T) {
	p, _ := sixelFixture(t)
	whole := p
	whole.Columns, whole.Rows = 8, 6
	window := p
	window.Columns, window.Rows = 4, 3
	window.VirtualColumns, window.VirtualRows, window.OffsetColumn, window.OffsetRow = 8, 6, 2, 1
	area := image.Rect(2, 2, 6, 5)
	render := func(q ImagePlacement, cells image.Rectangle) *sixelScreen {
		source := &generationSurface{Buffer: uv.NewBuffer(12, 8), generation: 1}
		setSixelWindow(source.Buffer, q, cells, area.Min.X, area.Min.Y)
		screen := newSixelScreen(t, 12, 8, q.CellWidth, q.CellHeight)
		r := newSixelTestRenderer(t, screen, 12, 8)
		r.show(t, sixelView(q, source))
		requireImageArea(t, area, image.Rectangle{})(screen)
		return screen
	}
	want := render(whole, image.Rect(2, 1, 6, 4))
	got := render(window, image.Rect(0, 0, 4, 3))
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			for py := range p.CellHeight {
				for px := range p.CellWidth {
					a, _ := got.pixel(x, y, px, py)
					b, _ := want.pixel(x, y, px, py)
					if a != b {
						t.Fatalf("cell %d,%d pixel %d,%d: window %v, whole grid %v", x, y, px, py, a, b)
					}
				}
			}
		}
	}
	bad := window
	bad.OffsetColumn = 5 // the window would end outside the virtual grid
	if bad.valid() {
		t.Fatal("a window outside its virtual grid was accepted")
	}
	kitty := window
	kitty.Protocol = ImageKitty
	if kitty.valid() {
		t.Fatal("Kitty accepted a Sixel-only virtual grid")
	}
}
