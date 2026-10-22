package perf

import (
	"reflect"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/metrics"
)

// Shell event-loop series. The UI goroutine runs Update, View and the frame
// handoff for every message; these name which message types consume it and
// how long user input waited behind them.
var (
	uiUpdate = metrics.NewLatency(metrics.Meta{Name: "ui.update", Group: metrics.GroupUI, Title: "Update",
		Help: "UI goroutine time applying one message. Over 16 ms delays the next frame; over 50 ms is a visible freeze.",
		Warn: 8_000, Crit: 50_000})
	uiMessages = metrics.NewTable(metrics.Meta{Name: "ui.messages", Group: metrics.GroupUI, Title: "UI time by message",
		Help: "Update time per message type. The top rows own the UI goroutine.", Warn: 16_000, Crit: 50_000})
	uiInput = metrics.NewLatency(metrics.Meta{Name: "ui.input", Group: metrics.GroupUI, Title: "Input latency",
		Help: "Key, click or paste until its frame is handed to the terminal: queue wait + Update + paint.",
		Warn: 16_000, Crit: 50_000})
	uiInputWait = metrics.NewLatency(metrics.Meta{Name: "ui.input.wait", Group: metrics.GroupUI, Title: "Input queue wait",
		Help: "Time input waited for the UI goroutine to finish earlier work. Nonzero means the loop was saturated.",
		Warn: 8_000, Crit: 33_000, Stall: 100_000})
	uiView = metrics.NewLatency(metrics.Meta{Name: "ui.view", Group: metrics.GroupRender, Title: "Frame paint",
		Help: "Composing the shell frame (layout, paint, ANSI). Runs after every message.",
		Warn: 8_000, Crit: 33_000})
	uiPresent = metrics.NewLatency(metrics.Meta{Name: "ui.present", Group: metrics.GroupRender, Title: "Frame handoff",
		Help: "Handing the frame to the renderer. It must never wait for terminal output.",
		Warn: 1_000, Crit: 16_000})
	renderFlush = metrics.NewLatency(metrics.Meta{Name: "render.flush", Group: metrics.GroupRender, Title: "Terminal write",
		Help: "Diffing and writing a frame to the terminal on the renderer goroutine. Slow consoles (Windows) show here, not as input lag.",
		Warn: 16_000, Crit: 50_000, Stall: 100_000})
	renderBytes = metrics.NewCounter(metrics.Meta{Name: "render.bytes", Group: metrics.GroupRender, Title: "Terminal output", Unit: "bytes",
		Help: "Bytes written to the terminal. Large steady output with no visible change is wasted repaint."})
)

// LoopObserver records Bubble Tea loop and flush samples into metrics.
type LoopObserver struct {
	names map[reflect.Type]string
}

// NewLoopObserver returns the shell's event-loop observer.
func NewLoopObserver() *LoopObserver {
	return &LoopObserver{names: make(map[reflect.Type]string)}
}

// ObserveLoop runs on the event-loop goroutine.
func (o *LoopObserver) ObserveLoop(sample tea.LoopSample) {
	metrics.MarkUIGoroutine()
	name := o.typeName(sample.Msg)
	uiUpdate.ObserveLabeled(sample.Update, name)
	uiMessages.Observe(name, sample.Update)
	uiView.ObserveLabeled(sample.View, name)
	uiPresent.ObserveLabeled(sample.Present, name)
	if sample.Input {
		total := sample.Wait + sample.Update + sample.View + sample.Present
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
	renderBytes.Add(uint64(sample.Bytes))
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
		if key := msg.Key(); key.Text != "" && key.Mod&^tea.ModShift == 0 {
			return "typing"
		}
		return "key " + msg.String()
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
