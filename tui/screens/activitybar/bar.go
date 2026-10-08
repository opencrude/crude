package activitybar

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// Width is the always-on left activity rail in cells:
//
//	col 0  outer shell gutter
//	col 1  active marker
//	col 2  gap
//	col 3  view icon
//	col 4  icon breathing room / wide-icon continuation
//	col 5  count on the band's top/bottom row (moves right for a wider icon)
//	col 6  trailing padding
//	col 7  vertical rule separating the rail from the panel
const Width = 8

const (
	outerGutterCol = 0
	markerCol      = 1
	iconCol        = 3
	badgeCol       = 5
	panelPadCol    = 5
	afterIconCol   = 6
	ruleCol        = 7
)

// RowStride is the height of one rail entry. Entries start at the rail's
// first row so the rail reads as a column that begins at the top, and the
// whole band is clickable (a one-row target is too small for a mouse).
//
// The band is odd-height so the glyph sits on its exact middle row: every
// highlight (hover background, active marker) covers the whole band, so a
// band always reads as a balanced block around its icon rather than a box
// hanging below a top-aligned glyph.
const RowStride = 3

// iconRow is the band-local row carrying the icon. Badges use the row above
// or below it, keeping their raised/lowered position without tiny script glyphs.
const iconRow = RowStride / 2

const ScreenID ui.ComponentID = "activitybar"

const (
	ActionSelect   = "activity.select"
	ActionSettings = "activity.settings"
	ActionOverflow = "activity.overflow"
)

// View is one activity-bar destination.
type View int

const (
	ViewFiles View = iota
	ViewAgents
	ViewSkills
	ViewMCP
)

func (view View) ID() string {
	switch view {
	case ViewAgents:
		return "agents"
	case ViewSkills:
		return "skills"
	case ViewMCP:
		return "mcp"
	default:
		return "files"
	}
}

func (v View) Label() string {
	switch v {
	case ViewAgents:
		return "Agents"
	case ViewSkills:
		return "Skills"
	case ViewMCP:
		return "MCP"
	default:
		return "Explorer"
	}
}

// Icon returns the rail glyph for the view. Nerd-font codepoints come from
// the same coverage the file tree already relies on; the fallbacks are BMP
// symbols with neutral east-asian width so column math is unaffected.
func (v View) Icon(nerdFont bool) string {
	if nerdFont {
		switch v {
		case ViewAgents:
			// nf-fa-android. nf-fa-robot (U+F544) is a down-arrow in Nerd
			// Fonts 2.x, where that cell is still Material Design Icons.
			return "\uf17b"
		case ViewSkills:
			return "\uf02d"
		case ViewMCP:
			return "\uf1e6"
		default:
			return "\uf07b" // nf-fa-folder
		}
	}
	switch v {
	case ViewAgents:
		return "◈"
	case ViewSkills:
		return "≡"
	case ViewMCP:
		return "⋈"
	default:
		return "▤"
	}
}

// Glyph is the non-nerd-font rail symbol. Kept for callers that only need a
// stable single-cell identity for a view.
func (v View) Glyph() string { return v.Icon(false) }

// SettingsIcon is the manage control pinned to the bottom of the rail.
// nf-fa-cogs is the denser two-cog glyph the Agents entry used to carry —
// a single cog (nf-fa-cog) reads as a one-column speck.
func SettingsIcon(nerdFont bool) string {
	if nerdFont {
		return "\uf085" // nf-fa-cogs
	}
	return "⚙"
}

var views = []View{ViewFiles, ViewAgents, ViewSkills, ViewMCP}

// Views returns the rail order.
func Views() []View { return append([]View(nil), views...) }

// Screen is the VS Code-style left activity rail.
type Screen struct {
	ui.Base
	th        *theme.Theme
	tokens    *ui.TokenSet
	active    View
	panelOpen bool
	hover     int
	nerdFont  bool
	badges    map[View]int
	order     []View
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{
		Base:      ui.NewBase(ScreenID),
		th:        th,
		tokens:    ui.NewTokenSet(th),
		hover:     -1,
		panelOpen: true,
		badges:    map[View]int{},
		order:     Views(),
	}
	s.applyTheme(th)
	return s
}

