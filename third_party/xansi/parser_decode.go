package ansi

import (
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi/parser"
	"github.com/clipperhouse/uax29/v2/graphemes"
)

// State represents the state of the ANSI escape sequence parser used by
// [DecodeSequence].
type State = byte

// ANSI escape sequence states used by [DecodeSequence].
const (
	NormalState State = iota
	PrefixState
	ParamsState
	IntermedState
	EscapeState
	StringState
)

// DecodeSequence decodes the first ANSI escape sequence or a printable
// grapheme from the given data. It returns the sequence slice, the number of
// bytes read, the cell width for each sequence, and the new state.
//
// The cell width will always be 0 for control and escape sequences, 1 for
// ASCII printable characters, and the number of cells other Unicode characters
// occupy. It uses the uniseg package to calculate the width of Unicode
// graphemes and characters. This means it will always do grapheme clustering
// (mode 2027).
//
// Passing a non-nil [*Parser] as the last argument will allow the decoder to
// collect sequence parameters, data, and commands. The parser cmd will have
// the packed command value that contains intermediate and prefix characters.
// In the case of a OSC sequence, the cmd will be the OSC command number. Use
// [Cmd] and [Param] types to unpack command intermediates and prefixes as well
// as parameters.
//
// Zero [Cmd] means the CSI, DCS, or ESC sequence is invalid. Moreover, checking the
// validity of other data sequences, OSC, DCS, etc, will require checking for
// the returned sequence terminator bytes such as ST (ESC \\) and BEL).
//
// We store the command byte in [Cmd] in the most significant byte, the
// prefix byte in the next byte, and the intermediate byte in the least
// significant byte. This is done to avoid using a struct to store the command
// and its intermediates and prefixes. The command byte is always the least
// significant byte i.e. [Cmd & 0xff]. Use the [Cmd] type to unpack the
// command, intermediate, and prefix bytes. Note that we only collect the last
// prefix character and intermediate byte.
//
// The [p.Params] slice will contain the parameters of the sequence. Any
// sub-parameter will have the [parser.HasMoreFlag] set. Use the [Param] type
// to unpack the parameters.
//
// Example:
//
//	var state byte // the initial state is always zero [NormalState]
//	p := NewParser(32, 1024) // create a new parser with a 32 params buffer and 1024 data buffer (optional)
//	input := []byte("\x1b[31mHello, World!\x1b[0m")
//	for len(input) > 0 {
//		seq, width, n, newState := DecodeSequence(input, state, p)
//		log.Printf("seq: %q, width: %d", seq, width)
//		state = newState
//		input = input[n:]
//	}
//
// This function treats the text as a sequence of grapheme clusters.
func DecodeSequence[T string | []byte](b T, state byte, p *Parser) (seq T, width int, n int, newState byte) {
	return decodeSequence(GraphemeWidth, b, state, p)
}

