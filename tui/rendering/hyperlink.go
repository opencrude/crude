package rendering

import (
	"crude/tui/cell"
	"strings"
)

// oscHyperlink consumes only a complete OSC 8 sequence from the shared ANSI
// decoder. All other OSC/DCS/APC controls remain discarded metadata.
func oscHyperlink(sequence string) (*cell.Link, bool) {
	body := sequence
	if strings.HasPrefix(body, "\x1b]8;") {
		body = body[4:]
	} else if strings.HasPrefix(body, "\x9d8;") {
		body = body[3:]
	} else {
		return nil, false
	}
	switch {
	case strings.HasSuffix(body, "\x1b\\"):
		body = strings.TrimSuffix(body, "\x1b\\")
	case strings.HasSuffix(body, "\a"), strings.HasSuffix(body, "\x9c"):
		body = body[:len(body)-1]
	default:
		return nil, false
	}
	params, target, ok := strings.Cut(body, ";")
	if !ok {
		return nil, true
	}
	return cell.NewLink(target, params), true
}
