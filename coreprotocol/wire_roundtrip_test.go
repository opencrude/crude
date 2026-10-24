package coreprotocol_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tinylib/msgp/msgp"

	"crude/coreprotocol"
	"crude/types/jsondoc"
)

// JSON independently checks the codecs' semantic content. Optional publication
// lists are the deliberate exception: JSON erases a present pointer to a nil
// slice, whereas v2 must preserve absent versus delivered-empty publications.
// Normalize only those JSON inputs; pointer presence is still compared strictly.
// Nil and empty collection contents are equivalent in this JSON cross-check.
func TestEveryWireTypeRoundTripsLikeJSON(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for _, proto := range wireTypes {
		typ := reflect.TypeOf(proto).Elem()
		t.Run(typ.String(), func(t *testing.T) {
			for range 25 {
				value := reflect.New(typ)
				fill(value.Elem(), rng, 0)
				encoded, err := value.Interface().(msgp.Marshaler).MarshalMsg(nil)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				decoded := reflect.New(typ)
				rest, err := decoded.Interface().(msgp.Unmarshaler).UnmarshalMsg(encoded)
				if err != nil || len(rest) != 0 {
					t.Fatalf("unmarshal: %v (%d bytes left)", err, len(rest))
				}
				text, err := json.Marshal(publicationJSONInput(value.Interface()))
				if err != nil {
					t.Fatalf("json: %v", err)
				}
				reference := reflect.New(typ)
				if err := json.Unmarshal(text, reference.Interface()); err != nil {
					t.Fatalf("json decode: %v", err)
				}
				retainOpaqueNullPresence(value.Elem(), reference.Elem())
				if diff := compare(decoded.Elem(), reference.Elem(), typ.Name()); diff != "" {
					t.Fatalf("msgp and JSON round trips differ at %s", diff)
				}
			}
		})
	}
}

