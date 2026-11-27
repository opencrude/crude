package workspacehub

import (
	"fmt"
	"time"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	// OverviewID is the Overview page.
	OverviewID        ui.ComponentID = "workspace-hub/overview"
	overviewRefreshID ui.ComponentID = "workspace-hub/overview/refresh"
	// ActionRefreshUsage reads the cost report again.
	ActionRefreshUsage = "workspacehub.refresh-usage"
)

// SessionSpend is the shown session's cost as this shell knows it.
type SessionSpend struct {
	CostUSD  float64
	Requests int
	Known    bool
}

// OverviewData is what the Overview tab shows. Problem explains a report
// that could not be read; the tab never shows a zero in its place.
type OverviewData struct {
	Loading bool
	Problem string
	Report  workspace.UsageReport
	Session SessionSpend
	Read    time.Time
}

// Overview shows the workspace's cost: tiles for this session, today, the
// last seven days and the whole workspace, a daily chart and a table by model.
type Overview struct {
	ui.Base
	tokens  *ui.TokenSet
	data    OverviewData
	refresh *ui.Button
	scroll  pageScroll
	now     func() time.Time
}

// NewOverview builds an empty Overview that shows that it is reading.
func NewOverview(tokens *ui.TokenSet) *Overview {
	o := &Overview{Base: ui.NewBase(OverviewID), tokens: tokens, data: OverviewData{Loading: true}, now: time.Now}
	o.refresh = ui.NewButton(overviewRefreshID, "Refresh", ActionRefreshUsage)
	o.refresh.SetVariant(ui.VariantSecondary)
	o.scroll = newPageScroll(OverviewID, tokens)
	o.SetTokens(tokens)
	return o
}

// SetTokens applies a theme.
func (o *Overview) SetTokens(tokens *ui.TokenSet) {
	if tokens == nil {
		return
	}
	o.tokens = tokens
	o.refresh.SetTokens(tokens)
	o.refresh.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	o.scroll.bar.SetTokens(tokens)
	o.scroll.bar.SetSurfaceBackground(ui.ColorBg)
}

// SetData replaces what the tab shows.
func (o *Overview) SetData(data OverviewData) {
	o.data = data
	o.refresh.SetDisabled(data.Loading)
}

// Data is what the tab shows now.
func (o *Overview) Data() OverviewData { return o.data }

// PreferredFocus is the page itself, so the keyboard can scroll it.
func (o *Overview) PreferredFocus() ui.ComponentID { return OverviewID }

// FocusOrder is the page, then Refresh.
func (o *Overview) FocusOrder() []ui.ComponentID {
	return []ui.ComponentID{OverviewID, overviewRefreshID}
}

// Children are Refresh and the scrollbar.
func (o *Overview) Children() []ui.Component {
	children := []ui.Component{o.refresh}
	if !o.scroll.bar.Bounds().Empty() {
		children = append(children, o.scroll.bar)
	}
	return children
}

// Measure fills the space it is given.
func (o *Overview) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

// Layout places Refresh on the first row and the scrollbar at the right edge.
func (o *Overview) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	o.SetBounds(bounds)
	o.refresh.Layout(ctx, ui.Rect{})
	o.scroll.bar.Layout(ctx, ui.Rect{})
	if bounds.Empty() {
		return
	}
	o.scroll.model.SetViewport(bounds.H)
	o.scroll.model.SetContent(o.contentHeight(o.innerWidth(bounds.W)))
	if o.scroll.bar.NeededForViewport(bounds.H) {
		o.scroll.bar.Layout(ctx, ui.Rect{X: bounds.W - ui.ScrollbarWidth, W: ui.ScrollbarWidth, H: bounds.H})
	}
	w := o.refresh.Measure(ctx, ui.Loose(20, 1)).W
	if o.scroll.model.Offset() == 0 && bounds.W > w+30 {
		o.refresh.Layout(ctx, ui.Rect{X: 1 + o.innerWidth(bounds.W) - w, Y: 1, W: w, H: 1})
	}
}

