package bidi

import (
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// Painters that arrange the same text every frame (labels, chat rows,
// terminal rows) memoize the arranged line. The key holds everything the
// result depends on: the exact units, direction, geometry, the presentation
// policy and the terminal width profile, so no event has to invalidate it.

type lineKey struct {
	text          string
	dir           Direction
	limit, span   int
	policy, width uint64
}

const lineCacheLimit = 2048

var lineCache struct {
	sync.Mutex
	lines map[lineKey]*Line
}

// CachedLine returns the arranged display line of a paragraph identified by
// key. units builds the paragraph's units on a miss; they must be fully
// determined by key. limit bounds the columns of text used and span is the
// display line width; a negative span means the width of the text used.
// The result is shared and immutable; nil means "paint as written".
func CachedLine(key string, dir Direction, limit, span int, units func([]Unit) []Unit) *Line {
	if !Current().Arrange {
		return nil
	}
	k := lineKey{text: key, dir: dir, limit: limit, span: span, policy: Generation(), width: ansi.TerminalWidthGeneration()}
	lineCache.Lock()
	ln, ok := lineCache.lines[k]
	lineCache.Unlock()
	if ok {
		return ln
	}
	scratch := unitScratch.Get().(*[]Unit)
	all := units((*scratch)[:0])
	used, n := 0, 0
	for n < len(all) && used+max(0, all[n].Width) <= limit {
		used += max(0, all[n].Width)
		n++
	}
	if span < 0 {
		span = used
	}
	ln = Analyze(all[:n], dir, false).Line(0, used, span)
	clear(all)
	*scratch = all[:0]
	unitScratch.Put(scratch)

	lineCache.Lock()
	if lineCache.lines == nil || len(lineCache.lines) >= lineCacheLimit {
		lineCache.lines = make(map[lineKey]*Line, 256)
	}
	lineCache.lines[k] = ln
	lineCache.Unlock()
	return ln
}

var unitScratch = sync.Pool{New: func() any { s := make([]Unit, 0, 64); return &s }}

// TextLine arranges plain text as one paragraph: its grapheme clusters, with
// the shared terminal width profile, that fit in limit columns. It returns
// nil without any lookup for left-to-right text.
func TextLine(text string, dir Direction, limit, span int) *Line {
	if dir != RightToLeft && !NeedsLayout(text) {
		return nil
	}
	return CachedLine(text, dir, limit, span, func(dst []Unit) []Unit { return Clusters(text, dst) })
}
