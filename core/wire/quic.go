package corewire

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/quic-go/quic-go"
)

// alpn identifies this protocol inside TLS; a peer speaking anything else
// fails the handshake before any application byte is exchanged.
const alpn = "crude-core/2"

func quicConfig() *quic.Config {
	return &quic.Config{
		HandshakeIdleTimeout:           10 * time.Second,
		MaxIdleTimeout:                 livenessLimit,
		KeepAlivePeriod:                pingInterval,
		InitialStreamReceiveWindow:     streamWindow,
		MaxStreamReceiveWindow:         streamWindow,
		InitialConnectionReceiveWindow: 1 << 20,
		MaxConnectionReceiveWindow:     32 << 20,
		MaxIncomingStreams:             maxStreams,
		MaxIncomingUniStreams:          -1,
	}
}

// Identity is a core's QUIC certificate. Clients pin its fingerprint, learned
// over SSH or during pairing; no certificate authority is involved.
type Identity struct {
	Certificate tls.Certificate
	Fingerprint string // hex SHA-256 of the leaf certificate
}

// LoadIdentity reads the core certificate from dir, creating a new self-signed
// ECDSA P-256 identity on first use. The key file is private to the user.
func LoadIdentity(dir string) (Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Identity{}, err
	}
	// Serialize first publication across cores. Never replace one half of a
	// concurrently generated identity with a different key/certificate pair.
	lock := flock.New(filepath.Join(dir, "core-identity.lock"))
	if err := lock.Lock(); err != nil {
		return Identity{}, err
	}
	defer func() { _ = lock.Unlock() }()
	certPath, keyPath := filepath.Join(dir, "core-cert.pem"), filepath.Join(dir, "core-key.pem")
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return identity(cert), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Identity{}, err
	}
	for _, path := range []string{certPath, keyPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return Identity{}, fmt.Errorf("corewire: incomplete core identity at %s", path)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return Identity{}, err
	}
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "crude core"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(20, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"crude-core"}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return Identity{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Identity{}, err
	}
	if err := writePrivate(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		return Identity{}, err
	}
	if err := writePrivate(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return Identity{}, err
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return Identity{}, err
	}
	return identity(cert), nil
}

func writePrivate(path string, data []byte) error {
	tmp := path + ".tmp"
	// Exclusive creation refuses stale files and symlinks rather than writing
	// private material through an attacker-selected temporary path.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := writeAll(f, data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func identity(cert tls.Certificate) Identity {
	sum := sha256.Sum256(cert.Certificate[0])
	return Identity{Certificate: cert, Fingerprint: hex.EncodeToString(sum[:])}
}

// QUICListener is one immutable accept generation on a retained UDP transport.
// StopAccept affects only this generation; Close releases the shared transport
// and every carrier accepted by any generation.
type QUICListener struct {
	l *quic.Listener
	r *quicListenerResource
}

type quicListenerResource struct {
	mu          sync.Mutex
	transport   *quic.Transport
	packet      net.PacketConn
	tls         *tls.Config
	active      *QUICListener
	connections map[*quic.Conn]struct{}
	accepts     sync.WaitGroup
	cleanup     sync.WaitGroup
	closed      bool
	done        chan struct{}
	err         error
}

// ListenQUIC listens on a UDP address such as ":7430" or "127.0.0.1:0".
func ListenQUIC(addr string, id Identity) (*QUICListener, error) {
	tlsConf := &tls.Config{Certificates: []tls.Certificate{id.Certificate}, NextProtos: []string{alpn}, MinVersion: tls.VersionTLS13}
	packet, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	transport := &quic.Transport{Conn: packet}
	l, err := transport.Listen(tlsConf, quicConfig())
	if err != nil {
		_ = transport.Close()
		_ = packet.Close()
		return nil, err
	}
	r := &quicListenerResource{transport: transport, packet: packet, tls: tlsConf, connections: make(map[*quic.Conn]struct{}), done: make(chan struct{})}
	listener := &QUICListener{l: l, r: r}
	r.active = listener
	return listener, nil
}

