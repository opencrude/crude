package shellterm

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/term"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID   ui.ComponentID = "shellterm"
	ActionsID  ui.ComponentID = "shellterm/actions"
	SurfaceID  ui.ComponentID = "shellterm/surface"

	ActionPromote = "terminal.promote"
	ActionDock    = "terminal.dock"
	ActionClose   = "terminal.close"
	ActionWrite   = "terminal.write"
)

// Screen paints one PTY session and routes keys into it.
type Screen struct {
	ui.Base
	th      *theme.Theme
	tokens  *ui.TokenSet
	termID  string
	title   string
	vt      *term.Emulator
	actions *ui.ActionBar
	floating bool
	exited   bool
	write    func([]byte)
	resize   func(cols, rows int)
	onTitle  func(string)
	cols     int
	rows     int
}

// New creates a terminal screen for id.
func New(th *theme.Theme, id, title string) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:   ui.NewBase(ui.ComponentID("shellterm/" + id)),
		th:     th,
		tokens: tokens,
		termID: id,
		title:  title,
		vt:     term.NewEmulator(),
	}
	s.vt.SetTitleHandler(func(t string) {
		s.title = t
		if s.onTitle != nil {
			s.onTitle(t)
		}
	})
	s.rebuildActions()
	return s
}

func (s *Screen) ID() ui.ComponentID {
	if s.termID == "" {
		return ScreenID
	}
	return ui.ComponentID("shellterm/" + s.termID)
}

func (s *Screen) TermID() string { return s.termID }
func (s *Screen) Title() string {
	if s.title != "" {
		return s.title
	}
	return "Terminal"
}

func (s *Screen) SetWrite(fn func([]byte))          { s.write = fn }
func (s *Screen) SetResize(fn func(cols, rows int)) { s.resize = fn }
func (s *Screen) SetTitleHandler(fn func(string))   { s.onTitle = fn }

func (s *Screen) SetFloating(v bool) {
	s.floating = v
	s.rebuildActions()
}

func (s *Screen) SetExited(v bool) { s.exited = v }

func (s *Screen) SetWindowTitle(title string) {
	if title != "" {
		s.title = title
	}
}

func (s *Screen) Feed(data []byte) {
	if s.vt != nil {
		s.vt.Feed(data)
	}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.actions != nil {
		s.actions.SetTokens(tokens)
	}
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
}

func (s *Screen) rebuildActions() {
	floatLabel, floatAction := "Float", ActionPromote
	if s.floating {
		floatLabel, floatAction = "Dock", ActionDock
	}
	s.actions = ui.NewActionBar(ui.NewID(s.ID(), "actions"), s.tokens,
		ui.ActionBarButton{ID: ui.NewID(s.ID(), "float"), Label: floatLabel, Action: floatAction, Variant: ui.VariantSecondary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(s.ID(), "close"), Label: "Close", Action: ActionClose, Variant: ui.VariantGhost, Enabled: true},
	)
	s.actions.SetAlignEnd(true)
}

func (s *Screen) Children() []ui.Component {
	if s.actions == nil {
		return nil
	}
	return []ui.Component{s.actions}
}

func (s *Screen) PreferredHeight(maxHeight int) int {
	h := 14
	if maxHeight > 0 && h > maxHeight {
		h = maxHeight
	}
	if h < 6 {
		h = 6
	}
	return h
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	h := s.PreferredHeight(cs.MaxH)
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: h})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	actionH := 1
	if s.actions != nil {
		s.actions.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: r.H - actionH, W: r.W, H: actionH})
	}
	cols, rows := r.W, r.H-actionH
	if cols < 2 {
		cols = 2
	}
	if rows < 1 {
		rows = 1
	}
	if s.vt != nil && (cols != s.cols || rows != s.rows) {
		s.cols, s.rows = cols, rows
		s.vt.Resize(cols, rows)
		if s.resize != nil {
			s.resize(cols, rows)
		}
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := s.Bounds()
	fg, bg := cell.NoColor, cell.NoColor
	if s.tokens != nil {
		fg = s.tokens.Color(ui.ColorFg)
		bg = s.tokens.Color(ui.ColorPopupBg)
	}
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	if s.vt != nil {
		s.vt.Paint(buf, ox+r.X, oy+r.Y, fg, bg)
	}
	if s.exited && s.tokens != nil {
		msg := " [process exited] "
		buf.WriteString(ox+r.X, oy+r.Y+r.H-2, msg, s.tokens.Color(ui.ColorMuted), bg, 0)
	}
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind == ui.EventKey {
		if s.exited {
			return ui.EventResult{Handled: true}
		}
		data := term.EncodeKey(evt.Key)
		if len(data) > 0 {
			return s.writeBytes(data)
		}
		return ui.EventResult{Handled: true}
	}
	if evt.Kind == ui.EventCustom {
		if paste, ok := evt.Custom.(tea.PasteMsg); ok && !s.exited {
			return s.writeBytes([]byte(paste.Content))
		}
	}
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case ActionPromote, ActionDock, ActionClose:
		return ui.EventResult{
			Handled: true,
			Action:  &ui.Action{ID: action.ID, Payload: s.termID},
		}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: s.Title()}
}

func (s *Screen) writeBytes(data []byte) ui.EventResult {
	if len(data) == 0 {
		return ui.EventResult{Handled: true}
	}
	if s.write != nil {
		s.write(data)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	return ui.EventResult{
		Handled:    true,
		Action:     &ui.Action{ID: ActionWrite, Payload: writePayload{ID: s.termID, Data: data}},
		Invalidate: ui.InvalidatePaint,
	}
}

type writePayload struct {
	ID   string
	Data []byte
}

// WritePayload is the ActionWrite payload.
type WritePayload = writePayload
