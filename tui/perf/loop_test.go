package perf

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/internal/metrics"
)

func TestDisplayCadenceMeasuresWritesNotIdleGaps(t *testing.T) {
	observer := NewLoopObserver()
	if got := observer.DisplayInterval(); got != 16*time.Millisecond {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		duration, want time.Duration
		bytes          int
	}{
		{2 * time.Millisecond, 16 * time.Millisecond, 200},
		{80 * time.Millisecond, 80 * time.Millisecond, 200},
		{time.Second, 80 * time.Millisecond, 0},
		{time.Second, 250 * time.Millisecond, 200},
		{3 * time.Millisecond, 16 * time.Millisecond, 200},
	} {
		observer.ObserveFlush(tea.FlushSample{Duration: tc.duration, Bytes: tc.bytes})
		if got := observer.DisplayInterval(); got != tc.want {
			t.Fatalf("flush=%v bytes=%d got %v want %v", tc.duration, tc.bytes, got, tc.want)
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			observer.ObserveFlush(tea.FlushSample{Duration: 30 * time.Millisecond, Bytes: 50})
		}
	}()
	for range 1000 {
		_ = observer.DisplayInterval()
	}
	wg.Wait()
}

// stallModel stalls the UI goroutine once, in a function whose name the
// saved evidence must carry.
type stallModel struct{ stalled bool }

type stallMsg struct{}

func (m *stallModel) Init() tea.Cmd { return func() tea.Msg { return stallMsg{} } }

func (m *stallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(stallMsg); ok && !m.stalled {
		m.stalled = true
		holdTheUIThread()
		return m, tea.Quit
	}
	return m, nil
}

func (m *stallModel) View() tea.View { return tea.NewView("") }

//go:noinline
func holdTheUIThread() { time.Sleep(150 * time.Millisecond) }

// A stall before ObserveLoop has ever run must still capture a stack.
func TestFirstLoopBindsBeforeWork(t *testing.T) {
	metrics.ResetUIGoroutineForTest()
	t.Cleanup(metrics.ResetUIGoroutineForTest)
	o := NewLoopObserver()
	o.BeginLoop()
	holdTheUIThread()
	o.EndLoop()
	if !strings.Contains(strings.Join(o.stalled, "\n"), "holdTheUIThread") {
		t.Fatalf("first iteration stack: %v", o.stalled)
	}
}

// A real program loop that stalls in Update records a stall event that names
// the stalling function, so the Performance panel and the saved history show
// the code, not only the message type.
func TestStalledUpdateRecordsTheStalledCode(t *testing.T) {
	metrics.ResetForTest()
	metrics.ResetUIGoroutineForTest()
	t.Cleanup(metrics.ResetUIGoroutineForTest)
	started := time.Now()
	p := tea.NewProgram(&stallModel{}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(),
		tea.WithObserver(NewLoopObserver()))
	// Windows startup messages can run before the deliberately stalled input.
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	for _, event := range metrics.EventsSince(started) {
		if event.Series != "ui.update" {
			continue
		}
		if event.Label != "perf.stallMsg" {
			t.Fatalf("the stall names %q, want the message type", event.Label)
		}
		for _, frame := range event.Stack {
			if strings.Contains(frame, "holdTheUIThread") && strings.Contains(frame, "loop_test.go:") {
				return
			}
		}
		t.Fatalf("the stall stack does not name the stalling function:\n%s", strings.Join(event.Stack, "\n"))
	}
	t.Fatal("a 150 ms Update recorded no stall")
}
