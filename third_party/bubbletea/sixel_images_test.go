package tea

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

type sixelTestRenderer struct {
	*cursedRenderer
	ready <-chan struct{}
}

func newSixelTestRenderer(tb testing.TB, w io.Writer, width, height int) *sixelTestRenderer {
	tb.Helper()
	ready := make(chan struct{}, 1)
	r := newCursedRenderer(w, nil, width, height)
	r.sixelWake = func() {
		select {
		case ready <- struct{}{}:
		default:
		}
	}
	tb.Cleanup(func() { _ = r.close() })
	return &sixelTestRenderer{r, ready}
}

// Await actual Sixel output, not a sleep or a readiness proxy. Direct-renderer
// tests drive flush themselves; Program integration tests use the paced writer.
func (r *sixelTestRenderer) show(tb testing.TB, v View) {
	tb.Helper()
	r.render(v)
	r.awaitRasters(tb)
}

// shown returns what the renderer recorded for a terminal cell.
func (r *sixelTestRenderer) shown(x, y int) sixelCell {
	if !r.sixelAny {
		return sixelCell{}
	}
	return r.sixelShown[y*r.cellbuf.Width()+x]
}

func (r *sixelTestRenderer) awaitRasters(tb testing.TB) {
	tb.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if err := r.flush(false); err != nil {
			tb.Fatal(err)
		}
		if !r.sixelPending {
			return
		}
		select {
		case <-r.ready:
		case <-deadline.C:
			tb.Fatal("image did not reach the terminal writer")
		}
	}
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
	r := newSixelTestRenderer(t, &output, 12, 8)
	r.show(t, sixelView(p, source))
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
	r := newSixelTestRenderer(t, &output, 12, 8)
	v := sixelView(p, source)
	r.show(t, v)
	images := sixelPayloads(t, output.String())
	if len(images) != 1 || images[0].Bounds().Dx() != 6 || r.shown(0, 2).column != 1 {
		t.Fatal("left clipping changed source coordinates or raster dimensions")
	}
	source.Clear()
	setSixelCells(source.Buffer, p, 2, 2)
	cover := image.Rect(3, 3, 5, 4)
	source.FillArea(&uv.Cell{Content: "X", Width: 1}, cover)
	source.generation++
	output.Reset()
	r.show(t, v)
	if len(sixelPayloads(t, output.String())) != 4 || !strings.Contains(output.String(), "XX") {
		t.Fatal("ordinary opaque window did not split and cover the image")
	}
	for y := cover.Min.Y; y < cover.Max.Y; y++ {
		for x := cover.Min.X; x < cover.Max.X; x++ {
			if r.shown(x, y).base != 0 {
				t.Fatal("Sixel output crossed the compositor's window cover")
			}
		}
	}
	source.Clear()
	setSixelCells(source.Buffer, p, 7, 5)
	source.generation++
	output.Reset()
	r.show(t, v)
	if len(sixelPayloads(t, output.String())) != 1 || r.sixelShownArea() != image.Rect(7, 5, 11, 8) || r.shown(7, 5).column != 0 {
		t.Fatal("moving the image left stale crops or ignored its new position")
	}
	source.Clear()
	source.generation++
	v.Images = nil
	output.Reset()
	r.show(t, v)
	if r.sixelAny || output.Len() == 0 || len(sixelPayloads(t, output.String())) != 0 ||
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
	r := newSixelTestRenderer(t, &output, 12, 8)
	v := sixelView(p, source)
	r.render(v)
	if pixels.reads.Load() != 0 {
		t.Fatal("producer handoff resampled pixels on the UI goroutine")
	}
	r.awaitRasters(t)
	reads := pixels.reads.Load()
	if reads == 0 {
		t.Fatal("Sixel preparation never sampled the original pixels")
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
	r.show(t, v)
	if pixels.reads.Load() != reads || len(sixelPayloads(t, output.String())) != 0 {
		t.Fatal("unrelated text re-encoded or retransmitted the image")
	}

}

