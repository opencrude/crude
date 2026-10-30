package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/tui/wm"
)

// shellRegion classifies a screen point for VIEWMODEL POLICY only (focus
// stack updates, editor focus policy, status row, context menus). Actual
// pointer targeting happens in the root Host hit tree; these regions never
// translate coordinates. Sidebar divider drag is Host pointer capture on
// ui.SplitDivider, not a viewmodel-owned resize mode.
type shellRegion int

const (
	regionNone shellRegion = iota
	regionSidebar
	regionTabBar
	regionSessionDeck
	regionTermDock
	regionEditor
	regionInput
	regionChat
	regionLauncher
)

func (m *App) classifyShellRegion(x, y int, isWheel bool) shellRegion {
	if m.sessionDeckModal() && !isWheel {
		return regionSessionDeck
	}

	if m.termDockRect.Contains(x, y) {
		return regionTermDock
	}

	if x < m.sidebarHitWidth() {
		return regionSidebar
	}
	if g := m.paneAt(x, y); g != nil {
		if y < g.body.Y {
			return regionTabBar
		}
		if g.deck.Contains(x, y) {
			return regionSessionDeck
		}
		if g.editor.Contains(x, y) {
			return regionEditor
		}
		if g.input.Contains(x, y) {
			return regionInput
		}
		if g.chat.Contains(x, y) {
			return regionChat
		}
		if m.launcherHostVisible() {
			return regionLauncher
		}
		return regionNone
	}

	if !isWheel && m.tabBarVisible() {
		top := m.chromeTopRow()
		if y >= top-2 && y < top {
			return regionTabBar
		}
	}

	layout := m.computeLayout()
	if m.sessionDeckRect.Contains(x, y) {
		return regionSessionDeck
	}

	if m.isEditorActive() {
		if y >= layout.ChatAreaTop && y < layout.ChatAreaTop+m.ChatAreaHeight {
			return regionEditor
		}
	}

	// Launcher screen: no agents, no editor, but a launcher is active.
	// The launcher fills the main content area (same Y range as the
	// editor/chat area). Clicks OUTSIDE this Y range (e.g. on the
	// input bar at the bottom) fall through to the input/chat
	// regions below; the launcher must NOT steal those clicks.
	// The file tree (sidebar) is already handled above (regionSidebar
	// check), so the launcher never interferes with it.
	if m.AgentView.VisibleCount() == 0 && m.EditorState.ActiveView == "" && m.launcherScreen != nil {
		if y >= layout.ChatAreaTop && y < layout.ChatAreaTop+m.ChatAreaHeight {
			return regionLauncher
		}
		// Fall through: clicks below the chat area go to the input
		// region (so the user can still type / to filter).
	}

	if m.AgentView.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
		return regionInput
	}

	if m.OverlayState.IsChat() {
		if !m.inputRect.Empty() && y >= m.inputRect.Y {
			return regionInput
		}
		inputStartLine := m.ChatAreaHeight + m.chromeTopRow() + m.sessionDeckRect.H
		if y >= inputStartLine {
			return regionInput
		}
	}

	return regionChat
}

// blockingDeckShieldsPointer keeps a modal session panel from routing raw
// screen coordinates through the root Host into controls behind its slot.
// A drag that began inside the deck may finish outside; any capture owned by
// another shell component is stale once a blocking panel takes precedence.
func (m *App) blockingDeckShieldsPointer(x, y int, preserveDeckCapture bool) bool {
	if !m.sessionDeckModal() {
		return false
	}
	if m.rootHost != nil {
		captured, _, ok := m.rootHost.CapturedPointer()
		if ok {
			deckCaptured := false
			if c := m.rootHost.Component(captured); c != nil {
				_, deckCaptured = c.(*deckMount)
			}
			if preserveDeckCapture && deckCaptured {
				return false
			}
			m.rootHost.CancelPointerCapture()
			if preserveDeckCapture {
				return true
			}
		}
	}
	return !m.sessionDeckRect.Contains(x, y)
}

