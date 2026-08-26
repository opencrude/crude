package chatprojection

import (
	"fmt"
	"strconv"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/tools"
	"crude/tui/ui"
)

// BuildDiffRows turns a raw unified-diff string into native PaintLines using
// the colour-free structural parser in tui/tools and semantic roles from
// ui.SemanticColor. It preserves the observable behaviour of the former ANSI
// renderer (RenderDiffNoHeader): skip-header, compact-mode context trimming,
// bottom truncation with "+N more changes not shown", line-number gutter with
// alignment, and word-wrapping of long content. Each produced PaintLine is
// prefixed with the tool-body gutter "│ ". Returns nil for empty/unparseable
// input (caller falls back to a plain line).
func BuildDiffRows(raw string, maxLines, maxWidth int) []PaintLine {
	files := tools.ParseUnifiedDiffStruct(raw)
	if len(files) == 0 {
		return nil
	}

	var out []PaintLine
	for fi, file := range files {
		if fi > 0 {
			out = append(out, buildDiffGutterBlank())
		}

		// No header (skipHeader). headerLines = 0.
		allRows := tools.BuildDiffRows(file)
		needCompact := maxLines > 0 && len(allRows) > maxLines
		var rows []tools.DiffRow
		if needCompact {
			rows = tools.TrimDiffContext(allRows, tools.CompactContextLines)
		} else {
			rows = allRows
		}

		availLines := maxLines
		truncated := false
		truncatedRemaining := 0
		if availLines > 0 && len(rows) > availLines {
			truncatedRemaining = len(rows) - availLines
			rows = rows[:availLines]
			truncated = true
		}

		maxNumWidth := 3
		for _, r := range rows {
			if r.LineNum > 0 {
				w := rendering.VisibleWidth(strconv.Itoa(r.LineNum))
				if w > maxNumWidth {
					maxNumWidth = w
				}
			}
		}

		gutterWidth := maxNumWidth + 3 + 2 // numStr + " │ " + "IND "
		contentWidth := maxWidth - gutterWidth
		if contentWidth < 10 {
			contentWidth = 10
		}

		for _, r := range rows {
			if r.Ellipsis {
				out = append(out, buildDiffEllipsisRow(maxNumWidth))
				continue
			}
			out = append(out, buildDiffContentRows(r, maxNumWidth, contentWidth)...)
		}

		if truncated {
			out = append(out, buildDiffTruncationRow(maxNumWidth, truncatedRemaining))
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// BuildClaudeDiffRows renders a unified diff in the Claude-style body format:
// no fences, no file/hunk headers — just right-aligned dim line numbers, a
// tight `+`/`-` indicator column, and per-line background bands (full-width
// green for additions, red for deletions) so changes read as colored stripes.
// Context lines render without a band; trimmed gaps render as untinted `···`.
// The band is padded to `totalWidth` so tinted rows span the full painted row.
// When expanded is false the preview is budgeted and a "+N more changes"
// footer reports the hidden remainder.
func BuildClaudeDiffRows(raw string, textWidth, totalWidth int, expanded bool) []PaintLine {
	files := tools.ParseUnifiedDiffStruct(raw)
	if len(files) == 0 {
		return nil
	}

	const previewDiffRows = 24
	var out []PaintLine
	remaining := previewDiffRows
	if expanded {
		remaining = -1
	}
	for fi, file := range files {
		if fi > 0 {
			out = append(out, buildDiffGutterBlank())
		}
		rows := tools.BuildDiffRows(file)
		if len(rows) > 0 && tools.CompactContextLines > 0 {
			rows = tools.TrimDiffContext(rows, tools.CompactContextLines)
		}

		maxNumWidth := 4
		for _, r := range rows {
			if r.LineNum > 0 {
				w := rendering.VisibleWidth(strconv.Itoa(r.LineNum))
				if w > maxNumWidth {
					maxNumWidth = w
				}
			}
		}

		contentWidth := textWidth - maxNumWidth - 3
		if contentWidth < 10 {
			contentWidth = 10
		}

		for _, r := range rows {
			if remaining == 0 {
				continue
			}
			if r.Ellipsis {
				out = append(out, buildDiffEllipsisRow(maxNumWidth))
				continue
			}
			rowLines := buildClaudeDiffContentRows(r, maxNumWidth, contentWidth, totalWidth)
			out = append(out, rowLines...)
			if remaining > 0 {
				remaining -= len(rowLines)
				if remaining < 0 {
					remaining = 0
				}
			}
		}
	}

	if remaining == 0 {
		hidden := countDiffRows(raw) - len(out)
		if hidden > 0 {
			out = append(out, buildDiffTruncationRow(0, hidden))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func countDiffRows(raw string) int {
	files := tools.ParseUnifiedDiffStruct(raw)
	n := 0
	for _, f := range files {
		n += len(tools.BuildDiffRows(f))
	}
	return n
}

// buildClaudeDiffContentRows renders one diff row with the tight gutter and
// full-width band. Long content hard-wraps with continuation rows that keep
// the band and blank the number/sign columns.
func buildClaudeDiffContentRows(r tools.DiffRow, maxNumWidth, contentWidth, totalWidth int) []PaintLine {
	numStr := ""
	if r.LineNum > 0 {
		numStr = strconv.Itoa(r.LineNum)
	}
	paddedNum := fmt.Sprintf("%*s", maxNumWidth, numStr)

	indicator := " "
	contentRole := ui.ColorDiffContext
	indicatorRole := ui.ColorMuted
	band := ui.SemanticColor(0)
	bandSet := false
	switch r.Kind {
	case tools.DiffLineAddition:
		indicator = "+"
		contentRole = ui.ColorDiffAddition
		indicatorRole = ui.ColorDiffAddition
		band = ui.ColorDiffAdditionBg
		bandSet = true
	case tools.DiffLineDeletion:
		indicator = "-"
		contentRole = ui.ColorDiffDeletion
		indicatorRole = ui.ColorDiffDeletion
		band = ui.ColorDiffDeletionBg
		bandSet = true
	case tools.DiffLineContext:
		indicator = " "
		contentRole = ui.ColorDiffContext
		indicatorRole = ui.ColorMuted
	}

	content := r.Content
	segments := []string{content}
	if rendering.VisibleWidth(content) > contentWidth {
		segments = rendering.HardWrap(content, contentWidth)
	}
	out := make([]PaintLine, 0, len(segments))
	for si, seg := range segments {
		sign := indicator
		blankNum := paddedNum
		if si > 0 {
			sign = " "
			blankNum = strings.Repeat(" ", len(paddedNum))
		}
		pl := PaintLine{Spans: []RowSpan{
			{Text: "│ ", Role: ui.ColorMuted},
			{Text: blankNum, Role: ui.ColorMuted},
			{Text: " ", Role: ui.ColorMuted},
			{Text: sign + " ", Role: indicatorRole},
			{Text: seg, Role: contentRole},
		}}
		if bandSet {
			pl = bandPaintLine(pl, band, totalWidth)
		}
		out = append(out, pl)
	}
	return out
}

// bandPaintLine pads a PaintLine with the band background so the tint spans
// the full painted row width (stopping at the scrollbar gutter like pane bg).
func bandPaintLine(pl PaintLine, band ui.SemanticColor, totalWidth int) PaintLine {
	spans := append([]RowSpan{}, pl.Spans...)
	// Keep the outer Crude gutter untinted; number/sign/content and trailing
	// cells share one semantic band.
	for i := 1; i < len(spans); i++ {
		spans[i].BgRole = band
		spans[i].BgSet = true
	}
	pl.Spans = spans
	used := pl.VisibleWidth()
	if used >= totalWidth {
		return pl
	}
	pad := totalWidth - used
	spans = append([]RowSpan{}, pl.Spans...)
	spans = append(spans, RowSpan{Text: strings.Repeat(" ", pad), Role: ui.ColorFg, BgRole: band, BgSet: true})
	return PaintLine{Spans: spans}
}

// buildDiffGutterBlank returns the inter-file blank row.
func buildDiffGutterBlank() PaintLine {
	return PaintLine{Spans: []RowSpan{{Text: "│ ", Role: ui.ColorMuted}}}
}

// buildDiffEllipsisRow returns an untinted "···" elision row.
func buildDiffEllipsisRow(maxNumWidth int) PaintLine {
	prefix := strings.Repeat(" ", maxNumWidth) + "   ···"
	return PaintLine{Spans: []RowSpan{
		{Text: "│ ", Role: ui.ColorMuted},
		{Text: prefix, Role: ui.ColorMuted},
	}}
}

func buildDiffTruncationRow(maxNumWidth, remaining int) PaintLine {
	msg := fmt.Sprintf("%s   +%d more changes not shown", strings.Repeat(" ", maxNumWidth), remaining)
	return PaintLine{Spans: []RowSpan{
		{Text: "│ ", Role: ui.ColorMuted},
		{Text: msg, Role: ui.ColorMuted},
	}}
}

// buildDiffContentRows renders one diff content row, word-wrapping long
// content. The indicator and content use the diff semantic role for the
// line kind; the line number and separator use Muted (matching the ANSI
// renderer where the gutter uses mutedStyle/sepStyle and the indicator
// switches to the line-kind style).
func buildDiffContentRows(r tools.DiffRow, maxNumWidth, contentWidth int) []PaintLine {
	numStr := ""
	if r.LineNum > 0 {
		numStr = strconv.Itoa(r.LineNum)
	}
	paddedNum := fmt.Sprintf("%*s", maxNumWidth, numStr)

	indicator := " "
	contentRole := ui.ColorDiffContext
	indicatorRole := ui.ColorMuted
	switch r.Kind {
	case tools.DiffLineAddition:
		indicator = "+"
		contentRole = ui.ColorDiffAddition
		indicatorRole = ui.ColorDiffAddition
	case tools.DiffLineDeletion:
		indicator = "─"
		contentRole = ui.ColorDiffDeletion
		indicatorRole = ui.ColorDiffDeletion
	case tools.DiffLineContext:
		indicator = " "
		contentRole = ui.ColorDiffContext
		indicatorRole = ui.ColorMuted
	}

	content := r.Content
	if rendering.VisibleWidth(content) <= contentWidth {
		return []PaintLine{buildOneDiffRow(paddedNum, indicator, content, contentRole, indicatorRole)}
	}
	segs := rendering.HardWrap(content, contentWidth)
	rows := make([]PaintLine, 0, len(segs))
	for si, seg := range segs {
		if si == 0 {
			rows = append(rows, buildOneDiffRow(paddedNum, indicator, seg, contentRole, indicatorRole))
		} else {
			// Continuation: blank line number, blank indicator, content only.
			blankNum := strings.Repeat(" ", maxNumWidth)
			rows = append(rows, PaintLine{Spans: []RowSpan{
				{Text: "│ ", Role: ui.ColorMuted},
				{Text: blankNum, Role: ui.ColorMuted},
				{Text: " │ ", Role: ui.ColorMuted},
				{Text: "  ", Role: ui.ColorMuted},
				{Text: seg, Role: contentRole},
			}})
		}
	}
	return rows
}

func buildOneDiffRow(paddedNum, indicator, content string, contentRole, indicatorRole ui.SemanticColor) PaintLine {
	return PaintLine{Spans: []RowSpan{
		{Text: "│ ", Role: ui.ColorMuted},
		{Text: paddedNum, Role: ui.ColorMuted},
		{Text: " │ ", Role: ui.ColorMuted},
		{Text: indicator + " ", Role: indicatorRole},
		{Text: content, Role: contentRole},
	}}
}

// BuildPlanRows turns plan-box text into native PaintLines using the
// colour-free structural parser in tui/tools. Each plan line is classified
// and mapped to a semantic role. Each row is prefixed with "│ " (the tool
// gutter) and padded with one space on each side (matching the former
// Padding(0,1) box). maxWidth is the total width available for one
// plan row including the "│ " prefix and the 1-col padding either side, so
// the text content width is maxWidth-4.
func BuildPlanRows(text string, maxWidth int) []PaintLine {
	contentWidth := tools.PlanBoxContentWidth(maxWidth - 4)
	lines := tools.ParsePlanBoxStruct(text, contentWidth)
	if len(lines) == 0 {
		return nil
	}
	out := make([]PaintLine, 0, len(lines))
	for _, pl := range lines {
		out = append(out, buildPlanRow(pl))
	}
	return out
}

func buildPlanRow(pl tools.PlanLine) PaintLine {
	if pl.Text == "" {
		// Blank line still carries the gutter + padding to fill the box width.
		return PaintLine{Spans: []RowSpan{
			{Text: "│ ", Role: ui.ColorMuted},
			{Text: " ", Role: ui.ColorMuted},
			{Text: "", Role: ui.ColorMuted},
			{Text: " ", Role: ui.ColorMuted},
		}}
	}
	role := planRowRole(pl.Kind)
	attr := cell.Attrs(0)
	if pl.Kind == tools.PlanRowHeader {
		attr = cell.AttrBold
	}
	return PaintLine{Spans: []RowSpan{
		{Text: "│ ", Role: ui.ColorMuted},
		{Text: " ", Role: ui.ColorMuted},
		{Text: pl.Text, Role: role, Attr: attr},
	}}
}

func planRowRole(k tools.PlanRowKind) ui.SemanticColor {
	switch k {
	case tools.PlanRowDone:
		return ui.ColorSuccess
	case tools.PlanRowActive:
		return ui.ColorAccent2
	case tools.PlanRowFailed:
		return ui.ColorError
	case tools.PlanRowWarning:
		return ui.ColorWarning
	case tools.PlanRowTodo:
		return ui.ColorMuted
	case tools.PlanRowHeader:
		return ui.ColorAccent
	case tools.PlanRowSuspended:
		return ui.ColorWarning
	default:
		return ui.ColorMuted
	}
}