// DecodeSequenceWc decodes the first ANSI escape sequence or a printable
// grapheme from the given data. It returns the sequence slice, the number of
// bytes read, the cell width for each sequence, and the new state.
//
// The cell width will always be 0 for control and escape sequences, 1 for
// ASCII printable characters, and the number of cells other Unicode characters
// occupy. It uses the uniseg package to calculate the width of Unicode
// graphemes and characters. This means it will always do grapheme clustering
// (mode 2027).
//
// Passing a non-nil [*Parser] as the last argument will allow the decoder to
// collect sequence parameters, data, and commands. The parser cmd will have
// the packed command value that contains intermediate and prefix characters.
// In the case of a OSC sequence, the cmd will be the OSC command number. Use
// [Cmd] and [Param] types to unpack command intermediates and prefixes as well
// as parameters.
//
// Zero [Cmd] means the CSI, DCS, or ESC sequence is invalid. Moreover, checking the
// validity of other data sequences, OSC, DCS, etc, will require checking for
// the returned sequence terminator bytes such as ST (ESC \\) and BEL).
//
// We store the command byte in [Cmd] in the most significant byte, the
// prefix byte in the next byte, and the intermediate byte in the least
// significant byte. This is done to avoid using a struct to store the command
// and its intermediates and prefixes. The command byte is always the least
// significant byte i.e. [Cmd & 0xff]. Use the [Cmd] type to unpack the
// command, intermediate, and prefix bytes. Note that we only collect the last
// prefix character and intermediate byte.
//
// The [p.Params] slice will contain the parameters of the sequence. Any
// sub-parameter will have the [parser.HasMoreFlag] set. Use the [Param] type
// to unpack the parameters.
//
// Example:
//
//	var state byte // the initial state is always zero [NormalState]
//	p := NewParser(32, 1024) // create a new parser with a 32 params buffer and 1024 data buffer (optional)
//	input := []byte("\x1b[31mHello, World!\x1b[0m")
//	for len(input) > 0 {
//		seq, width, n, newState := DecodeSequenceWc(input, state, p)
//		log.Printf("seq: %q, width: %d", seq, width)
//		state = newState
//		input = input[n:]
//	}
//
// This function treats the text as a sequence of wide characters and runes.
func DecodeSequenceWc[T string | []byte](b T, state byte, p *Parser) (seq T, width int, n int, newState byte) {
	return decodeSequence(WcWidth, b, state, p)
}