// handleShellPointer routes one pointer message through the root Host.
// The host hit-tests the shell tree, computes local coordinates from the
// layout geometry, and manages capture for drags.
func (m *App) handleShellPointer(msg tea.Msg) tea.Cmd {
	if m.rootHost == nil {
		return nil
	}
	res := m.rootHost.Handle(msg)
	return tea.Batch(res.Cmd, m.applyHostPointerActions(res))
}

// applyHostPointerActions routes a host action and returns its command.
// Dropping the command here silently broke mouse-activated sidebar items
// (their state changed but the follow-up command never ran).
func (m *App) applyHostPointerActions(res ui.EventResult) tea.Cmd {
	if res.Action == nil {
		return nil
	}
	return m.handleHostAction(res.Action)
}

func (m *App) setSidebarWidth(w int) {
	if m.Tree == nil {
		return
	}
	if w < 12 {
		w = 12
	}
	maxW := m.width / 2
	if maxW < 12 {
		maxW = 12
	}
	if w > maxW {
		w = maxW
	}
	if m.Tree.Width == w {
		return
	}
	m.Tree.Width = w
	if m.Tree.Model != nil {
		m.Tree.Model.Update(filetree.TreeWidthMsg{Width: w})
	}
}

func (m *App) routeInputMouse(msg tea.Msg) []tea.Cmd {
	var cmds []tea.Cmd
	canType := false
	if n := m.AgentView.ActiveNode(); n != nil && n.State != nil {
		canType = !n.State.Completed && n.State.IsActive()
	}
	updated, cmd, evt := m.Input.Update(m.inputLocalMouse(msg), canType && !m.OverlayState.IsBlocking(), m.OverlayState.IsBlocking())
	m.Input = updated
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, m.handleInputEvent(evt)...)
	return cmds
}

// inputLocalMouse converts a screen-absolute mouse message to shellinput-local
// coordinates using the input screen's rect from the root Host hit tree (one
// geometry source). The input model maps the local Y to a text position.
func (m *App) inputLocalMouse(msg tea.Msg) tea.Msg {
	if m.rootHost == nil {
		return msg
	}
	r, ok := m.screenRectOf(m.inputScreen.ID())
	if !ok || r.Empty() {
		return msg
	}
	return translateMouseDelta(msg, -r.X, -r.Y)
}

// translateMouseDelta shifts a mouse message's coordinates by (dx, dy).
func translateMouseDelta(msg tea.Msg, dx, dy int) tea.Msg {
	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		mouse := mm.Mouse()
		return tea.MouseClickMsg{X: mouse.X + dx, Y: mouse.Y + dy, Button: mm.Button, Mod: mm.Mod}
	case tea.MouseMotionMsg:
		mouse := mm.Mouse()
		return tea.MouseMotionMsg{X: mouse.X + dx, Y: mouse.Y + dy, Button: mm.Button, Mod: mm.Mod}
	case tea.MouseReleaseMsg:
		mouse := mm.Mouse()
		return tea.MouseReleaseMsg{X: mouse.X + dx, Y: mouse.Y + dy, Button: mm.Button, Mod: mm.Mod}
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg{X: mm.X + dx, Y: mm.Y + dy, Button: mm.Button, Mod: mm.Mod}
	}
	return msg
}

