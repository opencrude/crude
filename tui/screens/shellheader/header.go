// Package shellheader provides the native persistent header chrome.
package shellheader

import (
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const ScreenID ui.ComponentID = "shellheader"

// Projection is the presentation data for one header paint.
type Projection struct {
	ModelName     string
	YOLO          bool
	StartTime     time.Time
	Usage         string // plain-text usage/readiness (from status UsageView)
	ViewLabel     string
	Width         int
	LeftSegments  []Segment
	RightSegments []Segment
}

type Segment struct {
	Text     string
	Role     ui.SemanticColor
	Action   string
	TargetID string
	Priority int
}

type ActionMsg struct {
	Action   string
	TargetID string
}

type segmentHit struct {
	start, end     int
	action, target string
}

type paintRun struct {
	text string
	fg   cell.Color
	attr cell.Attrs
}

// Screen paints the two-row header (content + separator) via Host.Paint.
type Screen struct {
	ui.Base
	th     *theme.Theme
	tokens *ui.TokenSet
	proj   Projection
	hits   []segmentHit
}

// nowFunc is a test seam for deterministic paint goldens (the header renders
// an elapsed clock). Production uses time.Now; tests freeze it.
var nowFunc = time.Now

// New creates a header screen.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	return &Screen{
		Base:   ui.NewBase(ScreenID),
		th:     th,
		tokens: ui.NewTokenSet(th),
	}
}

// SetTokens updates runtime theme tokens.
func (s *Screen) SetTokens(tokens *ui.TokenSet) { s.tokens = tokens }

// SetTheme replaces the theme used for spacing and colors.
func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.tokens = ui.NewTokenSet(th)
}

// SetProjection updates header content for the next paint.
func (s *Screen) SetProjection(p Projection) { s.proj = p }

func (s *Screen) Children() []ui.Component { return nil }

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	h := 2
	if cs.MaxH > 0 && cs.MaxH < h {
		h = cs.MaxH
	}
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: h})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) { s.SetBounds(r) }

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	b := s.Bounds()
	if b.Empty() || buf == nil {
		return
	}
	width := b.W
	if s.proj.Width > 0 {
		width = s.proj.Width
	}
	leftRail, rightRail := ui.ShellContentRail(s.tokens, width)
	innerW := rightRail - leftRail
	if innerW < 1 {
		innerW = 1
	}

	if len(s.proj.LeftSegments) > 0 || len(s.proj.RightSegments) > 0 {
		s.paintSegments(buf, ox+b.X+leftRail, oy+b.Y, innerW)
	} else {
		left, right := s.buildRuns(innerW)
		y := oy + b.Y
		x := ox + b.X + leftRail
		writeRuns(buf, x, y, left)
		rightW := runsWidth(right)
		writeRuns(buf, ox+b.X+leftRail+innerW-rightW, y, right)
	}

	// Separator row
	border := s.color(ui.ColorBorder)
	sepY := oy + b.Y + 1
	if b.H >= 2 {
		buf.WriteString(ox+b.X, sepY, strings.Repeat(string(ui.FirstRune(s.tokens.Glyphs().BorderH, '─')), max(0, b.W)), border, cell.NoColor, 0)
	}
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind != ui.EventMouseClick || evt.MouseClick.Button != tea.MouseLeft || ctx.LocalY != 0 {
		return ui.EventResult{}
	}
	left, _ := ui.ShellContentRail(s.tokens, s.Bounds().W)
	x := ctx.LocalX - left
	for _, hit := range s.hits {
		if x >= hit.start && x < hit.end {
			msg := ActionMsg{Action: hit.action, TargetID: hit.target}
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return msg }}
		}
	}
	return ui.EventResult{}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleLabel, Label: "Header"}
}

func (s *Screen) color(role ui.SemanticColor) cell.Color {
	if s.tokens != nil {
		return s.tokens.Color(role)
	}
	return cell.NoColor
}

func (s *Screen) paintSegments(buf *surface.CellBuffer, x, y, innerW int) {
	s.hits = s.hits[:0]
	leftBudget := max(10, innerW/3)
	left := fitSegments(s.proj.LeftSegments, leftBudget, "  ")
	leftW := segmentWidth(left, "  ")
	right := fitSegments(s.proj.RightSegments, max(0, innerW-leftW-2), " · ")
	rightW := segmentWidth(right, " · ")
	s.paintSegmentGroup(buf, x, y, left, "  ", 0)
	rightX := max(leftW+2, innerW-rightW)
	s.paintSegmentGroup(buf, x+rightX, y, right, " · ", rightX)
}

func (s *Screen) paintSegmentGroup(buf *surface.CellBuffer, x, y int, segments []Segment, separator string, localX int) {
	for index, segment := range segments {
		if index > 0 {
			buf.WriteString(x, y, separator, s.color(ui.ColorMuted), cell.NoColor, 0)
			width := rendering.VisibleWidth(separator)
			x, localX = x+width, localX+width
		}
		start := localX
		attrs := cell.Attrs(0)
		if segment.Role == ui.ColorAccent {
			attrs = cell.AttrBold
		}
		buf.WriteString(x, y, segment.Text, s.color(segment.Role), cell.NoColor, attrs)
		width := rendering.VisibleWidth(segment.Text)
		x, localX = x+width, localX+width
		if segment.Action != "" {
			s.hits = append(s.hits, segmentHit{start: start, end: localX, action: segment.Action, target: segment.TargetID})
		}
	}
}

