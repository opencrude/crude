package tea

import "time"

// LoopSample describes one event-loop iteration: the message, how long input
// waited for the loop (input only), and the Update, View and frame handoff
// costs. All of it runs on the event-loop goroutine.
type LoopSample struct {
	Msg     Msg
	Input   bool
	Wait    time.Duration
	Update  time.Duration
	View    time.Duration
	Present time.Duration
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
