package viewmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/rendering"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/tui/widgets"
	wm "crude/tui/wm"
	"crude/workspace"
)

func (m *App) dispatchKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Global keybindings from the action registry — checked first,
	// before focus routing. These must work everywhere including the
	// launcher/welcome screen.
	if m.actionRegistry != nil {
		if match := m.actionRegistry.MatchGlobal(msg); match != nil {
			switch match.ID {
			case "palette.open":
				return m.toggleCommandPalette()
			case "search.project.open":
				return m.toggleFindInFiles()
			case "composer.toggle":
				return m.toggleComposer()
			case "app.quit":
				return m.handleQuit()
			case "view.toggle.sidebar":
				m.ToggleSidebar()
				return m, nil
			}
		}
	}

	if ml := m.activeMsgList(); ml != nil && ml.HasActiveSelection() {
		ml.ClearSelection()
		return m, nil
	}

	// Esc always closes the topmost popup first, regardless of focus.
	if msg.Code == tea.KeyEsc && m.windowManager != nil {
		if topWin := m.windowManager.TopWindow(); topWin != nil && topWin.Focusable() {
			if topWin.ClickOutsideBehavior() != wm.ClickOutsideIgnore {
				m.closePopupWindowByID(topWin.ID)
				return m, nil
			}
		}
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
		if m.FocusStack.Top() == FocusTree {
			m.FocusStack.Pop()
		}
		return m, nil
	}
	cmd := m.Tree.Model.Update(filetree.TreeKeyPressMsg{Key: msg})
	return m, cmd
}

