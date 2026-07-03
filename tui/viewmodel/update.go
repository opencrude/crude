package viewmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/provider"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
	"crude/tui/wm"
)

func (m *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Dirty is set selectively per message type, NOT unconditionally.
	// The full render pipeline (chroma syntax highlighting, glamour
	// markdown, compositor, ANSI generation) runs only when dirty
	// is true. Without this guard, every MouseMotionMsg (120-200/sec)
	// triggers a complete frame rebuild from scratch, causing 100%
	// CPU usage.

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.logger.Debug("UI", "resize", "width", msg.Width, "height", msg.Height)
		m.dirty = true
		m.contentDirty = true
		m.width = msg.Width
		m.height = msg.Height
		for _, a := range m.agents {
			if a.MsgList != nil {
				a.MsgList.SetSize(msg.Width, msg.Height)
			}
		}
		m.paneMgr.SetSize(msg.Width, msg.Height)
		m.input.SetWidth(msg.Width)
		m.statusbar.SetWidth(msg.Width)
		if m.compositor != nil {
			m.compositor.Resize(msg.Width, msg.Height)
		}
		// Resize all layer surfaces so they match the new dimensions.
		m.resizeLayerSurfaces()
		return m, nil

	case ttypes.TickMsg:
		return m.handleTick(msg)

	case tea.KeyPressMsg:
		m.dirty = true
		m.contentDirty = true
		return m.dispatchKeyPress(msg)

	case tea.PasteMsg:
		m.dirty = true
		m.contentDirty = true
		return m.dispatchPasteMsg(msg)

	case tea.MouseClickMsg:
		m.dirty = true
		m.contentDirty = true
		m.logger.Debug("UI", "mouse", "click", "x", msg.Mouse().X, "y", msg.Mouse().Y, "button", fmt.Sprintf("%d", msg.Button))
		return m.dispatchMouseClick(msg)

	case tea.MouseWheelMsg:
		m.dirty = true
		m.contentDirty = true
		m.logger.Debug("UI", "mouse", "wheel", "x", msg.X, "y", msg.Y, "button", fmt.Sprintf("%d", msg.Button))
		return m.dispatchMouseWheel(msg)

	case tea.MouseMotionMsg:
		// Do NOT set dirty here — the motion handler itself sets
		// m.dirty = true when hover state or other visual state
		// actually changes. Without this guard, every pixel of
		// mouse movement triggers a full frame rebuild.
		return m.dispatchMouseMotion(msg)

	case tea.MouseReleaseMsg:
		// Same as MouseMotionMsg: let the release handler set
		// dirty when it actually does something visible.
		m.logger.Debug("UI", "mouse", "release", "x", msg.Mouse().X, "y", msg.Mouse().Y)
		return m.dispatchMouseRelease(msg)

	case ttypes.SelectionTickMsg:
		m.dirty = true
		if ml := m.activeMsgList(); ml != nil {
			_, cmd := ml.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case scrollbar.FadeTickMsg, scrollbar.HideTickMsg:
		m.dirty = true
		cmds = append(cmds, m.dispatchEditorTick(msg)...)
		if ml := m.activeMsgList(); ml != nil {
			cmd := ml.HandleScrollbarTick(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if m.treeVisible && m.treeRenderer != nil {
			treeCmd := m.treeRenderer.HandleScrollbarTick(msg)
			if treeCmd != nil {
				cmds = append(cmds, treeCmd)
			}
		}
		if m.commandPaletteModel != nil {
			updated, sbCmd := m.commandPaletteModel.sb.Update(msg)
			m.commandPaletteModel.sb = updated
			if sbCmd != nil {
				cmds = append(cmds, sbCmd)
			}
		}
		for _, sp := range m.paneMgr.panes {
			if !sp.Done {
				if sp.MsgList != nil {
					paneCmd := sp.MsgList.HandleScrollbarTick(msg)
					if paneCmd != nil {
						cmds = append(cmds, paneCmd)
					}
				}
				sp.HandleScrollbarTick(msg)
			}
		}
		for _, w := range m.windowManager.Windows() {
			if handler, ok := w.Content.(interface{ HandleEvent(tea.Msg) tea.Cmd }); ok {
				if tickCmd := handler.HandleEvent(msg); tickCmd != nil {
					cmds = append(cmds, tickCmd)
				}
			}
		}

	default:
		if m.isEditorMsg(msg) {
			// Only set dirty when the cursor visibility actually
			// changes. During the 530ms solid-cursor window after
			// any activity, the blink tick is a no-op — setting
			// dirty would waste a full frame render for nothing.
			cursorBefore := m.editor != nil && m.editor.CursorVisible()
			cmds = append(cmds, m.dispatchEditorTick(msg)...)
			cursorAfter := m.editor != nil && m.editor.CursorVisible()
			if cursorBefore != cursorAfter {
				m.dirty = true
				m.contentDirty = true
			}
		}
	}

	cmds = append(cmds, m.handleAppMessages(msg)...)

	return m, tea.Batch(cmds...)
}

func (m *App) handleTick(msg ttypes.TickMsg) (tea.Model, tea.Cmd) {
	spinFrame := int(time.Now().UnixMilli())
	m.spinFrame = spinFrame
	m.statusbar.SetSpinFrame(spinFrame)
	m.statusbar.SetCurrentAgent(m.activeAgentView)
	m.input.SetSpinFrame(spinFrame)
	m.statusbar.ClearExpired()
	for _, a := range m.agents {
		a.Tick(spinFrame)
	}
	m.paneMgr.TickAll(spinFrame)
	m.syncAgentWindowTabs()
	for _, sp := range m.paneMgr.panes {
		if !sp.Done && sp.MsgList != nil {
			if agent, ok := m.agents[sp.ID]; ok {
				if agent.TokenBuf != "" {
					sp.MsgList.AppendToken(agent.TokenBuf)
					agent.TokenBuf = ""
					agent.LastTokenTime = time.Now()
				}
			}
			if ml := m.activeMsgList(); ml != nil {
				ml.InvalidateSubPane(sp.ID)
			}
		}
	}
	if m.editor.IsOpen() && m.editor.DiskChanged() {
		// Notify the LSP server that the file changed on disk. This fires
		// regardless of which tab is active — the LSP needs disk-change
		// notifications even when the user is in the file tree or chat.
		if m.lspManager != nil {
			lspCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			m.lspManager.NotifyWatchedFilesChanged(lspCtx, m.editor.FilePath(), 2) // 2 = Changed
			cancel()
		}
		// Only auto-reload when the editor has no unsaved (dirty) changes;
		// otherwise the user's in-progress edits would be lost.
		if !m.editor.Modified() {
			if err := m.editor.Reload(); err != nil {
				m.logger.Error("EDITOR", "reload_error", "error", err.Error())
			} else if m.lspManager != nil {
				// Push the reloaded content to the LSP server. The LSP
				// treats open files as in-memory documents (fed via
				// didChange) and ignores disk changes for them, so
				// without this the server's document stays stale and it
				// never re-analyzes the externally-modified file.
				lspCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				m.lspManager.UpdateFile(lspCtx, m.editor.FilePath(), m.editor.Value())
				cancel()
			}
		}
	}

	connected, status, errMsg := m.lspManager.Status()
	statusbarLSP := connected || status == editor.LSPConnected || status == editor.LSPStarting || errMsg != ""

	// Status bar is EXCLUSIVELY for whole-project insights.
	// Per-file diagnostics are shown in the editor (gutter + hover popup +
	// editor's own status line at the bottom of the editor body).
	if statusbarLSP {
		ins := m.lspManager.ProjectInsights()
		m.statusbar.SetProjectInsights(ins)
	} else {
		m.statusbar.SetProjectInsights(editor.ProjectInsights{})
	}

	// Mark dirty every tick — cheap layers (header, status bar, sidebar)
	// re-render on every frame anyway, and the launcher needs animation
	// ticks to drive its spin frame and scroll effects.
	isLauncher := len(m.agents) == 0 && m.activeEditorView == "" && m.launcher != nil
	if m.statusbar.HasAnimation() || isLauncher {
		m.dirty = true
	}

	if m.anyActive() {
		return m, streamingTickCmd()
	}
	if len(m.agents) == 0 && m.activeEditorView == "" {
		return m, streamingTickCmd()
	}
	return m, tickCmd()
}

func (m *App) handleAppMessages(msg tea.Msg) []tea.Cmd {
	var cmds []tea.Cmd

	if msg, ok := msg.(ttypes.AgentMsg); ok {
		cmds = append(cmds, m.handleAgentEvent(ttypes.AgentEventMsg(msg)))
	}

	switch msg := msg.(type) {
	case lspDefinitionResultMsg:
		m.handleDefinitionResult(msg)
	case lspReferencesResultMsg:
		m.handleReferencesResult(msg)
	case lspDocumentSymbolsResultMsg:
		m.handleDocumentSymbolsResult(msg)
	case lspRenameResultMsg:
		m.handleRenameResult(msg)
	case lspCompletionResultMsg:
		m.handleCompletionResult(msg)
	case ttypes.ErrorMsg:
		m.logger.Debug("UI", "error", "msg", string(msg))
		m.statusbar.SetError(string(msg))
	case ttypes.UpdateModelMsg:
		m.modelName = string(msg)
		m.logger.Debug("UI", "model_update", "model", string(msg))
		for _, a := range m.agents {
			if a.MsgList != nil {
				a.MsgList.SetModelName(string(msg))
			}
		}
		if ml := m.activeMsgList(); ml != nil {
			ml.SetModelName(string(msg))
		}
	case ttypes.OpenSettingsMsg:
		m.logger.Info("UI", "open_settings")
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)
	case ttypes.ModelsListMsg:
		m.logger.Info("MODEL", "list_received", "count", len(msg))
		m.settings.SetModels([]ttypes.ModelEntry(msg))
	case ttypes.ModelsErrorMsg:
		m.logger.Error("MODEL", "list_error", "error", string(msg))
		m.settings.SetModelsError(string(msg))
	case ttypes.SettingsSaveResultMsg:
		if msg.Err != nil {
			m.logger.Error("CONFIG", "save_error", "error", msg.Err.Error())
			m.statusbar.SetError(msg.Err.Error())
		} else {
			m.logger.Info("CONFIG", "saved")
			m.modelName = msg.Settings.Model
			m.lastSettings = msg.Settings
			m.closeOverlayTab("Settings")
			m.statusbar.SetInfo("settings saved")
		}
	case rendering.ClipboardCopiedMsg:
		m.logger.Debug("CLIPBOARD", "copied")
		m.statusbar.SetInfo("copied to clipboard")
	case rendering.ClipboardErrorMsg:
		m.logger.Error("CLIPBOARD", "error", "error", string(msg))
		m.statusbar.SetError(string(msg))
	case ttypes.UndoResultMsg:
		if msg.Error != "" {
			m.logger.Error("JOURNAL", "undo_error", "error", msg.Error)
			m.statusbar.SetError(msg.Error)
		}
		if msg.Desc != "" && m.activeAgentView != "" {
			if ml := m.activeMsgList(); ml != nil {
				ml.AppendMessage(ttypes.ChatMessage{
					Role:    ttypes.RoleAssistant,
					Content: msg.Desc,
					Time:    time.Now(),
				})
			}
		}
	case ttypes.HistoryMessagesMsg:
		if ml := m.activeMsgList(); ml != nil {
			for _, hm := range msg {
				ml.AppendMessage(hm)
			}
			ml.ScrollToBottom()
		}
	case widgets.FilePickerSelectMsg:
		m.addFileReference(msg.Path)
		m.windowManager.RemoveWindow("filepicker")
		m.setMode(ModeChat)
	case widgets.FilePickerCancelMsg:
		m.windowManager.RemoveWindow("filepicker")
		m.setMode(ModeChat)
	case filetree.FileSelectedMsg:
		m.logger.Info("TREE", "file_selected", "path", msg.Path)
		cmd, err := m.openEditorTab(msg.Path)
		if err != nil {
			m.logger.Error("TREE", "open_editor_error", "error", err.Error())
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.unfocus()
		m.syncRenderFocus()
	case filetree.EnterFilterModeMsg:
		m.logger.Debug("TREE", "enter_filter_mode")
		m.treeFocused = true
	case filetree.TreeRefreshDoneMsg:
		if msg.Err != nil {
			m.logger.Error("TREE", "refresh_error", "error", msg.Err.Error())
			m.statusbar.SetError("tree refresh error: " + msg.Err.Error())
		}
	}

	return cmds
}

func (m *App) ToggleSidebar() {
	if m.treeModel == nil {
		return
	}
	m.treeVisible = !m.treeVisible
	if m.treeVisible {
		if m.activeEditorView == "" {
			m.treeFocused = true
		}
		m.treeModel.Update(filetree.TreeWidthMsg{Width: m.treeWidth})
	} else {
		m.treeFocused = false
	}
	m.dirty = true
}

func (m *App) translateToLocalMouse(msg tea.Msg, fw *wm.FloatingWindow) tea.Msg {
	if fw == nil {
		return msg
	}
	winX, winY := fw.Position()
	bounds := fw.ContentBounds()
	offX := winX + bounds.X
	offY := winY + bounds.Y

	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		mouse := mm.Mouse()
		return tea.MouseClickMsg{X: mouse.X - offX, Y: mouse.Y - offY, Button: mm.Button}
	case tea.MouseMotionMsg:
		mouse := mm.Mouse()
		return tea.MouseMotionMsg{X: mouse.X - offX, Y: mouse.Y - offY, Button: mm.Button}
	case tea.MouseReleaseMsg:
		mouse := mm.Mouse()
		return tea.MouseReleaseMsg{X: mouse.X - offX, Y: mouse.Y - offY, Button: mm.Button}
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg{X: mm.X - offX, Y: mm.Y - offY, Button: mm.Button}
	}
	return msg
}

func (m *App) translateToTreeMouse(msg tea.Msg) tea.Msg {
	offsetY := m.treeYOffset
	bcOff := m.breadcrumbOffset()
	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		mouse := mm.Mouse()
		return tea.MouseClickMsg{X: mouse.X, Y: mouse.Y - offsetY - bcOff, Button: mm.Button}
	case tea.MouseMotionMsg:
		mouse := mm.Mouse()
		return tea.MouseMotionMsg{X: mouse.X, Y: mouse.Y - offsetY - bcOff, Button: mm.Button}
	case tea.MouseReleaseMsg:
		mouse := mm.Mouse()
		return tea.MouseReleaseMsg{X: mouse.X, Y: mouse.Y - offsetY - bcOff, Button: mm.Button}
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg{X: mm.X, Y: mm.Y - offsetY - bcOff, Button: mm.Button}
	}
	return msg
}