func (m *App) dispatchMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	mouse := msg.Mouse()

	// Right-click is handled separately: it always shows a context
	// menu (or dismisses an existing one) and never affects the
	// focus or current selection state.
	if msg.Button == tea.MouseRight {
		return m.dispatchMouseRightClick(msg)
	}

	// Click-outside handling for all popups. Check every open popup:
	// if the click missed it, act based on its click-outside behavior.
	if m.handlePopupClickOutside(msg) {
		return m, nil // modal popup: suppress the click entirely
	}

	if m.windowManager != nil {
		if hitWin := m.windowManager.HandleMouse(msg); hitWin != nil {
			if hitWin.Closable() && hitWin.IsOnCloseButton(mouse.X, mouse.Y) {
				m.closePopupWindowByID(hitWin.ID)
				return m, nil
			}
			if m.windowManager.IsDragging() {
				return m, tea.Batch(cmds...)
			}
			if group, ok := paneGroupFromOverlay(ui.OverlayID(hitWin.ID)); ok && m.Win.IsFloating(group) {
				m.activatePaneWindow(group)
			}
			if cmd := m.handlePopupContentClick(hitWin, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			// Mark popup content dirty so the click effect is rendered.
			hitWin.MarkContentDirty()
			return m, tea.Batch(cmds...)
		}
	}
	if m.blockingDeckShieldsPointer(mouse.X, mouse.Y, false) {
		return m, nil
	}
	// Header controls are app chrome, not the sidebar/chat beneath their X
	// coordinate. Inspecting the link must retain the draft's keyboard owner.
	if m.headerScreen != nil {
		if rect, ok := m.screenRectOf(m.headerScreen.ID()); ok && rect.Contains(mouse.X, mouse.Y) {
			defer m.syncRootFocus()
			return m, m.handleShellPointer(msg)
		}
	}
	previousSession := m.ActiveSession
	m.activatePaneAt(mouse.X, mouse.Y)
	if m.blockingDeckShieldsPointer(mouse.X, mouse.Y, false) {
		return m, nil
	}

	region := m.classifyShellRegion(mouse.X, mouse.Y, false)

	switch region {
	case regionEditor:
		m.unfocus()
		cmd := m.handleShellPointer(msg)
		m.updateDiagHoverPopup(mouse.X, mouse.Y)
		return m, cmd

	case regionSessionDeck:
		m.setTermDockFocused(false)
		m.FocusStack.Clear()
		m.setSessionDeckFocused(true)
		return m, m.handleShellPointer(msg)

	case regionTermDock:
		m.setSessionDeckFocused(false)
		m.setTermDockFocused(true)
		m.FocusStack.Clear()
		return m, m.handleShellPointer(msg)
	}

	// Focus-stack policy for interactive shell regions. Runs before the
	// root host routes the click so the stack and the host focus agree
	// by the next frame's syncRootFocus.
	switch region {
	case regionSidebar:
		m.setSessionDeckFocused(false)
		m.setTermDockFocused(false)
		m.FocusStack.Clear()
		m.FocusStack.Push(FocusTree)
	case regionTabBar:
		// A pointer selection/close commits the operation; it is not entry
		// into keyboard strip navigation. Host focuses the hit control while
		// dispatching, then the selected content takes focus back.
		m.unfocus()
		defer m.syncRootFocus()
	case regionChat:
		// Click-away dismisses the current draft's range, not a different
		// session's saved range just restored by activating its pane.
		if m.ActiveSession == previousSession {
			m.Input.ClearInputSelection()
		}
		m.unfocus()
	case regionInput:
		m.unfocus()
	case regionLauncher:
		m.setSessionDeckFocused(false)
		m.setTermDockFocused(false)
		// A prior sidebar click leaves FocusTree on the policy stack. The
		// root Host correctly focuses the clicked launcher search field, but
		// focusTarget would otherwise route the next key back into the file
		// filter. Clear that stale policy owner before Host pointer routing.
		m.FocusStack.Clear()
		m.LastInteractionTime = time.Now()
	}

	res := m.rootHost.Handle(msg)
	if region == regionTabBar {
		// Restore content before activation can open a decision modal and
		// capture its return focus. The Host's temporary pointer focus must
		// never become that modal's saved keyboard owner.
		m.syncRootFocus()
	}
	if cmd := m.applyHostPointerActions(res); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if res.Cmd != nil {
		cmds = append(cmds, res.Cmd)
	}

	if region == regionNone && m.FocusStack.Top() == FocusTree && mouse.X >= m.sidebarHitWidth() {
		m.FocusStack.Pop()
	}

	return m, tea.Batch(cmds...)
}

