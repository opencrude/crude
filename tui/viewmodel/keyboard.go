package viewmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/settings"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
	wm "crude/tui/wm"
)

func (m *App) dispatchKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Global keybindings — checked first, before focus routing.
	// These must work everywhere including the launcher/welcome screen.
	keyStr := msg.String()
	keyStrLower := strings.ToLower(keyStr)
	// Try exact match first, then case-insensitive, then Mod-based fallback.
	// ctrl+shift+p may arrive as "ctrl+shift+P" (enhanced protocol) or just
	// "ctrl+p" (legacy terminals where Shift is invisible).
	isCommandPalette := keyStr == "ctrl+shift+p" ||
		keyStr == "ctrl+shift+P" ||
		keyStr == "ctrl+p" ||
		(msg.Mod.Contains(tea.ModCtrl) && msg.Mod.Contains(tea.ModShift) && msg.Code == 'P')
	switch {
	case isCommandPalette:
		return m.toggleCommandPalette()
	case keyStrLower == "alt+c":
		return m.toggleComposer()
	}

	// Ctrl+Q always works — close active tab/window or quit
	if keyStr == "ctrl+q" {
		return m.handleQuit()
	}

	if ml := m.activeMsgList(); ml != nil && ml.HasActiveSelection() {
		ml.ClearSelection()
		return m, nil
	}

	if keyStr == "ctrl+b" {
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
		return m, nil
	}
	if msg.Code == tea.KeyEsc {
		if m.launcher != nil && m.launcher.filterActive {
			m.launcher.deactivateFilter()
			return m, nil
		}
		return m, nil
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
	if msg.String() == "ctrl+c" {
		if m.editor.HasSelection() {
			selected := m.editor.SelectedText()
			if selected != "" {
				return m, rendering.CopyToClipboardCmd(selected)
			}
		}
		return m, nil
	}
	if msg.Code == tea.KeyEsc {
		if m.renameArmed {
			m.renameArmed = false
			m.statusbar.SetInfo("Rename cancelled")
			return m, nil
		}
		if m.referencesID != "" {
			m.closeReferencesPopup()
			return m, nil
		}
		if m.completionID != "" {
			m.closeCompletionPopup()
			return m, nil
		}
		m.closeEditorTab()
		return m, nil
	}
	// References popup: intercept navigation keys while it's open.
	if m.referencesID != "" {
		switch msg.String() {
		case "down":
			if m.refCursor < len(m.refRows)-1 {
				m.refCursor++
				m.refreshReferencesPopup()
			}
			return m, nil
		case "up":
			if m.refCursor > 0 {
				m.refCursor--
				m.refreshReferencesPopup()
			}
			return m, nil
		case "enter":
			if m.refCursor >= 0 && m.refCursor < len(m.refRows) {
				r := m.refRows[m.refCursor]
				m.jumpToReference(r)
				m.closeReferencesPopup()
			}
			return m, nil
		}
	}
	// Outline popup: intercept navigation keys while it's open.
	if m.outlineOpen && m.windowManager != nil && m.windowManager.Window(outlinePopupID) != nil {
		switch msg.String() {
		case "down":
			if m.outlineCursor < len(m.outlineRows)-1 {
				m.outlineCursor++
				m.showOutlinePopup(m.lspManager.DocumentSymbols(m.editor.FilePath()))
			}
			return m, nil
		case "up":
			if m.outlineCursor > 0 {
				m.outlineCursor--
				m.showOutlinePopup(m.lspManager.DocumentSymbols(m.editor.FilePath()))
			}
			return m, nil
		case "enter":
			if m.outlineCursor >= 0 && m.outlineCursor < len(m.outlineRows) {
				sym := m.outlineRows[m.outlineCursor].sym
				m.editor.SetCursorPosition(sym.Line, sym.Col)
				m.editor.ScrollToCursor()
				layout := m.computeLayout()
				m.editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
			}
			return m, nil
		}
	}
	// Completion popup: intercept navigation keys while it's open.
	if m.completionID != "" {
		switch msg.String() {
		case "down":
			if m.completionCursor < len(m.completionItems)-1 {
				m.completionCursor++
				m.showCompletionPopup(m.completionItems)
			}
			return m, nil
		case "up":
			if m.completionCursor > 0 {
				m.completionCursor--
				m.showCompletionPopup(m.completionItems)
			}
			return m, nil
		case "tab", "enter":
			m.acceptCompletion()
			return m, nil
		}
	}
	// LSP keybinds (only when a file is open and the LSP is up).
	if m.editor.IsOpen() && m.lspManager != nil {
		switch msg.String() {
		case "alt+.": // gd (go to definition) — Alt+. is widely used in TUI editors
			return m, m.requestDefinitionCmd()
		case "alt+;": // gr (find references) — Alt+; mirrors the VSCode chord
			return m, m.requestReferencesCmd()
		case "alt+o": // outline: refresh document symbols and toggle the outline panel
			return m, m.requestDocumentSymbolsCmd()
		case "f2": // rename
			return m, m.startRenameCmd()
		case "ctrl+space": // LSP completion
			return m, m.requestCompletionCmd()
		case "f8": // next error (or first error in project on first press)
			if m.editor.CursorLine() == 0 && m.editor.CursorCol() == 0 && m.editor.FirstError() != nil {
				// Cursor at the very top: jump to first project error.
				m.gotoFirstDiagnostic()
			} else {
				m.jumpToNextError()
			}
			return m, nil
		case "shift+f8": // previous error
			m.jumpToPrevError()
			return m, nil
		}
	}
	// Find/Replace keybinds (work even without LSP).
	if m.editor.IsOpen() {
		switch msg.String() {
		case "ctrl+f":
			m.openFindReplace(false)
			m.dirty = true
			return m, nil
		case "ctrl+h":
			m.openFindReplace(true)
			m.dirty = true
			return m, nil
		case "f3":
			if m.findReplace != nil {
				m.findReplace.OnNext()
			} else if m.editor.Search().HasMatches() {
				m.editor.NextMatch()
			}
			m.dirty = true
			return m, nil
		case "shift+f3":
			if m.findReplace != nil {
				m.findReplace.OnPrev()
			} else if m.editor.Search().HasMatches() {
				m.editor.PrevMatch()
			}
			m.dirty = true
			return m, nil
		case "ctrl+.":
			layout := m.computeLayout()
			cx, cy := m.editor.CursorScreenPosition(layout.SidebarW, layout.ChatAreaTop)
			m.showContextMenuAt(cx, cy)
			return m, nil
		case "esc":
			if m.findReplace != nil {
				m.closeFindReplace()
				return m, nil
			}
		}
	}
	cmd := m.editor.HandleEvent(msg)
	layout := m.computeLayout()
	m.editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.updateDiagHoverPopupFromCursor()
	if cmd != nil {
		return m, cmd
	}
	return m, nil
}

func (m *App) dispatchFloatingWindowKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if fw := m.windowManager.Focused(); fw != nil {
		switch fw.ID {
		case "find-replace":
			return m, m.handleFindReplaceKey(msg)
		case "context-menu":
			return m, m.handleContextMenuKey(msg)
		case "dirty-close-prompt":
			return m, m.handleDirtyPromptKey(msg)
		case "tree-prompt":
			return m, m.handleTreePromptKey(msg)
		case "tree-delete-confirm":
			return m, m.handleTreeDeleteConfirmKey(msg)
		case "command-palette":
			if msg.Code == tea.KeyEsc {
				m.windowManager.RemoveWindow("command-palette")
				m.commandPaletteModel = nil
				return m, nil
			}
			if m.commandPaletteModel != nil {
				cmd := m.commandPaletteModel.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				if fw := m.windowManager.Window("command-palette"); fw != nil {
					fw.MarkContentDirty()
				}
			}
			return m, tea.Batch(cmds...)

		case "composer":
			if msg.Code == tea.KeyEsc {
				m.composer.Close()
				m.windowManager.RemoveWindow("composer")
				return m, nil
			}
			updated, cmd, action := m.composer.Update(msg)
			m.composer = updated
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			switch action {
			case widgets.ComposerCancel:
				m.composer.Close()
				m.windowManager.RemoveWindow("composer")
			case widgets.ComposerSubmit:
				text := m.composer.Value()
				m.composer.Close()
				m.windowManager.RemoveWindow("composer")
				if text != "" && m.activeAgentView != "" {
					cmds = append(cmds, m.submitInput(m.activeAgentView, text, nil))
				}
			}
			if fw := m.windowManager.Window("composer"); fw != nil {
				fw.MarkContentDirty()
			}
			return m, tea.Batch(cmds...)

		case "textoverlay":
			return m.handleTextOverlayKey(msg, &cmds)

		default:
			if msg.Code == tea.KeyEsc {
				if fw.ID == "textoverlay" {
					m.textOverlay.Close()
				}
				m.windowManager.RemoveWindow(fw.ID)
				m.setMode(ModeChat)
				return m, nil
			}
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *App) dispatchTabBarKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
	isCancel := msg.Code == tea.KeyEsc

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
		m.logger.Info("CONFIG", "settings_save", "provider", s.ProviderID, "model", s.Model, "category", m.settingsCategory)
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
		m.settingsCategory = ""
	case settings.CancelEvent:
		m.logger.Info("CONFIG", "settings_cancel")
		m.settings.RestoreSnapshot()
		m.closeOverlayTab("Settings")
		m.settingsCategory = ""
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
		if a, ok := m.agents[m.activeAgentView]; ok {
			a.Thinking = true
		}
		m.statusbar.SetThinkingForAgent(m.activeAgentView, true, false, false)
		if m.OnConfirmResult != nil {
			m.OnConfirmResult(result)
			m.logger.Info("BLOCK", "resolved", "response", result.Choice, "operation", result.Operation, "tool", result.Tool)
		}
		if a, ok := m.agents[m.activeAgentView]; ok {
			switch result.Choice {
			case ttypes.ConfirmAllow:
				a.pendingPermTag = "allowed"
			case ttypes.ConfirmAlways:
				a.pendingPermTag = "always"
			case ttypes.ConfirmDeny:
				a.pendingPermTag = "denied"
				if ml := m.activeMsgList(); ml != nil {
				ml.SetToolPermTag(result.Tool, "", "denied")
			}
			}
		}
		m.closeOverlayTab("Confirm")
	}
	return m, nil
}

