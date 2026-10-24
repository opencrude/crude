package tea

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// Observe real flush entry points, including empty refreshes which intentionally
// do not emit an ObserveFlush sample or manufacture terminal bytes.
type pacingRenderer struct {
	*cursedRenderer
	mu     sync.Mutex
	starts []time.Time
}

type pacingWriter struct {
	gate *gatedWriter
	mu   sync.Mutex
	buf  bytes.Buffer
}

func (w *pacingWriter) Write(p []byte) (int, error) {
	if w.gate != nil {
		_, _ = w.gate.Write(p)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *pacingWriter) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (r *pacingRenderer) flush(closing bool) error {
	if !closing {
		r.mu.Lock()
		r.starts = append(r.starts, time.Now())
		r.mu.Unlock()
	}
	return r.cursedRenderer.flush(closing)
}

func (r *pacingRenderer) samples() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.starts...)
}

type pacingModel struct{ counterModel }

func (m pacingModel) Update(msg Msg) (Model, Cmd) {
	if _, ok := msg.(KeyPressMsg); ok {
		msg = bumpMsg{}
	}
	model, cmd := m.counterModel.Update(msg)
	m.counterModel = model.(counterModel)
	return m, cmd
}

func pacingProgram(w io.Writer) (*Program, *pacingRenderer) {
	p := NewProgram(pacingModel{}, WithInput(strings.NewReader("")),
		WithOutput(w), WithWindowSize(80, 24), WithoutSignalHandler(),
		WithEnvironment([]string{"TERM=xterm-256color"}))
	r := &pacingRenderer{cursedRenderer: newCursedRenderer(w, nil, 80, 24)}
	p.renderer = r
	return p, r
}

func runPacingProgram(t *testing.T, p *Program) chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	synctest.Wait()
	t.Cleanup(func() {
		p.Quit()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return done
}

func TestProgramRendererIdleRefreshAndInputDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		output := &pacingWriter{}
		p, r := pacingProgram(output)
		runPacingProgram(t, p)
		// Let the internal width-probe barrier and the initial frame settle.
		time.Sleep(time.Second)
		synctest.Wait()
		before := len(r.samples())
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if n := len(r.samples()) - before; n != 4 {
			t.Fatalf("idle performed %d flushes in 2s, want exactly 4 (not 120)", n)
		}
		// Halfway through an idle slot: actual key entry must preempt the
		// 500ms timer, without waiting for the next idle refresh.
		time.Sleep(250 * time.Millisecond)
		before = len(r.samples())
		start := time.Now()
		writtenBefore := len(output.text())
		p.Send(KeyPressMsg{Code: 'a', Text: "a"})
		synctest.Wait()
		if got := r.samples(); len(got) != before+1 || got[len(got)-1].Sub(start) > 16*time.Millisecond {
			t.Fatalf("key after idle did not flush immediately: before=%d times=%v", before, got)
		}
		if !strings.Contains(output.text()[writtenBefore:], "1") || !strings.Contains(r.lastView.Content, "frame 1\n") {
			t.Fatal("key mutation was not presented to the output writer")
		}
		// An escape sequence with no subsequent View is independently ready.
		p.execute("raw-output-marker")
		synctest.Wait()
		time.Sleep(time.Second / 60)
		synctest.Wait()
		if len(r.samples()) != before+2 {
			t.Fatal("execute did not wake renderer")
		}
		if !strings.Contains(output.text(), "raw-output-marker") {
			t.Fatal("escape output wake did not write queued bytes")
		}
		// Continuous producers coalesce without moving the first deadline.
		before = len(r.samples())
		for range 10 {
			p.Send(bumpMsg{})
			synctest.Wait()
			time.Sleep(time.Millisecond)
		}
		time.Sleep(7 * time.Millisecond)
		synctest.Wait()
		if len(r.samples()) != before+1 {
			t.Fatal("active frame was postponed or flushed above FPS ceiling")
		}
		r.cursedRenderer.mu.Lock()
		latest := r.lastView.Content
		r.cursedRenderer.mu.Unlock()
		if !strings.Contains(latest, "frame 11\n") {
			t.Fatalf("latest state absent: %q", latest)
		}
	})
}

