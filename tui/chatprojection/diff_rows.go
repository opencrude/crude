package chatprojection

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/tools"
	"crude/tui/ui"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
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
		rows = normalizeDiffPresentationRows(rows)

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

// BuildBandedDiffRows renders a unified diff in Crude's compact banded format:
// no fences, no file/hunk headers — just right-aligned dim line numbers, a
// tight `+`/`-` indicator column, and per-line background bands (full-width
// green for additions, red for deletions) so changes read as colored stripes.
// Context lines render without a band; trimmed gaps render as untinted `···`.
// The band is padded to `totalWidth` so tinted rows span the full painted row.
// When expanded is false the preview is budgeted and a "+N more changes"
// footer reports the hidden remainder.
func BuildBandedDiffRows(raw string, textWidth, totalWidth int, expanded bool) []PaintLine {
	files := tools.ParseUnifiedDiffStruct(raw)
	if len(files) == 0 {
		return nil
	}

	const previewDiffRows = 24
	var all []PaintLine
	totalRows := 0
	for fi, file := range files {
		if fi > 0 {
			totalRows++
			if expanded || len(all) < previewDiffRows {
				all = append(all, buildDiffGutterBlank())
			}
		}
		if len(files) > 1 && file.Path != "" {
			totalRows++
			if expanded || len(all) < previewDiffRows {
				all = append(all, buildDiffFileTransitionRow(file.Path))
			}
		}
		rows := tools.BuildDiffRows(file)
		if len(rows) > 0 && tools.CompactContextLines > 0 {
			rows = tools.TrimDiffContext(rows, tools.CompactContextLines)
		}
		rows = normalizeDiffPresentationRows(rows)
		maxNumWidth := 1
		for _, r := range rows {
			if r.Kind == tools.DiffLineDeletion {
				if r.OldLine > 0 {
					w := rendering.VisibleWidth(strconv.Itoa(r.OldLine))
					if w > maxNumWidth {
						maxNumWidth = w
					}
				}
				continue
			}
			if r.LineNum > 0 {
				w := rendering.VisibleWidth(strconv.Itoa(r.LineNum))
				if w > maxNumWidth {
					maxNumWidth = w
				}
			}
		}
		maxNumWidth = min(maxNumWidth, max(1, textWidth-4))

		contentWidth := max(1, textWidth-maxNumWidth-3)
		var lexer chroma.Lexer
		if expanded || len(all) < previewDiffRows {
			lexer = lexers.Match(file.Path)
		}

		for _, r := range rows {
			if r.Ellipsis {
				totalRows++
				if expanded || len(all) < previewDiffRows {
					all = append(all, buildDiffEllipsisRow(maxNumWidth))
				}
				continue
			}
			segments := []string{r.Content}
			if rendering.VisibleWidth(r.Content) > contentWidth {
				segments = rendering.HardWrap(r.Content, contentWidth)
			}
			totalRows += len(segments)
			// Count all visual rows for the exact footer, but never tokenize or
			// allocate painted cells for content beyond the preview budget.
			if !expanded {
				segments = segments[:min(len(segments), previewDiffRows-len(all))]
			}
			if len(segments) > 0 {
				all = append(all, buildBandedDiffContentRows(r, lexer, segments, maxNumWidth, totalWidth)...)
			}
		}
	}
	if !expanded && totalRows > previewDiffRows {
		shown := previewDiffRows - 1
		hidden := totalRows - shown
		out := append([]PaintLine(nil), all[:shown]...)
		return append(out, buildDiffTruncationRow(0, hidden))
	}
	if len(all) == 0 {
		return nil
	}
	return all
}

// normalizeDiffPresentationRows removes block-local common indentation and
// maps four source indentation columns to two display columns. Ellipsis rows
// split independently-normalized blocks; source content is never mutated.
func normalizeDiffPresentationRows(rows []tools.DiffRow) []tools.DiffRow {
	out := append([]tools.DiffRow(nil), rows...)
	start := 0
	for start < len(out) {
		for start < len(out) && out[start].Ellipsis {
			start++
		}
		end := start
		for end < len(out) && !out[end].Ellipsis {
			end++
		}
		normalizeDiffPresentationBlock(out[start:end])
		start = end
	}
	return out
}