func (s *Screen) Active() View { return s.active }

func (s *Screen) SetActive(v View) { s.active = v }

func (screen *Screen) SetOrder(ids []string) {
	screen.order = nil
	for _, id := range ids {
		for _, view := range Views() {
			if view.ID() == id {
				screen.order = append(screen.order, view)
				break
			}
		}
	}
	screen.hover = -1
}

func (screen *Screen) ViewAt(localY int) (View, bool) {
	index := screen.hitTarget(localY)
	if index < 0 || index >= len(screen.order) {
		return ViewFiles, false
	}
	return screen.order[index], true
}

// SetPanelOpen reports whether the sidebar panel for the active view is
// currently mounted. A collapsed panel drops the accent marker so the rail
// shows the toggle state instead of claiming a panel is open.
func (s *Screen) SetPanelOpen(open bool) { s.panelOpen = open }

// PanelOpen reports the last state given to SetPanelOpen.
func (s *Screen) PanelOpen() bool { return s.panelOpen }

// SetBadge sets the pending-item count shown beside a view's icon. Counts
// above 9 collapse to "+" so the rail never widens.
func (s *Screen) SetBadge(v View, n int) {
	if s.badges == nil {
		s.badges = map[View]int{}
	}
	s.badges[v] = n
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) { s.tokens = tokens }

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
	s.applyTheme(th)
}

func (s *Screen) applyTheme(th *theme.Theme) {
	s.nerdFont = panelchrome.NerdFont(th)
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: Width, H: cs.MaxH})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) { s.SetBounds(r) }

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := s.Bounds()
	if buf == nil || s.tokens == nil || r.Empty() {
		return
	}
	bg := s.tokens.Color(ui.ColorBg)
	fg := s.tokens.Color(ui.ColorFg)
	accent := s.tokens.Color(ui.ColorAccent)
	muted := s.tokens.Color(ui.ColorMuted)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))

	left := ox + r.X
	top := oy + r.Y
	bottom := top + r.H
	for i, v := range s.order {
		if i >= s.visibleCount(r.H) {
			break
		}
		bandTop := top + i*RowStride
		if bandTop >= bottom {
			break
		}
		bandH := min(RowStride, bottom-bandTop)
		current := v == s.active
		hovered := i == s.hover && ctx.Hovered
		s.paintBand(buf, left, bandTop, bandH, bottom, v.Icon(s.nerdFont), badgeFor(v, s.badges[v]), current, hovered, bg, fg, accent, muted)
	}
	if count := s.visibleCount(r.H); count < len(s.order) && r.H >= RowStride*2 {
		s.paintBand(buf, left, top+count*RowStride, RowStride, bottom, "…", countBadge{}, false, s.hover == len(views)+1 && ctx.Hovered, bg, fg, accent, muted)
	}
	if localTop := settingsBandTop(r.H); localTop >= 0 {
		hovered := s.hover == settingsHitIndex() && ctx.Hovered
		s.paintBand(buf, left, top+localTop, RowStride, bottom, SettingsIcon(s.nerdFont), countBadge{}, false, hovered, bg, fg, accent, muted)
	}

	if r.W > ruleCol {
		panelchrome.PaintVRule(buf, left+ruleCol, top, r.H, s.tokens)
	}
}

