package corewire

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Stream-mux framing over one reliable byte stream. Every frame is
//
//	uvarint stream · type byte · uvarint length · payload
//
// Data frames carry at most frameMax bytes, so higher-priority output waits
// behind at most one frame of a lower class. Each stream has its own receive
// window (credit); a sender never has more than streamWindow unacknowledged
// bytes in flight on one stream. Stream 0 carries connection-level frames.
const (
	frameMax     = 16 << 10
	batchMax     = frameMax
	streamWindow = 256 << 10
	// Together the two directions admit at most 128 MiB of unread payload.
	// Completed-but-unread streams continue to occupy their admission slot.
	maxStreams    = 256
	acceptBacklog = 256
	closeTextMax  = 1024
	controlMax    = 128 << 10
)

// Heartbeat timing is variable only so tests can shorten it.
var (
	pingInterval  = 5 * time.Second
	livenessLimit = 20 * time.Second
)

const (
	frameOpen   byte = 1 // payload: class byte
	frameData   byte = 2
	frameFin    byte = 3
	frameReset  byte = 4
	frameWindow byte = 5 // payload: uvarint credit increment
	framePing   byte = 6 // stream 0, payload: 8-byte opaque token
	framePong   byte = 7 // stream 0, echoes the ping token
	frameClose  byte = 8 // stream 0, payload: reason text
)

// Mux multiplexes streams over one reliable byte stream (local socket, named
// pipe, SSH stdio or TCP). One writer goroutine drains streams by class and
// batches ready frames into a single write; one reader goroutine demultiplexes.
type Mux struct {
	rwc     io.ReadWriteCloser
	carrier Carrier
	client  bool
	epoch   time.Time

	mu       sync.Mutex
	wake     sync.Cond // writer: control frames or ready streams
	streams  map[uint64]*muxStream
	nextID   uint64
	peerMax  uint64 // highest stream ID the peer has opened
	opened   int    // live locally opened streams
	accepted int    // live peer-opened streams
	control  []byte // encoded connection and stream control frames
	ready    [classCount]streamQueue
	budget   [classCount]int
	closed   bool
	closing  bool          // Close asked the writer to flush control frames
	flushed  chan struct{} // closed once those frames reached the carrier
	err      error

	accept   chan *muxStream
	done     chan struct{}
	lastRecv atomic.Int64
	rtt      atomic.Int64
	workers  sync.WaitGroup
}

type muxStream struct {
	m     *Mux
	id    uint64
	class Class

	readable sync.Cond
	writable sync.Cond

	recv        []byte
	recvOff     int
	unacked     int
	finRecv     bool
	finSent     bool
	finPending  bool
	reset       bool // peer reset
	local       bool // closed locally
	out         []byte
	credit      int
	queued      bool
	writing     bool
	released    bool
	prev, next  *muxStream
	written     int
	openPending bool
}

type streamQueue struct{ head, tail *muxStream }

// NewMux starts multiplexing over rwc. client selects the stream-ID parity;
// the two ends of one byte stream must disagree. The Mux owns rwc.
func NewMux(rwc io.ReadWriteCloser, carrier Carrier, client bool) *Mux {
	m := &Mux{rwc: rwc, carrier: carrier, client: client, epoch: time.Now(), streams: make(map[uint64]*muxStream), accept: make(chan *muxStream, acceptBacklog), done: make(chan struct{}), flushed: make(chan struct{})}
	m.wake.L = &m.mu
	if client {
		m.nextID = 1
	} else {
		m.nextID = 2
	}
	m.lastRecv.Store(m.now())
	m.workers.Add(4)
	go func() { defer m.workers.Done(); <-m.done; _ = m.rwc.Close() }()
	go m.readLoop()
	go m.writeLoop()
	go m.heartbeat()
	return m
}

func (m *Mux) now() int64 { return int64(time.Since(m.epoch)) }

