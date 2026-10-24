package corewire

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"sync"
	"time"
)

// pinnedTLSConfig is the common network trust boundary for QUIC and TLS/TCP.
// The exact leaf fingerprint is learned through authenticated SSH or pairing;
// no CA fallback, hostname exception or insecure retry is permitted.
func pinnedTLSConfig(fingerprint string) (*tls.Config, error) {
	pin, err := hex.DecodeString(fingerprint)
	if err != nil || len(pin) != sha256.Size {
		return nil, errors.New("corewire: invalid certificate fingerprint")
	}
	return &tls.Config{
		NextProtos: []string{alpn}, MinVersion: tls.VersionTLS13,
		ServerName: "crude-core",
		// Trust is the mandatory exact pin, not a certificate authority. VerifyConnection
		// runs on every handshake, including any future resumed TLS connection.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if state.NegotiatedProtocol != alpn {
				return errors.New("corewire: core did not negotiate the core protocol")
			}
			if len(state.PeerCertificates) == 0 {
				return errors.New("corewire: core presented no certificate")
			}
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
				return errors.New("corewire: core certificate does not match the pinned fingerprint")
			}
			return nil
		},
	}, nil
}

// TLSListener accepts the same multiplexed streams as SSH/local sockets, over
// TLS 1.3. The certificate is a transport identity, not client authorization:
// callers still authenticate the device/session through the session handshake.
type TLSListener struct {
	l       *net.TCPListener
	config  *tls.Config
	mu      sync.Mutex
	closed  bool
	pending map[*tls.Conn]struct{}
	done    chan struct{}
	ready   chan *tls.Conn
	slots   chan struct{}
	workers sync.WaitGroup
	err     error
}

// ListenTLS listens for pinned TLS/TCP fallback carriers using the core identity.
func ListenTLS(addr string, id Identity) (*TLSListener, error) {
	address, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	l, err := net.ListenTCP("tcp", address)
	if err != nil {
		return nil, err
	}
	listener := &TLSListener{l: l, config: &tls.Config{
		Certificates: []tls.Certificate{id.Certificate}, NextProtos: []string{alpn}, MinVersion: tls.VersionTLS13,
	}, pending: make(map[*tls.Conn]struct{}), done: make(chan struct{}), ready: make(chan *tls.Conn, 32), slots: make(chan struct{}, 32)}
	listener.workers.Add(1)
	go listener.acceptLoop()
	return listener, nil
}

func (l *TLSListener) Addr() net.Addr { return l.l.Addr() }

func (l *TLSListener) Close() error {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.done)
		_ = l.l.Close()
		for c := range l.pending {
			_ = c.NetConn().Close()
		}
	}
	l.mu.Unlock()
	l.workers.Wait()
	for {
		select {
		case tc := <-l.ready:
			l.mu.Lock()
			delete(l.pending, tc)
			l.mu.Unlock()
			<-l.slots
		default:
			return nil
		}
	}
}

// Accept waits only for completed TLS handshakes, never behind one slow peer.
// The listener owns at most 32 handshaking/queued connections; excess connections
// are refused. Each handshake has a ten-second ceiling. Canceling Accept stops
// that caller's wait, not other clients' handshakes. Close joins all listener work.
func (l *TLSListener) Accept(ctx context.Context) (Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case tc := <-l.ready:
		l.mu.Lock()
		delete(l.pending, tc)
		closed := l.closed
		l.mu.Unlock()
		<-l.slots
		if closed {
			_ = tc.Close()
			return nil, net.ErrClosed
		}
		if ctx.Err() != nil {
			_ = tc.Close()
			return nil, ctx.Err()
		}
		return NewMux(tc, CarrierTCP, false), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.done:
		l.mu.Lock()
		err := l.err
		l.mu.Unlock()
		if err == nil {
			err = net.ErrClosed
		}
		return nil, err
	}
}

func (l *TLSListener) acceptLoop() {
	defer l.workers.Done()
	for {
		c, err := l.l.AcceptTCP()
		if err != nil {
			l.mu.Lock()
			if !l.closed {
				l.closed = true
				l.err = err
				close(l.done)
				for pending := range l.pending {
					_ = pending.NetConn().Close()
				}
			}
			l.mu.Unlock()
			return
		}
		select {
		case l.slots <- struct{}{}:
		default:
			_ = c.Close()
			continue
		}
		tc := tls.Server(c, l.config)
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			_ = tc.Close()
			<-l.slots
			return
		}
		l.pending[tc] = struct{}{}
		l.workers.Add(1)
		l.mu.Unlock()
		go l.handshake(tc)
	}
}

func (l *TLSListener) handshake(tc *tls.Conn) {
	defer l.workers.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tc.HandshakeContext(ctx); err == nil && tc.ConnectionState().NegotiatedProtocol == alpn {
		select {
		case l.ready <- tc:
			return
		case <-l.done:
		}
	}
	_ = tc.Close()
	l.mu.Lock()
	delete(l.pending, tc)
	l.mu.Unlock()
	<-l.slots
}

// DialTLS establishes a TLS 1.3 carrier with an exact mandatory certificate pin.
func DialTLS(ctx context.Context, addr, fingerprint string) (Conn, error) {
	config, err := pinnedTLSConfig(fingerprint)
	if err != nil {
		return nil, err
	}
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := tls.Dialer{NetDialer: &net.Dialer{}, Config: config}
	c, err := d.DialContext(hctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return NewMux(c, CarrierTCP, true), nil
}
