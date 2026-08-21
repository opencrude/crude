package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/composer"
	"crude/tui/screens/confirmation"
	filepickerscreen "crude/tui/screens/filepicker"
	findinfiles "crude/tui/screens/findinfiles"
	helpscreen "crude/tui/screens/help"
	"crude/tui/screens/launcher"
	memorybrowser "crude/tui/screens/memorybrowser"
	"crude/tui/screens/question"
	sessionpicker "crude/tui/screens/sessionpicker"
	"crude/tui/screens/sessionsettings"
	"crude/tui/screens/shellchat"
	textoverlay "crude/tui/screens/textoverlay"
	"crude/tui/screens/yolo"
	settingsui "crude/tui/settings"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
	"crude/workspace"
)

func (m *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Dirty is set selectively per message type, NOT unconditionally.
	// The full render pipeline (editor cell paint, chat rows, compositor,
	// ANSI generation) runs after every Update. The editor's chroma work
	// is internally cached by line, so unconditional repaint stays cheap
	// (BenchmarkEditorPaint_*). Mouse motion at 120-200/sec is the hot
	// path — the editor repaint (~180-250µs at 120x40) plus compositor
	// (~100µs) stays well inside the 16ms frame budget.

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Logger.Debug("UI", "resize", "width", msg.Width, "height", msg.Height)
		m.width = msg.Width
		m.height = msg.Height
		m.subPaneProjections.SetSize(msg.Width, msg.Height)
		m.Input.SetWidth(msg.Width)
		m.StatusBar.SetWidth(msg.Width)
		if m.compositor != nil {
			m.compositor.Resize(msg.Width, msg.Height)
		}
		// The shell surface is resized on the next View() frame by
		// renderShellToSurface (single full-screen surface).
		return m, nil

	case ttypes.TickMsg:
		return m.handleTick(msg)

	case tea.KeyPressMsg:
		return m.dispatchKeyPress(msg)

	case tea.PasteMsg:
		return m.dispatchPasteMsg(msg)

	case tea.MouseClickMsg:
		m.Logger.Debug("UI", "mouse", "click", "x", msg.Mouse().X, "y", msg.Mouse().Y, "button", fmt.Sprintf("%d", msg.Button))
		return m.dispatchMouseClick(msg)

	case tea.MouseWheelMsg:
		m.Logger.Debug("UI", "mouse", "wheel", "x", msg.X, "y", msg.Y, "button", fmt.Sprintf("%d", msg.Button))
		return m.dispatchMouseWheel(msg)

	case tea.MouseMotionMsg:
		return m.dispatchMouseMotion(msg)

	case tea.MouseReleaseMsg:
		// Same as MouseMotionMsg: let the release handler set
		// dirty when it actually does something visible.
		m.Logger.Debug("UI", "mouse", "release", "x", msg.Mouse().X, "y", msg.Mouse().Y)
		return m.dispatchMouseRelease(msg)

	case ttypes.SelectionTickMsg:
		if m.chatScreen != nil && m.rootHost != nil {
			m.rootHost.Focus(shellchat.ScreenID)
			res := m.rootHost.Handle(msg)
			if res.Cmd != nil {
				cmds = append(cmds, res.Cmd)
			}
		}

	default:
		if m.isEditorMsg(msg) {
			// Blink ticks are repaint-free when cursor visibility does not
			// change: the editor's chroma cache is untouched and the
			// compositor diffs identical cells into no ANSI output.
			cmds = append(cmds, m.dispatchEditorTick(msg)...)
		}
	}

	cmds = append(cmds, m.handleAppMessages(msg)...)

	return m, tea.Batch(cmds...)
}

