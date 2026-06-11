package editor

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/surface"
	"crude/tui/widget"
)

func (e *Editor) Measure(availW, availH int) (int, int) {
	return availW, availH
}

func (e *Editor) Render(surf *surface.Surface, ctx *widget.RenderContext) {
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
	textW := w - sw
	if textW < 1 {
		textW = 1
	}
	bodyH := h - 1

	errColor := e.cellColor(e.th.Colors.Error)
	warnColor := e.cellColor(e.th.Colors.Warning)
	infoColor := e.cellColor(e.th.Colors.Muted)

	buf := surf.Buffer()
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

		numW := sw - 1
		numStr := e.formatLineNum(lineIdx+1, numW)

		for x := 0; x < w; x++ {
			if x < sw-1 {
				if x < len(numStr) {
					buf.Set(x, bufY, cell.NewCell(rune(numStr[x]), e.cellColor(e.th.Colors.Muted), e.cellColor(e.th.Colors.Bg), 0))
				} else {
					buf.Set(x, bufY, blankCell)
				}
			} else if x == sw-1 {
				buf.Set(x, bufY, cell.NewCell('│', e.cellColor(e.th.Colors.Border), bgColor, 0))
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
					diagAttr = cell.AttrUnderline
					switch sev {
					case DiagError:
						diagFg = errColor
					case DiagWarning:
						diagFg = warnColor
					default:
						diagFg = infoColor
					}
				}

				if ri < len(runes) && vCol < visCol+widths[ri] && vCol < lineVisW {
					if vCol == visCol {
						switch {
						case focused && isCursorLine && vCol == e.cursor.Col:
							buf.Set(cx, bufY, cell.NewCell(runes[ri], e.cellColor(e.th.Colors.Bg), e.cellColor(e.th.Colors.Accent), cell.AttrBold))
						case e.isSelected(lineIdx, vCol):
							buf.Set(cx, bufY, cell.NewCell(runes[ri], e.cellColor(e.th.Colors.Fg), selBgColor, 0))
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
					default:
						buf.Set(cx, bufY, blankCell)
					}
				}
			}
		}
	}

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

func (e *Editor) View(width, height int, padLeft, padRight int) []string {
	if !e.open || width < 10 || height < 4 {
		return nil
	}
	e.viewW = width - padLeft - padRight
	e.viewH = height

	sw := e.gutterWidth()
	sbW := scrollbar.ScrollbarWidth
	textW := e.viewW - sw - sbW
	if textW < 1 {
		textW = 1
	}
	bodyH := height

	lc := e.buf.LineCount()
	e.sbTrackH = bodyH
	e.scrollbar.SetTrackHeight(bodyH)
	maxScroll := lc - bodyH
	if maxScroll < 0 {
		maxScroll = 0
	}
	e.scrollbar.SetScrollInfo(lc, bodyH, e.scrollY)
	sbView := e.scrollbar.View()
	sbLines := strings.Split(sbView, "\n")

	lines := make([]string, 0, bodyH)

	padL := strings.Repeat(" ", padLeft)
	padR := strings.Repeat(" ", padRight)
	gutterStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Muted)
	sepStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Border)
	fgStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Fg)
	showCursor := e.cursorVisible
	cursorFg := lipgloss.NewStyle().Foreground(e.th.Colors.Bg).Background(e.th.Colors.Accent).Bold(true)
	blankStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Muted)
	sbBgStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Border)
	selStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Fg).Background(e.th.Colors.Selection)
	errStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Error).Underline(true)
	warnStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Warning).Underline(true)
	infoStyle := lipgloss.NewStyle().Foreground(e.th.Colors.Muted).Underline(true)

	diagStyles := map[int]lipgloss.Style{
		DiagError:   errStyle,
		DiagWarning: warnStyle,
		DiagInfo:    infoStyle,
		DiagHint:    infoStyle,
	}

	for y := 0; y < bodyH; y++ {
		lineIdx := e.scrollY + y
		runes, lineVisW, widths, colorMap := e.lineRenderInfo(lineIdx, e.scrollX)
		diags := e.lineDiagnostics(lineIdx)

		ri := 0
		visCol := 0
		for ri < len(runes) && visCol+widths[ri] <= e.scrollX {
			visCol += widths[ri]
			ri++
		}

		numStr := e.formatLineNum(lineIdx+1, sw-1)
		numPadded := strings.Repeat(" ", sw-1-len(numStr)) + numStr
		gutter := gutterStyle.Render(numPadded)
		sep := sepStyle.Render("│")

		isCursorLine := lineIdx == e.cursor.Line && showCursor

		var textPart strings.Builder
		hlCache := make(map[string]lipgloss.Style)
		for i := 0; i < textW; i++ {
			vCol := e.scrollX + i

			if vCol >= lineVisW {
				for j := i; j < textW; j++ {
					cvCol := e.scrollX + j
					sev := diagSeverityAtCol(diags, cvCol)
					switch {
					case cvCol == e.cursor.Col && isCursorLine:
						textPart.WriteString(cursorFg.Render(" "))
					case e.isSelected(lineIdx, cvCol):
						textPart.WriteString(selStyle.Render(" "))
					case sev > 0:
						textPart.WriteString(diagStyles[sev].Render(" "))
					default:
						textPart.WriteString(blankStyle.Render(" "))
					}
				}
				break
			}

			for ri < len(runes) && vCol >= visCol+widths[ri] {
				visCol += widths[ri]
				ri++
			}

			if ri < len(runes) && vCol < visCol+widths[ri] && vCol < lineVisW {
				sev := diagSeverityAtCol(diags, vCol)
				if vCol == visCol {
					ch := string(runes[ri])
					switch {
					case vCol == e.cursor.Col && isCursorLine:
						textPart.WriteString(cursorFg.Render(ch))
					case e.isSelected(lineIdx, vCol):
						textPart.WriteString(selStyle.Render(ch))
					case sev > 0:
						ds := diagStyles[sev]
						if vCol < len(colorMap) && colorMap[vCol] != "" {
							ds = ds.Foreground(lipgloss.Color(colorMap[vCol])).Underline(true)
						}
						textPart.WriteString(ds.Render(ch))
					default:
						if vCol < len(colorMap) && colorMap[vCol] != "" {
							key := colorMap[vCol]
							hlS, ok := hlCache[key]
							if !ok {
								hlS = lipgloss.NewStyle().Foreground(lipgloss.Color(key))
								hlCache[key] = hlS
							}
							textPart.WriteString(hlS.Render(ch))
						} else {
							textPart.WriteString(fgStyle.Render(ch))
						}
					}
				} else {
					switch {
					case vCol == e.cursor.Col && isCursorLine:
						textPart.WriteString(cursorFg.Render(" "))
					case e.isSelected(lineIdx, vCol):
						textPart.WriteString(selStyle.Render(" "))
					case sev > 0:
						textPart.WriteString(diagStyles[sev].Render(" "))
					default:
						textPart.WriteString(blankStyle.Render(" "))
					}
				}
			} else {
				sev := diagSeverityAtCol(diags, vCol)
				switch {
				case vCol == e.cursor.Col && isCursorLine:
					textPart.WriteString(cursorFg.Render(" "))
				case e.isSelected(lineIdx, vCol):
					textPart.WriteString(selStyle.Render(" "))
				case sev > 0:
					textPart.WriteString(diagStyles[sev].Render(" "))
				default:
					textPart.WriteString(blankStyle.Render(" "))
				}
			}
		}
		textStr := textPart.String()

		sbChar := " "
		if y < len(sbLines) && sbLines[y] != "" {
			sbChar = sbLines[y]
		}
		renderedSB := sbBgStyle.Render(sbChar)

		innerLine := gutter + sep + textStr + renderedSB
		innerVisW := rendering.VisibleWidth(innerLine)
		rightPad := e.viewW - innerVisW
		if rightPad < 0 {
			rightPad = 0
		}
		line := padL + innerLine + strings.Repeat(" ", rightPad) + padR
		lines = append(lines, line)
	}

	return lines
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
	lineCount := e.buf.LineCount()
	digits := 1
	for n := lineCount; n >= 10; n /= 10 {
		digits++
	}
	if digits < 2 {
		digits = 2
	}
	return digits + 1
}

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
	if e.lspConnected {
		errs, warns, hints := e.DiagnosticCounts()
		if errs > 0 || warns > 0 || hints > 0 {
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
	}
	right := e.itoa(e.cursor.Line+1) + ":" + e.itoa(e.cursor.Col+1) + "  " + e.itoa(lc) + "L"
	padding := w - rendering.VisibleWidth(left) - rendering.VisibleWidth(right) - 2
	if padding < 1 {
		padding = 1
	}
	return " " + left + strings.Repeat(" ", padding) + right + " "
}

