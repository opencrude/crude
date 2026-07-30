package editor

import (
	"regexp"
	"strings"
	"time"
)

// SearchOptions controls how a query is matched. Defaults are case-
// insensitive, substring (not whole word), and literal (not regex).
type SearchOptions struct {
	CaseSensitive bool
	WholeWord     bool
	Regex         bool
}

// SearchMatch is a single hit within the buffer. Start and End are
// line/col positions; the match is the half-open range [Start, End).
type SearchMatch struct {
	Start   Position
	End     Position
	Matched string
}

// SearchState holds the live find/replace UI state for one tab. The
// editor widget reads Matches and CurrentIdx to render highlights and
// the floating popup reads Query, Replace, Options, and CurrentIdx to
// show its UI.
type SearchState struct {
	// UI text
	Query   string
	Replace string
	Options SearchOptions

	// Computed matches for the current Query + Options. Updated
	// whenever the user types a character in the find input. Matches
	// are in buffer order; CurrentIdx is an index into this slice.
	Matches     []SearchMatch
	CurrentIdx  int
	LastCompute time.Time

	// True when the search UI is open (Ctrl+F or Ctrl+H pressed).
	// The editor render path checks this to draw highlights.
	Open bool
}

// NewSearchState returns a fresh search state.
func NewSearchState() *SearchState {
	return &SearchState{
		CurrentIdx: -1,
	}
}

// HasMatches reports whether the last search found anything.
func (s *SearchState) HasMatches() bool {
	return len(s.Matches) > 0
}

// CurrentMatch returns the match under the cursor, or false if none.
func (s *SearchState) CurrentMatch() (SearchMatch, bool) {
	if s.CurrentIdx < 0 || s.CurrentIdx >= len(s.Matches) {
		return SearchMatch{}, false
	}
	return s.Matches[s.CurrentIdx], true
}

// Next advances the cursor to the next match (wrapping around).
func (s *SearchState) Next() {
	if len(s.Matches) == 0 {
		s.CurrentIdx = -1
		return
	}
	s.CurrentIdx = (s.CurrentIdx + 1) % len(s.Matches)
}

// Prev moves the cursor to the previous match (wrapping around).
func (s *SearchState) Prev() {
	if len(s.Matches) == 0 {
		s.CurrentIdx = -1
		return
	}
	s.CurrentIdx--
	if s.CurrentIdx < 0 {
		s.CurrentIdx = len(s.Matches) - 1
	}
}

// Reset clears matches and resets the cursor. Called when the query
// changes or the buffer mutates.
func (s *SearchState) Reset() {
	s.Matches = nil
	s.CurrentIdx = -1
}

// computeMatches populates Matches for the given buffer text. Returns
// the matches in source order.
//
// Matching rules:
//   - Regex: compile the query as a Go regexp. \b is honored for whole
//     word, but if WholeWord is true we wrap the pattern in \b…\b.
//   - Literal: scan the text with strings.Index repeatedly. Case-
//     insensitive uses strings.ToLower of both sides (the buffer is
//     scanned once on each call; the buffer is typically <1MB so this
//     is acceptable). WholeWord wraps the query with \b when used in
//     non-regex mode (re2 syntax for word boundaries).
//
// Returns nil if the query is empty or fails to compile.
func (s *SearchState) computeMatches(bufText string) {
	s.Reset()
	if s.Query == "" {
		return
	}

	if s.Options.Regex {
		pattern := s.Query
		if s.Options.WholeWord {
			pattern = `\b` + pattern + `\b`
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return
		}
		if !s.Options.CaseSensitive {
			re, err = regexp.Compile("(?i)" + pattern)
			if err != nil {
				return
			}
		}
		s.scanRegexMatches(re, bufText)
		return
	}

	// Literal substring.
	needle := s.Query
	hay := bufText
	if !s.Options.CaseSensitive {
		needle = strings.ToLower(needle)
		hay = strings.ToLower(bufText)
	}

	if s.Options.WholeWord {
		s.scanWholeWordLiteral(needle, bufText, hay)
		return
	}

	// Plain substring scan. O(n*m) worst case, but Go's strings.Index
	// is highly optimized and our buffers are small.
	from := 0
	for {
		idx := strings.Index(hay[from:], needle)
		if idx < 0 {
			return
		}
		abs := from + idx
		// matched text comes from the original (case-preserved) buffer
		matched := bufText[abs : abs+len(needle)]
		start, end := offsetToLineCol(bufText, abs, abs+len(needle))
		s.Matches = append(s.Matches, SearchMatch{Start: start, End: end, Matched: matched})
		from = abs + len(needle)
		if from > len(bufText) {
			return
		}
	}
}

