package corewire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestMuxCloseWriteWaitsForBlockedWrite(t *testing.T) {
	client, server := muxPair(t)
	s, _ := client.OpenStream(context.Background(), ClassBulk)
	peer := accept(t, server)
	want := bytes.Repeat([]byte("ordered"), streamWindow)
	wrote := make(chan error, 1)
	go func() { _, err := s.Write(want); wrote <- err }()
	ms := s.(*muxStream)
	waitFor(t, func() bool { client.mu.Lock(); defer client.mu.Unlock(); return ms.credit == 0 })
	ended := make(chan error, 1)
	go func() { ended <- s.CloseWrite() }()
	waitFor(t, func() bool { client.mu.Lock(); defer client.mu.Unlock(); return ms.finPending })
	got, err := io.ReadAll(peer)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("truncated ordered write: %d/%d, %v", len(got), len(want), err)
	}
	if err := <-wrote; err != nil {
		t.Fatal(err)
	}
	if err := <-ended; err != nil {
		t.Fatal(err)
	}
}

func TestMuxResetAndCloseReportPartialWrite(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "peer"}[reset], func(t *testing.T) {
			client, server := muxPair(t)
			s, _ := client.OpenStream(context.Background(), ClassBulk)
			peer := accept(t, server)
			type result struct {
				n   int
				err error
			}
			done := make(chan result, 1)
			go func() { n, err := s.Write(make([]byte, 2*streamWindow)); done <- result{n, err} }()
			ms := s.(*muxStream)
			waitFor(t, func() bool { client.mu.Lock(); defer client.mu.Unlock(); return ms.credit == 0 })
			wantErr := ErrClosed
			if reset {
				_ = peer.Close()
				wantErr = ErrReset
			} else {
				_ = s.Close()
			}
			select {
			case got := <-done:
				if got.n != streamWindow || !errors.Is(got.err, wantErr) {
					t.Fatalf("Write = %d, %v", got.n, got.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Write remained blocked")
			}
		})
	}
}

func TestMuxFinishedUnreadStreamsRetainAdmission(t *testing.T) {
	client, server := muxPair(t)
	s, _ := client.OpenStream(context.Background(), ClassInteractive)
	peer := accept(t, server)
	_, _ = peer.Write([]byte("retained"))
	_ = peer.CloseWrite()
	_ = s.CloseWrite()
	ms := s.(*muxStream)
	waitFor(t, func() bool { client.mu.Lock(); defer client.mu.Unlock(); return ms.finRecv })
	client.mu.Lock()
	retained := client.streams[ms.id] == ms && client.opened == 1
	client.mu.Unlock()
	if !retained {
		t.Fatal("unread data released its stream admission slot")
	}
	got, err := io.ReadAll(s)
	if err != nil || string(got) != "retained" {
		t.Fatalf("read = %q, %v", got, err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.streams) != 0 {
		t.Fatal("drained stream not released")
	}
}

func bareMux() *Mux {
	m := &Mux{streams: make(map[uint64]*muxStream), accept: make(chan *muxStream, acceptBacklog), done: make(chan struct{}), epoch: time.Now(), nextID: 2}
	m.wake.L = &m.mu
	return m
}

func TestMuxSchedulerFairnessAndPriority(t *testing.T) {
	m := bareMux()
	for c := ClassControl; c < classCount; c++ {
		s := m.newStream(uint64(c+1), c)
		s.out = []byte{1}
		m.schedule(s)
	}
	counts := [classCount]int{}
	for range 15 {
		s := m.nextReady()
		if s == nil {
			t.Fatal("ready stream starved")
		}
		counts[s.class]++
		m.schedule(s)
	}
	if counts != [classCount]int{8, 4, 2, 1} {
		t.Fatalf("class service = %v", counts)
	}
	// Reset exhausted round and prove arriving input overtakes saturated bulk.
	m.budget = [classCount]int{8, 4, 2, 1}
	for m.ready[ClassControl].head != nil {
		m.unschedule(m.ready[ClassControl].head)
	}
	if got := m.nextReady(); got.class != ClassInput {
		t.Fatalf("selected class %d", got.class)
	}
}

func TestMuxControlFloodIsBounded(t *testing.T) {
	m := bareMux()
	for range controlMax {
		m.queueControl(0, framePong, make([]byte, 8))
	}
	if len(m.control) > controlMax || !errors.Is(m.err, ErrProtocol) {
		t.Fatalf("control backlog %d, %v", len(m.control), m.err)
	}
}

func TestMuxRejectsShortPing(t *testing.T) {
	m := bareMux()
	err := m.readFrame(bufio.NewReader(bytes.NewReader([]byte{0, framePing, 0})), make([]byte, frameMax))
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("short ping: %v", err)
	}
}

type terminalReader struct {
	b     []byte
	err   error
	reads int
}

func (r *terminalReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, io.EOF
	}
	return copy(p, r.b), r.err
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) { return w.Buffer.Write(p[:min(len(p), 2)]) }