func (e *Editor) lineRenderInfo(lineIdx int, scrollX int) ([]rune, int, []int, []string) {
	if lineIdx < 0 {
		return nil, -1, nil, nil
	}
	b, err := e.buf.Line(lineIdx)
	if err != nil {
		return nil, -1, nil, nil
	}
	text := stringsReplaceTab(string(b))

	if e.lineRenderCache != nil {
		if cached := e.lineRenderCache[lineIdx]; cached != nil && cached.text == text && cached.scrollX == scrollX {
			return cached.runes, cached.totalW, cached.widths, cached.colorMap
		}
	}

	runes := []rune(text)
	widths := make([]int, len(runes))
	totalW := 0
	for i, r := range runes {
		w := runeVisualWidth(r)
		widths[i] = w
		totalW += w
	}
	colorMap := e.buildLineColorMapWithRunes(text, totalW, runes, widths)

	if e.lineRenderCache == nil {
		e.lineRenderCache = make(map[int]*lineRenderCache, 128)
	}
	if len(e.lineRenderCache) > 256 {
		clear(e.lineRenderCache)
	}
	e.lineRenderCache[lineIdx] = &lineRenderCache{
		text:     text,
		scrollX:  scrollX,
		runes:    runes,
		widths:   widths,
		totalW:   totalW,
		colorMap: colorMap,
	}

	return runes, totalW, widths, colorMap
}

func (e *Editor) cellColor(c color.Color) cell.Color {
	return rendering.ThemeColorToCell(c)
}

func (e *Editor) lineVisualWidth(lineIdx int) int {
	runes, width, _, _ := e.lineRenderInfo(lineIdx, 0)
	if width >= 0 {
		return width
	}
	return lineVisualWidthFromRunes(runes)
}

func (e *Editor) lineRunes(lineIdx int) []rune {
	runes, _, _, _ := e.lineRenderInfo(lineIdx, 0)
	return runes
}

func (e *Editor) ensureCursorVisible() {
	sw := e.gutterWidth()
	sbW := scrollbar.ScrollbarWidth
	availW := e.viewW - sw - sbW
	if availW < 1 {
		availW = 1
	}
	availH := e.viewH
	if availH < 1 {
		availH = 1
	}

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

	lc := e.buf.LineCount()
	e.sbTrackH = e.viewH
	e.scrollbar.SetTrackHeight(e.viewH)
	e.scrollbar.SetScrollInfo(lc, e.viewH, e.scrollY)
}

func stringsReplaceTab(s string) string {
	return strings.ReplaceAll(s, "\t", "    ")
}
