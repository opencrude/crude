package editor

// Indentation guides port VS Code's guide levels:
// GuidesTextModelPart.getLinesIndentGuides, _getIndentLevelForWhitespaceLine
// and computeIndentLevel (microsoft/vscode, src/vs/editor/common/model/
// guidesTextModelPart.ts and utils.ts, MIT License, Copyright (c) Microsoft
// Corporation). A line with text has ceil(indent/indentSize) guides. A
// whitespace-only line takes its level from the nearest lines with text above
// and below; offSide languages (blocks end by dedent) come from catalog data.
// Guides are painted at visual columns 0, indentSize, ... below the level.

// indentGuideGlyph is the dim dashed rule painted at each guide column.
const indentGuideGlyph = "╎"

// computeIndentLevel is the leading visual width of a line, or -1 when the
// line has no text. Tabs advance to the next tab stop.
func computeIndentLevel(line []byte, tabSize int) int {
	indent := 0
	for _, b := range line {
		switch b {
		case ' ':
			indent++
		case '\t':
			indent = indent - indent%tabSize + tabSize
		default:
			return indent
		}
	}
	return -1
}

// guideLevels answers guide levels for increasing source lines during one
// paint. Like VS Code it remembers the nearest lines with text above and
// below, so a run of blank lines is scanned once per frame, not once per row.
// A skipped line range (a fold) restarts the scan above, as VS Code computes
// each visible segment separately.
type guideLevels struct {
	e                       *Editor
	tabSize, indentSize     int
	offSide                 bool
	next                    int
	aboveIndex, aboveIndent int
	belowIndex, belowIndent int
}

func (e *Editor) newGuideLevels(indentSize int) guideLevels {
	return guideLevels{
		e: e, tabSize: max(1, e.TabWidth()), indentSize: max(1, indentSize), offSide: e.offSideLanguage(),
		next: -1, aboveIndex: -2, aboveIndent: -1, belowIndex: -2, belowIndent: -1,
	}
}

func (g *guideLevels) indent(line int) int {
	raw, err := g.e.buf.lineView(line)
	if err != nil {
		return -1
	}
	return computeIndentLevel(raw, g.tabSize)
}

// level is the number of guides on line, or 0 outside the document.
func (g *guideLevels) level(line int) int {
	count := g.e.LineCount()
	if line < 0 || line >= count {
		return 0
	}
	if line != g.next {
		g.aboveIndex = -2
		if line < g.next {
			g.belowIndex = -2
		}
	}
	g.next = line + 1
	if current := g.indent(line); current >= 0 {
		g.aboveIndex, g.aboveIndent = line, current
		return (current + g.indentSize - 1) / g.indentSize
	}
	if g.aboveIndex == -2 {
		g.aboveIndex, g.aboveIndent = -1, -1
		for l := line - 1; l >= 0; l-- {
			if indent := g.indent(l); indent >= 0 {
				g.aboveIndex, g.aboveIndent = l, indent
				break
			}
		}
	}
	if g.belowIndex != -1 && (g.belowIndex == -2 || g.belowIndex < line) {
		g.belowIndex, g.belowIndent = -1, -1
		for l := line + 1; l < count; l++ {
			if indent := g.indent(l); indent >= 0 {
				g.belowIndex, g.belowIndent = l, indent
				break
			}
		}
	}
	return g.whitespaceLevel()
}

func (g *guideLevels) whitespaceLevel() int {
	above, below, size := g.aboveIndent, g.belowIndent, g.indentSize
	switch {
	case above == -1 || below == -1:
		return 0 // at the top or bottom of the file
	case above < below:
		return 1 + above/size // inside the region above
	case above == below:
		return (below + size - 1) / size // between two regions
	case g.offSide:
		return (below + size - 1) / size // same level as the region below
	default:
		return 1 + below/size // inside the region that ends below
	}
}

// offSideLanguage reads the catalog fact once per document path.
func (e *Editor) offSideLanguage() bool { return e.languageFacts().offSide }
