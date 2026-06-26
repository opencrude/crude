package viewmodel

import (
	"fmt"
	"image/color"
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
	regionStatusBar
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

	// Status bar is always the last visible row. Treat it as its
	// own region so right-click can offer LSP/session controls.
	// Placed after the floating-window check so a popup covering
	// the status bar (e.g. command palette) wins.
	if m.height > 0 && y == m.height-1 {
		return regionStatusBar
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
	translated = translateMouseX(translated, -m.editor.PadL)
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

	case regionStatusBar:
		// Left-click on the status bar is a no-op (it's a passive
		// display, not an interactive region). Right-click goes
		// through dispatchMouseRightClick instead.
		return m, nil
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

		// Popup hover pre-selection: if the mouse is over a focusable
		// popup, update its hover state and return (don't route motion
		// to the underlying editor/tree). Iterate top-to-bottom.
		for i := len(m.windowManager.Windows()) - 1; i >= 0; i-- {
			fw := m.windowManager.Windows()[i]
			if !fw.Visible() || !fw.Focusable() {
				continue
			}
			if fw.ContainsPoint(mouse.X, mouse.Y) {
				m.handlePopupContentHover(fw, mouse.X, mouse.Y)
				return m, tea.Batch(cmds...)
			}
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
	severityIconColors := map[int]color.Color{
		1: m.theme.Colors.Error,
		2: m.theme.Colors.Warning,
		3: m.theme.Colors.Muted,
		4: m.theme.Colors.Muted,
	}
	sevColor, _ := severityIconColors[state.Severity]
	sevLabel, ok := severityLabels[state.Severity]
	if !ok {
		sevLabel = "Info"
	}
	sevIcon, ok := severityIcons[state.Severity]
	if !ok {
		sevIcon = "ℹ"
	}

	titleStr := fmt.Sprintf("%s:%d:%d", m.editor.Title(), state.Line+1, state.Col+1)
	sourceLine := strings.TrimRight(m.editor.LineText(state.Line), "\n\r ")

	// Compute max width across all content.
	maxLineW := rendering.VisibleWidth(titleStr)
	if w := rendering.VisibleWidth(sourceLine); w > maxLineW {
		maxLineW = w
	}
	for _, msg := range state.Messages {
		if w := rendering.VisibleWidth(msg); w > maxLineW {
			maxLineW = w
		}
	}

	// Compute popup dimensions.
	// boxW = 2 (side borders) + 4 (inner padding: 2 left + 2 right) + maxLineW
	boxW := maxLineW + 6
	if boxW < 44 {
		boxW = 44
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	innerW := boxW - 6 // 2 left pad + 2 right pad + 2 side borders (1 each)

	// Styles.
	mutedStyle := lipgloss.NewStyle().Background(m.theme.Colors.PopupBg).Foreground(m.theme.Colors.Muted)
	titleStyle := lipgloss.NewStyle().Background(m.theme.Colors.PopupBg).Foreground(m.theme.Colors.Accent).Bold(true)
	sourceStyle := lipgloss.NewStyle().Background(m.theme.Colors.PopupBg).Foreground(m.theme.Colors.Fg)
	bodyStyle := lipgloss.NewStyle().Background(m.theme.Colors.PopupBg).Foreground(m.theme.Colors.Fg)

	// Build inner content lines.
	var content []string
	// Title row: file:line:col — Accent bold.
	content = append(content, widgets.PadRightANSI(titleStr, innerW, m.theme, titleStyle))
	if sourceLine != "" {
		content = append(content, widgets.PadRightANSI(sourceLine, innerW, m.theme, sourceStyle))
	}
	// Divider.
	content = append(content, widgets.PadRightANSI(strings.Repeat("─", innerW), innerW, m.theme, mutedStyle))
	// Body lines (word-wrapped).
	hasBody := false
	for _, msg := range state.Messages {
		for _, segment := range strings.Split(msg, "\n") {
			for _, line := range rendering.WordWrap(segment, innerW) {
				content = append(content, widgets.PadRightANSI(line, innerW, m.theme, bodyStyle))
				hasBody = true
			}
		}
	}
	if !hasBody {
		content = append(content, widgets.PadRightANSI(strings.Repeat(" ", innerW), innerW, m.theme, bodyStyle))
	}

	// Wrap in unified chrome. The border is tinted with the severity
	// color, the title is the severity label.
	lines := widgets.PopupChromeLines(m.theme, widgets.PopupChromeConfig{
		Title:       sevLabel,
		TitleIcon:   sevIcon,
		Width:       boxW,
		Focused:     false,
		BorderAccent: sevColor,
		TitleAccent:  sevColor,
	}, content)

	popupH := len(lines)
	popupX, popupY := m.diagPopupPosition(screenX, screenY, boxW, popupH)

	// Create or update the popup window.
	if m.diagPopupID != "" {
		if existing := m.windowManager.Window(m.diagPopupID); existing != nil {
			existing.Content = widgets.NewPopupContent(lines)
			existing.SetSize(boxW, popupH)
			existing.SetPosition(popupX, popupY)
			existing.MarkContentDirty()
			return
		}
	}

	popupContent := widgets.NewPopupContent(lines)
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
			m.windowManager.RemoveWindow("context-menu")
			m.dirty = true
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
	case regionOverlay:
		// No menu for overlay regions; they're modal anyway.
		return m, nil
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
	w := widgets.ContextMenuPopupWidth(st)
	h := widgets.ContextMenuPopupHeight(st)
	x, y := clampPopupPosition(mouse.X, mouse.Y, w, h, m.width, m.height)

	lines := widgets.ContextMenuLines(m.theme, st, 0)
	pop := widgets.NewPopupContent(lines)
	// ContextMenuLines now self-renders the unified popup chrome
	// (border, title, padding). The FloatingWindow is just a
	// positioned box with no decoration.
	fw := wm.NewFloatingWindow("context-menu", "", pop, w, h,
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
	)
	fw.SetPosition(x, y)
	if m.windowManager != nil {
		m.windowManager.AddWindow(fw)
	}
	m.lastContextMenu = st
	// Record when the menu opened so we can drive the slide-in
	// animation. Without this, the menu would just pop in.
	m.contextMenuOpenedAt = time.Now()
	m.dirty = true
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
