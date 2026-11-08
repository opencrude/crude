package tea

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func kittyTestView(image *KittyImage) View {
	v := NewView("placeholder")
	v.AltScreen = true
	v.Images = []KittyImagePlacement{{Image: image, Columns: 8, Rows: 4}}
	return v
}

func kittyFlush(t *testing.T, r *cursedRenderer, v View) {
	t.Helper()
	r.render(v)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
}

func kittyDelete(image *KittyImage) string {
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", image.ID())
}

func kittyAPCs(t *testing.T, output string) []string {
	t.Helper()
	var commands []string
	for {
		start := strings.Index(output, "\x1b_G")
		if start < 0 {
			return commands
		}
		output = output[start+3:]
		end := strings.Index(output, "\x1b\\")
		if end < 0 {
			t.Fatal("unterminated Kitty APC")
		}
		commands = append(commands, output[:end])
		output = output[end+2:]
	}
}

func TestKittyImageImmutableInputAndBounds(t *testing.T) {
	input := []byte("immutable PNG payload")
	i := NewKittyImage(input)
	if i == nil || i.ID() == 0 {
		t.Fatal("missing nonzero image ID")
	}
	want := string(input)
	input[0] = '!'
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 40, 10)
	kittyFlush(t, r, kittyTestView(i))
	commands := kittyAPCs(t, output.String())
	_, encoded, found := strings.Cut(commands[0], ";")
	if !found {
		t.Fatal("upload has no payload")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(data) != want {
		t.Fatalf("upload retained caller bytes: %q, %v", data, err)
	}
	if NewKittyImage(nil) != nil || NewKittyImage(make([]byte, maxKittyImageBytes+1)) != nil {
		t.Fatal("invalid byte bounds admitted")
	}
	if (*KittyImage)(nil).ID() != 0 || new(KittyImage).ID() != 0 {
		t.Fatal("zero resource must have zero ID")
	}
}

func TestKittyRendererRevealsFirstImageOnce(t *testing.T) {
	i := NewKittyImage([]byte("PNG bytes"))
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 40, 10)
	r.syncdUpdates = true
	pair := ansi.SetModeSynchronizedOutput + ansi.ResetModeSynchronizedOutput
	// Enter the alternate screen first, as Crude does before any file opens.
	empty := NewView("empty")
	empty.AltScreen = true
	kittyFlush(t, r, empty)
	output.Reset()
	kittyFlush(t, r, kittyTestView(i))
	if strings.Count(output.String(), pair) != 1 {
		t.Fatalf("first image did not request exactly one extra pass: %q", output.String())
	}
	if strings.Index(output.String(), "a=p,U=1") > strings.Index(output.String(), pair) {
		t.Fatal("extra pass preceded the placement")
	}
	output.Reset()
	v := kittyTestView(i)
	v.Images[0].Columns = 12
	kittyFlush(t, r, v)
	if strings.Contains(output.String(), pair) {
		t.Fatal("placement update requested another reveal")
	}
}

func TestKittyRendererUploadReuseGeometryRemovalAndClose(t *testing.T) {
	i := NewKittyImage([]byte("PNG bytes"))
	unowned := NewKittyImage([]byte("never submitted"))
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 40, 10)
	v := kittyTestView(i)
	kittyFlush(t, r, v)
	commands := kittyAPCs(t, output.String())
	if len(commands) != 2 || !strings.HasPrefix(commands[0], fmt.Sprintf("a=t,f=100,q=2,i=%d,", i.ID())) ||
		commands[1] != fmt.Sprintf("a=p,U=1,i=%d,c=8,r=4,q=2,p=1", i.ID()) {
		t.Fatalf("wrong initial upload/virtual placement: %q", commands)
	}
	if strings.Index(output.String(), "a=p,U=1") > strings.Index(output.String(), "placeholder") {
		t.Fatal("resource arrived after placeholder cells")
	}
	output.Reset()
	kittyFlush(t, r, v)
	if output.Len() != 0 {
		t.Fatalf("unchanged view wrote %q", output.String())
	}
	v.Content = "changed cells"
	kittyFlush(t, r, v)
	if commands := kittyAPCs(t, output.String()); len(commands) != 0 {
		t.Fatalf("warm resource emitted commands: %q", commands)
	}
	output.Reset()
	v.Images[0].Columns = 12
	kittyFlush(t, r, v) // image-only change must not be swallowed by viewEquals
	commands = kittyAPCs(t, output.String())
	if len(commands) != 1 || commands[0] != fmt.Sprintf("a=p,U=1,i=%d,c=12,r=4,q=2,p=1", i.ID()) {
		t.Fatalf("geometry change reuploaded or skipped placement: %q", commands)
	}
	output.Reset()
	v.Images = nil
	kittyFlush(t, r, v)
	if output.String() != kittyDelete(i) {
		t.Fatalf("removal did not delete exact owned ID: %q", output.String())
	}
	kittyFlush(t, r, kittyTestView(i))
	output.Reset()
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	commands = kittyAPCs(t, output.String())
	if len(commands) != 1 || "\x1b_G"+commands[0]+"\x1b\\" != kittyDelete(i) ||
		strings.Contains(output.String(), kittyDelete(unowned)) || len(r.kittyImages) != 0 {
		t.Fatalf("close did not delete only its exact owned image: %q", commands)
	}
}