func (l *QUICListener) Addr() net.Addr { return l.l.Addr() }

// StopAccept retires this generation without interrupting established carriers.
// Queued but unaccepted connections are closed, including on prepared rollback.
// The owner must eventually Close the shared resource to release its UDP socket.
func (l *QUICListener) StopAccept() error {
	l.r.mu.Lock()
	defer l.r.mu.Unlock()
	err := l.l.Close()
	for {
		conn, acceptErr := l.l.Accept(context.Background())
		if acceptErr != nil {
			break
		}
		_ = conn.CloseWithError(0, "listener retired")
	}
	if l.r.active == l {
		l.r.active = nil
	}
	return err
}

// PrepareAccept reserves a new accept generation on this retained transport.
// Call only after StopAccept. It performs QUIC handshakes, but the owner must not
// call Accept or serve application traffic until its configuration commits.
// Abort with StopAccept, not Close: existing carriers still own this transport.
// Old handles remain immutable, so late Accept exits cannot observe a new listener.
func (l *QUICListener) PrepareAccept() (*QUICListener, error) {
	l.r.mu.Lock()
	defer l.r.mu.Unlock()
	if l.r.closed {
		return nil, net.ErrClosed
	}
	if l.r.active != nil {
		return nil, errors.New("corewire: QUIC acceptance is already active")
	}
	next, err := l.r.transport.Listen(l.r.tls, quicConfig())
	if err != nil {
		return nil, err
	}
	listener := &QUICListener{l: next, r: l.r}
	l.r.active = listener
	return listener, nil
}

// Close synchronously releases all owned transport resources. Listener-only
// retirement is explicit; aborting a prepared endpoint never leaks a UDP port.
func (l *QUICListener) Close() error {
	l.r.mu.Lock()
	if l.r.closed {
		l.r.mu.Unlock()
		<-l.r.done
		return l.r.err
	}
	l.r.closed = true
	var err error
	active := l.r.active
	if active != nil {
		err = active.l.Close()
	}
	l.r.active = nil
	l.r.mu.Unlock()
	// Closing acceptance releases all Accept calls. A call that already took
	// a connection from the queue must finish transferring it to this resource
	// before the close snapshot; none can start after closed is published.
	l.r.accepts.Wait()
	l.r.mu.Lock()
	var connections []*quic.Conn
	if active != nil {
		// Listener.Close stops new handshakes, but deliberately retains its
		// already completed accept queue. No application owner has these yet.
		for {
			conn, acceptErr := active.l.Accept(context.Background())
			if acceptErr != nil {
				break
			}
			connections = append(connections, conn)
		}
	}
	for conn := range l.r.connections {
		connections = append(connections, conn)
	}
	l.r.mu.Unlock()
	// Transport.Close is intentionally abrupt: it cannot notify peers after
	// discarding UDP. Finish application CONNECTION_CLOSE writes first, across
	// every accept generation, so fallback does not wait for idle liveness.
	for _, conn := range connections {
		err = errors.Join(err, conn.CloseWithError(0, "listener closed"))
	}
	l.r.cleanup.Wait()
	err = errors.Join(err, l.r.transport.Close(), l.r.packet.Close())
	l.r.mu.Lock()
	l.r.err = err
	close(l.r.done)
	l.r.mu.Unlock()
	return l.r.err
}

func (l *QUICListener) Accept(ctx context.Context) (Conn, error) {
	l.r.mu.Lock()
	if l.r.closed {
		l.r.mu.Unlock()
		return nil, net.ErrClosed
	}
	l.r.accepts.Add(1)
	l.r.mu.Unlock()
	defer l.r.accepts.Done()
	c, err := l.l.Accept(ctx)
	if err != nil {
		return nil, err
	}
	l.r.mu.Lock()
	closed := l.r.closed
	l.r.connections[c] = struct{}{}
	l.r.cleanup.Add(1)
	l.r.mu.Unlock()
	context.AfterFunc(c.Context(), func() {
		defer l.r.cleanup.Done()
		l.r.mu.Lock()
		delete(l.r.connections, c)
		l.r.mu.Unlock()
	})
	if closed {
		return nil, net.ErrClosed
	}
	return &quicConn{c: c}, nil
}

