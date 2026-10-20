package fileio

import (
	"encoding/base64"
	"encoding/json"
	"unicode/utf8"
)

// commandOutputPayload preserves raw bytes without allowing control-heavy output
// to expand sixfold as JSON escapes and overflow the bounded outcome journal.
// The standard JSON encoder determines the size; this is not another ANSI parser.
func commandOutputPayload(raw string) (output, encoding string) {
	if utf8.ValidString(raw) {
		encoded, _ := json.Marshal(raw) // strings always marshal successfully
		if len(encoded) <= len(raw)*2+256 {
			return raw, ""
		}
	}
	return base64.StdEncoding.EncodeToString([]byte(raw)), "base64"
}
