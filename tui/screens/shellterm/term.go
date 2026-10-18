package shellterm

import (
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/termview"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID  ui.ComponentID = "shellterm"
	SurfaceID ui.ComponentID = "shellterm/surface"

	ActionPromote     = "terminal.promote"
	ActionDock        = "terminal.dock"
	ActionClose       = "terminal.close"
	ActionTakeControl = "terminal.takeControl"
)

// Screen paints one PTY session and routes keys into it.
type Screen struct {
	ui.Base
	th                   *theme.Theme
	tokens               *ui.TokenSet
	termID               string
	title                string
	view                 termview.View
	projection           workspace.TerminalProjection
	controlled           bool
	closed               bool
	command              func(Command) tea.Cmd
	pending              []Command
	readSequence         uint64
	readStart, readCount int
	readPending          bool
	readViewport         uint64
	copyPending          bool
	blinkOn              bool
	floating             bool
	exited               bool
	copy                 func(string) tea.Cmd
	now                  func() uint64
	cols                 int
	rows                 int
	scroll               *ui.ScrollModel
	scrollbar            *ui.ScrollbarControl

	focusKnown         bool
	focused            bool
	selecting          bool
	lastCursorActivity time.Time
	syncRetryAt        time.Time
	attention          bool
}

// New creates a projection-only terminal screen for id. The host supplies rows
// and an explicit control lease; construction never creates a terminal engine.
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
		if oldOffset != newOffset {
			s.view.ScrollTo(newOffset)
			s.requestRows()
		}
	})
	s.now = func() uint64 { return uint64(time.Now().UnixNano()) }
	s.blinkOn = true
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

func (s *Screen) SetCommand(fn func(Command) tea.Cmd) { s.command = fn }
func (s *Screen) SetControlled(v bool) {
	if s.controlled == v {
		return
	}
	s.controlled = v
	if !v {
		kept := s.pending[:0]
		for _, c := range s.pending {
			if c.Kind == CommandReadRows {
				kept = append(kept, c)
			}
		}
		s.pending = kept
	}
	if v {
		s.queue(Command{Kind: CommandFocus, Focused: s.focused})
		s.requestResize()
	}
}
func (s *Screen) Controlled() bool { return s.controlled }

// SetProjection installs a detached immutable service snapshot, rejecting older generations.
func (s *Screen) SetProjection(p workspace.TerminalProjection) {
	if s.closed || p.Info.ID != s.termID || p.Generation < s.view.Generation {
		return
	}
	changed := p.Generation != s.view.Generation
	wasSynchronized := s.view.Grid.Synchronized
	if !s.view.Install(p.Grid, p.Generation) {
		return
	}
	if !s.view.HasSelection() {
		s.copyPending = false
	}
	// A refresh does not cancel an independent scroll/copy intent. Its result
	// may still supply the requested page, even after another ingress generation.
	p.Grid = s.view.Grid
	s.projection = p
	s.cols, s.rows = p.Grid.Cols, p.Grid.GridRows
	s.title = p.Info.Title
	s.exited = !p.Info.Alive
	if p.Grid.Synchronized && !wasSynchronized {
		s.syncRetryAt = time.Now().Add(terminalvt.SynchronizedOutputTimeout)
	} else if !p.Grid.Synchronized {
		s.syncRetryAt = time.Time{}
	}
	if changed && !p.Grid.Synchronized {
		s.resetCursorBlink(time.Now())
	}
	s.requestRows()
}

// SetRows fences asynchronous page responses by both request intent and source generation.
func (s *Screen) SetRows(sequence uint64, p workspace.TerminalProjection) bool {
	if !s.readPending || sequence != s.readSequence || p.Info.ID != s.termID {
		return false
	}
	s.readPending = false
	if s.readViewport != s.view.Intent() {
		s.requestRows()
		return false
	}
	if p.Generation < s.view.Generation || p.Grid.ReadSequence < s.view.Grid.ReadSequence {
		// Exact intent completed, but another reader already published newer
		// state. Retire it so future missing-page/timeout retries can proceed.
		s.requestRows()
		return false
	}
	if s.readCount > 0 && (p.Grid.HistoryEpoch != s.view.Grid.HistoryEpoch || p.Grid.Cols != s.cols || p.Grid.GridRows != s.rows) {
		// The engine replaces an incompatible anchored selection read with a
		// whole clamped viewport. Cancel copy, then atomically install it once.
		s.copyPending = false
		s.view.ClearSelection()
	}
	s.SetProjection(p)
	if s.copyPending {
		s.requestSelectionRows()
	}
	return true
}

