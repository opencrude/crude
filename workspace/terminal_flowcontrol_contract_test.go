package workspace

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type flowControlTerminalIO struct {
	writeStarted chan struct{}
	releaseWrite chan struct{}
	startOnce    sync.Once
	releaseOnce  sync.Once
	writeErr     error
	sizeErr      error

	mu       sync.Mutex
	sizeCols int
	sizeRows int
}

func newFlowControlTerminalIO(block bool) *flowControlTerminalIO {
	s := &flowControlTerminalIO{writeStarted: make(chan struct{})}
	if block {
		s.releaseWrite = make(chan struct{})
	}
	return s
}

func (s *flowControlTerminalIO) Read([]byte) (int, error) { return 0, io.EOF }

func (s *flowControlTerminalIO) Write(p []byte) (int, error) {
	s.startOnce.Do(func() { close(s.writeStarted) })
	if s.releaseWrite != nil {
		<-s.releaseWrite
	}
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	return len(p), nil
}

func (s *flowControlTerminalIO) SetSize(cols, rows int) error {
	s.mu.Lock()
	s.sizeCols, s.sizeRows = cols, rows
	s.mu.Unlock()
	return s.sizeErr
}

func (s *flowControlTerminalIO) Close() error       { return nil }
func (s *flowControlTerminalIO) Wait() (int, error) { return 0, nil }
func (s *flowControlTerminalIO) Pid() int           { return 1 }
func (s *flowControlTerminalIO) unblock()           { s.releaseOnce.Do(func() { close(s.releaseWrite) }) }
func (s *flowControlTerminalIO) requestedSize() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sizeCols, s.sizeRows
}

func TestTerminalManagerQueueOverflowReturnsBackpressure(t *testing.T) {
	sink := newFlowControlTerminalIO(true)
	writer := newPtyWriter(sink)
	mgr := NewTerminalManager(t.TempDir())
	mgr.items["blocked"] = &terminalSession{
		id:     "blocked",
		alive:  true,
		io:     sink,
		writer: writer,
	}
	mgr.order = []string{"blocked"}
	t.Cleanup(func() {
		writer.stop()
		sink.unblock()
		writer.wait(time.Second)
	})

	if err := mgr.Write("blocked", []byte("in flight")); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	select {
	case <-sink.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("writer did not enter the blocking sink")
	}

	full := bytes.Repeat([]byte{'x'}, ptyWriteQueueCap)
	if err := mgr.Write("blocked", full); err != nil {
		t.Fatalf("fill queue: %v", err)
	}
	err := mgr.Write("blocked", []byte("overflow"))
	if !errors.Is(err, ErrTerminalBackpressure) {
		t.Fatalf("overflow error=%v; want ErrTerminalBackpressure", err)
	}
	writer.mu.Lock()
	queued := writer.queued
	writer.mu.Unlock()
	if queued != ptyWriteQueueCap {
		t.Fatalf("rejected write mutated queue: queued=%d want=%d", queued, ptyWriteQueueCap)
	}
}

func TestPTYWriterRetainsAsynchronousSinkError(t *testing.T) {
	sentinel := errors.New("sink failed")
	sink := newFlowControlTerminalIO(false)
	sink.writeErr = sentinel
	writer := newPtyWriter(sink)
	t.Cleanup(writer.stop)

	if n, err := writer.Write([]byte("first")); n != len("first") || err != nil {
		t.Fatalf("initial enqueue=(%d, %v)", n, err)
	}
	writer.wait(time.Second)
	if n, err := writer.Write([]byte("next")); n != 0 || !errors.Is(err, sentinel) {
		t.Fatalf("write after sink failure=(%d, %v); want retained sink error", n, err)
	}
}

func TestTerminalResizeUsesSharedMinimumAndCommitsOnlyOnSuccess(t *testing.T) {
	sentinel := errors.New("resize failed")
	sink := newFlowControlTerminalIO(false)
	sink.sizeErr = sentinel
	mgr := NewTerminalManager(t.TempDir())
	mgr.items["tiny"] = &terminalSession{
		id:    "tiny",
		cols:  40,
		rows:  10,
		alive: true,
		io:    sink,
	}
	mgr.order = []string{"tiny"}

	err := mgr.Resize("tiny", 1, 1)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Resize error=%v; want wrapped sentinel", err)
	}
	if cols, rows := sink.requestedSize(); cols != MinTerminalCols || rows != MinTerminalRows {
		t.Fatalf("SetSize=%dx%d want shared minimum %dx%d", cols, rows, MinTerminalCols, MinTerminalRows)
	}
	info, ok := mgr.Get("tiny")
	if !ok {
		t.Fatal("terminal disappeared")
	}
	if info.Cols != 40 || info.Rows != 10 {
		t.Fatalf("failed resize committed snapshot %dx%d; want 40x10", info.Cols, info.Rows)
	}

	sink.sizeErr = nil
	if err := mgr.Resize("tiny", 1, 1); err != nil {
		t.Fatalf("successful Resize: %v", err)
	}
	info, _ = mgr.Get("tiny")
	if info.Cols != MinTerminalCols || info.Rows != MinTerminalRows {
		t.Fatalf("successful resize snapshot=%dx%d want=%dx%d", info.Cols, info.Rows, MinTerminalCols, MinTerminalRows)
	}
}
