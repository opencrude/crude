package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/shelleditor"
	"crude/tui/screens/shellinput"
	"crude/tui/ui"
	"crude/tui/wm"
)

// shellRegion classifies a screen point for VIEWMODEL POLICY only (focus
// stack updates, editor legacy routing, status row, context menus). Actual
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
	regionStatusBar
	regionLauncher
)

func (m *App) classifyShellRegion(x, y int, isWheel bool) shellRegion {
	if m.sessionDeckBlocking() {
		return regionSessionDeck
	}

	// Status bar is always the last visible row. Treat it as its
	// own region so right-click can offer LSP/session controls.
	// Placed after the floating-window check so a popup covering
	// the status bar (e.g. command palette) wins.
	if m.height > 0 && y == m.height-1 {
		return regionStatusBar
	}

	if m.termDockRect.Contains(x, y) {
		return regionTermDock
	}

	if x < m.sidebarHitWidth() {
		return regionSidebar
	}

	if !isWheel && m.tabBarVisible() && y == 3 {
		return regionTabBar
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
	// regions below — the launcher must NOT steal those clicks.
	// The file tree (sidebar) is already handled above (regionSidebar
	// check), so the launcher never interferes with it.
	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" && m.launcherScreen != nil {
		if y >= layout.ChatAreaTop && y < layout.ChatAreaTop+m.ChatAreaHeight {
			return regionLauncher
		}
		// Fall through: clicks below the chat area go to the input
		// region (so the user can still type / to filter).
	}

	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		return regionInput
	}

	if m.OverlayState.IsChat() {
		if !m.inputRect.Empty() && y >= m.inputRect.Y {
			return regionInput
		}
		tabBarExtra := 0
		if m.tabBarVisible() {
			tabBarExtra = 2
		}
		inputStartLine := m.ChatAreaHeight + 4 + tabBarExtra + m.sessionDeckRect.H
		if y >= inputStartLine {
			return regionInput
		}
	}

	return regionChat
}