// RowsFailed releases only the exact in-flight page intent. The host presents
// the service error and may request another page on the next interaction.
func (s *Screen) RowsFailed(sequence uint64) {
	if s.readPending && sequence == s.readSequence {
		s.readPending = false
		s.copyPending = false
	}
}

// TakePendingCommands drains intents from layout/focus/page installation. The host
// executes them asynchronously, never from Paint or Layout.
func (s *Screen) TakePendingCommands() []Command { out := s.pending; s.pending = nil; return out }
func (s *Screen) queue(c Command)                { c.ID = s.termID; s.pending = append(s.pending, c) }
func (s *Screen) requestResize() {
	c, r := workspace.NormalizeTerminalSize(s.Bounds().W, s.Bounds().H)
	if s.controlled && !s.exited && (c != s.cols || r != s.rows) {
		for _, p := range s.pending {
			if p.Kind == CommandResize && p.Cols == c && p.Rows == r {
				return
			}
		}
		s.queue(Command{Kind: CommandResize, Cols: c, Rows: r})
	}
}
func (s *Screen) requestPage(start, count int) {
	if s.closed || count < 0 {
		return
	}
	if s.readPending && s.readStart == start && s.readCount == count {
		return
	}
	s.readSequence++
	kept := s.pending[:0]
	for _, c := range s.pending {
		if c.Kind != CommandReadRows {
			kept = append(kept, c)
		}
	}
	s.pending = kept
	s.readStart, s.readCount = start, count
	s.readViewport = s.view.Intent()
	s.readPending = true
	s.queue(Command{Kind: CommandReadRows, Start: start, Count: count, ViewportStart: s.view.ViewportStart(), Sequence: s.readSequence, Generation: s.view.Generation, Viewport: s.view.Intent(), HistoryEpoch: s.view.Grid.HistoryEpoch, HistoryBase: s.view.Grid.HistoryBase})
}
func (s *Screen) requestRows() {
	if s.view.Grid.Synchronized {
		return
	}
	if _, _, ok := s.view.VisibleMissing(); ok {
		// A viewport is one bounded atomic read, not a succession of holes from
		// competing generations. Count zero uses the engine's current grid height.
		s.requestPage(s.view.ViewportStart(), 0)
	}
}

// ViewportRead snapshots presentation intent on Update; the host performs the
// bounded service read off Update. Every invalidation reads the desired viewport
// directly rather than installing the active grid and blanking history first.
func (s *Screen) ViewportRead() Command {
	return Command{ID: s.termID, Kind: CommandReadRows, Start: s.view.ViewportStart(), Viewport: s.view.Intent(), HistoryEpoch: s.view.Grid.HistoryEpoch, HistoryBase: s.view.Grid.HistoryBase}
}
func (s *Screen) AcceptsViewport(intent uint64) bool {
	return intent == 0 || intent == s.view.Intent()
}
func (s *Screen) requestSelectionRows() {
	if s.view.Grid.Synchronized {
		return
	}
	if a, n, ok := s.view.SelectionMissing(); ok {
		s.requestPage(a, n)
	}
}

