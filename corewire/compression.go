package corewire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
)

// Network stream framing, independently in each direction:
//
//	mode byte (0 = identity, 1 = zstd)
//	identity: the remaining bytes are the application stream
//	zstd: repeated (uint32 raw length, uint32 encoded length, encoded bytes)
//
// Integers are big endian. A zero raw length is the mandatory final zstd
// trailer, followed by the carrier's FIN. Concatenating the encoded bytes is
// one standard zstd frame, NOT independent frames or a proprietary codec.
// Mode selection is explicit on the wire and fixed for the direction: control,
// input and a first write below 256 bytes use identity. Other directions retain
// one compressor/history until FIN. Every Write flushes synchronously; no timer,
// per-message goroutine or shared compression context can delay input or combine
// data from different streams/users. Callers must not reuse a stream across trust
// boundaries (the core itself is single-user).
//
// Zstd uses raw blocks when compression does not pay; their standard block/frame
// overhead and this record envelope are not claimed to be zero expansion.
const (
	compressionThreshold = 256
	compressionChunk     = 16 << 10
	compressionWindow    = 64 << 10
	compressionEncoded   = compressionChunk + 64
)

type compressedStream struct {
	Stream
	closed                                 atomic.Bool
	rmu, wmu                               sync.Mutex
	readStarted, writeStarted, writeClosed bool
	readMode, writeMode                    byte
	rerr, werr                             error
	enc                                    *zstd.Encoder
	encoded                                bytes.Buffer
	dec                                    *zstd.Decoder
	records                                compressionRecords
	decoded                                uint64
}

func networkStream(s Stream, carrier Carrier) Stream {
	if carrier == CarrierLocal {
		return s
	}
	return &compressedStream{Stream: s}
}

func (s *compressedStream) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.closed.Load() || s.writeClosed {
		return 0, ErrClosed
	}
	if s.werr != nil {
		return 0, s.werr
	}
	if len(p) == 0 {
		return 0, nil
	}
	if err := s.startWrite(len(p)); err != nil {
		return 0, s.writeFail(err)
	}
	if s.writeMode == 0 {
		return s.Stream.Write(p)
	}
	written := 0
	for len(p) > 0 {
		n := min(len(p), compressionChunk)
		s.encoded.Reset()
		if _, err := s.enc.Write(p[:n]); err != nil {
			return written, s.writeFail(err)
		}
		if err := s.enc.Flush(); err != nil {
			return written, s.writeFail(err)
		}
		if err := s.writeRecord(n); err != nil {
			return written, s.writeFail(err)
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

func (s *compressedStream) startWrite(size int) error {
	if s.writeStarted {
		return nil
	}
	s.writeStarted = true
	if s.Class() > ClassInput && size >= compressionThreshold {
		s.writeMode = 1
		var err error
		s.enc, err = zstd.NewWriter(&s.encoded, zstd.WithEncoderConcurrency(1),
			zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithWindowSize(compressionWindow),
			zstd.WithLowerEncoderMem(true), zstd.WithEncoderCRC(true))
		if err != nil {
			return err
		}
	}
	return writeAll(s.Stream, []byte{s.writeMode})
}

func (s *compressedStream) writeRecord(raw int) error {
	if s.encoded.Len() == 0 || s.encoded.Len() > compressionEncoded {
		return fmt.Errorf("%w: compression output bound", ErrProtocol)
	}
	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], uint32(raw))
	binary.BigEndian.PutUint32(header[4:], uint32(s.encoded.Len()))
	if err := writeAll(s.Stream, header[:]); err != nil {
		return err
	}
	return writeAll(s.Stream, s.encoded.Bytes())
}

func (s *compressedStream) writeFail(err error) error {
	s.werr = err
	if !compressionCarrierError(err) {
		_ = s.Stream.Close()
	}
	return err
}

func (s *compressedStream) CloseWrite() error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.closed.Load() {
		return ErrClosed
	}
	if s.writeClosed {
		return s.werr
	}
	s.writeClosed = true
	if s.werr != nil {
		return s.werr
	}
	if err := s.startWrite(0); err != nil {
		return s.writeFail(err)
	}
	if s.enc != nil {
		s.encoded.Reset()
		if err := s.enc.Close(); err != nil {
			return s.writeFail(err)
		}
		s.enc = nil
		if err := s.writeRecord(0); err != nil {
			return s.writeFail(err)
		}
	}
	s.werr = s.Stream.CloseWrite()
	return s.werr
}

