// Package corewire implements the bounded, versioned transport boundary between
// a user-owned core and its clients. It does not dispatch methods or own runtime state.
package corewire

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

const ProtocolVersion = 1
const MaxFrameSize = 8 << 20

// MaxPayloadSize bounds a complete logical response assembled from immutable
// connection-owned chunks. Encoded frames still obey MaxFrameSize.
const MaxPayloadSize = 64 << 20

type PayloadTransfer struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

const (
	KindHello    = "hello"
	KindWelcome  = "welcome"
	KindRequest  = "request"
	KindResponse = "response"
	KindEvent    = "event"
	KindResync   = "resync"
)

type Fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Frame struct {
	Version     int              `json:"version"`
	Kind        string           `json:"kind"`
	RequestID   string           `json:"request_id,omitempty"`
	ClientID    string           `json:"client_id,omitempty"`
	WorkspaceID string           `json:"workspace_id,omitempty"`
	Epoch       uint64           `json:"epoch,omitempty"`
	Revision    uint64           `json:"revision,omitempty"`
	Method      string           `json:"method,omitempty"`
	Payload     json.RawMessage  `json:"payload,omitempty"`
	Error       *Fault           `json:"error,omitempty"`
	Transfer    *PayloadTransfer `json:"transfer,omitempty"`
}

func (f Frame) validateMetadata() error {
	if f.Version != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version %d", f.Version)
	}
	switch f.Kind {
	case KindHello, KindWelcome, KindRequest, KindResponse, KindEvent, KindResync:
	default:
		return errors.New("invalid frame kind")
	}
	// Metadata is bounded independently of the potentially large document payload.
	for _, s := range []string{f.RequestID, f.ClientID, f.WorkspaceID, f.Method} {
		if len(s) > 65536 {
			return errors.New("frame metadata too large")
		}
	}
	if f.Error != nil && (len(f.Error.Code) > 65536 || len(f.Error.Message) > 65536) {
		return errors.New("fault too large")
	}
	if f.Transfer != nil {
		if f.Kind != KindResponse || f.Error != nil || len(f.Payload) != 0 || f.Transfer.ID == "" || len(f.Transfer.ID) > 256 || len(f.Transfer.Digest) != 64 || f.Transfer.Size <= 0 || f.Transfer.Size > MaxPayloadSize {
			return errors.New("invalid response transfer")
		}
	}
	if len(f.Payload) > MaxFrameSize {
		return errors.New("invalid or oversized payload")
	}
	return nil
}

func (f Frame) validate() error {
	if err := f.validateMetadata(); err != nil {
		return err
	}
	if len(f.Payload) > 0 && !json.Valid(f.Payload) {
		return errors.New("invalid payload")
	}
	return nil
}

// EncodedFrame is immutable, validated output. Preparing it once lets bounded
// queues account exact bytes without repeatedly compacting large raw JSON.
type EncodedFrame struct{ data []byte }

func (f EncodedFrame) Len() int { return len(f.data) }

func FrameSize(frame Frame) (int, error) {
	payload := frame.Payload
	frame.Payload = nil
	metadata, err := json.Marshal(frame)
	if err != nil {
		return 0, err
	}
	if len(payload) == 0 {
		return len(metadata), nil
	}
	return len(metadata) + len(payload) + len(`,"payload":`), nil
}

func EncodeFrame(frame Frame) (EncodedFrame, error) {
	if err := frame.validate(); err != nil {
		return EncodedFrame{}, err
	}
	payload := frame.Payload
	frame.Payload = nil
	metadata, err := json.Marshal(frame)
	if err != nil {
		return EncodedFrame{}, err
	}
	if len(payload) == 0 {
		return EncodedFrame{data: metadata}, nil
	}
	size := len(metadata) + len(payload) + len(`,"payload":`)
	if size > MaxFrameSize {
		return EncodedFrame{}, errors.New("frame exceeds maximum")
	}
	data := make([]byte, 0, size)
	data = append(data, metadata[:len(metadata)-1]...)
	data = append(data, `,"payload":`...)
	data = append(data, payload...)
	data = append(data, '}')
	return EncodedFrame{data: data}, nil
}

