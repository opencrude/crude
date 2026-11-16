package msgp

import (
	"errors"
	"testing"
)

// A truncated fixed-size array must fail, never decode zero-filled.
func TestReadExactBytesRejectsTruncatedPayload(t *testing.T) {
	encoded := AppendBytes(nil, []byte{1, 2, 3, 4})
	into := make([]byte, 4)
	for cut := range len(encoded) {
		if _, err := ReadExactBytes(encoded[:cut], into); err == nil {
			t.Fatalf("truncated at %d of %d bytes decoded without error", cut, len(encoded))
		}
	}
	if _, err := ReadExactBytes(encoded[:len(encoded)-1], into); !errors.Is(err, ErrShortBytes) {
		t.Fatalf("short payload error = %v, want ErrShortBytes", err)
	}
	if rest, err := ReadExactBytes(encoded, into); err != nil || len(rest) != 0 || into[3] != 4 {
		t.Fatalf("full payload: rest=%d err=%v into=%v", len(rest), err, into)
	}
}
