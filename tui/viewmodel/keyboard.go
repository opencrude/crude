package viewmodel

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/settings"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
)

func (m *App) dispatchKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if m.mainMsgList.HasActiveSelection() {
		m.mainMsgList.ClearSelection()
		return m, nil
	}

	if msg.String() == "ctrl+b" {
		m.ToggleSidebar()
		return m, nil
	}

	switch m.focusTarget() {
	case FocusLauncher:
		return m.dispatchLauncherKey(msg)

	case FocusTree:
		return m.dispatchTreeKey(msg)

	case FocusEditor:
		return m.dispatchEditorKey(msg)

	case FocusFloatingWindow:
		return m.dispatchFloatingWindowKey(msg)

	case FocusTabBar:
		return m.dispatchTabBarKey(msg)

	case FocusOverlay:
		return m.dispatchOverlayKey(msg, &cmds)

	default:
		return m.dispatchChatKey(msg, &cmds)
	}
}

func (m *App) dispatchLauncherKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.Quit = true
		return m, tea.Quit
	}
	if msg.Code == tea.KeyEsc {
		if m.launcher != nil && m.launcher.filterActive {
			m.launcher.deactivateFilter()
			return m, nil
		}
		m.Quit = true
		return m, tea.Quit
	}
	return m.handleLauncherKey(msg)
}

func (m *App) dispatchTreeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeyEsc || msg.String() == "ctrl+c" {
		m.treeFocused = false
		return m, nil
	}
	cmd := m.treeModel.Update(filetree.TreeKeyPressMsg{Key: msg})
	return m, cmd
}

func (m *App) dispatchEditorKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeyEsc {
		m.closeEditorTab()
		return m, nil
	}
	if msg.String() == "ctrl+c" {
		m.closeEditorTab()
		return m, nil
	}
	cmd := m.editor.HandleEvent(msg)
	m.updateDiagHoverPopupFromCursor()
	if cmd != nil {
		return m, cmd
	}
	return m, nil
}

func (m *App) dispatchFloatingWindowKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if msg.Code == tea.KeyEsc {
		fw := m.windowManager.Focused()
		if fw != nil {
			if fw.ID == "textoverlay" {
				m.textOverlay.Close()
			}
			m.windowManager.RemoveWindow(fw.ID)
			m.setMode(ModeChat)
		}
		return m, nil
	}

	// Forward other keys to the focused window's content.
	if fw := m.windowManager.Focused(); fw != nil {
		switch fw.ID {
		case "textoverlay":
			return m.handleTextOverlayKey(msg, &cmds)
		default:
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *App) dispatchTabBarKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.tabBarFocused = false
		m.logger.Debug("CANCEL", "ctrl_c", "mode", modeName(m.Mode))
		return m, m.handleCancel(msg)
	}
	switch msg.Code {
	case tea.KeyLeft:
		m.win.Prev()
		m.handleTabSwitch()
		return m, nil
	case tea.KeyRight:
		m.win.Next()
		m.handleTabSwitch()
		return m, nil
	case tea.KeyEnter:
		m.tabBarFocused = false
		m.handleTabSwitch()
		return m, nil
	case tea.KeyEsc:
		m.tabBarFocused = false
		return m, nil
	default:
		m.tabBarFocused = false
	}
	return m, nil
}

func (m *App) dispatchOverlayKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	isCancel := msg.Code == tea.KeyEsc || msg.String() == "ctrl+c"

	if keyMsg, ok := any(msg).(tea.KeyPressMsg); ok && m.overlay.TabCount() >= 2 {
		switch keyMsg.String() {
		case "ctrl+]":
			m.overlay.Next()
			m.syncOverlayMode()
			return m, nil
		case "ctrl+[":
			m.overlay.Prev()
			m.syncOverlayMode()
			return m, nil
		}
	}

	switch m.Mode {
	case ModeSettings:
		return m.handleSettingsKey(msg, cmds)
	case ModeConfirm:
		return m.handleConfirmKey(msg, cmds)
	case ModeQuestion:
		return m.handleQuestionKey(msg, cmds)
	case ModeYolo:
		return m.handleYoloKey(msg, cmds)
	case ModeTextOverlay:
		return m.handleTextOverlayKey(msg, cmds)
	case ModeFilePicker:
		return m.handleFilePickerKey(msg, cmds)
	}

	if isCancel {
		return m, m.handleCancel(msg)
	}
	return m, nil
}

func (m *App) handleSettingsKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	updated, cmd, evt := m.settings.Update(msg)
	m.settings = updated
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
	switch e := evt.(type) {
	case settings.SaveEvent:
		s := e.Settings
		m.logger.Info("CONFIG", "settings_save", "provider", s.ProviderID, "model", s.Model)
		if m.OnSettingsSave != nil {
			m.statusbar.SetInfo("saving...")
			onSave := m.OnSettingsSave
			*cmds = append(*cmds, func() tea.Msg {
				err := onSave(s)
				return ttypes.SettingsSaveResultMsg{Settings: s, Err: err}
			})
		} else {
			m.modelName = s.Model
			m.lastSettings = s
			m.closeOverlayTab("Settings")
		}
	case settings.CancelEvent:
		m.logger.Info("CONFIG", "settings_cancel")
		m.settings.RestoreSnapshot()
		m.closeOverlayTab("Settings")
	}
	return m, tea.Batch(*cmds...)
}

