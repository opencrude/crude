package editor

import (
	"crude/tui/rendering"
	"strings"
	"unicode/utf16"
)

// DefaultTabWidth is the editor tab-stop width when none is configured.
const DefaultTabWidth = 4

// TabStopAdvance returns how many columns a tab at visual column col
// advances to reach the next tab stop of width tabW.
func TabStopAdvance(col, tabW int) int {
	if tabW < 1 {
		tabW = DefaultTabWidth
	}
	if col < 0 {
		col = 0
	}
	adv := tabW - (col % tabW)
	if adv <= 0 {
		adv = tabW
	}
	return adv
}

// ExpandTabs expands tabs in a single line to spaces using tab stops.
// Non-tab text is measured with the repository grapheme-width path.
func ExpandTabs(line string, tabW int) string {
	if tabW < 1 {
		tabW = DefaultTabWidth
	}
	return rendering.ExpandTabs(line, tabW)
}

// LineVisualWidth returns the visual column width of a raw (unexpanded) line.
func LineVisualWidth[T string | []byte](line T, tabW int) int {
	if tabW < 1 {
		tabW = DefaultTabWidth
	}
	col := 0
	rest := line
	for len(rest) > 0 {
		if rest[0] == '\t' {
			col += TabStopAdvance(col, tabW)
			rest = rest[1:]
			continue
		}
		cluster, w := rendering.FirstGrapheme(rest)
		if len(cluster) == 0 {
			break
		}
		col += w
		rest = rest[len(cluster):]
	}
	return col
}

// visualColumnWalker projects increasing byte offsets along one source line.
// It uses the same tab and grapheme rules as ordinary editor coordinates.
type visualColumnWalker[T string | []byte] struct {
	line                     T
	tabWidth, offset, column int
}

func (w *visualColumnWalker[T]) advance(byteOff int) int {
	byteOff = max(0, min(byteOff, len(w.line)))
	for w.offset < byteOff {
		if w.line[w.offset] == '\t' {
			w.column += TabStopAdvance(w.column, w.tabWidth)
			w.offset++
			continue
		}
		cluster, width := rendering.FirstGrapheme(w.line[w.offset:])
		if len(cluster) == 0 || w.offset+len(cluster) > byteOff {
			// An offset inside a cluster projects to its first cell.
			break
		}
		w.column += width
		w.offset += len(cluster)
	}
	return w.column
}

// ByteOffsetToVisualCol converts a byte offset within a single line (no
// newlines) to a visual column, honouring tab stops and grapheme widths.
func ByteOffsetToVisualCol[T string | []byte](line T, byteOff, tabW int) int {
	w := visualColumnWalker[T]{line: line, tabWidth: tabW}
	return w.advance(byteOff)
}

// VisualColToByteOffset converts a visual column to a byte offset within
// a single line. If visualCol lands inside a wide cluster or tab span,
// the offset of the start of that unit is returned.
func VisualColToByteOffset[T string | []byte](line T, visualCol, tabW int) int {
	if tabW < 1 {
		tabW = DefaultTabWidth
	}
	if visualCol <= 0 {
		return 0
	}
	col := 0
	i := 0
	for i < len(line) {
		if line[i] == '\t' {
			adv := TabStopAdvance(col, tabW)
			if col+adv > visualCol {
				return i
			}
			col += adv
			i++
			continue
		}
		cluster, w := rendering.FirstGrapheme(line[i:])
		if len(cluster) == 0 {
			break
		}
		if col+w > visualCol {
			return i
		}
		col += w
		i += len(cluster)
		if col == visualCol {
			return i
		}
	}
	return i
}

// ByteOffsetToLineCol converts a buffer-wide byte offset into a 0-based
// line index and visual column within that line.
func ByteOffsetToLineCol(text string, off, tabW int) (line, col int) {
	if tabW < 1 {
		tabW = DefaultTabWidth
	}
	if off < 0 {
		off = 0
	}
	if off > len(text) {
		off = len(text)
	}
	lineStart := 0
	for i := 0; i < off; i++ {
		if text[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	lineEnd := len(text)
	if next := strings.IndexByte(text[lineStart:], '\n'); next >= 0 {
		lineEnd = lineStart + next
		if lineEnd > lineStart && text[lineEnd-1] == '\r' {
			lineEnd--
		}
	}
	return line, ByteOffsetToVisualCol(text[lineStart:lineEnd], off-lineStart, tabW)
}

// UTF16ColToVisualCol converts an LSP UTF-16 code-unit offset from the
// start of a line into a visual column (tab-expanded, grapheme-aware).
func UTF16ColToVisualCol(line string, utf16Col, tabW int) int {
	if tabW < 1 {
		tabW = DefaultTabWidth
	}
	if utf16Col <= 0 {
		return 0
	}
	visCol := 0
	units := 0
	rest := line
	for len(rest) > 0 && units < utf16Col {
		if rest[0] == '\t' {
			if units+1 > utf16Col {
				break
			}
			visCol += TabStopAdvance(visCol, tabW)
			units++
			rest = rest[1:]
			continue
		}
		cluster, w := rendering.FirstGrapheme(rest)
		if len(cluster) == 0 {
			break
		}
		// Count UTF-16 code units in the cluster.
		cu := 0
		for _, r := range cluster {
			cu += utf16.RuneLen(r)
			if cu <= 0 {
				cu = 1
			}
		}
		if units+cu > utf16Col {
			break
		}
		visCol += w
		units += cu
		rest = rest[len(cluster):]
	}
	return visCol
}

// lineDisplayGeometry expands tabs and collapses grapheme clusters into
// one display cell each (first rune + grapheme width). This is the
// canonical geometry used by rendering and cursor motion.
func lineDisplayGeometry(rawLine string, tabW int) (runes []rune, widths []int, totalW int) {
	_, runes, widths, totalW = lineDisplayGeometryWithClusters(rawLine, tabW)
	return runes, widths, totalW
}

func lineDisplayGeometryWithClusters(rawLine string, tabW int) (clusters []string, runes []rune, widths []int, totalW int) {
	expanded := ExpandTabs(rawLine, tabW)
	rest := expanded
	for len(rest) > 0 {
		cluster, w := rendering.FirstGrapheme(rest)
		if len(cluster) == 0 {
			break
		}
		rs := []rune(cluster)
		if len(rs) == 0 {
			break
		}
		clusters = append(clusters, cluster)
		runes = append(runes, rs[0])
		widths = append(widths, w)
		totalW += w
		rest = rest[len(cluster):]
	}
	return clusters, runes, widths, totalW
}