func (m *App) forwardEventToWindowContent(fw *wm.FloatingWindow, msg tea.Msg) tea.Cmd {
	if fw == nil || fw.Content == nil {
		return nil
	}
	localMsg := m.translateToLocalMouse(msg, fw)
	if handler, ok := fw.Content.(interface{ HandleEvent(tea.Msg) tea.Cmd }); ok {
		cmd := handler.HandleEvent(localMsg)
		fw.MarkContentDirty()
		return cmd
	}
	return nil
}

func translateMouseX(msg tea.Msg, delta int) tea.Msg {
	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		mouse := mm.Mouse()
		return tea.MouseClickMsg{X: mouse.X + delta, Y: mouse.Y, Button: mm.Button}
	case tea.MouseMotionMsg:
		mouse := mm.Mouse()
		return tea.MouseMotionMsg{X: mouse.X + delta, Y: mouse.Y, Button: mm.Button}
	case tea.MouseReleaseMsg:
		mouse := mm.Mouse()
		return tea.MouseReleaseMsg{X: mouse.X + delta, Y: mouse.Y, Button: mm.Button}
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg{X: mm.X + delta, Y: mm.Y, Button: mm.Button}
	}
	return msg
}

func translateMouseY(msg tea.Msg, delta int) tea.Msg {
	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		mouse := mm.Mouse()
		return tea.MouseClickMsg{X: mouse.X, Y: mouse.Y + delta, Button: mm.Button}
	case tea.MouseMotionMsg:
		mouse := mm.Mouse()
		return tea.MouseMotionMsg{X: mouse.X, Y: mouse.Y + delta, Button: mm.Button}
	case tea.MouseReleaseMsg:
		mouse := mm.Mouse()
		return tea.MouseReleaseMsg{X: mouse.X, Y: mouse.Y + delta, Button: mm.Button}
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg{X: mm.X, Y: mm.Y + delta, Button: mm.Button}
	}
	return msg
}

