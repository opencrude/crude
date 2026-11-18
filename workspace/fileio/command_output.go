package fileio

import (
	"encoding/base64"
	"unicode/utf8"
)

// JSON preserves UTF-8 text, including ANSI and control characters. Only byte
// ranges that are not valid UTF-8 need a lossless binary representation.
func commandOutputPayload(raw string) (output, encoding string) {
	if utf8.ValidString(raw) {
		return raw, ""
	}
	return base64.StdEncoding.EncodeToString([]byte(raw)), "base64"
}
