package perf

import (
	"reflect"
	"strconv"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/internal/metrics"
)

// Shell event-loop series. The UI goroutine runs Update, View and the frame
// handoff for every message; these name which message types consume it and
// how long user input waited behind them.
var (
	uiUpdate = metrics.NewLatency(metrics.Meta{Name: "ui.update", SaveLabels: true, Group: metrics.GroupUI, Title: "Update",
		Help: "UI goroutine time applying one message. Over 16 ms delays the next frame; over 50 ms is a visible freeze.",
		Warn: 8_000, Crit: 50_000})
	uiMessages = metrics.NewTable(metrics.Meta{Name: "ui.messages", SaveLabels: true, Group: metrics.GroupUI, Title: "UI time by message",
		Help: "Update time per message type. The top rows own the UI goroutine.", Warn: 16_000, Crit: 50_000})
	uiInput = metrics.NewLatency(metrics.Meta{Name: "ui.input", SaveLabels: true, Group: metrics.GroupUI, Title: "Input latency",
		Help: "Key, click or paste until its frame is handed to the terminal: queue wait + Update + paint.",
		Warn: 16_000, Crit: 50_000})
	uiInputWait = metrics.NewLatency(metrics.Meta{Name: "ui.input.wait", SaveLabels: true, Group: metrics.GroupUI, Title: "Input queue wait",
		Help: "Time input waited for the UI goroutine to finish earlier work. Nonzero means the loop was saturated.",
		Warn: 8_000, Crit: 33_000, Stall: 100_000})
	uiDispatch = metrics.NewLatency(metrics.Meta{Name: "ui.command.wait", SaveLabels: true, Group: metrics.GroupUI, Title: "Command handoff",
		Help: "Time the event loop waited to hand off the command returned by Update.", Warn: 8_000, Crit: 50_000})
	uiView = metrics.NewLatency(metrics.Meta{Name: "ui.view", SaveLabels: true, Group: metrics.GroupRender, Title: "Frame paint",
		Help: "Composing the shell frame (layout, paint, ANSI). Runs after every message.",
		Warn: 8_000, Crit: 33_000})
	uiPresent = metrics.NewLatency(metrics.Meta{Name: "ui.present", SaveLabels: true, Group: metrics.GroupRender, Title: "Frame handoff",
		Help: "Handing the frame to the renderer. It must never wait for terminal output.",
		Warn: 1_000, Crit: 16_000})
	renderFlush = metrics.NewLatency(metrics.Meta{Name: "render.flush", Group: metrics.GroupRender, Title: "Renderer flush",
		Help: "Total renderer pass, including snapshot, diff and output. Compare snapshot lock and output write before attributing a stall.",
		Warn: 16_000, Crit: 50_000, Stall: 100_000})
	renderSnapshot = metrics.NewLatency(metrics.Meta{Name: "render.snapshot", Group: metrics.GroupRender, Title: "Snapshot lock held", Help: "Time the writer held the producer snapshot lock. Frame handoff waits on this lock, not the terminal writer.", Warn: 8000, Crit: 16000})
	renderWrite    = metrics.NewLatency(metrics.Meta{Name: "render.write", Group: metrics.GroupRender, Title: "Output write", Help: "Time spent writing encoded frame bytes to the output writer. Excludes snapshot and diff.", Warn: 16000, Crit: 50000})
	renderBytes    = metrics.NewCounter(metrics.Meta{Name: "render.bytes", Group: metrics.GroupRender, Title: "Terminal output", Unit: "bytes",
		Help: "Bytes written to the terminal. Large steady output with no visible change is wasted repaint."})
)

// LoopObserver records Bubble Tea loop and flush samples into metrics.
type LoopObserver struct {
	names      map[reflect.Type]string
	displayNS  atomic.Int64
	stallPhase uint32
	stalled    []string // UI stack captured during the iteration that just ended
}

// BeginLoop runs on the event-loop goroutine before Update.
func (o *LoopObserver) BeginLoop() {
	metrics.MarkUIGoroutine()
	metrics.BeginUIWork()
}

