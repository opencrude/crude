package tea

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func assertDirectGrid(t testing.TB, a, b uv.ScreenBuffer) {
	t.Helper()
	if a.Bounds() != b.Bounds() {
		t.Fatalf("bounds %v != %v", a.Bounds(), b.Bounds())
	}
	for y := 0; y < a.Height(); y++ {
		for x := 0; x < a.Width(); x++ {
			if !a.CellAt(x, y).Equal(b.CellAt(x, y)) {
				t.Fatalf("cell (%d,%d): %#v != %#v", x, y, a.CellAt(x, y), b.CellAt(x, y))
			}
		}
	}
}

func BenchmarkDirectTrueColorFlush(b *testing.B) {
	const width, height = 200, 50
	r := newCursedRenderer(io.Discard, []string{"TERM=xterm-256color"}, width, height)
	r.setColorProfile(colorprofile.TrueColor)
	src := uv.NewScreenBuffer(width, height)
	v := NewViewFromSurface(src)
	v.AltScreen = true
	b.ReportAllocs()
	for frame := 0; frame < b.N; frame++ {
		for y, row := range src.Lines {
			for x := range row {
				n := uint32(frame*width*height + y*width + x)
				row[x] = uv.Cell{Content: " ", Width: 1, Style: uv.Style{Bg: uv.RGBColor(uint8(n>>16), uint8(n>>8), uint8(n))}}
			}
		}
		r.render(v)
		if err := r.flush(false); err != nil {
			b.Fatal(err)
		}
	}
}

func TestTrueColorFlushDoesNotAllocateAFullEnvelopeEachFrame(t *testing.T) {
	const width, height = 200, 50
	r := newCursedRenderer(io.Discard, []string{"TERM=xterm-256color"}, width, height)
	r.setColorProfile(colorprofile.TrueColor)
	src := uv.NewScreenBuffer(width, height)
	v := NewViewFromSurface(src)
	v.AltScreen = true
	frame := 0
	step := func() {
		frame++
		for y, row := range src.Lines {
			for x := range row {
				n := uint32(frame*width*height + y*width + x)
				row[x] = uv.Cell{Content: " ", Width: 1, Style: uv.Style{Bg: uv.RGBColor(uint8(n>>16), uint8(n>>8), uint8(n))}}
			}
		}
		r.render(v)
		if err := r.flush(false); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 {
		step()
	}
	result := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			step()
		}
	})
	// Actual RGB output exceeds 170 KiB. The remaining small allocations are
	// cursor/hash bookkeeping; no maximum-size retained buffer is preallocated.
	if result.AllocedBytesPerOp() >= 64<<10 {
		t.Fatalf("flush allocates a whole video envelope: %d B/frame", result.AllocedBytesPerOp())
	}
}

type generationSurface struct {
	*uv.Buffer
	owner      uv.SnapshotOwner
	generation uint64
	draws      int
}

func (s *generationSurface) SnapshotToken() uv.SnapshotToken {
	return uv.SnapshotToken{Owner: &s.owner, Generation: s.generation}
}

func (s *generationSurface) Draw(dst uv.Screen, area uv.Rectangle) {
	s.draws++
	s.Buffer.Draw(dst, area)
}

