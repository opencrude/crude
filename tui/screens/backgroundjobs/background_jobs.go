// Package backgroundjobs provides the session background-command detail panel.
package backgroundjobs

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/command"
	"crude/types/control"
)

const (
	ScreenID        ui.ComponentID = "session-background-jobs"
	ActionTerminate string         = "backgroundjobs.terminate"
	ActionBlur      string         = "backgroundjobs.blur"
	// ActionReveal shows the command's originating call in the conversation.
	ActionReveal string = "backgroundjobs.reveal"
	// ActionOutput opens the command's live output window.
	ActionOutput string = "backgroundjobs.output"
)

const (
	outputLabel    = "Output"
	terminateLabel = "Stop"
	// Bracketed button widths: "[ " + label + " ]".
	outputButtonW    = 10
	terminateButtonW = 8
)

// Control columns inside one job row, left to right.
const (
	columnCommand = iota
	columnOutput
	columnTerminate
)

const completedRetention = 15 * time.Second

type Job struct {
	TaskID    string
	Progress  command.Progress
	OwnerID   string
	Handle    string
	Command   string
	PID       int
	Status    string
	StartedAt time.Time
	EndedAt   time.Time
	Duration  time.Duration
	ExitCode  int
	Error     string
}

type Screen struct {
	ui.Base
	tokens     *ui.TokenSet
	jobs       []Job
	visible    int
	attention  bool
	lastNow    time.Time
	nextExpiry time.Time
	running    bool
	animating  bool
	ruleBleedX int
	ruleBleedW int
	rules      map[string]*ui.ProgressRule
	rows       []Job
	statuses   []rowStatus
	columns    columnLayout
	controls   map[ui.ComponentID]*rowControl
	buttonJobs map[ui.ComponentID]Job
	children   []ui.Component
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th), controls: make(map[ui.ComponentID]*rowControl)}
	s.rebuild(time.Now())
	return s
}

func (s *Screen) SetJobs(jobs []Job, now time.Time) {
	s.jobs = append(s.jobs[:0], jobs...)
	s.rebuild(now)
}

func (s *Screen) Refresh(now time.Time) bool {
	if (s.nextExpiry.IsZero() || !now.After(s.nextExpiry)) && (!s.running || now.Unix() == s.lastNow.Unix()) {
		return false
	}
	s.rebuild(now)
	return true
}

// NeedsActivityArc reports whether layout gave a running job a visible glyph.
func (s *Screen) NeedsActivityArc() bool { return s.animating }

func (s *Screen) VisibleCount() int { return s.visible }

func (s *Screen) ActionFocused(id ui.ComponentID) bool {
	_, ok := s.controls[id]
	return ok
}

func (s *Screen) NeedsAttention() bool {
	return s.attention
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	for _, control := range s.controls {
		control.setTokens(tokens)
	}
}

// SetRuleBleed records the deck row geometry supplied by the parent PanelDeck
// so the title rule can span the deck row edge to edge.
func (s *Screen) SetRuleBleed(offsetX, width int) { s.ruleBleedX, s.ruleBleedW = offsetX, width }

func (s *Screen) Children() []ui.Component { return s.children }
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: s.PreferredHeight(cs.MaxW, cs.MaxH)})
}

var _ ui.PreferredHeightComponent = (*Screen)(nil)