func TestKittyRendererChunksBounded(t *testing.T) {
	for _, size := range []int{1, 3072, 3073, 6144, 8000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data := bytes.Repeat([]byte{byte(size)}, size)
			i := NewKittyImage(data)
			var output bytes.Buffer
			r := newCursedRenderer(&output, nil, 40, 10)
			kittyFlush(t, r, kittyTestView(i))
			commands := kittyAPCs(t, output.String())
			var decoded []byte
			for index, command := range commands[:len(commands)-1] {
				opts, payload, ok := strings.Cut(command, ";")
				if !ok || len(payload) > 4096 || !strings.Contains(opts, "q=2") {
					t.Fatalf("unbounded/malformed chunk: %q", command)
				}
				more := index < len(commands)-2
				if strings.Contains(opts, "m=1") != more || strings.Contains(opts, "m=0") == more {
					t.Fatalf("wrong continuation flag: %q", opts)
				}
				chunk, err := base64.StdEncoding.DecodeString(payload)
				if err != nil {
					t.Fatal(err)
				}
				decoded = append(decoded, chunk...)
			}
			if !bytes.Equal(decoded, data) {
				t.Fatal("chunked upload corrupted PNG data")
			}
		})
	}
}

func TestKittyRendererSourceCropReconcilesWithoutReupload(t *testing.T) {
	i := NewKittyImage([]byte("original transparent PNG"))
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 40, 10)
	v := kittyTestView(i)
	v.Images[0].SourceX, v.Images[0].SourceY = 20, 30
	v.Images[0].SourceWidth, v.Images[0].SourceHeight = 64, 48
	kittyFlush(t, r, v)
	commands := kittyAPCs(t, output.String())
	want := fmt.Sprintf("a=p,U=1,i=%d,c=8,r=4,x=20,y=30,w=64,h=48,q=2,p=1", i.ID())
	if len(commands) != 2 || commands[1] != want || !strings.HasPrefix(commands[0], "a=t,f=100,") {
		t.Fatalf("crop did not upload original then place source rectangle: %q", commands)
	}
	_, encoded, _ := strings.Cut(commands[0], ";")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != "original transparent PNG" {
		t.Fatalf("crop changed uploaded resource: %q, %v", decoded, err)
	}
	output.Reset()
	kittyFlush(t, r, v)
	if output.Len() != 0 {
		t.Fatalf("unchanged crop wrote %q", output.String())
	}

	// Each crop field participates in image-only view equality and committed
	// placement state; updating even one must replace the sole prototype.
	for _, field := range []*int{&v.Images[0].SourceX, &v.Images[0].SourceY, &v.Images[0].SourceWidth, &v.Images[0].SourceHeight, &v.Images[0].Columns, &v.Images[0].Rows} {
		output.Reset()
		(*field)++
		kittyFlush(t, r, v)
		commands = kittyAPCs(t, output.String())
		p := v.Images[0]
		want = fmt.Sprintf("a=p,U=1,i=%d,c=%d,r=%d,x=%d,y=%d,w=%d,h=%d,q=2,p=1",
			i.ID(), p.Columns, p.Rows, p.SourceX, p.SourceY, p.SourceWidth, p.SourceHeight)
		if len(commands) != 1 || commands[0] != want || r.kittyImages[i.ID()] != p {
			t.Fatalf("crop/geometry update skipped, reuploaded or failed to commit: %q", commands)
		}
	}

	// Removing the crop explicitly replaces the cropped prototype with the
	// original full-image placement, still using the same uploaded resource.
	output.Reset()
	v.Images[0] = KittyImagePlacement{Image: i, Columns: 8, Rows: 4}
	kittyFlush(t, r, v)
	commands = kittyAPCs(t, output.String())
	if len(commands) != 1 || commands[0] != fmt.Sprintf("a=p,U=1,i=%d,c=8,r=4,q=2,p=1", i.ID()) {
		t.Fatalf("zero crop did not restore original placement: %q", commands)
	}
}