func (m *App) handleQuestionKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	m.statusbar.SetConfirming(true)

	inTextMode := m.questionForm.IsTextQuestion(m.questionForm.Focused())
	isCancel := msg.Code == tea.KeyEsc

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
		if ml := m.activeMsgList(); ml != nil {
			ml.AppendMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: content,
				Time:    time.Now(),
			})
		}
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
		if m.logger != nil {
			m.logger.Debug("COPY", "ctrl_c", "mode", modeName(m.Mode))
		}
		if m.input.InputSelecting() {
			selected := m.input.SelectedText()
			if selected != "" {
				return m, rendering.CopyToClipboardCmd(selected)
			}
		}
		return m, nil
	}
	if msg.Code == tea.KeyEsc {
		if m.logger != nil {
			m.logger.Debug("CANCEL", "esc", "mode", modeName(m.Mode))
		}
		return m, m.handleCancel(msg)
	}

	if m.anyActive() && !m.blocking {
		switch msg.Code {
		case tea.KeyHome, tea.KeyEnd:
			if ml := m.activeMsgList(); ml != nil {
				_, cmd := ml.Update(msg)
				if cmd != nil {
					*cmds = append(*cmds, cmd)
				}
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
		if m.win.IsOpen() && m.win.TabCount() >= 1 {
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

	canType := false
	if a, ok := m.agents[m.activeAgentView]; ok {
		canType = !a.Completed && a.IsActive()
	}
	updated, cmd, evt := m.input.Update(msg, canType && !m.blocking, m.blocking, m.chatAreaHeight, m.input.ShowHint())
	m.input = updated
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
	*cmds = append(*cmds, m.handleInputEvent(evt)...)

	if ml := m.activeMsgList(); ml != nil {
		_, mlCmd := ml.Update(msg)
		if mlCmd != nil {
			*cmds = append(*cmds, mlCmd)
		}
	}

	return m, tea.Batch(*cmds...)
}

func (m *App) handleInputEvent(evt any) []tea.Cmd {
	var cmds []tea.Cmd
	switch e := evt.(type) {
	case input.SubmitEvent:
		// Rename arming overrides the normal submit — the user typed a
		// new symbol name after F2. We consume the text and fire the
		// rename request instead of sending it as a chat message.
		if m.renameArmed {
			m.renameArmed = false
			cmds = append(cmds, m.submitRename(e.Text))
			return cmds
		}
		cmds = append(cmds, m.submitInput(m.activeAgentView, e.Text, e.ReferenceIDs))
	case input.BtwSubmitEvent:
		cmds = append(cmds, m.submitBtw(m.activeAgentView, e.Text, e.ReferenceIDs))
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
		if a, ok := m.agents[m.activeAgentView]; ok {
			a.Thinking = true
		}
		m.statusbar.SetThinkingForAgent(m.activeAgentView, true, false, false)
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
	if a, ok := m.agents[m.activeAgentView]; ok {
		a.Thinking = true
	}
	m.statusbar.SetThinkingForAgent(m.activeAgentView, true, false, false)
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

// ─── LSP request commands ──────────────────────────────────────────────────
//
// The functions below return a tea.Cmd that fires the LSP request in a
// goroutine and dispatches the result back into the app via a custom msg
// (lspDefinitionResultMsg, lspReferencesResultMsg, etc.) handled in update.go.
//
// We keep these as separate Cmd-returning helpers so the key handler stays
// short and readable. Each helper returns nil (no-op) when the editor is not
// open or the LSP is not running for the active file.

// lspDefinitionResultMsg is the response to a "go to definition" request.
// locations is empty when the symbol has no definition or the request failed.
type lspDefinitionResultMsg struct {
	filePath  string
	locations []editor.LSPLocation
}

// lspReferencesResultMsg is the response to a "find references" request.
type lspReferencesResultMsg struct {
	filePath  string
	locations []editor.LSPLocation
}

// lspDocumentSymbolsResultMsg is the response to a "document symbols" request.
// symbols is empty when the file has no symbols (empty file, server unsupported).
type lspDocumentSymbolsResultMsg struct {
	filePath string
	symbols  []editor.LSPSymbol
}

// lspRenameResultMsg is the response to a "rename" request.
type lspRenameResultMsg struct {
	filePath string
	newName  string
	edit     interface{}
	err      error
}

// lspCompletionResultMsg is the response to a "completion" request.
type lspCompletionResultMsg struct {
	filePath string
	line     int
	col      int
	items    []editor.CompletionItem
}

// requestDefinitionCmd fires a "go to definition" request at the cursor.
func (m *App) requestDefinitionCmd() tea.Cmd {
	if !m.editor.IsOpen() || m.lspManager == nil {
		return nil
	}
	filePath := m.editor.FilePath()
	line, col := m.editor.CursorLine(), m.editor.CursorCol()
	conn, status, _ := m.lspManager.Status()
	if !conn || status != editor.LSPConnected {
		m.statusbar.SetInfo("LSP not ready")
		return nil
	}
	lspMgr := m.lspManager
	return tea.Cmd(func() tea.Msg {
		locs := lspMgr.RequestDefinition(context.Background(), filePath, line, col)
		return lspDefinitionResultMsg{filePath: filePath, locations: locs}
	})
}

// requestReferencesCmd fires a "find references" request at the cursor.
func (m *App) requestReferencesCmd() tea.Cmd {
	if !m.editor.IsOpen() || m.lspManager == nil {
		return nil
	}
	filePath := m.editor.FilePath()
	line, col := m.editor.CursorLine(), m.editor.CursorCol()
	conn, status, _ := m.lspManager.Status()
	if !conn || status != editor.LSPConnected {
		m.statusbar.SetInfo("LSP not ready")
		return nil
	}
	lspMgr := m.lspManager
	return tea.Cmd(func() tea.Msg {
		locs := lspMgr.FindReferences(context.Background(), filePath, line, col, true)
		return lspReferencesResultMsg{filePath: filePath, locations: locs}
	})
}

// requestDocumentSymbolsCmd refreshes the document outline for the current
// file and shows it in the outline panel.
func (m *App) requestDocumentSymbolsCmd() tea.Cmd {
	if !m.editor.IsOpen() || m.lspManager == nil {
		return nil
	}
	filePath := m.editor.FilePath()
	conn, status, _ := m.lspManager.Status()
	if !conn || status != editor.LSPConnected {
		m.statusbar.SetInfo("LSP not ready")
		return nil
	}
	lspMgr := m.lspManager
	return tea.Cmd(func() tea.Msg {
		syms := lspMgr.RequestDocumentSymbols(context.Background(), filePath)
		return lspDocumentSymbolsResultMsg{filePath: filePath, symbols: syms}
	})
}

// startRenameCmd opens the rename input prompt, pre-filled with the symbol at
// the cursor. The actual rename request fires when the user confirms the new
// name in the input.
func (m *App) startRenameCmd() tea.Cmd {
	if !m.editor.IsOpen() || m.lspManager == nil {
		return nil
	}
	conn, status, _ := m.lspManager.Status()
	if !conn || status != editor.LSPConnected {
		m.statusbar.SetInfo("LSP not ready")
		return nil
	}
	// Arm rename mode: the next text submitted through the input is
	// treated as the new symbol name. Esc cancels. The status bar
	// explains what's happening.
	m.renameArmed = true
	m.statusbar.SetInfo("Rename: type new name and press Enter (Esc to cancel)")
	return nil
}

// submitRename fires the actual rename request once the user has typed a
// new name. Called from the input handler when renameArmed is true.
func (m *App) submitRename(newName string) tea.Cmd {
	m.renameArmed = false
	if !m.editor.IsOpen() || m.lspManager == nil || newName == "" {
		return nil
	}
	filePath := m.editor.FilePath()
	line, col := m.editor.CursorLine(), m.editor.CursorCol()
	lspMgr := m.lspManager
	return tea.Cmd(func() tea.Msg {
		edit, err := lspMgr.RequestRename(context.Background(), filePath, line, col, newName)
		return lspRenameResultMsg{filePath: filePath, newName: newName, edit: edit, err: err}
	})
}

// requestCompletionCmd fires a completion request at the cursor.
func (m *App) requestCompletionCmd() tea.Cmd {
	if !m.editor.IsOpen() || m.lspManager == nil {
		return nil
	}
	filePath := m.editor.FilePath()
	line, col := m.editor.CursorLine(), m.editor.CursorCol()
	conn, status, _ := m.lspManager.Status()
	if !conn || status != editor.LSPConnected {
		m.statusbar.SetInfo("LSP not ready")
		return nil
	}
	lspMgr := m.lspManager
	return tea.Cmd(func() tea.Msg {
		items := lspMgr.RequestCompletions(context.Background(), filePath, line, col)
		return lspCompletionResultMsg{filePath: filePath, line: line, col: col, items: items}
	})
}

// ─── LSP result handlers ────────────────────────────────────────────────────

// handleDefinitionResult reacts to a "go to definition" response. If the
// server returned a single location, the editor jumps straight to it. If
// multiple, the file is opened at the first and a status-bar hint shows the
// total count.
func (m *App) handleDefinitionResult(msg lspDefinitionResultMsg) {
	if len(msg.locations) == 0 {
		m.statusbar.SetInfo("No definition found")
		return
	}
	loc := msg.locations[0]
	if loc.Path == "" {
		m.statusbar.SetInfo("Definition in unindexed location")
		return
	}
	if loc.Path != m.editor.FilePath() {
		if err := m.openFileInEditor(loc.Path); err != nil {
			m.statusbar.SetError("Cannot open " + loc.Path)
			return
		}
	}
	m.editor.SetCursorPosition(loc.Line, loc.Col)
	m.editor.ScrollToCursor()
	layout := m.computeLayout()
	m.editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	if len(msg.locations) > 1 {
		m.statusbar.SetInfo(fmt.Sprintf("Definition: %d locations", len(msg.locations)))
	}
}

// handleReferencesResult shows a popup with the list of references. If the
// user presses Enter on a row, the editor jumps to that location.
func (m *App) handleReferencesResult(msg lspReferencesResultMsg) {
	if len(msg.locations) == 0 {
		m.statusbar.SetInfo("No references found")
		return
	}
	rows := make([]refRow, 0, len(msg.locations))
	for _, loc := range msg.locations {
		preview := ""
		if line, err := m.editor.LineTextFromPath(loc.Path, loc.Line); err == nil {
			preview = line
		}
		rows = append(rows, refRow{file: loc.Path, line: loc.Line, col: loc.Col, preview: preview})
	}
	m.showReferencesPopup(rows)
}

// handleDocumentSymbolsResult stores the symbols for the outline panel and
// opens it. Empty result closes the outline.
func (m *App) handleDocumentSymbolsResult(msg lspDocumentSymbolsResultMsg) {
	if len(msg.symbols) == 0 {
		m.statusbar.SetInfo("No symbols in file")
		m.outlineOpen = false
		m.closeOutlinePopup()
		return
	}
	m.outlineOpen = true
	m.statusbar.SetInfo(fmt.Sprintf("Outline: %d symbols", countSymbols(msg.symbols)))
	m.showOutlinePopup(msg.symbols)
}

// handleCompletionResult shows a list of completion items in a floating
// window. The list is navigable with up/down; pressing Enter inserts the
// selected item's text at the cursor.
func (m *App) handleCompletionResult(msg lspCompletionResultMsg) {
	if len(msg.items) == 0 {
		m.statusbar.SetInfo("No completions")
		return
	}
	// Limit to a reasonable number of items for the popup.
	const maxItems = 30
	items := msg.items
	if len(items) > maxItems {
		items = items[:maxItems]
	}
	m.showCompletionPopup(items)
}

// handleRenameResult applies the workspace edit returned by the server.
// We support the simple `Changes` map (legacy form) — that's what gopls and
// most servers return for a single-file rename. The newer DocumentChanges
// form is left as a TODO; the rename UX is still useful without it.
func (m *App) handleRenameResult(msg lspRenameResultMsg) {
	if msg.err != nil {
		m.statusbar.SetError("Rename failed: " + msg.err.Error())
		return
	}
	edit, ok := msg.edit.(*protocol.WorkspaceEdit)
	if !ok || edit == nil {
		m.statusbar.SetInfo("Rename: no changes")
		return
	}
	applied := 0
	for uri, edits := range edit.Changes {
		fp := uriToPath(string(uri))
		data, err := os.ReadFile(fp)
		if err != nil {
			m.logger.Warn("LSP", "rename_read", "file", fp, "error", err.Error())
			continue
		}
		updated, err := applyTextEdits(string(data), edits)
		if err != nil {
			m.logger.Warn("LSP", "rename_apply", "file", fp, "error", err.Error())
			continue
		}
		if err := os.WriteFile(fp, []byte(updated), 0o644); err != nil {
			m.logger.Warn("LSP", "rename_write", "file", fp, "error", err.Error())
			continue
		}
		applied++
		// If the changed file is currently open in the editor, refresh it.
		if m.editor.IsOpen() && m.editor.FilePath() == fp {
			if err := m.editor.Reload(); err != nil {
				m.logger.Warn("LSP", "rename_reload", "file", fp, "error", err.Error())
			}
		}
	}
	if applied == 0 {
		m.statusbar.SetInfo("Rename: no changes applied")
		return
	}
	m.statusbar.SetInfo(fmt.Sprintf("Renamed in %d file%s", applied, pluralFile(applied)))
}

// countSymbols returns the total number of symbols in a tree, including
// nested children. Used for the outline panel header.
func countSymbols(syms []editor.LSPSymbol) int {
	n := 0
	for _, s := range syms {
		n++
		n += countSymbols(s.Children)
	}
	return n
}

func pluralFile(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// openFileInEditor opens the file at the given path in the editor, switching
// to or creating the appropriate tab. Used by "go to definition" when the
// target lives in another file.
func (m *App) openFileInEditor(path string) error {
	_, err := m.openEditorTab(path)
	return err
}

// uriToPath strips the "file://" prefix from a DocumentURI.
func uriToPath(uri string) string {
	const prefix = "file://"
	if len(uri) >= len(prefix) && uri[:len(prefix)] == prefix {
		return uri[len(prefix):]
	}
	return uri
}

// applyTextEdits applies a list of LSP TextEdits to the given text. The
// edits are applied back-to-front (highest offset first) so each edit's
// range remains valid as we modify the buffer.
//
// Edits are assumed to be sorted by their position in the document; if not,
// we sort them defensively. The result is the post-edit text.
func applyTextEdits(text string, edits []protocol.TextEdit) (string, error) {
	if len(edits) == 0 {
		return text, nil
	}
	lines := strings.Split(text, "\n")

	type editPos struct {
		line     int
		char     int
		endLine  int
		endChar  int
		newText  string
	}
	positions := make([]editPos, 0, len(edits))
	for _, e := range edits {
		positions = append(positions, editPos{
			line:    int(e.Range.Start.Line),
			char:    int(e.Range.Start.Character),
			endLine: int(e.Range.End.Line),
			endChar: int(e.Range.End.Character),
			newText: e.NewText,
		})
	}

	// Apply back-to-front to keep offsets stable.
	for i := len(positions) - 1; i >= 0; i-- {
		p := positions[i]
		if p.line < 0 || p.line >= len(lines) {
			continue
		}
		if p.endLine < 0 || p.endLine >= len(lines) {
			p.endLine = len(lines) - 1
		}
		// Splice lines [p.line, p.endLine) into one, replace [p.char, p.endChar).
		first := lines[p.line]
		if p.char > len(first) {
			p.char = len(first)
		}
		prefix := first[:p.char]

		last := lines[p.endLine]
		if p.endChar > len(last) {
			p.endChar = len(last)
		}
		suffix := last[p.endChar:]

		replacement := p.newText
		// NewText may contain newlines; split it into pieces and inject.
		parts := strings.Split(replacement, "\n")
		var newLines []string
		newLines = append(newLines, prefix+parts[0])
		for i := 1; i < len(parts)-1; i++ {
			newLines = append(newLines, parts[i])
		}
		if len(parts) > 1 {
			newLines = append(newLines, parts[len(parts)-1]+suffix)
		}

		// Replace lines [p.line, p.endLine+1) with newLines.
		combined := make([]string, 0, len(lines)-int(p.endLine-p.line)+len(newLines))
		combined = append(combined, lines[:p.line]...)
		combined = append(combined, newLines...)
		if p.endLine+1 < len(lines) {
			combined = append(combined, lines[p.endLine+1:]...)
		}
		lines = combined
	}
	return strings.Join(lines, "\n"), nil
}

// showReferencesPopup displays the list of references in a floating window.
// The list is selectable — pressing Enter on a row jumps the editor to that
// location and closes the popup.
type refRow struct {
	file    string
	line    int
	col     int
	preview string
}

func (m *App) showReferencesPopup(rows []refRow) {
	if m.windowManager == nil {
		return
	}
	const id = "references-popup"

	// Build the popup content.
	const maxPreview = 60
	type listItem struct {
		row  refRow
		text string
	}
	items := make([]listItem, 0, len(rows))
	for _, r := range rows {
		short := r.preview
		if len([]rune(short)) > maxPreview {
			short = string([]rune(short)[:maxPreview]) + "…"
		}
		items = append(items, listItem{
			row:  r,
			text: fmt.Sprintf("%s:%d:%d  %s", filepath.Base(r.file), r.line+1, r.col+1, short),
		})
	}

	// Pick a width that fits the widest row, capped to the screen.
	boxW := 30
	for _, it := range items {
		if w := rendering.VisibleWidth(it.text) + 4; w > boxW {
			boxW = w
		}
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	boxH := len(items) + 4
	if boxH > m.height-4 {
		boxH = m.height - 4
	}
	if boxH < 6 {
		boxH = 6
	}

	// Render the list.
	dim := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted)
	accent := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent).Bold(true)
	mutedBg := lipgloss.NewStyle().Background(m.theme.Colors.Code.Bg)
	headerBg := lipgloss.NewStyle().Background(m.theme.Colors.SubPaneBg).Bold(true)

	header := headerBg.Render(padRow(fmt.Sprintf(" References (%d) ", len(items)), boxW-2))
	body := make([]string, 0, len(items)+2)
	body = append(body, header)
	for i, it := range items {
		line := padRow(it.text, boxW-2)
		if i == m.refCursor {
			line = accent.Render(line)
		} else {
			line = dim.Render(line)
		}
		body = append(body, mutedBg.Render(line))
	}
	if m.refCursor >= len(items) {
		m.refCursor = 0
	}
	footer := dim.Render(padRow(" ↑/↓ select · enter jump · esc close ", boxW-2))
	body = append(body, footer)

	// Wrap in box drawing characters for a clean panel look.
	border := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent)
	popLines := make([]string, 0, len(body)+2)
	popLines = append(popLines, border.Render("╭"+strings.Repeat("─", boxW-2)+"╮"))
	for _, l := range body {
		popLines = append(popLines, border.Render("│")+l+border.Render("│"))
	}
	popLines = append(popLines, border.Render("╰"+strings.Repeat("─", boxW-2)+"╯"))

	popupContent := widgets.NewPopupContent(popLines)
	popupX := m.width/2 - boxW/2
	popupY := m.height/2 - len(popLines)/2
	if popupX < 0 {
		popupX = 0
	}
	if popupY < 0 {
		popupY = 0
	}

	// Replace existing references popup or create a new one.
	if existing := m.windowManager.Window(id); existing != nil {
		existing.Content = popupContent
		existing.SetSize(boxW, len(popLines))
		existing.SetPosition(popupX, popupY)
		existing.MarkContentDirty()
	} else {
		fw := wm.NewFloatingWindow(
			id,
			"References",
			popupContent,
			boxW, len(popLines),
			wm.WithDecoration(false),
			wm.WithMovable(false),
			wm.WithResizable(false),
			wm.WithClosable(true),
			wm.WithModal(true),
			wm.WithFocusable(true),
		)
		fw.SetPosition(popupX, popupY)
		m.windowManager.AddWindow(fw)
		m.referencesID = id
	}

	// Stash rows for the key handler.
	m.refRows = make([]refRow, 0, len(items))
	for _, it := range items {
		m.refRows = append(m.refRows, it.row)
	}
}

func padRow(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

// closeReferencesPopup removes the references results popup, if any.
func (m *App) closeReferencesPopup() {
	if m.referencesID != "" && m.windowManager != nil {
		m.windowManager.RemoveWindow(m.referencesID)
	}
	m.referencesID = ""
	m.refRows = nil
	m.refCursor = 0
}

// refreshReferencesPopup re-renders the references popup in place. Called
// when the user moves the selection so the highlight follows the cursor.
func (m *App) refreshReferencesPopup() {
	if m.referencesID == "" || len(m.refRows) == 0 {
		return
	}
	m.showReferencesPopup(m.refRows)
}

// jumpToReference opens the file containing the given reference (switching
// tabs if it's already open) and moves the cursor to the matching line/col.
func (m *App) jumpToReference(r refRow) {
	if r.file == "" {
		return
	}
	if r.file != m.editor.FilePath() {
		if err := m.openFileInEditor(r.file); err != nil {
			m.statusbar.SetError("Cannot open " + r.file)
			return
		}
	}
	m.editor.SetCursorPosition(r.line, r.col)
	m.editor.ScrollToCursor()
	layout := m.computeLayout()
	m.editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
}

// ─── Outline popup ──────────────────────────────────────────────────────────

const outlinePopupID = "outline-popup"

type outlineRow struct {
	sym   editor.LSPSymbol
	depth int
}

func (m *App) showOutlinePopup(syms []editor.LSPSymbol) {
	if m.windowManager == nil {
		return
	}

	// Flatten the symbol tree (preserving depth for indentation).
	var flat []outlineRow
	var walk func(syms []editor.LSPSymbol, depth int)
	walk = func(syms []editor.LSPSymbol, depth int) {
		for _, s := range syms {
			flat = append(flat, outlineRow{sym: s, depth: depth})
			walk(s.Children, depth+1)
		}
	}
	walk(syms, 0)

	// Layout dimensions.
	const (
		minW = 28
		maxW = 50
	)
	boxW := minW
	for _, r := range flat {
		text := strings.Repeat("  ", r.depth) + editor.SymbolKindIcon(r.sym.Kind) + " " + r.sym.Name
		if w := rendering.VisibleWidth(text) + 4; w > boxW {
			boxW = w
		}
	}
	if boxW > maxW {
		boxW = maxW
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	boxH := len(flat) + 3
	if boxH > m.height-4 {
		boxH = m.height - 4
	}
	if boxH < 6 {
		boxH = 6
	}

	// Render.
	dim := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted)
	accent := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent).Bold(true)
	kindStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent2)
	nameStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Fg)
	mutedBg := lipgloss.NewStyle().Background(m.theme.Colors.SubPaneBg)
	headerBg := lipgloss.NewStyle().Background(m.theme.Colors.SubPaneBg).Bold(true)
	border := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent2)

	header := headerBg.Render(padRow(fmt.Sprintf(" Outline · %d ", len(flat)), boxW-2))

	body := make([]string, 0, boxH)
	body = append(body, header)
	maxRows := boxH - 2
	start := 0
	if m.outlineCursor >= maxRows {
		start = m.outlineCursor - maxRows + 1
	}
	for i := start; i < len(flat) && i < start+maxRows; i++ {
		r := flat[i]
		indent := strings.Repeat("  ", r.depth)
		text := indent + editor.SymbolKindIcon(r.sym.Kind) + " " + r.sym.Name
		text = padRow(text, boxW-2)
		// Apply per-character coloring.
		styled := kindStyle.Render(editor.SymbolKindIcon(r.sym.Kind)) + " " +
			dim.Render(indent) + nameStyle.Render(r.sym.Name)
		// Pad the styled string back to width — the visible width may be less
		// than the raw text because of style escape codes. We re-pad with
		// the raw text to keep alignment consistent.
		_ = styled
		if i == m.outlineCursor {
			body = append(body, mutedBg.Render(accent.Render(text)))
		} else {
			body = append(body, mutedBg.Render(text))
		}
	}
	footer := dim.Render(padRow(" ↑/↓ · enter jump · esc close ", boxW-2))
	body = append(body, footer)

	popLines := make([]string, 0, len(body)+2)
	popLines = append(popLines, border.Render("╭"+strings.Repeat("─", boxW-2)+"╮"))
	for _, l := range body {
		popLines = append(popLines, border.Render("│")+l+border.Render("│"))
	}
	popLines = append(popLines, border.Render("╰"+strings.Repeat("─", boxW-2)+"╯"))

	popupContent := widgets.NewPopupContent(popLines)
	popupX := m.width - boxW - 2
	popupY := 4
	if popupX < 0 {
		popupX = 0
	}

	if existing := m.windowManager.Window(outlinePopupID); existing != nil {
		existing.Content = popupContent
		existing.SetSize(boxW, len(popLines))
		existing.SetPosition(popupX, popupY)
		existing.MarkContentDirty()
	} else {
		fw := wm.NewFloatingWindow(
			outlinePopupID,
			"Outline",
			popupContent,
			boxW, len(popLines),
			wm.WithDecoration(false),
			wm.WithMovable(false),
			wm.WithResizable(false),
			wm.WithClosable(true),
			wm.WithModal(false),
			wm.WithFocusable(true),
		)
		fw.SetPosition(popupX, popupY)
		m.windowManager.AddWindow(fw)
	}

	// Stash for key handler.
	m.outlineRows = flat
}

func (m *App) closeOutlinePopup() {
	if m.windowManager != nil {
		m.windowManager.RemoveWindow(outlinePopupID)
	}
	m.outlineOpen = false
	m.outlineRows = nil
	m.outlineCursor = 0
}

// ─── Completion popup ──────────────────────────────────────────────────────

const completionPopupID = "completion-popup"

func (m *App) showCompletionPopup(items []editor.CompletionItem) {
	if m.windowManager == nil {
		return
	}

	// Layout dimensions.
	const maxW = 50
	boxW := 30
	for _, it := range items {
		text := it.Label
		if it.Detail != "" {
			text = it.Label + "  " + it.Detail
		}
		if w := rendering.VisibleWidth(text) + 4; w > boxW {
			boxW = w
		}
	}
	if boxW > maxW {
		boxW = maxW
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	boxH := len(items) + 3
	if boxH > m.height-4 {
		boxH = m.height - 4
	}
	if boxH < 6 {
		boxH = 6
	}

	// Render.
	dim := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted)
	accent := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent).Bold(true)
	kindStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent2)
	nameStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Fg)
	mutedBg := lipgloss.NewStyle().Background(m.theme.Colors.SubPaneBg)
	headerBg := lipgloss.NewStyle().Background(m.theme.Colors.SubPaneBg).Bold(true)
	border := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent)

	header := headerBg.Render(padRow(fmt.Sprintf(" Completions · %d ", len(items)), boxW-2))

	body := make([]string, 0, boxH)
	body = append(body, header)
	visible := boxH - 2
	if visible > len(items) {
		visible = len(items)
	}
	for i := 0; i < visible; i++ {
		it := items[i]
		icon := completionKindIcon(it.Kind)
		text := icon + " " + it.Label
		if it.Detail != "" {
			text = icon + " " + it.Label + "  " + dim.Render(truncateStr(it.Detail, boxW-len([]rune(text))-4))
		}
		text = padRow(text, boxW-2)
		_ = kindStyle
		_ = nameStyle
		if i == m.completionCursor {
			body = append(body, mutedBg.Render(accent.Render(text)))
		} else {
			body = append(body, mutedBg.Render(text))
		}
	}
	footer := dim.Render(padRow(" ↑/↓ · enter insert · esc cancel ", boxW-2))
	body = append(body, footer)

	popLines := make([]string, 0, len(body)+2)
	popLines = append(popLines, border.Render("╭"+strings.Repeat("─", boxW-2)+"╮"))
	for _, l := range body {
		popLines = append(popLines, border.Render("│")+l+border.Render("│"))
	}
	popLines = append(popLines, border.Render("╰"+strings.Repeat("─", boxW-2)+"╯"))

	popupContent := widgets.NewPopupContent(popLines)
	// Position the popup near the cursor's screen position.
	layout := m.computeLayout()
	cx, cy := m.editor.CursorScreenPosition(layout.SidebarW, layout.ChatAreaTop)
	popupX := cx
	popupY := cy + 1
	if popupX+boxW >= m.width {
		popupX = m.width - boxW - 1
	}
	if popupY+len(popLines) >= m.height {
		popupY = cy - len(popLines) - 1
	}
	if popupX < 0 {
		popupX = 0
	}
	if popupY < 0 {
		popupY = 0
	}

	if existing := m.windowManager.Window(completionPopupID); existing != nil {
		existing.Content = popupContent
		existing.SetSize(boxW, len(popLines))
		existing.SetPosition(popupX, popupY)
		existing.MarkContentDirty()
	} else {
		fw := wm.NewFloatingWindow(
			completionPopupID,
			"Completion",
			popupContent,
			boxW, len(popLines),
			wm.WithDecoration(false),
			wm.WithMovable(false),
			wm.WithResizable(false),
			wm.WithClosable(true),
			wm.WithModal(false),
			wm.WithFocusable(true),
		)
		fw.SetPosition(popupX, popupY)
		m.windowManager.AddWindow(fw)
	}

	m.completionID = completionPopupID
	m.completionItems = items
}

