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
	fault       func(kind FaultKind, sent int) frame
	terminal    []frame
}

// sendStream owns flushing, the test gate, and the placement of test faults;
// the wire owns the envelope, terminal marker, and format of each frame.
func (s *Server) sendStream(w http.ResponseWriter, r *http.Request, turn Turn, codec streamCodec, events iter.Seq[frame]) {
	w.Header().Set("Content-Type", codec.contentType)
	w.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil || !turn.gate.await(r.Context(), s.done) {
		return
	}
	write := func(event frame) bool {
		return codec.write(w, event) == nil && controller.Flush() == nil
	}
	f := turn.fault
	inject := func(sent int) bool {
		if f.after != sent {
			return false
		}
		switch f.mode {
		case faultStream:
			write(codec.fault(f.kind, sent))
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
	if inject(sent) {
		return
	}
	for event := range events {
		if !write(event) {
			return
		}
		sent++
		if inject(sent) {
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