func (m *App) handleConfirmKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	m.statusbar.SetConfirming(true)
	updated, result, done := m.confirm.Update(msg)
	m.confirm = updated
	if done {
		m.statusbar.SetConfirming(false)
		m.blocking = false
		ma := m.mainAgent()
		ma.Thinking = true
		m.statusbar.SetThinking(true, false, false)
		if m.OnConfirmResult != nil {
			m.OnConfirmResult(result)
			m.logger.Info("BLOCK", "resolved", "response", result.Choice, "operation", result.Operation, "tool", result.Tool)
		}
		switch result.Choice {
		case ttypes.ConfirmAllow:
			ma.pendingPermTag = "allowed"
		case ttypes.ConfirmAlways:
			ma.pendingPermTag = "always"
		case ttypes.ConfirmDeny:
			ma.pendingPermTag = "denied"
			m.mainMsgList.SetToolPermTag(result.Tool, "", "denied")
		}
		m.closeOverlayTab("Confirm")
	}
	return m, nil
}

func (m *App) handleQuestionKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	m.statusbar.SetConfirming(true)

	inTextMode := m.questionForm.IsTextQuestion(m.questionForm.Focused())
	isCancel := msg.Code == tea.KeyEsc || msg.String() == "ctrl+c"

	if inTextMode {
		if isCancel {
			m.questionForm.ExitTextMode(m.questionForm.Focused())
			m.input.ClearInput()
			m.syncQuestionInputPrompt()
			return m, nil
		}
		if msg.String() == "ctrl+s" {
			text := trimAndSubmit(m.input.Value())
			if text != "" {
				m.questionForm.SetAnswer(m.questionForm.Focused(), text)
			}
			return m.finishQuestionIfNeeded(cmds), nil
		}
		if msg.Code == tea.KeyTab {
			if msg.Mod.Contains(tea.ModShift) {
				m.questionForm.FocusPrev()
			} else {
				m.questionForm.FocusNext()
			}
			m.syncQuestionInputPrompt()
			return m, nil
		}
		return m.routeToInputInQuestion(msg, cmds), nil
	}

	if isCancel {
		m.questionForm.Dismiss()
		m.blocking = false
		m.statusbar.SetConfirming(false)
		m.input.ResetPrompt()
		m.input.ClearInput()
		m.closeAllQuestionOverlayTabs()
		if m.OnQuestionResult != nil {
			m.OnQuestionResult(ttypes.QuestionResult{Cancelled: true})
		}
		return m, nil
	}

	updated, result, done := m.questionForm.Update(msg)
	m.questionForm = updated
	if !done && m.questionForm.IsTextQuestion(m.questionForm.Focused()) {
		existing := m.questionForm.Answer(m.questionForm.Focused())
		if existing != "" {
			m.input.SetInput(existing)
		} else {
			m.input.ClearInput()
		}
	}
	m.syncQuestionInputPrompt()
	if done {
		return m.finishQuestionResult(ttypes.QuestionResult(result), cmds), nil
	}
	return m, nil
}

func (m *App) handleYoloKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	m.statusbar.SetConfirming(true)
	updated, enabled, done := m.yoloDlg.Update(msg)
	m.yoloDlg = updated
	if done {
		m.statusbar.SetConfirming(false)
		m.yolo = enabled
		var content string
		if enabled {
			content = "⚠ YOLO mode ACTIVE — all safety checks disabled. Use /yolo again to restore."
			m.logger.Warn("SAFETY", "yolo_enabled")
		} else {
			content = "YOLO mode cancelled. Safety checks remain active."
			m.logger.Info("SAFETY", "yolo_disabled")
		}
		m.mainMsgList.AppendMessage(ttypes.ChatMessage{
			Role:    ttypes.RoleAssistant,
			Content: content,
			Time:    time.Now(),
		})
		if m.OnYoloCmd != nil {
			m.OnYoloCmd(enabled)
		}
		m.closeOverlayTab("YOLO")
	}
	return m, nil
}

func (m *App) handleTextOverlayKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	updated, cmd, action := m.textOverlay.Update(msg)
	m.textOverlay = updated
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
	switch action {
	case widgets.TextOverlaySave:
		m.saveReferenceFromOverlay()
	case widgets.TextOverlayCancel:
		m.textOverlay.Close()
		m.setMode(ModeChat)
	}
	if fw := m.windowManager.Focused(); fw != nil {
		fw.MarkContentDirty()
	}
	return m, tea.Batch(*cmds...)
}

func (m *App) handleFilePickerKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	if fw := m.windowManager.Focused(); fw != nil {
		if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
			*cmds = append(*cmds, cmd)
		}
	}
	return m, tea.Batch(*cmds...)
}