// DialQUIC connects to a core and verifies that its certificate matches the
// pinned fingerprint exactly.
func DialQUIC(ctx context.Context, addr, fingerprint string) (Conn, error) {
	tlsConf, err := pinnedTLSConfig(fingerprint)
	if err != nil {
		return nil, err
	}
	c, err := quic.DialAddr(ctx, addr, tlsConf, quicConfig())
	if err != nil {
		return nil, err
	}
	return &quicConn{c: c}, nil
}

type quicConn struct{ c *quic.Conn }

func (q *quicConn) Carrier() Carrier      { return CarrierQUIC }
func (q *quicConn) Done() <-chan struct{} { return q.c.Context().Done() }
func (q *quicConn) RTT() time.Duration    { return q.c.ConnectionStats().SmoothedRTT }
func (q *quicConn) Close() error          { return q.c.CloseWithError(0, "closed") }

func (q *quicConn) Err() error {
	if cause := context.Cause(q.c.Context()); cause != nil {
		return fmt.Errorf("%w: %v", ErrClosed, cause)
	}
	return nil
}

func (q *quicConn) OpenStream(ctx context.Context, class Class) (Stream, error) {
	if !class.valid() {
		return nil, errors.New("corewire: invalid stream class")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := q.c.OpenStreamSync(ctx)
	if err != nil {
		return nil, quicError(err)
	}
	s.SetPriority(uint8(class))
	// The class travels as the stream's first byte, so the peer can schedule
	// its replies; QUIC itself has no stream priorities on the wire.
	stop := context.AfterFunc(ctx, func() { s.CancelWrite(0); s.CancelRead(0) })
	_, err = s.Write([]byte{byte(class)})
	stop()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		s.CancelWrite(0)
		s.CancelRead(0)
		return nil, quicError(err)
	}
	return networkStream(&quicStream{s: s, class: class}, CarrierQUIC), nil
}

func (q *quicConn) AcceptStream(ctx context.Context) (Stream, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s, err := q.c.AcceptStream(ctx)
		if err != nil {
			return nil, quicError(err)
		}
		class, err := readQUICClass(ctx, s)
		if err != nil {
			s.CancelWrite(0)
			s.CancelRead(0)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		s.SetPriority(uint8(class))
		return networkStream(&quicStream{s: s, class: class}, CarrierQUIC), nil
	}
}

func readQUICClass(ctx context.Context, s *quic.Stream) (Class, error) {
	stop := context.AfterFunc(ctx, func() { s.CancelRead(0); s.CancelWrite(0) })
	_ = s.SetReadDeadline(time.Now().Add(10 * time.Second))
	class, err := readStreamClass(s)
	stop()
	_ = s.SetReadDeadline(time.Time{})
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return class, err
}

func readStreamClass(r io.Reader) (Class, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	if !Class(b[0]).valid() {
		return 0, ErrProtocol
	}
	return Class(b[0]), nil
}

type quicStream struct {
	s           *quic.Stream
	class       Class
	writeMu     sync.Mutex
	writeClosed bool
}

func (s *quicStream) Class() Class { return s.class }

func (s *quicStream) Read(p []byte) (int, error) {
	n, err := s.s.Read(p)
	return n, quicError(err)
}

func (s *quicStream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeClosed {
		return 0, ErrClosed
	}
	n, err := s.s.Write(p)
	return n, quicError(err)
}

func (s *quicStream) CloseWrite() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeClosed {
		return nil
	}
	s.writeClosed = true
	return quicError(s.s.Close())
}

func (s *quicStream) Close() error {
	s.s.CancelWrite(0)
	s.s.CancelRead(0)
	return nil
}

func quicError(err error) error {
	if err == nil {
		return nil
	}
	var reset *quic.StreamError
	if errors.As(err, &reset) {
		if reset.Remote {
			return ErrReset
		}
		return ErrClosed
	}
	var idle *quic.IdleTimeoutError
	if errors.As(err, &idle) {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	return err
}