// Framer owns connection cancellation. Any framing error closes the connection;
// callers must not reuse a stream after malformed input or a partial write.
// Reads and writes are independently serialized; writes never interleave.
type Framer struct {
	conn     io.ReadWriteCloser
	rmu, wmu sync.Mutex
	once     sync.Once
	done     chan struct{}
	max      atomic.Int64
}

func NewFramer(ctx context.Context, conn io.ReadWriteCloser) *Framer {
	f := &Framer{conn: conn, done: make(chan struct{})}
	f.max.Store(MaxFrameSize)
	go func() {
		select {
		case <-ctx.Done():
			_ = f.Close()
		case <-f.done:
		}
	}()
	return f
}

func (f *Framer) Close() error {
	var err error
	f.once.Do(func() { close(f.done); err = f.conn.Close() })
	return err
}

func (f *Framer) Read() (Frame, error) {
	f.rmu.Lock()
	defer f.rmu.Unlock()
	var frame Frame
	var header [4]byte
	if _, err := io.ReadFull(f.conn, header[:]); err != nil {
		_ = f.Close()
		return frame, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || int64(n) > f.max.Load() {
		_ = f.Close()
		return frame, errors.New("invalid frame length")
	}
	b := make([]byte, int(n))
	if _, err := io.ReadFull(f.conn, b); err != nil {
		_ = f.Close()
		return frame, err
	}
	if err := strictJSON(b, &frame); err != nil {
		_ = f.Close()
		return Frame{}, err
	}
	// strictJSON has already validated every nested payload byte and key.
	if err := frame.validateMetadata(); err != nil {
		_ = f.Close()
		return Frame{}, err
	}
	return frame, nil
}

// strictJSON rejects unknown envelope fields, trailing values, duplicate object
// keys (including payload keys), and excessive nesting rather than guessing intent.
func strictJSON(b []byte, dst any) error {
	if !utf8.Valid(b) {
		return errors.New("invalid UTF-8 JSON")
	}
	if !json.Valid(b) {
		return errors.New("invalid JSON")
	}
	// Inspect only object keys, not decoded string values. Token decoding used
	// to allocate and scan every large base64 chunk again solely to detect keys.
	scan := jsonKeys{data: b}
	if err := scan.value(0); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

type jsonKeys struct {
	data []byte
	pos  int
}

func (s *jsonKeys) space() {
	for s.pos < len(s.data) && (s.data[s.pos] == ' ' || s.data[s.pos] == '\n' || s.data[s.pos] == '\r' || s.data[s.pos] == '\t') {
		s.pos++
	}
}
func (s *jsonKeys) quoted() []byte {
	start := s.pos
	s.pos++
	for s.pos < len(s.data) {
		// Search quotes and escapes in native byte operations; ordinary response
		// byte strings contain neither escapes nor nested JSON syntax.
		rest := s.data[s.pos:]
		quote := bytes.IndexByte(rest, '"')
		escape := bytes.IndexByte(rest[:quote], '\\')
		if escape >= 0 {
			s.pos += escape + 2
			continue
		}
		s.pos += quote + 1
		return s.data[start:s.pos]
	}
	return nil
}
func (s *jsonKeys) value(depth int) error {
	s.space()
	switch s.data[s.pos] {
	case '{':
		if depth >= 128 {
			return errors.New("JSON nesting exceeds limit")
		}
		s.pos++
		s.space()
		seen := make(map[string]bool)
		for s.data[s.pos] != '}' {
			raw := s.quoted()
			var key string
			if err := json.Unmarshal(raw, &key); err != nil {
				return err
			}
			if seen[key] {
				return errors.New("duplicate JSON key")
			}
			seen[key] = true
			s.space()
			s.pos++
			if err := s.value(depth + 1); err != nil {
				return err
			}
			s.space()
			if s.data[s.pos] == ',' {
				s.pos++
				s.space()
			} else {
				break
			}
		}
		s.pos++
	case '[':
		if depth >= 128 {
			return errors.New("JSON nesting exceeds limit")
		}
		s.pos++
		s.space()
		for s.data[s.pos] != ']' {
			if err := s.value(depth + 1); err != nil {
				return err
			}
			s.space()
			if s.data[s.pos] == ',' {
				s.pos++
			} else {
				break
			}
		}
		s.pos++
	case '"':
		s.quoted()
	default:
		for s.pos < len(s.data) && s.data[s.pos] != ',' && s.data[s.pos] != ']' && s.data[s.pos] != '}' {
			s.pos++
		}
	}
	return nil
}

func (f *Framer) Write(frame Frame) error {
	encoded, err := EncodeFrame(frame)
	if err != nil {
		_ = f.Close()
		return err
	}
	return f.WriteEncoded(encoded)
}

func (f *Framer) WriteEncoded(encoded EncodedFrame) error {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	b := encoded.data
	if len(b) == 0 {
		_ = f.Close()
		return errors.New("empty prepared frame")
	}
	if int64(len(b)) > f.max.Load() {
		_ = f.Close()
		return errors.New("frame exceeds negotiated maximum")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	for _, part := range [][]byte{h[:], b} {
		for len(part) > 0 {
			n, e := f.conn.Write(part)
			if e != nil {
				_ = f.Close()
				return e
			}
			if n <= 0 || n > len(part) {
				_ = f.Close()
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}

type negotiation struct {
	MaxFrame int `json:"max_frame"`
}

// NegotiateClient must run before application traffic, with no competing reads.
// Version 1 is exact, not a best-effort downgrade. The returned cap includes JSON.
func (f *Framer) NegotiateClient(clientID string, maximum int) (int, error) {
	if maximum <= 0 || maximum > MaxFrameSize {
		return 0, errors.New("invalid maximum")
	}
	b, _ := json.Marshal(negotiation{maximum})
	if err := f.Write(Frame{Version: ProtocolVersion, Kind: KindHello, ClientID: clientID, Payload: b}); err != nil {
		return 0, err
	}
	r, err := f.Read()
	if err != nil {
		return 0, err
	}
	var n negotiation
	if r.Kind != KindWelcome || r.ClientID != clientID || r.Error != nil || strictJSON(r.Payload, &n) != nil || n.MaxFrame <= 0 || n.MaxFrame > maximum {
		_ = f.Close()
		return 0, errors.New("invalid welcome")
	}
	f.max.Store(int64(n.MaxFrame))
	return n.MaxFrame, nil
}

// NegotiateServer returns the peer identity for the application to register.
func (f *Framer) NegotiateServer(maximum int) (string, int, error) {
	if maximum <= 0 || maximum > MaxFrameSize {
		return "", 0, errors.New("invalid maximum")
	}
	r, err := f.Read()
	if err != nil {
		return "", 0, err
	}
	var n negotiation
	if r.Kind != KindHello || r.ClientID == "" || r.Error != nil || strictJSON(r.Payload, &n) != nil || n.MaxFrame <= 0 || n.MaxFrame > MaxFrameSize {
		_ = f.Close()
		return "", 0, errors.New("invalid hello")
	}
	if n.MaxFrame > maximum {
		n.MaxFrame = maximum
	}
	b, _ := json.Marshal(n)
	if err := f.Write(Frame{Version: ProtocolVersion, Kind: KindWelcome, ClientID: r.ClientID, Payload: b}); err != nil {
		return "", 0, err
	}
	f.max.Store(int64(n.MaxFrame))
	return r.ClientID, n.MaxFrame, nil
}
