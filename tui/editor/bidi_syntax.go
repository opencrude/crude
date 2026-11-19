package editor

import (
	"github.com/alecthomas/chroma/v2"
	"unicode"
	"unicode/utf8"

	"crude/tui/bidi"
)

// Source code order for right-to-left text (UTS #55, Unicode Source Code
// Handling, section 4.1, applying UAX #9 protocol HL4).
//
// The syntax highlighter already splits each line into tokens. Every token of
// code (a keyword, an identifier, an operator, a number, a string or comment
// delimiter) is an atom: an isolated left-to-right block whose position
// follows the source order and never depends on the text around it. The
// content of a string or a comment is an atom with the direction of its own
// first strong character, so a Persian comment reads naturally and two Hebrew
// strings never swap places. Atoms separated only by spaces form one
// syntactic element, isolated as a whole. Literal text (the prose of Markdown
// or Typst) is not code: it stays in the line's paragraph, so under "By line"
// a Persian sentence with #cite(<key>) inside is a right-to-left paragraph in
// which the citation keeps its own order.

// Per-column syntax roles. syntaxLine.roles holds one per source column; the
// high bit marks the first column of a token.
const (
	roleNone byte = iota
	roleSpace
	roleText
	roleCode
	roleNumber
	roleString
	roleComment

	roleMask       byte = 0x0f
	roleTokenStart byte = 0x80
)

// syntaxRole classifies a token for source code ordering.
func syntaxRole(kind chroma.TokenType) byte {
	switch {
	case kind == chroma.TextWhitespace:
		return roleSpace
	case kind == chroma.CommentPreproc, kind == chroma.CommentPreprocFile:
		return roleCode
	case kind.InCategory(chroma.Comment):
		return roleComment
	case kind == chroma.LiteralStringEscape, kind == chroma.LiteralStringInterpol,
		kind == chroma.LiteralStringAffix, kind == chroma.LiteralStringDelimiter:
		// Escapes and placeholders are syntax inside literal text.
		return roleCode
	case kind.InSubCategory(chroma.LiteralString):
		return roleString
	case kind.InSubCategory(chroma.LiteralNumber):
		return roleNumber
	case kind.InCategory(chroma.Text), kind.InCategory(chroma.Generic):
		return roleText
	default:
		return roleCode
	}
}

// roleSlab hands out the role slices of projected lines from shared chunks,
// so projecting a line costs no allocation of its own. Chunks are never
// reused: a slice's address identifies one projection (editorLineBidi.roles).
type roleSlab struct{ free []byte }

func (s *roleSlab) take(n int) []byte {
	if n == 0 {
		return nil
	}
	if len(s.free) < n {
		s.free = make([]byte, max(n, 16<<10))
	}
	out := s.free[:n:n]
	s.free = s.free[n:]
	return out
}

// lineRoles returns the syntax roles of a source line for the current
// revision, or nil when the line has no syntax yet.
func (e *Editor) lineRoles(lineIdx int) []byte {
	if e.syntaxBuffer != e.buf || e.syntaxRevision != e.buf.Revision() {
		return nil
	}
	return e.chromaLineCache[lineIdx].roles
}

// Isolate controls, prebuilt so that isolating a line allocates nothing.
var (
	beforeControls = [...]string{"", bidi.LRI, bidi.FSI, bidi.LRI + bidi.LRI, bidi.LRI + bidi.FSI}
	afterControls  = [...]string{"", bidi.PDI, bidi.PDI + bidi.PDI}
)