func (s *Screen) PreferredHeight(_, maxHeight int) int {
	if s.visible == 0 {
		return 0
	}
	// title + rule + header + rule + rows (the deck adds its own chrome).
	return min(maxHeight, 4+s.visible)
}
func (s *Screen) Layout(ctx ui.LayoutContext, rect ui.Rect) {
	s.SetBounds(rect)
	jobs := s.rows[:min(len(s.rows), max(0, rect.H-4))]
	s.statuses = s.statuses[:0]
	for _, job := range jobs {
		s.statuses = append(s.statuses, describeStatus(job))
	}
	s.columns = s.columnWidths(rect.W, jobs)
	s.children = s.children[:0]
	s.buttonJobs = make(map[ui.ComponentID]Job)
	controls := make(map[ui.ComponentID]*rowControl)
	place := func(kind, column, row int, job Job, r ui.Rect, label string) {
		if r.W <= 0 {
			return
		}
		id := ui.NewID(ScreenID, controlName(kind)+"/"+job.OwnerID+"/"+job.Handle+"/"+fmt.Sprint(job.PID)+"/"+fmt.Sprint(job.StartedAt.UnixNano()))
		control := s.controls[id]
		if control == nil {
			control = newRowControl(id, kind)
		}
		control.row, control.column, control.job = row, column, job
		control.setLabel(label)
		control.setTokens(s.tokens)
		control.Layout(ctx, r)
		controls[id] = control
		s.buttonJobs[id] = job
		s.children = append(s.children, control)
	}
	cols := s.columns
	s.animating = false
	var rules map[string]*ui.ProgressRule
	for index, job := range jobs {
		y := 4 + index
		s.animating = s.animating || job.Status == "running" && cols.statusW >= cols.glyphW
		if cols.barW > 0 && hasBar(job) {
			if rules == nil {
				rules = make(map[string]*ui.ProgressRule)
			}
			bar := s.rules[job.TaskID]
			if bar == nil {
				bar = ui.NewProgressRule(ui.NewID(ScreenID, "progress/"+job.TaskID), s.tokens)
			}
			bar.SetTokens(s.tokens)
			bar.SetActive(true)
			bar.SetMeasurement(job.Progress.Current, job.Progress.Total)
			bar.Layout(ctx, ui.Rect{X: cols.barX, Y: y, W: cols.barW, H: 1})
			rules[job.TaskID] = bar
		}
		// The link's one-cell padding sits in the column gap so its text
		// aligns with the JOB header.
		if text := s.commandText(job); text != "" && cols.commandW > 0 {
			w := min(rendering.VisibleWidth(text)+2, cols.commandW+2)
			place(controlReveal, columnCommand, index, job, ui.Rect{X: max(0, cols.commandX-1), Y: y, W: w, H: 1}, text)
		}
		if cols.outputW > 0 {
			place(controlOutput, columnOutput, index, job, ui.Rect{X: cols.outputX, Y: y, W: cols.outputW, H: 1}, outputLabel)
		}
		if job.Status == "running" && cols.terminateW > 0 {
			label := terminateLabel
			if cols.terminateW < terminateButtonW {
				label = string(ui.FirstRune(s.tokens.Glyphs().Close, '✕'))
			}
			place(controlTerminate, columnTerminate, index, job, ui.Rect{X: cols.terminateX, Y: y, W: cols.terminateW, H: 1}, label)
		}
	}
	s.controls = controls
	s.rules = rules
}

// hasBar requires measured work. Indeterminate activity has only the arc.
func hasBar(job Job) bool { return job.Status == "running" && job.Progress.Total > 0 }

// commandText is the single linked label for a shell command or capability.
func (s *Screen) commandText(job Job) string {
	return rendering.TruncateVisual(rendering.SanitizeForTerminal(job.Command), s.columns.commandW, "…")
}

func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() || s.visible == 0 {
		return
	}
	bg := s.tokens.Color(ui.ColorBg)
	divider := s.tokens.Color(ui.ColorPopupDivider)
	muted := s.tokens.Color(ui.ColorMuted)
	fg := s.tokens.Color(ui.ColorFg)
	ui.FillRect(buf, originX, originY, r, cell.BlankCell(bg))

	x, y := originX+r.X, originY+r.Y
	hBorder := string(ui.FirstRune(ui.DefaultGlyphsFromTokens(s.tokens).BorderH, '─'))

	// Title, bold, truncated to the deck row.
	buf.WriteString(x, y, rendering.TruncateLine("Background jobs", r.W), fg, bg, cell.AttrBold)

	// Full-bleed rule under the title (same mechanism as the settings picker).
	divX, divW := x, r.W
	if s.ruleBleedW-s.ruleBleedX > r.W {
		divX, divW = x-s.ruleBleedX, s.ruleBleedW
	}
	if r.H > 1 {
		buf.WriteString(divX, y+1, strings.Repeat(hBorder, max(0, divW)), divider, bg, 0)
	}

	cols := s.columns
	headerY := y + 2
	if r.H > 2 {
		s.paintHeader(buf, x, headerY, r.W, cols, muted, divider, bg, hBorder, r.H > 3)
	}

	arc := ui.ActivityArc(ctx.Frame)
	for i, status := range s.statuses {
		job := s.rows[i]
		s.paintRow(buf, x, headerY+2+i, cols, job, status.text, arc)
		if hasBar(job) {
			if bar := s.rules[job.TaskID]; bar != nil {
				bar.Paint(buf, x, y, ctx)
			}
		}
	}
}

