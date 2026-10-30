package lspclient

import (
	"errors"
	"io"
	"sync"
	"time"
)

// This file decouples LSP subprocess pipe I/O from caller goroutines.
//
// Background: jsonrpc2's buffered stream performs a synchronous,
// context-unaware bufio flush on every WriteObject (see
// third_party/powernap). When the language server's stdin pipe is full,
// e.g. while the server chews through the project-wide didOpen sync, or
// when the server is wedged, that flush blocks the calling goroutine
// indefinitely. Several LSP operations (CloseFile, NotifyWatchedFilesChanged,
// SyncFileFromDisk) run on Bubble Tea's update goroutine, so a blocked flush
// previously froze the entire UI, and the connection's write mutex convoyed
// every other LSP operation behind the stalled write.
//
// The fix has two parts:
//
//  1. asyncPipeWriter replaces the raw stdin pipe as the stream's writer.
//     Write only enqueues a copy of the payload (O(1), microseconds) and
//     returns; a single dedicated writer goroutine performs the actual pipe
//     writes in FIFO order. Server back-pressure can therefore only stall
//     that goroutine, never the caller.
//
//  2. lspPipeMonitor observes BOTH stream sides. The reader side reports
//     EOF/error when the server process dies (even with no write pending),
//     and the writer side reports fatal conditions (queue cap, pipe error,
//     graceful close). The combined Done channel lets the LSP manager reap
//     and restart dead servers.
//
// Bounded queue semantics: the queue is byte-bounded. When the server
// stops draining entirely, the queue hits its cap, the writer records a
// fatal error, drops all queued bytes, and every subsequent Write fails
// fast. The jsonrpc2 connection then fails its operations cleanly (it never
// retries a partial write), so no protocol corruption can occur; the LSP
// manager reaps the client on Done and restarts it on the next file open.

const (
	// lspWriteQueueCap bounds queued (not yet written) LSP traffic. A
	// single message can legitimately be large (whole-file didChange on
	// save), so the cap is generous; it is only reachable when the server
	// has stopped reading stdin entirely.
	lspWriteQueueCap = 64 << 20
)

var (
	errLSPWriteQueueFull = errors.New("lsp: write queue full; language server stalled")
	errLSPWriterClosed   = errors.New("lsp: writer closed")
	errLSPWriterUnjoined = errors.New("lsp: asynchronous pipe writer did not stop before its shutdown deadline")
)

// asyncPipeWriter is a bounded, serialized, non-blocking writer facade over
// an LSP server's stdin pipe. See the file comment for the failure modes it
// eliminates.
type asyncPipeWriter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	sink   io.Writer
	queue  [][]byte
	queued int

	// accepting is false once Close() or a fatal condition occurs; Write
	// then fails fast with fatal (or errLSPWriterClosed).
	accepting bool
	fatal     error

	done       chan struct{}
	endOnce    sync.Once
	workerDone chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

// newAsyncPipeWriter starts a writer goroutine over sink.
func newAsyncPipeWriter(sink io.Writer) *asyncPipeWriter {
	w := &asyncPipeWriter{
		sink:       sink,
		accepting:  true,
		done:       make(chan struct{}),
		workerDone: make(chan struct{}),
	}
	w.cond = sync.NewCond(&w.mu)
	go w.run()
	return w
}

// Done is closed when the writer stops for any reason: a fatal condition
// (queue cap, sink/pipe error) closes it immediately (even if a sink write
// is still blocked in flight), while a graceful Close closes it once the
// writer drains and exits.
func (w *asyncPipeWriter) Done() <-chan struct{} { return w.done }

func (w *asyncPipeWriter) markEnded() { w.endOnce.Do(func() { close(w.done) }) }

// Write enqueues a copy of p and returns immediately. It performs no pipe
// I/O on the caller's goroutine. It returns len(p) unless the writer is
// closed or the bounded queue is full (fatal).
func (w *asyncPipeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if !w.accepting {
		err := w.fatal
		if err == nil {
			err = errLSPWriterClosed
		}
		w.mu.Unlock()
		return 0, err
	}
	if len(p) > lspWriteQueueCap-w.queued {
		// The server has stopped draining. Fail fast instead of blocking
		// the caller: drop everything and let the connection die cleanly.
		w.fatal = errLSPWriteQueueFull
		w.accepting = false
		w.cond.Broadcast()
		w.mu.Unlock()
		w.markEnded()
		return 0, errLSPWriteQueueFull
	}
	buf := make([]byte, len(p))
	copy(buf, p)
	w.queue = append(w.queue, buf)
	w.queued += len(buf)
	w.cond.Signal()
	w.mu.Unlock()
	return len(p), nil
}

