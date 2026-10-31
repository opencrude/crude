package jsonrpc2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Request represents a JSON-RPC request or
// notification. See
// http://www.jsonrpc.org/specification#request_object and
// http://www.jsonrpc.org/specification#notification.
type Request struct {
	Method string           `json:"method"`
	Params *json.RawMessage `json:"params,omitempty"`
	ID     ID               `json:"id"`
	Notif  bool             `json:"-"`

	// Meta optionally provides metadata to include in the request.
	//
	// NOTE: It is not part of spec. However, it is useful for propagating
	// tracing context, etc.
	Meta *json.RawMessage `json:"meta,omitempty"`

	// ExtraFields optionally adds fields to the root of the JSON-RPC request.
	//
	// NOTE: It is not part of the spec, but there are other protocols based on
	// JSON-RPC 2 that require it.
	ExtraFields []RequestField `json:"-"`
}

// MarshalJSON implements json.Marshaler and adds the "jsonrpc":"2.0"
// property.
func (r Request) MarshalJSON() ([]byte, error) {
	r2 := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  r.Method,
	}
	for _, field := range r.ExtraFields {
		r2[field.Name] = field.Value
	}
	if !r.Notif {
		r2["id"] = &r.ID
	}
	if r.Params != nil {
		r2["params"] = r.Params
	}
	if r.Meta != nil {
		r2["meta"] = r.Meta
	}
	return json.Marshal(r2)
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *Request) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if start != json.Delim('{') {
		return errors.New("request must be an object")
	}
	// Decode into a fresh value: errors must leave the receiver untouched.
	next := Request{Notif: true}
	seen := make(map[string]bool)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("request member name must be a string")
		}
		if seen[name] {
			return fmt.Errorf("duplicate request member %q", name)
		}
		seen[name] = true
		if name == "params" || name == "meta" {
			// RawMessage copies the original value bytes, including explicit
			// null and opaque spellings. Do not interpret nested payloads here.
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				return err
			}
			if name == "params" {
				next.Params = &raw
			} else {
				next.Meta = &raw
			}
			continue
		}
		var value interface{}
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		switch name {
		case "method":
			next.Method, ok = value.(string)
			if !ok {
				return errors.New("missing method field")
			}
		case "id":
			switch id := value.(type) {
			case nil: // Preserve upstream's explicit-null notification behavior.
			case string:
				next.ID = ID{Str: id, IsString: true}
				next.Notif = false
			case json.Number:
				n, err := id.Int64()
				if err != nil {
					return fmt.Errorf("failed to unmarshal ID: %w", err)
				}
				next.ID = ID{Num: uint64(n)}
				next.Notif = false
			default:
				return fmt.Errorf("unexpected ID type: %T", id)
			}
		case "jsonrpc": // As upstream, ignore the protocol extension field.
		default:
			next.ExtraFields = append(next.ExtraFields, RequestField{Name: name, Value: value})
		}
	}
	if _, err := decoder.Token(); err != nil { // closing brace
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("unexpected value after request")
	}
	if !seen["method"] {
		return errors.New("missing method field")
	}
	*r = next
	return nil
}

// SetParams sets r.Params to the JSON encoding of v. If JSON
// marshaling fails, it returns an error.
func (r *Request) SetParams(v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.Params = (*json.RawMessage)(&b)
	return nil
}

// SetMeta sets r.Meta to the JSON encoding of v. If JSON
// marshaling fails, it returns an error.
func (r *Request) SetMeta(v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.Meta = (*json.RawMessage)(&b)
	return nil
}

// SetExtraField adds an entry to r.ExtraFields, so that it is added to the
// JSON encoding of the request, as a way to add arbitrary extensions to
// JSON RPC 2.0. If JSON marshaling fails, it returns an error.
func (r *Request) SetExtraField(name string, v interface{}) error {
	switch name {
	case "id", "jsonrpc", "meta", "method", "params":
		return fmt.Errorf("invalid extra field %q", name)
	}
	r.ExtraFields = append(r.ExtraFields, RequestField{
		Name:  name,
		Value: v,
	})
	return nil
}

// RequestField is a top-level field that can be added to the JSON-RPC request.
type RequestField struct {
	Name  string
	Value interface{}
}
