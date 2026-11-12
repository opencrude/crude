package editor

import (
	"regexp"
	"slices"

	"crude/insight/catalog"
)

// Fold ranges port VS Code's indentation range provider: computeRanges and
// RangesCollector (microsoft/vscode, src/vs/editor/contrib/folding/browser/
// indentRangeProvider.ts at e608ca420ca76f4bfc2cfbff02538d66242e8174, MIT
// License, Copyright (c) Microsoft Corporation). VS Code uses this provider
// when no language server supplies folding ranges; Crude has no such server
// request, so it is the only fold source. A line folds the lines below it that
// are indented more. Region markers fold whatever their indentation. The two
// language facts (offSide and the markers) come from catalog data; there is no
// language-named code. Lines here are zero-based where VS Code's are one-based.

// maxIndentFolds is VS Code's editor.foldingMaximumRegions default
// (MAX_FOLDING_REGIONS_FOR_INDENT_DEFAULT).
const maxIndentFolds = 5000

// previousRegion is VS Code's PreviousRegion: indent is -2 for a marker.
type previousRegion struct {
	indent, endAbove, line int
}

// Region marker kinds of one line.
const (
	foldMarkerNone = iota
	foldMarkerStart
	foldMarkerEnd
)

// foldLineFacts classifies one line: its indentation (-1 when it has no
// text) and its region marker kind. VS Code tests markers only on lines with
// text, matching the combined pattern `(start)|(?:end)`.
func foldLineFacts(content []byte, tabSize int, markers *regexp.Regexp) (indent, marker int) {
	indent = computeIndentLevel(content, tabSize)
	if indent >= 0 && markers != nil {
		if m := markers.FindSubmatchIndex(content); m != nil {
			marker = foldMarkerEnd
			if m[2] >= 0 {
				marker = foldMarkerStart
			}
		}
	}
	return indent, marker
}

// indentFoldScan holds the scratch buffers of computeRanges, so a rescan
// after an edit allocates nothing when the result fits the retained buffers.
type indentFoldScan struct {
	collected            []FoldRange // insertFirst order: bottom-up
	indents, occurrences []int
	regions              []previousRegion
	out                  []FoldRange
}

// compute returns nested fold ranges sorted by start line. line reports one
// line's facts (see foldLineFacts). The result is scratch owned by s.
func (s *indentFoldScan) compute(lineCount int, line func(int) (indent, marker int), offSide bool, limit int) []FoldRange {
	s.collected, s.indents, s.occurrences = s.collected[:0], s.indents[:0], s.occurrences[:0]
	insertFirst := func(start, end, indent int) {
		s.collected = append(s.collected, FoldRange{start, end})
		s.indents = append(s.indents, indent)
		if indent < 1000 {
			for indent >= len(s.occurrences) {
				s.occurrences = append(s.occurrences, 0)
			}
			s.occurrences[indent]++
		}
	}
	regions := append(s.regions[:0], previousRegion{indent: -1, endAbove: lineCount, line: lineCount}) // sentinel
	for l := lineCount - 1; l >= 0; l-- {
		indent, marker := line(l)
		previous := &regions[len(regions)-1]
		if indent == -1 {
			if offSide {
				// Empty lines belong to the previous block; this only moves the
				// end of the block above.
				previous.endAbove = l
			}
			continue
		}
		switch marker {
		case foldMarkerStart:
			i := len(regions) - 1
			for i > 0 && regions[i].indent != -2 {
				i--
			}
			if i > 0 {
				regions = regions[:i+1]
				previous = &regions[i]
				// A marker range includes its end line.
				insertFirst(l, previous.line, indent)
				previous.line, previous.indent, previous.endAbove = l, indent, l
				continue
			}
			// No end marker below: treat the line as a regular line.
		case foldMarkerEnd:
			regions = append(regions, previousRegion{indent: -2, endAbove: l, line: l})
			continue
		}
		if previous.indent > indent {
			for previous.indent > indent {
				regions = regions[:len(regions)-1]
				previous = &regions[len(regions)-1]
			}
			if end := previous.endAbove - 1; end-l >= 1 {
				insertFirst(l, end, indent)
			}
		}
		if previous.indent == indent {
			previous.endAbove = l
		} else {
			regions = append(regions, previousRegion{indent: indent, endAbove: l, line: l})
		}
	}
	s.regions = regions
	s.out = indentRangesWithin(s.out[:0], s.collected, s.indents, s.occurrences, limit)
	return s.out
}