func decodeSequence[T string | []byte](m Method, b T, state State, p *Parser) (seq T, width int, n int, newState byte) {
	for i := 0; i < len(b); i++ {
		c := b[i]

		switch state {
		case NormalState:
			switch c {
			case ESC:
				if p != nil {
					if len(p.params) > 0 {
						p.params[0] = parser.MissingParam
					}
					p.cmd = 0
					p.paramsLen = 0
					p.dataLen = 0
				}
				state = EscapeState
				continue
			case CSI, DCS:
				if p != nil {
					if len(p.params) > 0 {
						p.params[0] = parser.MissingParam
					}
					p.cmd = 0
					p.paramsLen = 0
					p.dataLen = 0
				}
				state = PrefixState
				continue
			case OSC, APC, SOS, PM:
				if p != nil {
					p.cmd = parser.MissingCommand
					p.dataLen = 0
				}
				state = StringState
				continue
			}

			if p != nil {
				p.dataLen = 0
				p.paramsLen = 0
				p.cmd = 0
			}
			if c > US && c < DEL {
				// ASCII printable characters
				return b[i : i+1], 1, 1, NormalState
			}

			if c <= US || c == DEL || c < 0xC0 {
				// C0 & C1 control characters & DEL
				return b[i : i+1], 0, 1, NormalState
			}

			if utf8.RuneStart(c) {
				seq, width = FirstGraphemeCluster(b, m)
				i += len(seq)
				return b[:i], width, i, NormalState
			}

			// Invalid UTF-8 sequence
			return b[:i], 0, i, NormalState
		case PrefixState:
			if c >= '<' && c <= '?' {
				if p != nil {
					// We only collect the last prefix character.
					p.cmd &^= 0xff << parser.PrefixShift
					p.cmd |= int(c) << parser.PrefixShift
				}
				break
			}

			state = ParamsState
			fallthrough
		case ParamsState:
			if c >= '0' && c <= '9' {
				if p != nil {
					if p.params[p.paramsLen] == parser.MissingParam {
						p.params[p.paramsLen] = 0
					}

					p.params[p.paramsLen] *= 10
					p.params[p.paramsLen] += int(c - '0')
				}
				break
			}

			if c == ':' {
				if p != nil {
					p.params[p.paramsLen] |= parser.HasMoreFlag
				}
			}

			if c == ';' || c == ':' {
				if p != nil {
					p.paramsLen++
					if p.paramsLen < len(p.params) {
						p.params[p.paramsLen] = parser.MissingParam
					}
				}
				break
			}

			state = IntermedState
			fallthrough
		case IntermedState:
			if c >= ' ' && c <= '/' {
				if p != nil {
					p.cmd &^= 0xff << parser.IntermedShift
					p.cmd |= int(c) << parser.IntermedShift
				}
				break
			}

			if p != nil {
				// Increment the last parameter
				if p.paramsLen > 0 && p.paramsLen < len(p.params)-1 ||
					p.paramsLen == 0 && len(p.params) > 0 && p.params[0] != parser.MissingParam {
					p.paramsLen++
				}
			}

			if c >= '@' && c <= '~' {
				if p != nil {
					p.cmd &^= 0xff
					p.cmd |= int(c)
				}

				if HasDcsPrefix(b) {
					// Continue to collect DCS data
					if p != nil {
						p.dataLen = 0
					}
					state = StringState
					continue
				}

				return b[:i+1], 0, i + 1, NormalState
			}

			// Invalid CSI/DCS sequence
			return b[:i], 0, i, NormalState
		case EscapeState:
			switch c {
			case '[', 'P':
				if p != nil {
					if len(p.params) > 0 {
						p.params[0] = parser.MissingParam
					}
					p.paramsLen = 0
					p.cmd = 0
				}
				state = PrefixState
				continue
			case ']', 'X', '^', '_':
				if p != nil {
					p.cmd = parser.MissingCommand
					p.dataLen = 0
				}
				state = StringState
				continue
			}

			if c >= ' ' && c <= '/' {
				if p != nil {
					p.cmd &^= 0xff << parser.IntermedShift
					p.cmd |= int(c) << parser.IntermedShift
				}
				continue
			} else if c >= '0' && c <= '~' {
				if p != nil {
					p.cmd &^= 0xff
					p.cmd |= int(c)
				}
				return b[:i+1], 0, i + 1, NormalState
			}

			// Invalid escape sequence
			return b[:i], 0, i, NormalState
		case StringState:
			switch c {
			case BEL:
				if HasOscPrefix(b) {
					parseOscCmd(p)
					return b[:i+1], 0, i + 1, NormalState
				}
			case CAN, SUB:
				if HasOscPrefix(b) {
					// Ensure we parse the OSC command number
					parseOscCmd(p)
				}

				// Cancel the sequence
				return b[:i], 0, i, NormalState
			case ST:
				if HasOscPrefix(b) {
					// Ensure we parse the OSC command number
					parseOscCmd(p)
				}

				return b[:i+1], 0, i + 1, NormalState
			case ESC:
				if HasStPrefix(b[i:]) {
					if HasOscPrefix(b) {
						// Ensure we parse the OSC command number
						parseOscCmd(p)
					}

					// End of string 7-bit (ST)
					return b[:i+2], 0, i + 2, NormalState
				}

				// Otherwise, cancel the sequence
				return b[:i], 0, i, NormalState
			}

			if p != nil && p.dataLen < len(p.data) {
				p.data[p.dataLen] = c
				p.dataLen++

				// Parse the OSC command number
				if c == ';' && HasOscPrefix(b) {
					parseOscCmd(p)
				}
			}
		}
	}

	return b, 0, len(b), state
}

func parseOscCmd(p *Parser) {
	if p == nil || p.cmd != parser.MissingCommand {
		return
	}
	for j := range p.dataLen {
		d := p.data[j]
		if d < '0' || d > '9' {
			break
		}
		if p.cmd == parser.MissingCommand {
			p.cmd = 0
		}
		p.cmd *= 10
		p.cmd += int(d - '0')
	}
}

// Equal returns true if the given byte slices are equal.
func Equal[T string | []byte](a, b T) bool {
	return string(a) == string(b)
}

// HasPrefix returns true if the given byte slice has prefix.
func HasPrefix[T string | []byte](b, prefix T) bool {
	return len(b) >= len(prefix) && Equal(b[0:len(prefix)], prefix)
}

// HasSuffix returns true if the given byte slice has suffix.
func HasSuffix[T string | []byte](b, suffix T) bool {
	return len(b) >= len(suffix) && Equal(b[len(b)-len(suffix):], suffix)
}

// HasCsiPrefix returns true if the given byte slice has a CSI prefix.
func HasCsiPrefix[T string | []byte](b T) bool {
	return (len(b) > 0 && b[0] == CSI) ||
		(len(b) > 1 && b[0] == ESC && b[1] == '[')
}