func (s *Screen) paintBand(buf *surface.CellBuffer, left, bandTop, bandH, bottom int, icon string, badge countBadge, current, hovered bool, bg, fg, accent, muted cell.Color) {
	r := s.Bounds()
	rowBG := bg
	iconFG := muted
	if current || hovered {
		iconFG = fg
	}
	if hovered {
		rowBG = s.tokens.Color(ui.ColorHover)
		// Hover includes the entire icon and badge band. The outer gutter
		// stays inset from the terminal edge; the rule keeps its background.
		ui.FillRect(buf, 0, 0, ui.Rect{
			X: left + markerCol, Y: bandTop, W: ruleCol - markerCol, H: bandH,
		}, cell.BlankCell(rowBG))
	}
	// The accent marker spans the whole band so active and hovered
	// entries occupy identical footprints. The settings gear is not a
	// destination view, so it never takes the marker.
	if current && s.panelOpen && r.W > markerCol {
		for dy := 0; dy < bandH; dy++ {
			buf.Set(left+markerCol, bandTop+dy, cell.NewCell('▌', accent, rowBG, 0))
		}
	}
	y := bandTop + iconRow
	if y >= bottom {
		return
	}
	iconEnd := left + iconCol
	if r.W > iconCol {
		iconEnd = buf.WriteString(left+iconCol, y, icon, iconFG, rowBG, 0)
	}
	// Put the count diagonally beyond the full measured icon. Separate rows
	// provide the raised/lowered look at the terminal's normal, readable size.
	// Reuse WriteString's end so calibration also moves wider icons' badges.
	badgeX := max(badgeCol, iconEnd-left)
	badgeY := bandTop + badge.row
	if badge.text == "" || badge.row >= bandH || badgeY >= bottom {
		return
	}
	_, badgeWidth := rendering.FirstGrapheme(badge.text)
	if badgeX+badgeWidth <= min(r.W, ruleCol) {
		buf.WriteString(left+badgeX, badgeY, badge.text, accent, rowBG, cell.AttrBold)
	}
}

type countBadge struct {
	text string
	row  int // band-local; always inside the destination's existing hit target
}

// badgeFor uses ordinary digits, with placement supplying the script-like look:
// every destination's count sits upper-right.
func badgeFor(v View, n int) countBadge {
	if n <= 0 {
		return countBadge{}
	}
	badge := countBadge{text: "+"}
	if n <= 9 {
		badge.text = string(rune('0' + n))
	}
	return badge
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	switch evt.Kind {
	case ui.EventMouseMotion:
		idx := s.hitTarget(ctx.LocalY)
		if idx != s.hover {
			s.hover = idx
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		idx := s.hitTarget(ctx.LocalY)
		if idx == len(views)+1 {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionOverflow}, Invalidate: ui.InvalidatePaint}
		}
		if idx == settingsHitIndex() {
			return ui.EventResult{
				Handled:    true,
				Action:     &ui.Action{ID: ActionSettings},
				Invalidate: ui.InvalidatePaint,
			}
		}
		if idx < 0 || idx >= len(s.order) {
			return ui.EventResult{Handled: true}
		}
		return ui.EventResult{
			Handled:    true,
			Action:     &ui.Action{ID: ActionSelect, Payload: s.order[idx]},
			Invalidate: ui.InvalidatePaint | ui.InvalidateFocus,
		}
	}
	return ui.EventResult{}
}

func settingsHitIndex() int { return len(views) }

// settingsBandTop is the component-local row where the manage gear band
// starts. Empty when the rail is too short to keep a full band below the
// destinations.
func settingsBandTop(height int) int {
	top := height - RowStride
	if top < 0 {
		return -1
	}
	return top
}

// hitIndex maps a rail-local row to a destination view index. Entries own
// their whole RowStride band, so the gap row below an icon activates the
// same view. The bottom gear is not a view; use hitTarget or SettingsAt.
func hitIndex(localY int) int {
	if localY < 0 {
		return -1
	}
	idx := localY / RowStride
	if idx < 0 || idx >= len(views) {
		return -1
	}
	return idx
}

func (s *Screen) hitTarget(localY int) int {
	if SettingsAt(localY, s.Bounds().H) {
		return settingsHitIndex()
	}
	count := s.visibleCount(s.Bounds().H)
	if count < len(s.order) && localY >= count*RowStride && localY < (count+1)*RowStride && s.Bounds().H >= RowStride*2 {
		return len(views) + 1
	}
	if idx := localY / RowStride; localY >= 0 && idx < count {
		return idx
	}
	return -1
}

func visibleViewCount(height int) int {
	return visibleCount(height, len(views))
}

func (screen *Screen) visibleCount(height int) int { return visibleCount(height, len(screen.order)) }

func visibleCount(height, total int) int {
	available := max(0, height/RowStride-1)
	if available >= total {
		return total
	}
	return max(0, available-1)
}

// SettingsAt reports whether a rail-local row sits on the bottom manage gear.
func SettingsAt(localY, height int) bool {
	top := settingsBandTop(height)
	return top >= 0 && localY >= top && localY < top+RowStride
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Activity bar", Value: s.active.Label()}
}
