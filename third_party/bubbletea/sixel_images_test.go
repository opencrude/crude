package tea

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/charmbracelet/x/ansi/sixel"
)

func sixelFixture(t testing.TB) (ImagePlacement, *generationSurface) {
	t.Helper()
	pixels := image.NewNRGBA(image.Rect(3, 5, 11, 17))
	for y := 5; y < 17; y++ {
		for x := 3; x < 11; x++ {
			c := color.NRGBA{R: 255, A: 255}
			if x%2 == 0 {
				c = color.NRGBA{G: 255, A: 255}
			}
			pixels.SetNRGBA(x, y, c)
		}
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, pixels); err != nil {
		t.Fatal(err)
	}
	p := ImagePlacement{Image: NewTerminalImage(pngData.Bytes(), pixels), Columns: 4, Rows: 3,
		Protocol: ImageSixel, CellWidth: 2, CellHeight: 4}
	surface := &generationSurface{Buffer: uv.NewBuffer(12, 8), generation: 1}
	setSixelCells(surface.Buffer, p, 2, 2)
	return p, surface
}

func setSixelCells(buf *uv.Buffer, p ImagePlacement, ox, oy int) {
	for y := range p.Rows {
		for x := range p.Columns {
			text := string([]rune{kitty.Placeholder, kitty.Diacritic(y), kitty.Diacritic(x), kitty.Diacritic(int(p.Image.id >> 24))})
			buf.SetCell(ox+x, oy+y, &uv.Cell{Content: text, Width: 1, Style: uv.Style{
				Fg: uv.RGBColor(uint8(p.Image.id>>16), uint8(p.Image.id>>8), uint8(p.Image.id)),
				Bg: uv.RGBColor(20, 40, 60),
			}})
		}
	}
}

func sixelView(p ImagePlacement, surface uv.Surface) View {
	v := NewViewFromSurface(surface)
	v.AltScreen = true
	v.Images = []ImagePlacement{p}
	return v
}

// Use the existing VT and Sixel parsers, including for our output assertions.
func sixelPayloads(t testing.TB, output string) []image.Image {
	t.Helper()
	var images []image.Image
	parser := ansi.NewParser()
	parser.SetDataSize(0)
	parser.SetHandler(ansi.Handler{HandleDcs: func(cmd ansi.Cmd, _ ansi.Params, data []byte) {
		if cmd.Final() != 'q' {
			return
		}
		if bytes.HasSuffix(data, []byte{'-'}) {
			t.Error("unnecessary final graphics newline can scroll a bottom-edge image")
		}
		img, err := new(sixel.Decoder).Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		images = append(images, img)
	}})
	for _, b := range []byte(output) {
		parser.Advance(b)
	}
	return images
}

func TestSixelRendererPreservesSubcellPixelsAndNonzeroSource(t *testing.T) {
	p, source := sixelFixture(t)
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 12, 8)
	kittyFlush(t, r, sixelView(p, source))
	images := sixelPayloads(t, output.String())
	if len(images) != 1 || images[0].Bounds() != image.Rect(0, 0, 8, 12) {
		t.Fatalf("native raster bounds: %v", images)
	}
	for y := range 12 {
		for x := range 8 {
			got := color.NRGBAModel.Convert(images[0].At(x, y))
			want := color.NRGBAModel.Convert(p.Image.pixels.At(x+3, y+5))
			if got != want {
				t.Fatalf("subcell pixel %d,%d: %v want %v", x, y, got, want)
			}
		}
	}
	if strings.ContainsRune(output.String(), kitty.Placeholder) || len(kittyAPCs(t, output.String())) != 0 {
		t.Fatal("Sixel terminal received Kitty glyphs or resource commands")
	}
	if source.CellAt(2, 2).Content == " " {
		t.Fatal("renderer modified the producer's image cells")
	}
}

