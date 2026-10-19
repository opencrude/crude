package diffview

import (
	"context"
	"crude/textsearch"
	"crude/tui/rendering"
	"iter"
	"sort"
	"strings"
	"unicode"
)

type sourceUnit struct {
	raw, display          string
	offset, column, width int
}

const sourceCheckpointBytes = 512
const indexedLineBytes = 4096

type sourcePoint struct{ offset, column int }
type sourceLineIndex struct {
	points []sourcePoint
	width  int
}

// SourceLines is immutable source geometry prepared off Update. Only long lines
// retain sparse grapheme-boundary checkpoints; ordinary lines keep no map.
// Looking up a far-right viewport scans at most one checkpoint interval rather
// than every hidden grapheme before it. No source bytes are copied or clipped.
type SourceLines struct {
	owner *sourceOwner
}

type sourceGeometry struct {
	indexes map[int]sourceLineIndex
	width   int
}

type sourceOwner struct {
	lines      []string
	geometry   sourceGeometry
	generation uint64
}

func PrepareSourceLinesContext(ctx context.Context, lines []string) (SourceLines, error) {
	source := SourceLines{owner: &sourceOwner{lines: lines}}
	for number, text := range lines {
		if err := ctx.Err(); err != nil {
			return SourceLines{}, err
		}
		var index sourceLineIndex
		nextCheck, nextPoint := 0, sourceCheckpointBytes
		for unit := range sourceRange(text, 0, int(^uint(0)>>1)) {
			if unit.offset >= nextCheck {
				if err := ctx.Err(); err != nil {
					return SourceLines{}, err
				}
				nextCheck = unit.offset + 4096
			}
			if len(text) >= indexedLineBytes && unit.offset >= nextPoint {
				index.points = append(index.points, sourcePoint{unit.offset, unit.column})
				nextPoint = unit.offset + sourceCheckpointBytes
			}
			index.width = unit.column + unit.width
		}
		source.owner.geometry.width = max(source.owner.geometry.width, index.width)
		if len(index.points) > 0 {
			if source.owner.geometry.indexes == nil {
				source.owner.geometry.indexes = make(map[int]sourceLineIndex)
			}
			source.owner.geometry.indexes[number+1] = index
		}
	}
	return source, ctx.Err()
}

func (source SourceLines) Width() int {
	if source.owner == nil {
		return 0
	}
	return source.owner.geometry.width
}

func (source SourceLines) LineWidth(number int) int {
	if source.owner == nil {
		return 0
	}
	if index, ok := source.owner.geometry.indexes[number]; ok {
		return index.width
	}
	if number <= 0 || number > len(source.owner.lines) {
		return 0
	}
	return sourceWidth(source.owner.lines[number-1])
}

func (source SourceLines) rangeUnits(number, from, to int) iter.Seq[sourceUnit] {
	if source.owner == nil || number <= 0 || number > len(source.owner.lines) {
		return func(func(sourceUnit) bool) {}
	}
	point := sourcePoint{}
	if index, ok := source.owner.geometry.indexes[number]; ok {
		i := sort.Search(len(index.points), func(i int) bool { return index.points[i].column > from }) - 1
		if i >= 0 {
			point = index.points[i]
		}
	}
	return sourceRangeAt(source.owner.lines[number-1], from, to, point)
}

func (source SourceLines) Range(number, from, to int) iter.Seq[SourceUnit] {
	return func(yield func(SourceUnit) bool) {
		for unit := range source.rangeUnits(number, from, to) {
			if !yield(SourceUnit{Raw: unit.raw, Display: unit.display, Offset: unit.offset, Column: unit.column, Width: unit.width}) {
				return
			}
		}
	}
}

// sourceRange streams only the requested display columns. Painting and pointer
// landing must not allocate a grapheme map for an entire minified source line.
func sourceRange(text string, from, to int) iter.Seq[sourceUnit] {
	return sourceRangeAt(text, from, to, sourcePoint{})
}

func sourceRangeAt(text string, from, to int, start sourcePoint) iter.Seq[sourceUnit] {
	return func(yield func(sourceUnit) bool) {
		text := text
		if strings.HasSuffix(text, "\r\n") {
			text = strings.TrimSuffix(text, "\r\n")
		} else {
			text = strings.TrimSuffix(text, "\n")
		}
		if start.offset >= len(text) {
			return
		}
		text = text[start.offset:]
		column, offset := start.column, start.offset
		for text != "" && column < to {
			cluster, width := rendering.FirstGrapheme(text)
			if cluster == "" {
				break
			}
			display := cluster
			if cluster == "\t" {
				width = 4 - column%4
				display = strings.Repeat(" ", width)
			} else {
				for _, r := range cluster {
					if unicode.IsControl(r) {
						display = "�"
						width = 1
						break
					}
				}
			}
			if column+width > from && !yield(sourceUnit{cluster, display, offset, column, width}) {
				return
			}
			column += width
			offset += len(cluster)
			text = text[len(cluster):]
		}
	}
}

func sourceWidth(text string) int {
	width, _ := SourceWidthContext(context.Background(), text)
	return width
}

func SourceWidthContext(ctx context.Context, text string) (int, error) {
	column := 0
	nextCheck := 0
	for unit := range sourceRange(text, 0, int(^uint(0)>>1)) {
		if unit.offset >= nextCheck {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			nextCheck = unit.offset + 4096
		}
		column = unit.column + unit.width
	}
	return column, ctx.Err()
}

// SourceMatches projects exact source-byte matches in one grapheme pass. Search
// never builds a per-character array or lowercases the source (which shifts
// Unicode byte offsets). Matches inside a grapheme are not selectable targets.
type SourceMatch struct{ Start, End int }

func sourceMatches(ctx context.Context, text string, matcher *textsearch.Matcher, limit int) ([]SourceMatch, error) {
	text = strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	hits := matcher.FindAll(text, limit)
	if len(hits) == 0 {
		return nil, nil
	}
	var out []SourceMatch
	index, start := 0, -1
	nextCheck := 0
	for unit := range sourceRange(text, 0, int(^uint(0)>>1)) {
		if unit.offset >= nextCheck {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			nextCheck = unit.offset + 4096
		}
		if index == len(hits) {
			break
		}
		for index < len(hits) && hits[index].Start < unit.offset && start < 0 {
			index++
		}
		if index == len(hits) {
			break
		}
		hit := hits[index]
		if hit.Start == unit.offset {
			start = unit.column
		}
		end := unit.offset + len(unit.raw)
		if start >= 0 && hit.End <= end {
			if hit.End == end {
				out = append(out, SourceMatch{Start: start, End: unit.column + unit.width})
			}
			index++
			start = -1
		}
	}
	return out, ctx.Err()
}