func (m *App) handleTick(msg ttypes.TickMsg) (tea.Model, tea.Cmd) {
	var diskSyncCmd tea.Cmd
	now := time.Now()
	spinFrame := int(now.UnixMilli())
	m.SpinFrame = spinFrame
	m.StatusBar.SetSpinFrame(spinFrame)
	m.StatusBar.SetCurrentAgent(m.AgentMgr.ActiveID())
	m.Input.SetSpinFrame(spinFrame)
	m.StatusBar.ClearExpired()
	m.refreshSessionBackgroundJobs(now)
	m.tickTerminalCursors(now)
	var gitCmd tea.Cmd
	if m.editorGitStale.Load() && now.Sub(m.editorGitAt) >= 400*time.Millisecond {
		m.editorGitStale.Store(false)
		gitCmd = m.refreshEditorGitCmd()
	}
	// Advance popup reveal animations (slide-in on open). Returns true while
	// any reveal is mid-flight so the frame keeps repainting until done.
	if m.advanceReveals(time.Now()) {
		if fw := m.windowManager.Focused(); fw != nil {
			fw.MarkContentDirty()
		}
	}
	// Sessions browser: spin the working-session icon and refresh runtime
	// details while any listed session runs a turn. Stopped sessions keep
	// static icons and never trigger repaints.
	if m.sessionPicker != nil && m.sessionPicker.NeedsAnimation() {
		m.sessionPicker.SetSpinFrame(spinFrame)
		m.sessionPicker.SetLiveInfo(m.sessionLiveInfos(m.sessionPicker.Sessions()))
		if m.overlayHost != nil {
			if fw := m.overlayHost.Window(ui.OverlaySessionPicker); fw != nil {
				fw.MarkContentDirty()
			}
		}
	}
	for _, n := range m.AgentMgr.AllNodes() {
		if n.State != nil {
			n.State.Tick(spinFrame)
			m.syncAgentMessages(n.ID, false)
		}
	}
	m.subPaneProjections.TickAll(spinFrame)
	m.syncAgentWindowTabs()
	for _, sp := range m.PaneMgr.Panes() {
		if !sp.Done && m.subPaneProjections.Has(sp.ID) {
			if ml := m.activeMsgList(); ml != nil {
				ml.InvalidateSubPane(sp.ID)
			}
		}
	}
	if m.EditorState.Editor.IsOpen() && m.EditorState.Editor.DiskChanged() {
		filePath := m.EditorState.Editor.FilePath()
		var reloadedText string
		updateOpenFile := false
		// Only auto-reload when the editor has no unsaved (dirty) changes;
		// otherwise the user's in-progress edits would be lost.
		if !m.EditorState.Editor.Modified() {
			if err := m.EditorState.Editor.Reload(); err != nil {
				m.Logger.Error("EDITOR", "reload_error", "error", err.Error())
			} else {
				reloadedText = m.EditorState.Editor.Value()
				updateOpenFile = true
				m.editorGitStale.Store(true)
				m.editorGitAt = time.Time{}
			}
		}
		// LSP writes can wait on a subprocess pipe. Keep them off Bubble Tea's
		// update goroutine so an external edit cannot stall input or rendering.
		if m.LSPMgr != nil {
			lspMgr := m.LSPMgr
			diskSyncCmd = func() tea.Msg {
				lspCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				lspMgr.NotifyWatchedFilesChanged(lspCtx, filePath, 2) // 2 = Changed
				if updateOpenFile {
					lspMgr.UpdateFile(lspCtx, filePath, reloadedText)
				}
				return nil
			}
		}
	}

	connected, status, errMsg := m.LSPMgr.Status()
	statusbarLSP := connected || status == editor.LSPConnected || status == editor.LSPStarting || errMsg != ""

	// Status bar is EXCLUSIVELY for whole-project insights plus contextual
	// help. Per-file diagnostics paint in the editor gutter/hover; counts and
	// shortcuts for the active file appear in shellstatus editor context.
	if statusbarLSP {
		ins := m.LSPMgr.ProjectInsights()
		m.StatusBar.SetProjectInsights(ins)
	} else {
		m.StatusBar.SetProjectInsights(editor.ProjectInsights{})
	}

	// Status bar animations and popup reveals need their own content
	// re-rendered even when main content hasn't changed.
	if m.wantsFastTick() {
		return m, tea.Batch(diskSyncCmd, gitCmd, streamingTickCmd())
	}
	return m, tea.Batch(diskSyncCmd, gitCmd, tickCmd())
}