func (m *App) isMouseMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg:
		return true
	}
	return false
}

func (m *App) routeMouseToInlinePane(msg tea.Msg) (bool, tea.Cmd) {
	layout := m.computeLayout()

	var y int
	var isWheel bool
	var wheelUp, wheelDown bool

	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		y = mm.Mouse().Y
	case tea.MouseWheelMsg:
		y = mm.Y
		isWheel = true
		wheelUp = mm.Button == tea.MouseWheelUp
		wheelDown = mm.Button == tea.MouseWheelDown
	case tea.MouseMotionMsg:
		y = mm.Mouse().Y
	case tea.MouseReleaseMsg:
		y = mm.Mouse().Y
	default:
		return false, nil
	}

	ml := m.activeMsgList()
	if ml == nil {
		return false, nil
	}
	paneLineMap := ml.PaneLineMap()
	y2 := y - layout.ChatAreaTop
	if y2 < 0 || y2 >= len(paneLineMap) {
		return false, nil
	}

	info := paneLineMap[y2]
	if info.PaneID == "" || info.LineIdx < 0 {
		return false, nil
	}

	sp := m.paneMgr.Get(info.PaneID)
	if sp == nil || sp.Done {
		return false, nil
	}

	if sp.CanScrollCompact() {
		if wheelUp {
			sp.ScrollCompactUp()
			ml.InvalidateSubPane(sp.ID)
			return true, nil
		}
		if wheelDown {
			sp.ScrollCompactDown()
			ml.InvalidateSubPane(sp.ID)
			return true, nil
		}
		return true, nil
	}

	if sp.MsgList == nil {
		return false, nil
	}

	if isWheel && !sp.MsgList.CanScroll() {
		return false, nil
	}

	_, cmd := sp.MsgList.Update(msg)
	return true, cmd
}

