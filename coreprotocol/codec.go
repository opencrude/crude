package coreprotocol

import (
	"errors"

	"github.com/tinylib/msgp/msgp"

	"crude/localhistory"
	"crude/sessiondb"
	"crude/types/control"
	"crude/workspace"
)

// Value is anything the protocol carries: a Go type with a generated codec.
type Value interface {
	msgp.Marshaler
	msgp.Sizer
}

// Target is a value a protocol message decodes into.
type Target interface {
	msgp.Unmarshaler
}

// Empty is the request or result of a method that carries no value.
type Empty struct{}

var ErrTrailingBytes = errors.New("coreprotocol: trailing bytes after value")

// Encode appends v's encoding to dst.
func Encode(dst []byte, v Value) ([]byte, error) { return v.MarshalMsg(dst) }

// Marshal returns v's encoding in a new buffer.
func Marshal(v Value) ([]byte, error) { return v.MarshalMsg(make([]byte, 0, v.Msgsize())) }

// Decode fills v from exactly b.
func Decode(b []byte, v Target) error {
	rest, err := v.UnmarshalMsg(b)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return ErrTrailingBytes
	}
	return nil
}

// HandshakeIdentity reads only the frozen leading [Schema, Build] of a Hello or
// Welcome encoded by any build, whatever the rest of its tuple holds. The body
// must still be exactly one well-formed value: corrupt or trailing bytes are
// malformed, not evidence of another build.
func HandshakeIdentity(b []byte) (schema, build string, err error) {
	if rest, err := msgp.Skip(b); err != nil || len(rest) != 0 {
		return "", "", errors.Join(ErrTrailingBytes, err)
	}
	n, b, err := msgp.ReadArrayHeaderBytes(b)
	if err != nil {
		return "", "", err
	}
	if n < 2 {
		return "", "", msgp.ArrayError{Wanted: 2, Got: n}
	}
	if schema, b, err = msgp.ReadStringBytes(b); err != nil {
		return "", "", err
	}
	build, _, err = msgp.ReadStringBytes(b)
	return schema, build, err
}

// DecodePrefix fills v from the start of b and returns the remainder: a
// message is a header value followed directly by its payload value.
func DecodePrefix(b []byte, v Target) ([]byte, error) { return v.UnmarshalMsg(b) }

// Collections that methods return as a whole.
type (
	ClientDirectoryEntries []ClientDirectoryEntry
	ControlDescriptors     []control.Descriptor
	ControlShellInfos      []ControlShellInfo
	SessionRegistry        []sessiondb.SessionMeta
	SessionSnapshotList    []SessionSnapshot
	SnapshotRecords        []localhistory.SnapshotRecord
	TerminalInfos          []workspace.TerminalInfo
	WorkspaceInfos         []workspace.WorkspaceInfo
)

// Ptr boxes a value whose codec has pointer receivers.
func Ptr[T any](v T) *T { return &v }