func (m *App) dispatchEditorKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		if m.EditorState.Editor.HasSelection() {
			selected := m.EditorState.Editor.SelectedText()
			if selected != "" {
				return m, rendering.CopyToClipboardCmd(selected)
			}
		}
		return m, nil
	}
	if msg.Code == tea.KeyEsc {
		if m.LSPPopup.RenameArmed {
			m.LSPPopup.RenameArmed = false
			m.StatusBar.SetInfo("Rename cancelled")
			return m, nil
		}
		if m.LSPPopup.ReferencesID != "" {
			m.closeReferencesPopup()
			return m, nil
		}
		if m.LSPPopup.CompletionID != "" {
			m.closeCompletionPopup()
			return m, nil
		}
		// Esc cancels pending close and closes the tab. If the user
		// just dismissed a dirty-prompt with Cancel, the next Esc
		// closes the tab directly — no loop because the prompt is
		// already gone. (The 3-button prompt handles Save/Don't Save/
		// Cancel cleanly.)
		m.closeEditorTab()
		return m, nil
	}
	// References popup: fallback when popup is open but editor still has focus.
	if m.LSPPopup.ReferencesID != "" {
		return m, m.handleReferencesPopupKey(msg)
	}
	// Outline/completion navigation is handled when the floating window is
	// focused (dispatchFloatingWindowKey). Keep editor-path fallbacks only
	// when the popup exists but somehow did not take focus.
	if m.LSPPopup.OutlineOpen && m.windowManager != nil && m.windowManager.Window(outlinePopupID) != nil {
		return m, m.handleOutlinePopupKey(msg)
	}
	if m.LSPPopup.CompletionID != "" {
		return m, m.handleCompletionPopupKey(msg)
	}
	// LSP keybinds (only when a file is open and the LSP is up).
	if m.EditorState.Editor.IsOpen() && m.LSPMgr != nil {
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
			if m.EditorState.Editor.CursorLine() == 0 && m.EditorState.Editor.CursorCol() == 0 && m.EditorState.Editor.FirstError() != nil {
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
	if m.EditorState.Editor.IsOpen() {
		switch msg.String() {
		case "ctrl+f":
			m.openFindReplace(false)
			return m, nil
		case "ctrl+h":
			m.openFindReplace(true)
			return m, nil
		case "f3":
			if m.FindReplace != nil {
				m.FindReplace.OnNext()
			} else if m.EditorState.Editor.Search().HasMatches() {
				m.EditorState.Editor.NextMatch()
			}
			return m, nil
		case "shift+f3":
			if m.FindReplace != nil {
				m.FindReplace.OnPrev()
			} else if m.EditorState.Editor.Search().HasMatches() {
				m.EditorState.Editor.PrevMatch()
			}
			return m, nil
		case "ctrl+.":
			layout := m.computeLayout()
			cx, cy := m.EditorState.Editor.CursorScreenPosition(layout.SidebarW, layout.ChatAreaTop)
			m.showContextMenuAt(cx, cy)
			return m, nil
		case "esc":
			if m.FindReplace != nil {
				m.closeFindReplace()
				return m, nil
			}
		}
	}
	cmd := m.EditorState.Editor.HandleEvent(msg)
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
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
		case "references-popup":
			return m, m.handleReferencesPopupKey(msg)
		case "completion-popup":
			return m, m.handleCompletionPopupKey(msg)
		case "outline-popup":
			return m, m.handleOutlinePopupKey(msg)
		case "command-palette":
			if msg.Code == tea.KeyEsc {
				m.overlayHost.Close(ui.OverlayCommandPalette)
				return m, nil
			}
			if m.commandPaletteModel != nil {
				// Route text-editing keys to the search field; navigation/enter
				// to the list. The model handles the filter callback.
				refresh, cmd := m.handleCommandPaletteKey(msg)
				if refresh {
					m.refreshCommandPaletteFrame()
				}
				return m, cmd
			}
			return m, nil

		case "find-in-files":
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)

		case "composer":
			// Ctrl+C with a selection: copy (don't close the
			// composer — matches the chat input's behavior).
			if msg.String() == "ctrl+c" && m.ActiveSession.Composer.HasActiveSelection() {
				return m, rendering.CopyToClipboardCmd(m.ActiveSession.Composer.SelectedText())
			}
			if msg.Code == tea.KeyEsc {
				m.overlayHost.Close(ui.OverlayComposer)
				return m, nil
			}
			updated, cmd, action := m.ActiveSession.Composer.Update(msg)
			m.ActiveSession.Composer = updated
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			switch action {
			case widgets.ComposerCancel:
				m.overlayHost.Close(ui.OverlayComposer)
			case widgets.ComposerSubmit:
				text := m.ActiveSession.Composer.Value()
				m.overlayHost.Close(ui.OverlayComposer)
				if text != "" && m.AgentMgr.ActiveID() != "" {
					cmds = append(cmds, m.submitInput(m.AgentMgr.ActiveID(), text, nil))
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
				m.closePopupWindowByID(fw.ID)
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
		m.Win.Prev()
		m.handleTabSwitch()
		return m, nil
	case tea.KeyRight:
		m.Win.Next()
		m.handleTabSwitch()
		return m, nil
	case tea.KeyEnter:
		if m.FocusStack.Top() == FocusTabBar {
			m.FocusStack.Pop()
		}
		m.handleTabSwitch()
		return m, nil
	case tea.KeyEsc:
		if m.FocusStack.Top() == FocusTabBar {
			m.FocusStack.Pop()
		}
		return m, nil
	default:
		if m.FocusStack.Top() == FocusTabBar {
			m.FocusStack.Pop()
		}
	}
	return m, nil
}

func (m *App) dispatchOverlayKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	isCancel := msg.Code == tea.KeyEsc

	if keyMsg, ok := any(msg).(tea.KeyPressMsg); ok && m.Overlay.TabCount() >= 2 {
		switch keyMsg.String() {
		case "ctrl+]":
			m.Overlay.Next()
			m.syncOverlayMode()
			return m, nil
		case "ctrl+[":
			m.Overlay.Prev()
			m.syncOverlayMode()
			return m, nil
		}
	}

	switch m.OverlayState.Current() {
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
	updated, cmd := m.SessionSettings.Update(msg)
	if updated != nil {
		m.SessionSettings = updated
	}
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
	return m, tea.Batch(*cmds...)
}

func (m *App) handleConfirmKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	m.StatusBar.SetConfirming(true)
	updated, result, done := m.ActiveSession.Confirm.Update(msg)
	m.ActiveSession.Confirm = updated
	if done {
		m.StatusBar.SetConfirming(false)
		m.OverlayState.SetBlocking(false)
		if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil {
			n.State.Thinking = true
		}
		m.StatusBar.SetThinkingForAgent(m.AgentMgr.ActiveID(), true, false, false)
		if m.ResolveConfirmation(m.AgentMgr.ActiveID(), result.Choice) && m.Logger != nil {
			m.Logger.Info("BLOCK", "resolved", "response", result.Choice, "operation", result.Operation, "tool", result.Tool)
		}
		if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil {
			switch result.Choice {
			case ttypes.ConfirmAllow:
				n.State.PendingPermTag = "allowed"
			case ttypes.ConfirmAlways:
				n.State.PendingPermTag = "always"
			case ttypes.ConfirmDeny:
				n.State.PendingPermTag = "denied"
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
	m.StatusBar.SetConfirming(true)

	inTextMode := m.ActiveSession.QuestionForm.IsTextQuestion(m.ActiveSession.QuestionForm.Focused())
	isCancel := msg.Code == tea.KeyEsc

	if inTextMode {
		if isCancel {
			m.ActiveSession.QuestionForm.ExitTextMode(m.ActiveSession.QuestionForm.Focused())
			m.Input.ClearInput()
			m.syncQuestionInputPrompt()
			return m, nil
		}
		if msg.String() == "ctrl+s" {
			text := trimAndSubmit(m.Input.Value())
			if text != "" {
				m.ActiveSession.QuestionForm.SetAnswer(m.ActiveSession.QuestionForm.Focused(), text)
			}
			return m.finishQuestionIfNeeded(cmds), nil
		}
		if msg.Code == tea.KeyTab {
			if msg.Mod.Contains(tea.ModShift) {
				m.ActiveSession.QuestionForm.FocusPrev()
			} else {
				m.ActiveSession.QuestionForm.FocusNext()
			}
			m.syncQuestionInputPrompt()
			return m, nil
		}
		return m.routeToInputInQuestion(msg, cmds), nil
	}

	if isCancel {
		m.ActiveSession.QuestionForm.Dismiss()
		m.OverlayState.SetBlocking(false)
		m.StatusBar.SetConfirming(false)
		m.Input.ResetPrompt()
		m.Input.ClearInput()
		m.closeAllQuestionOverlayTabs()
		m.ResolveQuestion(m.AgentMgr.ActiveID(), nil, nil, true)
		return m, nil
	}

	updated, result, done := m.ActiveSession.QuestionForm.Update(msg)
	m.ActiveSession.QuestionForm = updated
	if !done && m.ActiveSession.QuestionForm.IsTextQuestion(m.ActiveSession.QuestionForm.Focused()) {
		existing := m.ActiveSession.QuestionForm.Answer(m.ActiveSession.QuestionForm.Focused())
		if existing != "" {
			m.Input.SetInput(existing)
		} else {
			m.Input.ClearInput()
		}
	}
	m.syncQuestionInputPrompt()
	if done {
		return m.finishQuestionResult(ttypes.QuestionResult(result), cmds), nil
	}
	return m, nil
}

func (m *App) handleYoloKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	m.StatusBar.SetConfirming(true)
	updated, enabled, done := m.ActiveSession.YoloDlg.Update(msg)
	m.ActiveSession.YoloDlg = updated
	if done {
		m.StatusBar.SetConfirming(false)
		m.OverlayState.SetYolo(enabled)
		var content string
		if enabled {
			content = "⚠ YOLO mode ACTIVE — all safety checks disabled. Use /yolo again to restore."
			m.Logger.Warn("SAFETY", "yolo_enabled")
		} else {
			content = "YOLO mode cancelled. Safety checks remain active."
			m.Logger.Info("SAFETY", "yolo_disabled")
		}
		if m.AgentMgr.ActiveID() != "" {
			m.appendActiveMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: content,
				Time:    time.Now(),
			})
		}
		m.SetYolo(enabled)
		m.closeOverlayTab("YOLO")
	}
	return m, nil
}