// columnLayout describes the resolved tabular geometry for the current size.
type columnLayout struct {
	commandX, commandW, statusX, statusW     int
	barX, barW, glyphW                       int
	handleX, handleW, durationX, durationW   int
	outputX, outputW, terminateX, terminateW int
}

const (
	// barWidth is a progress bar's full length; shorter bars down to
	// barMinWidth still read as progress.
	barWidth, barMinWidth = 18, 6
	// jobFloor keeps a recognizable job name when space is short.
	jobFloor = 16
	// detailFloor is the error or phase detail a status earns before the
	// optional handle column is shown.
	detailFloor = 32
)

// columnWidths sizes every column from all visible rows together, so each
// column has one width and one position for the whole table. Output and Stop
// stay right-aligned. Space goes, in order, to a recognizable job name and
// each row's state, the elapsed time, some status detail, the handle, and
// then to the job name and status up to their full text; spare width trails
// the status column. PID and exit evidence belong to the expanded result.
func (s *Screen) columnWidths(total int, jobs []Job) columnLayout {
	gap := ui.TableMinGap
	var c columnLayout
	right := total
	reserve := func(w int) (int, int) {
		w = min(max(0, right), w)
		right -= w
		x := right
		right -= gap
		return x, w
	}
	var stop, tasks bool
	jobNat, leadMax, taskLead, statusNat := len("JOB"), 0, 0, 0
	handleW, timeW := len("HANDLE"), len("TIME")
	for i, job := range jobs {
		stop = stop || job.Status == "running"
		jobNat = max(jobNat, rendering.VisibleWidth(rendering.SanitizeForTerminal(job.Command)))
		status := s.statuses[i]
		leadMax = max(leadMax, status.leadWidth)
		statusNat = max(statusNat, rendering.VisibleWidth(status.text))
		if hasBar(job) {
			tasks = true
			taskLead = max(taskLead, status.leadWidth)
		}
		if job.Status != "running" {
			glyph, _, _ := s.statusGlyph(job, "")
			c.glyphW = max(c.glyphW, rendering.VisibleWidth(glyph))
		}
		handleW = max(handleW, rendering.VisibleWidth(job.Handle)+2)
		timeW = max(timeW, rendering.VisibleWidth(s.durationLabel(job)))
	}
	if stop {
		c.glyphW = max(c.glyphW, ui.ActivityArcWidth())
	}
	glyphSlot := c.glyphW + 1
	leadMax = max(len("STATUS"), leadMax+glyphSlot)
	statusNat = max(leadMax, statusNat+glyphSlot)
	if tasks {
		taskLead += glyphSlot
		statusNat = max(statusNat, taskLead+1+barWidth)
	}
	if stop {
		w := terminateButtonW
		if total < 72 {
			w = rendering.VisibleWidth(s.tokens.Glyphs().Close) + 4
		}
		c.terminateX, c.terminateW = reserve(w)
	}
	if right >= outputButtonW || !stop {
		c.outputX, c.outputW = reserve(outputButtonW)
	}
	c.commandX = min(1, max(0, right))
	// right is now the end of the free region. JOB and STATUS share it once
	// the gap between them is paid.
	flex := right - c.commandX - gap
	jobMin := min(jobNat, jobFloor)
	statusFloor := min(statusNat, leadMax+detailFloor)
	if flex-(timeW+gap) >= jobMin+leadMax {
		c.durationX, c.durationW = reserve(timeW)
		flex -= timeW + gap
	}
	if flex-(handleW+gap) >= jobMin+statusFloor {
		c.handleX, c.handleW = reserve(handleW)
		flex -= handleW + gap
	}
	flex = max(0, flex)
	switch {
	case flex < jobMin+leadMax:
		// Too narrow for both: keep a short job name, then the state.
		c.commandW = min(jobNat, max(min(jobNat, 8), flex-leadMax))
		c.statusW = max(0, flex-c.commandW)
	case flex >= jobNat+statusNat:
		c.commandW, c.statusW = jobNat, flex-jobNat
	default:
		// Share the shortfall: the shorter text is shown whole first.
		c.commandW = min(jobNat, max(jobMin, flex-statusNat, flex/2))
		c.statusW = flex - c.commandW
	}
	c.commandW = min(c.commandW, max(0, flex))
	c.statusX = c.commandX + c.commandW + gap
	if tasks && c.statusW >= taskLead+1+barMinWidth {
		c.barX = c.statusX + taskLead + 1
		c.barW = min(barWidth, c.statusW-taskLead-1)
	}
	return c
}