func (m *App) handleAgentTabClose(index int) tea.Cmd {
	if index < 0 || index >= m.win.TabCount() {
		return nil
	}
	tab := m.win.Tabs()[index]
	if !tab.Closable {
		return nil
	}

	if m.isAgentTabTitle(tab.Title) {
		agentID := m.agentIDFromTabTitle(tab.Title)
		m.win.CloseTab(index)
		m.cleanupAgent(agentID)
		if m.OnCancelSubAgent != nil {
			go m.OnCancelSubAgent(agentID)
		}
		m.handleTabSwitch()
		if len(m.agents) == 0 && m.activeEditorView == "" {
			if m.onListSessions != nil {
				m.launcher = NewLauncherState(m.onListSessions())
				m.lastInteractionTime = time.Now()
			}
			m.dirty = true
			return nil
		}
		m.dirty = true
		return nil
	}

	if m.isEditorTabTitle(tab.Title) {
		path := m.editorPathFromTabTitle(tab.Title)
		// If the tab has unsaved edits, prompt before destroying the
		// buffer. Otherwise close immediately.
		ed := m.editor
		if t, ok := m.editorTabs[path]; ok {
			ed = t.Editor
		}
		if ed != nil && ed.Modified() && m.Mode == ModeChat {
			m.showDirtyClosePrompt(path)
			return nil
		}
		m.closeEditorTabForce(path)
		return nil
	}

	m.win.CloseTab(index)
	m.handleTabSwitch()
	if len(m.agents) == 0 && m.activeEditorView == "" {
		if m.onListSessions != nil {
			m.launcher = NewLauncherState(m.onListSessions())
			m.lastInteractionTime = time.Now()
		}
	}
	return nil
}

