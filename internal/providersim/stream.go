package providersim

import (
	"io"
	"iter"
	"net/http"
)

type frame struct {
	event string
	data  []byte
}

type streamCodec struct {
	contentType string
	write       func(io.Writer, frame) error
	encodeFault func(f fault, sent int) frame
	// faultFinishes means the fault is a finish choice followed by the
	// documented terminator, not an error that interrupts framing.
	faultFinishes bool
	terminal      []frame
}

// awaitBody gates an entire non-stream response, regardless of event position.
func (s *Server) awaitBody(r *http.Request, t Turn) bool {
	return t.gate.await(r.Context(), s.done)
}

// sendStream owns flushing, the test gate, and the placement of test faults;
// the wire owns the envelope, terminal marker, and format of each frame.
func (s *Server) sendStream(w http.ResponseWriter, r *http.Request, turn Turn, codec streamCodec, events iter.Seq[frame]) {
	w.Header().Set("Content-Type", codec.contentType)
	w.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}
	write := func(event frame) bool {
		return codec.write(w, event) == nil && controller.Flush() == nil
	}
	wait := func(sent int) bool {
		return sent != turn.gateAfter || s.awaitBody(r, turn)
	}
	f := turn.fault
	inject := func(sent int) bool {
		if f.after != sent {
			return false
		}
		switch f.mode {
		case faultStream:
			if write(codec.encodeFault(f, sent)) && codec.faultFinishes {
				for _, terminal := range codec.terminal {
					if !write(terminal) {
						break
					}
				}
			}
			return true
		case faultCut:
			// net/http aborts the connection without writing the HTTP chunked
			// terminator; the client sees a transport error, not a clean EOF.
			panic(http.ErrAbortHandler)
		default:
			return false
		}
	}
	sent := 0
	if !wait(sent) || inject(sent) {
		return
	}
	for event := range events {
		if !write(event) {
			return
		}
		sent++
		if !wait(sent) || inject(sent) {
			return
		}
	}
	if f.mode == faultStream || f.mode == faultCut {
		s.report("simulate %s: fault placed after %d events, but turn emitted %d", r.URL.Path, f.after, sent)
		return
	}
	for _, terminal := range codec.terminal {
		if !write(terminal) {
			return
		}
		sent++
		if !wait(sent) {
			return
		}
	}
	if turn.gate != nil && (turn.gateAfter < 0 || turn.gateAfter > sent) {
		s.report("simulate %s: gate placed after %d events, but turn emitted %d", r.URL.Path, turn.gateAfter, sent)
	}
}

// writeSSE writes one JSON frame or a literal, single-line stream marker.
func writeSSE(w io.Writer, frame frame) error {
	if frame.event != "" {
		if _, err := io.WriteString(w, "event: "+frame.event+"\n"); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "data: "); err != nil {
		return err
	}
	if _, err := w.Write(frame.data); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n\n")
	return err
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// A disconnected test client does not turn a valid fixture into a test error.
	_, _ = w.Write(jsonBytes(value))
}
