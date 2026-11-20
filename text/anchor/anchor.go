// Package anchor captures and resolves ranges of text that must survive later
// edits, such as review comments on a plan the user keeps editing.
//
// An anchor follows the W3C Web Annotation model: a TextQuoteSelector (the
// exact text with some context before and after it) and a
// TextPositionSelector (byte offsets) that is only a hint. Resolution follows
// the Hypothesis client: every exact occurrence of the quote is scored by its
// context and its distance from the hint, and the best one wins. When the
// quote no longer occurs exactly, the fuzzy fallback matches the quote the way
// diff-match-patch applies a patch: Bitap matching of the quote (or its first
// and last MatchMaxBits bytes) near the hint, rejected when the matched text
// differs from the quote by more than half.
package anchor

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/sergi/go-diff/diffmatchpatch"

	"crude/types"
)

// contextBytes is how much text before and after the quote an anchor keeps.
const contextBytes = 32

// maxCandidates bounds how many exact occurrences are scored.
const maxCandidates = 4096

// Hypothesis' weights: the quote decides, context breaks ties between equal
// quotes, and the position hint breaks the rest.
const (
	prefixWeight = 20
	suffixWeight = 20
	posWeight    = 2
)

// Capture anchors the range [start, end) of text. Offsets are clamped to the
// text and moved back to rune boundaries.
func Capture(text string, start, end int) types.TextAnchor {
	start, end = clamp(text, start), clamp(text, end)
	if end < start {
		start, end = end, start
	}
	return types.TextAnchor{
		Start:  start,
		End:    end,
		Exact:  text[start:end],
		Prefix: text[clamp(text, start-contextBytes):start],
		Suffix: text[end:clampForward(text, end+contextBytes)],
	}
}

// Result is where an anchor is in a text. Found is false when the quoted text
// no longer occurs; Start and End then hold the clamped position hint, so a
// caller can still show the anchor near where it was. Exact is true when the
// quote occurs unchanged.
type Result struct {
	Start, End int
	Found      bool
	Exact      bool
}

// Resolve finds the anchored range in text.
func Resolve(text string, a types.TextAnchor) Result {
	hint := clamp(text, a.Start)
	if a.Exact == "" {
		// A position-only anchor, such as a comment on an empty line.
		return Result{Start: hint, End: hint, Found: true, Exact: true}
	}
	if best, ok := bestExact(text, a, hint); ok {
		return Result{Start: best, End: best + len(a.Exact), Found: true, Exact: true}
	}
	if start, end, ok := fuzzy(text, a.Exact, hint); ok {
		return Result{Start: start, End: end, Found: true}
	}
	end := clamp(text, hint+max(0, a.End-a.Start))
	return Result{Start: hint, End: max(hint, end)}
}

func bestExact(text string, a types.TextAnchor, hint int) (int, bool) {
	best, bestScore := -1, -1.0
	for from, count := 0, 0; from <= len(text) && count < maxCandidates; count++ {
		index := strings.Index(text[from:], a.Exact)
		if index < 0 {
			break
		}
		at := from + index
		if score := contextScore(text, a, at, hint); score > bestScore {
			best, bestScore = at, score
		}
		_, size := utf8.DecodeRuneInString(text[at:])
		from = at + max(1, size)
	}
	return best, best >= 0
}

// contextScore follows Hypothesis' matchQuote scoring for an exact quote:
// how much of the expected context surrounds this occurrence, plus a small
// preference for the occurrence nearest the hint.
func contextScore(text string, a types.TextAnchor, at, hint int) float64 {
	prefix, suffix := 1.0, 1.0
	if a.Prefix != "" {
		before := text[max(0, at-len(a.Prefix)):at]
		prefix = float64(commonSuffix(before, a.Prefix)) / float64(len(a.Prefix))
	}
	if a.Suffix != "" {
		end := at + len(a.Exact)
		after := text[end:min(len(text), end+len(a.Suffix))]
		suffix = float64(commonPrefix(after, a.Suffix)) / float64(len(a.Suffix))
	}
	position := 1.0
	if len(text) > 0 {
		position = 1 - float64(abs(at-hint))/float64(len(text))
	}
	return prefixWeight*prefix + suffixWeight*suffix + posWeight*position
}

// fuzzy locates a quote that changed slightly, as diff-match-patch's
// PatchApply locates the text a patch expects: Bitap finds where the quote
// starts (and, for a long quote, where its last bytes are), then a diff of the
// quote against the text there gives the matched range and its quality.
func fuzzy(text, quote string, hint int) (int, int, bool) {
	dmp := diffmatchpatch.New()
	bits := dmp.MatchMaxBits
	var start, limit int
	if len(quote) > bits {
		start = matchNear(dmp, text, quote[:bits], hint)
		if start >= 0 {
			last := matchNear(dmp, text, quote[len(quote)-bits:], hint+len(quote)-bits)
			if last < 0 || last <= start {
				return 0, 0, false
			}
			limit = last + bits
		}
	} else {
		start = matchNear(dmp, text, quote, hint)
		// The quote may have grown; the diff below drops what follows it.
		limit = start + len(quote) + len(quote)/2 + 1
	}
	if start < 0 {
		return 0, 0, false
	}
	start, limit = clamp(text, start), clampForward(text, limit)
	if limit <= start {
		return 0, 0, false
	}
	diffs := dmp.DiffCleanupSemanticLossless(dmp.DiffMain(quote, text[start:limit], false))
	for len(diffs) > 0 && diffs[len(diffs)-1].Type == diffmatchpatch.DiffInsert {
		diffs = diffs[:len(diffs)-1]
	}
	end := start
	for _, d := range diffs {
		if d.Type != diffmatchpatch.DiffDelete {
			end += len(d.Text)
		}
	}
	if end <= start || float64(dmp.DiffLevenshtein(diffs))/float64(utf8.RuneCountInString(quote)) > dmp.PatchDeleteThreshold {
		return 0, 0, false
	}
	return start, end, true
}

// matchNear is MatchMain limited to the text it can match. Bitap scores a
// candidate by its errors plus its distance from loc divided by
// MatchDistance, and rejects scores above MatchThreshold, so no match lies
// further than MatchDistance*MatchThreshold bytes from loc. Bitap's scratch
// arrays grow with the absolute offset searched, so searching the whole text
// late in a large document would allocate in proportion to the document.
func matchNear(dmp *diffmatchpatch.DiffMatchPatch, text, pattern string, loc int) int {
	radius := int(math.Ceil(float64(dmp.MatchDistance)*dmp.MatchThreshold)) + len(pattern)
	lo := clamp(text, loc-radius)
	hi := clampForward(text, loc+radius+len(pattern))
	found := dmp.MatchMain(text[lo:hi], pattern, loc-lo)
	if found < 0 {
		return -1
	}
	return lo + found
}

// Lines returns the one-based first and last lines of [start, end) in text.
// A range that ends at the start of a line ends on the line before it.
func Lines(text string, start, end int) (first, last int) {
	start, end = clamp(text, start), clamp(text, end)
	first = 1 + strings.Count(text[:start], "\n")
	last = first + strings.Count(text[start:end], "\n")
	if end > start && text[end-1] == '\n' {
		last--
	}
	return first, max(first, last)
}

// clamp limits offset to text and moves it back to a rune boundary.
func clamp(text string, offset int) int {
	offset = min(max(0, offset), len(text))
	for offset > 0 && offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset--
	}
	return offset
}

// clampForward limits offset to text and moves it forward to a rune boundary.
func clampForward(text string, offset int) int {
	offset = min(max(0, offset), len(text))
	for offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset++
	}
	return offset
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func commonSuffix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
