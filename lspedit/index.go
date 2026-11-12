package lspedit

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"crude/document"
)

// Source is an immutable UTF-8 snapshot with reusable line starts and sparse
// UTF-16 checkpoints. It owns no editor state; prepare it off Update.
type Source struct {
	text        string
	lines       []uint32
	checkpoints []columnCheckpoint
}

type columnCheckpoint struct{ line, column, offset uint32 }

// Long source lines need bounded repeated UTF-16 lookup without an entry per
// character. Each checkpoint is on an exact rune boundary.
const sourceCheckpointStride = 256

func NewSource(text string) (*Source, error) {
	if len(text) > document.MaxBytes || !utf8.ValidString(text) {
		return nil, fmt.Errorf("LSP edit: invalid source encoding or byte bound")
	}
	lines := make([]uint32, 1, strings.Count(text, "\n")+1)
	var checkpoints []columnCheckpoint
	line, column, next := uint32(0), uint32(0), uint32(sourceCheckpointStride)
	for offset, r := range text {
		if r == '\n' {
			lines = append(lines, uint32(offset+1))
			line, column, next = line+1, 0, sourceCheckpointStride
			continue
		}
		if column >= next {
			checkpoints = append(checkpoints, columnCheckpoint{line, column, uint32(offset)})
			next = column + sourceCheckpointStride
		}
		// CRLF is one line break and its CR does not consume a column.
		if r == '\r' && offset+1 < len(text) && text[offset+1] == '\n' {
			continue
		}
		column += uint32(utf16.RuneLen(r))
	}
	return &Source{text: text, lines: lines, checkpoints: checkpoints}, nil
}

// Text is the exact immutable source used to prepare this index.
func (s *Source) Text() string { return s.text }

// Offset resolves the same strict position as Offset without rescanning prior
// lines or revalidating the complete immutable source for each endpoint.
func (s *Source) Offset(line, col int) (int, error) {
	if s == nil || line < 0 || line >= len(s.lines) || col < 0 {
		return 0, fmt.Errorf("LSP edit: invalid position or document encoding")
	}
	start, end := int(s.lines[line]), len(s.text)
	if line+1 < len(s.lines) {
		end = int(s.lines[line+1]) - 1
		if end > start && s.text[end-1] == '\r' {
			end--
		}
	}
	index := sort.Search(len(s.checkpoints), func(i int) bool {
		point := s.checkpoints[i]
		return int(point.line) > line || int(point.line) == line && int(point.column) > col
	})
	if index > 0 {
		point := s.checkpoints[index-1]
		if int(point.line) == line {
			start, col = int(point.offset), col-int(point.column)
		}
	}
	return offsetInLine(s.text, start, end, col)
}