// HasOscPrefix returns true if the given byte slice has an OSC prefix.
func HasOscPrefix[T string | []byte](b T) bool {
	return (len(b) > 0 && b[0] == OSC) ||
		(len(b) > 1 && b[0] == ESC && b[1] == ']')
}

// HasApcPrefix returns true if the given byte slice has an APC prefix.
func HasApcPrefix[T string | []byte](b T) bool {
	return (len(b) > 0 && b[0] == APC) ||
		(len(b) > 1 && b[0] == ESC && b[1] == '_')
}

// HasDcsPrefix returns true if the given byte slice has a DCS prefix.
func HasDcsPrefix[T string | []byte](b T) bool {
	return (len(b) > 0 && b[0] == DCS) ||
		(len(b) > 1 && b[0] == ESC && b[1] == 'P')
}

// HasSosPrefix returns true if the given byte slice has a SOS prefix.
func HasSosPrefix[T string | []byte](b T) bool {
	return (len(b) > 0 && b[0] == SOS) ||
		(len(b) > 1 && b[0] == ESC && b[1] == 'X')
}

// HasPmPrefix returns true if the given byte slice has a PM prefix.
func HasPmPrefix[T string | []byte](b T) bool {
	return (len(b) > 0 && b[0] == PM) ||
		(len(b) > 1 && b[0] == ESC && b[1] == '^')
}

// HasStPrefix returns true if the given byte slice has a ST prefix.
func HasStPrefix[T string | []byte](b T) bool {
	return (len(b) > 0 && b[0] == ST) ||
		(len(b) > 1 && b[0] == ESC && b[1] == '\\')
}

// HasEscPrefix returns true if the given byte slice has an ESC prefix.
func HasEscPrefix[T string | []byte](b T) bool {
	return len(b) > 0 && b[0] == ESC
}

// FirstGraphemeCluster returns the first grapheme cluster in the given string
// or byte slice, and its monospace display width.
//
// On WidthProfileModern (Kitty/Ghostty/foot/WezTerm/Alacritty and Windows
// Terminal with UAX 2027 negotiated ON) it returns the first UAX #29
// grapheme cluster and runs correctClusterWidth to cap at 2 and reconcile
// the table with what the terminal actually advances (☰ → 1, emoji → 2,
// VS15 → 1, etc.).
//
// On WidthProfileWindowsTerminal (WT_SESSION set, UAX 2027 OFF) the
// terminal still performs UAX #29 clustering (flags, ZWJ families, VS16
// sequences are one cluster) but it does NOT advance the cursor for the
// joining format codepoints inside the cluster. So the cluster width is
// the SUM of the per-codepoint WT widths with VS15/VS16/ZWJ/combining
// and the 2nd…Nth regional indicator all returning 0. Aggregated
// examples that match the terminal cursor advance: 🧑‍🌾=2, 🇯🇵=2,
// ♻️=1, ⚡️=2. Layout and ultraviolet must both go through this path or
// the dual-source rule (layout_width == uv_cell_width == terminal_advance)
// is violated and every later column shifts by 1+ — the exact artifact
// reported on Windows (lines with emojis end up one or more cells short,
// UV cannot reconcile because the cellbuf was filled with content where
// the terminal has no advance).
func FirstGraphemeCluster[T string | []byte](b T, m Method) (T, int) {
	if activeWidthProfile == WidthProfileWindowsTerminal {
		return firstWtCluster[T](b)
	}
	switch b := any(b).(type) {
	case string:
		cluster := graphemes.FromString(b).First()
		var w int
		if m == WcWidth {
			w = wcOptions.StringWidth(cluster)
		} else {
			w = dwOptions.String(cluster)
		}
		return T(cluster), correctClusterWidth(cluster, w)
	case []byte:
		cluster := graphemes.FromBytes(b).First()
		var w int
		if m == WcWidth {
			w = wcOptions.StringWidth(string(cluster))
		} else {
			w = dwOptions.Bytes(cluster)
		}
		return T(cluster), correctClusterWidth(string(cluster), w)
	}
	panic("unreachable")
}

