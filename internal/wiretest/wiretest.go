// Package wiretest encodes and decodes protocol values held as interfaces,
// for tests that build requests generically. Product code passes typed values
// so the compiler checks every codec.
package wiretest

import (
	"fmt"
	"reflect"

	"crude/coreprotocol"
)

// Marshal encodes v; v or a pointer to it must have a generated codec.
func Marshal(v any) ([]byte, error) {
	value, ok := v.(coreprotocol.Value)
	if !ok {
		p := reflect.New(reflect.TypeOf(v))
		p.Elem().Set(reflect.ValueOf(v))
		if value, ok = p.Interface().(coreprotocol.Value); !ok {
			return nil, fmt.Errorf("%T has no protocol codec", v)
		}
	}
	return coreprotocol.Marshal(value)
}

// Decode decodes b into out, a pointer to a type with a generated codec.
func Decode(b []byte, out any) error {
	target, ok := out.(coreprotocol.Target)
	if !ok {
		return fmt.Errorf("%T has no protocol codec", out)
	}
	return coreprotocol.Decode(b, target)
}
