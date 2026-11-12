package lspedit

import (
	"strings"
	"testing"
)

// Source index queries use the same strict UTF-16 contract as ordinary edits,
// including CRLF, surrogate and bare-CR boundaries.
func TestIndexedSourceResolvesLiteralUnicodeCoordinates(t *testing.T) {
	source, err := NewSource("a😀b\r\n界\rx\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ line, col, want int }{{0, 0, 0}, {0, 1, 1}, {0, 3, 5}, {0, 4, 6}, {1, 0, 8}, {1, 1, 11}, {1, 2, 12}, {1, 3, 13}, {2, 0, 14}} {
		got, err := source.Offset(p.line, p.col)
		if err != nil || got != p.want {
			t.Fatalf("position %d:%d = %d, %v; want %d", p.line, p.col, got, err, p.want)
		}
	}
	for _, p := range [][2]int{{0, 2}, {0, 5}, {1, 4}, {2, 1}, {3, 0}, {-1, 0}, {0, -1}} {
		if got, err := source.Offset(p[0], p[1]); err == nil {
			t.Fatalf("accepted invalid UTF-16 location %v as %d", p, got)
		}
	}
	if _, err := NewSource("bad\xff"); err == nil {
		t.Fatal("accepted non-UTF-8 source")
	}
}

// Repeated lookups at long-line checkpoint boundaries must retain strict UTF-16
// behavior, including surrogate splits and a CRLF adjacent to the boundary.
func TestIndexedSourceLongLinesKeepExactBoundaries(t *testing.T) {
	text := strings.Repeat("a", 255) + "😀界\r\n" + strings.Repeat("b", 260) + "\rZ"
	source, err := NewSource(text)
	if err != nil {
		t.Fatal(err)
	}
	points := []struct {
		line, col, offset int
		valid             bool
	}{
		{0, 255, 255, true}, {0, 256, 0, false}, {0, 257, 259, true}, {0, 258, 262, true}, {0, 259, 0, false},
		{1, 0, 264, true}, {1, 255, 519, true}, {1, 260, 524, true}, {1, 261, 525, true}, {1, 262, 526, true}, {1, 263, 0, false},
	}
	for _, point := range points {
		got, err := source.Offset(point.line, point.col)
		if (err == nil) != point.valid || point.valid && got != point.offset {
			t.Fatalf("Offset(%d,%d)=%d, %v; want %d valid=%t", point.line, point.col, got, err, point.offset, point.valid)
		}
	}
}

// Compare every candidate UTF-16 column against the original strict resolver,
// including empty lines, CRLF and grapheme components around checkpoints.
func TestIndexedSourceMatchesStrictOffset(t *testing.T) {
	for _, text := range []string{"", "\r\n\n", "plain\rvalue\n", strings.Repeat("é😀x", 180) + "\r\nend", strings.Repeat("\n", 300) + "界"} {
		source, err := NewSource(text)
		if err != nil {
			t.Fatal(err)
		}
		for line := 0; line <= strings.Count(text, "\n")+1; line++ {
			for col := 0; col <= len(text)+1; col++ {
				got, ge := source.Offset(line, col)
				want, we := Offset(text, line, col)
				if (ge == nil) != (we == nil) || ge == nil && got != want {
					t.Fatalf("line=%d col=%d indexed=%d %v reference=%d %v", line, col, got, ge, want, we)
				}
			}
		}
	}
}