func (m *Mux) Carrier() Carrier      { return m.carrier }
func (m *Mux) Done() <-chan struct{} { return m.done }
func (m *Mux) RTT() time.Duration    { return time.Duration(m.rtt.Load()) }
func (m *Mux) Err() error            { m.mu.Lock(); defer m.mu.Unlock(); return m.err }

// Close flushes queued control frames (stream ends and resets) for at most a
// second, so the peer can tell a deliberate end from a lost carrier, then
// closes the carrier.
func (m *Mux) Close() error {
	m.mu.Lock()
	if !m.closed && !m.closing {
		m.closing = true
		m.wake.Broadcast()
	}
	m.mu.Unlock()
	select {
	case <-m.flushed:
	case <-m.done:
	case <-time.After(time.Second):
	}
	m.fail(ErrClosed)
	m.workers.Wait()
	return nil
}
func (m *Mux) peerStream(id uint64) bool { return (id%2 == 1) != m.client }

func (m *Mux) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failLocked(err)
}

func (m *Mux) failLocked(err error) {
	if m.closed {
		return
	}
	m.closed, m.err = true, err
	close(m.done)
	for _, s := range m.streams {
		s.readable.Broadcast()
		s.writable.Broadcast()
	}
	m.wake.Broadcast()
}

func (m *Mux) OpenStream(ctx context.Context, class Class) (Stream, error) {
	if !class.valid() {
		return nil, errors.New("corewire: invalid stream class")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.closing {
		if m.err == nil {
			return nil, ErrClosed
		}
		return nil, m.err
	}
	if m.opened >= maxStreams || len(m.control) > controlMax-32 {
		return nil, ErrRefused
	}
	s := m.newStream(m.nextID, class)
	s.openPending = true
	m.nextID += 2
	m.opened++
	m.queueControl(s.id, frameOpen, []byte{byte(class)})
	return networkStream(s, m.carrier), nil
}

func (m *Mux) AcceptStream(ctx context.Context) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-m.done:
		return nil, m.Err()
	default:
	}
	select {
	case s := <-m.accept:
		return networkStream(s, m.carrier), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.done:
		return nil, m.Err()
	}
}

func (m *Mux) newStream(id uint64, class Class) *muxStream {
	s := &muxStream{m: m, id: id, class: class, credit: streamWindow}
	s.readable.L = &m.mu
	s.writable.L = &m.mu
	m.streams[id] = s
	return s
}

// release forgets a stream once neither direction can carry more frames.
func (m *Mux) release(s *muxStream) {
	if s.released || !s.reset && !s.local && (!s.finSent || !s.finRecv || s.writing || len(s.recv) != s.recvOff) {
		return
	}
	s.released = true
	m.unschedule(s)
	delete(m.streams, s.id)
	if m.peerStream(s.id) {
		m.accepted--
	} else {
		m.opened--
	}
}

func appendFrameHeader(b []byte, id uint64, kind byte, n int) []byte {
	b = binary.AppendUvarint(b, id)
	b = append(b, kind)
	return binary.AppendUvarint(b, uint64(n))
}

// queueControl must hold mu.
func (m *Mux) queueControl(id uint64, kind byte, payload []byte) {
	if m.closed {
		return
	}
	if len(m.control)+binary.MaxVarintLen64*2+1+len(payload) > controlMax {
		m.failLocked(fmt.Errorf("%w: excessive pending control traffic", ErrProtocol))
		return
	}
	m.control = appendFrameHeader(m.control, id, kind, len(payload))
	m.control = append(m.control, payload...)
	m.wake.Signal()
}

// Queue links are intrusive: rescheduling a bulk frame allocates nothing, and
// resetting a queued stream removes its reference immediately.
func (m *Mux) schedule(s *muxStream) {
	if s.queued || s.openPending || len(s.out) == 0 || s.credit <= 0 || s.reset || s.local {
		return
	}
	q := &m.ready[s.class]
	s.queued, s.prev, s.next = true, q.tail, nil
	if q.tail != nil {
		q.tail.next = s
	} else {
		q.head = s
	}
	q.tail = s
	m.wake.Signal()
}

