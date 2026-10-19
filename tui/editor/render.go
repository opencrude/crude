package editor

import (
	"image/color"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

func (e *Editor) Render(surf *surface.Surface, ctx ui.PaintContext) {
	if !e.open {
		return
	}
	if e.search != nil && e.search.Open {
		e.ensureSearchCurrent()
	}
	w, h := surf.Width(), surf.Height()
	if w != e.viewW || h != e.viewH {
		e.CancelPointerCapture()
	}
	if (w != e.viewW || h != e.viewH) && e.cursor.Line >= e.scrollY && e.cursor.Line < e.scrollY+e.bodyHeight() {
		e.pendingReveal = true
	}
	e.viewW = w
	e.viewH = h
	if w < 10 || h < 4 {
		return
	}

	bgColor := e.cellColor(e.th.Colors.Bg)
	selFgColor, selBgColor := e.tokens.TextSelectionColors(e.cellColor(e.th.Colors.Fg))
	blankCell := cell.NewCell(' ', bgColor, bgColor, 0)
	gutterBg := bgColor
	if e.th.Colors.Gutter != nil {
		gutterBg = e.cellColor(e.th.Colors.Gutter)
	}
	gutterBlank := cell.NewCell(' ', gutterBg, gutterBg, 0)
	sw := e.gutterWidth()
	sbW := e.scrollbarColWidth()
	textW := w - sw - sbW
	if textW < 1 {
		textW = 1
	}
	bodyH := h
	if e.statusChrome {
		bodyH = h - 1
	}
	if bodyH < 1 {
		bodyH = 1
	}
	if e.syncHorizontalScrollChrome(textW, bodyH) && bodyH > 1 {
		bodyH--
		e.syncHorizontalScrollChrome(textW, bodyH)
	}
	e.syncScrollChrome(bodyH)
	if e.pendingReveal {
		e.revealJump()
	}

	errColor := e.cellColor(e.th.Colors.Error)
	warnColor := e.cellColor(e.th.Colors.Warning)
	infoColor := e.cellColor(e.th.Colors.Muted)

	// Diagnostic gutter icon characters.
	diagIcons := map[int]rune{
		DiagError:   '✕',
		DiagWarning: '⚠',
		DiagInfo:    'ℹ',
		DiagHint:    'ℹ',
	}
	diagIconColors := map[int]cell.Color{
		DiagError:   errColor,
		DiagWarning: warnColor,
		DiagInfo:    infoColor,
		DiagHint:    infoColor,
	}
	diagAttrs := map[int]cell.Attrs{
		DiagError:   cell.AttrUnderline | cell.AttrUndercurl,
		DiagWarning: cell.AttrUnderline | cell.AttrUnderlineDotted,
		DiagInfo:    cell.AttrUnderline,
		DiagHint:    cell.AttrUnderline,
	}

	buf := surf.Buffer()
	// Every cell is the editor's: rows the body loop does not reach (the
	// horizontal scrollbar row, its corner) must never show what the editor
	// is mounted on, such as a window's popup fill.
	buf.Fill(blankCell)
	sbX := sw + textW
	e.remapDiagnosticPaint()
	e.ensureChromaColors(e.scrollY, e.scrollY+bodyH)
	for y := 0; y < bodyH; y++ {
		bufY := y
		if bufY >= h {
			break
		}

		lineIdx := e.scrollY + y
		clusters, lineVisW, widths, colorMap := e.lineRenderInfo(lineIdx, e.scrollX)
		diags := e.lineDiagnostics(lineIdx)

		ri := 0
		visCol := 0
		for ri < len(clusters) && visCol+widths[ri] <= e.scrollX {
			visCol += widths[ri]
			ri++
		}

		gutter := e.gutterGeometry()
		lineExists := lineIdx >= 0 && lineIdx < e.buf.LineCount()
		numStr := ""
		if lineExists {
			numStr = e.formatLineNum(lineIdx+1+e.lineNumberOffset, gutter.numberW)
		}

		// Determine highest severity diagnostic on this line for the gutter icon.
		lineSev := 0
		for _, d := range diags {
			if d.severity > lineSev {
				lineSev = d.severity
			}
		}
		gitKind := e.gitKindAt(lineIdx)

		for x := 0; x < sbX; x++ {
			if x == gutter.diagX {
				// Diagnostic icon column.
				if lineSev > 0 {
					icon, ok := diagIcons[lineSev]
					if !ok {
						icon = 'ℹ'
					}
					iconColor := diagIconColors[lineSev]
					cluster := string(icon)
					buf.Set(x, bufY, cell.NewClusterCell(cluster, max(1, rendering.VisibleWidth(cluster)), iconColor, gutterBg, 0))
				} else {
					buf.Set(x, bufY, gutterBlank)
				}
			} else if x >= gutter.numberX && x < gutter.numberX+gutter.numberW {
				index := x - gutter.numberX
				if index < len(numStr) {
					fg := e.cellColor(e.th.Colors.Muted)
					attrs := cell.Attrs(0)
					if lineIdx == e.cursor.Line {
						fg = e.cellColor(e.th.Colors.Fg)
						attrs = cell.AttrBold
					}
					buf.Set(x, bufY, cell.NewCell(rune(numStr[index]), fg, gutterBg, attrs))
				} else {
					buf.Set(x, bufY, gutterBlank)
				}
			} else if x == gutter.railX {
				ch, fg := e.gitGutterCell(gitKind)
				buf.Set(x, bufY, cell.NewCell(ch, fg, gutterBg, 0))
			} else if x == gutter.gapX {
				marker := e.gitDeletionMarkerAt(lineIdx, e.buf.LineCount())
				fg := gutterBg
				if marker != ' ' {
					color := e.th.Tree.Colors.GitDeleted
					if e.gitConflicted {
						color = e.th.Tree.Colors.GitConflicted
					}
					fg = e.cellColor(color)
				}
				buf.Set(x, bufY, cell.NewCell(marker, fg, gutterBg, 0))
			} else if x < sw {
				buf.Set(x, bufY, gutterBlank)
			} else {
				cx := x
				vCol := e.scrollX + (cx - sw)

				for ri < len(clusters) && vCol >= visCol+widths[ri] {
					visCol += widths[ri]
					ri++
				}

				sev := diagSeverityAtCol(diags, vCol)
				diagAttr := cell.Attrs(0)
				var diagFg cell.Color
				if sev > 0 {
					diagAttr = diagAttrs[sev]
					switch sev {
					case DiagError:
						diagFg = errColor
					case DiagWarning:
						diagFg = warnColor
					default:
						diagFg = infoColor
					}
				}

				// Determine search highlight for this cell. The
				// current match is rendered with a stronger
				// background; other matches get a softer tint.
				searchMatchIdx, searchIsCurrent := e.searchHighlightAt(lineIdx, vCol)
				if ri < len(clusters) && vCol < visCol+widths[ri] && vCol < lineVisW {
					if vCol == visCol {
						switch {
						case e.isSelected(lineIdx, vCol):
							fg := e.cellColor(e.th.Colors.Fg)
							if vCol < len(colorMap) && colorMap[vCol] != "" {
								fg = rendering.LipglossColorToCell(colorMap[vCol])
							}
							fg, bg := e.tokens.TextSelectionColors(fg)
							e.paintGrapheme(buf, cx, bufY, sbX, clusters[ri], widths[ri], fg, bg, 0, blankCell)
						case searchMatchIdx >= 0:
							hlBg := e.searchHighlightBg(searchIsCurrent)
							fg := e.cellColor(e.th.Colors.Fg)
							if vCol < len(colorMap) && colorMap[vCol] != "" {
								fg = rendering.LipglossColorToCell(colorMap[vCol])
							}
							if sev > 0 {
								fg = diagFg
							}
							if cell.ContrastRatio(fg, hlBg) < 4.5 {
								fg = cell.ContrastInk(hlBg)
							}
							e.paintGrapheme(buf, cx, bufY, sbX, clusters[ri], widths[ri], fg, hlBg, diagAttr, blankCell)
						default:
							fg := e.cellColor(e.th.Colors.Fg)
							if vCol < len(colorMap) && colorMap[vCol] != "" {
								fg = rendering.LipglossColorToCell(colorMap[vCol])
							}
							if sev > 0 {
								fg = diagFg
							}
							e.paintGrapheme(buf, cx, bufY, sbX, clusters[ri], widths[ri], fg, bgColor, diagAttr, blankCell)
						}
					} else if visCol < e.scrollX {
						switch {
						case e.isSelected(lineIdx, vCol):
							buf.Set(cx, bufY, cell.NewCell(' ', selFgColor, selBgColor, 0))
						case searchMatchIdx >= 0:
							hlBg := e.searchHighlightBg(searchIsCurrent)
							fg := e.cellColor(e.th.Colors.Fg)
							if sev > 0 {
								fg = diagFg
							}
							if cell.ContrastRatio(fg, hlBg) < 4.5 {
								fg = cell.ContrastInk(hlBg)
							}
							buf.Set(cx, bufY, cell.NewCell(' ', fg, hlBg, diagAttr))
						default:
							fg := bgColor
							if sev > 0 {
								fg = diagFg
							}
							buf.Set(cx, bufY, cell.NewCell(' ', fg, bgColor, diagAttr))
						}
					}
				} else {
					switch {
					case e.isSelected(lineIdx, vCol):
						buf.Set(cx, bufY, cell.NewCell(' ', selFgColor, selBgColor, 0))
					case searchMatchIdx >= 0:
						hlBg := e.searchHighlightBg(searchIsCurrent)
						buf.Set(cx, bufY, cell.NewCell(' ', cell.ContrastInk(hlBg), hlBg, 0))
					default:
						buf.Set(cx, bufY, blankCell)
					}
				}
			}
		}
	}
	if ctx.Focused && !e.readOnly && e.cursorBlink.Visible(ctx.Frame) {
		cursorX := sw + e.cursor.Col - e.scrollX
		cursorY := e.cursor.Line - e.scrollY
		if cursorX >= sw && cursorX < sbX && cursorY >= 0 && cursorY < bodyH {
			ui.PaintBlockCursor(buf, cursorX, cursorY, e.tokens.Color(ui.ColorCursor), bgColor)
		}
	}

	// The gutter runs the full body height, past the last line and beside
	// the horizontal scrollbar.
	gutterEnd := h
	if e.statusChrome {
		gutterEnd = h - 1
	}
	for y := bodyH; y < gutterEnd; y++ {
		for x := 0; x < sw; x++ {
			buf.Set(x, y, gutterBlank)
		}
	}
	e.paintScrollbar(buf, sbX, bodyH)
	e.paintHorizontalScrollbar(buf, sw, bodyH, textW)

	if e.statusChrome {
		statusY := h - 1
		if statusY >= 0 {
			statusStr := e.renderStatusLine(w)
			statusRunes := []rune(statusStr)
			for x := 0; x < w; x++ {
				if x < len(statusRunes) {
					buf.Set(x, statusY, cell.NewCell(statusRunes[x], e.cellColor(e.th.Colors.Muted), e.cellColor(e.th.Colors.Border), 0))
				} else {
					buf.Set(x, statusY, blankCell)
				}
			}
		}
	}
}

func diagSeverityAtCol(diags []diagSpan, col int) int {
	if len(diags) == 0 {
		return 0
	}
	for _, d := range diags {
		if col >= d.startCol && col < d.endCol {
			return d.severity
		}
	}
	return 0
}

func (e *Editor) gutterWidth() int {
	return e.gutterGeometry().width
}

type gutterGeometry struct {
	diagX   int
	numberX int
	numberW int
	railX   int
	gapX    int
	width   int
}

func (e *Editor) gutterGeometry() gutterGeometry {
	lineCount := e.buf.LineCount() + e.lineNumberOffset
	digits := 1
	for n := lineCount; n >= 10; n /= 10 {
		digits++
	}
	if digits < 2 {
		digits = 2
	}
	if e.hideLineNumbers {
		digits = 0
	}
	// Fixed lanes prevent source movement while diagnostics/Git arrive:
	// diagnostic, right-aligned number, change rail, deletion/source gap.
	diagWidth := max(rendering.VisibleWidth("✕"), rendering.VisibleWidth("⚠"), rendering.VisibleWidth("ℹ"))
	numberX := diagWidth + 1
	railX := numberX + digits + 1
	return gutterGeometry{diagX: 0, numberX: numberX, numberW: digits, railX: railX, gapX: railX + 1, width: railX + 2}
}

// GutterWidth is the painted left chrome width in columns.
func (e *Editor) GutterWidth() int { return e.gutterWidth() }

func (e *Editor) formatLineNum(n int, width int) string {
	s := e.itoa(n)
	if len(s) > width {
		s = s[len(s)-width:]
	}
	return strings.Repeat(" ", max(0, width-len(s))) + s
}

func (e *Editor) itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func (e *Editor) renderStatusLine(w int) string {
	lc := e.buf.LineCount()
	left := e.title
	if e.Modified() {
		left += " ●"
	}
	if errs, warns, hints := e.DiagnosticCounts(); errs > 0 || warns > 0 || hints > 0 {
		var parts []string
		if errs > 0 {
			parts = append(parts, "✕"+e.itoa(errs))
		}
		if warns > 0 {
			parts = append(parts, "⚠"+e.itoa(warns))
		}
		if hints > 0 {
			parts = append(parts, "ℹ"+e.itoa(hints))
		}
		left += "  " + strings.Join(parts, " ")
	}
	right := e.itoa(e.cursor.Line+1) + ":" + e.itoa(e.cursor.Col+1) + "  " + e.itoa(lc) + "L"
	padding := w - rendering.VisibleWidth(left) - rendering.VisibleWidth(right) - 2
	if padding < 1 {
		padding = 1
	}
	return " " + left + strings.Repeat(" ", padding) + right + " "
}

func (e *Editor) lineGeomInfo(lineIdx int) (runes []rune, totalW int, widths []int) {
	if lineIdx < 0 {
		return nil, 0, nil
	}
	b, err := e.buf.Line(lineIdx)
	if err != nil {
		return nil, 0, nil
	}
	if e.lineGeomCache != nil {
		// string(b) here is an allocation-free compile-time comparison, so the
		// cache hit (the per-frame steady path) allocates nothing at all; the
		// string is only materialized on a miss.
		if cached := e.lineGeomCache[lineIdx]; cached != nil && cached.raw == string(b) {
			return cached.runes, cached.totalW, cached.widths
		}
	}
	raw := string(b)
	clusters, runes, widths, totalW := lineDisplayGeometryWithClusters(raw, tabWidth)
	if e.lineGeomCache == nil {
		e.lineGeomCache = make(map[int]*lineGeomCache, 128)
	}
	if len(e.lineGeomCache) > 512 {
		clear(e.lineGeomCache)
	}
	e.lineGeomCache[lineIdx] = &lineGeomCache{
		raw:      raw,
		clusters: clusters,
		runes:    runes,
		widths:   widths,
		totalW:   totalW,
	}
	return runes, totalW, widths
}

func (e *Editor) lineRenderInfo(lineIdx int, scrollX int) ([]string, int, []int, []string) {
	runes, totalW, widths := e.lineGeomInfo(lineIdx)
	if runes == nil && totalW == 0 && widths == nil && lineIdx >= 0 {
		if _, err := e.buf.Line(lineIdx); err != nil {
			return nil, -1, nil, nil
		}
	}
	_ = scrollX // geometry is scroll-independent; kept for call-site compat
	colorMap := e.lineColorMap(lineIdx)
	// The grapheme clusters are already stored per line in lineGeomCache (the
	// caller treats the returned slice as read-only), so the per-frame steady
	// path returns the retained slice instead of rebuilding a copy per call.
	if cached := e.lineGeomCache[lineIdx]; cached != nil && len(cached.clusters) == len(runes) {
		return cached.clusters, totalW, widths, colorMap
	}
	clusters := make([]string, len(runes))
	for index, r := range runes {
		clusters[index] = string(r)
	}
	return clusters, totalW, widths, colorMap
}

func (e *Editor) cellColor(c color.Color) cell.Color {
	return rendering.ThemeColorToCell(c)
}

func (e *Editor) paintGrapheme(
	buf *surface.CellBuffer,
	x, y, textLimit int,
	cluster string,
	width int,
	fg, bg cell.Color,
	attr cell.Attrs,
	blank cell.Cell,
) {
	if width < 1 || x+width > textLimit {
		buf.Set(x, y, blank)
		return
	}
	buf.Set(x, y, cell.NewClusterCell(cluster, width, fg, bg, attr))
}

// searchHighlightAt returns the match index covering (line, col) or
// -1 if no match covers that position. The second return value is
// true when the match is the current selection in the search state.
//
// Matches that span multiple lines have a Start and End with
// different line numbers; the highlight extends from Start to
// EndCol on Start.Line, and from Col=0 to EndCol on End.Line.
func (e *Editor) searchHighlightAt(line, col int) (int, bool) {
	if e.landing != nil && e.landingRevision == e.Revision() && line == e.landing.Start.Line && col >= e.landing.Start.Col && col < e.landing.End.Col {
		return 0, true
	}
	if e.search == nil || !e.search.Open {
		return -1, false
	}
	matches := e.search.Matches
	current := e.search.CurrentIdx
	for i, m := range matches {
		if line < m.Start.Line || line > m.End.Line {
			continue
		}
		if line == m.Start.Line && line == m.End.Line {
			// Single-line match
			if col >= m.Start.Col && col < m.End.Col {
				return i, i == current
			}
			continue
		}
		if line == m.Start.Line {
			if col >= m.Start.Col {
				return i, i == current
			}
			continue
		}
		if line == m.End.Line {
			if col < m.End.Col {
				return i, i == current
			}
			continue
		}
		// Multi-line interior: all columns match.
		return i, i == current
	}
	return -1, false
}

// searchHighlightBg returns the background color used for search
// match highlights. The current match uses a saturated accent; other
// matches use a softer selection tint.
func (e *Editor) searchHighlightBg(isCurrent bool) cell.Color {
	if isCurrent {
		return e.cellColor(e.th.Colors.Accent2)
	}
	return e.cellColor(e.th.Colors.Selection)
}

func (e *Editor) lineVisualWidth(lineIdx int) int {
	_, width, _ := e.lineGeomInfo(lineIdx)
	return width
}

func (e *Editor) ensureCursorVisible() {
	sw := e.gutterWidth()
	sbW := e.scrollbarColWidth()
	availW := e.viewW - sw - sbW
	if availW < 1 {
		availW = 1
	}
	availH := e.bodyHeight()

	margin := scrollMargin
	if margin > availH/2 {
		margin = availH / 2
	}
	if margin < 1 {
		margin = 1
	}

	if e.cursor.Line < e.scrollY+margin {
		e.scrollY = e.cursor.Line - margin
	}
	if e.cursor.Line >= e.scrollY+availH-margin {
		e.scrollY = e.cursor.Line - availH + margin + 1
	}
	if e.scrollY < 0 {
		e.scrollY = 0
	}
	maxScroll := e.buf.LineCount() - availH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if e.scrollY > maxScroll {
		e.scrollY = maxScroll
	}

	hMargin := 5
	if hMargin > availW/2 {
		hMargin = availW / 2
	}
	if hMargin < 1 {
		hMargin = 1
	}

	if e.cursor.Col < e.scrollX+hMargin {
		e.scrollX = e.cursor.Col - hMargin
	}
	if e.cursor.Col >= e.scrollX+availW-hMargin {
		e.scrollX = e.cursor.Col - availW + hMargin + 1
	}
	if e.scrollX < 0 {
		e.scrollX = 0
	}

	e.syncScrollChrome(availH)
}
