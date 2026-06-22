package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/widgets"
	"crude/tui/wm"
)

type mouseRegion int

const (
	regionFloatingWindow mouseRegion = iota
	regionSidebar
	regionSidebarDivider
	regionTabBar
	regionOverlay
	regionEditor
	regionInput
	regionChat
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

	if m.treeVisible && m.treeModel != nil && x < m.treeWidth {
		if x == m.treeWidth {
			return regionSidebarDivider
		}
		return regionSidebar
	}

	if !isWheel && m.win.IsOpen() && m.win.TabCount() >= 1 && y == 3 {
		return regionTabBar
	}

	layout := m.computeLayout()

	if m.overlay.IsOpen() && !isWheel {
		overlayStartLine := m.chatAreaHeight + 3
		if m.win.IsOpen() && m.win.TabCount() >= 1 {
			overlayStartLine += 2
		}
		if y >= overlayStartLine {
			return regionOverlay
		}
	}

	if m.activeEditorView != "" && m.editor.IsOpen() {
		if y >= layout.ChatAreaTop && y < layout.ChatAreaTop+m.chatAreaHeight {
			return regionEditor
		}
	}

	if len(m.agents) == 0 && m.activeEditorView == "" {
		return regionInput
	}

	if m.Mode == ModeChat {
		tabBarExtra := 0
		if m.win.IsOpen() && m.win.TabCount() >= 1 {
			tabBarExtra = 2
		}
		overlayExtra := 0
		if m.overlay.IsOpen() {
			overlayExtra = len(m.renderOverlayArea())
		}
		inputStartLine := m.chatAreaHeight + 4 + tabBarExtra + overlayExtra
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
	return m.editor.HandleEvent(translated)
}

func (m *App) routeSidebarMouse(msg tea.Msg) tea.Cmd {
	translated := m.translateToTreeMouse(msg)
	return m.treeModel.Update(filetree.TreeMouseMsg{Mouse: translated})
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
		adjusted = tea.MouseClickMsg{X: mm.X, Y: mouse.Y - m.treeYOffset - bcOff, Button: mm.Button}
	case tea.MouseMotionMsg:
		mouse := mm.Mouse()
		adjusted = tea.MouseMotionMsg{X: mm.X, Y: mouse.Y - m.treeYOffset - bcOff, Button: mm.Button}
	case tea.MouseReleaseMsg:
		mouse := mm.Mouse()
		adjusted = tea.MouseReleaseMsg{X: mm.X, Y: mouse.Y - m.treeYOffset - bcOff, Button: mm.Button}
	}
	return m.treeRenderer.HandleScrollbarMsg(adjusted)
}

// breadcrumbOffset returns 1 when the tree is scrolled (breadcrumb visible),
// 0 otherwise.
func (m *App) breadcrumbOffset() int {
	if m.treeModel.ScrollOffset() > 0 {
		return 1
	}
	return 0
}

