package shellterm

import (
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/term"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID  ui.ComponentID = "shellterm"
	SurfaceID ui.ComponentID = "shellterm/surface"

	ActionPromote = "terminal.promote"
	ActionDock    = "terminal.dock"
	ActionClose   = "terminal.close"
	ActionWrite   = "terminal.write"
)

const (
	// Cursor activity holds the cursor visibly on for one phase. Once idle,
	// alternating off/on phases begin at the same cadence.
	cursorBlinkIdleDelay = 600 * time.Millisecond
	cursorBlinkPeriod    = 600 * time.Millisecond
)

// Screen paints one PTY session and routes keys into it.
type Screen struct {
	ui.Base
	th        *theme.Theme
	tokens    *ui.TokenSet
	termID    string
	title     string
	vt        *term.Emulator
	initErr   error
	floating  bool
	exited    bool
	write     func([]byte)
	resize    func(cols, rows int) error
	copy      func(string) tea.Cmd
	now       func() uint64
	cols      int
	rows      int
	scroll    *ui.ScrollModel
	scrollbar *ui.ScrollbarControl

	focusKnown         bool
	focused            bool
	selecting          bool
	lastCursorActivity time.Time
	attention          bool

	fedBell bool
	fedClip string
	hasClip bool
	fedPwd  bool
}

// FeedEffects reports side effects of one Feed batch that the caller must
// publish OUTSIDE the current update pass (event-bus publishes from inside
// Feed would re-enter the update loop).
type FeedEffects struct {
	TitleChanged bool
	Bell         bool
	Clipboard    string
	HasClipboard bool
	PwdChanged   bool
}

// New creates a terminal screen for id. Construction succeeds even when
// libghostty-vt is unavailable; Paint shows the error and input is a no-op.
func New(th *theme.Theme, id, title string) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:               ui.NewBase(ui.ComponentID("shellterm/" + id)),
		th:                 th,
		tokens:             tokens,
		termID:             id,
		title:              title,
		lastCursorActivity: time.Now(),
	}
	s.scroll = ui.NewScrollModel()
	s.scrollbar = ui.NewScrollbarControl(ui.ComponentID("shellterm/scrollbar/"+id), s.scroll, tokens)
	s.scrollbar.OnScroll(func(oldOffset, newOffset int) {
		if s.vt != nil && oldOffset != newOffset {
			s.vt.ScrollDelta(newOffset - oldOffset)
		}
	})
	s.now = func() uint64 { return uint64(time.Now().UnixNano()) }
	s.vt, s.initErr = term.NewEmulator()
	if s.vt != nil {
		// Effect state is data-only: publishing to the workspace/event bus is
		// the caller's job (via the FeedEffects returned from Feed), because
		// Feed runs while Bubble Tea is handling TerminalOutputMsg and a
		// synchronous event-bus publish would re-enter the update loop.
		s.vt.SetTitleHandler(func(t string) {
			s.title = t
		})
		s.vt.SetReplyHandler(func(b []byte) {
			if s.write != nil {
				s.write(b)
			}
		})
		s.vt.SetBellHandler(func() {
			s.fedBell = true
		})
		s.vt.SetClipboardHandler(func(text string) {
			s.fedClip = text
			s.hasClip = true
		})
		s.vt.SetPwdHandler(func(string) {
			s.fedPwd = true
		})
		s.applyDefaultColors()
	}
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

func (s *Screen) SetWrite(fn func([]byte))                { s.write = fn }
func (s *Screen) SetResize(fn func(cols, rows int) error) { s.resize = fn }

// SetCopy builds the asynchronous command that writes selected text to the
// system clipboard. Clipboard subprocess I/O must never run in Handle, which
// executes on Bubble Tea's update goroutine.
func (s *Screen) SetCopy(fn func(string) tea.Cmd) { s.copy = fn }

// SetNow overrides the multi-click clock (tests).
func (s *Screen) SetNow(fn func() uint64) {
	if fn != nil {
		s.now = fn
	}
}

// PWD returns the child's OSC 7 working directory as a filesystem path
// (OSC 7 payloads are file:// URLs).
func (s *Screen) PWD() string {
	if s.vt == nil {
		return ""
	}
	raw := s.vt.PWD()
	if raw == "" || !strings.HasPrefix(raw, "file://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return raw
	}
	return u.Path
}