func (m *App) handleOverlayTabClose(index int) tea.Cmd {
	if index < 0 || index >= m.overlay.TabCount() {
		return nil
	}
	tab := m.overlay.Tabs()[index]
	if !tab.Closable {
		return nil
	}

	switch tab.Title {
	case "Settings":
		m.settings.RestoreSnapshot()
		m.overlay.CloseTab(index)
		m.syncOverlayMode()
		if ml := m.activeMsgList(); ml != nil {
			ml.InvalidateCache()
		}
	case "Confirm":
		m.confirm.Dismiss()
		m.blocking = false
		m.statusbar.SetConfirming(false)
		m.overlay.CloseTab(index)
		m.syncOverlayMode()
		if m.OnConfirmResult != nil {
			m.OnConfirmResult(ttypes.ConfirmResult{Choice: ttypes.ConfirmDeny})
		}
	case "Question":
		m.questionForm.Dismiss()
		m.blocking = false
		m.statusbar.SetConfirming(false)
		m.input.ResetPrompt()
		m.input.ClearInput()
		m.overlay.CloseTab(index)
		m.syncOverlayMode()
		if m.OnQuestionResult != nil {
			m.OnQuestionResult(ttypes.QuestionResult{Cancelled: true})
		}
	case "YOLO":
		m.yoloDlg.Dismiss()
		m.statusbar.SetConfirming(false)
		m.overlay.CloseTab(index)
		m.syncOverlayMode()
	default:
		m.overlay.CloseTab(index)
		m.syncOverlayMode()
	}

	return nil
}

