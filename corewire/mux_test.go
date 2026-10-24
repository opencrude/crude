package corewire

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func muxPair(t testing.TB) (*Mux, *Mux) {
	t.Helper()
	a, b := net.Pipe()
	client, server := NewMux(a, CarrierLocal, true), NewMux(b, CarrierLocal, false)
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return client, server
}

func accept(t testing.TB, m Conn) Stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := m.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	return s
}

func TestMuxEchoesAndEndsStreams(t *testing.T) {
	client, server := muxPair(t)
	s, err := client.OpenStream(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	peer := accept(t, server)
	if peer.Class() != ClassInteractive {
		t.Fatalf("class %d", peer.Class())
	}
	got, err := io.ReadAll(peer)
	if err != nil || string(got) != "hello" {
		t.Fatalf("read %q, %v", got, err)
	}
	if _, err := peer.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	_ = peer.CloseWrite()
	got, err = io.ReadAll(s)
	if err != nil || string(got) != "world" {
		t.Fatalf("reply %q, %v", got, err)
	}
	waitFor(t, func() bool {
		client.mu.Lock()
		defer client.mu.Unlock()
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(client.streams) == 0 && len(server.streams) == 0
	})
}

// A transfer far larger than one stream's credit arrives intact; the writer
// must wait for credit rather than overrun the receiver.
func TestMuxLargeTransferRespectsCredit(t *testing.T) {
	client, server := muxPair(t)
	data := make([]byte, 8<<20)
	_, _ = rand.Read(data)
	s, _ := client.OpenStream(context.Background(), ClassBulk)
	errs := make(chan error, 1)
	go func() {
		_, err := s.Write(data)
		if err == nil {
			err = s.CloseWrite()
		}
		errs <- err
	}()
	peer := accept(t, server)
	got, err := io.ReadAll(peer)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatal("transfer corrupted")
	}
}

// slowConn delivers writes at a bounded rate, like an SSH channel.
type slowConn struct {
	net.Conn
	perWrite time.Duration
}

func (c slowConn) Write(b []byte) (int, error) {
	time.Sleep(c.perWrite)
	return c.Conn.Write(b)
}

// Input written while a bulk transfer saturates the carrier overtakes the
// queued bulk frames.
func TestMuxInputOvertakesBulk(t *testing.T) {
	a, b := net.Pipe()
	client := NewMux(slowConn{a, 2 * time.Millisecond}, CarrierSSH, true)
	server := NewMux(b, CarrierSSH, false)
	defer client.Close()
	defer server.Close()
	bulk, _ := client.OpenStream(context.Background(), ClassBulk)
	go func() { _, _ = bulk.Write(make([]byte, 4<<20)) }()
	bulkPeer := accept(t, server)
	go func() { _, _ = io.Copy(io.Discard, bulkPeer) }()
	time.Sleep(20 * time.Millisecond)
	input, _ := client.OpenStream(context.Background(), ClassInput)
	start := time.Now()
	if _, err := input.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	peer := accept(t, server)
	var key [1]byte
	if _, err := io.ReadFull(peer, key[:]); err != nil {
		t.Fatal(err)
	}
	// Bulk alone needs ~256 KiB of credit / 32 KiB per 2 ms write ≈ 16 ms per
	// window; input must arrive within a few writes, not after the transfer.
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("input waited %v behind bulk", elapsed)
	}
}

func TestMuxResetReachesBothDirections(t *testing.T) {
	client, server := muxPair(t)
	s, _ := client.OpenStream(context.Background(), ClassInteractive)
	_, _ = s.Write([]byte("x"))
	peer := accept(t, server)
	var one [1]byte
	_, _ = io.ReadFull(peer, one[:])
	blocked := make(chan error, 1)
	go func() { _, err := peer.Read(one[:]); blocked <- err }()
	_ = s.Close()
	select {
	case err := <-blocked:
		if !errors.Is(err, ErrReset) {
			t.Fatalf("read after reset: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reset did not wake the peer")
	}
	if _, err := s.Write([]byte("y")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
}

func TestMuxManyConcurrentStreams(t *testing.T) {
	client, server := muxPair(t)
	go func() {
		for {
			s, err := server.AcceptStream(context.Background())
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(s, s); _ = s.CloseWrite() }()
		}
	}()
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := client.OpenStream(context.Background(), Class(i%int(classCount)))
			if err != nil {
				t.Error(err)
				return
			}
			want := bytes.Repeat([]byte{byte(i)}, 40<<10)
			go func() { _, _ = s.Write(want); _ = s.CloseWrite() }()
			got, err := io.ReadAll(s)
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("stream %d: %d bytes, %v", i, len(got), err)
			}
		}()
	}
	wg.Wait()
}

