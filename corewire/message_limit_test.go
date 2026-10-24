package corewire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// Open and the first bulk chunk commonly arrive in the same transport read.
// Tightening the role-specific limit must not replace the buffered reader.
func TestReaderSetLimitPreservesCoalescedRemainder(t *testing.T) {
	var wire bytes.Buffer
	for _, body := range [][]byte{[]byte("open"), bytes.Repeat([]byte("c"), 1024), []byte("tail")} {
		if err := WriteMessage(&wire, body); err != nil {
			t.Fatal(err)
		}
	}
	r := NewReader(&wire, MaxMessage)
	if got, err := r.Next(); err != nil || string(got) != "open" {
		t.Fatalf("open: %q %v", got, err)
	}
	if wire.Len() != 0 {
		t.Fatal("fixture did not coalesce the messages")
	}
	r.SetLimit(1024)
	if got, err := r.Next(); err != nil || !bytes.Equal(got, bytes.Repeat([]byte("c"), 1024)) {
		t.Fatalf("chunk: %d %v", len(got), err)
	}
	r.SetLimit(4)
	if got, err := r.Next(); err != nil || string(got) != "tail" {
		t.Fatalf("tail: %q %v", got, err)
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestReaderSetLimitRejectsDeclarationBeforeGrowth(t *testing.T) {
	var wire bytes.Buffer
	if err := WriteMessage(&wire, []byte("open")); err != nil {
		t.Fatal(err)
	}
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], 32<<20)
	wire.Write(prefix[:n])
	r := NewReader(&wire, MaxMessage)
	if _, err := r.Next(); err != nil {
		t.Fatal(err)
	}
	before := cap(r.buf)
	r.SetLimit(1024)
	if _, err := r.Next(); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("declared overlimit: %v", err)
	}
	if cap(r.buf) != before {
		t.Fatalf("declared length grew allocation: %d -> %d", before, cap(r.buf))
	}
}

func FuzzReaderLimitTransitions(f *testing.F) {
	f.Add([]byte("chunk"), uint16(5))
	f.Add(bytes.Repeat([]byte("x"), 8192), uint16(1024))
	f.Fuzz(func(t *testing.T, body []byte, bound uint16) {
		if len(body) > 128<<10 {
			return
		}
		limit := int(bound) + 1
		var wire bytes.Buffer
		if err := WriteMessage(&wire, []byte("open")); err != nil {
			t.Fatal(err)
		}
		if err := WriteMessage(&wire, body); err != nil {
			t.Fatal(err)
		}
		r := NewReader(&wire, MaxMessage)
		if _, err := r.Next(); err != nil {
			t.Fatal(err)
		}
		r.SetLimit(limit)
		got, err := r.Next()
		if len(body) > limit {
			if !errors.Is(err, ErrMessageTooLarge) {
				t.Fatalf("overlimit accepted: %v", err)
			}
		} else if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("buffered body changed: %v", err)
		}
	})
}