func TestKittyRendererInvalidSourceCropsIgnored(t *testing.T) {
	for _, crop := range [][4]int{
		{-1, 0, 2, 2}, {0, -1, 2, 2}, {0, 0, -1, 2}, {0, 0, 2, -1},
		{1, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 0, 2}, {0, 0, 2, 0},
	} {
		t.Run(fmt.Sprint(crop), func(t *testing.T) {
			i := NewKittyImage([]byte("not uploaded"))
			var output bytes.Buffer
			r := newCursedRenderer(&output, nil, 40, 10)
			v := kittyTestView(i)
			v.Images[0].SourceX, v.Images[0].SourceY = crop[0], crop[1]
			v.Images[0].SourceWidth, v.Images[0].SourceHeight = crop[2], crop[3]
			kittyFlush(t, r, v)
			if err := r.close(); err != nil {
				t.Fatal(err)
			}
			if commands := kittyAPCs(t, output.String()); len(commands) != 0 {
				t.Fatalf("invalid crop acquired ownership or emitted protocol: %q", commands)
			}
		})
	}
	if strconv.IntSize == 64 {
		tooLarge := uint64(1) << 32
		i := NewKittyImage([]byte("not uploaded"))
		for _, crop := range [][4]int{
			{int(tooLarge), 0, 1, 1}, {0, int(tooLarge), 1, 1},
			{0, 0, int(tooLarge), 1}, {0, 0, 1, int(tooLarge)},
		} {
			p := KittyImagePlacement{Image: i, Columns: 1, Rows: 1,
				SourceX: crop[0], SourceY: crop[1], SourceWidth: crop[2], SourceHeight: crop[3]}
			if p.valid() {
				t.Fatalf("crop exceeds protocol's uint32 field: %+v", p)
			}
		}
	}
}

func TestKittyRendererCropFailureAndInvalidReplacement(t *testing.T) {
	i := NewKittyImage([]byte("original PNG"))
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 40, 10)
	v := kittyTestView(i)
	v.Images[0].SourceWidth, v.Images[0].SourceHeight = 64, 48
	kittyFlush(t, r, v)
	committed := r.kittyImages[i.ID()]
	v.Images[0].SourceX = 3
	r.w = kittyFailWriter{}
	r.render(v)
	if err := r.flush(false); err == nil {
		t.Fatal("failed crop update was accepted")
	}
	if r.kittyImages[i.ID()] != committed || r.lastView.Images[0] != committed {
		t.Fatal("failed write committed the new crop")
	}
	output.Reset()
	r.w = &output
	kittyFlush(t, r, v)
	commands := kittyAPCs(t, output.String())
	if len(commands) != 3 || !strings.HasPrefix(commands[0], "a=d,") ||
		!strings.HasPrefix(commands[1], "a=t,") ||
		commands[2] != fmt.Sprintf("a=p,U=1,i=%d,c=8,r=4,x=3,y=0,w=64,h=48,q=2,p=1", i.ID()) {
		t.Fatalf("retry did not recreate the requested crop: %q", commands)
	}
	// An invalid earlier descriptor must not hide the valid later descriptor.
	v.Images = append([]KittyImagePlacement{{Image: i, Columns: 8, Rows: 4, SourceX: -1}}, v.Images...)
	output.Reset()
	kittyFlush(t, r, v)
	if commands := kittyAPCs(t, output.String()); len(commands) != 0 || r.kittyImages[i.ID()] != v.Images[1] {
		t.Fatalf("invalid duplicate displaced a valid crop: %q", commands)
	}
	// If only an invalid replacement remains, drop the previously owned image.
	v.Images = v.Images[:1]
	output.Reset()
	kittyFlush(t, r, v)
	if output.String() != kittyDelete(i) || len(r.kittyImages) != 0 {
		t.Fatalf("invalid replacement retained stale crop ownership: %q", output.String())
	}
}