func TestMuxPeerCloseFailsStreams(t *testing.T) {
	client, server := muxPair(t)
	s, _ := client.OpenStream(context.Background(), ClassInteractive)
	_, _ = s.Write([]byte("x"))
	accept(t, server)
	_ = server.Close()
	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client did not observe the closed carrier")
	}
	if _, err := s.Read(make([]byte, 1)); !errors.Is(err, ErrClosed) {
		t.Fatalf("read on dead carrier: %v", err)
	}
}

func TestMuxDetectsSilentPeer(t *testing.T) {
	oldPing, oldLive := pingInterval, livenessLimit
	pingInterval, livenessLimit = 10*time.Millisecond, 50*time.Millisecond
	defer func() { pingInterval, livenessLimit = oldPing, oldLive }()
	a, b := net.Pipe()
	defer b.Close()
	go func() { _, _ = io.Copy(io.Discard, b) }() // reads, never answers
	m := NewMux(a, CarrierSSH, true)
	defer m.Close()
	select {
	case <-m.Done():
		if !errors.Is(m.Err(), ErrTimeout) {
			t.Fatalf("err %v", m.Err())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("silent peer not detected")
	}
}

func TestMuxMeasuresRTT(t *testing.T) {
	client, _ := muxPair(t)
	waitFor(t, func() bool { return client.RTT() > 0 })
}

func TestMuxRejectsCreditOverrun(t *testing.T) {
	a, b := net.Pipe()
	m := NewMux(a, CarrierLocal, false)
	defer m.Close()
	go func() { _, _ = io.Copy(io.Discard, b) }()
	var frames []byte
	frames = appendFrameHeader(frames, 1, frameOpen, 1)
	frames = append(frames, byte(ClassBulk))
	chunk := make([]byte, frameMax)
	for range streamWindow/frameMax + 1 {
		frames = appendFrameHeader(frames, 1, frameData, len(chunk))
		frames = append(frames, chunk...)
	}
	go func() { _, _ = b.Write(frames) }()
	select {
	case <-m.Done():
		if !errors.Is(m.Err(), ErrProtocol) {
			t.Fatalf("err %v", m.Err())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("credit overrun accepted")
	}
}

func waitFor(t testing.TB, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// FuzzMuxFrames feeds arbitrary bytes to the frame reader: it must reject or
// accept them without panicking, and buffer no more than its credit allows.
func FuzzMuxFrames(f *testing.F) {
	var seed []byte
	seed = appendFrameHeader(seed, 1, frameOpen, 1)
	seed = append(seed, byte(ClassInput))
	seed = appendFrameHeader(seed, 1, frameData, 3)
	seed = append(seed, "abc"...)
	seed = appendFrameHeader(seed, 1, frameWindow, 2)
	seed = append(seed, 0x80, 0x01)
	seed = appendFrameHeader(seed, 1, frameFin, 0)
	seed = appendFrameHeader(seed, 0, framePing, 8)
	seed = append(seed, make([]byte, 8)...)
	f.Add(seed)
	f.Add([]byte{0, 9, 0})
	f.Add([]byte{1, 2, 0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Fuzz(func(t *testing.T, data []byte) {
		a, b := net.Pipe()
		m := NewMux(a, CarrierLocal, false)
		go func() { _, _ = io.Copy(io.Discard, b) }()
		go func() {
			for {
				s, err := m.AcceptStream(context.Background())
				if err != nil {
					return
				}
				go func() { _, _ = io.Copy(io.Discard, s) }()
			}
		}()
		_, _ = b.Write(data)
		_ = b.Close()
		<-m.Done()
		_ = m.Close()
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, s := range m.streams {
			if len(s.recv)-s.recvOff > streamWindow {
				t.Fatalf("stream %d buffered %d bytes", s.id, len(s.recv))
			}
		}
	})
}

func BenchmarkMuxSmallMessages(b *testing.B) {
	client, server := muxPair(b)
	s, _ := client.OpenStream(context.Background(), ClassInput)
	_, _ = s.Write([]byte{0})
	peer := accept(b, server)
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	msg := make([]byte, 64)
	b.ReportAllocs()
	b.SetBytes(int64(len(msg)))
	for b.Loop() {
		if _, err := s.Write(msg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMuxBulk(b *testing.B) {
	client, server := muxPair(b)
	s, _ := client.OpenStream(context.Background(), ClassBulk)
	_, _ = s.Write([]byte{0})
	peer := accept(b, server)
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	msg := make([]byte, 1<<20)
	b.ReportAllocs()
	b.SetBytes(int64(len(msg)))
	for b.Loop() {
		if _, err := s.Write(msg); err != nil {
			b.Fatal(err)
		}
	}
}