func TestSixelRendererClipOccludeMoveAndRemove(t *testing.T) {
	p, source := sixelFixture(t)
	source.Clear()
	setSixelCells(source.Buffer, p, -1, 2)
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 12, 8)
	v := sixelView(p, source)
	kittyFlush(t, r, v)
	images := sixelPayloads(t, output.String())
	if len(images) != 1 || images[0].Bounds().Dx() != 6 || r.sixelImages[0].tile.column != 1 {
		t.Fatal("left clipping changed source coordinates or raster dimensions")
	}
	source.Clear()
	setSixelCells(source.Buffer, p, 2, 2)
	cover := image.Rect(3, 3, 5, 4)
	source.FillArea(&uv.Cell{Content: "X", Width: 1}, cover)
	source.generation++
	output.Reset()
	kittyFlush(t, r, v)
	if len(sixelPayloads(t, output.String())) != 4 || !strings.Contains(output.String(), "XX") {
		t.Fatal("ordinary opaque window did not split and cover the image")
	}
	for _, entry := range r.sixelImages {
		if !entry.tile.rect.Intersect(cover).Empty() {
			t.Fatal("Sixel output crossed the compositor's window cover")
		}
	}
	source.Clear()
	setSixelCells(source.Buffer, p, 7, 5)
	source.generation++
	output.Reset()
	kittyFlush(t, r, v)
	if len(sixelPayloads(t, output.String())) != 1 || len(r.sixelImages) != 1 || r.sixelImages[0].tile.rect.Min != image.Pt(7, 5) {
		t.Fatal("moving the image left stale crops or ignored its new position")
	}
	source.Clear()
	source.generation++
	v.Images = nil
	output.Reset()
	kittyFlush(t, r, v)
	if len(r.sixelImages) != 0 || output.Len() == 0 || len(sixelPayloads(t, output.String())) != 0 ||
		strings.Contains(output.String(), ansi.EraseEntireScreen) {
		t.Fatal("removing an image did not repaint only its old cells")
	}
}

type countedSixelPixels struct {
	image.Image
	reads atomic.Int64
}

func (p *countedSixelPixels) At(x, y int) color.Color {
	p.reads.Add(1)
	return p.Image.At(x, y)
}

func TestSixelRendererWarmFramesAndUnrelatedTextReuseRaster(t *testing.T) {
	p, source := sixelFixture(t)
	pixels := &countedSixelPixels{Image: p.Image.pixels}
	p.Image.pixels = pixels
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 12, 8)
	v := sixelView(p, source)
	r.render(v)
	if pixels.reads.Load() != 0 {
		t.Fatal("producer handoff resampled pixels on the UI goroutine")
	}
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	reads := pixels.reads.Load()
	if reads == 0 {
		t.Fatal("terminal writer never sampled the original pixels")
	}
	output.Reset()
	allocs := testing.AllocsPerRun(50, func() {
		r.render(v)
		if err := r.flush(false); err != nil {
			panic(err)
		}
	})
	if allocs != 0 || output.Len() != 0 {
		t.Fatalf("unchanged image: %g allocations, %d output bytes", allocs, output.Len())
	}
	// Different row: no re-encoding or retransmission.
	source.SetCell(0, 0, &uv.Cell{Content: "A", Width: 1})
	source.generation++
	kittyFlush(t, r, v)
	if pixels.reads.Load() != reads || len(sixelPayloads(t, output.String())) != 0 {
		t.Fatal("unrelated text re-encoded or retransmitted the image")
	}
	// Same row: the text writer can erase cells, so restore the cached raster.
	source.SetCell(0, 3, &uv.Cell{Content: "B", Width: 1})
	source.generation++
	output.Reset()
	kittyFlush(t, r, v)
	if pixels.reads.Load() != reads || len(sixelPayloads(t, output.String())) != 1 {
		t.Fatal("same-row text failed to restore the cached image without resampling")
	}
}

func TestSixelRendererResizeResetFailureAndClose(t *testing.T) {
	for _, action := range []string{"resize", "reset", "clear", "failed-write"} {
		t.Run(action, func(t *testing.T) {
			p, source := sixelFixture(t)
			r := newCursedRenderer(io.Discard, nil, 12, 8)
			v := sixelView(p, source)
			kittyFlush(t, r, v)
			switch action {
			case "resize":
				r.resize(4, 4) // before the producer has delivered new geometry
			case "reset":
				r.reset()
			case "clear":
				r.clearScreen()
			case "failed-write":
				source.Clear()
				setSixelCells(source.Buffer, p, 7, 5)
				source.generation++
				r.w = kittyFailWriter{}
				r.render(v)
				if err := r.flush(false); err == nil {
					t.Fatal("failed write was accepted")
				}
				if r.lastView.sixels[0].rect.Min != image.Pt(2, 2) {
					t.Fatal("failed publication committed new image geometry")
				}
			}
			var output bytes.Buffer
			r.w = &output
			kittyFlush(t, r, v)
			if len(sixelPayloads(t, output.String())) != 1 {
				t.Fatal("image was not restored after terminal invalidation")
			}
			for _, entry := range r.sixelImages {
				if !entry.tile.rect.In(r.cellbuf.Bounds()) {
					t.Fatal("stale resize geometry escaped the actual terminal")
				}
			}
			output.Reset()
			if err := r.close(); err != nil {
				t.Fatal(err)
			}
			if len(r.sixelImages) != 0 || len(r.sixelUncertain) != 0 {
				t.Fatal("closed terminal retained image resources")
			}
		})
	}
}