func TestKittyRendererSnapshotsAndConflatesCompleteViews(t *testing.T) {
	a, b, c := NewKittyImage([]byte("A")), NewKittyImage([]byte("B")), NewKittyImage([]byte("C"))
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 40, 10)
	kittyFlush(t, r, kittyTestView(a))
	output.Reset()
	r.render(kittyTestView(b)) // never flushed: no upload or deletion for B
	v := kittyTestView(c)
	r.render(v)
	v.Images[0] = KittyImagePlacement{Image: b, Columns: 99, Rows: 99}
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	commands := kittyAPCs(t, output.String())
	if len(commands) != 3 || !strings.Contains(output.String(), kittyDelete(a)) ||
		strings.Contains(output.String(), fmt.Sprintf("i=%d,", b.ID())) ||
		!strings.Contains(output.String(), fmt.Sprintf("a=p,U=1,i=%d,c=8,r=4", c.ID())) {
		t.Fatalf("dropped view or mutable descriptor affected flush: %q", commands)
	}
	if r.lastView.Images[0].Image != c || r.view.Images[0].Image != c {
		t.Fatal("immutable resource pointer not retained")
	}
	output.Reset()
	r.render(kittyTestView(b))
	v.Images = nil
	r.render(v) // dropping C and B must delete C only
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if output.String() != kittyDelete(c) {
		t.Fatalf("empty complete set did not reconcile last actual frame: %q", output.String())
	}
}

func TestKittyRendererLifecycleReuploadsOwnedResources(t *testing.T) {
	for _, operation := range []string{"clear", "reset", "suspend", "alt-exit", "alt-enter"} {
		t.Run(operation, func(t *testing.T) {
			i := NewKittyImage([]byte("owned PNG"))
			var output bytes.Buffer
			r := newCursedRenderer(&output, nil, 40, 10)
			v := kittyTestView(i)
			if operation == "alt-enter" {
				v.AltScreen = false
			}
			kittyFlush(t, r, v)
			output.Reset()
			switch operation {
			case "clear":
				r.clearScreen()
			case "reset":
				r.reset()
			case "suspend":
				if err := r.close(); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(output.String(), kittyDelete(i)) {
					t.Fatal("suspend did not delete owned resource")
				}
				output.Reset()
				r.start()
			case "alt-exit", "alt-enter":
				v.AltScreen = !v.AltScreen
			}
			kittyFlush(t, r, v)
			commands := kittyAPCs(t, output.String())
			wantCount := 3
			if operation == "suspend" {
				wantCount = 2 // close already deleted it
			}
			if len(commands) != wantCount || strings.Count(output.String(), "a=t,f=100") != 1 ||
				strings.Count(output.String(), "a=p,U=1") != 1 || !strings.Contains(output.String(), "placeholder") {
				t.Fatalf("lifecycle failed to recreate resources and cells: %q", output.String())
			}
			if len(r.kittyImages) != 1 || r.kittyImages[i.ID()].Image != i {
				t.Fatal("lifecycle did not retain recreated resource ownership")
			}
			if strings.HasPrefix(operation, "alt-") {
				mode := ansi.ResetModeAltScreenSaveCursor
				if v.AltScreen {
					mode = ansi.SetModeAltScreenSaveCursor
				}
				deleted, switched, uploaded := strings.Index(output.String(), kittyDelete(i)), strings.Index(output.String(), mode), strings.Index(output.String(), "\x1b_Ga=t")
				if deleted < 0 || switched <= deleted || uploaded <= switched {
					t.Fatalf("screen lifecycle out of order: %q", output.String())
				}
			}
			output.Reset()
			kittyFlush(t, r, v)
			if output.Len() != 0 {
				t.Fatal("lifecycle invalidation remained dirty")
			}
			v.Content = "updated placeholder"
			kittyFlush(t, r, v)
			if commands := kittyAPCs(t, output.String()); len(commands) != 0 {
				t.Fatalf("recreated resource uploaded again on text change: %q", commands)
			}
		})
	}
}

func TestKittyRendererInvalidPlacementsNeverDeleteUnowned(t *testing.T) {
	i := NewKittyImage([]byte("never valid"))
	v := NewView("text")
	v.Images = []KittyImagePlacement{{}, {Image: new(KittyImage), Columns: 1, Rows: 1}, {Image: i}, {Image: i, Columns: -1, Rows: 3}}
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 40, 10)
	kittyFlush(t, r, v)
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if commands := kittyAPCs(t, output.String()); len(commands) != 0 {
		t.Fatalf("invalid/unowned descriptors emitted image commands: %q", commands)
	}
}

func TestKittyRendererTmuxWrapsEveryAPC(t *testing.T) {
	i := NewKittyImage(bytes.Repeat([]byte("x"), 8000))
	var plain, tmux bytes.Buffer
	a := newCursedRenderer(&plain, nil, 40, 10)
	b := newCursedRenderer(&tmux, []string{"TMUX=/tmp/test,1,0"}, 40, 10)
	kittyFlush(t, a, kittyTestView(i))
	kittyFlush(t, b, kittyTestView(i))
	if err := a.close(); err != nil {
		t.Fatal(err)
	}
	if err := b.close(); err != nil {
		t.Fatal(err)
	}
	commands := kittyAPCs(t, plain.String())
	if len(commands) != 5 || strings.Count(tmux.String(), "\x1bPtmux;") != len(commands) {
		t.Fatalf("not every image APC wrapped: %q", tmux.String())
	}
	for _, command := range commands {
		if !strings.Contains(tmux.String(), ansi.TmuxPassthrough("\x1b_G"+command+"\x1b\\")) {
			t.Fatalf("missing passthrough for %q", command)
		}
	}
}

