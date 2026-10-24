package coreprotocol_test

import (
	"math/rand/v2"
	"reflect"
	"testing"

	"crude/coreprotocol"
)

// Every generated type is a seed, not only a convenient envelope. Native fuzz
// mutations then cross type, tuple, length, collection and payload boundaries.
func FuzzEveryWireDecoder(f *testing.F) {
	rng := rand.New(rand.NewPCG(81, 109))
	for i, prototype := range wireTypes {
		typ := reflect.TypeOf(prototype).Elem()
		for _, populated := range []bool{false, true} {
			value := reflect.New(typ)
			if populated {
				fill(value.Elem(), rng, 0)
			}
			body, err := coreprotocol.Marshal(value.Interface().(coreprotocol.Value))
			if err != nil {
				f.Fatalf("%s seed: %v", typ, err)
			}
			f.Add(uint16(i), body)
		}
	}
	for _, hostile := range [][]byte{
		{0xdd, 0xff, 0xff, 0xff, 0xff},
		{0xdf, 0xff, 0xff, 0xff, 0xff},
		{0xdb, 0xff, 0xff, 0xff, 0xff},
		{0xc6, 0xff, 0xff, 0xff, 0xff},
		{0xc1}, {0xc0}, {0x90},
	} {
		f.Add(uint16(0), hostile)
	}
	f.Fuzz(func(t *testing.T, selector uint16, body []byte) {
		if len(body) > 128<<10 {
			t.Skip()
		}
		typ := reflect.TypeOf(wireTypes[int(selector)%len(wireTypes)]).Elem()
		decoded := reflect.New(typ)
		if err := coreprotocol.Decode(body, decoded.Interface().(coreprotocol.Target)); err != nil {
			return
		}
		canonical, err := coreprotocol.Marshal(decoded.Interface().(coreprotocol.Value))
		if err != nil {
			t.Fatalf("accepted %s cannot re-encode: %v", typ, err)
		}
		reused := reflect.New(typ)
		if err := coreprotocol.Decode(canonical, reused.Interface().(coreprotocol.Target)); err != nil {
			t.Fatalf("accepted %s does not round-trip: %v", typ, err)
		}
		if difference := exact(decoded.Elem(), reused.Elem(), typ.String(), false); difference != "" {
			t.Fatalf("accepted value changed: %s", difference)
		}
	})
}