// TakePendingCopy returns the clipboard command after every selected page arrived.
func (s *Screen) TakePendingCopy() tea.Cmd {
	if !s.copyPending {
		return nil
	}
	text, ok := s.view.SelectionText()
	if !ok {
		return nil
	}
	s.copyPending = false
	if text != "" && s.copy != nil {
		return s.copy(text)
	}
	return nil
}

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
	raw := s.projection.Info.CWD
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
	if s.focusKnown && s.focused == v {
		return
	}
	s.focusKnown = true
	s.focused = v
	s.resetCursorBlink(time.Now())
	if s.controlled && !s.exited {
		s.queue(Command{Kind: CommandFocus, Focused: v})
	}
}
func (s *Screen) resetCursorBlink(now time.Time) bool {
	s.lastCursorActivity = now
	changed := !s.blinkOn
	s.blinkOn = true
	return changed
}
func (s *Screen) TickCursor(now time.Time) bool {
	// An abandoned DEC 2026 update may produce no more PTY events. Re-read via
	// the existing off-Update command lane so the engine can release its bounded
	// hold. This is owner/sequence fenced like any other row request.
	if !s.syncRetryAt.IsZero() && !now.Before(s.syncRetryAt) {
		s.syncRetryAt = now.Add(terminalvt.SynchronizedOutputTimeout)
		s.requestPage(s.view.ViewportStart(), 0)
	}
	on := !s.focused || ui.CursorPhaseVisible(int(now.UnixMilli()), int(s.lastCursorActivity.UnixMilli()), s.view.Grid.Cursor.Blink)
	changed := on != s.blinkOn
	s.blinkOn = on
	return changed
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

func (s *Screen) Close() {
	s.closed = true
	s.pending = nil
	s.readPending = false
	s.copyPending = false
	s.CancelPointerCapture()
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.scrollbar != nil {
		s.scrollbar.SetTokens(tokens)
	}
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
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
	s.requestResize()
	s.requestRows()
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := s.Bounds()
	fg, bg := cell.NoColor, cell.NoColor
	if s.tokens != nil {
		fg = s.tokens.Color(ui.ColorFg)
		bg = s.tokens.Color(ui.ColorBg)
	}
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	if s.projection.Error != "" {
		if r.H > 0 {
			buf.WriteString(ox+r.X, oy+r.Y, rendering.TruncateVisual(" "+s.projection.Error, r.W, "…"), fg, bg, 0)
		}
		return
	}
	if ctx.Frame > 0 {
		s.TickCursor(time.UnixMilli(int64(ctx.Frame)))
	} else {
		s.TickCursor(time.Now())
	}
	s.view.Paint(buf, ox+r.X, oy+r.Y, r.W, r.H, s.tokens, s.focused, s.blinkOn)
	s.paintScrollbackScrollbar(buf, ox+r.X, oy+r.Y)
	if s.exited && s.tokens != nil {
		msg := " [process exited] "
		y := oy + r.Y + max(0, r.H-2)
		if r.H > 0 {
			buf.WriteString(ox+r.X, y, rendering.TruncateVisual(msg, r.W, "…"), s.tokens.Color(ui.ColorMuted), bg, 0)
		}
	}
	_ = ctx
}

// syncScrollbackScrollbar binds terminal scrollback to the shared scrollbar
// interaction and geometry path. The control remains mounted while dragging
// to avoid losing capture when the thumb reaches the bottom.
func (s *Screen) syncScrollbackScrollbar() bool {
	if s.closed || s.scrollbar == nil || s.rows < 2 || s.cols < 2 {
		return false
	}
	offset, total, visible := s.view.Scrollbar()
	ui.SyncScrollModel(s.scroll, total, visible, offset)
	s.scrollbar.SetScroll(s.scroll)
	s.scrollbar.Layout(ui.LayoutContext{}, ui.Rect{X: max(0, s.Bounds().W-ui.ScrollbarWidth), W: ui.ScrollbarWidth, H: s.Bounds().H})
	return total > visible && (!s.view.Pinned() || s.scrollbar.IsDragging())
}

func (s *Screen) paintScrollbackScrollbar(buf *surface.CellBuffer, ox, oy int) {
	if !s.syncScrollbackScrollbar() {
		return
	}
	s.scrollbar.Paint(buf, ox, oy, ui.PaintContext{})
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind == ui.EventKey {
		res := s.handleKey(evt.Key)
		s.requestRows()
		return res
	}
	if evt.Kind == ui.EventCustom {
		if paste, ok := evt.Custom.(tea.PasteMsg); ok && !s.exited && !s.closed {
			if !s.controlled {
				return s.takeControlResult()
			}
			s.resetCursorBlink(time.Now())
			s.view.ClearSelection()
			s.view.ScrollBottom()
			s.requestRows()
			return s.send(Command{Kind: CommandPaste, Paste: []byte(paste.Content)})
		}
	}
	switch evt.Kind {
	case ui.EventMouseClick, ui.EventMouseRelease, ui.EventMouseMotion, ui.EventMouseWheel:
		return s.handleMouse(ctx, evt)
	}
	return ui.EventResult{}
}

func (s *Screen) handleKey(key tea.KeyPressMsg) ui.EventResult {
	if s.closed {
		return ui.EventResult{Handled: true}
	}
	s.resetCursorBlink(time.Now())
	ctrlShift := key.Mod == tea.ModCtrl|tea.ModShift
	if ctrlShift {
		switch key.Code {
		case 'c', 'C':
			var cmd tea.Cmd
			if s.view.HasSelection() {
				cmd = s.copySelection()
			}
			return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
		case 'a', 'A':
			s.view.SelectAll()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	// Shift+PgUp/PgDn/Home/End drive scrollback while the primary screen is
	// visible. Full-screen apps (alt screen) get the keys unchanged.
	if key.Mod == tea.ModShift && !s.view.Grid.Modes.AltScreen {
		page := max(1, s.rows-1)
		switch key.Code {
		case tea.KeyPgUp:
			s.view.ScrollDelta(-page)
			s.requestRows()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case tea.KeyPgDown:
			s.view.ScrollDelta(page)
			s.requestRows()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case tea.KeyHome:
			s.view.ScrollTop()
			s.requestRows()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case tea.KeyEnd:
			s.view.ScrollBottom()
			s.requestRows()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	if !s.controlled || s.exited {
		return s.takeControlResult()
	}
	s.view.ClearSelection()
	s.view.ScrollBottom()
	s.requestRows()
	return s.send(Command{Kind: CommandKey, Key: neutralKey(key)})
}

func (s *Screen) handleMouse(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.closed {
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
		if s.selecting || evt.MouseMotion.Button != tea.MouseNone || s.view.Grid.Modes.MouseTracking {
			s.resetCursorBlink(time.Now())
		}
	}
	// Shift bypasses application mouse tracking (standard escape hatch so
	// the user can always select/copy).
	if s.view.Grid.Modes.MouseTracking && !mouseShift(evt) && s.controlled && !s.exited {
		return s.send(Command{Kind: CommandMouse, Mouse: neutralMouse(evt, cellX, cellY)})
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
		if s.view.Grid.Modes.AltScreen {
			code := terminalvt.KeyUp
			if delta > 0 {
				code = terminalvt.KeyDown
			}
			return s.send(Command{Kind: CommandKey, Key: terminalvt.Key{Code: code}, Repeat: 3})
		}
		s.view.ScrollDelta(delta)
		s.requestRows()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case ui.EventMouseClick:
		if evt.MouseClick.Button == tea.MouseLeft {
			// A handled left click makes the Host capture the pointer, so
			// drags keep landing here even when they leave the bounds.
			s.selecting = true
			s.view.SelectionPress(cellX, cellY, s.now())
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
	case ui.EventMouseMotion:
		if s.selecting && evt.MouseMotion.Button == tea.MouseLeft {
			s.view.SelectionDrag(cellX, cellY)
			s.requestRows()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseRelease:
		if s.selecting && evt.MouseRelease.Button == tea.MouseLeft {
			s.selecting = false
			var cmd tea.Cmd
			if s.view.SelectionRelease(cellX, cellY) {
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
	if s.copy == nil {
		return nil
	}
	text, ok := s.view.SelectionText()
	if !ok {
		s.copyPending = true
		s.requestSelectionRows()
		return nil
	}
	if text != "" {
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

func (s *Screen) takeControlResult() ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionTakeControl, Payload: s.termID}}
}
func (s *Screen) send(c Command) ui.EventResult {
	if s.closed || s.exited {
		return ui.EventResult{Handled: true}
	}
	if !s.controlled {
		return s.takeControlResult()
	}
	c.ID = s.termID
	var cmd tea.Cmd
	if s.command != nil {
		cmd = s.command(c)
	} else {
		s.queue(c)
	}
	return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
}