// Close stops accepting writes and lets the writer drain the remaining
// queue. It reports the join outcome, not an earlier protocol write failure:
// Write/Done already expose that failure, and a fully joined broken pipe is
// not an unverified subprocess retirement. The process owner closes actual
// pipes first; a sink which still cannot be joined is an explicit failure.
// Repeated closes share one bounded wait, with no detached waiter goroutines.
func (w *asyncPipeWriter) Close() error {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.accepting = false
		w.cond.Broadcast()
		w.mu.Unlock()
		timer := time.NewTimer(6 * time.Second)
		defer timer.Stop()
		select {
		case <-w.workerDone:
		case <-timer.C:
			w.closeErr = errLSPWriterUnjoined
		}
	})
	return w.closeErr
}

// run drains the queue in FIFO order until it is empty and closed, or until
// a sink write fails. It is the ONLY goroutine that touches the pipe.
func (w *asyncPipeWriter) run() {
	defer close(w.workerDone)
	defer w.markEnded()
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && w.accepting {
			w.cond.Wait()
		}
		if len(w.queue) == 0 {
			// Closed with nothing left to send.
			w.mu.Unlock()
			return
		}
		if !w.accepting && w.fatal != nil {
			// Fatal condition: drop everything, fail fast.
			w.queue = nil
			w.queued = 0
			w.mu.Unlock()
			return
		}
		buf := w.queue[0]
		w.queue[0] = nil
		w.queue = w.queue[1:]
		w.queued -= len(buf)
		w.mu.Unlock()

		n, err := w.sink.Write(buf)
		if err == nil && n != len(buf) {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.mu.Lock()
			if w.fatal == nil {
				w.fatal = err
			}
			w.accepting = false
			w.queue = nil
			w.queued = 0
			w.cond.Broadcast()
			w.mu.Unlock()
			return
		}
	}
}

// lspPipeMonitor wraps both sides of an LSP subprocess stream and exposes a
// single Done channel closed when EITHER side ends: the server died (read
// EOF/error) or the writer failed/closed (queue cap, pipe error, close).
type lspPipeMonitor struct {
	reader io.Reader
	writer *asyncPipeWriter
	done   chan struct{}
	once   sync.Once
}

// wrapLSPPipe wraps the given reader/writer and returns the wrapped sides
// plus a monitor whose Done channel signals connection death.
func wrapLSPPipe(reader io.Reader, writer io.Writer) (io.Reader, io.Writer, *lspPipeMonitor) {
	m := &lspPipeMonitor{
		done: make(chan struct{}),
	}
	m.writer = newAsyncPipeWriter(writer)
	m.reader = &lspReadMonitor{reader: reader, onEnd: m.markEnded}
	// Writer death (fatal, pipe error, graceful close) also ends the
	// monitored connection.
	go func() {
		<-m.writer.Done()
		m.markEnded()
	}()
	return m.reader, m.writer, m
}

// Done is closed when the monitored connection ends for any reason.
func (m *lspPipeMonitor) Done() <-chan struct{} {
	if m == nil {
		return nil
	}
	return m.done
}

func (m *lspPipeMonitor) markEnded() {
	m.once.Do(func() { close(m.done) })
}

// Close stops the writer side. Reader-side resources belong to the stream
// closer (processCloser).
func (m *lspPipeMonitor) Close() error {
	if m == nil || m.writer == nil {
		return nil
	}
	return m.writer.Close()
}

// lspReadMonitor closes the connection's Done channel when the server's
// stdout reaches EOF or errors, i.e. the server process died or the pipe
// broke. Without this, a dead server with no pending write would never be
// observed.
type lspReadMonitor struct {
	reader io.Reader
	onEnd  func()
	once   sync.Once
}

func (r *lspReadMonitor) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil {
		r.once.Do(r.onEnd)
	}
	return n, err
}
