// Package wirefault injects transport faults into a real stream-mux carrier
// for tests: it reads the frames crossing a connection and drops the core's
// reply to one method, so the command executes but its acknowledgement is
// lost exactly as on a failing network.
package wirefault

import (
	"encoding/binary"
	"net"
	"sync"

	"crude/coreprotocol"
	"crude/corewire"
)

// Frame types of the stream-mux carrier (corewire/mux.go).
const (
	frameData byte = 2
	frameFin  byte = 3
)

// DropReplies wraps the core side of a carrier. Replies on streams whose
// command method is method never reach the client; Dropped closes only when a
// complete final reply is discarded. Admission/progress cannot attest an effect.
type DropReplies struct {
	net.Conn
	method  string
	Dropped chan struct{}

	once    sync.Once
	mu      sync.Mutex
	in, out []byte
	opening map[uint64][]byte // first bytes of client streams, until Open decodes
	methods map[uint64]string
	replies map[uint64][]byte
}

func NewDropReplies(conn net.Conn, method string) *DropReplies {
	return &DropReplies{Conn: conn, method: method, Dropped: make(chan struct{}), opening: map[uint64][]byte{}, methods: map[uint64]string{}, replies: map[uint64][]byte{}}
}

// Read observes client frames to learn each stream's command method.
func (d *DropReplies) Read(p []byte) (int, error) {
	n, err := d.Conn.Read(p)
	d.mu.Lock()
	d.in = append(d.in, p[:n]...)
	d.in = frames(d.in, func(id uint64, kind byte, payload []byte) bool {
		if kind == frameData {
			if _, known := d.methods[id]; !known {
				d.learn(id, payload)
			}
		}
		return true
	})
	d.mu.Unlock()
	return n, err
}

func (d *DropReplies) learn(id uint64, payload []byte) {
	buffer := append(d.opening[id], payload...)
	size, n := binary.Uvarint(buffer)
	if n <= 0 || len(buffer) < n+int(size) {
		d.opening[id] = buffer
		return
	}
	delete(d.opening, id)
	var open coreprotocol.Open
	if _, err := coreprotocol.DecodePrefix(buffer[n:n+int(size)], &open); err == nil {
		d.methods[id] = open.Method
	} else {
		d.methods[id] = ""
	}
}

// Write forwards core frames except the held method's reply data and end.
func (d *DropReplies) Write(p []byte) (int, error) {
	d.mu.Lock()
	d.out = append(d.out, p...)
	var forward []byte
	d.out = frames(d.out, func(id uint64, kind byte, payload []byte) bool {
		drop := (kind == frameData || kind == frameFin) && d.methods[id] == d.method
		if drop && kind == frameData {
			d.observeReply(id, payload)
		}
		if kind == frameFin {
			delete(d.replies, id)
		}
		return !drop
	}, &forward)
	d.mu.Unlock()
	if len(forward) > 0 {
		if _, err := d.Conn.Write(forward); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// observeReply reassembles the bounded framed message, not the transport frame:
// one message may span several writes/frames and admission may precede final.
// Invalid/incomplete bytes never signal that the original effect completed.
func (d *DropReplies) observeReply(id uint64, payload []byte) {
	buffer := append(d.replies[id], payload...)
	for len(buffer) > 0 {
		size, n := binary.Uvarint(buffer)
		if n < 0 || size > corewire.MaxMessage {
			delete(d.replies, id)
			return
		}
		if n == 0 || uint64(len(buffer)-n) < size {
			d.replies[id] = buffer
			return
		}
		var reply coreprotocol.Reply
		if _, err := coreprotocol.DecodePrefix(buffer[n:n+int(size)], &reply); err != nil {
			delete(d.replies, id)
			return
		}
		if reply.Final {
			d.once.Do(func() { close(d.Dropped) })
		}
		buffer = buffer[n+int(size):]
	}
	delete(d.replies, id)
}

// frames consumes every complete frame in buf, calling keep for each; kept
// frames are appended to the optional forward buffer. It returns the
// incomplete remainder.
func frames(buf []byte, keep func(id uint64, kind byte, payload []byte) bool, forward ...*[]byte) []byte {
	for {
		id, a := binary.Uvarint(buf)
		if a <= 0 || len(buf) < a+1 {
			return buf
		}
		kind := buf[a]
		size, b := binary.Uvarint(buf[a+1:])
		if b <= 0 || len(buf) < a+1+b+int(size) {
			return buf
		}
		end := a + 1 + b + int(size)
		if keep(id, kind, buf[a+1+b:end]) && len(forward) > 0 {
			*forward[0] = append(*forward[0], buf[:end]...)
		}
		buf = buf[end:]
	}
}