func (m *App) handleTextOverlayKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	// Ctrl+C with a selection: copy. Without a selection, Ctrl+C
	// is the cancel shortcut (the original behavior — Esc is also
	// bound to cancel). This matches the chat input's "Ctrl+C is
	// never cancel" rule.
	if msg.String() == "ctrl+c" && m.ActiveSession.TextOverlay.HasActiveSelection() {
		return m, rendering.CopyToClipboardCmd(m.ActiveSession.TextOverlay.SelectedText())
	}
	updated, cmd, action := m.ActiveSession.TextOverlay.Update(msg)
	m.ActiveSession.TextOverlay = updated
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
	switch action {
	case widgets.TextOverlaySave:
		m.saveReferenceFromOverlay()
	case widgets.TextOverlayCancel:
		m.ActiveSession.TextOverlay.Close()
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
		if m.Logger != nil {
			m.Logger.Debug("COPY", "ctrl_c", "mode", m.OverlayState.Name())
		}
		if m.Input.InputSelecting() {
			selected := m.Input.SelectedText()
			if selected != "" {
				return m, rendering.CopyToClipboardCmd(selected)
			}
		}
		return m, nil
	}
	if msg.Code == tea.KeyEsc {
		if m.Logger != nil {
			m.Logger.Debug("CANCEL", "esc", "mode", m.OverlayState.Name())
		}
		return m, m.handleCancel(msg)
	}

	if m.anyActive() && !m.OverlayState.IsBlocking() {
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
		if m.Win.IsOpen() {
			if msg.String() == "ctrl+]" {
				m.Win.Next()
			} else {
				m.Win.Prev()
			}
			m.handleTabSwitch()
			return m, nil
		}
	case "ctrl+t":
		if m.Win.IsOpen() && m.Win.TabCount() >= 1 {
			m.setFocusTabBar()
			return m, nil
		}
	case "ctrl+1":
		if m.Tree.Model != nil && m.Tree.Visible {
			if m.FocusStack.Top() == FocusTree {
				m.FocusStack.Pop()
			} else {
				m.FocusStack.Clear()
				m.FocusStack.Push(FocusTree)
			}
			return m, nil
		}
	}

	if m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen() {
		return m.dispatchEditorKey(msg)
	}

	canType := false
	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil {
		canType = !n.State.Completed && n.State.IsActive()
	}
	updated, cmd, evt := m.Input.Update(msg, canType && !m.OverlayState.IsBlocking(), m.OverlayState.IsBlocking(), m.ChatAreaHeight, m.Input.ShowHint())
	m.Input = updated
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
		if m.LSPPopup.RenameArmed {
			m.LSPPopup.RenameArmed = false
			cmds = append(cmds, m.submitRename(e.Text))
			return cmds
		}
		cmds = append(cmds, m.submitInput(m.AgentMgr.ActiveID(), e.Text, e.ReferenceIDs))
	case input.BtwSubmitEvent:
		cmds = append(cmds, m.submitBtw(m.AgentMgr.ActiveID(), e.Text, e.ReferenceIDs))
	case input.SlashCommandEvent:
		cmds = append(cmds, m.executeSlashCommand(e.Command))
	case input.SettingsOpenEvent:
		m.SessionSettings.SaveSnapshot()
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
	if m.ActiveSession.QuestionForm.AllAnswered() {
		result := m.ActiveSession.QuestionForm.BuildResult()
		m.StatusBar.SetConfirming(false)
		m.OverlayState.SetBlocking(false)
		if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil {
			n.State.Thinking = true
		}
		m.StatusBar.SetThinkingForAgent(m.AgentMgr.ActiveID(), true, false, false)
		questionResult := ttypes.QuestionResult(result)
		m.ResolveQuestion(m.AgentMgr.ActiveID(), questionResult.Answers, questionResult.Questions, questionResult.Cancelled)
		m.closeAllQuestionOverlayTabs()
		m.Input.ResetPrompt()
		m.Input.ClearInput()
	}
	return m
}

func (m *App) finishQuestionResult(result ttypes.QuestionResult, cmds *[]tea.Cmd) tea.Model {
	m.StatusBar.SetConfirming(false)
	m.OverlayState.SetBlocking(false)
	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil {
		n.State.Thinking = true
	}
	m.StatusBar.SetThinkingForAgent(m.AgentMgr.ActiveID(), true, false, false)
	m.ResolveQuestion(m.AgentMgr.ActiveID(), result.Answers, result.Questions, result.Cancelled)
	m.closeAllQuestionOverlayTabs()
	m.Input.ResetPrompt()
	m.Input.ClearInput()
	return m
}

func (m *App) routeToInputInQuestion(msg tea.KeyPressMsg, cmds *[]tea.Cmd) tea.Model {
	updated, cmd, evt := m.Input.Update(msg, false, false, m.ChatAreaHeight, false)
	m.Input = updated
	if cmd != nil {
		*cmds = append(*cmds, cmd)
	}
	switch e := evt.(type) {
	case input.SubmitEvent:
		m.ActiveSession.QuestionForm.SetAnswer(m.ActiveSession.QuestionForm.Focused(), e.Text)
		m.Input.ClearInput()
		if m.ActiveSession.QuestionForm.AllAnswered() {
			m.finishQuestionIfNeeded(cmds)
		} else {
			m.ActiveSession.QuestionForm.FocusNext()
			m.syncQuestionInputPrompt()
		}
	}
	return m
}

