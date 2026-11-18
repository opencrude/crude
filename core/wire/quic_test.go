package corewire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestQUICListenerRetirementAndSynchronousClose(t *testing.T) {
	id, err := LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := ListenQUIC("127.0.0.1:0", id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	address := l.Addr().String()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := DialQUIC(ctx, address, id.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := l.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := l.StopAccept(); err != nil {
		t.Fatal(err)
	}
	go func() {
		s, err := server.AcceptStream(ctx)
		if err != nil {
			return
		}
		_, _ = io.Copy(s, s)
		_ = s.CloseWrite()
	}()
	s, err := client.OpenStream(ctx, ClassInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Write([]byte("still alive")); err != nil {
		t.Fatal(err)
	}
	_ = s.CloseWrite()
	got, err := io.ReadAll(s)
	if err != nil || string(got) != "still alive" {
		t.Fatalf("retired listener killed carrier: %q %v", got, err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatalf("closed listener retained UDP socket: %v", err)
	}
	_ = packet.Close()
}

func TestQUICListenerPreparedAcceptGenerations(t *testing.T) {
	id, err := LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := ListenQUIC("127.0.0.1:0", id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := first.PrepareAccept(); err == nil {
		t.Fatal("prepared a second listener without retiring the first")
	}
	client, err := DialQUIC(ctx, first.Addr().String(), id.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := first.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if err := first.StopAccept(); err != nil {
		t.Fatal(err)
	}
	current := first
	for range 4 {
		next, err := current.PrepareAccept()
		if err != nil {
			t.Fatal(err)
		}
		if next == current || next.l == current.l || next.r != first.r || next.Addr().String() != first.Addr().String() {
			t.Fatal("reactivation changed transport identity or reused accept generation")
		}
		// A stale worker calling Accept only sees its own closed generation.
		if conn, err := current.Accept(ctx); conn != nil || err == nil {
			t.Fatalf("retired generation accepted a new carrier: %v %v", conn, err)
		}
		queued, err := DialQUIC(ctx, next.Addr().String(), id.Fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		if err := next.StopAccept(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-queued.Done():
		case <-ctx.Done():
			t.Fatal("aborted generation retained an unaccepted carrier")
		}
		_ = queued.Close()
		// Retiring a stale generation must not stop its replacement.
		active, err := next.PrepareAccept()
		if err != nil {
			t.Fatal(err)
		}
		if err := current.StopAccept(); err != nil {
			t.Fatal(err)
		}
		fresh, err := DialQUIC(ctx, active.Addr().String(), id.Fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := active.Accept(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = fresh.Close()
		_ = accepted.Close()
		if err := active.StopAccept(); err != nil {
			t.Fatal(err)
		}
		// Each rollback and commit must leave the original carrier usable.
		echo := make(chan error, 1)
		go func() {
			s, err := server.AcceptStream(ctx)
			if err == nil {
				_, err = io.Copy(s, s)
				if err == nil {
					err = s.CloseWrite()
				}
			}
			echo <- err
		}()
		s, err := client.OpenStream(ctx, ClassInput)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Write([]byte("retained")); err != nil {
			t.Fatal(err)
		}
		_ = s.CloseWrite()
		if got, err := io.ReadAll(s); err != nil || string(got) != "retained" {
			t.Fatalf("reactivation killed original stream: %q %v", got, err)
		}
		if err := <-echo; err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		current = active
	}
	// Final Close through any generation owns the one shared resource.
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.PrepareAccept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed resource reactivated: %v", err)
	}
	packet, err := net.ListenPacket("udp", first.Addr().String())
	if err != nil {
		t.Fatalf("Close retained transport socket: %v", err)
	}
	_ = packet.Close()
}

func TestQUICCarrierPinsTheCoreCertificate(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := ListenQUIC("127.0.0.1:0", id)
	if err != nil {
		t.Skipf("UDP unavailable: %v", err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept(context.Background())
			if err != nil {
				return
			}
			go func() {
				s, err := c.AcceptStream(context.Background())
				if err != nil {
					return
				}
				_, _ = io.Copy(s, s)
				_ = s.CloseWrite()
			}()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := DialQUIC(ctx, l.Addr().String(), id.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.OpenStream(ctx, ClassInput)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Write([]byte("ping"))
	_ = s.CloseWrite()
	got, err := io.ReadAll(s)
	if err != nil || string(got) != "ping" {
		t.Fatalf("echo %q, %v", got, err)
	}

	other, _ := LoadIdentity(t.TempDir())
	if _, err := DialQUIC(ctx, l.Addr().String(), other.Fingerprint); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("wrong pin accepted: %v", err)
	}
	again, err := LoadIdentity(dir)
	if err != nil || again.Fingerprint != id.Fingerprint {
		t.Fatalf("identity not persisted: %v", err)
	}
}

func quicPair(t testing.TB) (*quicConn, *quicConn) {
	t.Helper()
	id, err := LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := ListenQUIC("127.0.0.1:0", id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := DialQUIC(ctx, l.Addr().String(), id.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	s, err := l.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return c.(*quicConn), s.(*quicConn)
}

func TestQUICClassOnlyStreamEndsCleanly(t *testing.T) {
	c, s := quicPair(t)
	stream, err := c.OpenStream(context.Background(), ClassInput)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.CloseWrite()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	peer, err := s.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(peer)
	if len(got) != 0 || err != nil {
		t.Fatalf("empty stream: %q, %v", got, err)
	}
}

func TestQUICAcceptCancellationDuringPreface(t *testing.T) {
	c, s := quicPair(t)
	raw, err := c.c.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Consume the stream's first byte at the QUIC layer to expose a visible
	// stream with no bytes available to the carrier preface reader.
	_, _ = raw.Write([]byte{byte(ClassInput)})
	accepted, err := s.c.AcceptStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var class [1]byte
	_, _ = io.ReadFull(accepted, class[:])
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = readQUICClass(ctx, accepted)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("preface cancellation: %v", err)
	}
}

func FuzzQUICStreamClass(f *testing.F) {
	f.Add([]byte{byte(ClassControl)})
	f.Add([]byte{byte(ClassInput)})
	f.Add([]byte{255})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		class, err := readStreamClass(bytes.NewReader(data))
		if len(data) == 0 {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("empty preface: %v", err)
			}
			return
		}
		if data[0] >= byte(classCount) {
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("invalid class: %v", err)
			}
			return
		}
		if err != nil || byte(class) != data[0] {
			t.Fatalf("class %d: %v", class, err)
		}
	})
}

func BenchmarkQUICInputUnderBulk(b *testing.B) {
	client, server := quicPair(b)
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

func TestQUICIdentityConcurrentPublicationAndPartialFailure(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	results := make(chan Identity, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := LoadIdentity(dir)
			if err != nil {
				t.Error(err)
				return
			}
			results <- id
		}()
	}
	wg.Wait()
	close(results)
	var fingerprint string
	for id := range results {
		if fingerprint == "" {
			fingerprint = id.Fingerprint
		}
		if fingerprint != id.Fingerprint {
			t.Fatal("identity replaced during publication")
		}
	}
	partial := t.TempDir()
	path := filepath.Join(partial, "core-key.pem")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(partial); err == nil {
		t.Fatal("partial identity silently replaced")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "preserve" {
		t.Fatalf("partial identity changed: %q %v", got, err)
	}
}

func TestQUICWriteAfterHalfClose(t *testing.T) {
	c, _ := quicPair(t)
	s, err := c.OpenStream(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("late")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after FIN: %v", err)
	}
}

func TestQUICIdentityRefusesExistingTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "core-key.pem.tmp")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(dir); err == nil {
		t.Fatal("overwrote an existing identity temporary file")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "keep" {
		t.Fatalf("temporary file changed: %q, %v", got, err)
	}
}
