package lspnav

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Standard DocumentHighlightKind values. An omitted kind means Text.
const (
	HighlightText  = 1
	HighlightRead  = 2
	HighlightWrite = 3
)

// MaxHighlights bounds one document-highlight answer.
const MaxHighlights = 4096

// Highlight is one occurrence in the invoking document.
type Highlight struct {
	Range Range
	Kind  int
}

// HighlightResult distinguishes explicit null, an observed empty list and
// occurrences, with the same closed-object rules as navigation.
type HighlightResult struct {
	Answer     Answer
	Highlights []Highlight
}

// ParseHighlights consumes an original textDocument/documentHighlight answer:
// null or DocumentHighlight[]. Unknown members, malformed coordinates, unknown
// kinds and over-bound answers fail atomically. Ranges are checked against the
// invoking source by the caller.
func ParseHighlights(raw []byte) (HighlightResult, error) {
	if len(raw) > MaxRawBytes || !utf8.Valid(raw) {
		return HighlightResult{}, fmt.Errorf("LSP highlights: invalid UTF-8 or raw byte bound exceeded")
	}
	if err := validateJSON(raw); err != nil {
		return HighlightResult{}, err
	}
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return HighlightResult{Answer: NullAnswer}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return HighlightResult{}, fmt.Errorf("LSP highlights: expected highlight array")
	}
	var out []Highlight
	for decoder.More() {
		if len(out) == MaxHighlights {
			return HighlightResult{}, fmt.Errorf("LSP highlights: highlight count bound exceeded")
		}
		var row json.RawMessage
		if err := decoder.Decode(&row); err != nil {
			return HighlightResult{}, err
		}
		fields, err := object(row, "range", "kind")
		if err != nil {
			return HighlightResult{}, err
		}
		span, err := parseRange(fields["range"])
		if err != nil {
			return HighlightResult{}, err
		}
		kind := HighlightText
		if raw, supplied := fields["kind"]; supplied {
			if kind, err = coordinate(raw); err != nil || kind < HighlightText || kind > HighlightWrite {
				return HighlightResult{}, fmt.Errorf("LSP highlights: unknown highlight kind")
			}
		}
		out = append(out, Highlight{Range: span, Kind: kind})
	}
	if len(out) == 0 {
		return HighlightResult{Answer: EmptyAnswer}, nil
	}
	return HighlightResult{Answer: TargetsAnswer, Highlights: out}, nil
}
