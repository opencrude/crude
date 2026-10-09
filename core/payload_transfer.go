package core

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sync"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

const payloadTransferCount = 4
const payloadTransferTotal = 128 << 20
const payloadTransferTTL = 30 * time.Second

type storedPayload struct {
	data    json.RawMessage
	digest  string
	timer   *time.Timer
	expires time.Time
}

type payloadTransfers struct {
	mu      sync.Mutex
	entries map[string]*storedPayload
	bytes   int
	maximum int
	closed  bool
}

func newPayloadTransfers(maximum ...int) *payloadTransfers {
	m := corewire.MaxFrameSize
	if len(maximum) > 0 && maximum[0] > 0 && maximum[0] < m {
		m = maximum[0]
	}
	return &payloadTransfers{entries: make(map[string]*storedPayload), maximum: m}
}

func (s *payloadTransfers) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, p := range s.entries {
		p.timer.Stop()
	}
	s.entries = make(map[string]*storedPayload)
	s.bytes = 0
}

func payloadFault(frame corewire.Frame, message string) corewire.Frame {
	frame.Payload = nil
	frame.Transfer = nil
	frame.Error = &corewire.Fault{Code: "payload_transfer", Message: message}
	return frame
}

// WrapFrame detaches large immutable JSON responses before they enter the small
// output queue. Runtime/journal responses remain untouched, including receipts.
func (s *payloadTransfers) WrapFrame(frame corewire.Frame) corewire.Frame {
	if frame.Kind != corewire.KindResponse || frame.Transfer != nil || len(frame.Payload) == 0 {
		return frame
	}
	if len(frame.Payload) <= s.maximum {
		size, err := corewire.FrameSize(frame)
		if err != nil {
			return payloadFault(frame, "invalid response JSON")
		}
		if size <= s.maximum {
			return frame
		}
	}
	if len(frame.Payload) > coreprotocol.MaxPayloadBytes || !json.Valid(frame.Payload) {
		return payloadFault(frame, "response exceeds logical payload limit or is invalid JSON")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return payloadFault(frame, "cannot allocate transfer identity")
	}
	id := hex.EncodeToString(random[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.entries) >= payloadTransferCount || s.bytes+len(frame.Payload) > payloadTransferTotal {
		return payloadFault(frame, "connection payload capacity exceeded")
	}
	digest := sha256.Sum256(frame.Payload)
	p := &storedPayload{data: append(json.RawMessage(nil), frame.Payload...), digest: hex.EncodeToString(digest[:]), expires: time.Now().Add(payloadTransferTTL)}
	s.entries[id] = p
	s.bytes += len(p.data)
	p.timer = time.AfterFunc(payloadTransferTTL, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.entries[id] == p {
			// A read may have renewed the lease while this expired callback was
			// waiting for the lock. Never retire active immutable evidence then.
			if remaining := time.Until(p.expires); remaining > 0 {
				p.timer.Reset(remaining)
				return
			}
			delete(s.entries, id)
			s.bytes -= len(p.data)
		}
	})
	frame.Payload = nil
	frame.Transfer = &corewire.PayloadTransfer{ID: id, Digest: p.digest, Size: int64(len(p.data))}
	return frame
}

func decodePayloadRequest(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// ReadFrame must be called only after validating the immutable negotiated owner.
// A token from any other connection has no entry here and grants no authority.
func (s *payloadTransfers) ReadFrame(request corewire.Frame) (corewire.Frame, bool) {
	if request.Method != coreprotocol.PayloadRead && request.Method != coreprotocol.PayloadRelease {
		return corewire.Frame{}, false
	}
	response := request
	response.Kind = corewire.KindResponse
	response.Payload = nil
	response.Transfer = nil
	response.Error = nil
	var read coreprotocol.PayloadReadRequest
	if request.Method == coreprotocol.PayloadRelease {
		var release coreprotocol.PayloadReleaseRequest
		if decodePayloadRequest(request.Payload, &release) != nil {
			return payloadFault(response, "invalid release request"), true
		}
		read.ID, read.Digest = release.ID, release.Digest
	} else if decodePayloadRequest(request.Payload, &read) != nil || read.Offset < 0 || read.Limit <= 0 || read.Limit > coreprotocol.PayloadChunkBytes {
		return payloadFault(response, "invalid chunk request"), true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.entries[read.ID]
	if s.closed || p == nil || !time.Now().Before(p.expires) || read.Digest != p.digest {
		return payloadFault(response, "unknown, expired or mismatched transfer"), true
	}
	if request.Method == coreprotocol.PayloadRelease {
		delete(s.entries, read.ID)
		s.bytes -= len(p.data)
		p.timer.Stop()
		response.Payload = json.RawMessage(`{}`)
		return response, true
	}
	if read.Offset >= int64(len(p.data)) {
		return payloadFault(response, "chunk offset outside payload"), true
	}
	// The lease is an inactivity bound, not an arbitrary deadline on a legitimate
	// dense response. Progress renews it; a disconnected/abandoned reader expires.
	p.expires = time.Now().Add(payloadTransferTTL)
	p.timer.Reset(payloadTransferTTL)
	// Keep base64 JSON data plus envelope within tiny negotiated frame bounds.
	// Standard clients retain 256 KiB pages; smaller peers receive safe progress.
	available := max(0, (s.maximum-2048)*3/4)
	if available == 0 {
		return payloadFault(response, "negotiated frame is too small for a payload chunk"), true
	}
	read.Limit = min(read.Limit, available)
	next := read.Offset + int64(read.Limit)
	if next > int64(len(p.data)) {
		next = int64(len(p.data))
	}
	chunk := coreprotocol.PayloadChunk{Data: p.data[read.Offset:next], Offset: read.Offset, Next: next, Total: int64(len(p.data)), Digest: p.digest, EOF: next == int64(len(p.data))}
	response.Payload, _ = json.Marshal(chunk)
	return response, true
}
