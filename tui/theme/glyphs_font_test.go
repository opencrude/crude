package theme

import "testing"

// Fold headers use chevrons: codicons with a Nerd Font, Unicode otherwise.
// Switching the font back restores the Unicode pair.
func TestWithFontGlyphsChoosesFoldChevrons(t *testing.T) {
	nerd := WithFontGlyphs(DefaultGlyphs(), true)
	if nerd.FoldExpanded != "\ueab4" || nerd.FoldCollapsed != "\ueab6" {
		t.Fatalf("Nerd Font fold glyphs %q %q", nerd.FoldExpanded, nerd.FoldCollapsed)
	}
	plain := WithFontGlyphs(nerd, false)
	if plain.FoldExpanded != "⌄" || plain.FoldCollapsed != "›" || plain.TreeExpanded != "▾" {
		t.Fatalf("plain fold glyphs %q %q", plain.FoldExpanded, plain.FoldCollapsed)
	}
}
