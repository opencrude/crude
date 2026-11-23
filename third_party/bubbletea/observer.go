package tea

import "time"

// LoopSample describes one event-loop iteration: the message, how long input
// waited for the loop (input only), and the Update, View and frame handoff
// costs. All of it runs on the event-loop goroutine.
type LoopSample struct {
	Msg      Msg
	Input    bool
	Wait     time.Duration
	Update   time.Duration
	Dispatch time.Duration // handing off the command returned by Update
	View     time.Duration
	Present  time.Duration
}

// FlushSample describes one renderer flush: diffing the latest frame and
// writing it to the terminal. It runs on the renderer goroutine.
type FlushSample struct {
	Duration time.Duration
	Bytes    int
}

// Observer receives loop and flush samples. ObserveLoop runs on the event
// loop and ObserveFlush on the renderer goroutine; both must be cheap.
type Observer interface {
	ObserveLoop(LoopSample)
	ObserveFlush(FlushSample)
}

// LoopWatcher is an optional Observer extension (Crude addition). BeginLoop
// runs on the event loop just before Update, EndLoop after the frame handoff
// or when the loop stops. Between them a watcher can inspect an iteration
// that is still running, for example to record what a stalled Update does.
type LoopWatcher interface {
	BeginLoop()
	SetLoopPhase(LoopPhase)
	EndLoop()
}

// LoopPhase identifies the work a watched iteration is doing now.
type LoopPhase uint32

const (
	LoopUpdate LoopPhase = iota
	LoopDispatch
	LoopView
	LoopPresent
)

// WithObserver installs a performance observer (Crude addition).
func WithObserver(observer Observer) ProgramOption {
	return func(p *Program) {
		p.observer = observer
	}
}

// timedInput carries a terminal input event with the time the reader
// produced it.
type timedInput struct {
	msg Msg
	at  time.Time
}