func TestDirectSnapshotRetainsOnlyExactCompletedGeneration(t *testing.T) {
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 8, 2)
	src := &generationSurface{Buffer: uv.NewBuffer(8, 2), generation: 1}
	src.SetCell(0, 0, &uv.Cell{Content: "A", Width: 1})
	v := NewViewFromSurface(src)
	v.AltScreen = true
	r.render(v)
	// The producer can reuse borrowed memory after handoff. A repeated exact
	// publication must reuse its detached snapshot, not read that memory again.
	src.SetCell(0, 0, &uv.Cell{Content: "poison", Width: 1})
	v.WindowTitle = "metadata still changes"
	r.render(v)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if src.draws != 1 || r.cellbuf.CellAt(0, 0).Content != "A" || r.lastView.WindowTitle != v.WindowTitle {
		t.Fatal("repeated publication reread borrowed source or lost metadata", src.draws)
	}
	// Every new generation, even on the same source pointer, must be copied.
	src.generation++
	src.SetCell(0, 0, &uv.Cell{Content: "B", Width: 1})
	r.render(v)
	src.generation++
	src.SetCell(0, 0, &uv.Cell{Content: "C", Width: 1})
	r.render(v)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if src.draws != 3 || r.cellbuf.CellAt(0, 0).Content != "C" {
		t.Fatal("generation conflation hid the latest cells")
	}
	// Owner, size, and transitions through strings are separate boundaries.
	other := &generationSurface{Buffer: uv.NewBuffer(8, 2), generation: src.generation}
	other.SetCell(0, 0, &uv.Cell{Content: "D", Width: 1})
	v.Surface = other
	r.render(v)
	r.render(NewView("intermediate string"))
	r.render(v)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if other.draws != 2 || r.cellbuf.CellAt(0, 0).Content != "D" {
		t.Fatal("new owner or intervening string reused an unrelated snapshot")
	}
}

func TestDirectCellRendererMatchesStringOutput(t *testing.T) {
	var directOut, referenceOut bytes.Buffer
	direct := newCursedRenderer(&directOut, []string{"TERM=xterm-256color"}, 40, 5)
	reference := newCursedRenderer(&referenceOut, []string{"TERM=xterm-256color"}, 40, 5)
	direct.setColorProfile(colorprofile.TrueColor)
	reference.setColorProfile(colorprofile.TrueColor)
	for i, text := range []string{
		"plain 界 é ♻️ 👨‍👩‍👧‍👦 🇯🇵 END\nsecond row",
		"\x1b[1;3;4;38;2;12;34;56;48;5;22mstyled 界\x1b[0m\n\x1b]8;id=x;https://example.org\x1b\\link\x1b]8;;\x1b\\",
		"", "short", "resized 界\nmore\nrows",
	} {
		w, h := 40, 5
		if i == 4 {
			w, h = 12, 3
			direct.resize(w, h)
			reference.resize(w, h)
		}
		buf := uv.NewScreenBuffer(w, h)
		buf.Method = ansi.GraphemeWidth
		uv.NewStyledString(text).Draw(buf, buf.Bounds())
		v := NewViewFromSurface(buf)
		v.AltScreen, v.ReportFocus, v.MouseMode = true, true, MouseModeAllMotion
		v.WindowTitle, v.IconName = "direct cells", "direct cells"
		v.Cursor = NewCursor(i%w, i%h)
		ref := v
		ref.Surface, ref.Content = nil, text
		// Empty direct alt-screen frames retain full geometry; compare to a
		// blank full-screen string, not legacy string's zero-height behavior.
		if text == "" {
			ref.Content = strings.Repeat(" ", w)
		}
		direct.render(v)
		reference.render(ref)
		if err := direct.flush(false); err != nil {
			t.Fatal(err)
		}
		if err := reference.flush(false); err != nil {
			t.Fatal(err)
		}
		assertDirectGrid(t, direct.cellbuf, reference.cellbuf)
		if directOut.String() != referenceOut.String() {
			t.Fatalf("frame %d terminal writes differ:\ndirect %q\nstring %q", i, directOut.String(), referenceOut.String())
		}
		directOut.Reset()
		referenceOut.Reset()
	}
}