func (m *Mux) unschedule(s *muxStream) {
	if !s.queued {
		return
	}
	q := &m.ready[s.class]
	if s.prev != nil {
		s.prev.next = s.next
	} else {
		q.head = s.next
	}
	if s.next != nil {
		s.next.prev = s.prev
	} else {
		q.tail = s.prev
	}
	s.queued, s.prev, s.next = false, nil, nil
}

// Prefer urgent traffic while guaranteeing service to every active class.
// Unused budgets stay available, so input arriving during bulk is next.
func (m *Mux) nextReady() *muxStream {
	for range 2 {
		for c := range m.ready {
			if m.budget[c] > 0 && m.ready[c].head != nil {
				s := m.ready[c].head
				m.unschedule(s)
				m.budget[c]--
				return s
			}
		}
		m.budget = [classCount]int{8, 4, 2, 1}
	}
	return nil
}

func (m *Mux) hasReady() bool {
	for c := range m.ready {
		if m.ready[c].head != nil {
			return true
		}
	}
	return false
}

func (m *Mux) writeLoop() {
	defer m.workers.Done()
	batch := make([]byte, 0, batchMax+frameMax+64)
	for {
		m.mu.Lock()
		for !m.closed && len(m.control) == 0 && (m.closing || !m.hasReady()) {
			if m.closing {
				m.mu.Unlock()
				close(m.flushed)
				<-m.done
				return
			}
			m.wake.Wait()
		}
		if m.closed {
			m.mu.Unlock()
			return
		}
		// Bound control serialization too: an open/ping flood must neither
		// produce a giant carrier write nor starve application streams.
		end := 0
		for end < len(m.control) && end < batchMax {
			id, n := binary.Uvarint(m.control[end:])
			if m.control[end+n] == frameOpen {
				if s := m.streams[id]; s != nil {
					s.openPending = false
					m.schedule(s)
				}
			}
			size, k := binary.Uvarint(m.control[end+n+1:])
			end += n + 1 + k + int(size)
		}
		batch = append(batch[:0], m.control[:end]...)
		m.control = m.control[:copy(m.control, m.control[end:])]
		if !m.closing {
			s := m.nextReady()
			if s != nil {
				n := min(frameMax, s.credit, len(s.out))
				batch = appendFrameHeader(batch, s.id, frameData, n)
				batch = append(batch, s.out[:n]...)
				s.out, s.credit = s.out[n:], s.credit-n
				s.written += n
				if len(s.out) == 0 {
					s.writable.Broadcast()
				}
				m.schedule(s)
			}
		}
		m.mu.Unlock()
		if err := writeAll(m.rwc, batch); err != nil {
			m.fail(fmt.Errorf("%w: carrier write: %v", ErrClosed, err))
			return
		}
	}
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func (m *Mux) heartbeat() {
	defer m.workers.Done()
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	m.ping()
	for {
		select {
		case <-m.done:
			return
		case <-t.C:
			if time.Duration(m.now()-m.lastRecv.Load()) > livenessLimit {
				m.fail(ErrTimeout)
				return
			}
			m.ping()
		}
	}
}

func (m *Mux) ping() {
	var token [8]byte
	binary.BigEndian.PutUint64(token[:], uint64(m.now()))
	m.mu.Lock()
	if !m.closed && !m.closing {
		m.queueControl(0, framePing, token[:])
	}
	m.mu.Unlock()
}

func (m *Mux) readLoop() {
	defer m.workers.Done()
	r := bufio.NewReaderSize(m.rwc, 64<<10)
	payload := make([]byte, frameMax)
	for {
		err := m.readFrame(r, payload)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				err = fmt.Errorf("%w: carrier ended", ErrClosed)
			} else if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// Keep the carrier's own diagnostic (an SSH failure, say).
				err = fmt.Errorf("%w: carrier ended: %v", ErrClosed, err)
			} else if !errors.Is(err, ErrProtocol) && !errors.Is(err, ErrClosed) {
				err = fmt.Errorf("%w: carrier read: %v", ErrClosed, err)
			}
			m.fail(err)
			return
		}
	}
}

