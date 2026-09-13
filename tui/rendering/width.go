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
	corpus := []string{
		"●", "○", "·", "│", "─", "┌", "┐", "└", "┘", "╭", "╮", "╰", "╯",
		"├", "┤", "┬", "┴", "┼", "╷", "╵", "▌", "▏", "▤", "▥", "▣",
		"◆", "◇", "☰", "⚡", "⚠", "✓", "✕", "✎", "…", "↑", "↓", "←", "→",
		"⇄", "⇡", "⇣", "⊕", "✂", "⌕", "⌘", "▸", "▾", "❯", "⚙", "☆", "✦", "♻",
		"✅", "⭐", "📎", "🔒", "🔓",
		// Text/emoji presentation boundaries. Terminals and fallback fonts often
		// render these unqualified BMP symbols differently from Unicode's emoji
		// default; exact CPR measurements prevent one-cell stale line tails.
		"⌚", "⌛", "⏩", "⏪", "⏫", "⏬", "⏰", "⏳", "⏸", "⏹", "⏺",
		"▪", "▫", "▶", "◀", "◻", "◼", "◽", "◾", "⎿",
		"☔", "☕", "♿", "⚓", "⚪", "⚫", "⚽", "⚾", "⛄", "⛅", "⛎", "⛔",
		"⛪", "⛲", "⛳", "⛵", "⛺", "⛽", "⬅", "⬆", "⬇", "⬛", "⬜", "⭕", "㊗", "㊙",
		"♈", "♉", "♊", "♋", "♌", "♍", "♎", "♏", "♐", "♑", "♒", "♓",
		"⏺︎", "⏺️", "▶︎", "▶️", "⚠︎", "⚠️", "©︎", "©️", "™︎", "™️",
		"#️⃣", "*️⃣", "0️⃣",
		// Crude's fixed Nerd Font chrome. Dynamic file icons share the same PUA
		// cell policy, while these representatives protect every persistent rail.
		"\uf17b", "\uf120", "\uf418", "\uf07b", "\uf085",
	}
	return corpus
}

// FirstGrapheme returns the first grapheme cluster and its cell width.
func FirstGrapheme(s string) (cluster string, width int) {
	return ansi.FirstGraphemeCluster(s, WidthMethod)
}

// PlainWidth is VisibleWidth for strings with no ANSI escapes.
func PlainWidth(s string) int {
	return WidthMethod.StringWidth(s)
}