// dispatchPasteMsg routes a tea.PasteMsg (from bracketed-paste mode or
// an explicit Ctrl+V) to the correct focus target.
//
// Before this fix, the only two cases the dispatcher knew about were
// "editor" and "everything else → chat input". That meant paste
// silently went to the chat input even when the user was in a
// settings form, a find/replace popup, a text overlay, the file
// picker, the composer, the context menu's text field, etc. — which
// is why "Ctrl+V doesn't work in settings" was the user's #1 pain.
//
// The new dispatch mirrors dispatchKeyPress: it asks the focus
// resolver "where am I?", and routes to the widget that owns the
// focus. Each branch falls through to the next if it doesn't own
// the focus; the very last branch (chat input) is the catch-all for
// "user is typing in the composer" which is the original behaviour
// we want to preserve.
func (m *App) dispatchPasteMsg(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch m.focusTarget() {
	case FocusEditor:
		if m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen() {
			cmd := m.EditorState.Editor.HandleEvent(msg)
			return m, cmd
		}

	case FocusOverlay:
		// Settings / form / YOLO / question / file picker / etc.
		// Each of these widgets has its own Update method that
		// accepts tea.PasteMsg. The overlay mode dispatcher is
		// the single place that knows which overlay is active.
		switch m.OverlayState.Current() {
		case ModeSettings:
			// Route paste to session settings form
			updated, cmd := m.SessionSettings.Update(msg)
			if updated != nil {
				m.SessionSettings = updated
			}
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		case ModeFilePicker:
			// File picker doesn't have a paste target; ignore.
		default:
			// Other overlays (confirm, question, yolo, text overlay)
			// either don't take paste or have their own paste path
			// triggered by the user pressing Enter. Fall through.
		}
		return m, tea.Batch(cmds...)

	case FocusFloatingWindow:
		// A popup is focused. Route to the focused window's
		// content if it knows how to handle paste. The current
		// popups that accept paste are the find/replace popup
		// (via its OnChange callback flow — but the user actually
		// pastes into the find field via the keyboard handler in
		// app.go) and the text overlay / composer (which wrap a
		// components.TextInput). For now, just route to the
		// content if it implements a Paste handler.
		if fw := m.windowManager.Focused(); fw != nil {
			cmd := m.pasteToWindowContent(fw, msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)
	}

	// Default / FocusInput / FocusLauncher / FocusTree / FocusTabBar /
	// FocusNone → route to chat input (the original behaviour).
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
	return m, tea.Batch(cmds...)
}

// pasteToWindowContent forwards a PasteMsg to the content of a
// focused floating window. The content must implement either
// HandleEvent(tea.Msg) tea.Cmd or Update(tea.Msg) — whichever
// matches its public API. Returns the resulting cmd (or nil).
func (m *App) pasteToWindowContent(fw *wm.FloatingWindow, msg tea.PasteMsg) tea.Cmd {
	if fw == nil || fw.Content == nil {
		return nil
	}
	switch c := fw.Content.(type) {
	case interface{ HandleEvent(tea.Msg) tea.Cmd }:
		return c.HandleEvent(msg)
	case interface{ Update(tea.Msg) tea.Cmd }:
		return c.Update(msg)
	}
	return nil
}

func (m *App) dispatchEditorTick(msg any) []tea.Cmd {
	var cmds []tea.Cmd
	if m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen() {
		cmd := m.EditorState.Editor.HandleEvent(msg)
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

//  ─ LSP request commands             ──
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
	if !m.EditorState.Editor.IsOpen() || m.LSPMgr == nil {
		return nil
	}
	filePath := m.EditorState.Editor.FilePath()
	line, col := m.EditorState.Editor.CursorLine(), m.EditorState.Editor.CursorCol()
	conn, status, _ := m.LSPMgr.Status()
	if !conn || status != editor.LSPConnected {
		m.StatusBar.SetInfo("LSP not ready")
		return nil
	}
	lspMgr := m.LSPMgr
	return tea.Cmd(func() tea.Msg {
		locs := lspMgr.RequestDefinition(context.Background(), filePath, line, col)
		return lspDefinitionResultMsg{filePath: filePath, locations: locs}
	})
}

// requestReferencesCmd fires a "find references" request at the cursor.
func (m *App) requestReferencesCmd() tea.Cmd {
	if !m.EditorState.Editor.IsOpen() || m.LSPMgr == nil {
		return nil
	}
	filePath := m.EditorState.Editor.FilePath()
	line, col := m.EditorState.Editor.CursorLine(), m.EditorState.Editor.CursorCol()
	conn, status, _ := m.LSPMgr.Status()
	if !conn || status != editor.LSPConnected {
		m.StatusBar.SetInfo("LSP not ready")
		return nil
	}
	lspMgr := m.LSPMgr
	return tea.Cmd(func() tea.Msg {
		locs := lspMgr.FindReferences(context.Background(), filePath, line, col, true)
		return lspReferencesResultMsg{filePath: filePath, locations: locs}
	})
}

// requestDocumentSymbolsCmd refreshes the document outline for the current
// file and shows it in the outline panel.
func (m *App) requestDocumentSymbolsCmd() tea.Cmd {
	if !m.EditorState.Editor.IsOpen() || m.LSPMgr == nil {
		return nil
	}
	filePath := m.EditorState.Editor.FilePath()
	conn, status, _ := m.LSPMgr.Status()
	if !conn || status != editor.LSPConnected {
		m.StatusBar.SetInfo("LSP not ready")
		return nil
	}
	lspMgr := m.LSPMgr
	return tea.Cmd(func() tea.Msg {
		syms := lspMgr.RequestDocumentSymbols(context.Background(), filePath)
		return lspDocumentSymbolsResultMsg{filePath: filePath, symbols: syms}
	})
}

// startRenameCmd opens the rename input prompt, pre-filled with the symbol at
// the cursor. The actual rename request fires when the user confirms the new
// name in the input.
func (m *App) startRenameCmd() tea.Cmd {
	if !m.EditorState.Editor.IsOpen() || m.LSPMgr == nil {
		return nil
	}
	conn, status, _ := m.LSPMgr.Status()
	if !conn || status != editor.LSPConnected {
		m.StatusBar.SetInfo("LSP not ready")
		return nil
	}
	// Arm rename mode: the next text submitted through the input is
	// treated as the new symbol name. Esc cancels. The status bar
	// explains what's happening.
	m.LSPPopup.RenameArmed = true
	m.StatusBar.SetInfo("Rename: type new name and press Enter (Esc to cancel)")
	return nil
}

// submitRename fires the actual rename request once the user has typed a
// new name. Called from the input handler when renameArmed is true.
func (m *App) submitRename(newName string) tea.Cmd {
	m.LSPPopup.RenameArmed = false
	if !m.EditorState.Editor.IsOpen() || m.LSPMgr == nil || newName == "" {
		return nil
	}
	filePath := m.EditorState.Editor.FilePath()
	line, col := m.EditorState.Editor.CursorLine(), m.EditorState.Editor.CursorCol()
	lspMgr := m.LSPMgr
	return tea.Cmd(func() tea.Msg {
		edit, err := lspMgr.RequestRename(context.Background(), filePath, line, col, newName)
		return lspRenameResultMsg{filePath: filePath, newName: newName, edit: edit, err: err}
	})
}

// requestCompletionCmd fires a completion request at the cursor.
func (m *App) requestCompletionCmd() tea.Cmd {
	if !m.EditorState.Editor.IsOpen() || m.LSPMgr == nil {
		return nil
	}
	filePath := m.EditorState.Editor.FilePath()
	line, col := m.EditorState.Editor.CursorLine(), m.EditorState.Editor.CursorCol()
	conn, status, _ := m.LSPMgr.Status()
	if !conn || status != editor.LSPConnected {
		m.StatusBar.SetInfo("LSP not ready")
		return nil
	}
	lspMgr := m.LSPMgr
	return tea.Cmd(func() tea.Msg {
		items := lspMgr.RequestCompletions(context.Background(), filePath, line, col)
		return lspCompletionResultMsg{filePath: filePath, line: line, col: col, items: items}
	})
}

//  LSP result handlers ─

// handleDefinitionResult reacts to a "go to definition" response. If the
// server returned a single location, the editor jumps straight to it. If
// multiple, the file is opened at the first and a status-bar hint shows the
// total count.
func (m *App) handleDefinitionResult(msg lspDefinitionResultMsg) {
	if len(msg.locations) == 0 {
		m.StatusBar.SetInfo("No definition found")
		return
	}
	loc := msg.locations[0]
	if loc.Path == "" {
		m.StatusBar.SetInfo("Definition in unindexed location")
		return
	}
	if loc.Path != m.EditorState.Editor.FilePath() {
		if err := m.openFileInEditor(loc.Path); err != nil {
			m.StatusBar.SetError("Cannot open " + loc.Path)
			return
		}
	}
	m.EditorState.Editor.SetCursorPosition(loc.Line, loc.Col)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	if len(msg.locations) > 1 {
		m.StatusBar.SetInfo(fmt.Sprintf("Definition: %d locations", len(msg.locations)))
	}
}

// handleReferencesResult shows a popup with the list of references. If the
// user presses Enter on a row, the editor jumps to that location.
func (m *App) handleReferencesResult(msg lspReferencesResultMsg) {
	if len(msg.locations) == 0 {
		m.StatusBar.SetInfo("No references found")
		return
	}
	rows := make([]refRow, 0, len(msg.locations))
	for _, loc := range msg.locations {
		preview := ""
		if line, err := m.EditorState.Editor.LineTextFromPath(loc.Path, loc.Line); err == nil {
			preview = line
		}
		rows = append(rows, refRow{File: loc.Path, Line: loc.Line, Col: loc.Col, Preview: preview})
	}
	m.showReferencesPopup(rows)
}

// handleDocumentSymbolsResult stores the symbols for the outline panel and
// opens it. Empty result closes the outline.
func (m *App) handleDocumentSymbolsResult(msg lspDocumentSymbolsResultMsg) {
	if len(msg.symbols) == 0 {
		m.StatusBar.SetInfo("No symbols in file")
		m.LSPPopup.OutlineOpen = false
		m.closeOutlinePopup()
		return
	}
	m.LSPPopup.OutlineOpen = true
	m.StatusBar.SetInfo(fmt.Sprintf("Outline: %d symbols", countSymbols(msg.symbols)))
	m.showOutlinePopup(msg.symbols)
}

// handleCompletionResult shows a list of completion items in a floating
// window. The list is navigable with up/down; pressing Enter inserts the
// selected item's text at the cursor.
func (m *App) handleCompletionResult(msg lspCompletionResultMsg) {
	if len(msg.items) == 0 {
		m.StatusBar.SetInfo("No completions")
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
		m.StatusBar.SetError("Rename failed: " + msg.err.Error())
		return
	}
	edit, ok := msg.edit.(*protocol.WorkspaceEdit)
	if !ok || edit == nil {
		m.StatusBar.SetInfo("Rename: no changes")
		return
	}

	// Collect all edits into FileEdit structs for the workspace service.
	var fileEdits []workspace.FileEdit
	var filePaths []string
	for uri, edits := range edit.Changes {
		fp := uriToPath(string(uri))
		data, err := os.ReadFile(fp)
		if err != nil {
			m.Logger.Warn("LSP", "rename_read", "file", fp, "error", err.Error())
			continue
		}
		updated, err := applyTextEdits(string(data), edits)
		if err != nil {
			m.Logger.Warn("LSP", "rename_apply", "file", fp, "error", err.Error())
			continue
		}
		fileEdits = append(fileEdits, workspace.FileEdit{
			Path:       fp,
			OldContent: string(data),
			NewContent: updated,
		})
		filePaths = append(filePaths, fp)
	}

	if len(fileEdits) == 0 {
		m.StatusBar.SetInfo("Rename: no changes applied")
		return
	}

	// Apply all edits through the workspace service.
	result := m.ApplyWorkspaceEdit(fileEdits)
	if !result.Succeeded() {
		m.StatusBar.SetError("Rename: " + result.Error())
		return
	}

	// Reload editors for changed files.
	for _, fp := range filePaths {
		if m.EditorState.Editor.IsOpen() && m.EditorState.Editor.FilePath() == fp {
			if err := m.EditorState.Editor.Reload(); err != nil {
				m.Logger.Warn("LSP", "rename_reload", "file", fp, "error", err.Error())
			}
		}
	}

	m.StatusBar.SetInfo(fmt.Sprintf("Renamed in %d file%s", len(fileEdits), pluralFile(len(fileEdits))))
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
		line    int
		char    int
		endLine int
		endChar int
		newText string
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
type refRow = RefRow

func (m *App) showReferencesPopup(rows []refRow) {
	if m.windowManager == nil {
		return
	}
	const id = "references-popup"

	const maxPreview = 60
	listItems := make([]ui.ListItem, 0, len(rows))
	for i, r := range rows {
		short := r.Preview
		if len([]rune(short)) > maxPreview {
			short = string([]rune(short)[:maxPreview]) + "…"
		}
		label := fmt.Sprintf("%s:%d:%d", filepath.Base(r.File), r.Line+1, r.Col+1)
		listItems = append(listItems, ui.ListItem{
			ID:      fmt.Sprintf("%d", i),
			Label:   label,
			Detail:  short,
			Payload: i, // index into RefRows
		})
	}

	// Pick a width that fits the widest row, capped to the screen.
	boxW := 30
	for _, it := range listItems {
		text := it.Label
		if it.Detail != "" {
			text += "  " + it.Detail
		}
		if w := rendering.VisibleWidth(text) + 8; w > boxW {
			boxW = w
		}
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	// chrome (2) + rows; min 6
	boxH := len(listItems) + 2
	if boxH > m.height-4 {
		boxH = m.height - 4
	}
	if boxH < 6 {
		boxH = 6
	}
	if m.LSPPopup.RefCursor >= len(listItems) {
		m.LSPPopup.RefCursor = 0
	}
	if m.LSPPopup.RefCursor < 0 && len(listItems) > 0 {
		m.LSPPopup.RefCursor = 0
	}

	title := fmt.Sprintf("References (%d)", len(listItems))
	frame := widgets.BuildListPopupFrame(m.Theme, widgets.ListPopupSpec{
		ID:       id,
		Title:    title,
		Items:    listItems,
		Selected: m.LSPPopup.RefCursor,
		Width:    boxW,
		Height:   boxH,
		Closable: true,
	})

	popupX := m.width/2 - boxW/2
	popupY := m.height/2 - boxH/2
	if popupX < 0 {
		popupX = 0
	}
	if popupY < 0 {
		popupY = 0
	}

	if existing := m.windowManager.Window(id); existing != nil {
		// Prefer mutating selection when content is already a ComponentWidget.
		if cw, ok := existing.Content.(*ui.ComponentWidget); ok && cw != nil {
			if list := widgets.ListPopupList(cw); list != nil {
				list.SetItems(listItems)
				list.SelectIndex(m.LSPPopup.RefCursor)
				existing.SetSize(boxW, boxH)
				existing.SetPosition(popupX, popupY)
				existing.MarkContentDirty()
			} else {
				existing.Content = frame
				existing.SetSize(boxW, boxH)
				existing.SetPosition(popupX, popupY)
				existing.MarkContentDirty()
			}
		} else {
			existing.Content = frame
			existing.SetSize(boxW, boxH)
			existing.SetPosition(popupX, popupY)
			existing.MarkContentDirty()
		}
	} else {
		fw := wm.NewFloatingWindow(
			id,
			"",
			frame,
			boxW, boxH,
			wm.WithDecoration(false),
			wm.WithMovable(false),
			wm.WithResizable(false),
			wm.WithClosable(false),
			wm.WithModal(true),
			wm.WithFocusable(true),
			wm.WithClickOutside(wm.ClickOutsideCloseRoute),
		)
		fw.SetPosition(popupX, popupY)
		m.overlayHost.Open(ui.OverlayReferences, fw, ui.PopoverPolicy(true), func() {
			m.LSPPopup.ReferencesID = ""
			m.LSPPopup.RefRows = nil
			m.LSPPopup.RefCursor = 0
		})
		m.LSPPopup.ReferencesID = id
	}

	m.LSPPopup.RefRows = rows
}

// closeReferencesPopup removes the references results popup, if any.
func (m *App) closeReferencesPopup() {
	m.overlayHost.Close(ui.OverlayReferences)
}

// refreshReferencesPopup re-renders the references popup in place. Called
// when the user moves the selection so the highlight follows the cursor.
func (m *App) refreshReferencesPopup() {
	if m.LSPPopup.ReferencesID == "" || len(m.LSPPopup.RefRows) == 0 {
		return
	}
	m.showReferencesPopup(m.LSPPopup.RefRows)
}

// syncReferencesListCursor updates the WindowFrame list selection without a full rebuild.
func (m *App) syncReferencesListCursor() {
	if m.windowManager == nil || m.LSPPopup.ReferencesID == "" {
		return
	}
	fw := m.windowManager.Window(m.LSPPopup.ReferencesID)
	if fw == nil {
		return
	}
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil {
		if list := widgets.ListPopupList(cw); list != nil {
			list.SelectIndex(m.LSPPopup.RefCursor)
			fw.MarkContentDirty()
			return
		}
	}
	m.refreshReferencesPopup()
}

// jumpToReference opens the file containing the given reference (switching
// tabs if it's already open) and moves the cursor to the matching line/col.
func (m *App) jumpToReference(r refRow) {
	if r.File == "" {
		return
	}
	if r.File != m.EditorState.Editor.FilePath() {
		if err := m.openFileInEditor(r.File); err != nil {
			m.StatusBar.SetError("Cannot open " + r.File)
			return
		}
	}
	m.EditorState.Editor.SetCursorPosition(r.Line, r.Col)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
}

// ─── Outline popup ──────────────────────────────────────────────────────────

const outlinePopupID = "outline-popup"

type outlineRow = OutlineRow

func (m *App) showOutlinePopup(syms []editor.LSPSymbol) {
	if m.windowManager == nil {
		return
	}

	// Flatten the symbol tree (preserving depth for indentation).
	var flat []outlineRow
	var walk func(syms []editor.LSPSymbol, depth int)
	walk = func(syms []editor.LSPSymbol, depth int) {
		for _, s := range syms {
			flat = append(flat, outlineRow{Sym: s, Depth: depth})
			walk(s.Children, depth+1)
		}
	}
	walk(syms, 0)

	listItems := make([]ui.ListItem, 0, len(flat))
	for i, r := range flat {
		// Indent via Icon prefix so WindowFrame+List owns row paint.
		icon := strings.Repeat("  ", r.Depth) + editor.SymbolKindIcon(r.Sym.Kind)
		listItems = append(listItems, ui.ListItem{
			ID:      fmt.Sprintf("%d", i),
			Icon:    icon,
			Label:   r.Sym.Name,
			Payload: i,
		})
	}

	const (
		minW = 28
		maxW = 50
	)
	boxW := minW
	for _, it := range listItems {
		text := it.Icon + " " + it.Label
		if w := rendering.VisibleWidth(text) + 8; w > boxW {
			boxW = w
		}
	}
	if boxW > maxW {
		boxW = maxW
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	// chrome (2) + rows
	boxH := len(listItems) + 2
	if boxH > m.height-4 {
		boxH = m.height - 4
	}
	if boxH < 6 {
		boxH = 6
	}
	if m.LSPPopup.OutlineCursor >= len(listItems) {
		m.LSPPopup.OutlineCursor = 0
	}
	if m.LSPPopup.OutlineCursor < 0 && len(listItems) > 0 {
		m.LSPPopup.OutlineCursor = 0
	}

	title := fmt.Sprintf("Outline · %d", len(listItems))
	frame := widgets.BuildListPopupFrame(m.Theme, widgets.ListPopupSpec{
		ID:       outlinePopupID,
		Title:    title,
		Items:    listItems,
		Selected: m.LSPPopup.OutlineCursor,
		Width:    boxW,
		Height:   boxH,
		Closable: true,
	})

	popupX := m.width - boxW - 2
	popupY := 4
	if popupX < 0 {
		popupX = 0
	}
	if popupY < 0 {
		popupY = 0
	}

	if existing := m.windowManager.Window(outlinePopupID); existing != nil {
		if cw, ok := existing.Content.(*ui.ComponentWidget); ok && cw != nil {
			if list := widgets.ListPopupList(cw); list != nil {
				list.SetItems(listItems)
				list.SelectIndex(m.LSPPopup.OutlineCursor)
				existing.SetSize(boxW, boxH)
				existing.SetPosition(popupX, popupY)
				existing.MarkContentDirty()
			} else {
				existing.Content = frame
				existing.SetSize(boxW, boxH)
				existing.SetPosition(popupX, popupY)
				existing.MarkContentDirty()
			}
		} else {
			existing.Content = frame
			existing.SetSize(boxW, boxH)
			existing.SetPosition(popupX, popupY)
			existing.MarkContentDirty()
		}
	} else {
		fw := wm.NewFloatingWindow(
			outlinePopupID,
			"",
			frame,
			boxW, boxH,
			wm.WithDecoration(false),
			wm.WithMovable(false),
			wm.WithResizable(false),
			wm.WithClosable(false),
			wm.WithModal(false),
			wm.WithFocusable(true),
			wm.WithClickOutside(wm.ClickOutsideCloseRoute),
		)
		fw.SetPosition(popupX, popupY)
		m.overlayHost.Open(ui.OverlayOutline, fw, ui.PopoverPolicy(true), func() {
			m.LSPPopup.OutlineOpen = false
			m.LSPPopup.OutlineRows = nil
			m.LSPPopup.OutlineCursor = 0
		})
	}

	m.LSPPopup.OutlineRows = flat
	m.LSPPopup.OutlineOpen = true
}

func (m *App) closeOutlinePopup() {
	m.overlayHost.Close(ui.OverlayOutline)
}

func (m *App) syncOutlineListCursor() {
	if m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window(outlinePopupID)
	if fw == nil {
		return
	}
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil {
		if list := widgets.ListPopupList(cw); list != nil {
			list.SelectIndex(m.LSPPopup.OutlineCursor)
			fw.MarkContentDirty()
			return
		}
	}
	// Rebuild only if frame is missing list content.
	if m.LSPMgr != nil && m.EditorState.Editor != nil {
		m.showOutlinePopup(m.LSPMgr.DocumentSymbols(m.EditorState.Editor.FilePath()))
	}
}

func (m *App) handleOutlinePopupKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "down":
		if m.LSPPopup.OutlineCursor < len(m.LSPPopup.OutlineRows)-1 {
			m.LSPPopup.OutlineCursor++
			m.syncOutlineListCursor()
		}
	case "up":
		if m.LSPPopup.OutlineCursor > 0 {
			m.LSPPopup.OutlineCursor--
			m.syncOutlineListCursor()
		}
	case "enter":
		if m.LSPPopup.OutlineCursor >= 0 && m.LSPPopup.OutlineCursor < len(m.LSPPopup.OutlineRows) {
			sym := m.LSPPopup.OutlineRows[m.LSPPopup.OutlineCursor].Sym
			m.EditorState.Editor.SetCursorPosition(sym.Line, sym.Col)
			m.EditorState.Editor.ScrollToCursor()
			layout := m.computeLayout()
			m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
			// Keep outline open (keyboard jump); mouse activation closes.
		}
	case "esc":
		m.closeOutlinePopup()
	}
	return nil
}

func (m *App) jumpToOutline(idx int) {
	if idx < 0 || idx >= len(m.LSPPopup.OutlineRows) {
		return
	}
	sym := m.LSPPopup.OutlineRows[idx].Sym
	m.EditorState.Editor.SetCursorPosition(sym.Line, sym.Col)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
}

// ─── Completion popup ──────────────────────────────────────────────────────

const completionPopupID = "completion-popup"

func (m *App) showCompletionPopup(items []editor.CompletionItem) {
	if m.windowManager == nil {
		return
	}

	listItems := make([]ui.ListItem, 0, len(items))
	for i, it := range items {
		listItems = append(listItems, ui.ListItem{
			ID:      fmt.Sprintf("%d", i),
			Icon:    completionKindIcon(it.Kind),
			Label:   it.Label,
			Detail:  it.Detail,
			Payload: i,
		})
	}

	const maxW = 50
	boxW := 30
	for _, it := range listItems {
		text := it.Icon + " " + it.Label
		if it.Detail != "" {
			text += "  " + it.Detail
		}
		if w := rendering.VisibleWidth(text) + 8; w > boxW {
			boxW = w
		}
	}
	if boxW > maxW {
		boxW = maxW
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	boxH := len(listItems) + 2
	if boxH > m.height-4 {
		boxH = m.height - 4
	}
	if boxH < 6 {
		boxH = 6
	}
	if m.LSPPopup.CompletionCursor >= len(listItems) {
		m.LSPPopup.CompletionCursor = 0
	}
	if m.LSPPopup.CompletionCursor < 0 && len(listItems) > 0 {
		m.LSPPopup.CompletionCursor = 0
	}

	title := fmt.Sprintf("Completions · %d", len(listItems))
	frame := widgets.BuildListPopupFrame(m.Theme, widgets.ListPopupSpec{
		ID:       completionPopupID,
		Title:    title,
		Items:    listItems,
		Selected: m.LSPPopup.CompletionCursor,
		Width:    boxW,
		Height:   boxH,
		Closable: true,
	})

	layout := m.computeLayout()
	cx, cy := m.EditorState.Editor.CursorScreenPosition(layout.SidebarW, layout.ChatAreaTop)
	popupX := cx
	popupY := cy + 1
	if popupX+boxW >= m.width {
		popupX = m.width - boxW - 1
	}
	if popupY+boxH >= m.height {
		popupY = cy - boxH - 1
	}
	if popupX < 0 {
		popupX = 0
	}
	if popupY < 0 {
		popupY = 0
	}

	if existing := m.windowManager.Window(completionPopupID); existing != nil {
		if cw, ok := existing.Content.(*ui.ComponentWidget); ok && cw != nil {
			if list := widgets.ListPopupList(cw); list != nil {
				list.SetItems(listItems)
				list.SelectIndex(m.LSPPopup.CompletionCursor)
				existing.SetSize(boxW, boxH)
				existing.SetPosition(popupX, popupY)
				existing.MarkContentDirty()
			} else {
				existing.Content = frame
				existing.SetSize(boxW, boxH)
				existing.SetPosition(popupX, popupY)
				existing.MarkContentDirty()
			}
		} else {
			existing.Content = frame
			existing.SetSize(boxW, boxH)
			existing.SetPosition(popupX, popupY)
			existing.MarkContentDirty()
		}
	} else {
		fw := wm.NewFloatingWindow(
			completionPopupID,
			"",
			frame,
			boxW, boxH,
			wm.WithDecoration(false),
			wm.WithMovable(false),
			wm.WithResizable(false),
			wm.WithClosable(false),
			wm.WithModal(false),
			wm.WithFocusable(true),
			wm.WithClickOutside(wm.ClickOutsideCloseRoute),
		)
		fw.SetPosition(popupX, popupY)
		m.overlayHost.Open(ui.OverlayCompletion, fw, ui.PopoverPolicy(true), func() {
			m.LSPPopup.CompletionID = ""
			m.LSPPopup.CompletionItems = nil
			m.LSPPopup.CompletionCursor = 0
		})
	}

	m.LSPPopup.CompletionID = completionPopupID
	m.LSPPopup.CompletionItems = items
}

func (m *App) closeCompletionPopup() {
	m.overlayHost.Close(ui.OverlayCompletion)
}

func (m *App) syncCompletionListCursor() {
	if m.windowManager == nil || m.LSPPopup.CompletionID == "" {
		return
	}
	fw := m.windowManager.Window(m.LSPPopup.CompletionID)
	if fw == nil {
		return
	}
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil {
		if list := widgets.ListPopupList(cw); list != nil {
			list.SelectIndex(m.LSPPopup.CompletionCursor)
			fw.MarkContentDirty()
			return
		}
	}
	m.showCompletionPopup(m.LSPPopup.CompletionItems)
}

func (m *App) handleCompletionPopupKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "down":
		if m.LSPPopup.CompletionCursor < len(m.LSPPopup.CompletionItems)-1 {
			m.LSPPopup.CompletionCursor++
			m.syncCompletionListCursor()
		}
	case "up":
		if m.LSPPopup.CompletionCursor > 0 {
			m.LSPPopup.CompletionCursor--
			m.syncCompletionListCursor()
		}
	case "tab", "enter":
		m.acceptCompletion()
	case "esc":
		m.closeCompletionPopup()
	}
	return nil
}

func (m *App) handleReferencesPopupKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "down":
		if m.LSPPopup.RefCursor < len(m.LSPPopup.RefRows)-1 {
			m.LSPPopup.RefCursor++
			m.syncReferencesListCursor()
		}
	case "up":
		if m.LSPPopup.RefCursor > 0 {
			m.LSPPopup.RefCursor--
			m.syncReferencesListCursor()
		}
	case "enter":
		if m.LSPPopup.RefCursor >= 0 && m.LSPPopup.RefCursor < len(m.LSPPopup.RefRows) {
			r := m.LSPPopup.RefRows[m.LSPPopup.RefCursor]
			m.jumpToReference(r)
			m.closeReferencesPopup()
		}
	case "esc":
		m.closeReferencesPopup()
	}
	return nil
}

func (m *App) acceptCompletion() {
	if m.LSPPopup.CompletionCursor < 0 || m.LSPPopup.CompletionCursor >= len(m.LSPPopup.CompletionItems) {
		m.closeCompletionPopup()
		return
	}
	item := m.LSPPopup.CompletionItems[m.LSPPopup.CompletionCursor]
	insert := item.InsertText
	if insert == "" {
		insert = item.Label
	}
	// Insert the text at the cursor. The editor's insertRune handles
	// multi-byte characters; we go rune-by-rune to keep it simple.
	for _, r := range insert {
		m.EditorState.Editor.InsertRune(r)
	}
	m.closeCompletionPopup()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
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