func normalizeDiffPresentationBlock(rows []tools.DiffRow) {
	common := -1
	expanded := make([]string, len(rows))
	for i, row := range rows {
		expanded[i] = rendering.ExpandTabs(row.Content, 4)
		if strings.TrimSpace(expanded[i]) == "" {
			continue
		}
		indent := len(expanded[i]) - len(strings.TrimLeft(expanded[i], " "))
		if common < 0 || indent < common {
			common = indent
		}
	}
	if common < 0 {
		common = 0
	}
	for i := range rows {
		if strings.TrimSpace(expanded[i]) == "" {
			rows[i].Content = ""
			continue
		}
		indent := len(expanded[i]) - len(strings.TrimLeft(expanded[i], " "))
		relative := max(0, indent-common)
		displayIndent := (relative/4)*2 + relative%4
		rows[i].Content = strings.Repeat(" ", displayIndent) + expanded[i][indent:]
	}
}

// buildBandedDiffContentRows renders one diff row with the tight gutter and
// full-width band. Long content hard-wraps with continuation rows that keep
// the band and blank the number/sign columns.
func buildBandedDiffContentRows(r tools.DiffRow, lexer chroma.Lexer, segments []string, maxNumWidth, totalWidth int) []PaintLine {
	numStr := ""
	// Deletions reference the old-file line; additions/context reference the
	// new-file line. This gives each changed row a meaningful gutter.
	if r.Kind == tools.DiffLineDeletion {
		if r.OldLine > 0 {
			numStr = strconv.Itoa(r.OldLine)
		}
	} else if r.LineNum > 0 {
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

	out := make([]PaintLine, 0, len(segments))
	for si, seg := range segments {
		sign := indicator
		blankNum := paddedNum
		if si > 0 {
			sign = " "
			blankNum = strings.Repeat(" ", len(paddedNum))
		}
		spans := []RowSpan{
			{Text: "│ ", Role: ui.ColorMuted},
			{Text: blankNum, Role: ui.ColorMuted},
			{Text: " ", Role: ui.ColorMuted},
			{Text: sign + " ", Role: indicatorRole},
		}
		spans = append(spans, syntaxDiffSpans(lexer, seg, contentRole)...)
		pl := PaintLine{Spans: spans}
		if bandSet {
			pl = bandPaintLine(pl, band, totalWidth)
		}
		out = append(out, pl)
	}
	return out
}

func syntaxDiffSpans(lexer chroma.Lexer, text string, fallback ui.SemanticColor) []RowSpan {
	if lexer == nil {
		return []RowSpan{{Text: text, Role: fallback}}
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, text)
	if err != nil {
		return []RowSpan{{Text: text, Role: fallback}}
	}
	style := styles.Get("github-dark")
	if style == nil {
		style = styles.Fallback
	}
	var spans []RowSpan
	for token := iterator(); token != chroma.EOF; token = iterator() {
		span := RowSpan{Text: token.Value, Role: fallback}
		entry := style.Get(token.Type)
		if entry.Colour.IsSet() {
			span.FGColor = rendering.LipglossColorToCell(entry.Colour.String())
			span.FGSet = !span.FGColor.IsDefault()
		}
		spans = append(spans, span)
	}
	if len(spans) == 0 {
		return []RowSpan{{Text: text, Role: fallback}}
	}
	return spans
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

func buildDiffFileTransitionRow(path string) PaintLine {
	return PaintLine{Spans: []RowSpan{
		{Text: "│   ", Role: ui.ColorMuted},
		{Text: path, Role: ui.ColorDiffFile, Attr: cell.AttrBold},
	}}
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

// PlanViewData is the neutral projection of one persisted plan, serialized in
// canonical update_plan/get_plan tool results. The renderer coalesces cards by
// plan id rather than parsing presentation text.
type PlanViewData struct {
	ID          string         `json:"id"`
	Revision    uint64         `json:"revision"`
	Objective   string         `json:"objective"`
	Status      string         `json:"status"`
	Explanation string         `json:"explanation"`
	Steps       []PlanStepView `json:"steps"`
}

// PlanStepView is one step within a PlanViewData card.
type PlanStepView struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// BuildPlanViewRows renders a first-class plan card from canonical plan JSON.
// It replaces the formatted-box parser for update_plan tool messages: each step
// becomes a single row with a semantic status marker, and the header carries
// the objective + status. Returns nil when the payload is not a valid plan.
func BuildPlanViewRows(raw string, contentWidth int) []PaintLine {
	view, ok := parsePlanView(raw)
	if !ok {
		return nil
	}
	return buildPlanViewBody(view, max(1, contentWidth), false)
}

// BuildPlanViewRowsCollapsed renders the collapsed ("unopened") plan card: a
// windowed slice centered on the live step — the current in_progress step (or
// the next pending one) plus its immediate neighbours — because what is
// happening now matters more to a reader than steps finished hours ago. Rows
// hidden on either side are summarized by count.
func BuildPlanViewRowsCollapsed(raw string, contentWidth int) []PaintLine {
	view, ok := parsePlanView(raw)
	if !ok {
		return nil
	}
	return buildPlanViewBody(view, max(1, contentWidth), true)
}

func buildPlanViewBody(view *PlanViewData, contentWidth int, collapsed bool) []PaintLine {
	var out []PaintLine
	// Header rows: "│   Plan: <objective> (<status> · n/m)". The objective is
	// the banner a reader scans first, so it wraps instead of truncating.
	out = append(out, buildPlanViewHeader(view, contentWidth)...)
	if view.Explanation != "" {
		for _, line := range rendering.WrapText(view.Explanation, max(1, contentWidth-4)) {
			out = append(out, PaintLine{Spans: []RowSpan{
				{Text: "│   ", Role: ui.ColorMuted},
				{Text: line, Role: ui.ColorMuted, Attr: cell.AttrItalic},
			}})
		}
	}
	steps := planDisplaySteps(view)
	if collapsed && view.Status != "completed" && view.Status != "cancelled" {
		lo, hi, windowed := planWindowBounds(steps)
		if windowed && lo > 0 {
			out = append(out, planWindowSummaryLine(lo, "earlier"))
		}
		if windowed {
			steps = steps[lo:hi]
		}
		for _, step := range steps {
			out = append(out, buildPlanViewStep(step, contentWidth, true)...)
		}
		if windowed && hi < len(view.Steps) {
			out = append(out, planWindowSummaryLine(len(view.Steps)-hi, "remaining"))
		}
	} else {
		for _, step := range steps {
			out = append(out, buildPlanViewStep(step, contentWidth, collapsed)...)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// planWindowSteps is the minimum number of steps a collapsed card shows before
// hiding anything: the live step with two neighbours on each side, widened at
// either end so a window near the start or end of the plan never shrinks below
// a readable height. A reader skimming a banner needs context around the live
// step, not a keyhole view of it.
const planWindowSteps = 5

// planWindowBounds picks the collapsed-presentation step window: the currently
// in_progress step (falling back to the first pending one, then to the latest
// touched step) surrounded by enough neighbours to reach planWindowSteps rows.
// Short plans and plans with no live step show everything.
func planWindowBounds(steps []PlanStepView) (int, int, bool) {
	if len(steps) <= planWindowSteps {
		return 0, 0, false
	}
	cur := -1
	for i, step := range steps {
		if step.Status == "in_progress" {
			cur = i
			break
		}
	}
	if cur < 0 {
		for i, step := range steps {
			if step.Status == "pending" {
				cur = i
				break
			}
		}
	}
	if cur < 0 {
		// No live step (a finished, failed, or cancelled plan): anchor on the
		// most recently touched step so the tail of the work stays visible.
		for i := len(steps) - 1; i >= 0; i-- {
			if steps[i].Status != "pending" {
				cur = i
				break
			}
		}
	}
	if cur < 0 {
		return 0, 0, false
	}
	lo := cur - planWindowSteps/2
	hi := lo + planWindowSteps
	if lo < 0 {
		lo, hi = 0, planWindowSteps
	}
	if hi > len(steps) {
		hi, lo = len(steps), len(steps)-planWindowSteps
	}
	return lo, hi, true
}

func planDisplaySteps(view *PlanViewData) []PlanStepView {
	steps := append([]PlanStepView(nil), view.Steps...)
	if view.Status != "cancelled" {
		return steps
	}
	reason := tools.CollapseLine(view.Explanation)
	for i := range steps {
		if steps[i].Status != "pending" && steps[i].Status != "in_progress" {
			continue
		}
		steps[i].Status = "cancelled"
		if tools.CollapseLine(steps[i].Reason) == "" {
			steps[i].Reason = reason
		}
	}
	return steps
}

func planWindowSummaryLine(n int, position string) PaintLine {
	noun := "steps"
	if n == 1 {
		noun = "step"
	}
	return PaintLine{Spans: []RowSpan{
		{Text: "│   ", Role: ui.ColorMuted},
		{Text: fmt.Sprintf("⋯ %d %s %s", n, position, noun), Role: ui.ColorMuted, Attr: cell.AttrItalic},
	}}
}

func parsePlanView(raw string) (*PlanViewData, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var view PlanViewData
	if err := json.Unmarshal([]byte(raw), &view); err == nil && view.ID != "" && len(view.Steps) > 0 {
		return &view, true
	}
	var envelope struct {
		Plan        PlanViewData `json:"plan"`
		CurrentPlan PlanViewData `json:"current_plan"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, false
	}
	for _, candidate := range []PlanViewData{envelope.Plan, envelope.CurrentPlan} {
		if candidate.ID != "" && len(candidate.Steps) > 0 {
			copy := candidate
			return &copy, true
		}
	}
	return nil, false
}

// buildPlanViewHeader renders the card banner rows: "│   Plan: <objective>
// (<status> · done/total)". The objective is never truncated — it wraps with a
// hanging indent aligned under the objective's first word, and the status
// suffix trails the final row (or starts its own row when it does not fit), so
// a long title stays completely visible in both compact and expanded form.
func buildPlanViewHeader(view *PlanViewData, contentWidth int) []PaintLine {
	status := view.Status
	if status == "" {
		status = "active"
	}
	done := 0
	for _, step := range planDisplaySteps(view) {
		if step.Status == "completed" {
			done++
		}
	}
	objective := tools.CollapseLine(view.Objective)
	if objective == "" {
		objective = "(untitled plan)"
	}
	type headerWord struct {
		text string
		role ui.SemanticColor
		attr cell.Attrs
	}
	const prefix = "Plan:"
	words := []headerWord{{text: prefix, role: ui.ColorAccent, attr: cell.AttrBold}}
	for _, word := range strings.Fields(objective) {
		words = append(words, headerWord{text: word, role: ui.ColorAccent, attr: cell.AttrBold})
	}
	suffix := fmt.Sprintf("(%s · %d/%d)", status, done, len(view.Steps))
	suffixRole := planStatusRole(status)
	for _, word := range strings.Fields(suffix) {
		words = append(words, headerWord{text: word, role: suffixRole})
	}

	const gutter = "│   "
	avail := max(1, contentWidth-rendering.VisibleWidth(gutter))
	indent := strings.Repeat(" ", rendering.VisibleWidth(prefix))
	var rows []PaintLine
	cur := []RowSpan{{Text: gutter, Role: ui.ColorMuted}}
	curWidth := 0
	newRow := func() {
		rows = append(rows, PaintLine{Spans: cur})
		cur = []RowSpan{{Text: gutter, Role: ui.ColorMuted}, {Text: indent, Role: ui.ColorMuted}}
		curWidth = rendering.VisibleWidth(indent)
	}
	for _, word := range words {
		for _, chunk := range planTextChunks(word.text, avail) {
			chunkWidth := rendering.VisibleWidth(chunk)
			space := 0
			if curWidth > 0 {
				space = 1
			}
			if curWidth+space+chunkWidth > avail && curWidth > 0 {
				newRow()
				space = 0
			}
			if space == 1 {
				cur = append(cur, RowSpan{Text: " ", Role: ui.ColorMuted})
				curWidth++
			}
			cur = append(cur, RowSpan{Text: chunk, Role: word.role, Attr: word.attr})
			curWidth += chunkWidth
		}
	}
	rows = append(rows, PaintLine{Spans: cur})
	return rows
}

// planTextChunks splits one token wider than the row into hard-wrapped
// pieces, so even an unbroken token stays fully visible — shared by the header
// objective words and the expanded step text.
func planTextChunks(word string, width int) []string {
	if rendering.VisibleWidth(word) <= width {
		return []string{word}
	}
	return rendering.HardWrap(word, width)
}

// buildPlanViewStep renders one step. Compact keeps the banner contract of
// exactly one elided row per step. Expanded is the reading view: the step's
// full text — description plus any result or reason — wraps across rows with a
// hanging indent aligned under the text, so nothing a step carries stays
// hidden behind an ellipsis.
func buildPlanViewStep(step PlanStepView, contentWidth int, collapsed bool) []PaintLine {
	marker, role := planStepMarker(step.Status)
	settled := step.Status == "completed" || step.Status == "skipped" || step.Status == "cancelled"
	textRole, textAttr := ui.ColorFg, cell.Attrs(0)
	if settled {
		// Settled work reads as closed: strike the whole step text, while the
		// status marker preserves whether it completed, skipped, or cancelled.
		textRole, textAttr = ui.ColorMuted, cell.AttrStrikethrough
	}
	text := planStepText(step)
	avail := max(1, contentWidth-6)
	if collapsed {
		return []PaintLine{{
			Spans: []RowSpan{
				{Text: "│   ", Role: ui.ColorMuted},
				{Text: marker + " ", Role: role},
				{Text: rendering.TruncateLine(text, avail), Role: textRole, Attr: textAttr},
			},
		}}
	}
	chunks := wrapPlanText(text, avail)
	indent := strings.Repeat(" ", rendering.VisibleWidth(marker+" "))
	rows := make([]PaintLine, 0, len(chunks))
	for i, chunk := range chunks {
		lead, leadRole := marker+" ", role
		if i > 0 {
			lead, leadRole = indent, ui.ColorMuted
		}
		rows = append(rows, PaintLine{Spans: []RowSpan{
			{Text: "│   ", Role: ui.ColorMuted},
			{Text: lead, Role: leadRole},
			{Text: chunk, Role: textRole, Attr: textAttr},
		}})
	}
	return rows
}

// planStepText composes a step's full text from its description plus optional
// lifecycle reason. Compact elides this text to one row and expanded wraps it
// whole, so both forms show the same sentence.
func planStepText(step PlanStepView) string {
	description := tools.CollapseLine(step.Description)
	if description == "" {
		description = tools.CollapseLine(step.ID)
	}
	if reason := tools.CollapseLine(step.Reason); reason != "" {
		return description + " (" + reason + ")"
	}
	return description
}

// wrapPlanText word-wraps plan text to width, hard-breaking a single token
// wider than the row so no content can overflow the card.
func wrapPlanText(text string, width int) []string {
	var rows []string
	cur := ""
	for _, word := range strings.Fields(text) {
		for _, chunk := range planTextChunks(word, width) {
			switch {
			case cur == "":
				cur = chunk
			case rendering.VisibleWidth(cur)+1+rendering.VisibleWidth(chunk) <= width:
				cur += " " + chunk
			default:
				rows = append(rows, cur)
				cur = chunk
			}
		}
	}
	if cur != "" || len(rows) == 0 {
		rows = append(rows, cur)
	}
	return rows
}

func planStatusRole(status string) ui.SemanticColor {
	switch status {
	case "completed":
		return ui.ColorSuccess
	case "paused":
		return ui.ColorWarning
	case "cancelled":
		return ui.ColorError
	default:
		return ui.ColorAccent2
	}
}

func planStepMarker(status string) (string, ui.SemanticColor) {
	switch status {
	case "completed":
		return "✓", ui.ColorSuccess
	case "in_progress":
		return "→", ui.ColorAccent2
	case "blocked":
		return "!", ui.ColorWarning
	case "failed":
		return "✗", ui.ColorError
	case "skipped":
		return "−", ui.ColorMuted
	case "cancelled":
		return "×", ui.ColorError
	default:
		return "☐", ui.ColorMuted
	}
}