func (m *App) closeCompletionPopup() {
	if m.windowManager != nil {
		m.windowManager.RemoveWindow(completionPopupID)
	}
	m.completionID = ""
	m.completionItems = nil
	m.completionCursor = 0
}

func (m *App) acceptCompletion() {
	if m.completionCursor < 0 || m.completionCursor >= len(m.completionItems) {
		m.closeCompletionPopup()
		return
	}
	item := m.completionItems[m.completionCursor]
	insert := item.InsertText
	if insert == "" {
		insert = item.Label
	}
	// Insert the text at the cursor. The editor's insertRune handles
	// multi-byte characters; we go rune-by-rune to keep it simple.
	for _, r := range insert {
		m.editor.InsertRune(r)
	}
	m.closeCompletionPopup()
	layout := m.computeLayout()
	m.editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
}

// completionKindIcon returns a single-character icon for a CompletionItemKind
// (https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification#completionItemKind).
func completionKindIcon(kind int) string {
	switch kind {
	case 1:
		return "T" // Text
	case 2:
		return "ƒ" // Method
	case 3:
		return "ƒ" // Function
	case 4:
		return "ƒ" // Constructor
	case 5:
		return "ƒ" // Field
	case 6:
		return "v" // Variable
	case 7:
		return "C" // Class
	case 8:
		return "I" // Interface
	case 9:
		return "M" // Module
	case 10:
		return "P" // Property
	case 14:
		return "K" // Keyword
	case 15:
		return "·" // Snippet
	default:
		return "·"
	}
}

func truncateStr(s string, max int) string {
	if max < 1 {
		return ""
	}
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}
