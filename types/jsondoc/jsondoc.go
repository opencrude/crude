// Package jsondoc carries free-form JSON objects (provider request extensions,
// language server settings, tool arguments) with exact JSON semantics: on the
// core protocol they travel as their JSON text, so numbers, nesting and nulls
// decode exactly as they would from the configuration they came from.
package jsondoc

import (
	"encoding/json"

	"github.com/tinylib/msgp/msgp"
)

// Object is a JSON object. A nil Object carries JSON null, not an empty object.
// The enclosing optional pointer, when any, owns absence on the wire.
type Object map[string]any

// MarshalMsg always appends JSON text as MessagePack bytes. In particular null
// must not collide with the enclosing pointer's MessagePack nil sentinel.
func (o Object) MarshalMsg(b []byte) ([]byte, error) {
	text, err := json.Marshal(map[string]any(o))
	if err != nil {
		return b, err
	}
	return msgp.AppendBytes(b, text), nil
}

// UnmarshalMsg decodes an object written by MarshalMsg.
func (o *Object) UnmarshalMsg(b []byte) ([]byte, error) {
	text, rest, err := msgp.ReadBytesZC(b)
	if err != nil {
		return b, err
	}
	var value map[string]any
	if err := json.Unmarshal(text, &value); err != nil {
		return b, err
	}
	*o = value
	return rest, nil
}

// Msgsize estimates MarshalMsg's output without encoding; buffers grow if
// the estimate is short.
func (o Object) Msgsize() int { return msgp.BytesPrefixSize + 4 + 64*len(o) }