func fitSegments(segments []Segment, width int, separator string) []Segment {
	if width <= 0 {
		return nil
	}
	indices := make([]int, 0, len(segments))
	for index, segment := range segments {
		if segment.Text != "" {
			indices = append(indices, index)
		}
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return segments[indices[i]].Priority < segments[indices[j]].Priority
	})
	kept := make([]bool, len(segments))
	used, count := 0, 0
	for _, index := range indices {
		needed := rendering.VisibleWidth(segments[index].Text)
		if count > 0 {
			needed += rendering.VisibleWidth(separator)
		}
		if used+needed <= width {
			kept[index], used, count = true, used+needed, count+1
		}
	}
	out := make([]Segment, 0, count)
	for index, segment := range segments {
		if kept[index] {
			out = append(out, segment)
		}
	}
	return out
}

func segmentWidth(segments []Segment, separator string) int {
	width := 0
	for index, segment := range segments {
		if index > 0 {
			width += rendering.VisibleWidth(separator)
		}
		width += rendering.VisibleWidth(segment.Text)
	}
	return width
}

func (s *Screen) buildRuns(innerW int) (left, right []paintRun) {
	muted := s.color(ui.ColorMuted)
	accent := s.color(ui.ColorAccent)
	errC := s.color(ui.ColorError)
	headerFG := accent
	if s.th != nil {
		headerFG = rendering.ThemeColorToCell(s.th.Colors.Accent)
	}

	modelBudget := max(1, (s.proj.Width-4)/3)
	if s.proj.Width <= 0 {
		modelBudget = max(1, (innerW-4)/3)
	}
	model := rendering.TruncateLine(s.proj.ModelName, modelBudget)

	left = []paintRun{{text: "crude", fg: headerFG, attr: cell.AttrBold}}
	if model != "" {
		left = append(left, paintRun{text: "  ", fg: muted}, paintRun{text: model, fg: muted})
	}
	if s.proj.ViewLabel != "" {
		left = append(left,
			paintRun{text: "  ", fg: muted},
			paintRun{text: "⟨" + s.proj.ViewLabel + "⟩", fg: accent, attr: cell.AttrBold},
		)
	}

	usage := s.proj.Usage
	start := s.proj.StartTime
	if start.IsZero() {
		start = nowFunc()
	}
	clock := nowFunc().Sub(start).Truncate(time.Second).String()

	appendRight := func(usageText string) []paintRun {
		var parts [][]paintRun
		if s.proj.YOLO {
			parts = append(parts, []paintRun{{text: "⚠ YOLO", fg: errC, attr: cell.AttrBold}})
		}
		if usageText != "" {
			parts = append(parts, []paintRun{{text: usageText, fg: muted}})
		}
		parts = append(parts, []paintRun{{text: clock, fg: muted}})
		var out []paintRun
		for i, p := range parts {
			if i > 0 {
				out = append(out, paintRun{text: "  ·  ", fg: muted})
			}
			out = append(out, p...)
		}
		return out
	}

	right = appendRight(usage)
	overflow := runsWidth(left) + runsWidth(right) - innerW
	if overflow > 0 && usage != "" {
		usage = rendering.TruncateLine(usage, max(0, rendering.VisibleWidth(usage)-overflow))
		right = appendRight(usage)
	}
	leftBudget := innerW - runsWidth(right)
	if runsWidth(left) > leftBudget {
		left = truncateRuns(left, max(0, leftBudget))
	}
	return left, right
}

func runsWidth(runs []paintRun) int {
	w := 0
	for _, r := range runs {
		w += rendering.VisibleWidth(r.text)
	}
	return w
}

func writeRuns(buf *surface.CellBuffer, x, y int, runs []paintRun) {
	for _, r := range runs {
		if r.text == "" {
			continue
		}
		buf.WriteString(x, y, r.text, r.fg, cell.NoColor, r.attr)
		x += rendering.VisibleWidth(r.text)
	}
}

func truncateRuns(runs []paintRun, maxW int) []paintRun {
	if maxW <= 0 {
		return nil
	}
	var out []paintRun
	used := 0
	for _, r := range runs {
		tw := rendering.VisibleWidth(r.text)
		if used+tw <= maxW {
			out = append(out, r)
			used += tw
			continue
		}
		remain := maxW - used
		if remain <= 0 {
			break
		}
		out = append(out, paintRun{
			text: rendering.TruncateLine(r.text, remain),
			fg:   r.fg,
			attr: r.attr,
		})
		break
	}
	return out
}

// PlainLine returns a plain-text one-line header for tests.
func PlainLine(p Projection) string {
	s := New(theme.DefaultDark())
	s.SetProjection(p)
	pad := s.th.Spacing.PadLR
	innerW := p.Width - pad*2
	if innerW < 1 {
		innerW = 1
	}
	left, right := s.buildRuns(innerW)
	var lb, rb strings.Builder
	for _, r := range left {
		lb.WriteString(r.text)
	}
	for _, r := range right {
		rb.WriteString(r.text)
	}
	spacer := innerW - rendering.VisibleWidth(lb.String()) - rendering.VisibleWidth(rb.String())
	if spacer < 0 {
		spacer = 0
	}
	return lb.String() + strings.Repeat(" ", spacer) + rb.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