func (s *Screen) paintHeader(buf *surface.CellBuffer, x, y, total int, c columnLayout, muted, divider, bg cell.Color, border string, rule bool) {
	for _, h := range [...]struct {
		label string
		x, w  int
	}{{"JOB", c.commandX, c.commandW}, {"STATUS", c.statusX, c.statusW}, {"HANDLE", c.handleX, c.handleW}, {"TIME", c.durationX, c.durationW}} {
		s.paintCell(buf, x+h.x, y, h.w, h.label, muted, bg, cell.AttrBold, ui.TableAlignLeft)
	}
	if rule {
		buf.WriteString(x, y+1, strings.Repeat(border, max(0, total)), divider, bg, 0)
	}
}

type rowStatus struct {
	text      string
	leadWidth int // state/phase width, without the glyph or failure detail
}

// describeStatus derives stable text when layout processes a new snapshot. The
// shared clock changes only the glyph. Output retains the full error evidence.
func describeStatus(job Job) rowStatus {
	var state, detail string
	switch job.Status {
	case "running":
		state = "Running"
		if job.Progress.Phase != "" {
			state = job.Progress.Phase
		}
		if job.Progress.Total > 0 {
			state = fmt.Sprintf("%3d%% · %s", job.Progress.Percent(), state)
		}
	case "completed":
		state = "Completed"
	case "killed":
		state = "Stopped"
	case "failed":
		state = "Failed"
		summary, _, _ := strings.Cut(strings.TrimSpace(job.Error), "\n")
		if summary = strings.TrimSpace(summary); summary != "" {
			first, size := utf8.DecodeRuneInString(summary)
			detail = string(unicode.ToUpper(first)) + summary[size:]
		}
	default:
		state = "Unknown"
	}
	state = rendering.SanitizeForTerminal(state)
	status := rowStatus{text: state, leadWidth: rendering.VisibleWidth(state)}
	if detail != "" {
		status.text += " · " + rendering.SanitizeForTerminal(detail)
	}
	return status
}

func (s *Screen) paintRow(buf *surface.CellBuffer, x, y int, c columnLayout, job Job, status, arc string) {
	bg := s.tokens.Color(ui.ColorBg)
	muted := s.tokens.Color(ui.ColorMuted)
	glyph, ink, attr := s.statusGlyph(job, arc)
	w := c.statusW
	if c.barW > 0 && hasBar(job) {
		w = c.barX - c.statusX - 1
	}
	glyphSlot := c.glyphW + 1
	s.paintCell(buf, x+c.statusX, y, min(w, c.glyphW), glyph, ink, bg, attr, ui.TableAlignLeft)
	s.paintCell(buf, x+c.statusX+glyphSlot, y, w-glyphSlot, status, ink, bg, attr, ui.TableAlignLeft)
	if c.handleW > 0 {
		s.paintCell(buf, x+c.handleX, y, c.handleW, "["+job.Handle+"]", muted, bg, 0, ui.TableAlignLeft)
	}
	if c.durationW > 0 {
		s.paintCell(buf, x+c.durationX, y, c.durationW, s.durationLabel(job), muted, bg, 0, ui.TableAlignRight)
	}
}

func (s *Screen) durationLabel(job Job) string {
	duration := job.Duration
	if job.Status == "running" && !job.StartedAt.IsZero() {
		duration = s.lastNow.Sub(job.StartedAt)
	}
	if duration <= 0 {
		return "0s"
	}
	return rendering.FormatThinkingDuration(duration)
}