func (o *Overview) innerWidth(width int) int {
	inner := width - 2
	if o.scroll.bar.NeededForViewport(o.Bounds().H) {
		inner -= ui.ScrollbarWidth
	}
	return max(0, inner)
}

// tile is one headline number. Its detail is the first of details that fits
// the tile, so a narrow tile drops the least important words.
type tile struct {
	label, value string
	details      []string
}

func (o *Overview) tiles() []tile {
	report, now := o.data.Report, o.now()
	var today, week workspace.DayCost
	for i, day := range report.Days {
		if i >= len(report.Days)-7 {
			week.CostUSD += day.CostUSD
			week.Requests += day.Requests
		}
		if day.Day == now.Format("2006-01-02") {
			today = day
		}
	}
	session := tile{"This session", "No cost yet", []string{"No requests yet"}}
	if o.data.Session.Known || o.data.Session.Requests > 0 {
		session = tile{"This session", USD(o.data.Session.CostUSD), []string{requests(o.data.Session.Requests)}}
	}
	// The workspace counts sessions when the report has them, else requests.
	count := requests(report.Requests)
	if len(report.Sessions) > 0 {
		count = sessionsLabel(len(report.Sessions))
	}
	workspaceDetails := []string{count}
	if report.First > 0 {
		workspaceDetails = []string{count + " since " + Day(time.Unix(0, report.First), now), count}
	}
	return []tile{
		session,
		{"Today", USD(today.CostUSD), []string{requests(today.Requests)}},
		{"Last 7 days", USD(week.CostUSD), []string{requests(week.Requests)}},
		{"This workspace", USD(report.CostUSD), workspaceDetails},
	}
}

// fitting is the first of texts that fits width, else the last.
func fitting(texts []string, width int) string {
	for _, text := range texts {
		if rendering.VisibleWidth(text) <= width {
			return text
		}
	}
	if len(texts) == 0 {
		return ""
	}
	return texts[len(texts)-1]
}

func requests(n int) string {
	if n == 1 {
		return "1 request"
	}
	return Count(int64(n)) + " requests"
}

func sessionsLabel(n int) string {
	if n == 1 {
		return "1 session"
	}
	return Count(int64(n)) + " sessions"
}

const chartRows = 6

func tileColumns(width int) int {
	if width >= 4*18+3*2 {
		return 4
	}
	return 2
}

// contentHeight is how many rows the page paints at width.
func (o *Overview) contentHeight(width int) int {
	if o.data.Loading || o.data.Problem != "" && len(o.data.Report.Days) == 0 {
		return 4
	}
	rows := 1 + 2 // heading and gap
	rows += (4/tileColumns(width))*4 + 1
	rows += 2 + chartRows + 2 // chart heading, bars, axis and gap
	rows += 2 + max(1, len(o.data.Report.Models))
	if o.data.Report.UnknownCost > 0 || o.data.Report.Unreadable > 0 {
		rows += 2
	}
	return rows + 1
}

// Paint draws the whole page, skipping rows outside the viewport.
func (o *Overview) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	bounds := o.Bounds()
	bg := o.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, originX, originY, bounds, cell.BlankCell(bg))
	p := pen{buf: buf, tokens: o.tokens, bg: bg, x: originX + bounds.X + 1, y: originY + bounds.Y, offset: o.scroll.model.Offset(), viewport: bounds.H}
	width := o.innerWidth(bounds.W)
	now := o.now()
	row := 1
	p.text(0, row, width, "Cost and use", ui.ColorFg, cell.AttrBold)
	if !o.data.Read.IsZero() {
		p.text(13, row, max(0, width-24), "· updated "+ui.RelativeAge(o.data.Read, now), ui.ColorMuted, 0)
	}
	row += 2
	switch {
	case o.data.Loading && len(o.data.Report.Days) == 0:
		p.text(0, row, width, "Reading the cost of every session…", ui.ColorMuted, 0)
		return
	case o.data.Problem != "" && len(o.data.Report.Days) == 0:
		p.text(0, row, width, o.data.Problem, ui.ColorError, 0)
		return
	}
	row = o.paintTiles(p, row, width)
	row = o.paintChart(p, row, width, now)
	o.paintModels(p, row, width)
}