// JSON null cannot express whether a *jsondoc.Object was absent or pointed to a
// null document. Recover only that lost pointer bit from the input; the exact
// property independently requires the v2 codec to preserve it without help.
func retainOpaqueNullPresence(input, reference reflect.Value) {
	if input.Type() == reflect.TypeFor[*jsondoc.Object]() {
		if !input.IsNil() && input.Elem().IsNil() && reference.IsNil() {
			reference.Set(reflect.New(input.Type().Elem()))
		}
		return
	}
	switch input.Kind() {
	case reflect.Pointer:
		if !input.IsNil() && !reference.IsNil() {
			retainOpaqueNullPresence(input.Elem(), reference.Elem())
		}
	case reflect.Struct:
		for i := range input.NumField() {
			if input.Type().Field(i).IsExported() {
				retainOpaqueNullPresence(input.Field(i), reference.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < min(input.Len(), reference.Len()); i++ {
			retainOpaqueNullPresence(input.Index(i), reference.Index(i))
		}
	}
}

// publicationJSONInput retains the original pointer presence while making the
// three publication lists unambiguous to JSON. Work on copies so the exact
// property and presence regressions always compare against untouched inputs.
func publicationJSONInput(value any) any {
	switch v := value.(type) {
	case *coreprotocol.WorkspaceState:
		copy := *v
		if v.Registry != nil && *v.Registry == nil {
			copy.Registry = coreprotocol.Ptr(coreprotocol.SessionRegistry{})
		}
		if v.Terminals != nil && *v.Terminals == nil {
			copy.Terminals = coreprotocol.Ptr(coreprotocol.TerminalInfos{})
		}
		return &copy
	case *coreprotocol.WorkspaceUpdate:
		copy := *v
		if v.Sessions != nil && *v.Sessions == nil {
			copy.Sessions = coreprotocol.Ptr(coreprotocol.SessionSnapshotList{})
		}
		return &copy
	default:
		return value
	}
}

// The codecs preserve every pointer's presence and all content, including nil
// versus empty collections held directly in struct fields.
func TestEveryWireTypeRoundTripsExactly(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 17))
	for _, proto := range wireTypes {
		typ := reflect.TypeOf(proto).Elem()
		t.Run(typ.String(), func(t *testing.T) {
			for range 25 {
				value := reflect.New(typ)
				fill(value.Elem(), rng, 0)
				encoded, err := value.Interface().(msgp.Marshaler).MarshalMsg(nil)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				decoded := reflect.New(typ)
				if _, err := decoded.Interface().(msgp.Unmarshaler).UnmarshalMsg(encoded); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if diff := exact(decoded.Elem(), value.Elem(), typ.Name(), false); diff != "" {
					t.Fatalf("round trip changed %s", diff)
				}
			}
		})
	}
}

// exact compares like reflect.DeepEqual, reporting the first difference;
// times compare as instants and opaque documents by content. Nil and empty
// must match for collections held directly in struct fields (field). Three
// shapes have one encoding for both and are compared by content: top-level
// collections, collections inside collections, and collections inside present
// pointers. Pointer absence itself is never equivalent to a present empty list.
func exact(a, b reflect.Value, path string, field bool) string {
	switch a.Type() {
	case timeType:
		if !a.Interface().(time.Time).Equal(b.Interface().(time.Time)) {
			return path
		}
		return ""
	case rawJSONType, objectType:
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			return fmt.Sprintf("%s (%v vs %v)", path, a.Interface(), b.Interface())
		}
		return ""
	}
	if k := a.Kind(); (k == reflect.Slice || k == reflect.Map) && field && a.Type().Name() == "" && a.IsNil() != b.IsNil() {
		return fmt.Sprintf("%s (nil %v vs %v)", path, a.IsNil(), b.IsNil())
	}
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				return fmt.Sprintf("%s (nil %v vs %v)", path, a.IsNil(), b.IsNil())
			}
			return ""
		}
		return exact(a.Elem(), b.Elem(), path, false)
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s (len %d vs %d)", path, a.Len(), b.Len())
		}
		for i := range a.Len() {
			if d := exact(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i), false); d != "" {
				return d
			}
		}
	case reflect.Map:
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s (len %d vs %d)", path, a.Len(), b.Len())
		}
		for _, key := range a.MapKeys() {
			bv := b.MapIndex(key)
			if !bv.IsValid() {
				return path + "[" + key.String() + "] missing"
			}
			if d := exact(a.MapIndex(key), bv, path+"["+key.String()+"]", false); d != "" {
				return d
			}
		}
	case reflect.Struct:
		for i := range a.NumField() {
			f := a.Type().Field(i)
			if !f.IsExported() || f.Tag.Get("json") == "-" {
				continue
			}
			if d := exact(a.Field(i), b.Field(i), path+"."+f.Name, true); d != "" {
				return d
			}
		}
	default:
		if !a.Equal(b) {
			return fmt.Sprintf("%s (%v vs %v)", path, a.Interface(), b.Interface())
		}
	}
	return ""
}

// Truncated input of every type fails cleanly, never panics or over-allocates.
func TestEveryWireTypeRejectsTruncation(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 5))
	for _, proto := range wireTypes {
		typ := reflect.TypeOf(proto).Elem()
		value := reflect.New(typ)
		fill(value.Elem(), rng, 0)
		encoded, _ := value.Interface().(msgp.Marshaler).MarshalMsg(nil)
		for cut := range len(encoded) {
			target := reflect.New(typ)
			if _, err := target.Interface().(msgp.Unmarshaler).UnmarshalMsg(encoded[:cut]); err == nil {
				t.Fatalf("%s: truncated at %d of %d bytes decoded without error", typ, cut, len(encoded))
			}
		}
	}
}

var (
	rawJSONType = reflect.TypeFor[json.RawMessage]()
	objectType  = reflect.TypeFor[jsondoc.Object]()
	timeType    = reflect.TypeFor[time.Time]()
	lspEditType = reflect.TypeFor[coreprotocol.LSPEdit]()
)