// firstWtCluster returns the first UAX #29 grapheme cluster in b and its
// Windows Terminal cell advance. WT clusters as a unit (flag = one
// cluster, ZWJ family = one cluster, VS16-qualified glyph = one
// cluster) but only the base codepoints advance the cursor; VS15, VS16,
// ZWJ, combining marks, and the 2nd…Nth regional indicator all advance 0.
func firstWtCluster[T string | []byte](b T) (T, int) {
	var cluster string
	switch b := any(b).(type) {
	case string:
		cluster = graphemes.FromString(b).First()
	case []byte:
		cluster = string(graphemes.FromBytes(b).First())
	default:
		panic("unreachable")
	}
	if len(cluster) == 0 {
		var zero T
		return zero, 0
	}
	return T(cluster), wtClusterWidth(cluster)
}

// wtClusterWidth returns the cursor advance for a single UAX #29 cluster
// on Windows Terminal. It sums per-codepoint widths with format chars and
// the 2nd…Nth regional indicator contributing 0, and caps the result at 2
// (the documented WT/Ghostty/foot cap).
func wtClusterWidth(cluster string) int {
	width := 0
	riSeen := 0
	for i, r := range cluster {
		w := 0
		switch {
		case r == 0x200D, r == 0xFE0E, r == 0xFE0F:
			// ZWJ, VS15, VS16: never advance under WT (the cluster
			// base already consumed the cursor).
			w = 0
		case r >= 0x1F1E6 && r <= 0x1F1FF:
			// Regional indicator: WT renders the first RI of a pair as
			// 2 cells; the 2nd RI merges into the flag glyph with no
			// additional advance. Treat subsequent RIs as 0.
			if riSeen == 0 {
				w = 2
			}
			riSeen++
		case isWtCombining(r):
			// Combining marks advance 0 once they're attached to a
			// base; a standalone combining mark still occupies a cell
			// but our caller is iterating cluster codepoints where the
			// base is always present (UAX #29 attaches it).
			w = 0
		default:
			w = wtRuneWidth(r)
		}
		_ = i
		width += w
	}
	if width > 2 {
		width = 2
	}
	return width
}

// isWtCombining reports whether r is a combining mark that does not
// advance the cursor on Windows Terminal once attached to a base.
func isWtCombining(r rune) bool {
	// Common combining ranges; the exact table is in runewidth but for
	// layout we only care that attached combining marks contribute 0.
	switch {
	case r >= 0x0300 && r <= 0x036F: // Combining Diacritical Marks
		return true
	case r >= 0x1AB0 && r <= 0x1AFF: // Combining Diacritical Marks Extended
		return true
	case r >= 0x1DC0 && r <= 0x1DFF: // Combining Diacritical Marks Supplement
		return true
	case r >= 0x20D0 && r <= 0x20FF: // Combining Diacritical Marks for Symbols
		return true
	case r >= 0xFE20 && r <= 0xFE2F: // Combining Half Marks
		return true
	}
	return false
}

// firstWtCodepoint returns the first UTF-8 codepoint of b and its
// Windows Terminal cell advance. It does not perform grapheme
// clustering; the caller iterates by repeatedly calling FirstGraphemeCluster.
func firstWtCodepoint[T string | []byte](b T) (T, int) {
	var (
		n, w int
	)
	switch b := any(b).(type) {
	case string:
		_, n, w = decodeFirstWtCodepoint(b)
	case []byte:
		_, n, w = decodeFirstWtCodepointByte(b)
	default:
		panic("unreachable")
	}
	if n == 0 {
		return b, 0
	}
	return any(b[:n]).(T), w
}

func decodeFirstWtCodepointByte(b []byte) (rune, int, int) {
	if len(b) == 0 {
		return 0, 0, 0
	}
	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size == 1 {
		return r, 1, 1
	}
	return r, size, wtRuneWidth(r)
}