func TestProgramRendererBlockedOutputConflatesAndJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &gatedWriter{entered: make(chan struct{}), release: make(chan struct{})}
		output := &pacingWriter{gate: w}
		p, r := pacingProgram(output)
		done := make(chan error, 1)
		go func() { _, err := p.Run(); done <- err }()
		var release sync.Once
		t.Cleanup(func() {
			release.Do(func() { close(w.release) })
			p.Quit()
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
		synctest.Wait()
		time.Sleep(time.Second)
		w.armed.Store(true)
		p.Send(bumpMsg{})
		synctest.Wait()
		<-w.entered
		for i := range 100 {
			p.Send(bumpMsg{})
			p.Send(WindowSizeMsg{Width: 81 + i, Height: 25})
			p.Send(RawMsg{Msg: "queued-escape"})
		}
		// These also used to acquire the writer's mutex on Update.
		p.Send(ColorProfileMsg{Profile: colorprofile.TrueColor})
		p.Send(ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeSet})
		p.Send(clearScreenMsg{})
		p.Send(printLineMessage{messageBody: "queued-above"})
		synctest.Wait()
		if len(p.rendererWake) != 1 {
			t.Fatal("wake notification did not conflate")
		}
		before := len(r.samples())
		time.Sleep(3300 * time.Millisecond)
		release.Do(func() { close(w.release) })
		synctest.Wait()
		time.Sleep(time.Nanosecond) // next available frame after the physical write
		synctest.Wait()
		if len(r.samples()) != before+1 {
			t.Fatal("blocked writer accumulated ticks or lost latest wake")
		}
		r.cursedRenderer.mu.Lock()
		if !strings.Contains(r.lastView.Content, "frame 101\n") || r.width != 180 {
			t.Errorf("latest complete state lost: width=%d view=%q", r.width, r.lastView.Content)
		}
		r.cursedRenderer.mu.Unlock()
		if text := output.text(); strings.Count(text, "queued-escape") != 100 || !strings.Contains(text, "queued-above") {
			t.Fatalf("queued output lost: %q", text)
		}
		p.stopRenderer(false)
		select {
		case <-p.rendererDone:
		default:
			t.Fatal("Run did not join renderer")
		}
		before = len(r.samples())
		time.Sleep(time.Second)
		if len(r.samples()) != before {
			t.Fatal("orphan refresh after shutdown")
		}
	})
}

func TestProgramRendererReleaseRestoreOwnsTimers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p, r := pacingProgram(io.Discard)
		runPacingProgram(t, p)
		time.Sleep(time.Second)
		for range 3 {
			if err := p.releaseTerminal(true); err != nil {
				t.Fatal(err)
			}
			before := len(r.samples())
			time.Sleep(time.Second)
			if len(r.samples()) != before {
				t.Fatal("released renderer timer still active")
			}
			p.execute("queued-while-released")
			if err := p.RestoreTerminal(); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			time.Sleep(time.Second / 60)
			synctest.Wait()
			if len(r.samples()) != before+1 {
				t.Fatal("restore did not repaint within the first frame ceiling")
			}
			time.Sleep(time.Second)
			synctest.Wait()
			if len(r.samples()) != before+3 {
				t.Fatal("restore leaked or duplicated a refresh timer")
			}
		}
	})
}

// Counts actual renderer entries, not observer samples (empty refreshes have
// no bytes). Run with -benchtime=1x for a bounded real-clock idle measurement.
func BenchmarkProgramRendererIdleWakeups(b *testing.B) {
	p, r := pacingProgram(io.Discard)
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	time.Sleep(time.Second)
	before := len(r.samples())
	start := time.Now()
	b.ResetTimer()
	for range b.N {
		time.Sleep(time.Second)
	}
	b.StopTimer()
	elapsed := time.Since(start)
	n := len(r.samples()) - before
	p.Quit()
	if err := <-done; err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(n)/elapsed.Seconds(), "flushes/s")
}
