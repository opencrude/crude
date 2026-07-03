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
	"crude/tui/workspace"
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
		m.Logger.Debug("UI", "resize", "width", msg.Width, "height", msg.Height)
		m.dirty = true
		m.contentDirty = true
		m.width = msg.Width
		m.height = msg.Height
		for _, n := range m.AgentMgr.AllNodes() {
			if n.State != nil && n.State.MsgList != nil {
				n.State.MsgList.SetSize(msg.Width, msg.Height)
			}
		}
		m.PaneMgr.SetSize(msg.Width, msg.Height)
		m.Input.SetWidth(msg.Width)
		m.StatusBar.SetWidth(msg.Width)
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
		m.Logger.Debug("UI", "mouse", "click", "x", msg.Mouse().X, "y", msg.Mouse().Y, "button", fmt.Sprintf("%d", msg.Button))
		return m.dispatchMouseClick(msg)

	case tea.MouseWheelMsg:
		m.dirty = true
		m.contentDirty = true
		m.Logger.Debug("UI", "mouse", "wheel", "x", msg.X, "y", msg.Y, "button", fmt.Sprintf("%d", msg.Button))
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
		m.Logger.Debug("UI", "mouse", "release", "x", msg.Mouse().X, "y", msg.Mouse().Y)
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
		if m.Tree.Visible && m.Tree.Renderer != nil {
			treeCmd := m.Tree.Renderer.HandleScrollbarTick(msg)
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
		for _, sp := range m.PaneMgr.Panes() {
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
			cursorBefore := m.EditorState.Editor != nil && m.EditorState.Editor.CursorVisible()
			cmds = append(cmds, m.dispatchEditorTick(msg)...)
			cursorAfter := m.EditorState.Editor != nil && m.EditorState.Editor.CursorVisible()
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
	m.SpinFrame = spinFrame
	m.StatusBar.SetSpinFrame(spinFrame)
	m.StatusBar.SetCurrentAgent(m.AgentMgr.ActiveID())
	m.Input.SetSpinFrame(spinFrame)
	m.StatusBar.ClearExpired()
	for _, n := range m.AgentMgr.AllNodes() {
		if n.State != nil {
			n.State.Tick(spinFrame)
		}
	}
	m.PaneMgr.TickAll(spinFrame)
	m.syncAgentWindowTabs()
	for _, sp := range m.PaneMgr.Panes() {
		if !sp.Done && sp.MsgList != nil {
			if n := m.AgentMgr.Get(sp.ID); n != nil && n.State != nil {
				if n.State.TokenBuf != "" {
					sp.MsgList.AppendToken(n.State.TokenBuf)
					n.State.TokenBuf = ""
					n.State.LastTokenTime = time.Now()
				}
			}
			if ml := m.activeMsgList(); ml != nil {
				ml.InvalidateSubPane(sp.ID)
			}
		}
	}
	if m.EditorState.Editor.IsOpen() && m.EditorState.Editor.DiskChanged() {
		// Notify the LSP server that the file changed on disk. This fires
		// regardless of which tab is active — the LSP needs disk-change
		// notifications even when the user is in the file tree or chat.
		if m.LSPMgr != nil {
			lspCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			m.LSPMgr.NotifyWatchedFilesChanged(lspCtx, m.EditorState.Editor.FilePath(), 2) // 2 = Changed
			cancel()
		}
		// Only auto-reload when the editor has no unsaved (dirty) changes;
		// otherwise the user's in-progress edits would be lost.
		if !m.EditorState.Editor.Modified() {
			if err := m.EditorState.Editor.Reload(); err != nil {
				m.Logger.Error("EDITOR", "reload_error", "error", err.Error())
			} else if m.LSPMgr != nil {
				// Push the reloaded content to the LSP server. The LSP
				// treats open files as in-memory documents (fed via
				// didChange) and ignores disk changes for them, so
				// without this the server's document stays stale and it
				// never re-analyzes the externally-modified file.
				lspCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				m.LSPMgr.UpdateFile(lspCtx, m.EditorState.Editor.FilePath(), m.EditorState.Editor.Value())
				cancel()
			}
		}
	}

	connected, status, errMsg := m.LSPMgr.Status()
	statusbarLSP := connected || status == editor.LSPConnected || status == editor.LSPStarting || errMsg != ""

	// Status bar is EXCLUSIVELY for whole-project insights.
	// Per-file diagnostics are shown in the editor (gutter + hover popup +
	// editor's own status line at the bottom of the editor body).
	if statusbarLSP {
		ins := m.LSPMgr.ProjectInsights()
		m.StatusBar.SetProjectInsights(ins)
	} else {
		m.StatusBar.SetProjectInsights(editor.ProjectInsights{})
	}

	// Mark dirty every tick — cheap layers (header, status bar, sidebar)
	// re-render on every frame anyway, and the launcher needs animation
	// ticks to drive its spin frame and scroll effects.
	isLauncher := m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" && m.launcher != nil
	if m.StatusBar.HasAnimation() || isLauncher {
		m.dirty = true
	}

	if m.anyActive() {
		return m, streamingTickCmd()
	}
	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
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
	case workspace.LSPDiagnosticsMsg:
		m.handleLSPDiagnosticsUpdate(msg)
	case workspace.FileWatcherMsg:
		m.handleFileWatcherEvent(msg)
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
		m.Logger.Debug("UI", "error", "msg", string(msg))
		m.StatusBar.SetError(string(msg))
	case ttypes.UpdateModelMsg:
		m.ModelName = string(msg)
		m.Logger.Debug("UI", "model_update", "model", string(msg))
		for _, n := range m.AgentMgr.AllNodes() {
			if n.State != nil && n.State.MsgList != nil {
				n.State.MsgList.SetModelName(string(msg))
			}
		}
		if ml := m.activeMsgList(); ml != nil {
			ml.SetModelName(string(msg))
		}
	case ttypes.OpenSettingsMsg:
		m.Logger.Info("UI", "open_settings")
		m.Settings.SaveSnapshot()
		m.setMode(ModeSettings)
	case ttypes.ModelsListMsg:
		m.Logger.Info("MODEL", "list_received", "count", len(msg))
		m.Settings.SetModels([]ttypes.ModelEntry(msg))
	case ttypes.ModelsErrorMsg:
		m.Logger.Error("MODEL", "list_error", "error", string(msg))
		m.Settings.SetModelsError(string(msg))
	case ttypes.SettingsSaveResultMsg:
		if msg.Err != nil {
			m.Logger.Error("CONFIG", "save_error", "error", msg.Err.Error())
			m.StatusBar.SetError(msg.Err.Error())
		} else {
			m.Logger.Info("CONFIG", "saved")
			m.ModelName = msg.Settings.Model
			m.LastSettings = msg.Settings
			m.closeOverlayTab("Settings")
			m.StatusBar.SetInfo("settings saved")
		}
	case rendering.ClipboardCopiedMsg:
		m.Logger.Debug("CLIPBOARD", "copied")
		m.StatusBar.SetInfo("copied to clipboard")
	case rendering.ClipboardErrorMsg:
		m.Logger.Error("CLIPBOARD", "error", "error", string(msg))
		m.StatusBar.SetError(string(msg))
	case ttypes.UndoResultMsg:
		if msg.Error != "" {
			m.Logger.Error("JOURNAL", "undo_error", "error", msg.Error)
			m.StatusBar.SetError(msg.Error)
		}
		if msg.Desc != "" && m.AgentMgr.ActiveID() != "" {
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
		m.Logger.Info("TREE", "file_selected", "path", msg.Path)
		cmd, err := m.openEditorTab(msg.Path)
		if err != nil {
			m.Logger.Error("TREE", "open_editor_error", "error", err.Error())
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.unfocus()
		m.syncRenderFocus()
	case filetree.EnterFilterModeMsg:
		m.Logger.Debug("TREE", "enter_filter_mode")
		m.FocusStack.Clear()
		m.FocusStack.Push(FocusTree)
	case filetree.TreeRefreshDoneMsg:
		if msg.Err != nil {
			m.Logger.Error("TREE", "refresh_error", "error", msg.Err.Error())
			m.StatusBar.SetError("tree refresh error: " + msg.Err.Error())
		}
	}

	return cmds
}

func (m *App) ToggleSidebar() {
	if m.Tree.Model == nil {
		return
	}
	m.Tree.Visible = !m.Tree.Visible
	if m.Tree.Visible {
		if m.EditorState.ActiveView == "" {
			m.FocusStack.Clear()
			m.FocusStack.Push(FocusTree)
		}
		m.Tree.Model.Update(filetree.TreeWidthMsg{Width: m.Tree.Width})
	} else {
		if m.FocusStack.Top() == FocusTree {
			m.FocusStack.Pop()
		}
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
	offsetY := m.Tree.YOffset
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

	sp := m.PaneMgr.Get(info.PaneID)
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
	if index < 0 || index >= m.Win.TabCount() {
		return nil
	}
	tab := m.Win.Tabs()[index]
	if !tab.Closable {
		return nil
	}

	if m.isAgentTabTitle(tab.Title) {
		agentID := m.agentIDFromTabTitle(tab.Title)
		m.Win.CloseTab(index)
		m.cleanupAgent(agentID)
		if m.ActiveSession.OnCancelSubAgent != nil {
			go m.ActiveSession.OnCancelSubAgent(agentID)
		}
		m.handleTabSwitch()
		if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
			if m.OnListSessions != nil {
				m.launcher = NewLauncherState(m.OnListSessions())
				m.LastInteractionTime = time.Now()
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
		ed := m.EditorState.Editor 
		if t, ok := m.EditorState.Tabs[path]; ok {
			ed = t.Editor
		}
		if ed != nil && ed.Modified() && m.OverlayState.IsChat() {
			m.showDirtyClosePrompt(path)
			return nil
		}
		m.closeEditorTabForce(path)
		return nil
	}

	m.Win.CloseTab(index)
	m.handleTabSwitch()
	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		if m.OnListSessions != nil {
			m.launcher = NewLauncherState(m.OnListSessions())
			m.LastInteractionTime = time.Now()
		}
	}
	return nil
}

func (m *App) handleOverlayTabClose(index int) tea.Cmd {
	if index < 0 || index >= m.Overlay.TabCount() {
		return nil
	}
	tab := m.Overlay.Tabs()[index]
	if !tab.Closable {
		return nil
	}

	switch tab.Title {
	case "Settings":
		m.Settings.RestoreSnapshot()
		m.Overlay.CloseTab(index)
		m.syncOverlayMode()
		if ml := m.activeMsgList(); ml != nil {
			ml.InvalidateCache()
		}
	case "Confirm":
		m.ActiveSession.Confirm.Dismiss()
		m.OverlayState.SetBlocking(false)
		m.StatusBar.SetConfirming(false)
		m.Overlay.CloseTab(index)
		m.syncOverlayMode()
		if m.ActiveSession.OnConfirmResult != nil {
			m.ActiveSession.OnConfirmResult(ttypes.ConfirmResult{Choice: ttypes.ConfirmDeny})
		}
	case "Question":
		m.ActiveSession.QuestionForm.Dismiss()
		m.OverlayState.SetBlocking(false)
		m.StatusBar.SetConfirming(false)
		m.Input.ResetPrompt()
		m.Input.ClearInput()
		m.Overlay.CloseTab(index)
		m.syncOverlayMode()
		if m.ActiveSession.OnQuestionResult != nil {
			m.ActiveSession.OnQuestionResult(ttypes.QuestionResult{Cancelled: true})
		}
	case "YOLO":
		m.ActiveSession.YoloDlg.Dismiss()
		m.StatusBar.SetConfirming(false)
		m.Overlay.CloseTab(index)
		m.syncOverlayMode()
	default:
		m.Overlay.CloseTab(index)
		m.syncOverlayMode()
	}

	return nil
}

func (m *App) closeAllQuestionOverlayTabs() {
	m.closeOverlayTab("Question")
}

func (m *App) purgeQuestionOverlayTabs() {
	if tab := m.Overlay.TabByTitle("Question"); tab != nil {
		m.Overlay.CloseTabByID(tab.ID)
	}
}

func (m *App) closeOverlayTab(title string) {
	if tab := m.Overlay.TabByTitle(title); tab != nil {
		m.Overlay.CloseTabByID(tab.ID)
	}
	m.syncOverlayMode()
	if ml := m.activeMsgList(); ml != nil {
		ml.InvalidateCache()
	}
}

func (m *App) syncQuestionInputPrompt() {
	if !m.OverlayState.Is(ModeQuestion) {
		m.Input.ResetPrompt()
		m.Input.ClearInput()
		return
	}
	if tab := m.Overlay.TabByTitle("Question"); tab != nil {
		m.Overlay.SetTabIconByIndex(m.Overlay.IndexOf(tab.ID), m.ActiveSession.QuestionForm.FocusedIcon())
	}
	if m.ActiveSession.QuestionForm.IsTextQuestion(m.ActiveSession.QuestionForm.Focused()) {
		q, _ := m.ActiveSession.QuestionForm.FocusedItem()
		label := q.Header
		if label == "" {
			label = q.Question
			if rendering.VisibleWidth(label) > 16 {
				label = rendering.TruncateVisual(label, 16, "…")
			}
		}
		m.Input.SetPrompt(fmt.Sprintf("Q%d %s", m.ActiveSession.QuestionForm.Focused()+1, label))
		existing := m.ActiveSession.QuestionForm.Answer(m.ActiveSession.QuestionForm.Focused())
		if existing != "" {
			m.Input.SetInput(existing)
		}
	} else {
		m.Input.ResetPrompt()
		m.Input.ClearInput()
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
	m.EditorState.ActiveView = ""
	m.AgentMgr.ClearActive()

	activeTab := m.Win.ActiveTab()
	if activeTab == nil {
		return
	}

	if m.isAgentTabTitle(activeTab.Title) {
		agentID := m.agentIDFromTabTitle(activeTab.Title)
		m.AgentMgr.SetActive(agentID)
		if n := m.AgentMgr.Get(agentID); n != nil && n.State != nil && n.State.MsgList != nil {
			n.State.MsgList.InvalidateAllMessages()
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
		tab, ok := m.EditorState.Tabs[path]
		if !ok {
			// Tab entry is missing (shouldn't normally happen, but be
			// defensive). Re-open to recover.
			if m.Logger != nil {
				m.Logger.Warn("EDITOR", "tab_missing_on_switch", "path", path)
			}
			_, err := m.openEditorTab(path)
			if err != nil && m.Logger != nil {
				m.Logger.Error("EDITOR", "reopen_failed", "error", err.Error())
			}
			return
		}
		m.EditorState.ActiveView = path
		m.EditorState.Editor = tab.Editor
	}
}

func (m *App) openReferenceOverlay(refID string) {
	if m.RefStore() == nil {
		return
	}
	ref, ok := m.RefStore().GetReference(refID)
	if !ok || !ref.Active() {
		return
	}
	title := ref.DisplayName()
	if title == "" {
		title = ref.ID
	}
	m.ActiveSession.TextOverlay.Open(refID, title, ref.Content)
	m.setMode(ModeTextOverlay)
}

func (m *App) saveReferenceFromOverlay() {
	refID := m.ActiveSession.TextOverlay.RefID()
	content := m.ActiveSession.TextOverlay.Value()
	m.ActiveSession.TextOverlay.Close()
	if m.RefStore() != nil && refID != "" {
		_ = m.RefStore().UpdateReferenceContent(refID, content)
	}
	m.setMode(ModeChat)
}

func (m *App) openFilePicker() {
	root, _ := m.workingDir()
	m.FilePicker.Open(root)
	m.setMode(ModeFilePicker)
}

func (m *App) addFileReference(path string) {
	if m.RefStore() == nil || path == "" {
		return
	}
	mime, isImage := provider.ImageMimeFromExt(filepath.Ext(path))
	kind := sessiondb.KindSelectedFile
	if isImage {
		kind = sessiondb.KindPastedImage
	} else {
		mime = "text/plain"
	}
	ref, err := m.RefStore().CreateReference(sessiondb.CreateRequest{
		Kind:     kind,
		Title:    filepath.Base(path),
		Mime:     mime,
		FilePath: path,
		Inline:   false,
	})
	if err != nil {
		m.StatusBar.SetError("Failed to add file reference")
		return
	}
	m.Input.InsertReference(ref)
}

func (m *App) workingDir() (string, error) {
	return os.Getwd()
}

func tickCmd() tea.Cmd {
	return TickCmd()
}

func streamingTickCmd() tea.Cmd {
	return StreamingTickCmd()
}
