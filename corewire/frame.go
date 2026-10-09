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
	Version     int             `json:"version"`
	Kind        string          `json:"kind"`
	RequestID   string          `json:"request_id,omitempty"`
	ClientID    string          `json:"client_id,omitempty"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Epoch       uint64          `json:"epoch,omitempty"`
	Revision    uint64          `json:"revision,omitempty"`
	Method      string          `json:"method,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Error       *Fault          `json:"error,omitempty"`
}

func (f Frame) validate() error {
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
	if len(f.Payload) > MaxFrameSize || (len(f.Payload) > 0 && !json.Valid(f.Payload)) {
		return errors.New("invalid or oversized payload")
	}
	return nil
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
	if err := frame.validate(); err != nil {
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
	d := json.NewDecoder(bytes.NewReader(b))
	var stack []map[string]bool
	var keys []bool
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch v := t.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				if len(stack) >= 128 {
					return errors.New("JSON nesting exceeds limit")
				}
				if len(keys) > 0 && stack[len(stack)-1] != nil {
					keys[len(keys)-1] = true
				}
				var m map[string]bool
				if v == '{' {
					m = make(map[string]bool)
				}
				stack = append(stack, m)
				keys = append(keys, true)
			case '}', ']':
				stack = stack[:len(stack)-1]
				keys = keys[:len(keys)-1]
			}
		default:
			if len(stack) > 0 && stack[len(stack)-1] != nil {
				i := len(stack) - 1
				if keys[i] {
					s, ok := v.(string)
					if !ok || stack[i][s] {
						return errors.New("duplicate or invalid JSON key")
					}
					stack[i][s] = true
				}
				keys[i] = !keys[i]
			}
		}
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

func (f *Framer) Write(frame Frame) error {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	if err := frame.validate(); err != nil {
		_ = f.Close()
		return err
	}
	b, err := json.Marshal(frame)
	if err != nil {
		_ = f.Close()
		return err
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