func (m *App) routeEditorMouse(msg tea.Msg) tea.Cmd {
	if m.rootHost == nil {
		return nil
	}
	r, ok := m.rootHost.ScreenRect(shelleditor.ScreenID)
	if !ok || r.Empty() {
		return nil
	}
	// The editor pane origin comes from the Host's hit tree (one geometry
	// source); the editor's own PadL is the model-internal content offset
	// painted by the shelleditor screen.
	translated := translateMouseDelta(msg, -r.X-m.EditorState.Editor.PadL, -r.Y)
	return m.EditorState.Editor.HandleEvent(translated)
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
	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil {
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
	r, ok := m.rootHost.ScreenRect(shellinput.ScreenID)
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
		return m, nil // modal popup — suppress the click entirely
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
			if cmd := m.handlePopupContentClick(hitWin, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			// Mark popup content dirty so the click effect is rendered.
			hitWin.MarkContentDirty()
			return m, tea.Batch(cmds...)
		}
	}

	region := m.classifyShellRegion(mouse.X, mouse.Y, false)

	switch region {
	case regionStatusBar:
		// Passive display row; right-click is handled separately.
		m.setSessionDeckFocused(false)
		m.setTermDockFocused(false)
		return m, nil

	case regionEditor:
		m.unfocus()
		cmd := m.routeEditorMouse(msg)
		m.updateDiagHoverPopup(mouse.X, mouse.Y)
		return m, cmd

	case regionSessionDeck:
		m.setTermDockFocused(false)
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
		m.setSessionDeckFocused(false)
		m.setTermDockFocused(false)
		m.FocusStack.Clear()
		m.FocusStack.Push(FocusTabBar)
	case regionChat, regionInput:
		m.unfocus()
	case regionLauncher:
		m.setSessionDeckFocused(false)
		m.setTermDockFocused(false)
		m.LastInteractionTime = time.Now()
	}

	// Legacy input-model selection: while the chat input is drag-selecting,
	// a click outside the input must reach the input model so it cancels
	// the selection (click-away contract).
	if region == regionChat && m.Input.InputSelecting() {
		cmds = append(cmds, m.routeInputMouse(msg)...)
		return m, tea.Batch(cmds...)
	}

	res := m.rootHost.Handle(msg)
	if cmd := m.applyHostPointerActions(res); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if res.Cmd != nil {
		cmds = append(cmds, res.Cmd)
	}
	// Tab-bar clicks bubble typed tabs.* actions up through the shell tree;
	// the viewmodel translates them into window tab operations.
	if res.Action != nil {
		switch res.Action.ID {
		case "tabs.activate", "tabs.close", "tabs.enter":
			if cmd := m.handleTabBarHostResult(res); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
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
			if cmd := m.forwardEventToWindowContent(hitWin, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			hitWin.MarkContentDirty()
			return m, tea.Batch(cmds...)
		}
	}
	region := m.classifyShellRegion(msg.X, msg.Y, true)

	switch region {
	case regionSidebar:
		return m, m.handleShellPointer(msg)

	case regionEditor:
		m.EditorState.Editor.HandleEvent(msg)
		return m, nil

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

func (m *App) dispatchMouseMotion(msg tea.MouseMotionMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	mouse := msg.Mouse()

	if m.windowManager != nil {
		m.windowManager.HandleMouseMotion(msg)
		if m.windowManager.IsDragging() {
			return m, tea.Batch(cmds...)
		}
	}

	// Root-host pointer capture: drags (chat selection, scrollbar thumbs,
	// split dividers, input selection, deck gutters) continue outside their
	// original bounds until release — one capture service for the whole shell.
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

	if m.windowManager != nil {
		// Popup hover pre-selection: if the mouse is over a focusable
		// popup, update its hover state and return (don't route motion
		// to the underlying editor/tree). Iterate top-to-bottom.
		for i := len(m.windowManager.Windows()) - 1; i >= 0; i-- {
			fw := m.windowManager.Windows()[i]
			if !fw.Visible() || !fw.Focusable() {
				continue
			}
			if fw.ContainsPoint(mouse.X, mouse.Y) {
				m.handlePopupContentHover(fw, mouse.X, mouse.Y, mouse.Button)
				return m, tea.Batch(cmds...)
			}
		}
	}

	region := m.classifyShellRegion(mouse.X, mouse.Y, true)

	// Launcher: when the launcher screen is active (no agents, no
	// editor), route motion only inside the launcher or the file tree.
	// This is a critical performance fix: every motion event outside
	// those regions clears stale tree/tab hover and returns without
	// the launcher re-render churn.
	if m.launcherScreen != nil && m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		switch region {
		case regionSidebar:
			return m, m.handleShellPointer(msg)
		case regionLauncher:
			m.LastInteractionTime = time.Now()
			return m, m.handleShellPointer(msg)
		default:
			if m.Tree.Model != nil {
				m.Tree.Model.Hover.Clear()
			}
			m.Win.Hover.Clear()
			return m, nil
		}
	}

	switch region {
	case regionSidebar:
		return m, m.handleShellPointer(msg)

	case regionTabBar:
		// Tab hover is viewmodel state (Win.Hover) fed from the native
		// Tabs control after routing the motion through the shell tree.
		cmd := m.handleTabBarEvent(msg)
		return m, cmd

	case regionSessionDeck:
		return m, m.handleShellPointer(msg)

	case regionEditor:
		if m.isEditorActive() {
			// Also route the motion through the root host so its hover
			// state follows the pointer into the editor: without this,
			// sidebar rows / rail icons kept their hover tint after the
			// pointer left them (shelleditor itself is input-no-op, so
			// this is hover-only bookkeeping).
			if m.rootHost != nil {
				m.rootHost.Handle(msg)
			}
			cmd := m.routeEditorMouse(msg)
			m.updateDiagHoverPopup(mouse.X, mouse.Y)
			return m, cmd
		}
		return m, nil

	case regionInput:
		if m.Input.InputSelecting() {
			return m, tea.Batch(m.routeInputMouse(msg)...)
		}
		return m, m.handleShellPointer(msg)

	case regionChat:
		if m.Input.InputSelecting() {
			return m, tea.Batch(m.routeInputMouse(msg)...)
		}
		return m, m.handleShellPointer(msg)
	}

	// Mouse is outside every interactive region — clear stale hover.
	if m.Tree.Model != nil {
		m.Tree.Model.Hover.Clear()
	}
	m.Win.Hover.Clear()
	m.clearDiagHoverPopup()
	if m.OverlayState.IsChat() {
		return m, m.handleShellPointer(msg)
	}
	return m, nil
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
			if fw := m.windowManager.Focused(); fw != nil {
				if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
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
		return m, m.handleShellPointer(msg)

	case regionEditor:
		cmd := m.routeEditorMouse(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case regionInput:
		if m.Input.InputSelecting() {
			cmds = append(cmds, m.routeInputMouse(msg)...)
			return m, tea.Batch(cmds...)
		}
		return m, m.handleShellPointer(msg)

	case regionLauncher:
		if m.launcherHostVisible() {
			return m, m.handleShellPointer(msg)
		}
		return m, nil

	case regionChat:
		if m.Input.InputSelecting() {
			cmds = append(cmds, m.routeInputMouse(msg)...)
			return m, tea.Batch(cmds...)
		}
		return m, m.handleShellPointer(msg)
	}

	return m, nil
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

	titleStr := fmt.Sprintf("%s:%d:%d", m.EditorState.Editor.Title(), state.Line+1, state.Col+1)
	sourceLine := strings.TrimRight(m.EditorState.Editor.LineText(state.Line), "\n\r ")

	// Build structured content lines.
	var diagLines []ui.DiagLine
	diagLines = append(diagLines, ui.DiagLine{
		Text: titleStr,
		Kind: ui.DiagLineTitle,
	})
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
	// palette, find/replace, etc.), let it handle its own context
	// menu if any. For now, the find/replace popup also gets a
	// dismiss-on-right-click behavior.
	if m.windowManager != nil {
		if hitWin := m.windowManager.HandleMouse(msg); hitWin != nil {
			// Right-click on a floating window: do nothing for now.
			return m, nil
		}
	}

	region := m.classifyShellRegion(mouse.X, mouse.Y, false)
	var st *ui.ContextMenuState
	switch region {
	case regionEditor:
		st = m.buildEditorContextMenu(mouse)
	case regionSidebar:
		st = m.buildSidebarContextMenu(mouse)
	case regionTabBar:
		st = m.buildTabBarContextMenu(mouse)
	case regionInput:
		st = m.buildInputContextMenu(mouse)
	case regionChat:
		st = m.buildChatContextMenu(mouse)
	case regionStatusBar:
		st = m.buildStatusBarContextMenu(mouse)
	default:
		return m, nil
	}
	if st == nil || len(st.Sections) == 0 {
		return m, nil
	}
	st.Build()
	if len(st.FlatItems) == 0 {
		return m, nil
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
	x, y := clampPopupPosition(mouse.X, mouse.Y, w, h, m.width, m.height)

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
	return m, nil
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
