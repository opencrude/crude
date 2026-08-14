package editor

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

func (e *Editor) Render(surf *surface.Surface, ctx ui.PaintContext) {
	if !e.open {
		return
	}
	w, h := surf.Width(), surf.Height()
	e.viewW = w
	e.viewH = h
	if w < 10 || h < 4 {
		return
	}

	bgColor := e.cellColor(e.th.Colors.Bg)
	selBgColor := e.cellColor(e.th.Colors.Selection)
	blankCell := cell.NewCell(' ', bgColor, bgColor, 0)
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
	e.syncScrollChrome(bodyH)

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
	sbX := sw + textW
	e.ensureChromaColors(e.scrollY, e.scrollY+bodyH)
	for y := 0; y < bodyH; y++ {
		bufY := y
		if bufY >= h {
			break
		}

		lineIdx := e.scrollY + y
		runes, lineVisW, widths, colorMap := e.lineRenderInfo(lineIdx, e.scrollX)
		diags := e.lineDiagnostics(lineIdx)

		ri := 0
		visCol := 0
		for ri < len(runes) && visCol+widths[ri] <= e.scrollX {
			visCol += widths[ri]
			ri++
		}

		numW := sw - 3 // git marker + diagnostic icon + rule
		numStr := e.formatLineNum(lineIdx+1+e.lineNumberOffset, numW)

		// Determine highest severity diagnostic on this line for the gutter icon.
		lineSev := 0
		for _, d := range diags {
			if d.severity > lineSev {
				lineSev = d.severity
			}
		}
		gitKind := e.gitKindAt(lineIdx)
		diagCol := sw - 3
		gitCol := sw - 2
		ruleCol := sw - 1

		for x := 0; x < sbX; x++ {
			if x < diagCol {
				if x < len(numStr) {
					buf.Set(x, bufY, cell.NewCell(rune(numStr[x]), e.cellColor(e.th.Colors.Muted), e.cellColor(e.th.Colors.Bg), 0))
				} else {
					buf.Set(x, bufY, blankCell)
				}
			} else if x == diagCol {
				// Diagnostic icon column.
				if lineSev > 0 {
					icon, ok := diagIcons[lineSev]
					if !ok {
						icon = 'ℹ'
					}
					iconColor := diagIconColors[lineSev]
					buf.Set(x, bufY, cell.NewCell(icon, iconColor, bgColor, 0))
				} else {
					buf.Set(x, bufY, blankCell)
				}
			} else if x == gitCol {
				ch, fg := e.gitGutterCell(gitKind)
				buf.Set(x, bufY, cell.NewCell(ch, fg, bgColor, 0))
			} else if x == ruleCol {
				borderCh := '│'
				if e.tokens != nil {
					if g := e.tokens.Glyphs().BorderV; g != "" {
						borderCh = []rune(g)[0]
					}
				}
				buf.Set(x, bufY, cell.NewCell(borderCh, e.cellColor(e.th.Colors.Border), bgColor, 0))
			} else {
				cx := x
				vCol := e.scrollX + (cx - sw)
				focused := ctx.Focused
				isCursorLine := lineIdx == e.cursor.Line

				for ri < len(runes) && vCol >= visCol+widths[ri] {
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

				if ri < len(runes) && vCol < visCol+widths[ri] && vCol < lineVisW {
					if vCol == visCol {
						switch {
						case focused && isCursorLine && vCol == e.cursor.Col:
							buf.Set(cx, bufY, cell.NewCell(runes[ri], e.cellColor(e.th.Colors.Bg), e.cellColor(e.th.Colors.Accent), cell.AttrBold))
						case e.isSelected(lineIdx, vCol):
							buf.Set(cx, bufY, cell.NewCell(runes[ri], e.cellColor(e.th.Colors.Fg), selBgColor, 0))
						case searchMatchIdx >= 0:
							hlBg := e.searchHighlightBg(searchIsCurrent)
							fg := e.cellColor(e.th.Colors.Fg)
							if vCol < len(colorMap) && colorMap[vCol] != "" {
								fg = e.cellColor(lipgloss.Color(colorMap[vCol]))
							}
							if sev > 0 {
								fg = diagFg
							}
							attr := diagAttr
							if searchIsCurrent {
								attr |= cell.AttrReverse
							}
							buf.Set(cx, bufY, cell.NewCell(runes[ri], fg, hlBg, attr))
						default:
							fg := e.cellColor(e.th.Colors.Fg)
							if vCol < len(colorMap) && colorMap[vCol] != "" {
								fg = e.cellColor(lipgloss.Color(colorMap[vCol]))
							}
							if sev > 0 {
								fg = diagFg
							}
							buf.Set(cx, bufY, cell.NewCell(runes[ri], fg, bgColor, diagAttr))
						}
					} else {
						switch {
						case focused && isCursorLine && vCol == e.cursor.Col:
							buf.Set(cx, bufY, cell.NewCell(' ', e.cellColor(e.th.Colors.Bg), e.cellColor(e.th.Colors.Accent), cell.AttrBold))
						case e.isSelected(lineIdx, vCol):
							buf.Set(cx, bufY, cell.NewCell(' ', e.cellColor(e.th.Colors.Fg), selBgColor, 0))
						case searchMatchIdx >= 0:
							hlBg := e.searchHighlightBg(searchIsCurrent)
							fg := e.cellColor(e.th.Colors.Fg)
							if sev > 0 {
								fg = diagFg
							}
							attr := diagAttr
							if searchIsCurrent {
								attr |= cell.AttrReverse
							}
							buf.Set(cx, bufY, cell.NewCell(' ', fg, hlBg, attr))
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
					case focused && isCursorLine && vCol == e.cursor.Col:
						buf.Set(cx, bufY, cell.NewCell(' ', e.cellColor(e.th.Colors.Bg), e.cellColor(e.th.Colors.Accent), cell.AttrBold))
					case e.isSelected(lineIdx, vCol):
						buf.Set(cx, bufY, cell.NewCell(' ', e.cellColor(e.th.Colors.Fg), selBgColor, 0))
					case searchMatchIdx >= 0:
						hlBg := e.searchHighlightBg(searchIsCurrent)
						buf.Set(cx, bufY, cell.NewCell(' ', e.cellColor(e.th.Colors.Muted), hlBg, 0))
					default:
						buf.Set(cx, bufY, blankCell)
					}
				}
			}
		}
	}

	e.paintScrollbar(buf, sbX, bodyH)

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
	lineCount := e.buf.LineCount() + e.lineNumberOffset
	digits := 1
	for n := lineCount; n >= 10; n /= 10 {
		digits++
	}
	if digits < 2 {
		digits = 2
	}
	// The gutter is: line number (digits) + diagnostic icon (1) +
	// git line-edit marker (1) + column guide (1).
	return digits + 3
}

// GutterWidth is the painted left chrome width in columns.
func (e *Editor) GutterWidth() int { return e.gutterWidth() }

func (e *Editor) formatLineNum(n int, width int) string {
	s := e.itoa(n)
	if len(s) > width {
		s = s[len(s)-width:]
	}
	return s
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
	if e.modified {
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
	raw := string(b)
	if e.lineGeomCache != nil {
		if cached := e.lineGeomCache[lineIdx]; cached != nil && cached.raw == raw {
			return cached.runes, cached.totalW, cached.widths
		}
	}
	runes, widths, totalW = lineDisplayGeometry(raw, tabWidth)
	if e.lineGeomCache == nil {
		e.lineGeomCache = make(map[int]*lineGeomCache, 128)
	}
	if len(e.lineGeomCache) > 512 {
		clear(e.lineGeomCache)
	}
	e.lineGeomCache[lineIdx] = &lineGeomCache{
		raw:    raw,
		runes:  runes,
		widths: widths,
		totalW: totalW,
	}
	return runes, totalW, widths
}

func (e *Editor) lineRenderInfo(lineIdx int, scrollX int) ([]rune, int, []int, []string) {
	runes, totalW, widths := e.lineGeomInfo(lineIdx)
	if runes == nil && totalW == 0 && widths == nil && lineIdx >= 0 {
		if _, err := e.buf.Line(lineIdx); err != nil {
			return nil, -1, nil, nil
		}
	}
	_ = scrollX // geometry is scroll-independent; kept for call-site compat
	colorMap := e.lineColorMap(lineIdx)
	return runes, totalW, widths, colorMap
}

func (e *Editor) cellColor(c color.Color) cell.Color {
	return rendering.ThemeColorToCell(c)
}

// searchHighlightAt returns the match index covering (line, col) or
// -1 if no match covers that position. The second return value is
// true when the match is the current selection in the search state.
//
// Matches that span multiple lines have a Start and End with
// different line numbers; the highlight extends from Start to
// EndCol on Start.Line, and from Col=0 to EndCol on End.Line.
func (e *Editor) searchHighlightAt(line, col int) (int, bool) {
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

func (e *Editor) lineRunes(lineIdx int) []rune {
	runes, _, _ := e.lineGeomInfo(lineIdx)
	return runes
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
