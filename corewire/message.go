package corewire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// MaxMessage bounds one application message on any stream. Larger transfers
// are split by their owner into bulk chunks.
const MaxMessage = 64 << 20

// headerReserve is room for the uvarint length in front of a message body.
const headerReserve = binary.MaxVarintLen64

var ErrMessageTooLarge = errors.New("corewire: message exceeds the size limit")

var messagePool = sync.Pool{New: func() any { b := make([]byte, headerReserve, 4096); return &b }}

// Message is an outgoing message buffer with room reserved for its length
// prefix. Append the body to B, send it with Send, then Release it.
type Message struct{ B []byte }

// NewMessage returns a pooled buffer whose B already holds the reserved prefix.
func NewMessage() *Message {
	b := messagePool.Get().(*[]byte)
	return &Message{B: (*b)[:headerReserve]}
}

// Body is the encoded message without the reserved prefix.
func (m *Message) Body() []byte { return m.B[headerReserve:] }

// Release returns the buffer to the pool; m must not be used afterwards.
// Very large buffers are dropped so the pool never pins bulk transfers.
func (m *Message) Release() {
	if cap(m.B) <= 1<<20 {
		b := m.B[:headerReserve]
		messagePool.Put(&b)
	}
	m.B = nil
}

// Framed returns the length-prefixed wire bytes, prefix written in place.
func (m *Message) Framed() ([]byte, error) {
	body := len(m.B) - headerReserve
	if body > MaxMessage {
		return nil, ErrMessageTooLarge
	}
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], uint64(body))
	start := headerReserve - n
	copy(m.B[start:], prefix[:n])
	return m.B[start:], nil
}

// Send writes the message to w in one write.
func (m *Message) Send(w io.Writer) error {
	b, err := m.Framed()
	if err != nil {
		return err
	}
	return writeAll(w, b)
}

// WriteMessage frames body and writes it in one write. Hot paths build a
// Message instead to avoid the copy.
func WriteMessage(w io.Writer, body []byte) error {
	if len(body) > MaxMessage {
		return ErrMessageTooLarge
	}
	m := NewMessage()
	defer m.Release()
	m.B = append(m.B, body...)
	return m.Send(w)
}

// Reader splits a stream into messages. The slice Next returns aliases an
// internal buffer and stays valid only until the following Next.
type Reader struct {
	r     io.Reader
	limit int
	buf   []byte
	start int
	end   int
	err   error // terminal read error, delivered after its accompanying bytes
}

func NewReader(r io.Reader, limit int) *Reader {
	if limit <= 0 || limit > MaxMessage {
		limit = MaxMessage
	}
	return &Reader{r: r, limit: limit, buf: make([]byte, 4096)}
}

// SetLimit narrows the next message's bound without discarding bytes already
// buffered after the role header. Call between Next calls on the owning reader.
func (r *Reader) SetLimit(limit int) {
	if limit <= 0 || limit > MaxMessage {
		limit = MaxMessage
	}
	r.limit = limit
}

// Next returns the next message body. io.EOF means the peer ended the stream
// cleanly between messages; a stream ending mid-message is io.ErrUnexpectedEOF.
func (r *Reader) Next() ([]byte, error) {
	size, n, err := r.length()
	if err != nil {
		return nil, err
	}
	if size > uint64(r.limit) {
		return nil, fmt.Errorf("%w: %d bytes", ErrMessageTooLarge, size)
	}
	need := n + int(size)
	if err := r.fill(need); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	body := r.buf[r.start+n : r.start+need]
	r.start += need
	return body, nil
}

// length decodes the uvarint prefix, reading more only while it is incomplete.
func (r *Reader) length() (uint64, int, error) {
	if r.start == r.end && len(r.buf) > 1<<20 {
		r.buf, r.start, r.end = make([]byte, 4096), 0, 0
	}
	for {
		if v, n := binary.Uvarint(r.buf[r.start:r.end]); n > 0 {
			return v, n, nil
		} else if n < 0 {
			return 0, 0, fmt.Errorf("%w: invalid message length", ErrProtocol)
		}
		available := r.end - r.start
		if err := r.fill(available + 1); err != nil {
			if errors.Is(err, io.EOF) && available > 0 {
				err = io.ErrUnexpectedEOF
			}
			return 0, 0, err
		}
	}
}

// fill ensures at least need unread bytes are buffered. The buffer grows
// geometrically as bytes arrive, so a declared length alone allocates nothing.
func (r *Reader) fill(need int) error {
	if r.end-r.start >= need {
		return nil
	}
	if r.start > 0 {
		r.end = copy(r.buf, r.buf[r.start:r.end])
		r.start = 0
	}
	for r.end < need {
		if r.err != nil {
			return r.err
		}
		if r.end == len(r.buf) {
			grown := make([]byte, min(need, max(2*len(r.buf), 4096)))
			copy(grown, r.buf[:r.end])
			r.buf = grown
		}
		n, err := r.r.Read(r.buf[r.end:])
		r.end += n
		r.err = err
		if n == 0 && err == nil {
			return io.ErrNoProgress
		}
	}
	return nil
}