func TestSixelRendererKeepsDisjointImageOnSharedRows(t *testing.T) {
	p, source := sixelFixture(t)
	var output bytes.Buffer
	r := newSixelTestRenderer(t, &output, 12, 8)
	v := sixelView(p, source)
	// A neighboring pane changes on these rows, with stable ink on both
	// sides. This update writes only one cell; there is no line erase.
	source.SetCell(0, 3, &uv.Cell{Content: "A", Width: 1})
	source.SetCell(11, 3, &uv.Cell{Content: "|", Width: 1})
	r.show(t, v)
	output.Reset()
	source.SetCell(0, 3, &uv.Cell{Content: "B", Width: 1})
	source.generation++
	r.show(t, v)
	if !strings.Contains(output.String(), "B") || len(sixelPayloads(t, output.String())) != 0 {
		t.Fatalf("updating a disjoint cell must paint text without retransmitting the stable image (%d output bytes)", output.Len())
	}
}

func TestSixelRendererRestoresRasterAfterTerminalLineErase(t *testing.T) {
	p, source := sixelFixture(t)
	// Adjacent spaces use the same style as native markers after the
	// renderer replaces them with spaces. A real EL can erase both.
	blank := *source.CellAt(2, 3)
	blank.Content = " "
	blank.Style.Fg = sixelCellForeground
	for x := range source.Width() {
		if x < 2 || x >= 2+p.Columns {
			source.SetCell(x, 3, &blank)
		}
	}
	ink := blank
	ink.Content = "A"
	source.SetCell(0, 3, &ink)
	source.SetCell(11, 7, &uv.Cell{Content: "|", Width: 1})
	var output bytes.Buffer
	r := newSixelTestRenderer(t, &output, 12, 8)
	v := sixelView(p, source)
	r.show(t, v)
	output.Reset()
	source.SetCell(0, 3, &blank)
	source.generation++
	r.show(t, v)
	out := output.String()
	if !strings.Contains(out, ansi.EraseLineRight) || len(sixelPayloads(t, out)) != 1 {
		t.Fatalf("terminal line erase must restore the intersected image: %q", out)
	}
}

