package tea

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gatedWriter models a stalled console: once armed, a write blocks until
// released.
type gatedWriter struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	if w.armed.Load() {
		w.once.Do(func() { close(w.entered) })
		<-w.release
	}
	return len(p), nil
}

type counterModel struct{ n int }

type bumpMsg struct{}

func (m counterModel) Init() Cmd { return nil }
func (m counterModel) Update(msg Msg) (Model, Cmd) {
	if _, ok := msg.(bumpMsg); ok {
		m.n++
	}
	return m, nil
}
func (m counterModel) View() View {
	v := NewView(strings.Repeat("frame "+strconv.Itoa(m.n)+"\n", 20))
	v.AltScreen = true
	return v
}

type recordingObserver struct {
	mu      sync.Mutex
	present []time.Duration
	flushes int
}

func (o *recordingObserver) ObserveLoop(s LoopSample) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := s.Msg.(bumpMsg); ok {
		o.present = append(o.present, s.Present)
	}
}

func (o *recordingObserver) ObserveFlush(FlushSample) {
	o.mu.Lock()
	o.flushes++
	o.mu.Unlock()
}

// A frame handoff must never wait for terminal output: the renderer writes
// under its own lock while the event loop keeps applying messages.
func TestFrameHandoffDoesNotWaitForTerminalWrites(t *testing.T) {
	writer := &gatedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	observer := &recordingObserver{}
	var sendElapsed atomic.Int64
	p := NewProgram(counterModel{},
		WithWindowSize(80, 24),
		WithEnvironment([]string{"TERM=xterm-256color"}),
		WithInput(strings.NewReader("")),
		WithOutput(writer),
		WithObserver(observer),
	)
	go func() {
		time.Sleep(50 * time.Millisecond)
		writer.armed.Store(true)
		p.Send(bumpMsg{}) // the next flush writes this frame and stalls
		<-writer.entered
		time.AfterFunc(400*time.Millisecond, func() { close(writer.release) })
		start := time.Now()
		for i := 0; i < 19; i++ {
			if i == 10 {
				p.Send(WindowSizeMsg{Width: 100, Height: 30}) // resize mid-stall
			}
			p.Send(bumpMsg{})
		}
		sendElapsed.Store(int64(time.Since(start)))
		p.Quit()
	}()
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.present) != 20 {
		t.Fatalf("observed %d bump iterations, want 20", len(observer.present))
	}
	for i, d := range observer.present {
		if d > 50*time.Millisecond {
			t.Fatalf("handoff %d waited %v for a stalled terminal write", i, d)
		}
	}
	if elapsed := time.Duration(sendElapsed.Load()); elapsed > 200*time.Millisecond {
		t.Fatalf("the event loop stalled %v behind a terminal write", elapsed)
	}
	if observer.flushes == 0 {
		t.Fatal("no flush samples recorded")
	}
}