func TestDirectCellHandoffSnapshotsMutableSourceAndMetadata(t *testing.T) {
	r := newCursedRenderer(io.Discard, nil, 8, 2)
	buf := uv.NewScreenBuffer(8, 2)
	buf.SetCell(1, 0, &uv.Cell{Content: "界", Width: 2})
	v := NewViewFromSurface(buf)
	v.AltScreen = true
	v.Cursor = NewCursor(1, 0)
	r.render(v)
	buf.Clear()
	v.Cursor.X = 7
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if r.cellbuf.CellAt(1, 0).Content != "界" || r.lastView.Cursor.X != 1 {
		t.Fatal("flush read mutable producer state after handoff")
	}
	// Multiple handoffs without a flush conflate to latest complete state.
	for _, x := range []int{0, 5, 3} {
		buf.Clear()
		buf.SetCell(x, 1, &uv.Cell{Content: "X", Width: 1})
		r.render(v)
	}
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if r.cellbuf.CellAt(3, 1).Content != "X" || r.cellbuf.CellAt(0, 1).Content != " " || r.cellbuf.CellAt(5, 1).Content != " " {
		t.Fatal("conflation retained an intermediate frame")
	}
	// An unchanged re-used source produces no terminal writes.
	r.render(v)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if r.flushedBytes() != 0 {
		t.Fatalf("unchanged cells emitted %d bytes", r.flushedBytes())
	}
	// Explicit empty surface wins even over nonempty legacy Content.
	v.Surface, v.Content = uv.NewBuffer(0, 0), "must not render"
	r.render(v)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.cellbuf.String(), "must not render") || strings.TrimSpace(r.cellbuf.String()) != "" {
		t.Fatal("empty direct frame retained stale content")
	}
}

type rowSnapshotSurface struct {
	*uv.Buffer
	scratch []uv.Cell
	rows    int
}

func (s *rowSnapshotSurface) Draw(dst uv.Screen, area uv.Rectangle) {
	rows, ok := dst.(interface{ SetCellRow(int, []uv.Cell) })
	if !ok {
		s.Buffer.Draw(dst, area)
		return
	}
	for y, row := range s.Lines {
		s.scratch = append(s.scratch[:0], row...)
		rows.SetCellRow(y, s.scratch)
		s.rows++
	}
	// Reuse scratch immediately, including before render returns.
	for i := range s.scratch {
		s.scratch[i] = uv.Cell{Content: "poison", Width: 1}
	}
}

func TestRowSnapshotCopiesScratchAndPreservesConflation(t *testing.T) {
	r := newCursedRenderer(io.Discard, nil, 8, 2)
	src := &rowSnapshotSurface{Buffer: uv.NewBuffer(8, 2)}
	v := NewViewFromSurface(src)
	v.AltScreen = true
	for _, size := range [][2]int{{8, 2}, {12, 3}, {4, 1}} {
		r.resize(size[0], size[1])
		src.Resize(size[0], size[1])
		for _, text := range []string{"first", "latest"} {
			src.Clear()
			for y := range src.Lines {
				src.SetCell(0, y, &uv.Cell{Content: text[:1], Width: 1, Style: uv.Style{Attrs: uv.AttrBold, Fg: uv.NewColor(ansi.IndexedColor(2))}, Link: uv.Link{URL: "https://example.org"}})
			}
			r.render(v)
		}
		want := src.Clone()
		src.Clear() // producer is free to mutate after submission
		if err := r.flush(false); err != nil {
			t.Fatal(err)
		}
		for y, row := range want.Lines {
			for x := range row {
				if !r.cellbuf.CellAt(x, y).Equal(&row[x]) {
					t.Fatalf("size %v cell %d,%d retained producer/scratch data: %+v", size, x, y, r.cellbuf.CellAt(x, y))
				}
			}
		}
	}
	if src.rows != 12 {
		t.Fatalf("snapshot did not use complete-row path: %d rows", src.rows)
	}
}