func (m *Mux) readFrame(r *bufio.Reader, scratch []byte) error {
	id, err := binary.ReadUvarint(r)
	if err != nil {
		return err
	}
	kind, err := r.ReadByte()
	if err != nil {
		return err
	}
	size, err := binary.ReadUvarint(r)
	if err != nil {
		return err
	}
	limit := uint64(0)
	switch kind {
	case frameData:
		limit = frameMax
	case frameOpen:
		limit = 1
	case frameWindow:
		limit = binary.MaxVarintLen64
	case framePing, framePong:
		limit = 8
	case frameClose:
		limit = closeTextMax
	case frameFin, frameReset:
	default:
		return fmt.Errorf("%w: unknown frame type %d", ErrProtocol, kind)
	}
	if size > limit {
		return fmt.Errorf("%w: frame type %d carries %d bytes", ErrProtocol, kind, size)
	}
	if (id == 0) != (kind == framePing || kind == framePong || kind == frameClose) {
		return fmt.Errorf("%w: frame type %d on stream %d", ErrProtocol, kind, id)
	}
	body := scratch[:size]
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	m.lastRecv.Store(m.now())
	switch kind {
	case framePing:
		if len(body) != 8 {
			return fmt.Errorf("%w: short ping", ErrProtocol)
		}
		m.mu.Lock()
		if !m.closed {
			m.queueControl(0, framePong, body)
		}
		m.mu.Unlock()
		return nil
	case framePong:
		if len(body) != 8 {
			return fmt.Errorf("%w: short pong", ErrProtocol)
		}
		sample := m.now() - int64(binary.BigEndian.Uint64(body))
		if sample < 0 {
			return nil
		}
		if old := m.rtt.Load(); old == 0 {
			m.rtt.Store(sample)
		} else {
			m.rtt.Store(old - old/8 + sample/8)
		}
		return nil
	case frameClose:
		return fmt.Errorf("%w: peer closed: %s", ErrClosed, printable(body))
	case frameOpen:
		return m.receiveOpen(id, body)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.streams[id]
	if s == nil {
		// Frames for a stream this side already released are in flight and
		// harmless; frames for a stream that never existed are a violation.
		if m.peerStream(id) && id > m.peerMax || !m.peerStream(id) && id >= m.nextID {
			return fmt.Errorf("%w: frame for unknown stream %d", ErrProtocol, id)
		}
		return nil
	}
	switch kind {
	case frameData:
		if s.finRecv {
			return fmt.Errorf("%w: data after end of stream %d", ErrProtocol, id)
		}
		if s.local {
			return nil
		}
		if len(s.recv)-s.recvOff+s.unacked+len(body) > streamWindow {
			return fmt.Errorf("%w: stream %d exceeded its credit", ErrProtocol, id)
		}
		if s.recvOff > 0 && s.recvOff >= len(s.recv)/2 {
			s.recv = s.recv[:copy(s.recv, s.recv[s.recvOff:])]
			s.recvOff = 0
		}
		s.recv = append(s.recv, body...)
		s.readable.Broadcast()
	case frameFin:
		if s.finRecv {
			return fmt.Errorf("%w: duplicate end of stream %d", ErrProtocol, id)
		}
		s.finRecv = true
		s.readable.Broadcast()
		m.release(s)
	case frameReset:
		s.reset = true
		s.recv, s.recvOff = nil, 0
		m.unschedule(s)
		s.readable.Broadcast()
		s.writable.Broadcast()
		m.release(s)
	case frameWindow:
		inc, n := binary.Uvarint(body)
		if n != len(body) || inc == 0 || inc > streamWindow || uint64(s.credit)+inc > streamWindow {
			return fmt.Errorf("%w: invalid credit for stream %d", ErrProtocol, id)
		}
		s.credit += int(inc)
		m.schedule(s)
	}
	return nil
}

func (m *Mux) receiveOpen(id uint64, body []byte) error {
	if len(body) != 1 || !Class(body[0]).valid() {
		return fmt.Errorf("%w: invalid stream class", ErrProtocol)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.peerStream(id) || id <= m.peerMax {
		return fmt.Errorf("%w: invalid stream identity %d", ErrProtocol, id)
	}
	m.peerMax = id
	if m.closed {
		return nil
	}
	if m.accepted >= maxStreams || len(m.accept) == cap(m.accept) {
		m.queueControl(id, frameReset, nil)
		return nil
	}
	s := m.newStream(id, Class(body[0]))
	m.accepted++
	m.accept <- s
	return nil
}

func printable(b []byte) string {
	out := make([]rune, 0, len(b))
	for _, r := range string(b) {
		if r >= 0x20 && r != 0x7f {
			out = append(out, r)
		}
	}
	return string(out)
}

func (s *muxStream) Class() Class { return s.class }

func (s *muxStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		if s.local {
			return 0, ErrClosed
		}
		if buffered := len(s.recv) - s.recvOff; buffered > 0 {
			n := copy(p, s.recv[s.recvOff:])
			s.recvOff += n
			if s.recvOff == len(s.recv) {
				s.recv, s.recvOff = s.recv[:0], 0
			}
			m.release(s)
			s.unacked += n
			if s.unacked >= streamWindow/2 && !s.finRecv && !s.reset && !m.closed {
				m.queueControl(s.id, frameWindow, binary.AppendUvarint(nil, uint64(s.unacked)))
				s.unacked = 0
			}
			return n, nil
		}
		if s.reset {
			return 0, ErrReset
		}
		if s.finRecv {
			return 0, io.EOF
		}
		if m.closed {
			return 0, m.err
		}
		s.readable.Wait()
	}
}

func (s *muxStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	for s.writing && !s.local && !s.reset && !m.closed {
		s.writable.Wait()
	}
	if err := s.writeErr(); err != nil {
		return 0, err
	}
	// The writer copies frames out of p before Write returns; p is never
	// retained, so callers may reuse their buffers immediately.
	s.writing, s.out, s.written = true, p, 0
	m.schedule(s)
	for len(s.out) > 0 && !s.local && !s.reset && !m.closed {
		s.writable.Wait()
	}
	written := s.written
	s.writing, s.out = false, nil
	s.writable.Broadcast()
	m.release(s)
	if written < len(p) {
		return written, s.writeErr()
	}
	return written, nil
}

func (s *muxStream) writeErr() error {
	switch {
	case s.local || s.finSent || s.finPending || s.m.closing:
		return ErrClosed
	case s.reset:
		return ErrReset
	case s.m.closed:
		return s.m.err
	}
	return nil
}

func (s *muxStream) CloseWrite() error {
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.finSent {
		return nil
	}
	if err := s.writeErr(); err != nil {
		return err
	}
	s.finPending = true
	for s.writing && !s.local && !s.reset && !m.closed {
		s.writable.Wait()
	}
	s.finPending = false
	if err := s.writeErr(); err != nil {
		return err
	}
	s.finSent = true
	m.queueControl(s.id, frameFin, nil)
	m.release(s)
	return nil
}

func (s *muxStream) Close() error {
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.local {
		return nil
	}
	s.local = true
	m.unschedule(s)
	s.recv, s.recvOff = nil, 0
	if !s.reset && (!s.finSent || !s.finRecv) && !m.closed {
		m.queueControl(s.id, frameReset, nil)
	}
	s.readable.Broadcast()
	s.writable.Broadcast()
	m.release(s)
	return nil
}
