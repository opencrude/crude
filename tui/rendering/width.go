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
		"e\u0301", "n\u0303", "a\u0308", "o\u0304", "a\u0323\u0301",
		"界", "中", "文", "あ", "한", "Ａ",
		"😀", "🙂", "👩", "👨", "🧑", "💻",
		"👩‍💻", "👨‍💻", "🧑‍💻", "👩‍🔬", "👨‍🚀", "🧑‍⚕️",
		"👍🏻", "👍🏼", "👍🏽", "👍🏾", "👍🏿", "👩🏽‍💻", "👨🏿‍🚀", "🧑🏻‍⚕️",
		"👨‍👩‍👧‍👦", "👩‍👩‍👦", "👨‍👨‍👧", "👩‍❤️‍👨", "👩‍❤️‍💋‍👩",
		"🇺🇸", "🇨🇦", "🇺🇳", "🏴‍☠️", "🏳️‍⚧️", "🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f",
		"1️⃣", "9️⃣", "1⃣", "2⃣",
		"❤", "❤️", "♥", "♥️", "☺", "☺️", "☀", "☀️", "✈", "✈️",
		"क्ष", "कि", "நி",
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