// SetTermFocused reports focus transitions to the child (CSI I/O) when it
// enabled DEC mode 1004. Idempotent per state.
func (s *Screen) SetTermFocused(v bool) {
	if s.vt == nil || s.exited {
		return
	}
	if s.focusKnown && s.focused == v {
		return
	}
	s.focusKnown = true
	s.focused = v
	s.resetCursorBlink(time.Now())
	if data := s.vt.EncodeFocus(v); len(data) > 0 && s.write != nil {
		s.write(data)
	}
}

// resetCursorBlink records terminal activity and immediately makes the cursor
// visible. Movement, input, output, resize, and focus changes all restart the
// idle delay instead of letting an old wall-clock blink phase hide the cursor.
func (s *Screen) resetCursorBlink(now time.Time) bool {
	s.lastCursorActivity = now
	if s.vt == nil || s.exited {
		return false
	}
	return s.vt.SetCursorState(s.focused, true)
}

// TickCursor advances the cursor's focused blink phase. It returns true only
// when the visible cursor state changed, allowing retained floating windows to
// invalidate their cached content without repainting on every application
// tick. Unfocused terminals keep a steady dim cursor.
func (s *Screen) TickCursor(now time.Time) bool {
	if s.vt == nil || s.exited {
		return false
	}
	blinkOn := true
	if s.focused && s.vt.CursorBlink() {
		idleFor := now.Sub(s.lastCursorActivity)
		if idleFor >= cursorBlinkIdleDelay {
			phase := (idleFor - cursorBlinkIdleDelay) / cursorBlinkPeriod
			blinkOn = phase%2 == 1
		}
	}
	return s.vt.SetCursorState(s.focused, blinkOn)
}

func (s *Screen) SetFloating(v bool) { s.floating = v }

func (s *Screen) SetExited(v bool) { s.exited = v }

// SetAttention records an unseen bell for presentation by the owning dock or
// floating frame. It reports whether the state changed.
func (s *Screen) SetAttention(v bool) bool {
	if s.attention == v {
		return false
	}
	s.attention = v
	return true
}

// HasAttention reports whether this terminal has an unseen bell.
func (s *Screen) HasAttention() bool { return s.attention }

func (s *Screen) SetWindowTitle(title string) {
	if title != "" {
		s.title = title
	}
}

// Feed parses PTY bytes. The returned effects must be published
// asynchronously — publishing to the event bus while Bubble Tea is still
// inside the TerminalOutputMsg update would re-enter the update loop through
// Program.Send and can deadlock it.
func (s *Screen) Feed(data []byte) FeedEffects {
	if s.vt == nil {
		return FeedEffects{}
	}
	before := s.title
	s.fedBell = false
	s.fedClip = ""
	s.hasClip = false
	s.fedPwd = false
	s.vt.Feed(data)
	if len(data) > 0 {
		s.resetCursorBlink(time.Now())
	}
	return FeedEffects{
		TitleChanged: s.title != before,
		Bell:         s.fedBell,
		Clipboard:    s.fedClip,
		HasClipboard: s.hasClip,
		PwdChanged:   s.fedPwd,
	}
}

func (s *Screen) Close() {
	if s.vt != nil {
		s.vt.Close()
		s.vt = nil
	}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.scrollbar != nil {
		s.scrollbar.SetTokens(tokens)
	}
	s.applyDefaultColors()
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
}