func TestSixelRendererResizeResetFailureAndClose(t *testing.T) {
	for _, action := range []string{"resize", "reset", "clear", "failed-write"} {
		t.Run(action, func(t *testing.T) {
			p, source := sixelFixture(t)
			r := newSixelTestRenderer(t, io.Discard, 12, 8)
			v := sixelView(p, source)
			r.show(t, v)
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
			r.show(t, v)
			if len(sixelPayloads(t, output.String())) != 1 {
				t.Fatal("image was not restored after terminal invalidation")
			}
			if len(r.sixelShown) != r.cellbuf.Width()*r.cellbuf.Height() || !r.sixelShownArea().In(r.cellbuf.Bounds()) {
				t.Fatal("stale resize geometry escaped the actual terminal")
			}
			output.Reset()
			if err := r.close(); err != nil {
				t.Fatal(err)
			}
			if r.sixelAny || len(r.sixelUncertain) != 0 {
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
	r := newSixelTestRenderer(t, &output, 12, 8)
	r.show(t, sixelView(p, source))
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
	r := newSixelTestRenderer(t, io.Discard, 12, 8)
	r.show(t, sixelView(p, source))
	r.w = kittyFailWriter{}
	if err := r.close(); err == nil || !r.sixelAny {
		t.Fatal("failed close forgot the terminal image that can still be visible")
	}
	var output bytes.Buffer
	r.w = &output
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if r.sixelAny || output.Len() == 0 {
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
		if _, err := prepareSixelBase(context.Background(), sixelWant{tile.placement, tile.background, tile.grid()}, false); err != nil {
			b.Fatal(err)
		}
	}
}

func sixelPhotoBenchmarkImage(b *testing.B) *TerminalImage {
	b.Helper()
	name := os.Getenv("CRUDE_SIXEL_PHOTO")
	if name == "" {
		name = filepath.Join(runtime.GOROOT(), "src", "image", "testdata", "video-001.jpeg")
		if _, err := os.Stat(name); os.IsNotExist(err) {
			b.Skip("Set CRUDE_SIXEL_PHOTO to a local photographic JPEG")
		}
	}
	f, err := os.Open(name)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	pixels, err := jpeg.Decode(f)
	if err != nil {
		b.Fatal(err)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, pixels); err != nil {
		b.Fatal(err)
	}
	return NewTerminalImage(data.Bytes(), pixels)
}

func BenchmarkSixelEncodePhoto(b *testing.B) {
	resource := sixelPhotoBenchmarkImage(b)
	for _, size := range []struct {
		name          string
		columns, rows int
	}{
		{"Typical", 90, 28},
		{"Large", 170, 68},
	} {
		b.Run(size.name, func(b *testing.B) {
			p := ImagePlacement{Image: resource, Columns: size.columns, Rows: size.rows,
				Protocol: ImageSixel, CellWidth: 9, CellHeight: 20}
			tile := sixelTile{placement: p, rect: image.Rect(0, 0, p.Columns, p.Rows), background: uv.RGBColor(20, 40, 60)}
			b.ReportAllocs()
			b.ResetTimer()
			var n int
			for range b.N {
				base, err := prepareSixelBase(context.Background(), sixelWant{tile.placement, tile.background, tile.grid()}, false)
				if err != nil {
					b.Fatal(err)
				}
				n += len(base.full)
			}
			b.ReportMetric(float64(n)/float64(b.N), "wire-B/op")
		})
	}
}

type sixelBenchmarkWriter struct{ n int }

func (w *sixelBenchmarkWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
}

func BenchmarkSixelRendererPhoto(b *testing.B) {
	resource := sixelPhotoBenchmarkImage(b)
	for _, mode := range []string{"Warm", "Unrelated", "SharedRow", "Move", "Open", "Resize", "Remove"} {
		b.Run(mode, func(b *testing.B) {
			p := ImagePlacement{Image: resource, Columns: 90, Rows: 28, Protocol: ImageSixel, CellWidth: 9, CellHeight: 20}
			source := &generationSurface{Buffer: uv.NewBuffer(160, 40), generation: 1}
			write := new(sixelBenchmarkWriter)
			r := newSixelTestRenderer(b, write, 160, 40)
			base, withImage := NewViewFromSurface(source), sixelView(p, source)
			base.AltScreen = true
			flush := func(v View) {
				r.render(v)
				if err := r.flush(false); err != nil {
					b.Fatal(err)
				}
			}
			setSixelCells(source.Buffer, p, 1, 1)
			if mode == "SharedRow" {
				source.SetCell(0, 3, &uv.Cell{Content: "B", Width: 1})
				source.SetCell(159, 3, &uv.Cell{Content: "|", Width: 1})
			}
			if mode != "Open" {
				r.show(b, withImage)
			}
			if mode == "Move" {
				// Prime both buffer sizes so the measured iterations describe
				// steady re-positioning, not the first growth of the envelope.
				for _, pos := range []int{2, 1} {
					source.Clear()
					setSixelCells(source.Buffer, p, pos, pos)
					source.generation++
					flush(withImage)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			write.n = 0
			var transferred int
			var firstPass time.Duration
			for i := range b.N {
				switch mode {
				case "Warm":
					flush(withImage)
				case "Unrelated", "SharedRow":
					y := 0
					if mode == "SharedRow" {
						y = 3
					}
					source.SetCell(0, y, &uv.Cell{Content: []string{"A", "B"}[i%2], Width: 1})
					source.generation++
					flush(withImage)
				case "Move":
					b.StopTimer()
					source.Clear()
					setSixelCells(source.Buffer, p, 1+(i+1)%2, 1+(i+1)%2)
					source.generation++
					b.StartTimer()
					flush(withImage)
				case "Open", "Resize", "Remove":
					b.StopTimer()
					source.Clear()
					source.generation++
					switch mode {
					case "Open":
						flush(base)
						setSixelCells(source.Buffer, p, 1, 1)
					case "Resize":
						setSixelCells(source.Buffer, p, 1, 1)
						r.show(b, withImage)
						source.Clear()
						p2 := p
						p2.Rows--
						setSixelCells(source.Buffer, p2, 1, 1)
						withImage = sixelView(p2, source)
					case "Remove":
						setSixelCells(source.Buffer, p, 1, 1)
						r.show(b, withImage)
						source.Clear()
					}
					source.generation++
					write.n = 0
					b.StartTimer()
					if mode == "Remove" {
						flush(base)
					} else {
						start := time.Now()
						flush(withImage)
						firstPass += time.Since(start)
						r.awaitRasters(b)
					}
					if mode == "Resize" {
						withImage = sixelView(p, source)
					}
				}
				transferred += write.n
				write.n = 0
			}
			b.ReportMetric(float64(transferred)/float64(b.N), "wire-B/op")
			if mode == "Open" || mode == "Resize" {
				b.ReportMetric(float64(firstPass.Nanoseconds())/1000/float64(b.N), "first-flush-us/op")
			}
		})
	}
}

type gatedSixelPixels struct {
	image.Image
	entered, release chan struct{}
	once             sync.Once
}

func (p *gatedSixelPixels) At(x, y int) color.Color {
	p.once.Do(func() {
		close(p.entered)
		<-p.release
	})
	return p.Image.At(x, y)
}

// Measure pane removal with real photographic work known to be pending. The
// gate releases at the removal handoff. A writer that owns conversion still
// cannot present removal until that cold raster has been completed and sent.
func BenchmarkSixelPendingRemovalPhoto(b *testing.B) {
	resource := sixelPhotoBenchmarkImage(b)
	p := ImagePlacement{Columns: 90, Rows: 28, Protocol: ImageSixel, CellWidth: 9, CellHeight: 20}
	source := &generationSurface{Buffer: uv.NewBuffer(160, 40), generation: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		gate := &gatedSixelPixels{Image: resource.pixels, entered: make(chan struct{}), release: make(chan struct{})}
		image := *resource
		image.pixels = gate
		p.Image = &image
		setSixelCells(source.Buffer, p, 1, 1)
		source.generation++
		r := newCursedRenderer(io.Discard, nil, 160, 40)
		r.render(sixelView(p, source))
		flushed := make(chan error, 1)
		go func() { flushed <- r.flush(false) }()
		select {
		case <-gate.entered:
		case <-time.After(5 * time.Second):
			close(gate.release)
			b.Fatal("cold raster was not admitted")
		}
		source.Clear()
		source.SetCell(1, 1, &uv.Cell{Content: "X", Width: 1})
		source.generation++

		b.StartTimer()
		r.render(View{Surface: source, AltScreen: true})
		close(gate.release)
		err := r.flush(false)
		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		if err := <-flushed; err != nil {
			b.Fatal(err)
		}
		if err := r.close(); err != nil {
			b.Fatal(err)
		}
	}
}

type sixelObservingWriter struct{ output chan string }

func (w sixelObservingWriter) Write(p []byte) (int, error) {
	w.output <- string(p)
	return len(p), nil
}

func awaitSixelStage(t *testing.T, gate <-chan struct{}) {
	t.Helper()
	select {
	case <-gate:
	case <-time.After(5 * time.Second):
		t.Fatal("did not reach cold raster preparation")
	}
}

func awaitSixelText(t *testing.T, out <-chan string, text string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame := <-out:
			if len(sixelPayloads(t, frame)) != 0 {
				t.Fatal("superseded image reached the terminal")
			}
			if strings.Contains(frame, text) {
				return
			}
		case <-deadline:
			t.Fatal("pending raster preparation blocked the terminal update")
		}
	}
}

func TestSixelRendererPresentsPaneRemovalWhileImagePreparationIsPending(t *testing.T) {
	p, source := sixelFixture(t)
	pixels := &gatedSixelPixels{Image: p.Image.pixels, entered: make(chan struct{}), release: make(chan struct{})}
	p.Image.pixels = pixels
	out := make(chan string, 16)
	w := sixelObservingWriter{out}
	program := NewProgram(nil, WithOutput(w))
	program.renderer = newCursedRenderer(w, nil, 12, 8)
	program.startRenderer()
	defer program.stopRenderer(true)
	release := sync.OnceFunc(func() { close(pixels.release) })
	defer release()

	program.present(sixelView(p, source))
	awaitSixelStage(t, pixels.entered)
	source.Clear()
	source.SetCell(1, 1, &uv.Cell{Content: "Closed", Width: 6})
	source.generation++
	program.present(View{Surface: source, AltScreen: true})
	awaitSixelText(t, out, "Closed")

	// Advance the real writer after releasing the obsolete preparation. Its
	// completion must not resurrect the removed pane.
	release()
	source.SetCell(1, 2, &uv.Cell{Content: "After", Width: 5})
	source.generation++
	program.present(View{Surface: source, AltScreen: true})
	awaitSixelText(t, out, "After")
}

func TestSixelRendererPendingRasterUsesLatestClippedGeometry(t *testing.T) {
	p, source := sixelFixture(t)
	pixels := &gatedSixelPixels{Image: p.Image.pixels, entered: make(chan struct{}), release: make(chan struct{})}
	p.Image.pixels = pixels
	var output bytes.Buffer
	r := newSixelTestRenderer(t, &output, 12, 8)
	release := sync.OnceFunc(func() { close(pixels.release) })
	defer release()

	r.render(sixelView(p, source))
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	awaitSixelStage(t, pixels.entered)
	r.resize(5, 6) // clips the old detached surface before the model fits again
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	release()
	r.awaitRasters(t)
	images := sixelPayloads(t, output.String())
	if len(images) != 1 || images[0].Bounds() != image.Rect(0, 0, 6, 12) {
		t.Fatalf("resize sent a stale raster instead of the clipped 6×12 crop: %v", images)
	}
}

func TestSixelRendererStopRestoresTerminalBeforeJoiningPendingRaster(t *testing.T) {
	p, source := sixelFixture(t)
	pixels := &gatedSixelPixels{Image: p.Image.pixels, entered: make(chan struct{}), release: make(chan struct{})}
	p.Image.pixels = pixels
	out := make(chan string, 16)
	w := sixelObservingWriter{out}
	program := NewProgram(nil, WithOutput(w))
	program.renderer = newCursedRenderer(w, nil, 12, 8)
	program.startRenderer()
	defer program.stopRenderer(true)
	release := sync.OnceFunc(func() { close(pixels.release) })
	defer release()

	program.present(sixelView(p, source))
	awaitSixelStage(t, pixels.entered)
	stopped := make(chan struct{})
	go func() {
		program.stopRenderer(false)
		close(stopped)
	}()
	awaitSixelText(t, out, ansi.ResetModeAltScreenSaveCursor)
	select {
	case <-stopped:
		t.Fatal("terminal stop abandoned a live raster worker")
	default:
	}
	release()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal stop did not join canceled raster preparation")
	}
	program.startRenderer()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame := <-out:
			images := sixelPayloads(t, frame)
			if len(images) == 0 {
				continue
			}
			if len(images) != 1 || images[0].Bounds() != image.Rect(0, 0, 8, 12) {
				t.Fatalf("restore did not recreate the intended raster: %v", images)
			}
			return
		case <-deadline:
			t.Fatal("the current image was not restored after terminal stop/start")
		}
	}
}

func TestSixelRendererHandoffWhileWriterStalled(t *testing.T) {
	p, source := sixelFixture(t)
	w := &gatedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	r := newSixelTestRenderer(t, w, 12, 8)
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
	if r.sixelAny {
		t.Fatal("superseded image survived the latest completed frame")
	}
}