func (s *compressedStream) Read(p []byte) (int, error) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	if s.closed.Load() {
		return 0, ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	if s.rerr != nil {
		return 0, s.rerr
	}
	if !s.readStarted {
		s.readStarted = true
		var mode [1]byte
		if _, err := io.ReadFull(s.Stream, mode[:]); err != nil {
			return 0, s.readFail(compressionError(err))
		}
		s.readMode = mode[0]
		if s.readMode > 1 {
			return 0, s.readFail(ErrProtocol)
		}
		if s.readMode == 1 {
			s.records.r = s.Stream
			var err error
			s.dec, err = zstd.NewReader(&s.records, zstd.WithDecoderConcurrency(1),
				zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxWindow(compressionWindow),
				zstd.WithDecoderMaxMemory(compressionWindow), zstd.WithDecodeBuffersBelow(0))
			if err != nil {
				return 0, s.readFail(compressionError(err))
			}
		}
	}
	if s.readMode == 0 {
		return s.Stream.Read(p)
	}
	n, err := s.dec.Read(p)
	s.decoded += uint64(n)
	if s.decoded > s.records.raw || err == io.EOF && (!s.records.ended || s.decoded != s.records.raw) {
		return 0, s.readFail(fmt.Errorf("%w: compressed raw length mismatch", ErrProtocol))
	}
	if err != nil {
		if err != io.EOF {
			err = compressionError(err)
		}
		s.rerr = err
		s.dec.Close()
		s.dec = nil
		if err != io.EOF && !compressionCarrierError(err) {
			_ = s.Stream.Close()
		}
	}
	return n, err
}

func (s *compressedStream) readFail(err error) error {
	s.rerr = err
	if s.dec != nil {
		s.dec.Close()
		s.dec = nil
	}
	if !compressionCarrierError(err) {
		_ = s.Stream.Close()
	}
	return err
}

func compressionError(err error) error {
	// Carrier cancellation/reset is not a clean compressed EOF or a codec fault.
	if compressionCarrierError(err) {
		return err
	}
	return fmt.Errorf("%w: compressed stream: %v", ErrProtocol, err)
}

func compressionCarrierError(err error) bool {
	return errors.Is(err, ErrClosed) || errors.Is(err, ErrReset) || errors.Is(err, ErrTimeout)
}

func (s *compressedStream) Close() error {
	s.closed.Store(true)
	// Interrupt blocked I/O BEFORE joining either direction's codec ownership.
	err := s.Stream.Close()
	s.wmu.Lock()
	if s.enc != nil {
		s.enc.Reset(io.Discard)
		_ = s.enc.Close()
		s.enc = nil
	}
	s.encoded = bytes.Buffer{}
	s.wmu.Unlock()
	s.rmu.Lock()
	if s.dec != nil {
		s.dec.Close()
		s.dec = nil
	}
	s.rmu.Unlock()
	return err
}

// compressionRecords strips bounded envelopes without allocating from peer
// lengths. The synchronous decoder consumes at most one bounded-window zstd block
// at a time. Cumulative output is checked against declared raw bytes on every
// Read and exactly at FIN; a forged small raw length cannot hide a bomb. Unlimited
// application streams remain unlimited; only each work unit/window is bounded.
type compressionRecords struct {
	r            io.Reader
	left         uint32
	raw          uint64
	final, ended bool
}

func (r *compressionRecords) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.ended {
		return 0, io.EOF
	}
	if r.left == 0 {
		if r.final {
			var extra [1]byte
			n, err := r.r.Read(extra[:])
			if n != 0 {
				return 0, ErrProtocol
			}
			if err == io.EOF {
				r.ended = true
			}
			return 0, err
		}
		var header [8]byte
		if _, err := io.ReadFull(r.r, header[:]); err != nil {
			return 0, compressionError(err)
		}
		raw, encoded := binary.BigEndian.Uint32(header[:4]), binary.BigEndian.Uint32(header[4:])
		if raw > compressionChunk || encoded == 0 || encoded > compressionEncoded || ^uint64(0)-r.raw < uint64(raw) {
			return 0, ErrProtocol
		}
		r.raw += uint64(raw)
		r.left, r.final = encoded, raw == 0
	}
	n, err := r.r.Read(p[:min(len(p), int(r.left))])
	r.left -= uint32(n)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}
