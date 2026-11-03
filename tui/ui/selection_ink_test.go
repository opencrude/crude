package ui

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
)

func TestSelectionInkIsBoundedAndRebuildsWithTheme(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	for i := 0; i < 300; i++ {
		ink := cell.NewTrueColor(uint8(i), uint8(i>>1), uint8(i>>2))
		fg, bg := tokens.TextSelectionColors(ink)
		if cell.ContrastRatio(fg, bg) < 4.5 {
			t.Fatalf("ink %d is unreadable", i)
		}
	}
	if len(tokens.selectionInk) > 256 {
		t.Fatalf("unbounded selection palette: %d", len(tokens.selectionInk))
	}
	tokens.Rebuild(theme.DefaultLight())
	if len(tokens.selectionInk) != 0 {
		t.Fatal("theme change retained selected colors from the previous palette")
	}
}

func BenchmarkSelectionInkWarm(b *testing.B) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	inks := []cell.Color{
		rendering.ThemeColorToCell(th.Colors.Code.Keyword),
		rendering.ThemeColorToCell(th.Colors.Code.String),
		rendering.ThemeColorToCell(th.Colors.Code.Comment),
	}
	for _, ink := range inks {
		tokens.TextSelectionColors(ink)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokens.TextSelectionColors(inks[i%len(inks)])
	}
}
