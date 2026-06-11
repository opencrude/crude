package viewmodel

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/widgets"
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

	if !isWheel && m.win.IsOpen() && m.win.TabCount() >= 2 && y == 3 {
		return regionTabBar
	}

	layout := m.computeLayout()

	if m.overlay.IsOpen() && !isWheel {
		overlayStartLine := m.chatAreaHeight + 3
		if m.win.IsOpen() && m.win.TabCount() >= 2 {
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

	if m.Mode == ModeChat {
		tabBarExtra := 0
		if m.win.IsOpen() && m.win.TabCount() >= 2 {
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

	if m.Mode != ModeLauncher {
		return regionChat
	}

	return regionNone
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
	adjusted := msg
	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		mouse := mm.Mouse()
		adjusted = tea.MouseClickMsg{X: mm.X, Y: mouse.Y - m.treeYOffset - 1, Button: mm.Button}
	case tea.MouseMotionMsg:
		mouse := mm.Mouse()
		adjusted = tea.MouseMotionMsg{X: mm.X, Y: mouse.Y - m.treeYOffset - 1, Button: mm.Button}
	case tea.MouseReleaseMsg:
		mouse := mm.Mouse()
		adjusted = tea.MouseReleaseMsg{X: mm.X, Y: mouse.Y - m.treeYOffset - 1, Button: mm.Button}
	}
	return m.treeRenderer.HandleScrollbarMsg(adjusted)
}

func (m *App) routeChatMouse(msg tea.Msg, mouse tea.Mouse, isLeftClick bool) []tea.Cmd {
	var cmds []tea.Cmd
	activeML := m.activeMessageList()

	if m.input.InputSelecting() {
		updated, cmd, evt := m.input.Update(msg, m.mainAgentActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
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
		if !handled {
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
	updated, cmd, evt := m.input.Update(msg, m.mainAgentActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
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
				if !m.isMouseMsg(msg) {
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
		if localY >= 1 {
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
	if m.win.IsOpen() && m.win.TabCount() >= 2 {
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
		_, cmd := activeML.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	default:
		if m.Mode == ModeLauncher && m.launcher != nil {
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
		m.mainViewDirty = true
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
			if cmd != nil {
				return m, cmd
			}
			return m, nil
		}
	}

	if m.Mode == ModeChat {
		activeML := m.activeMessageList()
		if activeML.IsScrollbarDragging() {
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
		m.windowManager.HandleMouseRelease(msg)
		if fw := m.windowManager.Focused(); fw != nil {
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
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
		if activeML.IsScrollbarDragging() {
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