// isolateSource sets the isolates of a line's units from the syntax role of
// each unit (role returns roleNone for units without one, such as inlay
// hints). Units that already carry isolates are left alone.
func isolateSource(units []bidi.Unit, role func(i int) (r byte, start bool), byLine bool) {
	type atom struct {
		start, end int
		first      bool // first-strong content of a string or comment
	}
	var scratch [32]atom
	atoms := scratch[:0]
	for i := 0; i < len(units); {
		r, _ := role(i)
		end := i + 1
		for end < len(units) {
			next, start := role(end)
			if start || next != r {
				break
			}
			end++
		}
		switch r {
		case roleCode, roleNumber:
			if s, e := trimSpace(units, i, end); s < e {
				atoms = append(atoms, atom{start: s, end: e})
			}
		case roleString, roleComment:
			lead, trail := delimiters(units, i, end, r == roleComment)
			if s, e := trimSpace(units, i, lead); s < e {
				atoms = append(atoms, atom{start: s, end: e})
			}
			if s, e := trimSpace(units, lead, trail); s < e {
				atoms = append(atoms, atom{start: s, end: e, first: true})
			}
			if s, e := trimSpace(units, trail, end); s < e {
				atoms = append(atoms, atom{start: s, end: e})
			}
		}
		i = end
	}
	// Keep indentation and surrounding syntax outside prose, as IntelliJ's
	// BidiRunUtil separates indent and lexical regions. Balanced inline code
	// within prose (for example a citation) retains its own nested isolate.
	proseStart := 0
	closeProse := func(end int) {
		if byLine {
			if a, b := trimSpace(units, proseStart, end); a < b && (len(atoms) == 0 || literalBetween(units, a, b, role)) {
				units[a].Before = bidi.FSI + units[a].Before
				units[b-1].After += bidi.PDI
			}
		}
	}
	// Atoms separated only by spaces, inlay hints or units without a role
	// form one element. Literal text ends it.
	for first := 0; first < len(atoms); {
		last := first
		for last+1 < len(atoms) && !literalBetween(units, atoms[last].end, atoms[last+1].start, role) {
			last++
		}
		lo, hi := atoms[first].start, atoms[last].end
		firstRune, _ := utf8.DecodeRuneInString(units[lo].Text)
		lastRune, _ := utf8.DecodeRuneInString(units[hi-1].Text)
		// A transition through a closing/opening delimiter separates literal
		// regions (including successive table cells). Let the lexer identify
		// code; never interpret brackets in literal text as source delimiters.
		boundary := unicode.Is(unicode.Pe, firstRune) || unicode.Is(unicode.Ps, lastRune)
		if boundary {
			closeProse(lo)
			proseStart = hi
		}
		element := last > first
		for k := first; k <= last; k++ {
			a := atoms[k]
			open, close := 1, 1 // LRI ... PDI
			if a.first {
				open = 2 // FSI
			}
			if element && k == first {
				open += 2 // LRI before the atom's initiator
			}
			if element && k == last {
				close = 2
			}
			if units[a.start].Before != "" || units[a.end-1].After != "" {
				continue
			}
			units[a.start].Before = beforeControls[open]
			units[a.end-1].After = afterControls[close]
		}
		first = last + 1
	}
	closeProse(len(units))
}

// literalBetween reports whether units [from, to) hold literal text.
func literalBetween(units []bidi.Unit, from, to int, role func(int) (byte, bool)) bool {
	for i := from; i < to; i++ {
		if r, _ := role(i); r == roleText && !blank(units[i]) {
			return true
		}
	}
	return false
}

// trimSpace narrows [start, end) to exclude blank units at both ends.
func trimSpace(units []bidi.Unit, start, end int) (int, int) {
	for start < end && blank(units[start]) {
		start++
	}
	for end > start && blank(units[end-1]) {
		end--
	}
	return start, end
}

func blank(u bidi.Unit) bool {
	if u.Object {
		return false
	}
	for _, r := range u.Text {
		if r != ' ' && r != '\t' {
			return false
		}
	}
	return true
}

// delimiters returns where the content of a string or comment token
// [start, end) begins and ends: after its opening quote or comment marker
// and before its closing one. Spaces next to a delimiter belong to it.
func delimiters(units []bidi.Unit, start, end int, comment bool) (lead, trail int) {
	opening, closing := "\"'`", "\"'`"
	if comment {
		opening, closing = "/*#;%-!<", "*/->#"
	}
	lead = start
	for lead < end && (blank(units[lead]) || delimiterUnit(units[lead], opening)) {
		lead++
	}
	trail = end
	for trail > lead && (blank(units[trail-1]) || delimiterUnit(units[trail-1], closing)) {
		trail--
	}
	return lead, trail
}

func delimiterUnit(u bidi.Unit, set string) bool {
	if u.Object || len(u.Text) != 1 {
		return false
	}
	for i := 0; i < len(set); i++ {
		if u.Text[0] == set[i] {
			return true
		}
	}
	return false
}
