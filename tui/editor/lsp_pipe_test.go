package editor

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// closeableBlockingSink is an io.Writer that never accepts bytes until
// Close — it simulates a wedged language server whose stdin pipe is full,
// unblocking on Close (as the processCloser does by closing the pipe).
type closeableBlockingSink struct {
	mu     sync.Mutex
	closed bool
}

func (s *closeableBlockingSink) Write(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		s.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
}

func (s *closeableBlockingSink) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// fakeSink collects writes in FIFO order for assertions.
type fakeSink struct {
	mu     sync.Mutex
	chunks [][]byte
}

func (s *fakeSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf := make([]byte, len(p))
	copy(buf, p)
	s.chunks = append(s.chunks, buf)
	return len(p), nil
}

func (s *fakeSink) all() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, c := range s.chunks {
		b.Write(c)
	}
	return b.String()
}

// TestAsyncPipeWriter_WriteNeverBlocksOnSlowSink is the core contract: with a
// server that never reads, Write must return immediately (enqueue only) and
// never touch the pipe on the caller's goroutine.
func TestAsyncPipeWriter_WriteNeverBlocksOnSlowSink(t *testing.T) {
	sink := &closeableBlockingSink{}
	w := newAsyncPipeWriter(sink)
	defer func() { _ = w.Close() }()
	defer sink.close()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			if _, err := w.Write([]byte("payload")); err != nil {
				t.Errorf("write %d: %v", i, err)
				break
			}
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on a stalled pipe — this is the UI-hang bug")
	}
}

// TestAsyncPipeWriter_FIFOOrdering verifies writes reach the sink in order.
func TestAsyncPipeWriter_FIFOOrdering(t *testing.T) {
	sink := &fakeSink{}
	w := newAsyncPipeWriter(sink)
	defer func() { _ = w.Close() }()

	for i := 0; i < 50; i++ {
		if _, err := w.Write([]byte{byte('a' + i%26)}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if sink.all() == "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwx" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ordering not complete: %q", sink.all())
		}
		time.Sleep(time.Millisecond)
	}
}

// TestAsyncPipeWriter_CopySemantics verifies Write copies the caller's
// buffer (jsonrpc2 reuses its bufio buffer between messages).
func TestAsyncPipeWriter_CopySemantics(t *testing.T) {
	sink := &fakeSink{}
	w := newAsyncPipeWriter(sink)
	defer func() { _ = w.Close() }()

	buf := []byte("first")
	if _, err := w.Write(buf); err != nil {
		t.Fatal(err)
	}
	copy(buf, []byte("XXXXX"))
	buf2 := []byte("second")
	if _, err := w.Write(buf2); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		got := sink.all()
		if got == "firstsecond" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %q, want firstsecond", got)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestAsyncPipeWriter_QueueCapFailsFast verifies the bounded queue: when the
// server never drains, writes beyond the cap return a fatal error instead of
// blocking, and subsequent writes fail fast.
func TestAsyncPipeWriter_QueueCapFailsFast(t *testing.T) {
	sink := &closeableBlockingSink{}
	w := newAsyncPipeWriter(sink)
	defer func() { _ = w.Close() }()
	defer sink.close()

	// Fill the queue past the cap.
	chunk := make([]byte, 1<<20)
	written := 0
	for {
		if _, err := w.Write(chunk); err != nil {
			if !errors.Is(err, errLSPWriteQueueFull) {
				t.Fatalf("expected queue-full error, got %v", err)
			}
			break
		}
		written++
		if written > lspWriteQueueCap/(1<<20)+2 {
			t.Fatalf("queue accepted %d MiB without a cap", written)
		}
	}

	// Subsequent writes fail fast (no block).
	done := make(chan struct{})
	go func() {
		if _, err := w.Write([]byte("x")); err == nil {
			t.Error("write after fatal should error")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("write after queue-cap fatal blocked")
	}

	// Done must close: the connection is dead and can be reaped.
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not close after fatal queue cap")
	}
}

// TestAsyncPipeWriter_SinkErrorPropagates verifies a pipe write error ends
// the writer, fails pending+future writes, and closes Done.
func TestAsyncPipeWriter_SinkErrorPropagates(t *testing.T) {
	errSink := &errorSink{err: errors.New("broken pipe")}
	w := newAsyncPipeWriter(errSink)
	if _, err := w.Write([]byte("boom")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not close after sink error")
	}
	if _, err := w.Write([]byte("after")); err == nil {
		t.Fatal("write after sink error should fail fast")
	}
}

type errorSink struct {
	err error
}

func (s *errorSink) Write(p []byte) (int, error) { return 0, s.err }

// TestLSPPipeMonitor_ReadEOFClosesDone verifies a dead server is observed
// through the read side even with no pending writes. The jsonrpc2 read loop
// continuously drains the reader, so the test does the same.
func TestLSPPipeMonitor_ReadEOFClosesDone(t *testing.T) {
	reader, writer := io.Pipe()
	_, _, mon := wrapLSPPipe(reader, writer)
	defer func() { _ = mon.Close() }()

	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := mon.reader.Read(buf); err != nil {
				return
			}
		}
	}()

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-mon.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not close after read EOF (server died)")
	}
}
