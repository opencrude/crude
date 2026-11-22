package corewire

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func tlsListener(t testing.TB) (*TLSListener, Identity) {
	t.Helper()
	id, err := LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := ListenTLS("127.0.0.1:0", id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, id
}

func TestTLSCarrierPinnedCompressedStreams(t *testing.T) {
	l, id := tlsListener(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := make(chan Conn, 1)
	go func() {
		c, err := l.Accept(ctx)
		if err != nil {
			server <- nil
			return
		}
		server <- c
	}()
	c, err := DialTLS(ctx, l.Addr().String(), id.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	peer := <-server
	if peer == nil {
		t.Fatal("TLS accept failed")
	}
	defer peer.Close()
	if c.Carrier() != CarrierTCP || peer.Carrier() != CarrierTCP {
		t.Fatal("not the TCP network carrier")
	}
	tc := c.(*Mux).rwc.(*tls.Conn)
	state := tc.ConnectionState()
	if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != alpn {
		t.Fatal("incorrect TLS negotiation")
	}
	s, err := c.OpenStream(ctx, ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	remote := accept(t, peer)
	want := bytes.Repeat([]byte("compressed TLS stream"), 3000)
	done := make(chan error, 1)
	go func() {
		_, err := s.Write(want)
		if err == nil {
			err = s.CloseWrite()
		}
		done <- err
	}()
	got, err := io.ReadAll(remote)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("TLS data %d/%d: %v", len(got), len(want), err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.(*compressedStream).writeMode != 1 {
		t.Fatal("TLS bypassed shared compression")
	}
}

func TestTLSCarrierRejectsWrongPinAndInvalidFingerprints(t *testing.T) {
	l, id := tlsListener(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if c, err := l.Accept(ctx); err == nil {
			_ = c.Close()
		}
	}()
	other, err := LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c, err := DialTLS(ctx, l.Addr().String(), other.Fingerprint); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("wrong pin accepted: %v", err)
	}
	for _, pin := range []string{"", "abcdef", strings.Repeat("z", 64)} {
		if _, err := DialTLS(ctx, l.Addr().String(), pin); err == nil {
			t.Fatal("invalid pin accepted")
		}
	}
	// A bad client must not poison the listener for the valid client behind it.
	c, err := DialTLS(ctx, l.Addr().String(), id.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	<-done
}

func TestTLSAcceptCancellationAndCloseDuringHandshake(t *testing.T) {
	for _, duringHandshake := range []bool{false, true} {
		for _, closeListener := range []bool{false, true} {
			l, _ := tlsListener(t)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { _, err := l.Accept(ctx); done <- err }()
			if duringHandshake {
				c, err := net.Dial("tcp", l.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				waitFor(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return len(l.pending) == 1 })
			}
			want := context.Canceled
			if closeListener {
				_ = l.Close()
				want = net.ErrClosed
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("accept cancellation: %v want %v", err, want)
				}
			case <-time.After(time.Second):
				t.Fatal("accept remained blocked")
			}
			cancel()
			// Closing the listener must join even its stalled unauthenticated
			// handshakes, not just release the Accept waiter.
			_ = l.Close()
			l.mu.Lock()
			pending := len(l.pending)
			l.mu.Unlock()
			if pending != 0 {
				t.Fatalf("pending sockets retained: %d", pending)
			}
		}
	}
}

func TestTLSStalledPeerDoesNotBlockValidPeer(t *testing.T) {
	l, id := tlsListener(t)
	stalled, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	waitFor(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return len(l.pending) == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := DialTLS(ctx, l.Addr().String(), id.Fingerprint)
	if err != nil {
		t.Fatalf("good client blocked behind stalled TLS: %v", err)
	}
	defer c.Close()
	peer, err := l.Accept(ctx)
	if err != nil {
		t.Fatalf("good accept blocked behind stalled TLS: %v", err)
	}
	defer peer.Close()
}

func TestTLSBoundsPendingHandshakesAndJoinsClose(t *testing.T) {
	l, _ := tlsListener(t)
	var sockets []net.Conn
	defer func() {
		for _, c := range sockets {
			_ = c.Close()
		}
	}()
	for range cap(l.slots) {
		c, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		sockets = append(sockets, c)
	}
	waitFor(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return len(l.pending) == cap(l.slots) })
	overflow, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer overflow.Close()
	_ = overflow.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := overflow.Read(b[:]); err != io.EOF {
		t.Fatalf("excess handshake not refused: %v", err)
	}
	_ = l.Close()
	l.mu.Lock()
	pending := len(l.pending)
	l.mu.Unlock()
	if pending != 0 || len(l.slots) != 0 {
		t.Fatalf("listener leaked: pending=%d slots=%d", pending, len(l.slots))
	}
}

func TestTLSDialCancellationDuringHandshake(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := DialTLS(ctx, l.Addr().String(), strings.Repeat("a", 64)); done <- err }()
	c := <-accepted
	defer c.Close()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("dial did not cancel")
	}
}

func TestTLSRequiresALPNAndTLS13(t *testing.T) {
	for _, tls12 := range []bool{false, true} {
		l, id := tlsListener(t)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := l.Accept(ctx); done <- err }()
		config, err := pinnedTLSConfig(id.Fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		if tls12 {
			config.MinVersion = tls.VersionTLS12
			config.MaxVersion = tls.VersionTLS12
		} else {
			config.NextProtos = []string{"not-crude"}
		}
		c, err := tls.Dial("tcp", l.Addr().String(), config)
		if err == nil {
			_ = c.Close()
			t.Fatal("invalid TLS negotiation accepted")
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