func (o *Overview) paintTiles(p pen, row, width int) int {
	columns := tileColumns(width)
	gap := 2
	tileW := max(8, (width-(columns-1)*gap)/columns)
	for index, t := range o.tiles() {
		col := (index % columns) * (tileW + gap)
		top := row + (index/columns)*4
		for line := range 3 {
			p.glyph(col, top+line, '▎', ui.ColorAccent)
		}
		p.text(col+2, top, tileW-2, t.label, ui.ColorMuted, 0)
		p.text(col+2, top+1, tileW-2, t.value, ui.ColorFg, cell.AttrBold)
		p.text(col+2, top+2, tileW-2, fitting(t.details, tileW-2), ui.ColorMuted, 0)
	}
	return row + (4/columns)*4 + 1
}

func (o *Overview) paintChart(p pen, row, width int, now time.Time) int {
	days := o.data.Report.Days
	p.text(0, row, width, "Cost per day", ui.ColorFg, cell.AttrBold)
	end := p.text(13, row, width-13, fmt.Sprintf("· last %d days", len(days)), ui.ColorMuted, 0)
	peak, peakDay := 0.0, ""
	for _, day := range days {
		if day.CostUSD > peak {
			peak, peakDay = day.CostUSD, day.Day
		}
	}
	if peak > 0 && width-end > 20 {
		at, _ := time.ParseInLocation("2006-01-02", peakDay, time.Local)
		p.right(0, row, width, "highest "+USD(peak)+" on "+Day(at, now), ui.ColorMuted, 0)
	}
	row += 2
	if peak <= 0 || len(days) == 0 {
		p.text(0, row, width, "No priced requests in this period.", ui.ColorMuted, 0)
		return row + chartRows + 2
	}
	colW := min(4, max(1, width/len(days)))
	barW := colW
	if colW >= 2 {
		barW = colW - 1
	}
	today := now.Format("2006-01-02")
	for index, day := range days {
		level := ui.ColumnLevel(day.CostUSD, peak, chartRows)
		role := ui.ColorAccent
		if day.Day == today {
			role = ui.ColorAccent2
		}
		for cellRow := range chartRows {
			glyph := ui.ColumnCell(level, cellRow)
			for x := range barW {
				if glyph == ' ' && cellRow == 0 {
					p.glyph(index*colW+x, row+chartRows-1, '▁', ui.ColorBorder)
					continue
				}
				p.glyph(index*colW+x, row+chartRows-1-cellRow, glyph, role)
			}
		}
	}
	axis := row + chartRows
	chartW := colW * len(days)
	first, _ := time.ParseInLocation("2006-01-02", days[0].Day, time.Local)
	middle, _ := time.ParseInLocation("2006-01-02", days[len(days)/2].Day, time.Local)
	p.text(0, axis, chartW, Day(first, now), ui.ColorMuted, 0)
	if label := Day(middle, now); chartW >= 40 {
		p.text((len(days)/2)*colW, axis, chartW/2, label, ui.ColorMuted, 0)
	}
	p.right(0, axis, chartW, "today", ui.ColorMuted, 0)
	return axis + 2
}