func (m *App) dispatchMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if m.windowManager != nil {
		if hitWin := m.windowManager.HandleMouseWheel(msg); hitWin != nil {
			m.setWheelHost(windowContentHost(hitWin))
			if cmd := m.forwardEventToWindowContent(hitWin, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			hitWin.MarkContentDirty()
			return m, tea.Batch(cmds...)
		}
		for _, fw := range m.windowManager.Windows() {
			if fw.Visible() && fw.Modal() {
				m.setWheelHost(nil)
				return m, nil
			}
		}
	}
	m.setWheelHost(m.rootHost)
	region := m.classifyShellRegion(msg.X, msg.Y, true)
	// A modal deck still shields clicks/selection, but wheel-over-chat is a
	// read-only navigation action and remains available behind it. Agent prompt
	// docks are nonmodal and use ordinary Host hit testing for every surface.
	if m.blockingDeckShieldsPointer(msg.X, msg.Y, false) && region != regionChat {
		return m, nil
	}

	switch region {
	case regionSidebar:
		return m, m.handleShellPointer(msg)

	case regionEditor:
		return m, m.handleShellPointer(msg)

	case regionSessionDeck:
		return m, m.handleShellPointer(msg)

	case regionLauncher:
		res := m.rootHost.Handle(msg)
		m.LastInteractionTime = time.Now()
		return m, res.Cmd

	default:
		// Chat (and anything else in the shell tree) scrolls through the
		// root host hit-test; unhandled wheel stays unhandled.
		return m, m.handleShellPointer(msg)
	}
}

func (m *App) setWheelHost(host *ui.Host) {
	if m.wheelHost == host {
		return
	}
	if m.wheelHost != nil {
		m.wheelHost.ResetWheelGesture()
	}
	if host != nil {
		host.ResetWheelGesture()
	}
	m.wheelHost = host
}

