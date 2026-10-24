package jsondoc

import (
	"bytes"
	"testing"

	"github.com/tinylib/msgp/msgp"
)

func TestObjectNullIsOpaqueJSONNotPointerAbsence(t *testing.T) {
	var null Object
	raw, err := null.MarshalMsg(nil)
	if err != nil {
		t.Fatal(err)
	}
	if msgp.IsNil(raw) {
		t.Fatal("JSON null collided with optional pointer absence")
	}
	text, rest, err := msgp.ReadBytesZC(raw)
	if err != nil || len(rest) != 0 || !bytes.Equal(text, []byte("null")) {
		t.Fatalf("opaque JSON=%q rest=%x err=%v", text, rest, err)
	}
	dst := Object{"later": true}
	if rest, err = dst.UnmarshalMsg(raw); err != nil || len(rest) != 0 || dst != nil {
		t.Fatalf("null failed to clear reused object: %v %v", dst, err)
	}
	if _, err = dst.UnmarshalMsg(msgp.AppendNil(nil)); err == nil {
		t.Fatal("pointer sentinel accepted as an object value")
	}
}