func (m *App) closeAllQuestionOverlayTabs() {
	m.closeOverlayTab("Question")
}

func (m *App) purgeQuestionOverlayTabs() {
	if tab := m.overlay.TabByTitle("Question"); tab != nil {
		m.overlay.CloseTabByID(tab.ID)
	}
}

func (m *App) closeOverlayTab(title string) {
	if tab := m.overlay.TabByTitle(title); tab != nil {
		m.overlay.CloseTabByID(tab.ID)
	}
	m.syncOverlayMode()
	if ml := m.activeMsgList(); ml != nil {
		ml.InvalidateCache()
	}
}

func (m *App) syncQuestionInputPrompt() {
	if m.Mode != ModeQuestion {
		m.input.ResetPrompt()
		m.input.ClearInput()
		return
	}
	if tab := m.overlay.TabByTitle("Question"); tab != nil {
		m.overlay.SetTabIconByIndex(m.overlay.IndexOf(tab.ID), m.questionForm.FocusedIcon())
	}
	if m.questionForm.IsTextQuestion(m.questionForm.Focused()) {
		q, _ := m.questionForm.FocusedItem()
		label := q.Header
		if label == "" {
			label = q.Question
			if rendering.VisibleWidth(label) > 16 {
				label = rendering.TruncateVisual(label, 16, "…")
			}
		}
		m.input.SetPrompt(fmt.Sprintf("Q%d %s", m.questionForm.Focused()+1, label))
		existing := m.questionForm.Answer(m.questionForm.Focused())
		if existing != "" {
			m.input.SetInput(existing)
		}
	} else {
		m.input.ResetPrompt()
		m.input.ClearInput()
	}
}