func (s *SearchState) scanRegexMatches(re *regexp.Regexp, bufText string) {
	locs := re.FindAllStringIndex(bufText, -1)
	for _, loc := range locs {
		start, end := offsetToLineCol(bufText, loc[0], loc[1])
		s.Matches = append(s.Matches, SearchMatch{
			Start:   start,
			End:     end,
			Matched: bufText[loc[0]:loc[1]],
		})
	}
}

func (s *SearchState) scanWholeWordLiteral(needle, orig, lowered string) {
	from := 0
	for {
		idx := strings.Index(lowered[from:], needle)
		if idx < 0 {
			return
		}
		abs := from + idx
		// Word boundary: char before (if any) and after (if any) must
		// not be a letter/digit/underscore.
		if isWordBoundary(lowered, abs) && isWordBoundary(lowered, abs+len(needle)) {
			start, end := offsetToLineCol(orig, abs, abs+len(needle))
			s.Matches = append(s.Matches, SearchMatch{
				Start:   start,
				End:     end,
				Matched: orig[abs : abs+len(needle)],
			})
		}
		from = abs + 1
		if from > len(lowered) {
			return
		}
	}
}

func isWordBoundary(s string, idx int) bool {
	if idx <= 0 || idx >= len(s) {
		return true
	}
	if idx == 0 {
		return true
	}
	c := s[idx]
	prev := s[idx-1]
	return !isSearchWordChar(c) || !isSearchWordChar(prev)
}

// isSearchWordChar reports whether the byte is a word character for
// whole-word matching. Distinct from movement.go's isWordChar (which
// takes a rune) because search scans byte-by-byte for speed on the
// hot path of repeated substring scans.
func isSearchWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// offsetToLineCol converts a byte offset pair (start, end) into
// (startPos, endPos) with visual columns (tab stops + grapheme widths).
func offsetToLineCol(text string, startOff, endOff int) (Position, Position) {
	startLine, startCol := ByteOffsetToLineCol(text, startOff, tabWidth)
	endLine, endCol := ByteOffsetToLineCol(text, endOff, tabWidth)
	return Position{Line: startLine, Col: startCol}, Position{Line: endLine, Col: endCol}
}

// Search returns the editor's SearchState. Callers can read Query,
// Options, Matches, CurrentIdx, etc. and call SetQuery/SetOptions to
// trigger a recompute.
func (e *Editor) Search() *SearchState {
	return e.search
}

// SetQuery updates the search query and recomputes matches against the
// current buffer contents. The cursor is moved to the first match if
// there is one. Returns the new match count.
func (e *Editor) SetQuery(q string) int {
	if e.search == nil {
		e.search = NewSearchState()
	}
	e.search.Query = q
	e.search.Open = true
	e.recomputeMatches()
	return len(e.search.Matches)
}

// SetOptions updates match options and recomputes matches. The
// CurrentIdx is preserved as long as the new match list is at least
// that long.
func (e *Editor) SetOptions(opts SearchOptions) {
	if e.search == nil {
		e.search = NewSearchState()
	}
	e.search.Options = opts
	e.recomputeMatches()
}

// SetReplace updates the replacement string. Does not affect matches.
func (e *Editor) SetReplace(r string) {
	if e.search == nil {
		e.search = NewSearchState()
	}
	e.search.Replace = r
}

// SetSearchOpen toggles whether the search UI is visible. When
// closed, highlights are not drawn.
func (e *Editor) SetSearchOpen(open bool) {
	if e.search == nil {
		e.search = NewSearchState()
	}
	e.search.Open = open
	if !open {
		e.search.Reset()
	}
}

