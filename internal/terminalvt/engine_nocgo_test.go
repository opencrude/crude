//go:build !cgo

package terminalvt

import (
	"errors"
	"testing"
)

func TestNoCGOAPIUnavailable(t *testing.T) {
	e, err := New(Options{})
	if e != nil || !errors.Is(err, ErrNoCGO) {
		t.Fatalf("new=%v %v", e, err)
	}
	e.Close()
	if _, err = e.Rows(RowRequest{Start: 0, Count: 1}); !errors.Is(err, ErrNoCGO) {
		t.Fatal(err)
	}
	if err = e.Resize(80, 24); !errors.Is(err, ErrNoCGO) {
		t.Fatal(err)
	}
	if len(e.Feed([]byte("x")).Replies) > 0 || e.EncodeKey(Key{}) != nil || e.EncodeMouse(Mouse{}) != nil || e.EncodePaste(nil) != nil || e.EncodeFocus(true) != nil {
		t.Fatal("stub did work")
	}
}