// paintModels draws the table by model. Each column is as wide as its widest
// cell over every row, so numbers line up and no column wastes space.
func (o *Overview) paintModels(p pen, row, width int) {
	models := o.data.Report.Models
	p.text(0, row, width, "By model", ui.ColorFg, cell.AttrBold)
	row++
	if len(models) == 0 {
		p.text(0, row+1, width, "No model requests are recorded in this workspace yet.", ui.ColorMuted, 0)
		return
	}
	type column struct {
		title string
		cell  func(workspace.ModelCost) string
		width int
	}
	columns := []column{
		{title: "Requests", cell: func(m workspace.ModelCost) string { return Count(int64(m.Requests)) }},
		{title: "Input", cell: func(m workspace.ModelCost) string { return Tokens(m.InputTokens) }},
		{title: "Cached", cell: func(m workspace.ModelCost) string { return Tokens(m.CachedTokens) }},
		{title: "Output", cell: func(m workspace.ModelCost) string { return Tokens(m.OutputTokens) }},
		{title: "Cost", cell: func(m workspace.ModelCost) string { return USD(m.CostUSD) }},
	}
	for i := range columns {
		columns[i].width = rendering.VisibleWidth(columns[i].title)
		for _, model := range models {
			columns[i].width = max(columns[i].width, rendering.VisibleWidth(columns[i].cell(model)))
		}
	}
	const shareW = 16
	share := width >= 70
	nameW := 0
	for _, model := range models {
		nameW = max(nameW, rendering.VisibleWidth(modelName(model)))
	}
	fixed := func() int {
		total := 0
		for _, c := range columns {
			total += c.width + 2
		}
		if share {
			total += shareW + 2
		}
		return total
	}
	// Drop columns from the least important while the model name is cramped.
	for _, drop := range []string{"Cached", "Output", "Input", "Requests"} {
		if width-fixed() >= min(nameW, 24) {
			break
		}
		for i, c := range columns {
			if c.title == drop {
				columns = append(columns[:i], columns[i+1:]...)
				break
			}
		}
	}
	nameW = max(8, min(nameW, width-fixed()))
	x := nameW + 2
	p.text(0, row+1, nameW, "Model", ui.ColorMuted, 0)
	for _, c := range columns {
		p.right(x, row+1, c.width, c.title, ui.ColorMuted, 0)
		x += c.width + 2
	}
	if share {
		p.text(x, row+1, shareW, "Share of cost", ui.ColorMuted, 0)
	}
	total := o.data.Report.CostUSD
	for index, model := range models {
		y := row + 2 + index
		p.text(0, y, nameW, modelName(model), ui.ColorFg, 0)
		x := nameW + 2
		for _, c := range columns {
			role := ui.ColorMuted
			if c.title == "Cost" {
				role = ui.ColorFg
			}
			p.right(x, y, c.width, c.cell(model), role, 0)
			x += c.width + 2
		}
		if share && total > 0 {
			fraction := model.CostUSD / total
			p.bar(x, y, shareW-5, fraction, ui.ColorAccent)
			p.right(x+shareW-5, y, 5, fmt.Sprintf("%.0f%%", fraction*100), ui.ColorMuted, 0)
		}
	}
	row += 2 + len(models) + 1
	if unknown := o.data.Report.UnknownCost; unknown > 0 {
		p.text(0, row, width, fmt.Sprintf("%s had no known price and are not in the totals.", requests(unknown)), ui.ColorMuted, 0)
		row++
	}
	if unreadable := o.data.Report.Unreadable; unreadable > 0 {
		p.text(0, row, width, fmt.Sprintf("Crude could not read %d session files. Their cost is not shown.", unreadable), ui.ColorWarning, 0)
	}
}

func modelName(model workspace.ModelCost) string {
	if model.ModelID == "" {
		return "Unknown model"
	}
	return model.ModelID
}

// Handle scrolls the page.
func (o *Overview) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if o.scroll.handle(evt) {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

// Semantics describes the page.
func (o *Overview) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Workspace cost", Description: "This workspace: " + USD(o.data.Report.CostUSD)}
}

var _ ui.Parent = (*Overview)(nil)
