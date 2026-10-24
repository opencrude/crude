// Package corewire is the carrier boundary between a user-owned core and its
// clients: multiplexed, credit-controlled streams over QUIC or over any
// reliable byte stream (local socket, named pipe, SSH stdio, TCP). It does not
// encode application values, dispatch methods or own runtime state.
package corewire

import (
	"context"
	"errors"
	"io"
	"time"
)

// Class orders a stream's output on carriers that share one byte stream. Lower
// classes have preference; bounded weighted service prevents starvation. A
// keystroke never waits behind an unbounded bulk transfer.
type Class uint8

const (
	// ClassControl carries the session handshake and session-level traffic.
	ClassControl Class = iota
	// ClassInput carries ordered user input (terminal keys, pointer, paste).
	ClassInput
	// ClassInteractive carries commands, topic updates and terminal frames.
	ClassInteractive
	// ClassBulk carries large transfers (file content, media).
	ClassBulk
	classCount
)

func (c Class) valid() bool { return c < classCount }

// Carrier names the transport under a connection, for status and pacing.
type Carrier string

const (
	CarrierLocal Carrier = "local"
	CarrierSSH   Carrier = "ssh"
	CarrierTCP   Carrier = "tcp"
	CarrierQUIC  Carrier = "quic"
)

var (
	// ErrClosed reports use of a stream or connection after it ended.
	ErrClosed = errors.New("corewire: closed")
	// ErrReset reports that the peer abandoned the stream.
	ErrReset = errors.New("corewire: stream reset by peer")
	// ErrTimeout reports a carrier whose peer stopped answering heartbeats.
	ErrTimeout = errors.New("corewire: peer stopped responding")
	// ErrProtocol reports a peer that violated framing or flow control.
	ErrProtocol = errors.New("corewire: protocol violation")
	// ErrRefused reports a stream the peer could not accept.
	ErrRefused = errors.New("corewire: stream refused")
)

// Stream is one ordered, reliable, flow-controlled byte stream. Reads and
// writes may run concurrently with each other; each direction is used by one
// goroutine at a time.
type Stream interface {
	io.Reader
	io.Writer
	// CloseWrite ends this side's data; the peer reads io.EOF after it.
	CloseWrite() error
	// Close abandons both directions. A peer still reading or writing the
	// stream observes ErrReset.
	Close() error
	Class() Class
}

// Conn is one carrier lifetime. Sessions above it outlive any one Conn.
type Conn interface {
	OpenStream(ctx context.Context, class Class) (Stream, error)
	AcceptStream(ctx context.Context) (Stream, error)
	Close() error
	Done() <-chan struct{}
	// Err is the reason Done closed.
	Err() error
	// RTT is the smoothed round-trip estimate, zero until measured.
	RTT() time.Duration
	Carrier() Carrier
}