func TestKittyRendererHandoffWhileWriterStalled(t *testing.T) {
	w := &gatedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	r := newCursedRenderer(w, nil, 40, 10)
	a, b := NewKittyImage([]byte("A")), NewKittyImage([]byte("B"))
	r.render(kittyTestView(a))
	w.armed.Store(true)
	flushed := make(chan error, 1)
	go func() { flushed <- r.flush(false) }()
	select {
	case <-w.entered:
	case <-time.After(2 * time.Second):
		close(w.release)
		<-flushed
		t.Fatal("flush did not enter writer")
	}
	done := make(chan struct{})
	go func() {
		r.render(kittyTestView(b))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		close(w.release)
		<-done
		<-flushed
		t.Fatal("resource handoff blocked behind writer")
	}
	close(w.release)
	if err := <-flushed; err != nil {
		t.Fatal(err)
	}
	if r.lastView.Images[0].Image != a {
		t.Fatal("pending view altered in-flight flush")
	}
	var output bytes.Buffer
	r.w = &output
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), kittyDelete(a)) || !strings.Contains(output.String(), fmt.Sprintf("a=t,f=100,q=2,i=%d", b.ID())) {
		t.Fatalf("latest frame lost across stalled output: %q", output.String())
	}
}

type kittyFailWriter struct{}

func (kittyFailWriter) Write(p []byte) (int, error) {
	return len(p) / 2, errors.New("stalled terminal failed")
}

func TestKittyRendererFailedWriteDoesNotCommitResources(t *testing.T) {
	a, b := NewKittyImage([]byte("A")), NewKittyImage([]byte("B"))
	r := newCursedRenderer(io.Discard, nil, 40, 10)
	kittyFlush(t, r, kittyTestView(a))
	r.w = kittyFailWriter{}
	r.render(kittyTestView(b))
	if err := r.flush(false); err == nil {
		t.Fatal("writer failure ignored")
	}
	if r.lastView.Images[0].Image != a || len(r.kittyImages) != 1 || r.kittyImages[a.ID()].Image != a {
		t.Fatal("failed frame committed resource state")
	}
	var output bytes.Buffer
	r.w = &output
	r.render(View{AltScreen: true})
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	commands := kittyAPCs(t, output.String())
	if len(commands) != 2 || !strings.Contains(output.String(), kittyDelete(a)) || !strings.Contains(output.String(), kittyDelete(b)) {
		t.Fatalf("failed write left potentially transmitted resource behind: %q", commands)
	}
}

func TestKittyRendererWarmHandoffAndReconcileAllocateNothing(t *testing.T) {
	for _, images := range []bool{false, true} {
		t.Run(fmt.Sprint(images), func(t *testing.T) {
			r := newCursedRenderer(io.Discard, nil, 40, 10)
			v := NewViewFromSurface(uv.NewBuffer(40, 10))
			v.AltScreen = true
			if images {
				v.Images = []KittyImagePlacement{{Image: NewKittyImage([]byte("warm")), Columns: 8, Rows: 4}}
			}
			kittyFlush(t, r, v)
			allocs := testing.AllocsPerRun(100, func() {
				r.render(v)
				r.reconcileKittyImages(r.view.Images, false, false)
			})
			if allocs != 0 || r.buf.Len() != 0 {
				t.Fatalf("warm handoff/reconcile: %g allocs, %d output bytes", allocs, r.buf.Len())
			}
		})
	}
}

func BenchmarkKittyRendererWarmFrame(b *testing.B) {
	for _, images := range []bool{false, true} {
		b.Run(fmt.Sprintf("images=%v", images), func(b *testing.B) {
			r := newCursedRenderer(io.Discard, nil, 40, 10)
			v := NewViewFromSurface(uv.NewBuffer(40, 10))
			v.AltScreen = true
			if images {
				v.Images = []KittyImagePlacement{{Image: NewKittyImage([]byte("warm")), Columns: 8, Rows: 4}}
			}
			r.render(v)
			if err := r.flush(false); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				r.render(v)
				if err := r.flush(false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