func (m *App) dispatchChatKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.logger.Debug("CANCEL", "ctrl_c", "mode", modeName(m.Mode))
		return m, m.handleCancel(msg)
	}
	if msg.Code == tea.KeyEsc {
		m.logger.Debug("CANCEL", "esc", "mode", modeName(m.Mode))
		return m, m.handleCancel(msg)
	}

	if m.anyActive() && !m.blocking {
		switch msg.Code {
		case tea.KeyHome, tea.KeyEnd:
			_, cmd := m.mainMsgList.Update(msg)
			if cmd != nil {
				*cmds = append(*cmds, cmd)
			}
			return m, tea.Batch(*cmds...)
		}
	}

	switch msg.String() {
	case "ctrl+]", "ctrl+[":
		if m.win.IsOpen() {
			if msg.String() == "ctrl+]" {
				m.win.Next()
			} else {
				m.win.Prev()
			}
			m.handleTabSwitch()
			return m, nil
		}
	case "ctrl+t":
		if m.win.IsOpen() && m.win.TabCount() >= 2 {
			m.setFocusTabBar()
			return m, nil
		}
	case "ctrl+1":
		if m.treeModel != nil && m.treeVisible {
			m.treeFocused = !m.treeFocused
			return m, nil
		}
	}

	if m.activeEditorView != "" && m.editor.IsOpen() {
		return m.dispatchEditorKey(msg)
	}

	updated, cmd, evt := m.input.Update(msg, m.mainAgentActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
	m.input = updated
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
	*cmds = append(*cmds, m.handleInputEvent(evt)...)

	_, mlCmd := m.mainMsgList.Update(msg)
	if mlCmd != nil {
		*cmds = append(*cmds, mlCmd)
	}

	return m, tea.Batch(*cmds...)
}

func (m *App) handleInputEvent(evt any) []tea.Cmd {
	var cmds []tea.Cmd
	switch e := evt.(type) {
	case input.SubmitEvent:
		cmds = append(cmds, m.submitInput(e.Text, e.ReferenceIDs))
	case input.BtwSubmitEvent:
		cmds = append(cmds, m.submitBtw(e.Text, e.ReferenceIDs))
	case input.SlashCommandEvent:
		cmds = append(cmds, m.executeSlashCommand(e.Command))
	case input.SettingsOpenEvent:
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
	case input.RefOpenEvent:
		m.openReferenceOverlay(e.ID)
	case input.AttachFilesEvent:
		m.openFilePicker()
	}
	return cmds
}

func trimAndSubmit(val string) string {
	return strings.TrimSpace(val)
}

func (m *App) finishQuestionIfNeeded(cmds *[]tea.Cmd) tea.Model {
	if m.questionForm.AllAnswered() {
		result := m.questionForm.BuildResult()
		m.statusbar.SetConfirming(false)
		m.blocking = false
		ma := m.mainAgent()
		ma.Thinking = true
		m.statusbar.SetThinking(true, false, false)
		if m.OnQuestionResult != nil {
			m.OnQuestionResult(ttypes.QuestionResult(result))
		}
		m.closeAllQuestionOverlayTabs()
		m.input.ResetPrompt()
		m.input.ClearInput()
	}
	return m
}

func (m *App) finishQuestionResult(result ttypes.QuestionResult, cmds *[]tea.Cmd) tea.Model {
	m.statusbar.SetConfirming(false)
	m.blocking = false
	ma := m.mainAgent()
	ma.Thinking = true
	m.statusbar.SetThinking(true, false, false)
	if m.OnQuestionResult != nil {
		m.OnQuestionResult(result)
	}
	m.closeAllQuestionOverlayTabs()
	m.input.ResetPrompt()
	m.input.ClearInput()
	return m
}

func (m *App) routeToInputInQuestion(msg tea.KeyPressMsg, cmds *[]tea.Cmd) tea.Model {
	updated, cmd, evt := m.input.Update(msg, false, false, m.chatAreaHeight, false)
	m.input = updated
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
	switch e := evt.(type) {
	case input.SubmitEvent:
		m.questionForm.SetAnswer(m.questionForm.Focused(), e.Text)
		m.input.ClearInput()
		if m.questionForm.AllAnswered() {
			m.finishQuestionIfNeeded(cmds)
		} else {
			m.questionForm.FocusNext()
			m.syncQuestionInputPrompt()
		}
	}
	return m
}

func (m *App) dispatchPasteMsg(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch m.focusTarget() {
	case FocusEditor:
		cmd := m.editor.HandleEvent(msg)
		return m, cmd
	default:
		updated, cmd, evt := m.input.Update(msg, m.mainAgentActive() && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
		m.input = updated
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, m.handleInputEvent(evt)...)
		return m, tea.Batch(cmds...)
	}
}

func (m *App) dispatchEditorTick(msg any) []tea.Cmd {
	var cmds []tea.Cmd
	if m.activeEditorView != "" && m.editor.IsOpen() {
		cmd := m.editor.HandleEvent(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func (m *App) isEditorMsg(msg any) bool {
	_, ok := msg.(editor.CursorBlinkMsg)
	return ok
}