// indentRangesWithin is RangesCollector.toIndentRanges: over the limit it
// keeps the least indented ranges, so outer blocks stay foldable. It reverses
// the bottom-up collection into start order. VS Code re-reads the start line's
// indentation here, which is the indent each range was collected with.
func indentRangesWithin(out, collected []FoldRange, indents, occurrences []int, limit int) []FoldRange {
	if len(collected) <= limit {
		for i := len(collected) - 1; i >= 0; i-- {
			out = append(out, collected[i])
		}
		return out
	}
	entries, maxIndent := 0, len(occurrences)
	for i, n := range occurrences {
		if n > 0 {
			if n+entries > limit {
				maxIndent = i
				break
			}
			entries += n
		}
	}
	for i := len(collected) - 1; i >= 0 && len(out) < limit; i-- {
		indent := indents[i]
		if indent < maxIndent || indent == maxIndent && entries < limit {
			if indent == maxIndent {
				entries++
			}
			out = append(out, collected[i])
		}
	}
	return out
}

// foldUnknown marks a cached line whose facts must be read again.
const foldUnknown = -3

// foldLine is one line's cached facts for the fold scan.
type foldLine struct {
	indent int32
	marker int8
}

// foldLines caches line facts per view. Edits splice the cache through the
// buffer's line-change journal and mark only the touched lines unknown, so a
// keystroke reads one line instead of the whole document. A tab width or
// language change, a replacement or a gap in the journal rebuilds it.
type foldLines struct {
	buffer   *PieceTable
	revision uint64
	tabSize  int
	facts    *languageFacts
	lines    []foldLine
	scan     indentFoldScan
}

func (c *foldLines) sync(e *Editor, tabSize int, facts *languageFacts) {
	revision, count := e.Revision(), e.LineCount()
	journal := &e.buf.lineChanges
	valid := c.buffer == e.buf && c.tabSize == tabSize && c.facts == facts &&
		revision >= c.revision && revision-c.revision <= uint64(len(journal))
	for next := c.revision + 1; valid && next <= revision; next++ {
		change := journal[next%uint64(len(journal))]
		oldEnd, newEnd := change.end+1, change.end+1+change.delta
		if change.revision != next || change.start < 0 || change.start > oldEnd || oldEnd > len(c.lines) || newEnd < change.start {
			valid = false
			break
		}
		if grow := newEnd - oldEnd; grow > 0 {
			c.lines = append(c.lines, make([]foldLine, grow)...)
			copy(c.lines[newEnd:], c.lines[oldEnd:len(c.lines)-grow])
		} else if grow < 0 {
			c.lines = append(c.lines[:newEnd], c.lines[oldEnd:]...)
		}
		for i := change.start; i < newEnd; i++ {
			c.lines[i] = foldLine{indent: foldUnknown}
		}
	}
	if !valid || len(c.lines) != count {
		c.lines = slices.Grow(c.lines[:0], count)[:count]
		for i := range c.lines {
			c.lines[i] = foldLine{indent: foldUnknown}
		}
	}
	c.buffer, c.revision, c.tabSize, c.facts = e.buf, revision, tabSize, facts
}

// line reports cached facts, reading a line again only when it is unknown.
func (c *foldLines) line(e *Editor, l int) (indent, marker int) {
	cached := &c.lines[l]
	if cached.indent == foldUnknown {
		raw, _ := e.buf.lineView(l)
		indent, marker := foldLineFacts(raw, c.tabSize, c.facts.markers)
		*cached = foldLine{indent: int32(indent), marker: int8(marker)}
	}
	return int(cached.indent), int(cached.marker)
}

// languageFacts are the catalog facts the editor reads for one document
// path: offSide for indent guides and folds, and the combined region marker
// pattern. A new path makes a new value, so pointer identity is a cache key.
type languageFacts struct {
	path    string
	offSide bool
	markers *regexp.Regexp
}

func (e *Editor) languageFacts() *languageFacts {
	path := e.filePath
	if path == "" {
		path = e.title
	}
	if e.language == nil || e.language.path != path {
		facts := &languageFacts{path: path}
		if language, ok := catalog.Current().LanguageForPath(path, nil); ok {
			facts.offSide = language.OffSide
			if m := language.FoldingMarkers; m != nil {
				// VS Code: new RegExp(`(${start})|(?:${end})`). The catalog
				// validates each pattern, so the combination compiles.
				facts.markers, _ = regexp.Compile("(" + m.Start + ")|(?:" + m.End + ")")
			}
		}
		e.language = facts
	}
	return e.language
}
