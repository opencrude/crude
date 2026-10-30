package types

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// LSPSourcePosition is user source intent, not a wire position. Resolve it only
// against the immutable source leased for the actual request.
type LSPSourcePosition struct {
	Line, Column int
	Symbol       string
}

func (p LSPSourcePosition) Resolve(text string) (line, column int, err error) {
	if p.Line < 1 || p.Column < 0 || !utf8.ValidString(p.Symbol) {
		return 0, 0, errors.New("a positive 1-based source line and valid symbol/column are required")
	}
	for i := 1; i < p.Line; i++ {
		_, rest, ok := strings.Cut(text, "\n")
		if !ok {
			return 0, 0, fmt.Errorf("source has fewer than %d lines", p.Line)
		}
		text = rest
	}
	text, _, _ = strings.Cut(text, "\n")
	text = strings.TrimSuffix(text, "\r")
	runes := []rune(text)
	if p.Column > len(runes)+1 {
		return 0, 0, fmt.Errorf("column %d is past line %d (%d characters)", p.Column, p.Line, len(runes))
	}
	start := max(0, p.Column-1)
	if p.Symbol != "" {
		suffix := string(runes[start:])
		offset := strings.Index(suffix, p.Symbol)
		if offset < 0 {
			return 0, 0, fmt.Errorf("symbol %q is not on line %d: %q", p.Symbol, p.Line, strings.TrimSpace(text))
		}
		start += utf8.RuneCountInString(suffix[:offset])
	} else if p.Column == 0 {
		return 0, 0, errors.New("symbol (or 1-based column) is required")
	}
	return p.Line - 1, len(utf16.Encode(runes[:start])), nil
}