func TestDirectCellHandoffDoesNotWaitForStalledFlush(t *testing.T) {
	w := &gatedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	r := newCursedRenderer(w, []string{"TERM=xterm-256color"}, 80, 24)
	buf := uv.NewScreenBuffer(80, 24)
	buf.SetCell(0, 0, &uv.Cell{Content: "A", Width: 1})
	v := NewViewFromSurface(buf)
	v.AltScreen = true
	r.render(v)
	w.armed.Store(true)
	flushed := make(chan error, 1)
	go func() { flushed <- r.flush(false) }()
	select {
	case <-w.entered:
	case <-time.After(2 * time.Second):
		close(w.release)
		t.Fatal("flush did not reach writer")
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 2; i++ {
			buf.Clear()
			buf.SetCell(i%80, 0, &uv.Cell{Content: "B", Width: 1})
			r.render(v)
			r.resize(80, 24)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		close(w.release)
		<-done
		<-flushed
		t.Fatal("direct handoff waited for terminal I/O")
	}
	close(w.release)
	if err := <-flushed; err != nil {
		t.Fatal(err)
	}
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if r.cellbuf.CellAt(1, 0).Content != "B" {
		t.Fatal("latest complete frame did not survive stalled output")
	}
}

func BenchmarkDirectRendererHandoff(b *testing.B) {
	buf := uv.NewScreenBuffer(200, 60)
	buf.Method = ansi.GraphemeWidth
	uv.NewStyledString(strings.Repeat("dense styled text 界 é 🧑‍🌾\n", 60)).Draw(buf, buf.Bounds())
	r := newCursedRenderer(io.Discard, nil, 200, 60)
	v := NewViewFromSurface(buf)
	v.AltScreen = true
	r.render(v)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.render(v)
	}
}

func TestFlushPacingUsesMeasuredWorkWithoutTickBacklog(t *testing.T) {
	for _, tc := range []struct{ minimum, flush, want time.Duration }{
		{16 * time.Millisecond, 0, 16 * time.Millisecond},
		{16 * time.Millisecond, 5 * time.Millisecond, 11 * time.Millisecond},
		{16 * time.Millisecond, 40 * time.Millisecond, time.Nanosecond},
		{time.Second, time.Millisecond, 499 * time.Millisecond},
		{16 * time.Millisecond, time.Second, time.Nanosecond},
	} {
		if got := nextFrameDelay(tc.minimum, tc.flush); got != tc.want {
			t.Fatalf("minimum=%v flush=%v delay=%v want=%v", tc.minimum, tc.flush, got, tc.want)
		}
	}
}

func TestDirectFrameOnlyTouchesChangedRows(t *testing.T) {
	r := newCursedRenderer(io.Discard, nil, 20, 4)
	buf := uv.NewScreenBuffer(20, 4)
	v := NewViewFromSurface(buf)
	v.AltScreen = true
	r.render(v)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	buf.SetCell(2, 1, &uv.Cell{Content: "界", Width: 2})
	r.render(v)
	r.drawFrame(false)
	if n := r.cellbuf.TouchedLines(); n != 1 {
		t.Fatalf("single-row invalidation touched %d rows", n)
	}
	if row := r.cellbuf.Touched[1]; row.FirstCell != 2 || row.LastCell != 4 {
		t.Fatalf("wide cell damage %+v", row)
	}
}

func FuzzDirectRendererResizeAndConflation(f *testing.F) {
	f.Add([]byte{40, 5, 1, 0, 12, 3, 2, 1, 0, 0, 0, 0, 80, 24, 3, 1})
	f.Add([]byte{1, 1, 0, 1, 2, 2, 1, 1, 8, 3, 2, 0})
	f.Fuzz(func(t *testing.T, sequence []byte) {
		if len(sequence) > 128 {
			sequence = sequence[:128]
		}
		r := newCursedRenderer(io.Discard, nil, 1, 1)
		for i := 0; i+3 < len(sequence); i += 4 {
			w, h := int(sequence[i]%41), int(sequence[i+1]%13)
			buf := uv.NewScreenBuffer(w, h)
			buf.Method = ansi.GraphemeWidth
			text := []string{"", "row 界 é end", "\x1b[1;4;38;5;123mstyled🇯🇵\x1b[0m", "top\nbottom"}[sequence[i+2]%4]
			uv.NewStyledString(text).Draw(buf, buf.Bounds())
			r.resize(w, h)
			v := NewViewFromSurface(buf)
			v.AltScreen = true
			r.render(v)
			// A subset of snapshots are consumed; others are overwritten by
			// the next handoff. Compare each consumed frame to a full repaint.
			if sequence[i+3]&1 == 0 {
				continue
			}
			if err := r.flush(false); err != nil {
				t.Fatal(err)
			}
			assertDirectGrid(t, r.cellbuf, buf)
		}
	})
}