func TestMessageRetainsTerminalReadError(t *testing.T) {
	sentinel := errors.New("connection reset")
	source := &terminalReader{b: []byte{1, 'a', 2, 'b'}, err: sentinel}
	r := NewReader(source, 0)
	if got, err := r.Next(); string(got) != "a" || err != nil {
		t.Fatalf("first: %q, %v", got, err)
	}
	if _, err := r.Next(); !errors.Is(err, sentinel) {
		t.Fatalf("lost terminal read error: %v", err)
	}
	if source.reads != 1 {
		t.Fatalf("read past terminal error: %d", source.reads)
	}
}

func TestMessageCompletesShortWrites(t *testing.T) {
	w := new(shortWriter)
	if err := WriteMessage(w, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	got, err := NewReader(&w.Buffer, 0).Next()
	if string(got) != "payload" || err != nil {
		t.Fatalf("message truncated: %q, %v", got, err)
	}
}

// The parser is exercised synchronously: no fuzz input creates goroutines or
// blocks on a live carrier. Credit, admission, reset and FIN share this path.
func FuzzMuxStateMachine(f *testing.F) {
	f.Add([]byte{1, frameOpen, 1, byte(ClassBulk), 1, frameData, 1, 'x', 1, frameFin, 0})
	f.Add([]byte{1, frameOpen, 1, 0, 1, frameWindow, 1, 1})
	f.Add([]byte{1, frameOpen, 1, 0, 1, frameReset, 0, 1, frameData, 1, 'x'})
	f.Fuzz(func(t *testing.T, data []byte) {
		m := bareMux()
		r := bufio.NewReader(bytes.NewReader(data))
		scratch := make([]byte, frameMax)
		for range min(len(data)+1, 4096) {
			if err := m.readFrame(r, scratch); err != nil || m.closed {
				break
			}
			for _, s := range m.streams {
				if s.credit < 0 || s.credit > streamWindow || len(s.recv)-s.recvOff+s.unacked > streamWindow {
					t.Fatal("credit invariant")
				}
			}
			if len(m.control) > controlMax || len(m.streams) > maxStreams {
				t.Fatal("resource invariant")
			}
		}
	})
}

func BenchmarkMuxScheduler(b *testing.B) {
	m := bareMux()
	s := m.newStream(1, ClassBulk)
	s.out = []byte{1}
	m.schedule(s)
	b.ReportAllocs()
	for b.Loop() {
		s := m.nextReady()
		m.schedule(s)
	}
}

func BenchmarkMuxInputUnderBulk(b *testing.B) {
	client, server := muxPair(b)
	bulk, _ := client.OpenStream(context.Background(), ClassBulk)
	bulkPeer := accept(b, server)
	go func() { _, _ = io.Copy(io.Discard, bulkPeer) }()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		data := make([]byte, 1<<20)
		for {
			if _, err := bulk.Write(data); err != nil {
				return
			}
		}
	}()
	input, _ := client.OpenStream(context.Background(), ClassInput)
	peer := accept(b, server)
	go func() { _, _ = io.Copy(peer, peer) }()
	msg := []byte{'q'}
	reply := make([]byte, 1)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := input.Write(msg); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(input, reply); err != nil {
			b.Fatal(err)
		}
	}
	_ = bulk.Close()
	wg.Wait()
}

// Ensure the data+terminal-error regression is seeded independently of mux.
func FuzzMessageFragmentation(f *testing.F) {
	f.Add([]byte{3, 'a', 'b', 'c', 0}, uint8(1))
	f.Add(binary.AppendUvarint(nil, MaxMessage+1), uint8(7))
	f.Fuzz(func(t *testing.T, data []byte, step uint8) {
		r := NewReader(&fragmentReader{b: data, step: 1 + int(step)}, 1<<16)
		for range min(len(data)+1, 4096) {
			body, err := r.Next()
			if err != nil {
				return
			}
			if len(body) > 1<<16 {
				t.Fatal("message limit")
			}
		}
	})
}

type fragmentReader struct {
	b    []byte
	step int
}

func (r *fragmentReader) Read(p []byte) (int, error) {
	n := copy(p, r.b[:min(len(r.b), r.step)])
	r.b = r.b[n:]
	if len(r.b) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func TestMuxClosingRefusesNewStreams(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	m := NewMux(a, CarrierLocal, true)
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
	if _, err := m.OpenStream(context.Background(), ClassInput); !errors.Is(err, ErrClosed) {
		t.Fatalf("open during close: %v", err)
	}
	_ = m.Close()
}

func TestMuxPendingOpenCannotSendDataEarly(t *testing.T) {
	m := bareMux()
	s := m.newStream(1, ClassInput)
	s.openPending, s.out = true, []byte("early")
	m.schedule(s)
	if m.hasReady() {
		t.Fatal("data scheduled before its OPEN control frame")
	}
	s.openPending = false
	m.schedule(s)
	if m.nextReady() != s {
		t.Fatal("data did not become ready after OPEN")
	}
}