func (s *Screen) statusGlyph(job Job, arc string) (string, cell.Color, cell.Attrs) {
	switch job.Status {
	case "running":
		return arc, s.tokens.Color(ui.ColorAccent2), cell.AttrBold
	case "completed":
		return "✓", s.tokens.Color(ui.ColorSuccess), cell.AttrBold
	case "failed":
		return "✗", s.tokens.Color(ui.ColorError), cell.AttrBold
	case "killed":
		return "!", s.tokens.Color(ui.ColorWarning), cell.AttrBold
	default:
		return "?", s.tokens.Color(ui.ColorMuted), 0
	}
}

func (s *Screen) paintCell(buf *surface.CellBuffer, x, y, width int, text string, fg, bg cell.Color, attr cell.Attrs, align ui.TableColumnAlign) {
	if width <= 0 {
		return
	}
	text = rendering.TruncateLine(text, width)
	tw := rendering.VisibleWidth(text)
	if tw > width {
		tw = width
	}
	var cx int
	switch align {
	case ui.TableAlignRight:
		cx = width - tw
	case ui.TableAlignCenter:
		cx = (width - tw) / 2
	}
	if cx < 0 {
		cx = 0
	}
	buf.WriteString(x+cx, y, text, fg, bg, attr)
}

type TerminateRequest struct {
	Target *Screen
	Job    Job
}

// RevealRequest asks the host to show the command's originating call.
type RevealRequest struct {
	Target *Screen
	Job    Job
}

// OutputRequest asks the host to open the command's live output window.
type OutputRequest struct {
	Target *Screen
	Job    Job
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if move, ok := action.Payload.(focusMove); ok && action.ID == actionFocusMove {
		return s.moveFocus(move)
	}
	control, ok := s.controls[ui.ComponentID(action.ID)]
	if !ok {
		return ui.EventResult{Action: &action}
	}
	job := s.buttonJobs[control.ID()]
	switch control.kind {
	case controlReveal:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionReveal, Payload: RevealRequest{Target: s, Job: job}}}
	case controlOutput:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionOutput, Payload: OutputRequest{Target: s, Job: job}}}
	case controlTerminate:
		if job.Status == "running" {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionTerminate, Payload: TerminateRequest{Target: s, Job: job}}}
		}
	}
	return ui.EventResult{Handled: true}
}

// moveFocus walks the row/column control grid: Left/Right within a row,
// Up/Down to the nearest control in the same column, Tab in reading order.
func (s *Screen) moveFocus(move focusMove) ui.EventResult {
	from := -1
	for index, child := range s.children {
		if child.ID() == move.from {
			from = index
			break
		}
	}
	if from < 0 || len(s.children) == 0 {
		return ui.EventResult{Handled: true}
	}
	current := s.children[from].(*rowControl)
	target := -1
	switch move.kind {
	case moveNext, movePrevious:
		step := 1
		if move.kind == movePrevious {
			step = -1
		}
		target = (from + step + len(s.children)) % len(s.children)
	case moveLeft, moveRight:
		step := 1
		if move.kind == moveLeft {
			step = -1
		}
		if next := from + step; next >= 0 && next < len(s.children) && s.children[next].(*rowControl).row == current.row {
			target = next
		}
	case moveUp, moveDown:
		row := current.row + 1
		if move.kind == moveUp {
			row = current.row - 1
		}
		best := -1
		for index, child := range s.children {
			control := child.(*rowControl)
			if control.row != row {
				continue
			}
			if best < 0 || abs(control.column-current.column) < abs(s.children[best].(*rowControl).column-current.column) {
				best = index
			}
		}
		target = best
	}
	if target < 0 {
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: s.children[target].ID()}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (s *Screen) Handle(_ ui.EventContext, event ui.Event) ui.EventResult {
	if !event.IsKey() {
		return ui.EventResult{}
	}
	if event.Key.Code == tea.KeyEsc {
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionBlur}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	if len(s.children) == 0 {
		return ui.EventResult{}
	}
	if event.Key.Code == tea.KeyUp || event.Key.Code == tea.KeyDown || event.Key.Code == tea.KeyTab {
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: s.children[0].ID()}}
	}
	return ui.EventResult{}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Background jobs", Value: fmt.Sprintf("%d visible", s.visible)}
}

