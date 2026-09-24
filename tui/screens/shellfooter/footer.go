package shellfooter

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const ScreenID ui.ComponentID = "shell/footer"

// pillPad keeps each label off the edge of its background fill.
const pillPad = " "

type Segment struct {
	Text string
	// Role is the ink for plain text. Pills choose contrasting ink for their fill.
	Role ui.SemanticColor
	// Pill paints a padded control with a vivid, theme-derived background.
	// FillRole supplies its hue; active controls keep that hue with added emphasis.
	FillRole  ui.SemanticColor
	Pill      bool
	Active    bool
	Attention bool
	Action    string
	TargetID  string
	Priority  int
}

// Projection is the footer's two groups. A group is either a pill strip (every
// segment a Pill) or a plain text row — the separator between segments follows
// the group's kind, so the two never mix inside one group: pills abut their
// neighbours and plain segments keep the dotted separator.
type Projection struct {
	Left  []Segment
	Right []Segment
}

type ActionMsg struct {
	Action   string
	TargetID string
}

type segmentHit struct {
	start, end     int
	action, target string
}

type Screen struct {
	ui.Base
	tokens     *ui.TokenSet
	projection Projection
	hits       []segmentHit
}

func New(th *theme.Theme, _ *ui.ActionRegistry) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	return &Screen{Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th)}
}

func (s *Screen) Children() []ui.Component            { return nil }
func (s *Screen) SetProjection(projection Projection) { s.projection = projection }
func (s *Screen) SetTokens(tokens *ui.TokenSet)       { s.tokens = tokens }

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: 2})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) { s.SetBounds(r) }

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() || buf == nil {
		return
	}
	bg := s.tokens.Color(ui.ColorBg)
	divider := s.tokens.Color(ui.ColorBorder)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	for x := 0; x < r.W; x++ {
		buf.Set(ox+r.X+x, oy+r.Y, cell.NewCell(ui.FirstRune(s.tokens.Glyphs().BorderH, '-'), divider, bg, 0))
	}
	leftRail, rightRail := ui.ShellContentRail(s.tokens, r.W)
	innerW := rightRail - leftRail
	s.hits = s.hits[:0]
	// The labeled workspace-panel controls are stable navigation, so the right
	// group gets its complete width first. Contextual hints use the remainder
	// and may collapse while momentary feedback is visible.
	rightSeparator := groupSeparator(s.projection.Right)
	rightBudget := innerW
	if !hasPills(s.projection.Right) {
		rightBudget = max(8, innerW/2)
	}
	right := fitSegments(s.projection.Right, rightBudget, rightSeparator)
	rightW := paintedGroupWidth(right, rightSeparator)
	left := fitSegments(s.projection.Left, max(0, innerW-rightW-2), " · ")
	s.paintGroup(buf, ox+r.X+leftRail, oy+r.Y+1, left, " · ", leftRail)
	// The left budget already reserves separation when hints fit. Do not reserve
	// a phantom gap when they collapse: a full-width strip must stay on the rail.
	rightX := rightRail - rightW
	s.paintGroup(buf, ox+r.X+rightX, oy+r.Y+1, right, rightSeparator, rightX)
}

// paintedGroupWidth measures the columns paintGroup writes for a group. A pill
// strip drops its trailing pad on the last pill so the strip's label and its
// fill both end exactly on the shared rail instead of a column short of it.
func paintedGroupWidth(segments []Segment, separator string) int {
	width := segmentsWidth(segments, separator)
	if len(segments) > 0 && segments[len(segments)-1].Pill && width > 0 {
		width--
	}
	return width
}

// groupSeparator keeps the pill strip contiguous: color changes are the only
// boundaries. Plain guidance segments keep their dotted separator.
func groupSeparator(segments []Segment) string {
	if hasPills(segments) {
		return ""
	}
	return " · "
}

func (s *Screen) paintGroup(buf *surface.CellBuffer, x, y int, segments []Segment, separator string, localX int) {
	for index, segment := range segments {
		if index > 0 {
			buf.WriteString(x, y, separator, s.tokens.Color(ui.ColorMuted), s.tokens.Color(ui.ColorBg), 0)
			width := rendering.VisibleWidth(separator)
			x, localX = x+width, localX+width
		}
		start := localX
		text := segment.Text
		fg, bg := s.tokens.Color(segment.Role), s.tokens.Color(ui.ColorBg)
		attr := cell.Attrs(0)
		leftPad, rightPad := "", ""
		if segment.Pill {
			leftPad, rightPad = pillPad, pillPad
			if index == len(segments)-1 {
				// The strip's trailing edge is the rail itself: its last label
				// ends on the shared rail instead of one padded column inside it.
				rightPad = ""
			}
			fg, bg = s.tokens.StatusStripColors(segment.FillRole, segment.Active, segment.Attention)
			if segment.Active {
				attr = cell.AttrBold
			}
		}
		buf.WriteString(x, y, leftPad+text+rightPad, fg, bg, attr)
		width := rendering.VisibleWidth(leftPad + text + rightPad)
		x, localX = x+width, localX+width
		if segment.Action != "" {
			s.hits = append(s.hits, segmentHit{start: start, end: localX, action: segment.Action, target: segment.TargetID})
		}
	}
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind != ui.EventMouseClick || evt.MouseClick.Button != tea.MouseLeft || ctx.LocalY != 1 {
		return ui.EventResult{}
	}
	for _, hit := range s.hits {
		if ctx.LocalX >= hit.start && ctx.LocalX < hit.end {
			msg := ActionMsg{Action: hit.action, TargetID: hit.target}
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return msg }}
		}
	}
	return ui.EventResult{}
}

func (s *Screen) Semantics() ui.Semantics {
	parts := make([]string, 0, len(s.projection.Left)+len(s.projection.Right))
	for _, segment := range append(append([]Segment(nil), s.projection.Left...), s.projection.Right...) {
		parts = append(parts, segment.Text)
	}
	return ui.Semantics{Role: ui.RoleGroup, Label: strings.Join(parts, " ")}
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
	sort.SliceStable(indices, func(i, j int) bool { return segments[indices[i]].Priority < segments[indices[j]].Priority })
	kept := make([]bool, len(segments))
	used, count := 0, 0
	for _, index := range indices {
		needed := rendering.VisibleWidth(segments[index].Text)
		if segments[index].Pill {
			// Conservative: a strip's last pill drops its trailing pad, so a kept
			// set paints up to one column narrower than measured here.
			needed += 2
		}
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

func hasPills(segments []Segment) bool {
	for _, segment := range segments {
		if segment.Pill {
			return true
		}
	}
	return false
}

func segmentsWidth(segments []Segment, separator string) int {
	width := 0
	for index, segment := range segments {
		if index > 0 {
			width += rendering.VisibleWidth(separator)
		}
		width += rendering.VisibleWidth(segment.Text)
		if segment.Pill {
			width += 2
		}
	}
	return width
}