func fill(v reflect.Value, rng *rand.Rand, depth int) {
	if depth > 5 {
		return
	}
	switch v.Type() {
	case rawJSONType:
		if rng.IntN(2) == 0 {
			v.SetBytes([]byte(fmt.Sprintf(`{"n":%d,"s":"x"}`, rng.IntN(100))))
		}
		return
	case objectType:
		if rng.IntN(2) == 0 {
			v.Set(reflect.ValueOf(jsondoc.Object{"n": float64(rng.IntN(100)), "nested": map[string]any{"ok": true}}))
		}
		return
	case timeType:
		v.Set(reflect.ValueOf(time.Unix(rng.Int64N(4e9), rng.Int64N(1e9)).UTC()))
		return
	case lspEditType:
		return // the LSP library validates its own documents; covered by core tests
	}
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(rng.IntN(2) == 0)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(rng.Int64N(1<<(min(v.Type().Bits(), 40)-1)) - rng.Int64N(1<<(min(v.Type().Bits(), 40)-2)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		v.SetUint(rng.Uint64N(1 << min(v.Type().Bits(), 52)))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(rng.IntN(1000)) / 8)
	case reflect.String:
		words := []string{"", "a", "héllo", "line\nbreak", "日本", "x y z", `quote"d`}
		v.SetString(words[rng.IntN(len(words))])
	case reflect.Pointer:
		if rng.IntN(3) != 0 {
			p := reflect.New(v.Type().Elem())
			fill(p.Elem(), rng, depth+1)
			v.Set(p)
		}
	case reflect.Slice:
		if rng.IntN(4) == 0 {
			return // nil
		}
		n := rng.IntN(3)
		s := reflect.MakeSlice(v.Type(), n, n)
		for i := range n {
			fill(s.Index(i), rng, depth+1)
		}
		v.Set(s)
	case reflect.Array:
		for i := range v.Len() {
			fill(v.Index(i), rng, depth+1)
		}
	case reflect.Map:
		if rng.IntN(4) == 0 {
			return // nil
		}
		n := rng.IntN(3)
		m := reflect.MakeMapWithSize(v.Type(), n)
		for i := range n {
			key := reflect.New(v.Type().Key()).Elem()
			key.SetString(fmt.Sprintf("k%d", i))
			value := reflect.New(v.Type().Elem()).Elem()
			fill(value, rng, depth+1)
			m.SetMapIndex(key, value)
		}
		v.Set(m)
	case reflect.Struct:
		for i := range v.NumField() {
			field := v.Type().Field(i)
			if !field.IsExported() || field.Tag.Get("json") == "-" {
				continue
			}
			fill(v.Field(i), rng, depth+1)
		}
	}
}

func compare(a, b reflect.Value, path string) string {
	if a.Type() == rawJSONType {
		// JSON re-encodes an absent document as the literal null; downstream
		// JSON encodes the nil the codec keeps as that same null.
		x, y := strings.TrimSpace(string(a.Bytes())), strings.TrimSpace(string(b.Bytes()))
		if x == "null" {
			x = ""
		}
		if y == "null" {
			y = ""
		}
		if x != y {
			return path
		}
		return ""
	}
	if a.Type() == timeType {
		if !a.Interface().(time.Time).Equal(b.Interface().(time.Time)) {
			return path
		}
		return ""
	}
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				return path + " (nil pointer)"
			}
			return ""
		}
		return compare(a.Elem(), b.Elem(), path)
	case reflect.Slice:
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s (len %d vs %d)", path, a.Len(), b.Len())
		}
		for i := range a.Len() {
			if d := compare(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
	case reflect.Array:
		for i := range a.Len() {
			if d := compare(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
	case reflect.Map:
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s (len %d vs %d)", path, a.Len(), b.Len())
		}
		for _, key := range a.MapKeys() {
			bv := b.MapIndex(key)
			if !bv.IsValid() {
				return path + "[" + key.String() + "] missing"
			}
			if d := compare(a.MapIndex(key), bv, path+"["+key.String()+"]"); d != "" {
				return d
			}
		}
	case reflect.Struct:
		for i := range a.NumField() {
			if !a.Type().Field(i).IsExported() {
				continue
			}
			if d := compare(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name); d != "" {
				return d
			}
		}
	case reflect.Interface:
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			return path + fmt.Sprintf(" (%#v vs %#v)", a.Interface(), b.Interface())
		}
	default:
		if !a.Equal(b) {
			return fmt.Sprintf("%s (%v vs %v)", path, a.Interface(), b.Interface())
		}
	}
	return ""
}
