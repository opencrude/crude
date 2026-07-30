package editor

import (
	"testing"

	"crude/tui/rendering"
)

func TestTabStopAdvance(t *testing.T) {
	tests := []struct {
		col, tabW, want int
	}{
		{0, 4, 4},
		{1, 4, 3},
		{3, 4, 1},
		{4, 4, 4},
		{5, 4, 3},
		{0, 8, 8},
		{3, 8, 5},
		{8, 8, 8},
		{2, 2, 2},
		{1, 2, 1},
	}
	for _, tt := range tests {
		if got := TabStopAdvance(tt.col, tt.tabW); got != tt.want {
			t.Fatalf("TabStopAdvance(%d,%d)=%d want %d", tt.col, tt.tabW, got, tt.want)
		}
	}
}

func TestByteOffsetToVisualCol_LeadingTab(t *testing.T) {
	line := "\tfoo"
	// "foo" starts at byte 1; visual col after tab stop 4 is 4.
	if got := ByteOffsetToVisualCol(line, 1, 4); got != 4 {
		t.Fatalf("leading tab: col=%d want 4", got)
	}
	if got := ByteOffsetToVisualCol(line, 4, 4); got != 7 {
		t.Fatalf("end of foo: col=%d want 7", got)
	}
}

func TestByteOffsetToVisualCol_MidLineTab(t *testing.T) {
	line := "ab\tcd" // col after ab=2, tab advances 2 → col 4, then cd
	if got := ByteOffsetToVisualCol(line, 3, 4); got != 4 {
		t.Fatalf("after mid tab: col=%d want 4", got)
	}
	if got := ByteOffsetToVisualCol(line, 5, 4); got != 6 {
		t.Fatalf("end: col=%d want 6", got)
	}
}

func TestByteOffsetToVisualCol_TabWidth8(t *testing.T) {
	line := "\tfoo"
	if got := ByteOffsetToVisualCol(line, 1, 8); got != 8 {
		t.Fatalf("tabW=8: col=%d want 8", got)
	}
}

func TestByteOffsetToVisualCol_Multibyte(t *testing.T) {
	line := "éfoo" // é is 2 UTF-8 bytes, width 1
	if got := ByteOffsetToVisualCol(line, len("é"), 4); got != 1 {
		t.Fatalf("é: col=%d want 1", got)
	}
	line = "日本語foo"
	off := len("日本語")
	want := rendering.VisibleWidth("日本語")
	if got := ByteOffsetToVisualCol(line, off, 4); got != want {
		t.Fatalf("日本語: col=%d want %d", got, want)
	}
}

func TestByteOffsetToVisualCol_EmojiZWJ(t *testing.T) {
	emoji := "👨‍👩‍👧"
	line := emoji + "foo"
	off := len(emoji)
	want := rendering.VisibleWidth(emoji)
	if want != 2 {
		t.Fatalf("precondition: ZWJ emoji VisibleWidth=%d want 2", want)
	}
	if got := ByteOffsetToVisualCol(line, off, 4); got != want {
		t.Fatalf("ZWJ: col=%d want %d", got, want)
	}
}

func TestVisualColToByteOffset_RoundTrip(t *testing.T) {
	lines := []string{
		"\tfoo",
		"ab\tcd",
		"éfoo",
		"日本語x",
		"👨‍👩‍👧bar",
	}
	for _, line := range lines {
		w := LineVisualWidth(line, 4)
		for col := 0; col <= w; col++ {
			off := VisualColToByteOffset(line, col, 4)
			back := ByteOffsetToVisualCol(line, off, 4)
			if back > col {
				t.Fatalf("line %q col %d → off %d → col %d", line, col, off, back)
			}
		}
	}
}

func TestUTF16ColToVisualCol_TabAndWide(t *testing.T) {
	line := "\t日本語"
	// UTF-16: tab=1 unit, then each CJK is 1 unit; visual after tab=4, 日=+2 → 6
	if got := UTF16ColToVisualCol(line, 1, 4); got != 4 {
		t.Fatalf("after tab utf16: %d want 4", got)
	}
	if got := UTF16ColToVisualCol(line, 2, 4); got != 6 {
		t.Fatalf("after 日 utf16: %d want 6", got)
	}
}

func TestExpandTabs_Stops(t *testing.T) {
	if got := ExpandTabs("a\tb", 4); got != "a   b" {
		t.Fatalf("ExpandTabs mid: %q", got)
	}
	if got := ExpandTabs("\tx", 4); got != "    x" {
		t.Fatalf("ExpandTabs lead: %q", got)
	}
	if got := ExpandTabs("abcd\tx", 4); got != "abcd    x" {
		t.Fatalf("ExpandTabs at stop: %q", got)
	}
}

func TestLineDisplayGeometry_Grapheme(t *testing.T) {
	emoji := "👨‍👩‍👧"
	runes, widths, total := lineDisplayGeometry(emoji+"x", 4)
	if len(runes) != 2 {
		t.Fatalf("runes=%d want 2 (collapsed grapheme + x)", len(runes))
	}
	if widths[0] != 2 || widths[1] != 1 {
		t.Fatalf("widths=%v", widths)
	}
	if total != 3 {
		t.Fatalf("total=%d want 3", total)
	}
}
