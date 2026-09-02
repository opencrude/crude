package rendering

import (
	"github.com/charmbracelet/x/ansi"
)

// WidthMethod is the one display-width algorithm used by Crude layout,
// surface packing, and ultraviolet through Bubble Tea. See
// ../../docs/reference/terminal-width.md.
//
// The initial guess is Unicode-16 grapheme width. Patched Bubble Tea then
// asynchronously CPR-measures the effective terminal or multiplexer and
// publishes runtime corrections inside this same x/ansi method. Measured
// legacy clusters may exceed two cells.
//
// Vendored x/ansi applies correctClusterWidth inside FirstGraphemeCluster so
// layout and UV always share one number. Never mix bare runewidth or uniseg
// for layout columns.
var WidthMethod = ansi.GraphemeWidth

// TerminalWidthProbeCorpus lists product chrome graphemes whose measured width
// must be learned from the effective terminal or multiplexer. Bubble Tea adds
// language/emoji representatives and deduplicates this list.
func TerminalWidthProbeCorpus() []string {
	return []string{
		"●", "○", "·", "│", "─", "┌", "┐", "└", "┘", "╭", "╮", "╰", "╯",
		"├", "┤", "┬", "┴", "┼", "╷", "╵", "▌", "▏", "▤", "▥", "▣",
		"◆", "◇", "☰", "⚡", "⚠", "✓", "✕", "✎", "…", "↑", "↓", "←", "→",
		"⇄", "⇡", "⇣", "⊕", "✂", "⌕", "⌘", "▸", "▾", "❯", "⚙", "☆", "✦", "♻",
		"✅", "⭐", "📎", "🔒", "🔓",
	}
}

// FirstGrapheme returns the first grapheme cluster and its cell width.
func FirstGrapheme(s string) (cluster string, width int) {
	return ansi.FirstGraphemeCluster(s, WidthMethod)
}

// PlainWidth is VisibleWidth for strings with no ANSI escapes.
func PlainWidth(s string) int {
	return WidthMethod.StringWidth(s)
}