func (m *App) routeChatMouse(msg tea.Msg, mouse tea.Mouse, isLeftClick bool) []tea.Cmd {
	var cmds []tea.Cmd
	activeML := m.activeMessageList()
	canType := false
	if a, ok := m.agents[m.activeAgentView]; ok {
		canType = !a.Completed && a.IsActive()
	}

	if m.input.InputSelecting() {
		updated, cmd, evt := m.input.Update(msg, canType && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
		m.input = updated
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
	if a, ok := m.agents[m.activeAgentView]; ok {
		canType = !a.Completed && a.IsActive()
	}
	updated, cmd, evt := m.input.Update(msg, canType && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
	m.input = updated
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, m.handleInputEvent(evt)...)
	return cmds
}

func (m *App) dispatchMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	mouse := msg.Mouse()

	if m.windowManager != nil {
		if hitWin := m.windowManager.HandleMouse(msg); hitWin != nil {
			if hitWin.Closable() && hitWin.IsOnCloseButton(mouse.X, mouse.Y) {
				m.windowManager.RemoveWindow(hitWin.ID)
				if hitWin.ID == "textoverlay" {
					m.textOverlay.Close()
				}
				m.setMode(ModeChat)
				return m, nil
			}
			if m.windowManager.IsDragging() {
				return m, tea.Batch(cmds...)
			}
			if m.Mode == ModeTextOverlay {
				updated, cmd, action := m.textOverlay.Update(msg)
				m.textOverlay = updated
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				switch action {
				case widgets.TextOverlaySave:
					m.saveReferenceFromOverlay()
				case widgets.TextOverlayCancel:
					m.textOverlay.Close()
					m.setMode(ModeChat)
				}
				hitWin.MarkContentDirty()
			} else if hitWin.ID == "command-palette" && m.commandPaletteModel != nil {
				if cmd := m.commandPaletteModel.Update(msg); cmd != nil {
					cmds = append(cmds, cmd)
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

	region := m.classifyMouse(mouse.X, mouse.Y, false)

	switch region {
	case regionSidebar:
		m.treeFocused = true
		localY := mouse.Y - m.treeYOffset
		if localY >= 0 {
			if mouse.X == m.treeWidth-1 {
				cmd := m.routeTreeScrollbarMsg(msg)
				return m, cmd
			}
			cmd := m.routeSidebarMouse(msg)
			return m, cmd
		}
		return m, nil

	case regionSidebarDivider:
		if msg.Button == tea.MouseLeft {
			m.treeResizing = true
			m.treeResizeStartX = mouse.X
			m.treeResizeStartWidth = m.treeWidth
		}
		return m, nil

	case regionTabBar:
		m.unfocus()
		m.tabBarFocused = true
		adjustedX := mouse.X
		if m.treeVisible && m.treeModel != nil {
			adjustedX -= m.treeWidth + 1
		}
		adjustedX -= m.theme.Spacing.PadLR
		if adjustedX < 0 {
			adjustedX = 0
		}
		hit := m.win.HitTest(adjustedX, 0)
		if hit.TabIndex >= 0 {
			if hit.IsClose {
				cmds = append(cmds, m.handleAgentTabClose(hit.TabIndex))
				return m, tea.Batch(cmds...)
			}
			m.win.SwitchTo(hit.TabIndex)
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

	case regionOverlay:
		return m.dispatchOverlayMouseClick(msg)
	}

	if m.treeFocused && mouse.X >= m.treeWidth {
		m.treeFocused = false
	}

	return m, nil
}

func (m *App) dispatchOverlayMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	mouse := msg.Mouse()

	overlayStartLine := m.chatAreaHeight + 3
	if m.win.IsOpen() && m.win.TabCount() >= 1 {
		overlayStartLine += 2
	}

	if m.overlay.TabCount() >= 2 && mouse.Y == overlayStartLine+1 {
		adjustedX := mouse.X
		if m.treeVisible && m.treeModel != nil {
			adjustedX -= m.treeWidth + 1
		}
		adjustedX -= m.theme.Spacing.PadLR
		if adjustedX < 0 {
			adjustedX = 0
		}
		hit := m.overlay.HitTest(adjustedX, 0)
		if hit.TabIndex >= 0 {
			if hit.IsClose {
				cmds = append(cmds, m.handleOverlayTabClose(hit.TabIndex))
				return m, tea.Batch(cmds...)
			}
			m.overlay.SwitchTo(hit.TabIndex)
			m.syncOverlayMode()
			return m, nil
		}
	}

	overlayContentOffset := 1
	if m.overlay.TabCount() >= 2 {
		overlayContentOffset = 3
	}
	contentStartLine := overlayStartLine + overlayContentOffset

	activeTab := m.overlay.ActiveTab()
	if activeTab != nil && activeTab.Title == "Question" && m.questionForm.Count() > 1 && mouse.Y >= contentStartLine {
		contentY := mouse.Y - contentStartLine
		innerW := m.width - 2*m.theme.Spacing.PadLR
		sidebarW := m.questionForm.ComputeSidebarWidth(innerW)
		sidebarStartX := m.theme.Spacing.PadLR + innerW - sidebarW
		if mouse.X >= sidebarStartX {
			itemIdx := contentY / 2
			if itemIdx >= 0 && itemIdx < m.questionForm.Count() {
				m.questionForm.SetFocused(itemIdx)
				m.syncQuestionInputPrompt()
				return m, nil
			}
		}
	}
	return m, nil
}

func (m *App) dispatchMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if m.windowManager != nil {
		if hitWin := m.windowManager.HandleMouseWheel(msg); hitWin != nil {
			if m.Mode == ModeTextOverlay {
				updated, cmd, _ := m.textOverlay.Update(msg)
				m.textOverlay = updated
				if cmd != nil {
					cmds = append(cmds, cmd)
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
		m.editor.HandleEvent(msg)
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

	default:
		if len(m.agents) == 0 && m.launcher != nil {
			m.lastInteractionTime = time.Now()
			if msg.Button == tea.MouseWheelUp {
				m.launcher.moveUp()
				return m, nil
			}
			if msg.Button == tea.MouseWheelDown {
				m.launcher.moveDown()
				return m, nil
			}
		}
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
		if fw := m.windowManager.Focused(); fw != nil && fw.ID == "command-palette" && m.commandPaletteModel != nil {
			if cmd := m.commandPaletteModel.Update(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
	}

	if m.treeResizing {
		deltaX := mouse.X - m.treeResizeStartX
		newWidth := m.treeResizeStartWidth + deltaX
		minW := 12
		if newWidth < minW {
			newWidth = minW
		}
		if newWidth > m.width/2 {
			newWidth = m.width / 2
		}
		m.treeWidth = newWidth
		m.treeModel.Update(filetree.TreeWidthMsg{Width: newWidth})
		m.dirty = true
		return m, nil
	}

	if m.treeVisible && m.treeModel != nil && mouse.X < m.treeWidth {
		if mouse.X == m.treeWidth-1 {
			cmd := m.routeTreeScrollbarMsg(msg)
			return m, cmd
		}
		cmd := m.routeSidebarMouse(msg)
		return m, cmd
	}

	if m.activeEditorView != "" && m.editor.IsOpen() {
		region := m.classifyMouse(mouse.X, mouse.Y, false)
		if region == regionEditor {
			cmd := m.routeEditorMouse(msg)
			m.updateDiagHoverPopup(mouse.X, mouse.Y)
			if cmd != nil {
				return m, cmd
			}
			return m, nil
		}
	}

	// Mouse moved away from editor — clear any diagnostic popup.
	m.clearDiagHoverPopup()

	if m.Mode == ModeChat {
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
				if fw.ID == "command-palette" && m.commandPaletteModel != nil {
					if cmd := m.commandPaletteModel.Update(msg); cmd != nil {
						cmds = append(cmds, cmd)
					}
				} else if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
	}

	if m.treeResizing {
		m.treeResizing = false
		return m, nil
	}

	if m.treeVisible && m.treeModel != nil && mouse.X < m.treeWidth {
		if mouse.X == m.treeWidth-1 {
			cmd := m.routeTreeScrollbarMsg(msg)
			return m, cmd
		}
		cmd := m.routeSidebarMouse(msg)
		return m, cmd
	}

	region := m.classifyMouse(mouse.X, mouse.Y, false)

	switch region {
	case regionEditor:
		cmd := m.routeEditorMouse(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case regionInput:
		cmds = append(cmds, m.routeInputMouse(msg)...)
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
	if m.windowManager == nil || !m.editor.IsOpen() {
		return
	}
	state := m.editor.HoverState()
	if len(state.Messages) == 0 {
		m.clearDiagHoverPopup()
		return
	}

	// Build popup content lines with minimal styling.
	title := fmt.Sprintf(" %s:%d:%d", m.editor.Title(), state.Line+1, state.Col+1)
	body := make([]string, 0, len(state.Messages))
	maxLineW := 0
	for _, msg := range state.Messages {
		body = append(body, msg)
		if rendering.VisibleWidth(msg) > maxLineW {
			maxLineW = rendering.VisibleWidth(msg)
		}
	}

	// Compute popup dimensions.
	boxW := maxLineW + 4 // 2 padding + 2 border
	if boxW < 40 {
		boxW = 40
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	innerW := boxW - 4

	// Render each message line wrapped to fit.
	bodyStyled := make([]string, 0, len(body))
	border := lipgloss.NewStyle().Foreground(m.theme.Colors.Border)
	bodyStyle := lipgloss.NewStyle().Background(m.theme.Colors.Code.Bg).Foreground(m.theme.Colors.Fg)
	for _, msg := range body {
		if rendering.VisibleWidth(msg) > innerW {
			msg = rendering.TruncateLine(msg, innerW)
		}
		bodyStyled = append(bodyStyled, border.Render("│ ")+bodyStyle.Render(padRight(msg, innerW))+border.Render(" │"))
	}

	// Title line.
	titleRendered := border.Render("│ ") + lipgloss.NewStyle().Foreground(m.theme.Colors.Accent).Bold(true).Render(padRight(title, innerW)) + border.Render(" │")
	// Top and bottom borders.
	top := border.Render("╭" + strings.Repeat("─", boxW-2) + "╮")
	bottom := border.Render("╰" + strings.Repeat("─", boxW-2) + "╯")

	popupLines := make([]string, 0, 2+len(bodyStyled)+1)
	popupLines = append(popupLines, top)
	popupLines = append(popupLines, titleRendered)
	popupLines = append(popupLines, bodyStyled...)
	popupLines = append(popupLines, bottom)

	popupH := len(popupLines)
	popupX, popupY := m.diagPopupPosition(screenX, screenY, boxW, popupH)

	// Create or update the popup window.
	if m.diagPopupID != "" {
		if existing := m.windowManager.Window(m.diagPopupID); existing != nil {
			existing.Content = widgets.NewPopupContent(popupLines)
			existing.SetSize(boxW, popupH)
			existing.SetPosition(popupX, popupY)
			existing.MarkContentDirty()
			return
		}
	}

	popupContent := widgets.NewPopupContent(popupLines)
	fw := wm.NewFloatingWindow(
		"diag-hover",
		"",
		popupContent,
		boxW, popupH,
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(false),
		wm.WithFocusable(false),
	)
	fw.SetPosition(popupX, popupY)
	m.windowManager.AddWindow(fw)
	m.diagPopupID = "diag-hover"
}

// updateDiagHoverPopupFromCursor shows the diagnostic hover popup at the
// keyboard cursor position.
func (m *App) updateDiagHoverPopupFromCursor() {
	layout := m.computeLayout()
	screenX, screenY := m.editor.CursorScreenPosition(layout.SidebarW, layout.ChatAreaTop)
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
	if m.diagPopupID != "" && m.windowManager != nil {
		m.windowManager.RemoveWindow(m.diagPopupID)
		m.diagPopupID = ""
	}
}

// padRight pads a string to the given visible width with spaces on the right.
func padRight(s string, width int) string {
	pad := width - rendering.VisibleWidth(s)
	if pad < 0 {
		pad = 0
	}
	return s + strings.Repeat(" ", pad)
}