// wantsFastTick reports whether the 16 ms streaming tick is justified right
// now: an agent/sub-pane is actively streaming, or a popup reveal animation
// is mid-flight. An idle launcher does NOT qualify — it has no 60 Hz
// animation, and a permanent 16 ms tick there repaints full 160x40 frames
// (roughly a quarter CPU on an idle floating terminal).
func (m *App) wantsFastTick() bool {
	// Streaming agents and unfinished sub-panes repaint at frame rate.
	if m.anyActive() {
		return true
	}
	// Popup reveal animations need the fast tick to render the slide-in.
	return len(m.reveals) > 0
}

func (m *App) handleAppMessages(msg tea.Msg) []tea.Cmd {
	var cmds []tea.Cmd

	if msg, ok := msg.(ttypes.AgentMsg); ok {
		cmds = append(cmds, m.handleAgentEvent(ttypes.AgentEventMsg(msg)))
	}

	switch msg := msg.(type) {
	case sourceControlResultMsg:
		m.applySourceControlResult(msg)
	case editorGitAnnotationMsg:
		m.applyEditorGitAnnotation(msg)
	case clipboardAsyncRequestMsg:
		if cmd := m.clipboardAsyncCommand(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case clipboardAsyncResultMsg:
		if cmd := m.applyClipboardAsyncResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case clipboardAsyncImageStoredMsg:
		m.applyClipboardAsyncImageStored(msg)
	case directoryAsyncResultMsg:
		m.applyDirectoryAsyncResult(msg)
	case ui.SearchDebouncedMsg:
		if m.findInFiles != nil {
			if cmd := m.findInFiles.HandleSearchDebounced(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if m.launcherScreen != nil {
			if cmd := m.launcherScreen.HandleSearchDebounced(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case launcher.ActivateMsg:
		if m.launcherScreen == msg.Screen {
			cmds = append(cmds, m.handleLauncherActivate(msg))
		}
	case launcher.DeleteMsg:
		if m.launcherScreen == msg.Screen {
			cmds = append(cmds, m.handleLauncherDelete(msg))
		}
	case launcherWorkspacesMsg:
		// Async workspace listing. Apply only the newest kick, and only if
		// the launcher is still mounted (a session may have opened in the
		// meantime). Applying keeps the user's filter/selection: SetData
		// rebuilds items without resetting filter state.
		if msg.Gen != m.launcherLoadGen.Load() || m.launcherScreen == nil {
			break
		}
		m.launcherScreen.SetData(msg.Cwd, msg.Infos)
		m.launcherScreen.SyncFocus()
	case workspaceTreeReadyMsg:
		m.applyWorkspaceTreeReady(msg)
	case workspace.LSPDiagnosticsMsg:
		m.handleLSPDiagnosticsUpdate(msg)
	case workspace.FileWatcherMsg:
		if cmd := m.handleFileWatcherEvent(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case findinfiles.SearchRequestMsg:
		if m.findInFiles != nil && m.findInFiles.AcceptsSearch(msg.Screen, msg.Generation) {
			m.findInFilesRequest = m.StartProjectSearch(msg.Query, msg.Options)
			m.findInFilesGen = msg.Generation
			m.markProjectSearchDirty()
		}
	case findinfiles.SearchCancelMsg:
		if m.findInFiles == msg.Screen {
			m.CancelProjectSearch()
			m.findInFilesRequest = 0
			m.findInFilesGen = 0
		}
	case workspace.ProjectSearchCompletedMsg:
		if m.findInFiles != nil && msg.Result.RequestID == m.findInFilesRequest {
			m.findInFiles.ApplySearchResults(
				m.findInFilesGen,
				msg.Result.Matches,
				msg.Result.Err,
				msg.Result.Duration,
			)
			m.markProjectSearchDirty()
			m.findInFilesRequest = 0
		}
	case workspace.TerminalOutputMsg:
		if cmd := m.handleTerminalOutput(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case workspace.TerminalExitedMsg:
		m.handleTerminalExited(msg)
	case workspace.TerminalTitleMsg:
		m.applyTerminalTitle(msg.ID, msg.Title)
	case findinfiles.OpenMsg:
		if m.findInFiles != msg.Screen {
			break
		}
		if cmd := m.openFindInFilesResult(msg.Match); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case findinfiles.CloseMsg:
		if m.findInFiles == msg.Screen {
			m.closeFindInFiles()
		}
	case findinfiles.ReplaceMsg:
		if cmd := m.applyProjectReplace(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case sessionpicker.SelectMsg:
		if m.sessionPicker != nil && msg.Target == m.sessionPicker {
			if cmd := m.handleSessionPickerSelect(msg.Session); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case sessionpicker.CancelMsg:
		if m.sessionPicker != nil && msg.Target == m.sessionPicker {
			m.closeSessionPicker()
		}
	case memorybrowser.PinMsg:
		m.handleMemoryBrowserPin(msg)
	case memorybrowser.ForgetMsg:
		m.handleMemoryBrowserForget(msg)
	case memorybrowser.CancelMsg:
		if m.memoryBrowser != nil && msg.Target == m.memoryBrowser {
			m.closeMemoryBrowser()
		}
	case helpscreen.CancelMsg:
		if m.helpScreen != nil && msg.Target == m.helpScreen {
			m.closeHelp()
		}
	case ttypes.ConfigReloadedMsg:
		if msg.ThemePreset != "" {
			m.applyThemePreset(msg.ThemePreset)
		}
	case composer.SubmitMsg:
		if composerScreen := m.sessionComposer(); composerScreen == nil || msg.Target != composerScreen {
			break
		}
		m.overlayHost.Close(ui.OverlayComposer)
		if msg.Text != "" && m.AgentMgr.ActiveID() != "" {
			cmds = append(cmds, m.submitInput(m.AgentMgr.ActiveID(), msg.Text, nil))
		}
	case composer.CancelMsg:
		if composerScreen := m.sessionComposer(); composerScreen != nil && msg.Target == composerScreen {
			m.overlayHost.Close(ui.OverlayComposer)
		}
	case textoverlay.SaveMsg:
		if overlay := m.sessionTextOverlay(); overlay != nil && msg.Target == overlay {
			m.saveReferenceFromOverlay()
		}
	case textoverlay.CancelMsg:
		if overlay := m.sessionTextOverlay(); overlay != nil && msg.Target == overlay {
			m.setMode(ModeChat)
		}
	case lspDefinitionResultMsg:
		if m.acceptsLSPGeneration(msg.generation) {
			m.handleDefinitionResult(msg)
		}
	case lspReferencesResultMsg:
		if m.acceptsLSPGeneration(msg.generation) {
			m.handleReferencesResult(msg)
		}
	case lspDocumentSymbolsResultMsg:
		if m.acceptsLSPGeneration(msg.generation) {
			m.handleDocumentSymbolsResult(msg)
		}
	case lspRenameResultMsg:
		if m.acceptsLSPGeneration(msg.generation) {
			m.handleRenameResult(msg)
		}
	case lspCompletionResultMsg:
		if m.acceptsLSPGeneration(msg.generation) {
			m.handleCompletionResult(msg)
		}
	case ttypes.ErrorMsg:
		m.Logger.Debug("UI", "error", "msg", string(msg))
		m.StatusBar.SetError(string(msg))
	case ttypes.UpdateModelMsg:
		m.ModelName = string(msg)
		m.Logger.Debug("UI", "model_update", "model", string(msg))
		for _, node := range m.AgentMgr.AllNodes() {
			if m.subPaneProjections.Has(node.ID) {
				m.subPaneProjections.Get(node.ID, workspace.PaneAgent).renderer.SetModelName(string(msg))
			}
		}
		if ml := m.activeMsgList(); ml != nil {
			ml.SetModelName(string(msg))
		}
	case ttypes.OpenSettingsMsg:
		m.Logger.Info("UI", "open_session_settings")
		m.openSessionSettings()
	case ttypes.SettingsSaveResultMsg:
		if msg.Err != nil {
			m.Logger.Error("CONFIG", "save_error", "error", msg.Err.Error())
			m.StatusBar.SetError(msg.Err.Error())
		} else {
			m.Logger.Info("CONFIG", "saved")
			m.ModelName = msg.Settings.Model
			m.LastSettings = msg.Settings
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
			m.appendActiveMessage(ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: msg.Desc,
				Time:    time.Now(),
			})
		}
	case ttypes.HistoryMessagesMsg:
		if m.AgentMgr.ActiveID() != "" {
			for _, hm := range msg {
				m.appendActiveMessage(hm)
			}
		}
	case filepickerscreen.NavigateMsg:
		if !msg.ValidFor(m.FilePicker) {
			break
		}
		if cmd := m.beginDirectoryAsyncLoadFor(msg.Target, msg.Path, msg.Intent()); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case filepickerscreen.SelectMsg:
		if !msg.ValidFor(m.FilePicker) {
			break
		}
		m.invalidateDirectoryAsyncLoad(msg.Target)
		m.overlayHost.Close(ui.OverlayFilePicker)
		if msg.Folder {
			if m.launcherScreen != nil {
				_ = m.launcherOpenFolder(msg.Path)
			} else if err := m.SwitchProject(msg.Path); err != nil {
				if m.StatusBar != nil {
					m.StatusBar.SetError("Failed to open workspace: " + err.Error())
				}
			} else if m.StatusBar != nil {
				m.StatusBar.SetInfo("Opened " + msg.Path)
			}
		} else {
			m.addFileReference(msg.Path)
		}
	case filepickerscreen.CancelMsg:
		if msg.ValidFor(m.FilePicker) {
			m.invalidateDirectoryAsyncLoad(msg.Target)
			m.overlayHost.Close(ui.OverlayFilePicker)
		}
	case settingsui.SaveMsg:
		m.Logger.Info("CONFIG", "settings_save", "scope", string(msg.Scope))
		appSets := msg.AppSettings
		if msg.Scope == ttypes.SettingsScopeWorkspace {
			if err := m.SaveWorkspaceSettings(msg.Overrides); err != nil {
				m.StatusBar.SetError("Failed to save workspace settings: " + err.Error())
			} else {
				m.applyThemePreset(themePresetFromSettings(m.LoadAppSettings()))
				m.refreshAllSessionSettingsProjections()
				m.StatusBar.SetInfo("Workspace settings saved")
			}
		} else {
			if m.Logger != nil {
				m.Logger.Info("WORKSPACE", "save_app_settings", "theme_preset", appSets.Theme.Preset, "provider", appSets.ActiveProvider)
			}
			if err := m.SaveAppSettings(appSets); err != nil {
				m.StatusBar.SetError("Failed to save application settings: " + err.Error())
			} else if err := m.SaveProviderSettings(appSets.ActiveProvider, appSets.Providers); err != nil {
				m.StatusBar.SetError("Failed to save providers: " + err.Error())
			} else {
				m.applyThemePreset(themePresetFromSettings(appSets))
				m.refreshAllSessionSettingsProjections()
				m.StatusBar.SetInfo("Application settings saved")
			}
		}
		m.overlayHost.Close(ui.OverlayWorkspaceSettings)
	case settingsui.ScopeMsg:
		m.openSettings(msg.Scope)
	case settingsui.ResetSectionMsg:
		overrides := m.LoadWorkspaceOverrides()
		overrides.ClearCategory(msg.Key)
		if err := m.SaveWorkspaceSettings(overrides); err != nil {
			m.StatusBar.SetError("Failed to reset section: " + err.Error())
		} else {
			m.applyThemePreset(themePresetFromSettings(m.LoadAppSettings()))
			m.refreshAllSessionSettingsProjections()
			m.openSettings(ttypes.SettingsScopeWorkspace)
		}
	case settingsui.CancelMsg:
		// Live preset cycling previews via ThemePreviewMsg without persisting.
		// Cancel must restore the last saved palette across the whole UI.
		m.applyThemePreset(themePresetFromSettings(m.LoadAppSettings()))
		m.overlayHost.Close(ui.OverlayWorkspaceSettings)
	case settingsui.ThemePreviewMsg:
		if msg.Preset != "" {
			m.applyThemePreset(msg.Preset)
		}
	case settingsui.FetchModelsMsg:
		request := msg
		cmds = append(cmds, func() tea.Msg {
			models, err := m.HandleFetchModels(
				request.ProviderID, request.BaseURL, request.APIKey, request.StandardID,
			)
			if err != nil {
				return settingsui.ModelsErrorMsg{ProviderID: request.ProviderID, Error: err.Error()}
			}
			return settingsui.ModelsMsg{ProviderID: request.ProviderID, Models: models}
		})
	case settingsui.ModelsMsg, settingsui.ModelsErrorMsg:
		if fw := m.windowManager.Window("workspace-settings"); fw != nil {
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case settingsui.ChatGPTSignInResultMsg:
		if msg.Err == nil && msg.Creds != nil {
			if err := m.saveChatGPTOAuth(msg.Creds); err != nil {
				m.StatusBar.SetError("Failed to save ChatGPT sign-in: " + err.Error())
			} else {
				m.refreshAllSessionSettingsProjections()
				label := msg.Creds.Email
				if label == "" {
					label = "ChatGPT"
				}
				m.StatusBar.SetInfo("Signed in as " + label)
			}
		} else {
			reason := "Sign in with ChatGPT failed"
			if msg.Err != nil {
				reason = msg.Err.Error()
			}
			// A fatal error (revoked consent, bad refresh token) means the
			// stored session is dead — clear it so we don't keep retrying.
			var signInErr *provider.ChatGPTSignInError
			if msg.Err != nil && errors.As(msg.Err, &signInErr) && signInErr.Fatal {
				if err := m.saveChatGPTOAuth(nil); err == nil {
					m.refreshAllSessionSettingsProjections()
				}
			}
			m.StatusBar.SetError(reason)
		}
		// Refresh the open form so the status row + sign-in/sign-out button swap.
		if fw := m.windowManager.Window("workspace-settings"); fw != nil {
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case settingsui.ChatGPTSignOutMsg:
		if err := m.saveChatGPTOAuth(nil); err != nil {
			m.StatusBar.SetError("Failed to clear ChatGPT sign-in: " + err.Error())
		} else {
			m.refreshAllSessionSettingsProjections()
			m.StatusBar.SetInfo("Signed out of ChatGPT")
		}
		if fw := m.windowManager.Window("workspace-settings"); fw != nil {
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case sessionsettings.SaveMsg:
		m.Logger.Info("CONFIG", "session_settings_save")
		m.StatusBar.SetInfo("saving...")
		s := msg.Settings
		session, _ := m.sessionProjections.projectionForSettings(msg.Target)
		if session == nil {
			m.StatusBar.SetError("Session settings owner is no longer available")
			break
		}
		sessionID := session.SessionID
		target := msg.Target
		cmds = append(cmds, func() tea.Msg {
			err := m.HandleSettingsSaveForSession(sessionID, s)
			return sessionSettingsSaveResultMsg{target: target, settings: s, err: err}
		})
	case sessionsettings.SaveAsWorkspaceMsg:
		// Promote the current agent settings to workspace defaults so every
		// future agent (and new session) inherits them.
		m.Logger.Info("CONFIG", "session_settings_save_as_workspace")
		appDefaults := m.AppDefaults()
		working := appDefaults
		working.Agents = msg.Settings.Agents
		overrides := types.WorkspaceOverridesFromDiff(appDefaults, working)
		if err := m.SaveWorkspaceSettings(overrides); err != nil {
			m.StatusBar.SetError("Failed to save as workspace settings: " + err.Error())
		} else {
			m.refreshAllSessionSettingsProjections()
			m.StatusBar.SetInfo("Agent settings saved as workspace defaults")
		}
	case sessionSettingsSaveResultMsg:
		_, projection := m.sessionProjections.projectionForSettings(msg.target)
		if msg.err != nil {
			m.Logger.Error("CONFIG", "save_error", "error", msg.err.Error())
			m.StatusBar.SetError(msg.err.Error())
		} else if projection != nil {
			if msg.target == m.sessionSettings() {
				m.ModelName = msg.settings.Model
				m.LastSettings = msg.settings
			}
			m.closeProjectionPanel(projection, sessionSettingsPanelID)
			m.StatusBar.SetInfo("settings saved")
		}
	case sessionsettings.CancelMsg:
		m.Logger.Info("CONFIG", "session_settings_cancel")
		_, projection := m.sessionProjections.projectionForSettings(msg.Target)
		if msg.Target != nil {
			msg.Target.RestoreSnapshot()
		}
		m.closeProjectionPanel(projection, sessionSettingsPanelID)
	case sessionsettings.ResetMsg:
		session, projection := m.sessionProjections.projectionForSettings(msg.Target)
		if session == nil {
			m.StatusBar.SetError("Session settings owner is no longer available")
		} else if err := m.ResetSessionSettingsForSession(session.SessionID); err != nil {
			m.StatusBar.SetError("Failed to reset session settings: " + err.Error())
		} else {
			m.refreshSessionSettingsProjection(session)
			m.StatusBar.SetInfo("Session settings now inherit workspace defaults")
		}
		m.closeProjectionPanel(projection, sessionSettingsPanelID)
	case sessionsettings.FetchModelsMsg:
		request := msg
		target := m.sessionSettings()
		cmds = append(cmds, func() tea.Msg {
			models, err := m.HandleFetchModels(
				request.ProviderID, request.BaseURL, request.APIKey, request.StandardID,
			)
			return sessionSettingsModelsResultMsg{target: target, providerID: request.ProviderID, models: models, err: err}
		})
	case sessionSettingsModelsResultMsg:
		_, projection := m.sessionProjections.projectionForSettings(msg.target)
		if projection != nil {
			if msg.err != nil {
				msg.target.ApplyModelsError(msg.providerID, msg.err.Error())
			} else {
				msg.target.ApplyModels(msg.providerID, msg.models)
			}
			projection.deckHost.MarkLayout()
		}
	case question.ResultMsg:
		m.resolveSessionQuestion(msg.Target, msg.Result, false)
	case question.CancelMsg:
		m.resolveSessionQuestion(msg.Target, question.Result{}, true)
	case confirmation.ResultMsg:
		m.resolveSessionConfirmation(msg.Target, msg.Choice)
	case yolo.ResultMsg:
		m.resolveSessionYolo(msg.Target, msg.Enabled)
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
		// Revealing the sidebar must not steal launcher focus — Enter on the
		// welcome screen remains New Session. Use Ctrl+1 (view.focus.sidebar)
		// for an explicit tree-focus move. When an editor is already open and
		// nothing else owns the stack, focusing the tree is a reasonable default.
		if !m.launcherHostVisible() && m.EditorState.ActiveView != "" && m.FocusStack.Top() == FocusNone {
			m.FocusStack.Clear()
			m.FocusStack.Push(FocusTree)
		}
		m.Tree.Model.Update(filetree.TreeWidthMsg{Width: m.Tree.Width})
	} else {
		if m.FocusStack.Top() == FocusTree {
			m.FocusStack.Pop()
		}
	}
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

// saveChatGPTOAuth persists ChatGPT OAuth credentials at application scope.
// A nil creds clears the tokens (sign-out / fatal sign-in failure). OAuth is
// always stored at app scope — workspace overlays strip credentials — so this
// loads the full app settings, mutates only the chatgpt entry, and saves both
// the file and the live session config so the running agent picks it up.
func (m *App) saveChatGPTOAuth(creds *provider.OAuthCredentials) error {
	s := m.LoadAppSettings()
	if s.Providers == nil {
		s.Providers = make(map[string]ttypes.ProviderConfig)
	}
	cfg := s.Providers["chatgpt"]
	cfg.OAuth = creds
	s.Providers["chatgpt"] = cfg
	if err := m.SaveAppSettings(s); err != nil {
		return err
	}
	return m.SaveProviderSettings(s.ActiveProvider, s.Providers)
}

func (m *App) forwardEventToWindowContent(fw *wm.FloatingWindow, msg tea.Msg) tea.Cmd {
	if fw == nil || fw.Content == nil {
		return nil
	}
	localMsg := m.translateToLocalMouse(msg, fw)
	if w, ok := fw.Content.(*ui.WindowFrameWidget); ok {
		if ui.IsTerminalOverlay(ui.OverlayID(fw.ID)) && w.Host() != nil {
			if body := w.Body(); body != nil {
				w.Host().Focus(body.ID())
			}
		}
		result := w.Dispatch(localMsg)
		fw.MarkContentDirty()
		if result.Action != nil && result.Action.ID == "window.close" {
			m.closePopupWindowByID(fw.ID)
			return result.Cmd
		}
		if result.Action != nil && result.Action.ID == "window.titleaction" {
			// Terminal windows use the title action as their Dock button.
			if id, ok := ui.TerminalIDFromOverlay(ui.OverlayID(fw.ID)); ok {
				m.dockTerminal(id)
				return result.Cmd
			}
		}
		cmd := m.handleHostAction(result.Action)
		return tea.Batch(result.Cmd, cmd)
	}
	if handler, ok := fw.Content.(interface{ HandleEvent(tea.Msg) tea.Cmd }); ok {
		cmd := handler.HandleEvent(localMsg)
		fw.MarkContentDirty()
		return cmd
	}
	return nil
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
		// Closing an agent tab minimizes it: the orchestrator keeps
		// running and the conversation survives. Terminating is an
		// explicit action from the tab or Agents-panel context menu.
		m.minimizeAgent(agentID)
		m.handleTabSwitch()
		if m.AgentMgr.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
			m.openLauncher()
			m.LastInteractionTime = time.Now()
			return nil
		}
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
	if m.AgentMgr.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
		m.openLauncher()
	}
	return nil
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
		// Closing the last tab returns to launcher.
		return
	}

	if m.isAgentTabTitle(activeTab.Title) {
		agentID := m.agentIDFromTabTitle(activeTab.Title)
		m.AgentMgr.SetActive(agentID)
		if m.subPaneProjections.Has(agentID) {
			m.subPaneProjections.InvalidateAllMessages(agentID)
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
		m.editorGitStale.Store(true)
		m.editorGitAt = time.Time{}
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
	if overlay := m.sessionTextOverlay(); overlay != nil {
		overlay.Open(refID, title, ref.Content)
	}
	m.setMode(ModeTextOverlay)
}

func (m *App) saveReferenceFromOverlay() {
	overlay := m.sessionTextOverlay()
	if overlay == nil {
		return
	}
	refID := overlay.RefID()
	content := overlay.Value()
	overlay.Close()
	if m.RefStore() != nil && refID != "" {
		_ = m.RefStore().UpdateReferenceContent(refID, content)
	}
	m.setMode(ModeChat)
}

func (m *App) openFilePicker() {
	root, _ := m.workingDir()
	m.openPicker(root, filepickerscreen.ModeFile)
}

func (m *App) openPicker(root string, mode filepickerscreen.Mode) {
	if m.FilePicker == nil {
		m.FilePicker = filepickerscreen.New(m.Theme)
	}
	m.invalidateDirectoryAsyncLoad(m.FilePicker)
	m.overlayHost.Close(ui.OverlayFilePicker)
	m.FilePicker.Open(root, mode, nil)
	cmd := m.beginDirectoryAsyncLoad(m.FilePicker, root)
	m.setMode(ModeFilePicker)
	m.runDetachedAsyncCmd(cmd)
}

func pickerEntries(entries []workspace.DirectoryEntry) []filepickerscreen.Entry {
	result := make([]filepickerscreen.Entry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, filepickerscreen.Entry{Name: entry.Name, Path: entry.Path, IsDir: entry.IsDir})
	}
	return result
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
	cmd := workspace.TickCmd()
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if tick, ok := msg.(workspace.TickMsg); ok {
			return ttypes.TickMsg(tick)
		}
		return msg
	}
}

func streamingTickCmd() tea.Cmd {
	cmd := workspace.StreamingTickCmd()
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if tick, ok := msg.(workspace.TickMsg); ok {
			return ttypes.TickMsg(tick)
		}
		return msg
	}
}
