package ui

import "math"

// VerticalEighths are the block glyphs from an empty cell to a full one,
// filled from the bottom. Column charts draw each column's cells with them.
var VerticalEighths = [9]rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// horizontalEighths fill a cell from the left, one eighth to a full cell.
var horizontalEighths = [8]rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉', '█'}

// ShareBar returns the glyphs of a horizontal bar width cells long, filled to
// share (0 to 1) in eighths of a cell. A zero rune is an empty track cell that
// the caller paints in its own track style. Any share above zero shows at
// least one eighth, so a small part never looks absent.
func ShareBar(width int, share float64) []rune {
	out := make([]rune, max(0, width))
	share = math.Min(math.Max(share, 0), 1)
	fill := int(math.Round(share * float64(len(out)*8)))
	if share > 0 && fill == 0 {
		fill = 1
	}
	for i := range out {
		if n := min(max(fill-i*8, 0), 8); n > 0 {
			out[i] = horizontalEighths[n-1]
		}
	}
	return out
}

// FoldColumns sums values into width columns. Value i lands in column
// i*width/len(values), so the newest value is always in the last column.
func FoldColumns(values []float64, width int) []float64 {
	out := make([]float64, max(0, width))
	if len(out) == 0 {
		return out
	}
	for i, value := range values {
		out[i*len(out)/len(values)] += value
	}
	return out
}

// ColumnLevel is the height of value against peak in eighths of a cell, for a
// chart rows cells tall. A value above zero shows at least one eighth.
func ColumnLevel(value, peak float64, rows int) int {
	if peak <= 0 || value <= 0 {
		return 0
	}
	return min(rows*8, max(1, int(math.Round(value/peak*float64(rows*8)))))
}

// ColumnCell is the glyph of one cell of a column level eighths tall. Row
// counts cells from the bottom of the chart.
func ColumnCell(level, row int) rune {
	return VerticalEighths[min(max(level-row*8, 0), 8)]
}