// handleTabSwitch re-derives the active editor/agent view from the
// current window tab. Called on every tab change (open, close,
// switch). Both activeEditorView and activeAgentView are *always*
// cleared at the start so that closing the last tab correctly falls
// back to the launcher view — leaving them stale when no tab is
// active would break the renderer's region classification and cause
// Ctrl+Q to attempt a no-op close instead of quitting.
func (m *App) handleTabSwitch() {
	// Always reset before re-deriving. This is the single source of
	// truth for which view is active.
	m.activeEditorView = ""
	m.activeAgentView = ""

	activeTab := m.win.ActiveTab()
	if activeTab == nil {
		return
	}

	if m.isAgentTabTitle(activeTab.Title) {
		agentID := m.agentIDFromTabTitle(activeTab.Title)
		m.activeAgentView = agentID
		if agent, ok := m.agents[agentID]; ok && agent.MsgList != nil {
			agent.MsgList.InvalidateAllMessages()
		}
		return
	}

	if m.isEditorTabTitle(activeTab.Title) {
		path := m.editorPathFromTabTitle(activeTab.Title)
		// Look up the tab's pre-loaded Editor. We DO NOT call
		// editor.Open() here — that would reload the file from disk
		// and destroy cursor, scroll, undo, and any unsaved edits.
		// The Editor was created and loaded once in openEditorTab and
		// lives in the editorTabs map for the lifetime of the tab.
		tab, ok := m.editorTabs[path]
		if !ok {
			// Tab entry is missing (shouldn't normally happen, but be
			// defensive). Re-open to recover.
			if m.logger != nil {
				m.logger.Warn("EDITOR", "tab_missing_on_switch", "path", path)
			}
			_, err := m.openEditorTab(path)
			if err != nil && m.logger != nil {
				m.logger.Error("EDITOR", "reopen_failed", "error", err.Error())
			}
			return
		}
		m.activeEditorView = path
		m.editor = tab.Editor
	}
}

func (m *App) openReferenceOverlay(refID string) {
	if m.refStore == nil {
		return
	}
	ref, ok := m.refStore.GetReference(refID)
	if !ok || !ref.Active() {
		return
	}
	title := ref.DisplayName()
	if title == "" {
		title = ref.ID
	}
	m.textOverlay.Open(refID, title, ref.Content)
	m.setMode(ModeTextOverlay)
}

func (m *App) saveReferenceFromOverlay() {
	refID := m.textOverlay.RefID()
	content := m.textOverlay.Value()
	m.textOverlay.Close()
	if m.refStore != nil && refID != "" {
		_ = m.refStore.UpdateReferenceContent(refID, content)
	}
	m.setMode(ModeChat)
}

func (m *App) openFilePicker() {
	root, _ := m.workingDir()
	m.filePicker.Open(root)
	m.setMode(ModeFilePicker)
}

func (m *App) addFileReference(path string) {
	if m.refStore == nil || path == "" {
		return
	}
	mime, isImage := provider.ImageMimeFromExt(filepath.Ext(path))
	kind := sessiondb.KindSelectedFile
	if isImage {
		kind = sessiondb.KindPastedImage
	} else {
		mime = "text/plain"
	}
	ref, err := m.refStore.CreateReference(sessiondb.CreateRequest{
		Kind:     kind,
		Title:    filepath.Base(path),
		Mime:     mime,
		FilePath: path,
		Inline:   false,
	})
	if err != nil {
		m.statusbar.SetError("Failed to add file reference")
		return
	}
	m.input.InsertReference(ref)
}

func (m *App) workingDir() (string, error) {
	return os.Getwd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(400*time.Millisecond, func(t time.Time) tea.Msg {
		return ttypes.TickMsg(t)
	})
}

func streamingTickCmd() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg {
		return ttypes.TickMsg(t)
	})
}