func TestSixelRendererAlphaIsCompositedOverSurface(t *testing.T) {
	p, source := sixelFixture(t)
	pixels := p.Image.pixels.(*image.NRGBA)
	pixels.SetNRGBA(3, 5, color.NRGBA{})
	pixels.SetNRGBA(4, 5, color.NRGBA{R: 255, A: 128})
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 12, 8)
	kittyFlush(t, r, sixelView(p, source))
	images := sixelPayloads(t, output.String())
	for x, want := range []color.NRGBA{{20, 40, 60, 255}, {138, 20, 30, 255}} {
		got := color.NRGBAModel.Convert(images[0].At(x, 0)).(color.NRGBA)
		for _, pair := range [][2]uint8{{got.R, want.R}, {got.G, want.G}, {got.B, want.B}} {
			if d := int(pair[0]) - int(pair[1]); d < -2 || d > 2 {
				t.Fatalf("alpha pixel %d: %v want %v (within Sixel palette precision)", x, got, want)
			}
		}
	}
}

func TestSixelSizeBounds(t *testing.T) {
	for _, values := range [][4]int{{0, 1, 8, 16}, {1, 1, 0, 16}, {1, 1, 8, 2048}, {1 << 30, 1 << 30, 8, 16}, {297, 297, 1024, 1024}} {
		if SixelSizeSupported(values[0], values[1], values[2], values[3]) {
			t.Fatalf("unbounded raster admitted: %v", values)
		}
	}
	if !SixelSizeSupported(160, 40, 9, 20) {
		t.Fatal("ordinary Windows Terminal geometry rejected")
	}
}

func TestSixelCloseFailureKeepsExactDamageUntilSuccessfulCleanup(t *testing.T) {
	p, source := sixelFixture(t)
	r := newCursedRenderer(io.Discard, nil, 12, 8)
	kittyFlush(t, r, sixelView(p, source))
	r.w = kittyFailWriter{}
	if err := r.close(); err == nil || len(r.sixelImages) != 1 {
		t.Fatal("failed close forgot the terminal image that can still be visible")
	}
	var output bytes.Buffer
	r.w = &output
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if len(r.sixelImages) != 0 || output.Len() == 0 {
		t.Fatal("successful close did not finish image cleanup")
	}
}

func BenchmarkSixelEncodeVisibleRaster(b *testing.B) {
	p, _ := sixelFixture(b)
	p.Columns, p.Rows, p.CellWidth, p.CellHeight = 160, 40, 8, 16
	tile := sixelTile{placement: p, rect: image.Rect(0, 0, 160, 40), background: uv.RGBColor(20, 40, 60)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := encodeSixel(tile); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSixelRendererHandoffWhileWriterStalled(t *testing.T) {
	p, source := sixelFixture(t)
	w := &gatedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	r := newCursedRenderer(w, nil, 12, 8)
	v := sixelView(p, source)
	r.render(v)
	w.armed.Store(true)
	flushed := make(chan error, 1)
	go func() { flushed <- r.flush(false) }()
	select {
	case <-w.entered:
	case <-time.After(5 * time.Second):
		close(w.release)
		<-flushed
		t.Fatal("flush did not reach the terminal writer")
	}
	done := make(chan struct{})
	go func() {
		source.Clear()
		source.generation++
		r.render(View{Surface: source, AltScreen: true})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(w.release)
		<-done
		<-flushed
		t.Fatal("image handoff blocked the UI behind terminal output")
	}
	close(w.release)
	if err := <-flushed; err != nil {
		t.Fatal(err)
	}
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if len(r.sixelImages) != 0 {
		t.Fatal("superseded image survived the latest completed frame")
	}
}
