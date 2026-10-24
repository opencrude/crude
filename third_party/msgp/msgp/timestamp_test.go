package msgp_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	"github.com/tinylib/msgp/msgp"
)

func TestTimestampStandardAPIs(t *testing.T) {
	for _, instant := range []time.Time{{}, time.Unix(0, 0), time.Unix(-1, 999999999),
		time.Unix(math.MinInt64, 1), time.Unix(math.MaxInt64, 999999999),
		time.Unix(1<<34, 12), time.Unix(1<<32, 0), time.Now()} {
		encoded := msgp.AppendTime(nil, instant)
		if explicit := msgp.AppendTimeExt(nil, instant); !bytes.Equal(encoded, explicit) {
			t.Fatalf("default and explicit time codecs differ: %x vs %x", encoded, explicit)
		}
		for _, write := range []func(*msgp.Writer, time.Time) error{(*msgp.Writer).WriteTime, (*msgp.Writer).WriteTimeExt} {
			var buffer bytes.Buffer
			writer := msgp.NewWriter(&buffer)
			if err := write(writer, instant); err != nil {
				t.Fatal(err)
			}
			if err := writer.Flush(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buffer.Bytes(), encoded) {
				t.Fatalf("stream and byte timestamps differ: %x vs %x", buffer.Bytes(), encoded)
			}
		}
		withTail := append(append([]byte{}, encoded...), 0xc3)
		got, rest, err := msgp.ReadTimeBytes(withTail)
		if err != nil || len(rest) != 1 || rest[0] != 0xc3 {
			t.Fatalf("byte decode lost boundary: %v %x", err, rest)
		}
		assertTimestamp(t, got, instant)
		// One-byte reads exercise fragmented network input, not only bytes.Buffer.
		reader := msgp.NewReader(&byteAtATime{bytes.NewReader(withTail)})
		stream, err := reader.ReadTime()
		if err != nil {
			t.Fatal(err)
		}
		assertTimestamp(t, stream, instant)
		if tail, err := reader.ReadBool(); err != nil || !tail {
			t.Fatalf("stream decode lost boundary: %v %v", tail, err)
		}
		if msgp.NextType(encoded) != msgp.TimeType {
			t.Fatalf("timestamp not recognized: %x", encoded)
		}
		for cut := range len(encoded) {
			if _, _, err := msgp.ReadTimeBytes(encoded[:cut]); err == nil {
				t.Fatalf("truncated timestamp accepted at %d: %x", cut, encoded)
			}
			if _, err := msgp.NewReader(bytes.NewReader(encoded[:cut])).ReadTime(); err == nil {
				t.Fatalf("truncated timestamp stream accepted at %d: %x", cut, encoded)
			}
		}
	}
}

type byteAtATime struct{ io.Reader }

func (r *byteAtATime) Read(b []byte) (int, error) {
	if len(b) > 1 {
		b = b[:1]
	}
	return r.Reader.Read(b)
}

func assertTimestamp(t *testing.T, got, want time.Time) {
	t.Helper()
	if got.Unix() != want.Unix() || got.Nanosecond() != want.Nanosecond() || got.IsZero() != want.IsZero() || got.Location() != time.UTC {
		t.Fatalf("instant/zero/UTC changed: %v -> %v", want, got)
	}
}

func TestTimestampRejectsMalformed(t *testing.T) {
	for _, text := range []string{
		"d7ffee6b280000000000",                         // timestamp-64: 1e9 nanoseconds
		"c70cff3b9aca000000000000000000",               // timestamp-96: 1e9 nanoseconds
		"c70cffffffffff0000000000000000",               // timestamp-96: uint32 max nanoseconds
		"c70c05000000000000000000000000",               // private extension 5
		"d6fe00000000", "d4ff00", "d5ff0000", "c700ff", // wrong type/size
	} {
		encoded, err := hex.DecodeString(text)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := msgp.ReadTimeBytes(encoded); err == nil {
			t.Errorf("malformed bytes accepted: %s", text)
		}
		if _, err := msgp.NewReader(bytes.NewReader(encoded)).ReadTime(); err == nil {
			t.Errorf("malformed stream accepted: %s", text)
		}
	}
	// An extension header advertising 4GiB is invalid before requesting its
	// body. MaxElements is not a substitute for the timestamp's 12-byte bound.
	reader := msgp.NewReader(bytes.NewReader([]byte{0xc9, 0xff, 0xff, 0xff, 0xff, 0xff}))
	_, err := reader.ReadTime()
	var invalid msgp.InvalidTimestamp
	if !errors.As(err, &invalid) {
		t.Fatalf("oversized header must fail as a timestamp length before body read: %v", err)
	}
}

func FuzzTimestampSecondsNanos(f *testing.F) {
	for _, sec := range []int64{math.MinInt64, -62135596800, -1, 0, 1, math.MaxUint32, 1 << 34, math.MaxInt64} {
		f.Add(sec, uint32(0))
		f.Add(sec, uint32(999999999))
	}
	f.Fuzz(func(t *testing.T, sec int64, nanos uint32) {
		nanos %= 1000000000
		instant := time.Unix(sec, int64(nanos))
		encoded := msgp.AppendTime(nil, instant)
		// Independently inspect the spec layout and signed seconds on the wire.
		var wireSec int64
		var wireNanos uint32
		switch len(encoded) {
		case 6:
			if encoded[0] != 0xd6 || encoded[1] != 0xff {
				t.Fatalf("not timestamp-32: %x", encoded)
			}
			wireSec = int64(binary.BigEndian.Uint32(encoded[2:]))
		case 10:
			if encoded[0] != 0xd7 || encoded[1] != 0xff {
				t.Fatalf("not timestamp-64: %x", encoded)
			}
			value := binary.BigEndian.Uint64(encoded[2:])
			wireSec, wireNanos = int64(value&0x3ffffffff), uint32(value>>34)
		case 15:
			if encoded[0] != 0xc7 || encoded[1] != 12 || encoded[2] != 0xff {
				t.Fatalf("not timestamp-96: %x", encoded)
			}
			wireSec, wireNanos = int64(binary.BigEndian.Uint64(encoded[7:])), binary.BigEndian.Uint32(encoded[3:])
		default:
			t.Fatalf("invalid timestamp length: %x", encoded)
		}
		if wireSec != sec || wireNanos != nanos {
			t.Fatalf("wire fields (%d,%d), want (%d,%d)", wireSec, wireNanos, sec, nanos)
		}
		got, rest, err := msgp.ReadTimeBytes(encoded)
		if err != nil || len(rest) != 0 {
			t.Fatalf("timestamp decode: %v", err)
		}
		assertTimestamp(t, got, instant)
	})
}

func FuzzTimestampMalformed(f *testing.F) {
	for _, seed := range [][]byte{{}, {0xc9, 255, 255, 255, 255, 255}, {0xd6, 255, 0, 0, 0, 0}, {0xc7, 12, 5}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		got, rest, err := msgp.ReadTimeBytes(input)
		if err != nil {
			return
		}
		if got.Location() != time.UTC || got.Nanosecond() < 0 || got.Nanosecond() >= 1000000000 {
			t.Fatalf("invalid timestamp result: %v", got)
		}
		stream := msgp.NewReader(bytes.NewReader(input))
		other, err := stream.ReadTime()
		if err != nil || got != other {
			t.Fatalf("stream/byte mismatch: %v %v %v", got, other, err)
		}
		if len(input)-len(rest) < 6 || len(input)-len(rest) > 18 {
			t.Fatal("unbounded timestamp payload")
		}
	})
}