// correctClusterWidth implements the modern-terminal width consensus used by
// Ghostty, foot, Windows Terminal (grapheme mode), and Kitty (post–Unicode
// Core / text-sizing work):
//
//  1. Measure extended grapheme clusters (UAX #29), not code points.
//  2. Final cell width is only 0, 1, or 2 (never 3+).
//  3. Real emoji (emoji presentation / ZWJ sequences / VS16) stay 2.
//  4. Text presentation (VS15) forces 1.
//
// This runs for every FirstGraphemeCluster call, so Crude layout and
// ultraviolet (via the same x/ansi) always agree — required on all platforms
// including Windows Terminal.
//
// Note: the previous "trigrams ☰ U+2630..U+2637 → 1" override was deleted
// because real Windows Terminal advances 2 for those glyphs (confirmed on
// the user's WT 1.20+ session), and the override was making the cellbuf
// 1 cell short per line containing a trigram.
func correctClusterWidth(cluster string, w int) int {
	if cluster == "" {
		return 0
	}
	// Cap: Ghostty / foot / WT store at most 2 columns per grapheme.
	if w > 2 {
		w = 2
	}
	if w <= 1 {
		return w
	}

	var (
		hasVS15 bool // U+FE0E text presentation
		hasVS16 bool // U+FE0F emoji presentation
		hasZWJ  bool // U+200D zero-width joiner
		hasRI   bool // regional indicator (flags)
		base    rune
	)
	for i, r := range cluster {
		if i == 0 {
			base = r
		}
		switch {
		case r == 0xFE0E:
			hasVS15 = true
		case r == 0xFE0F:
			hasVS16 = true
		case r == 0x200D:
			hasZWJ = true
		case r >= 0x1F1E6 && r <= 0x1F1FF:
			hasRI = true
		}
	}

	// Explicit text presentation → narrow (even if tables say 2).
	if hasVS15 && !hasVS16 {
		return 1
	}
	// Emoji presentation / ZWJ sequences / flag pairs → wide.
	if hasVS16 || hasZWJ || hasRI {
		return 2
	}
	// Emoji & pictographic blocks (default emoji presentation).
	if isEmojiBase(base) {
		return 2
	}
	// CJK / fullwidth / Hangul / kana — always wide.
	if isEastAsianWide(base) {
		return 2
	}
	return w
}

// isEmojiBase reports code points that default to emoji presentation or live
// in the main emoji blocks (UTS #51). Not exhaustive of every pictograph,
// but covers the ranges terminals treat as 2-cell emoji.
func isEmojiBase(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1F9FF: // Misc Symbols and Pictographs … Supplemental Symbols and Pictographs
		return true
	case r >= 0x1FA00 && r <= 0x1FAFF: // Chess, symbols extended-A, etc.
		return true
	case r >= 0x1F600 && r <= 0x1F64F: // Emoticons
		return true
	case r >= 0x1F680 && r <= 0x1F6FF: // Transport and Map
		return true
	case r >= 0x2600 && r <= 0x26FF: // Misc symbols (☀⚡ etc.) — many are emoji-default
		// Subset: only those with Emoji_Presentation tend to be 2; ⚡ is 2,
		// ⚠ without VS16 is often 1. Keep table width for this block.
		return false
	case r >= 0x2700 && r <= 0x27BF: // Dingbats
		return false
	case r >= 0x231A && r <= 0x231B: // watch, hourglass
		return true
	case r >= 0x23E9 && r <= 0x23F3:
		return true
	case r >= 0x23F8 && r <= 0x23FA:
		return true
	case r == 0x25AA || r == 0x25AB || r == 0x25B6 || r == 0x25C0:
		return false
	case r >= 0x25FB && r <= 0x25FE:
		return true
	case r == 0x2614 || r == 0x2615: // umbrella, hot beverage
		return true
	case r >= 0x2648 && r <= 0x2653: // zodiac
		return true
	case r == 0x267F || r == 0x2693 || r == 0x26A1 || r == 0x26AA || r == 0x26AB:
		return true
	case r >= 0x26BD && r <= 0x26BE:
		return true
	case r >= 0x26C4 && r <= 0x26C5:
		return true
	case r == 0x26CE || r == 0x26D4 || r == 0x26EA || r == 0x26F2 || r == 0x26F3 || r == 0x26F5 || r == 0x26FA || r == 0x26FD:
		return true
	case r >= 0x2702 && r <= 0x27B0:
		// dingbats — mixed; leave to table
		return false
	case r >= 0x2B05 && r <= 0x2B07:
		return true
	case r >= 0x2B1B && r <= 0x2B1C:
		return true
	case r == 0x2B50 || r == 0x2B55:
		return true
	case r >= 0x2934 && r <= 0x2935:
		return true
	case r >= 0x3297 && r <= 0x3299:
		return true
	case r >= 0x1F000 && r <= 0x1F02F: // Mahjong / domino
		return true
	case r >= 0x1F0A0 && r <= 0x1F0FF: // Playing cards
		return true
	}
	return false
}

func isEastAsianWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return true
	case r >= 0x2E80 && r <= 0xA4CF: // CJK radicals … Yi
		return true
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul syllables
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK compatibility ideographs
		return true
	case r >= 0xFE10 && r <= 0xFE19:
		return true
	case r >= 0xFE30 && r <= 0xFE6F:
		return true
	case r >= 0xFF00 && r <= 0xFF60: // fullwidth forms
		return true
	case r >= 0xFFE0 && r <= 0xFFE6:
		return true
	case r >= 0x20000 && r <= 0x3FFFD: // CJK ext
		return true
	}
	return false
}

// Cmd represents a sequence command. This is used to pack/unpack a sequence
// command with its intermediate and prefix characters. Those are commonly
// found in CSI and DCS sequences.
type Cmd int

// Prefix returns the unpacked prefix byte of the CSI sequence.
// This is always gonna be one of the following '<' '=' '>' '?' and in the
// range of 0x3C-0x3F.
// Zero is returned if the sequence does not have a prefix.
func (c Cmd) Prefix() byte {
	return byte(parser.Prefix(int(c)))
}

// Intermediate returns the unpacked intermediate byte of the CSI sequence.
// An intermediate byte is in the range of 0x20-0x2F. This includes these
// characters from ' ', '!', '"', '#', '$', '%', '&', ”', '(', ')', '*', '+',
// ',', '-', '.', '/'.
// Zero is returned if the sequence does not have an intermediate byte.
func (c Cmd) Intermediate() byte {
	return byte(parser.Intermediate(int(c)))
}

// Final returns the unpacked command byte of the CSI sequence.
func (c Cmd) Final() byte {
	return byte(parser.Command(int(c)))
}

// Command packs a command with the given prefix, intermediate, and final. A
// zero byte means the sequence does not have a prefix or intermediate.
//
// Prefixes are in the range of 0x3C-0x3F that is one of `<=>?`.
//
// Intermediates are in the range of 0x20-0x2F that is anything in
// `!"#$%&'()*+,-./`.
//
// Final bytes are in the range of 0x40-0x7E that is anything in the range
// `@A–Z[\]^_`a–z{|}~`.
func Command(prefix, inter, final byte) (c int) {
	c = int(final)
	c |= int(prefix) << parser.PrefixShift
	c |= int(inter) << parser.IntermedShift
	return c
}

// Param represents a sequence parameter. Sequence parameters with
// sub-parameters are packed with the HasMoreFlag set. This is used to unpack
// the parameters from a CSI and DCS sequences.
type Param int

// Param returns the unpacked parameter at the given index.
// It returns the default value if the parameter is missing.
func (s Param) Param(def int) int {
	p := int(s) & parser.ParamMask
	if p == parser.MissingParam {
		return def
	}
	return p
}

// HasMore unpacks the HasMoreFlag from the parameter.
func (s Param) HasMore() bool {
	return s&parser.HasMoreFlag != 0
}

// Parameter packs an escape code parameter with the given parameter and
// whether this parameter has following sub-parameters.
func Parameter(p int, hasMore bool) (s int) {
	s = p & parser.ParamMask
	if hasMore {
		s |= parser.HasMoreFlag
	}
	return s
}
