package editor

import (
	"crude/text/indent"
	"crude/tui/bidi"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/types"
	"sort"
	"strings"
)

// SetPresentation consumes the existing display preferences without changing
// canonical bytes, source analysis, selection, snippets, dirty state or history.
// Linked views share the setting but retain independent paint caches.
func (e *Editor) SetPresentation(settings types.EditorSettings) {
	settings = types.NormalizeEditorSettings(settings)
	policy := indentation.Choices{Style: settings.IndentStyle, Size: settings.IndentSize, TabWidth: settings.TabSize}
	if e.buf.indentSettings != policy {
		e.buf.indentSettings = policy
		e.applyIndentationWidth()
	}
	e.eachView(func(view *Editor) {
		view.SetLineNumbers(settings.LineNumbers)
		view.setWrapping(settings.WordWrap)
		if view.syntaxColors != settings.SyntaxTheme {
			view.syntaxColors = settings.SyntaxTheme
			view.refreshSyntaxPalette()
		}
		view.renderWhitespace = settings.RenderWhitespace
		view.indentGuides = settings.IndentGuides
		view.setMinimap(settings.Minimap)
		direction := bidi.LeftToRight
		if settings.TextDirectionOrDefault() == types.EditorTextDirectionByLine {
			direction = bidi.Auto
		}
		view.SetTextDirection(direction)
	})
}

func (e *Editor) refreshSyntaxPalette() {
	e.chromaStyle = rendering.SourceStyle(theme.SyntaxPalette(e.th, e.syntaxColors))
	clear(e.chromaLineCache)
	clear(e.chromaLineScratch)
}

type whitespaceGeometry struct {
	leading, trailing int
	complete          bool
	tabs              [][2]int
}

func (e *Editor) whitespaceInfo(line int) *whitespaceGeometry {
	markers := e.renderWhitespace == "all" || e.renderWhitespace == "boundary"
	if !markers && !e.indentGuides {
		return nil
	}
	rawLine, err := e.buf.lineView(line)
	if err != nil {
		return nil
	}
	cached := e.lineGeomCache[line]
	if cached == nil || cached.raw != string(rawLine) {
		e.lineGeomInfo(line)
		cached = e.lineGeomCache[line]
	}
	if cached == nil {
		return nil
	}
	return cached.spaceGeometry(e.TabWidth(), markers)
}

// spaceGeometry supplies source tab ranges to both whitespace painting and
// bidi segmentation, independent of whether markers are visible.
func (cached *lineGeomCache) spaceGeometry(tabW int, markers bool) *whitespaceGeometry {
	if cached.whitespace != nil && (cached.whitespace.complete || !markers) {
		return cached.whitespace
	}
	raw := cached.raw
	leading := len(raw) - len(strings.TrimLeft(raw, " \t"))
	info := &whitespaceGeometry{leading: ByteOffsetToVisualCol(raw, leading, tabW), complete: markers}
	if !markers {
		cached.whitespace = info
		return info
	}
	trailing := len(strings.TrimRight(raw, " \t"))
	info.trailing = ByteOffsetToVisualCol(raw, trailing, tabW)
	for offset, col := 0, 0; offset < len(raw); {
		n, w := firstSourceUnit(tabW, raw[offset:], col)
		if n == 0 {
			break
		}
		if raw[offset] == '\t' {
			info.tabs = append(info.tabs, [2]int{col, col + w})
		}
		offset, col = offset+n, col+w
	}
	cached.whitespace = info
	return info
}
func (g *whitespaceGeometry) marker(col int, boundary bool) string {
	if g == nil || boundary && col >= g.leading && col < g.trailing {
		return ""
	}
	index := sort.Search(len(g.tabs), func(i int) bool { return g.tabs[i][1] > col })
	if index < len(g.tabs) && col >= g.tabs[index][0] {
		if col == g.tabs[index][0] {
			return "→"
		}
		return ""
	}
	return "·"
}