func (s *Screen) rebuild(now time.Time) {
	s.rows = s.visibleJobs(now)
	s.visible = len(s.rows)
	s.lastNow = now
	s.attention, s.running = false, false
	s.nextExpiry = time.Time{}
	for _, job := range s.rows {
		if job.Status == "running" {
			s.running = true
		} else if !job.EndedAt.IsZero() {
			expires := job.EndedAt.Add(completedRetention)
			if s.nextExpiry.IsZero() || expires.Before(s.nextExpiry) {
				s.nextExpiry = expires
			}
		}
		if job.Status == "failed" || job.Status == "killed" {
			s.attention = true
		}
	}
}

func (s *Screen) visibleJobs(now time.Time) []Job {
	visible := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		if job.Status != "running" && !job.EndedAt.IsZero() && now.Sub(job.EndedAt) > completedRetention {
			continue
		}
		visible = append(visible, job)
	}
	sort.Slice(visible, func(i, j int) bool {
		if visible[i].StartedAt.Equal(visible[j].StartedAt) {
			return visible[i].Handle < visible[j].Handle
		}
		return visible[i].StartedAt.Before(visible[j].StartedAt)
	})
	return visible
}

var _ ui.Component = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)

const actionFocusMove = "backgroundjobs.focus.move"

type moveKind uint8

const (
	moveNext moveKind = iota
	movePrevious
	moveLeft
	moveRight
	moveUp
	moveDown
)

type focusMove struct {
	from ui.ComponentID
	kind moveKind
}

const (
	controlReveal = iota
	controlOutput
	controlTerminate
)

func controlName(kind int) string {
	switch kind {
	case controlReveal:
		return "command"
	case controlOutput:
		return "output"
	default:
		return "terminate"
	}
}

// rowControl is one native control in a job row: the command link, Output or
// Terminate. It adds grid navigation and the row's command identity to the
// shared Button/ActionLink behavior.
type rowControl struct {
	ui.Component
	kind   int
	row    int
	column int
	job    Job
	button *ui.Button
	link   *ui.ActionLink
}

func newRowControl(id ui.ComponentID, kind int) *rowControl {
	control := &rowControl{kind: kind}
	if kind == controlReveal {
		control.link = ui.NewActionLink(id, "", string(id))
		control.link.SetVariant(ui.VariantSecondary)
		control.Component = control.link
		return control
	}
	control.button = ui.NewButton(id, "", string(id))
	control.button.SetVariant(ui.VariantSecondary)
	control.Component = control.button
	return control
}

func (c *rowControl) setLabel(label string) {
	if c.link != nil {
		c.link.SetLabel(label)
	} else {
		c.button.SetLabel(label)
	}
}

func (c *rowControl) setTokens(tokens *ui.TokenSet) {
	if c.link != nil {
		c.link.SetTokens(tokens)
		return
	}
	c.button.SetTokens(tokens)
	c.button.SetSurfaceBackground(tokens.Color(ui.ColorBg))
}

func (c *rowControl) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() {
		kind, ok := moveKind(0), true
		switch event.Key.Code {
		case tea.KeyUp:
			kind = moveUp
		case tea.KeyDown:
			kind = moveDown
		case tea.KeyLeft:
			kind = moveLeft
		case tea.KeyRight:
			kind = moveRight
		case tea.KeyTab:
			kind = moveNext
			if event.Key.Mod.Contains(tea.ModShift) {
				kind = movePrevious
			}
		default:
			ok = false
		}
		if ok {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: actionFocusMove, Payload: focusMove{from: c.ID(), kind: kind}}}
		}
	}
	return c.Component.Handle(ctx, event)
}

func (c *rowControl) base() *ui.Button {
	if c.link != nil {
		return &c.link.Button
	}
	return c.button
}

// ControlActions and HandleControl keep the shared semantic-control contract
// of the wrapped Button/ActionLink.
func (c *rowControl) ControlActions() []control.Descriptor { return c.base().ControlActions() }
func (c *rowControl) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	return c.base().HandleControl(ctx, op)
}

func (c *rowControl) Semantics() ui.Semantics {
	semantics := c.Component.Semantics()
	switch c.kind {
	case controlReveal:
		semantics.Label = "Show " + c.job.Handle + " in the conversation"
		semantics.Description = c.job.Command
	case controlOutput:
		semantics.Label = "Show output of " + c.job.Handle
		semantics.Description = "Opens the live output of this background command"
	default:
		semantics.Label = terminateLabel + " " + c.job.Handle
		semantics.Description = "Stops only this background command"
	}
	return semantics
}
