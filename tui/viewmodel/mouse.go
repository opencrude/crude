package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/tui/widgets"
	"crude/tui/wm"
)

type mouseRegion int

const (
	regionFloatingWindow mouseRegion = iota
	regionSidebar
	regionSidebarDivider
	regionTabBar
	regionSessionDeck
	regionEditor
	regionInput
	regionChat
	regionStatusBar
	regionLauncher
	regionNone
)

func (m *App) classifyMouse(x, y int, isWheel bool) mouseRegion {
	if m.windowManager != nil {
		if isWheel {
			if m.windowManager.HandleMouseWheel(tea.MouseWheelMsg{X: x, Y: y}) != nil {
				return regionFloatingWindow
			}
		} else {
			if hitWin := m.windowManager.HandleMouse(tea.MouseClickMsg{X: x, Y: y}); hitWin != nil {
				return regionFloatingWindow
			}
		}
	}
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

	if m.Tree.Visible && m.Tree.Model != nil && x < m.Tree.Width {
		if x == m.Tree.Width {
			return regionSidebarDivider
		}
		return regionSidebar
	}

	if !isWheel && m.Win.IsOpen() && m.Win.TabCount() >= 1 && y == 3 {
		return regionTabBar
	}

	layout := m.computeLayout()
	if m.sessionDeckRect.Contains(x, y) {
		return regionSessionDeck
	}

	if m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen() {
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
	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" && m.launcher != nil {
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
		tabBarExtra := 0
		if m.Win.IsOpen() && m.Win.TabCount() >= 1 {
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
	layout := m.computeLayout()
	translated := translateMouseY(msg, -layout.ChatAreaTop)
	translated = translateMouseX(translated, -layout.SidebarW)
	translated = translateMouseX(translated, -m.EditorState.Editor.PadL)
	return m.EditorState.Editor.HandleEvent(translated)
}

func (m *App) routeSidebarMouse(msg tea.Msg) tea.Cmd {
	translated := m.translateToTreeMouse(msg)
	return m.Tree.Model.Update(filetree.TreeMouseMsg{Mouse: translated})
}

// routeTreeScrollbarMsg translates a mouse event from screen coordinates to
// scrollbar-relative coordinates (Y=0 at first tree content row) and forwards
// it to the tree renderer's scrollbar handler.
func (m *App) routeTreeScrollbarMsg(msg tea.Msg) tea.Cmd {
	bcOff := m.breadcrumbOffset()
	adjusted := msg
	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		mouse := mm.Mouse()
		adjusted = tea.MouseClickMsg{X: mm.X, Y: mouse.Y - m.Tree.YOffset - bcOff, Button: mm.Button}
	case tea.MouseMotionMsg:
		mouse := mm.Mouse()
		adjusted = tea.MouseMotionMsg{X: mm.X, Y: mouse.Y - m.Tree.YOffset - bcOff, Button: mm.Button}
	case tea.MouseReleaseMsg:
		mouse := mm.Mouse()
		adjusted = tea.MouseReleaseMsg{X: mm.X, Y: mouse.Y - m.Tree.YOffset - bcOff, Button: mm.Button}
	}
	return m.Tree.Renderer.HandleScrollbarMsg(adjusted)
}

// breadcrumbOffset returns 1 when the tree is scrolled (breadcrumb visible),
// 0 otherwise.
func (m *App) breadcrumbOffset() int {
	if m.Tree.Model.ScrollOffset() > 0 {
		return 1
	}
	return 0
}

func (m *App) routeChatMouse(msg tea.Msg, mouse tea.Mouse, isLeftClick bool) []tea.Cmd {
	var cmds []tea.Cmd
	activeML := m.activeMessageList()
	canType := false
	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil {
		canType = !n.State.Completed && n.State.IsActive()
	}

	if m.Input.InputSelecting() {
		updated, cmd, evt := m.Input.Update(msg, canType && !m.OverlayState.IsBlocking(), m.OverlayState.IsBlocking(), m.ChatAreaHeight, m.Input.ShowHint())
		m.Input = updated
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, m.handleInputEvent(evt)...)
	} else {
		handled, paneCmd := m.routeMouseToInlinePane(msg)
		if paneCmd != nil {
			cmds = append(cmds, paneCmd)
		}
		if !handled && activeML != nil {
			_, cmd := activeML.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

func (m *App) routeInputMouse(msg tea.Msg) []tea.Cmd {
	var cmds []tea.Cmd
	canType := false
	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil {
		canType = !n.State.Completed && n.State.IsActive()
	}
	updated, cmd, evt := m.Input.Update(msg, canType && !m.OverlayState.IsBlocking(), m.OverlayState.IsBlocking(), m.ChatAreaHeight, m.Input.ShowHint())
	m.Input = updated
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, m.handleInputEvent(evt)...)
	return cmds
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
			if m.OverlayState.Is(ModeTextOverlay) {
				overlay := m.sessionTextOverlay()
				if overlay == nil {
					return m, tea.Batch(cmds...)
				}
				updated, cmd, action := overlay.Update(msg)
				*overlay = updated
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				switch action {
				case widgets.TextOverlaySave:
					m.saveReferenceFromOverlay()
				case widgets.TextOverlayCancel:
					overlay.Close()
					m.setMode(ModeChat)
				}
				hitWin.MarkContentDirty()
			} else if cmd := m.handlePopupContentClick(hitWin, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			// Mark popup content dirty so the click effect is rendered.
			hitWin.MarkContentDirty()
			return m, tea.Batch(cmds...)
		}
	}

	region := m.classifyMouse(mouse.X, mouse.Y, false)

	switch region {
	case regionSidebar:
		m.setSessionDeckFocused(false)
		m.FocusStack.Clear()
		m.FocusStack.Push(FocusTree)
		localY := mouse.Y - m.Tree.YOffset
		if localY >= 0 {
			if mouse.X == m.Tree.Width-1 {
				cmd := m.routeTreeScrollbarMsg(msg)
				return m, cmd
			}
			if mouse.X >= m.Tree.Width-1-filetree.ScrollbarGutterWidth {
				m.Tree.Model.Hover.Clear()
				return m, nil
			}
			cmd := m.routeSidebarMouse(msg)
			return m, cmd
		}
		return m, nil

	case regionSidebarDivider:
		m.setSessionDeckFocused(false)
		if msg.Button == tea.MouseLeft {
			m.Tree.Resizing = true
			m.Tree.ResizeStartX = mouse.X
			m.Tree.ResizeStartWidth = m.Tree.Width
		}
		return m, nil

	case regionTabBar:
		m.setSessionDeckFocused(false)
		m.FocusStack.Clear()
		m.FocusStack.Push(FocusTabBar)
		adjustedX := mouse.X
		if m.Tree.Visible && m.Tree.Model != nil {
			adjustedX -= m.Tree.Width + 1
		}
		adjustedX -= m.Theme.Spacing.PadLR
		if adjustedX < 0 {
			adjustedX = 0
		}
		hit := m.Win.HitTest(adjustedX, 0)
		if hit.TabIndex >= 0 {
			if hit.IsClose {
				cmds = append(cmds, m.handleAgentTabClose(hit.TabIndex))
				return m, tea.Batch(cmds...)
			}
			m.Win.SwitchTo(hit.TabIndex)
			m.handleTabSwitch()
			return m, nil
		}

	case regionEditor:
		m.unfocus()
		cmd := m.routeEditorMouse(msg)
		m.updateDiagHoverPopup(mouse.X, mouse.Y)
		if cmd != nil {
			return m, cmd
		}
		return m, nil

	case regionInput:
		m.unfocus()
		cmds = append(cmds, m.routeInputMouse(msg)...)
		return m, tea.Batch(cmds...)

	case regionChat:
		m.unfocus()
		cmds = append(cmds, m.routeChatMouse(msg, mouse, msg.Button == tea.MouseLeft)...)
		return m, tea.Batch(cmds...)

	case regionSessionDeck:
		m.setSessionDeckFocused(true)
		return m, m.handleSessionDeckEvent(m.translateSessionDeckMouse(msg))

	case regionStatusBar:
		m.setSessionDeckFocused(false)
		// Left-click on the status bar is a no-op (it's a passive
		// display, not an interactive region). Right-click goes
		// through dispatchMouseRightClick instead.
		return m, nil

	case regionLauncher:
		m.setSessionDeckFocused(false)
		// Launcher click: hit-test the screen Y to find the rendered
		// line, then translate that to a flat item index. Single-click
		// selects the item; double-click activates it. We use the
		// standard "click to select" UX, then the user can press Enter
		// or click again to activate. (Single-click activating is too
		// aggressive — a misclick on the New Session box would lose
		// any in-flight context.)
		return m.handleLauncherClick(mouse.X, mouse.Y)
	}

	if m.FocusStack.Top() == FocusTree && mouse.X >= m.Tree.Width {
		m.FocusStack.Pop()
	}

	return m, nil
}

func (m *App) dispatchMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if m.windowManager != nil {
		if hitWin := m.windowManager.HandleMouseWheel(msg); hitWin != nil {
			if m.OverlayState.Is(ModeTextOverlay) {
				if overlay := m.sessionTextOverlay(); overlay != nil {
					updated, cmd, _ := overlay.Update(msg)
					*overlay = updated
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
				hitWin.MarkContentDirty()
			} else {
				if cmd := m.forwardEventToWindowContent(hitWin, msg); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			return m, tea.Batch(cmds...)
		}
	}
	region := m.classifyMouse(msg.X, msg.Y, true)

	switch region {
	case regionSidebar:
		cmd := m.routeSidebarMouse(msg)
		return m, cmd

	case regionEditor:
		m.EditorState.Editor.HandleEvent(msg)
		return m, nil

	case regionChat:
		activeML := m.activeMessageList()
		if activeML != nil {
			_, cmd := activeML.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case regionSessionDeck:
		return m, m.handleSessionDeckEvent(m.translateSessionDeckMouse(msg))

	case regionLauncher:
		layout := m.computeLayout()
		x := msg.X - layout.SidebarW - layout.PadLR
		y := msg.Y - layout.ChatAreaTop
		if m.launcher != nil && m.launcher.workspacePaneContains(x, y) {
			m.LastInteractionTime = time.Now()
			delta := 0
			if msg.Button == tea.MouseWheelUp {
				delta = -3
			} else if msg.Button == tea.MouseWheelDown {
				delta = 3
			}
			if delta != 0 {
				m.launcher.scrollBy(delta, m.launcher.workspaceViewportH)
			}
		}
		return m, nil

	default:
	}

	return m, nil
}

func (m *App) dispatchMouseMotion(msg tea.MouseMotionMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	mouse := msg.Mouse()

	if m.windowManager != nil {
		m.windowManager.HandleMouseMotion(msg)
		if m.windowManager.IsDragging() {
			return m, tea.Batch(cmds...)
		}
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

	if m.Tree.Resizing {
		deltaX := mouse.X - m.Tree.ResizeStartX
		newWidth := m.Tree.ResizeStartWidth + deltaX
		minW := 12
		if newWidth < minW {
			newWidth = minW
		}
		if newWidth > m.width/2 {
			newWidth = m.width / 2
		}
		m.Tree.Width = newWidth
		m.Tree.Model.Update(filetree.TreeWidthMsg{Width: newWidth})
		return m, nil
	}

	// Launcher: when the launcher screen is active (no agents, no
	// editor), route motion directly to the launcher hover handler
	// and return. This is a critical performance fix: without this
	// early return, every mouse-motion event over the launcher
	// would fall through to the tree-hover-clear, tab-bar-hover-
	// clear, clearDiagHoverPopup, and routeChatMouse paths — each
	// of which does non-trivial work on every pixel of mouse
	// movement.
	//
	// Before returning, clear tree and tab-bar hover state so the
	// stale highlight doesn't persist when the mouse leaves those
	// areas for the launcher.
	if m.launcher != nil && m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		// Mouse is over the file tree — route there instead.
		if m.Tree.Visible && m.Tree.Model != nil && mouse.X < m.Tree.Width {
			if mouse.X == m.Tree.Width-1 {
				cmd := m.routeTreeScrollbarMsg(msg)
				return m, cmd
			}
			if mouse.X >= m.Tree.Width-1-filetree.ScrollbarGutterWidth {
				m.Tree.Model.Hover.Clear()
				return m, nil
			}
			cmd := m.routeSidebarMouse(msg)
			return m, cmd
		}
		// Clear tree hover (mouse left the tree area).
		if m.Tree.Model != nil {
			m.Tree.Model.Hover.Clear()
		}
		// Clear tab bar hover (mouse left the tab row).
		m.Win.Hover.Clear()
		m.handleLauncherHover(mouse.X, mouse.Y)
		return m, tea.Batch(cmds...)
	}

	if m.Tree.Visible && m.Tree.Model != nil && mouse.X < m.Tree.Width {
		if mouse.X == m.Tree.Width-1 {
			cmd := m.routeTreeScrollbarMsg(msg)
			return m, cmd
		}
		if mouse.X >= m.Tree.Width-1-filetree.ScrollbarGutterWidth {
			m.Tree.Model.Hover.Clear()
			return m, nil
		}
		cmd := m.routeSidebarMouse(msg)
		return m, cmd
	}

	// Mouse is outside the tree — clear the hover state.
	if m.Tree.Model != nil {
		m.Tree.Model.Hover.Clear()
	}

	// Tab bar hover: update the hover state when the mouse moves over
	// the horizontal tab bar (row 3). Use HitTest to find the tab under
	// the cursor, with the same X-adjustment used in dispatchMouseClick.
	if m.Win.IsOpen() && m.Win.TabCount() >= 1 && mouse.Y == 3 {
		adjustedX := mouse.X
		if m.Tree.Visible && m.Tree.Model != nil {
			adjustedX -= m.Tree.Width + 1
		}
		adjustedX -= m.Theme.Spacing.PadLR
		if adjustedX < 0 {
			adjustedX = 0
		}
		hit := m.Win.HitTest(adjustedX, 0)
		if hit.TabIndex >= 0 {
			m.Win.Hover.Set(hit.TabIndex)
			return m, tea.Batch(cmds...)
		}
	}

	// Mouse is outside the tab bar — clear the hover state.
	m.Win.Hover.Clear()
	if m.sessionDeckRect.Contains(mouse.X, mouse.Y) {
		return m, m.handleSessionDeckEvent(m.translateSessionDeckMouse(msg))
	}

	// Overlay hover: forward mouse motion to the active overlay's
	// interactive element (currently only the Confirm dialog's
	// ButtonGroup). This is what gives the confirm dialog buttons
	// the "live" hover background that makes the rest of the app
	// feel like a real GUI.
	//
	// Only fire this when the mouse is actually inside the overlay
	// region — the classifyMouse() call would also be expensive
	// for every motion event, so we use a cheap bounds check first.
	if m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen() {
		// Cheap Y-bounds check instead of full classifyMouse:
		// classifyMouse checks WM, sidebar, tab bar, overlay, editor,
		// launcher, input, and chat — but all those except "editor
		// vs chat" have already been handled above (tree, tabs,
		// overlay, etc.). We only need to know: is the mouse in
		// the editor's Y range?
		layout := m.computeLayout()
		if mouse.Y >= layout.ChatAreaTop && mouse.Y < layout.ChatAreaTop+m.ChatAreaHeight && mouse.X >= layout.SidebarW {
			cmd := m.routeEditorMouse(msg)
			m.updateDiagHoverPopup(mouse.X, mouse.Y)
			m.contentDirty = true
			if cmd != nil {
				return m, cmd
			}
			return m, nil
		}
	}

	// Mouse moved away from editor — clear any diagnostic popup.
	m.clearDiagHoverPopup()

	if m.OverlayState.IsChat() {
		activeML := m.activeMessageList()
		if activeML != nil && activeML.IsScrollbarDragging() {
			_, cmd := activeML.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		cmds = append(cmds, m.routeChatMouse(msg, mouse, false)...)
		return m, tea.Batch(cmds...)
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

	if m.Tree.Resizing {
		m.Tree.Resizing = false
		return m, nil
	}

	if m.Tree.Visible && m.Tree.Model != nil && mouse.X < m.Tree.Width {
		if mouse.X == m.Tree.Width-1 {
			cmd := m.routeTreeScrollbarMsg(msg)
			return m, cmd
		}
		if mouse.X >= m.Tree.Width-1-filetree.ScrollbarGutterWidth {
			m.Tree.Model.Hover.Clear()
			return m, nil
		}
		cmd := m.routeSidebarMouse(msg)
		return m, cmd
	}

	region := m.classifyMouse(mouse.X, mouse.Y, false)

	switch region {
	case regionSessionDeck:
		return m, m.handleSessionDeckEvent(m.translateSessionDeckMouse(msg))

	case regionEditor:
		cmd := m.routeEditorMouse(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case regionInput:
		cmds = append(cmds, m.routeInputMouse(msg)...)
		return m, tea.Batch(cmds...)

	case regionLauncher:
		// Launcher hover: pre-select the item under the cursor so
		// the user can click to confirm. (Single-click already
		// selects via handleLauncherClick; this gives a "click-to-
		// select, click-again-to-activate" UX with hover preview.)
		m.handleLauncherHover(mouse.X, mouse.Y)
		return m, tea.Batch(cmds...)

	case regionChat:
		activeML := m.activeMessageList()
		if activeML != nil && activeML.IsScrollbarDragging() {
			_, cmd := activeML.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		cmds = append(cmds, m.routeChatMouse(msg, mouse, false)...)
		return m, tea.Batch(cmds...)
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
	var diagLines []widgets.DiagLine
	diagLines = append(diagLines, widgets.DiagLine{
		Text: titleStr,
		Kind: widgets.DiagLineTitle,
	})
	if sourceLine != "" {
		diagLines = append(diagLines, widgets.DiagLine{
			Text: sourceLine,
			Kind: widgets.DiagLineSource,
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
	diagLines = append(diagLines, widgets.DiagLine{
		Text: widgets.DiagLineDividerText(innerW),
		Kind: widgets.DiagLineDivider,
	})

	// Body lines (word-wrapped).
	for _, msg := range state.Messages {
		diagLines = append(diagLines, widgets.DiagLine{
			Text: msg,
			Kind: widgets.DiagLineBody,
		})
	}

	// Map severity to DiagSeverity for the frame.
	diagSev := widgets.DiagSevInfo
	switch state.Severity {
	case 1:
		diagSev = widgets.DiagSevError
	case 2:
		diagSev = widgets.DiagSevWarning
	}

	popupH := len(diagLines) + 2
	popupX, popupY := m.diagPopupPosition(screenX, screenY, boxW, popupH)

	frame := widgets.BuildDiagnosticFrame(m.Theme, widgets.DiagFrameSpec{
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

// padRight pads a string to the given visible width with spaces on the right.
func padRight(s string, width int) string {
	pad := width - rendering.VisibleWidth(s)
	if pad < 0 {
		pad = 0
	}
	return s + strings.Repeat(" ", pad)
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

	region := m.classifyMouse(mouse.X, mouse.Y, false)
	var st *widgets.ContextMenuState
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
	frame := widgets.BuildContextMenuFrame(m.Theme, st)
	w, h := frame.Measure(60, 40)
	// Prefer computed chrome sizes from state for tight menus.
	if cw := widgets.ContextMenuPopupWidth(st); cw > 0 {
		w = cw
	}
	if ch := widgets.ContextMenuPopupHeight(st); ch > 0 {
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