func (m *App) dispatchMouseMotion(msg tea.MouseMotionMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	mouse := msg.Mouse()

	if m.windowManager != nil {
		m.windowManager.HandleMouseMotion(msg)
		if m.windowManager.IsDragging() {
			return m, tea.Batch(cmds...)
		}
		// Track the resize-edge hover so the frame chrome can highlight
		// exactly which border a press will drag. Clear the highlight when
		// the pointer leaves a window or its edges.
		hit := m.windowManager.WindowAt(mouse.X, mouse.Y)
		for _, fw := range m.windowManager.Windows() {
			if !fw.Visible() || !fw.Focusable() {
				continue
			}
			if fw == hit && fw.Resizable() {
				fw.SetHoverEdge(fw.HitTestEdge(mouse.X, mouse.Y))
			} else if fw.HoverEdge() != wm.EdgeNone {
				fw.SetHoverEdge(wm.EdgeNone)
			}
		}
	}
	if m.rootHost != nil && m.rootHost.HasPointerCapture() &&
		m.blockingDeckShieldsPointer(mouse.X, mouse.Y, true) {
		return m, tea.Batch(cmds...)
	}

	// Root-host pointer capture: drags (chat selection, scrollbar thumbs,
	// split dividers, input selection, deck gutters) continue outside their
	// original bounds until release, using one capture service for the whole shell.
	if m.rootHost != nil && m.rootHost.HasPointerCapture() {
		m.clearPlanHover()
		m.clearConnectionHover()
		res := m.rootHost.Handle(msg)
		if cmd := m.applyHostPointerActions(res); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if res.Cmd != nil {
			cmds = append(cmds, res.Cmd)
		}
		return m, tea.Batch(cmds...)
	}

	if m.routeConnectionHoverMotion(msg) {
		return m, tea.Batch(cmds...)
	}
	if m.routePlanHoverMotion(msg) {
		return m, tea.Batch(cmds...)
	}
	// A drag that began in a window (editor selection, scrollbar) follows the
	// pointer outside it until release, as root-host capture does.
	if fw := m.capturingWindow(); fw != nil {
		if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	if m.windowManager != nil {
		// Hover uses the same z-order authority as click and wheel; creation
		// order can differ after a user raises an older pane window.
		if fw := m.windowManager.WindowAt(mouse.X, mouse.Y); fw != nil {
			if m.chatScreen != nil {
				m.chatScreen.ClearPaneGutterHover()
			}
			m.handlePopupContentHover(fw, mouse.X, mouse.Y, mouse.Button)
			return m, tea.Batch(cmds...)
		}
	}
	if m.blockingDeckShieldsPointer(mouse.X, mouse.Y, true) {
		return m, tea.Batch(cmds...)
	}

	region := m.classifyShellRegion(mouse.X, mouse.Y, true)
	if region != regionChat && m.chatScreen != nil {
		m.chatScreen.ClearPaneGutterHover()
	}

	// The root Host is the one hover authority for every shell region (dock,
	// header, footer and launcher included); motion is presentation-coalesced
	// by the frame gate, so routing it everywhere costs no extra frames.
	switch region {
	case regionEditor:
		if m.isEditorActive() {
			cmd := m.handleShellPointer(msg)
			m.updateDiagHoverPopup(mouse.X, mouse.Y)
			return m, cmd
		}
	case regionInput, regionChat:
		if m.Input.InputDragging() {
			return m, tea.Batch(m.routeInputMouse(msg)...)
		}
	case regionLauncher:
		m.LastInteractionTime = time.Now()
	}
	if region != regionSidebar && m.Tree.Model != nil {
		m.Tree.Model.Hover.Clear()
	}
	if region != regionTabBar {
		m.Win.Hover.Clear()
	}
	if region != regionEditor {
		m.clearDiagHoverPopup()
	}
	return m, m.handleShellPointer(msg)
}

// capturingWindow is the window whose content host holds pointer capture.
func (m *App) capturingWindow() *wm.FloatingWindow {
	if m.windowManager == nil {
		return nil
	}
	windows := m.windowManager.Windows()
	for i := len(windows) - 1; i >= 0; i-- {
		if host := windowContentHost(windows[i]); host != nil && host.HasPointerCapture() {
			return windows[i]
		}
	}
	return nil
}

// windowContentHost returns the component Host behind a floating window's
// content: a WindowFrame body or a ComponentWidget overlay.
func windowContentHost(fw *wm.FloatingWindow) *ui.Host {
	if fw == nil {
		return nil
	}
	switch content := fw.Content.(type) {
	case *ui.WindowFrameWidget:
		return content.Host()
	case *ui.ComponentWidget:
		return content.Host()
	}
	return nil
}

// pointerReleaseWindow returns the floating window that owns a release: the
// one whose content Host captured the pointer (passive popovers capture drags
// without ever taking focus), else the focused window.
func (m *App) pointerReleaseWindow() *wm.FloatingWindow {
	if fw := m.capturingWindow(); fw != nil {
		return fw
	}
	return m.windowManager.Focused()
}

func (m *App) dispatchMouseRelease(msg tea.MouseReleaseMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	mouse := msg.Mouse()

	if m.windowManager != nil {
		released := m.windowManager.HandleMouseRelease(msg)
		// If a drag or resize just ended, don't forward the release event to content.
		// The content never received the initial click (dispatchMouseClick returns
		// early when IsDragging()), so forwarding a stray release would confuse it.
		if released == nil {
			if fw := m.pointerReleaseWindow(); fw != nil {
				host := windowContentHost(fw)
				owned := host != nil && host.HasPointerCapture()
				if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if owned {
					// The window owned this gesture; the shell beneath never saw
					// its press and must not receive its release.
					return m, tea.Batch(cmds...)
				}
			}
		}
	}
	if m.blockingDeckShieldsPointer(mouse.X, mouse.Y, true) {
		return m, tea.Batch(cmds...)
	}

	// Root-host capture release: ends drags (chat selection, scrollbar,
	// split dividers, input selection, deck gutters) wherever the pointer is.
	if m.rootHost != nil && m.rootHost.HasPointerCapture() {
		res := m.rootHost.Handle(msg)
		if cmd := m.applyHostPointerActions(res); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if res.Cmd != nil {
			cmds = append(cmds, res.Cmd)
		}
		return m, tea.Batch(cmds...)
	}

	region := m.classifyShellRegion(mouse.X, mouse.Y, false)

	switch region {
	case regionSessionDeck:
		return m, tea.Batch(append(cmds, m.handleShellPointer(msg))...)

	case regionEditor:
		cmd := m.handleShellPointer(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case regionInput:
		if m.Input.InputDragging() {
			cmds = append(cmds, m.routeInputMouse(msg)...)
			return m, tea.Batch(cmds...)
		}
		return m, tea.Batch(append(cmds, m.handleShellPointer(msg))...)

	case regionLauncher:
		if m.launcherHostVisible() {
			return m, tea.Batch(append(cmds, m.handleShellPointer(msg))...)
		}
		return m, tea.Batch(cmds...)

	case regionChat:
		if m.Input.InputDragging() {
			cmds = append(cmds, m.routeInputMouse(msg)...)
			return m, tea.Batch(cmds...)
		}
		return m, tea.Batch(append(cmds, m.handleShellPointer(msg))...)
	}

	return m, tea.Batch(cmds...)
}

// updateDiagHoverPopup checks the editor's hover state and shows/updates a
// floating popup window with diagnostic messages at the hovered position.
func (m *App) updateDiagHoverPopup(screenX, screenY int) {
	if m.windowManager == nil || !m.EditorState.Editor.IsOpen() {
		return
	}
	state := m.EditorState.Editor.HoverState()
	if len(state.Messages) == 0 {
		m.clearDiagHoverPopup()
		return
	}

	severityLabels := map[int]string{
		1: "Error",   // DiagError
		2: "Warning", // DiagWarning
		3: "Info",    // DiagInfo
		4: "Hint",    // DiagHint
	}
	severityIcons := map[int]string{
		1: "✕", // DiagError
		2: "⚠", // DiagWarning
		3: "ℹ", // DiagInfo
		4: "ℹ", // DiagHint
	}
	sevLabel, ok := severityLabels[state.Severity]
	if !ok {
		sevLabel = "Info"
	}
	sevIcon, ok := severityIcons[state.Severity]
	if !ok {
		sevIcon = "ℹ"
	}

	titleStr := fmt.Sprintf("%s:%d:%d", m.EditorState.Editor.BaseTitle(), state.Line+1, state.Col+1)
	sourceLine := strings.TrimRight(m.EditorState.Editor.LineText(state.Line), "\n\r ")

	// Build structured content lines.
	var diagLines []ui.DiagLine
	diagLines = append(diagLines, ui.DiagLine{
		Text: titleStr,
		Kind: ui.DiagLineTitle,
	})
	diagLines = append(diagLines,
		ui.DiagLine{Text: m.EditorState.Editor.DiagnosticStatus(), Kind: ui.DiagLineBody},
		ui.DiagLine{Text: m.EditorState.Editor.DiagnosticProvenance(), Kind: ui.DiagLineBody},
	)
	if sourceLine != "" {
		diagLines = append(diagLines, ui.DiagLine{
			Text: sourceLine,
			Kind: ui.DiagLineSource,
		})
	}

	// Compute width from content.
	maxLineW := rendering.VisibleWidth(titleStr)
	if w := rendering.VisibleWidth(sourceLine); w > maxLineW {
		maxLineW = w
	}
	for _, msg := range state.Messages {
		if w := rendering.VisibleWidth(msg); w > maxLineW {
			maxLineW = w
		}
	}
	boxW := maxLineW + 6
	if boxW < 44 {
		boxW = 44
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	innerW := boxW - 6

	// Divider
	diagLines = append(diagLines, ui.DiagLine{
		Text: ui.DiagLineDividerText(innerW),
		Kind: ui.DiagLineDivider,
	})

	// Body lines (word-wrapped).
	for _, msg := range state.Messages {
		diagLines = append(diagLines, ui.DiagLine{
			Text: msg,
			Kind: ui.DiagLineBody,
		})
	}

	// Map severity to DiagSeverity for the frame.
	diagSev := ui.DiagSevInfo
	switch state.Severity {
	case 1:
		diagSev = ui.DiagSevError
	case 2:
		diagSev = ui.DiagSevWarning
	}

	popupH := len(diagLines) + 2
	popupX, popupY := m.diagPopupPosition(screenX, screenY, boxW, popupH)

	frame := ui.BuildDiagnosticFrame(m.Theme, ui.DiagFrameSpec{
		Title:    sevLabel,
		Icon:     sevIcon,
		Severity: diagSev,
		Lines:    diagLines,
		Width:    boxW,
		Height:   popupH,
	})

	// Create or update the popup window.
	if m.DiagPopupID != "" {
		if existing := m.windowManager.Window(m.DiagPopupID); existing != nil {
			existing.SetContent(frame)
			existing.SetSize(boxW, popupH)
			existing.SetPosition(popupX, popupY)
			existing.MarkContentDirty()
			return
		}
	}

	fw := wm.NewFloatingWindow(
		string(ui.OverlayDiagHover),
		"",
		frame,
		boxW, popupH,
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(false),
		wm.WithFocusable(false),
	)
	fw.SetPosition(popupX, popupY)
	m.overlayHost.Open(ui.OverlayDiagHover, fw, ui.PopoverPolicy(false), func() {
		m.DiagPopupID = ""
	})
	m.DiagPopupID = "diag-hover"
}

// updateDiagHoverPopupFromCursor shows the diagnostic hover popup at the
// keyboard cursor position.
func (m *App) updateDiagHoverPopupFromCursor() {
	layout := m.computeLayout()
	screenX, screenY := m.EditorState.Editor.CursorScreenPosition(layout.SidebarW, layout.ChatAreaTop)
	m.updateDiagHoverPopup(screenX, screenY)
}

func (m *App) diagPopupPosition(screenX, screenY, w, h int) (int, int) {
	popupX := screenX + 2
	popupY := screenY + 1
	if popupX+w >= m.width {
		popupX = screenX - w - 2
	}
	if popupY+h >= m.height {
		popupY = screenY - h - 1
	}
	if popupX < 0 {
		popupX = 0
	}
	if popupY < 0 {
		popupY = 0
	}
	return popupX, popupY
}

// clearDiagHoverPopup removes the diagnostic hover popup, if any.
func (m *App) clearDiagHoverPopup() {
	m.overlayHost.Close(ui.OverlayDiagHover)
}

// dispatchMouseRightClick shows a context menu appropriate to the
// region the user clicked. If a context menu is already open, the
// second right-click closes it (so the user can dismiss with the
// same gesture). Otherwise the menu opens at the cursor with
// region-aware items.
func (m *App) dispatchMouseRightClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()

	// If a context menu is already open, close it and return.
	if m.windowManager != nil {
		if fw := m.windowManager.Window("context-menu"); fw != nil {
			m.overlayHost.Close(ui.OverlayContextMenu)
			return m, nil
		}
	}

	// If a different floating window was hit (composer, command
	// palette, find/replace, etc.), its text controls offer whole-text
	// Copy/Cut; anything else keeps its own right-click behavior.
	if m.windowManager != nil {
		if hitWin := m.windowManager.HandleMouse(msg); hitWin != nil {
			if id, ok := ui.TerminalIDFromOverlay(ui.OverlayID(hitWin.ID)); ok {
				m.openTerminalControlMenu(id)
				return m, nil
			}
			if group, ok := paneGroupFromOverlay(ui.OverlayID(hitWin.ID)); ok && m.Win.IsFloating(group) {
				if st := m.paneWindowContextMenu(group, mouse); st != nil {
					return m, m.openContextMenuState(mouse, st)
				}
				return m, nil
			}
			if hit, ok := windowTextClipboardHit(hitWin, mouse); ok {
				return m, m.openContextMenuState(mouse, m.textClipboardMenu(hitWin.Title, hit))
			}
			// Right-click on a floating window: do nothing for now.
			return m, nil
		}
	}
	if m.blockingDeckShieldsPointer(mouse.X, mouse.Y, false) {
		return m, nil
	}
	m.activatePaneAt(mouse.X, mouse.Y)
	if m.blockingDeckShieldsPointer(mouse.X, mouse.Y, false) {
		return m, nil
	}
	// A session deck panel covers the chat: a right-click inside it belongs to
	// the panel's own controls (question write-ins), never to the message rows
	// painted behind it.
	if hit, ok := m.deckTextClipboardHit(mouse); ok {
		return m, m.openContextMenuState(mouse, m.textClipboardMenu("", hit))
	}

	region := m.classifyShellRegion(mouse.X, mouse.Y, false)
	var st *ui.ContextMenuState
	if g := m.paneAt(mouse.X, mouse.Y); g != nil && m.Win.GroupActiveTab(g.id) == nil && !m.launcherHostVisible() {
		return m, m.openContextMenuState(mouse, m.emptyPaneMenu())
	}
	switch region {
	case regionTermDock:
		if m.termDock != nil && m.termDock.TerminalActive() {
			m.openTerminalControlMenu(m.termDock.ActiveID())
			return m, nil
		}
	case regionEditor:
		st = m.buildEditorContextMenu(mouse)
	case regionSidebar:
		st = m.buildActivityRegionContextMenu(mouse)
	case regionTabBar:
		st = m.buildTabBarContextMenu(mouse)
	case regionInput:
		st = m.buildInputContextMenu(mouse)
	case regionChat:
		st = m.buildChatContextMenu(mouse)
	case regionLauncher:
		st = m.buildLauncherContextMenu(mouse)
	default:
		st = nil
	}
	// Any text control under the pointer (tree filter, launcher search,
	// activity filter, question-deck write-in) offers the same whole-text
	// Copy/Cut, so inline inputs never need a bespoke menu builder.
	if hit, ok := m.textClipboardHitAt(mouse); ok {
		if st == nil {
			st = &ui.ContextMenuState{Title: "Text"}
		}
		st.Sections = append(st.Sections, m.textClipboardMenuSection(hit))
	}
	return m, m.openContextMenuState(mouse, st)
}

// textClipboardMenu builds the popup for a text control that has no
// region-specific menu of its own (a field inside a floating window).
func (m *App) textClipboardMenu(title string, hit textClipboardHit) *ui.ContextMenuState {
	if title == "" {
		title = "Text"
	}
	st := &ui.ContextMenuState{Title: title}
	st.Sections = append(st.Sections, m.textClipboardMenuSection(hit))
	return st
}

// openContextMenuState measures, positions, and opens a context menu popup at
// the pointer. A nil or empty state is a no-op.
func (m *App) openContextMenuState(mouse tea.Mouse, st *ui.ContextMenuState) tea.Cmd {
	if st == nil || len(st.Sections) == 0 {
		return nil
	}
	st.Build()
	if len(st.FlatItems) == 0 {
		return nil
	}

	// Position the popup near the cursor without going off-screen.
	// WindowFrame + ui.List (no PopupChrome).
	frame := ui.BuildContextMenuFrame(m.Theme, st)
	w, h := frame.Measure(60, 40)
	// Prefer computed chrome sizes from state for tight menus.
	if cw := ui.ContextMenuPopupWidth(st); cw > 0 {
		w = cw
	}
	if ch := ui.ContextMenuPopupHeight(st); ch > 0 {
		h = ch
	}
	w, h = m.contextMenuSize(w, h)
	x, y := m.clampShellPopupPosition(mouse.X, mouse.Y, w, h)

	fw := wm.NewFloatingWindow(string(ui.OverlayContextMenu), "", frame, w, h,
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideCloseRoute),
	)
	fw.SetPosition(x, y)
	m.overlayHost.Open(ui.OverlayContextMenu, fw, ui.PopoverPolicy(true), func() {
		m.LastContextMenu = nil
	})
	m.LastContextMenu = st
	// OpenedAt retained for any remaining animation consumers; WindowFrame
	// path does not animate reveal.
	m.ContextMenuOpenedAt = time.Now()
	return nil
}

// Lists scroll inside the shell; a large layout's scope menu must not extend
// offscreen or leave its final actions unreachable by pointer.
func (m *App) contextMenuSize(w, h int) (int, int) {
	insets := m.frameInsets()
	return min(w, max(1, m.width-insets.Left-insets.Right)), min(h, max(1, m.height-insets.Top-insets.Bottom))
}

// clampPopupPosition returns a (x, y) for the popup that anchors at
// the cursor but stays within the screen bounds. The popup is
// positioned so its top-left is at (mx, my); if it would extend past
// the right or bottom, it's pulled back.
func clampPopupPosition(mx, my, w, h, screenW, screenH int) (int, int) {
	x := mx
	y := my
	if x+w > screenW {
		x = screenW - w
	}
	if y+h > screenH {
		y = screenH - h
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

// clampShellPopupPosition is clampPopupPosition with the shell frame applied:
// context menus and other anchored popups never sit on the frame cells.
func (m *App) clampShellPopupPosition(mx, my, w, h int) (int, int) {
	frame := m.frameInsets()
	x, y := clampPopupPosition(mx, my, w, h, m.width-frame.Right, m.height-frame.Bottom)
	if x < frame.Left {
		x = frame.Left
	}
	if y < frame.Top {
		y = frame.Top
	}
	return x, y
}
