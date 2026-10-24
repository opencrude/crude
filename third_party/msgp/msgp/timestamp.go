package msgp

import (
	"encoding/binary"
	"time"
)

// readTimestamp is shared by the byte-slice and streaming decoders. Decode the
// signed seconds and unsigned nanoseconds separately: UnixNano and duration
// arithmetic cannot represent the full timestamp-96 range.
func readTimestamp(b []byte) (time.Time, error) {
	var sec, nanos int64
	switch len(b) {
	case 4:
		sec = int64(binary.BigEndian.Uint32(b))
	case 8:
		v := binary.BigEndian.Uint64(b)
		sec, nanos = int64(v&((1<<34)-1)), int64(v>>34)
	case 12:
		nanos = int64(binary.BigEndian.Uint32(b))
		sec = int64(binary.BigEndian.Uint64(b[4:]))
	default:
		return time.Time{}, InvalidTimestamp{FieldLength: len(b)}
	}
	if nanos > 999999999 {
		return time.Time{}, InvalidTimestamp{Nanos: nanos}
	}
	return time.Unix(sec, nanos).UTC(), nil
}
