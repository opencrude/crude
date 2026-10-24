package corewire

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

func TestMessagesRoundTripAcrossReadBoundaries(t *testing.T) {
	var wire bytes.Buffer
	bodies := [][]byte{[]byte("a"), {}, bytes.Repeat([]byte("x"), 70000), []byte("tail")}
	for _, body := range bodies {
		if err := WriteMessage(&wire, body); err != nil {
			t.Fatal(err)
		}
	}
	r := NewReader(iotest.OneByteReader(&wire), 0)
	for i, want := range bodies {
		got, err := r.Next()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("message %d: %d bytes, %v", i, len(got), err)
		}
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end: %v", err)
	}
}

func TestMessageReaderRejectsOversizeBeforeAllocating(t *testing.T) {
	m := NewMessage()
	m.B = append(m.B, make([]byte, 100)...)
	framed, _ := m.Framed()
	r := NewReader(bytes.NewReader(framed), 10)
	if _, err := r.Next(); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("err %v", err)
	}
	// A declared 60 MiB message that never arrives allocates by what arrived.
	huge := []byte{0x80, 0x80, 0x80, 0x1e, 'x'}
	r = NewReader(bytes.NewReader(huge), 0)
	if _, err := r.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated: %v", err)
	}
	if len(r.buf) > 8192 {
		t.Fatalf("allocated %d bytes for a declared length", len(r.buf))
	}
}

func FuzzMessageReader(f *testing.F) {
	f.Add([]byte{3, 'a', 'b', 'c', 0})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(bytes.NewReader(data), 1<<16)
		for range 64 {
			body, err := r.Next()
			if err != nil {
				return
			}
			if len(body) > 1<<16 {
				t.Fatalf("message of %d bytes passed the limit", len(body))
			}
		}
	})
}

func BenchmarkMessageRoundTrip(b *testing.B) {
	var wire bytes.Buffer
	body := make([]byte, 256)
	r := NewReader(&wire, 0)
	b.ReportAllocs()
	for b.Loop() {
		m := NewMessage()
		m.B = append(m.B, body...)
		_ = m.Send(&wire)
		m.Release()
		if _, err := r.Next(); err != nil {
			b.Fatal(err)
		}
	}
}