func (s *Screen) applyDefaultColors() {
	if s.vt == nil || s.tokens == nil {
		return
	}
	s.vt.SetDefaultColors(s.tokens.Color(ui.ColorFg), s.tokens.Color(ui.ColorBg))
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
	cols, rows := workspace.NormalizeTerminalSize(r.W, r.H)
	if s.vt != nil && (cols != s.cols || rows != s.rows) {
		// Resize the PTY first. If its ioctl fails, keep the emulator on the
		// previous grid so the two sides cannot silently diverge; a later
		// layout retries because s.cols/s.rows were not committed.
		if s.resize != nil {
			if err := s.resize(cols, rows); err != nil {
				return
			}
		}
		s.cols, s.rows = cols, rows
		s.vt.Resize(cols, rows)
		s.resetCursorBlink(time.Now())
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := s.Bounds()
	fg, bg := cell.NoColor, cell.NoColor
	if s.tokens != nil {
		fg = s.tokens.Color(ui.ColorFg)
		bg = s.tokens.Color(ui.ColorBg)
	}
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	if s.initErr != nil {
		msg := " libghostty-vt unavailable: " + s.initErr.Error()
		buf.WriteString(ox+r.X, oy+r.Y, msg, fg, bg, 0)
		return
	}
	if s.vt != nil {
		s.TickCursor(time.Now())
		s.vt.Paint(buf, ox+r.X, oy+r.Y, fg, bg)
		s.paintScrollbackScrollbar(buf, ox+r.X, oy+r.Y)
	}
	if s.exited && s.tokens != nil {
		msg := " [process exited] "
		y := oy + r.Y + max(0, r.H-2)
		buf.WriteString(ox+r.X, y, msg, s.tokens.Color(ui.ColorMuted), bg, 0)
	}
	_ = ctx
}

// syncScrollbackScrollbar binds terminal scrollback to the shared scrollbar
// interaction and geometry path. The control remains mounted while dragging
// to avoid losing capture when the thumb reaches the bottom.
func (s *Screen) syncScrollbackScrollbar() bool {
	if s.vt == nil || s.scrollbar == nil || s.rows < 2 || s.cols < 2 {
		return false
	}
	offset, total, visible := s.vt.Scrollbar()
	ui.SyncScrollModel(s.scroll, total, visible, offset)
	s.scrollbar.SetScroll(s.scroll)
	s.scrollbar.Layout(ui.LayoutContext{}, ui.Rect{X: s.cols - ui.ScrollbarWidth, W: ui.ScrollbarWidth, H: s.rows})
	return total > visible && (!s.vt.ViewportPinned() || s.scrollbar.IsDragging())
}

func (s *Screen) paintScrollbackScrollbar(buf *surface.CellBuffer, ox, oy int) {
	if !s.syncScrollbackScrollbar() {
		return
	}
	s.scrollbar.Paint(buf, ox, oy, ui.PaintContext{})
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind == ui.EventKey {
		return s.handleKey(evt.Key)
	}
	if evt.Kind == ui.EventCustom {
		if paste, ok := evt.Custom.(tea.PasteMsg); ok && !s.exited && s.vt != nil {
			s.resetCursorBlink(time.Now())
			s.vt.ClearSelection()
			s.vt.ScrollBottom()
			return s.writeBytes(s.vt.EncodePaste([]byte(paste.Content)))
		}
	}
	switch evt.Kind {
	case ui.EventMouseClick, ui.EventMouseRelease, ui.EventMouseMotion, ui.EventMouseWheel:
		return s.handleMouse(ctx, evt)
	}
	return ui.EventResult{}
}

func (s *Screen) handleKey(key tea.KeyPressMsg) ui.EventResult {
	if s.exited || s.vt == nil {
		return ui.EventResult{Handled: true}
	}
	s.resetCursorBlink(time.Now())
	shift := key.Mod.Contains(tea.ModShift)
	ctrlShift := key.Mod.Contains(tea.ModCtrl) && shift
	if ctrlShift {
		switch key.Code {
		case 'c', 'C':
			if s.vt.HasSelection() {
				return ui.EventResult{
					Handled:    true,
					Cmd:        s.copySelection(),
					Invalidate: ui.InvalidatePaint,
				}
			}
		case 'a', 'A':
			s.vt.SelectAll()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	// Shift+PgUp/PgDn/Home/End drive scrollback while the primary screen is
	// visible. Full-screen apps (alt screen) get the keys unchanged.
	if shift && !s.vt.AltScreen() {
		page := max(1, s.rows-1)
		switch key.Code {
		case tea.KeyPgUp:
			s.vt.ScrollDelta(-page)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case tea.KeyPgDown:
			s.vt.ScrollDelta(page)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case tea.KeyHome:
			s.vt.ScrollTop()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case tea.KeyEnd:
			s.vt.ScrollBottom()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	s.vt.ClearSelection()
	if !s.vt.ViewportPinned() {
		s.vt.ScrollBottom()
	}
	data := s.vt.EncodeKey(key)
	if len(data) > 0 {
		return s.writeBytes(data)
	}
	return ui.EventResult{Handled: true}
}

func (s *Screen) handleMouse(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.exited || s.vt == nil {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
	}
	cellX, cellY := ctx.LocalX, ctx.LocalY
	if res := s.handleScrollbackScrollbar(ctx, evt); res.Handled {
		return res
	}
	if cellY >= s.rows && !s.selecting {
		if evt.Kind == ui.EventMouseMotion {
			return ui.EventResult{Handled: true}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
	}
	switch evt.Kind {
	case ui.EventMouseClick, ui.EventMouseRelease, ui.EventMouseWheel:
		s.resetCursorBlink(time.Now())
	case ui.EventMouseMotion:
		if s.selecting || evt.MouseMotion.Button != tea.MouseNone || s.vt.MouseTracking() {
			s.resetCursorBlink(time.Now())
		}
	}
	// Shift bypasses application mouse tracking (standard escape hatch so
	// the user can always select/copy).
	if s.vt.MouseTracking() && !mouseShift(evt) {
		data := s.vt.EncodeMouse(evt, cellX, cellY)
		if len(data) > 0 {
			return s.writeBytes(data)
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
	}
	switch evt.Kind {
	case ui.EventMouseWheel:
		delta := 0
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			delta = -3
		case tea.MouseWheelDown:
			delta = 3
		}
		if delta == 0 {
			return ui.EventResult{Handled: true}
		}
		// Alternate screen has no scrollback: translate the wheel into
		// arrow keys (less/vim/man scrolling) like Ghostty's alt-scroll.
		if s.vt.AltScreen() {
			code := tea.KeyUp
			if delta > 0 {
				code = tea.KeyDown
			}
			arrow := s.vt.EncodeKey(tea.KeyPressMsg{Code: code})
			if len(arrow) == 0 {
				return ui.EventResult{Handled: true}
			}
			out := make([]byte, 0, 3*len(arrow))
			for i := 0; i < 3; i++ {
				out = append(out, arrow...)
			}
			return s.writeBytes(out)
		}
		s.vt.ScrollDelta(delta)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case ui.EventMouseClick:
		if evt.MouseClick.Button == tea.MouseLeft {
			// A handled left click makes the Host capture the pointer, so
			// drags keep landing here even when they leave the bounds.
			s.selecting = true
			s.vt.SelectionPress(cellX, cellY, s.now())
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
	case ui.EventMouseMotion:
		if s.selecting && evt.MouseMotion.Button == tea.MouseLeft {
			s.vt.SelectionDrag(cellX, cellY)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseRelease:
		if s.selecting && evt.MouseRelease.Button == tea.MouseLeft {
			s.selecting = false
			var cmd tea.Cmd
			if s.vt.SelectionRelease(cellX, cellY) {
				cmd = s.copySelection()
			}
			return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
}

func (s *Screen) handleScrollbackScrollbar(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.scrollbar == nil {
		return ui.EventResult{}
	}
	visible := s.syncScrollbackScrollbar()
	if !visible && !s.scrollbar.IsDragging() {
		return ui.EventResult{}
	}
	if !s.scrollbar.IsDragging() {
		switch evt.Kind {
		case ui.EventMouseClick, ui.EventMouseWheel:
			if !s.scrollbar.PointerHitBounds(s.scrollbar.Bounds()).Contains(ctx.LocalX, ctx.LocalY) {
				return ui.EventResult{}
			}
		default:
			return ui.EventResult{}
		}
	}
	return s.scrollbar.Handle(ui.ChildEventContext(ctx, s.scrollbar.Bounds()), evt)
}

// CancelPointerCapture resets drag state when the Host revokes capture
// (slot hidden, another window claimed the pointer, ...).
func (s *Screen) CancelPointerCapture() {
	s.selecting = false
	if s.scrollbar != nil {
		s.scrollbar.CancelPointerCapture()
	}
}

// copySelection builds the command that sends the active selection to the
// system clipboard (copy-on-select, matching mainstream terminal behavior).
func (s *Screen) copySelection() tea.Cmd {
	if s.vt == nil || s.copy == nil {
		return nil
	}
	if text := s.vt.SelectionText(); text != "" {
		return s.copy(text)
	}
	return nil
}

func mouseShift(evt ui.Event) bool {
	switch evt.Kind {
	case ui.EventMouseClick:
		return evt.MouseClick.Mod.Contains(tea.ModShift)
	case ui.EventMouseRelease:
		return evt.MouseRelease.Mod.Contains(tea.ModShift)
	case ui.EventMouseMotion:
		return evt.MouseMotion.Mod.Contains(tea.ModShift)
	case ui.EventMouseWheel:
		return evt.MouseWheel.Mod.Contains(tea.ModShift)
	}
	return false
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
