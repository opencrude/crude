package lspedit

import (
	"strings"
	"testing"
	"unicode/utf8"

	"crude/document"
)

func TestPositionLiteralSourceCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		// Literal byte offset, line, UTF-16 column; not computed via Offset.
		points [][3]int
	}{
		{"empty", "", [][3]int{{0, 0, 0}}},
		{"ascii", "abc\nxy", [][3]int{
			{0, 0, 0}, {1, 0, 1}, {3, 0, 3}, {4, 1, 0}, {6, 1, 2},
		}},
		{"unicode tabs and combining", "\t界e\u0301😀Z", [][3]int{
			{0, 0, 0}, {1, 0, 1}, {4, 0, 2}, {5, 0, 3},
			{7, 0, 4}, {11, 0, 6}, {12, 0, 7},
		}},
		{"joined emoji rune boundaries", "👩‍💻!", [][3]int{
			{0, 0, 0}, {4, 0, 2}, {7, 0, 3}, {11, 0, 5}, {12, 0, 6},
		}},
		{"CRLF and trailing LF", "a\r\n😀\n", [][3]int{
			{0, 0, 0}, {1, 0, 1}, {3, 1, 0}, {7, 1, 2}, {8, 2, 0},
		}},
		{"empty CRLF lines", "\r\n\r\n", [][3]int{
			{0, 0, 0}, {2, 1, 0}, {4, 2, 0},
		}},
		{"bare CR", "a\rb\r", [][3]int{
			{1, 0, 1}, {2, 0, 2}, {3, 0, 3}, {4, 0, 4},
		}},
		{"CR before CRLF", "a\r\r\n", [][3]int{
			{2, 0, 2}, {4, 1, 0},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range tc.points {
				line, col, err := Position(tc.text, p[0])
				if err != nil || line != p[1] || col != p[2] {
					t.Errorf("Position(%q, %d) = (%d,%d,%v), want (%d,%d,nil)",
						tc.text, p[0], line, col, err, p[1], p[2])
				}
			}
		})
	}
}

func TestPositionRejectsInvalidSourceOffsets(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		offset     int
	}{
		{"negative", "abc", -1},
		{"past EOF", "abc", 4},
		{"large integer", "abc", int(^uint(0) >> 1)},
		{"empty past EOF", "", 1},
		{"split CJK first", "界", 1},
		{"split CJK second", "界", 2},
		{"split emoji first", "😀", 1},
		{"split emoji second", "😀", 2},
		{"split emoji third", "😀", 3},
		{"split combining rune", "e\u0301", 2},
		{"CRLF LF", "ab\r\nx", 3},
		{"empty CRLF LF", "\r\n", 1},
		{"invalid at caret", "\xff", 0},
		{"invalid before caret", "\xffa", 2},
		{"invalid after caret", "a\xff", 1},
		{"truncated UTF-8", "a\xf0\x9f", 1},
		{"surrogate encoding", "\xed\xa0\x80", 0},
		{"oversized", strings.Repeat("x", document.MaxBytes+1), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line, col, err := Position(tc.text, tc.offset)
			if err == nil || line != 0 || col != 0 {
				t.Fatalf("Position returned (%d,%d,%v); want zero coordinates and error", line, col, err)
			}
		})
	}
	text := strings.Repeat("x", document.MaxBytes)
	line, col, err := Position(text, document.MaxBytes)
	if err != nil || line != 0 || col != document.MaxBytes {
		t.Fatalf("exact byte bound: (%d,%d,%v)", line, col, err)
	}
}

func TestPositionOffsetRoundtrip(t *testing.T) {
	for _, text := range []string{"", "\n", "\r\n", "a\rb\r", "\t界e\u0301😀Z\r\n👩‍💻\n", "\r\r\n\n"} {
		for offset := 0; offset <= len(text); offset++ {
			// Admissibility is independent of either conversion implementation.
			valid := (offset == len(text) || utf8.RuneStart(text[offset])) &&
				(offset == 0 || offset == len(text) || text[offset] != '\n' || text[offset-1] != '\r')
			line, col, err := Position(text, offset)
			if !valid {
				if err == nil {
					t.Errorf("%q at %d: accepted invalid offset", text, offset)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%q at %d: %v", text, offset, err)
			}
			back, err := Offset(text, line, col)
			if err != nil || back != offset {
				t.Errorf("%q at %d: (%d,%d) returned (%d,%v)", text, offset, line, col, back, err)
			}
		}
	}
}