// SetLoopPhase attributes a watchdog sample only to the phase it observed.
func (o *LoopObserver) SetLoopPhase(phase tea.LoopPhase) { metrics.SetUIWorkPhase(uint32(phase)) }

// EndLoop runs on the event-loop goroutine after the frame handoff.
func (o *LoopObserver) EndLoop() { o.stallPhase, o.stalled = metrics.EndUIWork() }

// NewLoopObserver returns the shell's event-loop observer.
func NewLoopObserver() *LoopObserver {
	return &LoopObserver{names: make(map[reflect.Type]string)}
}

// ObserveLoop runs on the event-loop goroutine.
func (o *LoopObserver) ObserveLoop(sample tea.LoopSample) {
	name := o.typeName(sample.Msg)
	stack := o.stalled
	o.stalled = nil
	stackFor := func(phase tea.LoopPhase) []string {
		if uint32(phase) == o.stallPhase {
			return stack
		}
		return nil
	}
	uiUpdate.ObserveStack(sample.Update, name, stackFor(tea.LoopUpdate))
	uiMessages.Observe(name, sample.Update)
	uiDispatch.ObserveStack(sample.Dispatch, name, stackFor(tea.LoopDispatch))
	uiView.ObserveStack(sample.View, name, stackFor(tea.LoopView))
	uiPresent.ObserveStack(sample.Present, name, stackFor(tea.LoopPresent))
	if sample.Input {
		total := sample.Wait + sample.Update + sample.Dispatch + sample.View + sample.Present
		label := inputKind(sample.Msg)
		if sample.Wait > time.Millisecond {
			label += " · waited " + strconv.FormatInt(sample.Wait.Milliseconds(), 10) + " ms"
		}
		uiInput.ObserveLabeled(total, label)
		uiInputWait.ObserveLabeled(sample.Wait, inputKind(sample.Msg))
	}
}

// ObserveFlush runs on the renderer goroutine.
func (o *LoopObserver) ObserveFlush(sample tea.FlushSample) {
	renderFlush.Observe(sample.Duration)
	if sample.SnapshotHold > 0 {
		renderSnapshot.Observe(sample.SnapshotHold)
	}
	if sample.OutputWrite > 0 {
		renderWrite.Observe(sample.OutputWrite)
	}
	renderBytes.Add(uint64(sample.Bytes))
	if sample.Bytes > 0 {
		o.displayNS.Store(int64(max(16*time.Millisecond, min(sample.Duration, 250*time.Millisecond))))
	}
}

// DisplayInterval is the last measured output cost, not an idle-frame gap.
// It is safe to read on Update while the renderer writes the next frame.
func (o *LoopObserver) DisplayInterval() time.Duration {
	return max(16*time.Millisecond, time.Duration(o.displayNS.Load()))
}

func (o *LoopObserver) typeName(msg tea.Msg) string {
	if msg == nil {
		return "nil"
	}
	t := reflect.TypeOf(msg)
	if name, ok := o.names[t]; ok {
		return name
	}
	name := t.String()
	if len(o.names) < 512 {
		o.names[t] = name
	}
	return name
}

func inputKind(msg tea.Msg) string {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// Printable text is never recorded; only chords and named keys.
		key := msg.Key()
		if key.Text != "" {
			if key.Mod&^tea.ModShift == 0 {
				return "typing"
			}
			return "modified typing"
		}
		return "key " + key.Keystroke()
	case tea.KeyReleaseMsg:
		return "key release"
	case tea.MouseClickMsg:
		return "click"
	case tea.MouseReleaseMsg:
		return "release"
	case tea.MouseWheelMsg:
		return "wheel"
	case tea.MouseMotionMsg:
		return "pointer motion"
	case tea.PasteMsg, tea.PasteStartMsg, tea.PasteEndMsg:
		return "paste"
	case tea.WindowSizeMsg:
		return "resize"
	case tea.FocusMsg, tea.BlurMsg:
		return "focus"
	}
	return "input"
}
