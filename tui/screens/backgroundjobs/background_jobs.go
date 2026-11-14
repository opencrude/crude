// Package backgroundjobs provides the session background-command detail panel.
package backgroundjobs

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
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
	terminateLabel = "Terminate"
	// Bracketed button widths: "[ " + label + " ]".
	outputButtonW    = 10
	terminateButtonW = 13
)

// Control columns inside one job row, left to right.
const (
	columnCommand = iota
	columnOutput
	columnTerminate
)

const completedRetention = 15 * time.Second

type Job struct {
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
	ruleBleedX int
	ruleBleedW int
	rows       []Job
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
	visible := s.visibleJobs(now)
	running := false
	for _, job := range visible {
		if job.Status == "running" {
			running = true
			break
		}
	}
	if len(visible) == s.visible && (!running || now.Unix() == s.lastNow.Unix()) {
		return false
	}
	s.rebuild(now)
	return true
}

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
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: s.PreferredHeight(cs.MaxH)})
}
func (s *Screen) PreferredHeight(maxHeight int) int {
	if s.visible == 0 {
		return 0
	}
	// title + rule + header + rule + rows (the deck adds its own chrome).
	return min(maxHeight, 4+s.visible)
}
func (s *Screen) Layout(ctx ui.LayoutContext, rect ui.Rect) {
	s.SetBounds(rect)
	s.columns = s.columnWidths(rect.W)
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
	for index, job := range s.rows {
		y := 4 + index
		if y >= rect.H {
			break
		}
		// The command reads as a link to its call in the conversation. The
		// link's one-cell padding sits in the column gap so the text stays
		// aligned with the COMMAND header.
		if text := s.commandText(job); text != "" && cols.commandW > 0 {
			w := min(rendering.VisibleWidth(text)+2, cols.commandW+1)
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
}

// commandText is the command cell's text: the whole command when it fits,
// leaving room for an error summary on failed rows.
func (s *Screen) commandText(job Job) string {
	w := s.columns.commandW
	if job.Error != "" && (job.Status == "failed" || job.Status == "killed") {
		return rendering.TruncateVisual(job.Command, max(1, w-1), "…")
	}
	return rendering.TruncateLine(job.Command, w)
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

	for index, job := range s.rows {
		if index+4 >= r.H {
			break
		}
		s.paintRow(buf, x, headerY+2+index, cols, job, ctx.Frame)
	}
}

// columnLayout describes the resolved tabular geometry for the current size.
type columnLayout struct {
	statusX, statusW int
	handleX, handleW int
	commandX         int
	commandW         int
	durationX        int
	durationW        int
	pidX, pidW       int
	exitX, exitW     int
	actionX, actionW int
	// Output and Terminate share the action column; Terminate appears only
	// on running rows but its column is kept so Output buttons line up.
	outputX, outputW       int
	terminateX, terminateW int
	showDuration           bool
	showEnd                bool
}

func (s *Screen) columnWidths(total int) columnLayout {
	gap := ui.TableMinGap
	cols := columnLayout{statusW: 6, handleW: 6, durationW: 8, pidW: 3, exitW: 4}
	commandWidth := 7
	for _, job := range s.rows {
		glyph, _, _ := s.statusGlyph(job)
		cols.statusW = max(cols.statusW, rendering.VisibleWidth(glyph+" "+job.Status))
		cols.handleW = max(cols.handleW, rendering.VisibleWidth("["+job.Handle+"]"))
		cols.durationW = max(cols.durationW, rendering.VisibleWidth(s.durationLabel(job)))
		cols.pidW = max(cols.pidW, rendering.VisibleWidth(fmt.Sprint(job.PID)))
		cols.exitW = max(cols.exitW, rendering.VisibleWidth(fmt.Sprint(job.ExitCode)))
		commandWidth = max(commandWidth, rendering.VisibleWidth(job.Command))
		cols.outputW = outputButtonW
		if job.Status == "running" {
			cols.terminateW = terminateButtonW
		}
	}
	cols.actionW = cols.outputW
	if cols.terminateW > 0 {
		cols.actionW += 1 + cols.terminateW
	}
	baseWidth := cols.statusW + cols.handleW + commandWidth + 2*gap
	if cols.actionW > 0 {
		baseWidth += cols.actionW + gap
	}
	cols.showDuration = total >= baseWidth+cols.durationW+gap
	cols.showEnd = cols.showDuration && total >= baseWidth+cols.durationW+cols.pidW+cols.exitW+3*gap
	if cols.terminateW > 0 && total < baseWidth {
		// Narrow decks keep both decisions; Terminate becomes its glyph.
		compact := rendering.VisibleWidth(s.tokens.Glyphs().Close) + 4
		cols.actionW -= cols.terminateW - compact
		cols.terminateW = compact
	}
	if cols.actionW > max(0, total) {
		// Too narrow for both: stopping a running command outranks Output.
		if cols.terminateW > 0 {
			cols.outputW, cols.actionW = 0, cols.terminateW
		}
		cols.actionW = min(cols.actionW, max(0, total))
		cols.terminateW = min(cols.terminateW, cols.actionW)
		cols.outputW = min(cols.outputW, cols.actionW)
	}
	right := max(0, total)
	reserve := func(width int) (int, int) {
		width = min(width, right)
		right -= width
		position := right
		right = max(0, right-gap)
		return position, width
	}
	if cols.actionW > 0 {
		cols.actionX, cols.actionW = reserve(cols.actionW)
		cols.outputX = cols.actionX
		cols.terminateX = cols.actionX
		if cols.outputW > 0 {
			cols.outputW = min(cols.outputW, cols.actionW)
			cols.terminateX = cols.actionX + cols.outputW + 1
		}
		cols.terminateW = max(0, min(cols.terminateW, cols.actionX+cols.actionW-cols.terminateX))
	}
	if cols.showEnd {
		cols.exitX, cols.exitW = reserve(cols.exitW)
		cols.pidX, cols.pidW = reserve(cols.pidW)
	}
	if cols.showDuration {
		cols.durationX, cols.durationW = reserve(cols.durationW)
	}
	// On narrow decks, keep the two actions and readable command text. The
	// status glyph is sufficient; the handle remains in every control's
	// accessible label instead of squeezing the command to a few cells.
	if right < cols.statusW+cols.handleW+min(commandWidth, 20)+2*gap {
		cols.statusW, cols.handleW = min(1, right), 0
		cols.commandX = min(right, cols.statusW+gap)
		cols.commandW = max(0, right-cols.commandX)
		return cols
	}
	cols.statusW = min(cols.statusW, max(0, right-6-7-2*gap))
	if cols.statusW == 0 {
		cols.statusW = min(1, right)
	}
	cols.handleX = min(right, cols.statusW+gap)
	cols.handleW = min(cols.handleW, max(0, right-cols.handleX-gap-7))
	cols.commandX = min(right, cols.handleX+cols.handleW+gap)
	cols.commandW = max(0, right-cols.commandX)
	return cols
}

func (s *Screen) paintHeader(buf *surface.CellBuffer, x, y, total int, cols columnLayout, muted, divider, bg cell.Color, hBorder string, rule bool) {
	headers := []struct {
		label string
		at    int
		width int
	}{
		{"STATUS", cols.statusX, cols.statusW},
		{"HANDLE", cols.handleX, cols.handleW},
		{"COMMAND", cols.commandX, cols.commandW},
	}
	if cols.showDuration {
		headers = append(headers, struct {
			label string
			at    int
			width int
		}{"DURATION", cols.durationX, cols.durationW})
	}
	if cols.showEnd {
		headers = append(headers,
			struct {
				label string
				at    int
				width int
			}{"PID", cols.pidX, cols.pidW},
			struct {
				label string
				at    int
				width int
			}{"EXIT", cols.exitX, cols.exitW},
		)
	}
	for _, header := range headers {
		s.paintCell(buf, x+header.at, y, header.width, header.label, muted, bg, cell.AttrBold, ui.TableAlignLeft)
	}
	if rule {
		buf.WriteString(x, y+1, strings.Repeat(hBorder, max(0, total)), divider, bg, 0)
	}
}

func (s *Screen) paintRow(buf *surface.CellBuffer, x, y int, cols columnLayout, job Job, frame int) {
	bg := s.tokens.Color(ui.ColorBg)
	status := job.Status
	if status == "" {
		status = "unknown"
	}
	glyph, glyphColor, attr := s.statusGlyph(job)
	if job.Status == "running" {
		glyph = ui.ActivityDot(frame)
	}
	statusCell := glyph + " " + strings.ToUpper(status)
	s.paintCell(buf, x+cols.statusX, y, cols.statusW, statusCell, glyphColor, bg, attr, ui.TableAlignLeft)

	handle := "[" + job.Handle + "]"
	s.paintCell(buf, x+cols.handleX, y, cols.handleW, handle, s.tokens.Color(ui.ColorAccent2), bg, cell.AttrBold, ui.TableAlignLeft)

	commandCell := job.Command
	commandFg := s.tokens.Color(ui.ColorFg)
	if job.Error != "" && (job.Status == "failed" || job.Status == "killed") {
		commandFg = s.tokens.Color(ui.ColorMuted)
		commandCell = rendering.TruncateVisual(commandCell, max(1, cols.commandW-1), "…")
		remaining := cols.commandW - rendering.VisibleWidth(commandCell) - 1
		if remaining > 1 {
			if errText := rendering.TruncateVisual(job.Error, remaining-1, "…"); errText != "" {
				commandCell += " " + errText
			}
		}
	}
	s.paintCell(buf, x+cols.commandX, y, cols.commandW, commandCell, commandFg, bg, 0, ui.TableAlignLeft)

	if cols.showDuration {
		s.paintCell(buf, x+cols.durationX, y, cols.durationW, s.durationLabel(job), s.tokens.Color(ui.ColorMuted), bg, 0, ui.TableAlignRight)
	}

	if cols.showEnd {
		pid := "—"
		if job.PID > 0 {
			pid = fmt.Sprintf("%d", job.PID)
		}
		s.paintCell(buf, x+cols.pidX, y, cols.pidW, pid, s.tokens.Color(ui.ColorMuted), bg, 0, ui.TableAlignRight)
		exit := "—"
		if job.Status == "completed" || job.Status == "failed" {
			exit = fmt.Sprintf("%d", job.ExitCode)
		}
		exitFg := s.tokens.Color(ui.ColorMuted)
		if job.Status == "failed" || job.Status == "killed" {
			exitFg = s.tokens.Color(ui.ColorError)
		}
		s.paintCell(buf, x+cols.exitX, y, cols.exitW, exit, exitFg, bg, 0, ui.TableAlignRight)
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

func (s *Screen) statusGlyph(job Job) (string, cell.Color, cell.Attrs) {
	switch job.Status {
	case "running":
		return ui.ActivityDot(0), s.tokens.Color(ui.ColorAccent2), cell.AttrBold
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
	s.attention = false
	for _, job := range s.rows {
		if job.Status == "failed" || job.Status == "killed" {
			s.attention = true
			break
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
		semantics.Label = "Terminate " + c.job.Handle
		semantics.Description = "Stops only this background command"
	}
	return semantics
}
