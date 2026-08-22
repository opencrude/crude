// Package backgroundjobs provides the passive background-command detail panel.
package backgroundjobs

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID ui.ComponentID = "session-background-jobs"
)

const completedRetention = 15 * time.Second

type Job struct {
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

// Screen is the passive tabular background-jobs deck. It paints its own
// title/rule, a sticky column header, and one row per job (status glyph,
// handle, command, duration, PID, exit) using only semantic theme roles —
// no marker glyphs, no raw colors. It never claims focus and ignores input.
type Screen struct {
	ui.Base
	tokens     *ui.TokenSet
	jobs       []Job
	visible    int
	attention  bool
	lastNow    time.Time
	ruleBleedX int
	ruleBleedW int
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th)}
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

func (s *Screen) NeedsAttention() bool {
	return s.attention
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
}

// SetRuleBleed records the deck row geometry supplied by the parent PanelDeck
// so the title rule can span the deck row edge to edge.
func (s *Screen) SetRuleBleed(offsetX, width int) { s.ruleBleedX, s.ruleBleedW = offsetX, width }

func (s *Screen) Children() []ui.Component { return nil }
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
func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) { s.SetBounds(r) }

func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
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
	buf.WriteString(divX, y+1, strings.Repeat(hBorder, max(0, divW)), divider, bg, 0)

	cols := s.columnWidths(r.W)
	headerY := y + 2
	s.paintHeader(buf, x, headerY, r.W, cols, muted, divider, bg, hBorder)

	rows := s.visibleJobs(s.lastNow)
	for index, job := range rows {
		s.paintRow(buf, x, headerY+2+index, cols, job)
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
	showDuration     bool // DURATION column fits at this width
	showEnd          bool // PID/EXIT columns fit at this width
}

func (s *Screen) columnWidths(total int) columnLayout {
	cl := columnLayout{statusW: 11, handleW: 12, durationW: 9}
	cl.showDuration = total >= 45
	cl.showEnd = total >= 60
	cl.handleX = cl.statusW + 1
	cl.commandX = cl.handleX + cl.handleW + 1
	right := total
	if cl.showEnd {
		cl.exitW, cl.pidW = 5, 5
		right -= cl.exitW
		cl.exitX = right
		right -= cl.pidW
		cl.pidX = right
	}
	if cl.showDuration {
		right -= cl.durationW
		cl.durationX = right
	}
	cl.commandW = right - cl.commandX
	if cl.commandW < 8 {
		cl.commandW = 8
	}
	return cl
}

func (s *Screen) paintHeader(buf *surface.CellBuffer, x, y, total int, cols columnLayout, muted, divider, bg cell.Color, hBorder string) {
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
		buf.WriteString(x+header.at, y, rendering.TruncateLine(header.label, header.width), muted, bg, cell.AttrBold)
	}
	buf.WriteString(x, y+1, strings.Repeat(hBorder, max(0, total)), divider, bg, 0)
}

func (s *Screen) paintRow(buf *surface.CellBuffer, x, y int, cols columnLayout, job Job) {
	bg := s.tokens.Color(ui.ColorBg)
	status := job.Status
	if status == "" {
		status = "unknown"
	}
	glyph, glyphColor, attr := s.statusGlyph(job)
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

	duration := job.Duration
	if job.Status == "running" && !job.StartedAt.IsZero() {
		duration = s.lastNow.Sub(job.StartedAt)
	}
	if cols.showDuration {
		s.paintCell(buf, x+cols.durationX, y, cols.durationW, duration.Truncate(time.Second).String(), s.tokens.Color(ui.ColorMuted), bg, 0, ui.TableAlignRight)
	}

	if cols.showEnd {
		pid := "—"
		if job.PID > 0 {
			pid = fmt.Sprintf("%d", job.PID)
		}
		s.paintCell(buf, x+cols.pidX, y, cols.pidW, pid, s.tokens.Color(ui.ColorMuted), bg, 0, ui.TableAlignRight)
		exit := "—"
		if job.Status != "running" {
			exit = fmt.Sprintf("%d", job.ExitCode)
		}
		exitFg := s.tokens.Color(ui.ColorMuted)
		if job.Status == "failed" || job.Status == "killed" {
			exitFg = s.tokens.Color(ui.ColorError)
		}
		s.paintCell(buf, x+cols.exitX, y, cols.exitW, exit, exitFg, bg, 0, ui.TableAlignRight)
	}
}

func (s *Screen) statusGlyph(job Job) (string, cell.Color, cell.Attrs) {
	switch job.Status {
	case "running":
		if s.lastNow.Unix()%2 == 0 {
			return "●", s.tokens.Color(ui.ColorAccent2), cell.AttrBold
		}
		return "○", s.tokens.Color(ui.ColorAccent2), cell.AttrBold
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

func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult { return ui.EventResult{} }
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Background jobs", Value: fmt.Sprintf("%d visible", s.visible)}
}

func (s *Screen) rebuild(now time.Time) {
	s.visible = len(s.visibleJobs(now))
	s.lastNow = now
	s.attention = false
	for _, job := range s.visibleJobs(now) {
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