// NextMatch jumps the editor cursor to the next match (and scrolls
// it into view). Wraps around. Returns the match count and whether
// we found one.
func (e *Editor) NextMatch() (int, bool) {
	if e.search == nil || !e.search.HasMatches() {
		return 0, false
	}
	e.search.Next()
	if m, ok := e.search.CurrentMatch(); ok {
		e.SetCursorPosition(m.Start.Line, m.Start.Col)
		e.ScrollToCursor()
		return len(e.search.Matches), true
	}
	return 0, false
}

// PrevMatch jumps the editor cursor to the previous match.
func (e *Editor) PrevMatch() (int, bool) {
	if e.search == nil || !e.search.HasMatches() {
		return 0, false
	}
	e.search.Prev()
	if m, ok := e.search.CurrentMatch(); ok {
		e.SetCursorPosition(m.Start.Line, m.Start.Col)
		e.ScrollToCursor()
		return len(e.search.Matches), true
	}
	return 0, false
}

// ReplaceCurrent replaces the match under the cursor with the
// replace string and re-runs the search. The cursor stays near the
// replacement. Returns the new match count.
func (e *Editor) ReplaceCurrent() int {
	if e.search == nil || e.search.CurrentIdx < 0 {
		return 0
	}
	if e.search.CurrentIdx >= len(e.search.Matches) {
		return 0
	}
	m := e.search.Matches[e.search.CurrentIdx]
	repl := e.search.Replace

	// Delete the matched range, insert the replacement. We use
	// buf.Delete + buf.Insert which both fire undo/redo entries.
	e.buf.Delete(m.Start, m.End)
	if repl != "" {
		e.buf.Insert(m.Start, []byte(repl))
	}
	e.modified = true
	if e.buffer != nil {
		e.buffer.MarkDirty()
	}
	e.notifyContentChanged()
	e.invalidateEditorCaches()

	// Recompute matches and try to keep the cursor near the same
	// position. Since buffer text changed, we just keep CurrentIdx
	// clamped to the new list size and let the caller re-jump.
	e.recomputeMatches()
	if e.search.CurrentIdx >= len(e.search.Matches) {
		e.search.CurrentIdx = len(e.search.Matches) - 1
	}
	if e.search.CurrentIdx < 0 {
		e.search.CurrentIdx = 0
	}
	if m, ok := e.search.CurrentMatch(); ok {
		e.SetCursorPosition(m.Start.Line, m.Start.Col)
		e.ScrollToCursor()
	}
	return len(e.search.Matches)
}

// ReplaceAll replaces every match in the buffer. Returns the number
// of replacements actually made.
func (e *Editor) ReplaceAll() int {
	if e.search == nil || !e.search.HasMatches() {
		return 0
	}
	count := 0
	// Iterate from the end so earlier offsets stay valid as we
	// mutate the buffer.
	for i := len(e.search.Matches) - 1; i >= 0; i-- {
		m := e.search.Matches[i]
		repl := e.search.Replace
		e.buf.Delete(m.Start, m.End)
		if repl != "" {
			e.buf.Insert(m.Start, []byte(repl))
		}
		count++
	}
	e.modified = true
	if e.buffer != nil {
		e.buffer.MarkDirty()
	}
	e.notifyContentChanged()
	e.invalidateEditorCaches()
	e.recomputeMatches()
	return count
}

// recomputeMatches re-runs the search against the current buffer.
func (e *Editor) recomputeMatches() {
	if e.search == nil {
		return
	}
	prevIdx := e.search.CurrentIdx
	text := string(e.buf.Bytes())
	e.search.computeMatches(text)
	e.search.LastCompute = time.Now()
	// Try to preserve the cursor position. If the new list is
	// shorter, clamp.
	if prevIdx >= len(e.search.Matches) {
		prevIdx = len(e.search.Matches) - 1
	}
	if prevIdx < 0 && len(e.search.Matches) > 0 {
		prevIdx = 0
	}
	e.search.CurrentIdx = prevIdx
}
