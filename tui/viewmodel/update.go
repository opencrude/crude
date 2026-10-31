package viewmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/perf"
	"crude/tui/rendering"
	"crude/tui/screens/composer"
	"crude/tui/screens/confirmation"
	"crude/tui/screens/connectionpopover"
	"crude/tui/screens/decision"
	"crude/tui/screens/diffview"
	"crude/tui/screens/editorchanges"
	filepickerscreen "crude/tui/screens/filepicker"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/tui/screens/launcher"
	historyscreen "crude/tui/screens/localhistory"
	memorybrowser "crude/tui/screens/memorybrowser"
	"crude/tui/screens/operationissue"
	"crude/tui/screens/question"
	sessionpicker "crude/tui/screens/sessionpicker"
	"crude/tui/screens/sessionsettings"
	"crude/tui/screens/shellfooter"
	"crude/tui/screens/shellheader"
	statusoverview "crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	textoverlay "crude/tui/screens/textoverlay"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/tui/wm"
	"crude/types"
	"crude/workspace"
)

func (m *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, blurred := msg.(tea.BlurMsg); blurred {
		m.closeEditorSnippet()
	}
	m.syncEditorSnippet()
	defer m.invalidateMessageRegions(msg)
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseClickMsg, tea.BlurMsg:
		m.setWheelHost(nil)
	}
	m.syncMemoryBrowserLifecycle()
	m.captureToolView()
	previousView := m.toolLayout.Views[m.activityView.ID()]
	previousEditor := m.logicalEditorFocus()
	previousMode := m.editorSettings.AutoSave
	model, command := m.update(msg)
	command = tea.Batch(command, m.drainRemoteUICommands())
	command = tea.Batch(command, m.drainTerminalCommands())
	command = tea.Batch(command, m.continueRemoteWorkspaceSwitch())
	command = tea.Batch(command, m.drainLiveEditorCommands())
	command = tea.Batch(command, m.drainEditorSyntaxCommands())
	command = tea.Batch(command, m.drainComparisonGeometry())
	command = tea.Batch(command, m.drainComparisonSearch())
	command = tea.Batch(command, m.drainCoreRestartStartup())
	m.syncLocalDiffFreshness()
	m.syncSignatureHelp()
	m.syncEditorSnippet()
	m.syncMemoryBrowserLifecycle()
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseClickMsg, tea.MouseWheelMsg, tea.MouseReleaseMsg:
		m.captureToolView()
		if !reflect.DeepEqual(previousView, m.toolLayout.Views[m.activityView.ID()]) {
			m.toolLayoutVersion++
		}
	}
	if m.toolLayoutVersion > m.toolLayoutSavedVersion && !m.toolLayoutSaving {
		command = tea.Batch(command, m.queueToolLayoutSave())
	}
	m.updateSettingsWindowTitle()
	m.showNextOperationIssue()
	if previousMode == types.AutoSaveEditorFocusChange && m.editorSettings.AutoSave == previousMode && previousEditor != nil &&
		previousEditor.IsOpen() && previousEditor != m.logicalEditorFocus() && m.DirtyPromptPath == "" {
		command = tea.Batch(command, m.requestEditorSave(previousEditor, workspace.DocumentSaveFocusOut, false))
	}
	command = tea.Batch(command, m.finishCoreRestartUpdate())
	return model, command
}

func (m *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd, handled := m.handleTerminalImageMessage(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.handleRemoteInstallInput(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.HandleRemoteMessage(msg); handled {
		return m, cmd
	}
	onboardingHandled, onboardingCmd := m.handleOnboardingMessage(msg)
	if onboardingHandled {
		return m, onboardingCmd
	}
	if handled, cmd := m.handleWorkspaceOpenPromptInput(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.handleEditorRecoveryBrowserMessage(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.handleWorkspaceSwitchInput(msg); handled {
		return m, cmd
	}
	var cmds []tea.Cmd
	if onboardingCmd != nil {
		cmds = append(cmds, onboardingCmd)
	}
	coalescePresentation := false
	switch msg.(type) {
	case tea.MouseMotionMsg, interactiveFrameMsg, types.TickMsg:
	case types.AgentMsg, agent.AgentEvent, workspace.TerminalChangedMsg, terminalProjectionMsg, shellport.TerminalFrameMsg:
		coalescePresentation = true
	default:
		m.presentImmediateFrame()
	}

	// Any live input makes the following window expected work in the perf
	// sentinel: repaints during interaction are the product working, not a
	// degenerate loop.
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseWheelMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg:
		perf.NoteActivity()
	}

	// Mutations are processed for every message. High-rate pointer/stream/PTY
	// presentation is coalesced by frameGate. Host generations independently
	// retain source damage until a frame actually paints it.

	switch msg := msg.(type) {
	case editorSnippetChoiceMsg:
		m.handleSnippetChoice(msg)
		return m, nil
	case editor.SyntaxReadyMsg:
		m.applyEditorSyntax(msg)
		return m, nil
	case diffview.SearchReadyMsg:
		m.applyComparisonSearch(msg)
		return m, nil
	case comparisonGeometryMsg:
		m.applyComparisonGeometry(msg)
		return m, nil
	case shellport.ControlRequestMsg:
		return m, m.handleShellControl(msg)
	case coreRestartResultMsg:
		return m, m.applyCoreRestartResult(msg)
	case coreRestartPollMsg:
		return m, m.pollCoreRestart(msg)
	case shellControlDoneMsg:
		return m, m.finishShellControl(msg)
	case diagnosticControlDoneMsg:
		return m, m.finishDiagnosticControl(msg)
	case shellControlReplyMsg:
		if msg.err != nil {
			m.ws.Logger().Warn("CONTROL", "reply_failed", "error", msg.err.Error())
		}
		return m, nil
	case clientTreeDirectoryMsg:
		m.applyClientTreeDirectory(msg)
		return m, nil
	case shellport.ClientFilesChangedMsg:
		if msg.Owner == m.ws {
			return m, m.applyCoreFileChanges(msg.Changes)
		}
		return m, nil
	case clientProjectReplaceMsg:
		return m, m.applyClientProjectReplace(msg)
	case projectReplaceActionMsg:
		return m, m.handleProjectReplaceAction(msg)
	case projectReplaceAppliedMsg:
		return m, m.handleProjectReplaceApplied(msg)
	case liveEditorResult:
		m.applyLiveEditorResult(msg)
		return m, nil
	case liveConflictMsg:
		return m, m.applyLiveConflict(msg)
	case gitMessageEditorMsg:
		return m, m.applyGitMessageEditor(msg)
	case gitMergeDecisionMsg:
		return m, m.applyGitMergeDecision(msg)
	case gitConflictLoadedMsg:
		return m, m.applyGitConflictLoaded(msg)
	case gitConflictSavedMsg:
		return m, m.applyGitConflictSaved(msg)
	case editorRecoveryClearedMsg:
		m.applyEditorRecoveryCleared(msg)
		return m, nil
	case shellport.LiveDocumentsChangedMsg:
		return m, m.applyDocumentTopic(msg)
	case shellport.DocumentInsightMsg:
		m.applyDocumentInsight(msg)
		return m, nil
	case shellport.ReplicaChangesMsg:
		return m, m.applyReplicaChanges(msg)
	case remotePromptHistoryMsg:
		m.applyRemotePromptHistory(msg)
		return m, nil
	case perfSnapshotMsg:
		m.applyPerfSnapshot(msg)
		return m, nil
	case shellport.SettingsChangedMsg:
		m.applyThemePreset(themePresetFromSettings(m.ws.LoadAppSettings()))
		m.applyEditorSettings(m.ws.LoadAppSettings().Editor)
		return m, nil
	case shellport.ReplicaFailureMsg:
		if msg.Owner == m.ws {
			m.showOperationFailure(msg.Operation+" failed", msg.Error)
		}
		return m, nil
	case remoteWorkspaceReadyMsg:
		return m, m.applyRemoteWorkspace(msg)
	case planModeResultMsg:
		return m, m.applyPlanModeResult(msg)
	case loopActionResultMsg:
		m.applyLoopActionResult(msg)
		return m, nil
	case goalActionResultMsg:
		m.applyGoalActionResult(msg)
		return m, nil
	case remoteSessionReadyMsg:
		return m, m.applyRemoteSession(msg)
	case types.OperationIssueMsg:
		m.presentOperationIssue(msg.Issue)
		return m, nil
	case agent.AgentEvent:
		return m, m.handleAgentEnvelope(msg)
	case types.ResumeStartupMsg:
		return m, m.launcherResumeSession(msg.SessionID, nil)
	case operationissue.ActionMsg:
		return m, m.handleOperationIssueAction(msg)
	case repairConfigLoadedMsg:
		return m, m.applyRepairConfigLoaded(msg)
	case pastedTextStoredMsg:
		m.applyPastedText(msg)
	case referenceContentLoadedMsg:
		m.applyReferenceContent(msg)
	case desktopPathOpenedMsg:
		m.applyDesktopPathOpened(msg)
	case referenceContentSavedMsg:
		m.applyReferenceSave(msg)
	case statusHydrateErrMsg:
		m.notifyStatus(types.SeverityWarning, "status", "Notification history unavailable", msg.Err.Error(), types.ResourceRef{Kind: types.ResourceWorkspace, ID: m.ws.RootPath()})
		return m, nil
	case workspace.StatusCenterHydratedMsg:
		m.syncWorkspacePanels()
		return m, nil
	case agentResultFeedbackMsg:
		m.handleAgentResultFeedback(msg)
		return m, nil
	case statusoverview.NavigateMsg:
		return m, m.handleStatusOverviewNavigate(msg)
	case statusoverview.CopySummaryMsg:
		m.copyToClipboard(statusSummaryText(msg.Item))
		return m, nil
	case statusoverview.MoreMsg:
		m.openStatusOverviewMore(msg.Item)
		return m, nil
	case statusoverview.ViewChangedMsg:
		m.syncWorkspacePanels()
		return m, nil
	case statusoverview.MarkReadMsg:
		if m.ws != nil && m.ws.Status() != nil {
			m.ws.Status().MarkRead(msg.ID, true)
			m.syncStatusOverview()
		}
		return m, nil
	case statusoverview.DismissMsg:
		if m.ws != nil && m.ws.Status() != nil {
			m.ws.Status().Dismiss(msg.ID)
			m.syncStatusOverview()
		}
		return m, nil
	case statusoverview.CloseMsg:
		if m.overlayHost != nil {
			m.overlayHost.Close(ui.OverlayTabStats)
			m.overlayHost.Close(ui.OverlayPromptHistory)
		}
		if m.termDock != nil {
			m.termDock.ClosePanel()
			m.setTermDockFocused(false)
		}
		return m, nil
	case connectionpopover.ActionMsg:
		return m, m.handleConnectionHealthAction(msg)
	case shellheader.ActionMsg:
		switch msg.Action {
		case shellheader.ConnectionAction:
			m.openConnectionHover(m.connectionWordmarkRect())
		case "remotes":
			return m, m.OpenRemotes()
		case "panes":
			return m, m.openMainPaneMenu()
		case "workspace":
			m.openTabStats(msg.TargetID)
		case "activity":
			m.openTabStats(msg.TargetID)
		case "notifications":
			m.toggleWorkspacePanel(termdock.PanelNotices)
		case "tabs":
			m.openTabStats(msg.TargetID)
		case "git":
			return m, m.toggleGitPanel()
		case "goal":
			return m, m.goalCommand(strings.TrimPrefix(msg.TargetID, "agent:"), "")
		case "loops":
			return m, m.loopCommand(strings.TrimPrefix(msg.TargetID, "agent:"), "")
		case "git-branch":
			return m, m.openBranchSwitcher()
		case "git-changes":
			return m, m.openGitChangesWindow()
		case "workspace-path":
			return m, m.openWorkspacePathSwitcher()
		}
		return m, nil
	case shellfooter.ActionMsg:
		switch msg.Action {
		case "workspace":
			m.toggleWorkspacePanel(termdock.PanelWorkspace)
		case "problems":
			m.toggleWorkspacePanel(termdock.PanelProblems)
		case "notices", "notifications":
			m.toggleWorkspacePanel(termdock.PanelNotices)
		case "terminals":
			m.toggleWorkspacePanel(termdock.PanelTerminal)
		case "git":
			return m, m.toggleGitPanel()
		case "board":
			return m, m.toggleBoardBrowser()
		case "memory":
			return m.toggleMemoryBrowser()
		case "performance":
			return m, m.togglePerformancePanel()
		case "footer-more":
			m.openFooterMenu()
		}
		return m, nil
	case interactiveFrameMsg:
		m.handleInteractiveFrame(msg)
		return m, nil
	case tea.WindowSizeMsg:
		initialSize := m.width == 0 || m.height == 0
		m.ws.Logger().Debug("UI", "resize", "width", msg.Width, "height", msg.Height)
		m.width = msg.Width
		m.height = msg.Height
		if m.compositor != nil {
			m.compositor.Resize(msg.Width, msg.Height)
		}
		if initialSize && m.ActiveSession != nil {
			if err := m.ActiveSessionConnectionIssue(); err != nil {
				m.showAccountRecovery(true, err.Error())
			}
		}
		if initialSize {
			for _, issue := range m.ws.TakeStartupIssues() {
				m.presentOperationIssue(issue)
			}
		}
		// The shell surface is resized on the next View() frame by
		// renderShellToSurface (single full-screen surface).
		if !m.terminalImages.started {
			return m, m.probeTerminalImages()
		}
		return m, tea.Raw(terminalImageSizeQuery())

	case tea.TerminalWidthProfileMsg:
		m.applyTerminalWidthProfile(msg)
	case workspace.DocumentAutosaveDueMsg:
		return m, m.handleAutosaveDue(msg)
	case editorSaveResultMsg:
		return m, m.handleEditorSaveResult(msg)
	case editorFormatResultMsg:
		return m, m.handleEditorFormatResult(msg)
	case editorDiskResultMsg:
		m.applyEditorDisk(msg)
		return m, nil
	case editor.InspectChangeMsg:
		if msg.Editor.IsOpen() && msg.Editor.Revision() == msg.Revision {
			m.inspectEditorChange(msg.Editor, msg.Line)
		}
		return m, nil
	case editorchanges.NavigateMsg:
		m.navigateEditorChanges(msg)
		return m, nil
	case localDiffPreparedMsg:
		m.applyLocalDiffPrepared(msg)
		return m, nil
	case tea.BlurMsg:
		m.cancelPointerCaptures()
		if m.editorSettings.AutoSave == types.AutoSaveTerminalFocusLoss && m.DirtyPromptPath == "" {
			var saves []tea.Cmd
			for _, tab := range m.EditorState.Tabs {
				if tab != nil && tab.Editor != nil && tab.Editor.Modified() {
					saves = append(saves, m.requestEditorSave(tab.Editor, workspace.DocumentSaveFocusOut, false))
				}
			}
			return m, tea.Batch(saves...)
		}
		return m, nil

	case types.TickMsg:
		model, cmd := m.handleTick(msg)
		return model, tea.Batch(cmd, onboardingCmd)
	case editor.PointerScrollMsg:
		return m, m.deliverPointerScroll(msg)

	case tea.KeyboardEnhancementsMsg:
		// Shortcut captions name the chord this terminal delivers; the answer
		// to the keyboard query can arrive after the first frame.
		ui.NoteKeyboardEnhancements(msg)
		return m, nil

	case tea.KeyPressMsg:
		return m.dispatchKeyPress(msg)

	case tea.PasteMsg:
		return m.dispatchPasteMsg(msg)

	case tea.MouseClickMsg:
		m.ws.Logger().Debug("UI", "mouse", "click", "x", msg.Mouse().X, "y", msg.Mouse().Y, "button", fmt.Sprintf("%d", msg.Button))
		if m.settingsPromptOnTop() && msg.Button == tea.MouseRight {
			return m, nil
		}
		// The WM close hit is normally handled by dispatchMouseClick, which
		// cannot return its close command. The settings X must preserve the
		// surface's pure PromptMsg/CancelMsg command instead.
		if msg.Button == tea.MouseLeft && m.windowManager != nil {
			if fw := m.windowManager.Window(string(ui.OverlayWorkspaceSettings)); fw != nil &&
				m.overlayHost != nil && m.overlayHost.Top() != nil && m.overlayHost.Top().Window == fw &&
				fw.IsOnCloseButton(msg.Mouse().X, msg.Mouse().Y) {
				return m, m.closePopupWindowByID(fw.ID)
			}
		}
		return m.dispatchMouseClick(msg)

	case tea.MouseWheelMsg:
		if m.settingsPromptOnTop() {
			return m, nil
		}
		m.ws.Logger().Debug("UI", "mouse", "wheel", "x", msg.X, "y", msg.Y, "button", fmt.Sprintf("%d", msg.Button))
		return m.dispatchMouseWheel(msg)

	case tea.MouseMotionMsg:
		if m.settingsPromptOnTop() && m.windowManager != nil && !m.windowManager.IsDragging() {
			if prompt := m.overlayHost.Window(ui.OverlaySettingsPrompt); prompt != nil && !prompt.ContainsPoint(msg.Mouse().X, msg.Mouse().Y) {
				return m, nil
			}
		}
		model, cmd := m.dispatchMouseMotion(msg)
		return model, tea.Batch(cmd, m.requestCoalescedFrame(time.Now()))

	case tea.MouseReleaseMsg:
		if m.settingsPromptOnTop() && m.windowManager != nil && !m.windowManager.IsDragging() {
			return m, nil
		}
		// Same as MouseMotionMsg: let the release handler set
		// dirty when it actually does something visible.
		if m.ws.Logger() != nil {
			m.ws.Logger().Debug("UI", "mouse", "release", "x", msg.Mouse().X, "y", msg.Mouse().Y)
		}
		m.forceFrame()
		return m.dispatchMouseRelease(msg)

	case types.SelectionTickMsg:
		if m.chatScreen != nil && m.rootHost != nil {
			host := m.hostFor(m.chatScreen.ID())
			host.Focus(m.chatScreen.ID())
			res := host.Handle(msg)
			if res.Cmd != nil {
				cmds = append(cmds, res.Cmd)
			}
		}

	}

	cmds = append(cmds, m.handleAppMessages(msg)...)
	if coalescePresentation {
		cmds = append(cmds, m.requestCoalescedFrame(time.Now()))
	}

	return m, tea.Batch(cmds...)
}

// invalidateMessageRegions publishes source mutations to the one damage owner.
// Unknown messages conservatively invalidate the shell. High-rate terminal
// publications and clock updates have bounded, explicit presentation owners;
// scheduling a frame itself is never a source mutation.
func (m *App) invalidateMessageRegions(msg tea.Msg) {
	if m.rootHost == nil || m.shell == nil {
		return
	}
	mark := func(c ui.Component) {
		if c != nil {
			m.rootHost.MarkComponentPaint(c.ID())
		}
	}
	switch msg := msg.(type) {
	case interactiveFrameMsg:
		return
	case tea.KeyPressMsg:
		if m.terminalOwnsKeys() {
			// The focused terminal Host owns selection, blink and prediction
			// paint. Global terminal shortcuts own their layout/overlay damage.
			// Do not convert an admitted PTY key into a full shell repaint.
			return
		}
		m.rootHost.MarkPaint()
	case shellport.TerminalFrameMsg:
		if msg.Err != nil {
			m.rootHost.MarkPaint()
			return
		}
		// A grid publication may also change the tab title. Floating terminal
		// content is independently invalidated by its existing window owner.
		mark(m.shell.termDock)
	case terminalProjectionMsg:
		if msg.err != nil {
			m.rootHost.MarkPaint()
			return
		}
		mark(m.shell.termDock)
	case workspace.TerminalChangedMsg:
		// Only schedules the projection read; the result owns paint damage.
	case types.TickMsg:
		// Focused text controls (including the file-tree filter) own cursor
		// blink even when their surrounding slot has no clock-driven content.
		m.invalidateClockContent()
		mark(m.shell.header)
		mark(m.shell.footer)
		mark(m.shell.activity)
		mark(m.shell.termDock)
		// Agent/workspace lists have clock-driven activity. The file tree has
		// no clock presentation; its async refresh result invalidates normally.
		if m.activityView != activityFiles {
			mark(m.shell.sidebar)
		}
	default:
		m.rootHost.MarkPaint()
	}
}

func (m *App) handleTick(msg types.TickMsg) (tea.Model, tea.Cmd) {
	var diskSyncCmd tea.Cmd
	now := time.Now()
	spinFrame := int(now.UnixMilli())
	m.SpinFrame = spinFrame
	m.Input.SetSpinFrame(spinFrame)
	m.refreshSessionBackgroundJobs(now)
	m.drainLSPDiagnostics()
	completionCmd := m.autoCompletionCmd(now)
	m.syncStatusOverview()
	m.syncWorkspacePanels()
	m.tickTerminalCursors(now)
	if fw := m.windowManager.Focused(); fw != nil {
		if animated, ok := fw.Content.(interface{ NeedsFrame() bool }); ok && animated.NeedsFrame() {
			fw.MarkContentDirty()
		}
	}
	var gitCmd tea.Cmd
	if m.editorGitDue(now) {
		gitCmd = m.refreshEditorGitCmd()
	}
	// Header Git identity: the file and repository-metadata watchers arm a
	// debounced reconciliation read here, so the branch and change counts follow
	// the repository without the user opening the Git panel.
	var headerGitCmd tea.Cmd
	if m.gitSnapshotReady(now) {
		headerGitCmd = m.startGitSnapshotRead()
	}
	var treeGitCmd tea.Cmd
	const treeGitFallbackInterval = 15 * time.Second
	if m.treeGitRefreshAt.IsZero() {
		m.treeGitRefreshAt = now
	} else if now.Sub(m.treeGitRefreshAt) >= treeGitFallbackInterval &&
		m.Tree != nil && m.Tree.Model != nil {
		// File and repository-metadata watchers are the fast path. This sparse
		// asynchronous reconciliation covers fsnotify overflow/dropped events
		// without polling Git on the update goroutine.
		treeGitCmd = m.Tree.Model.RefreshGitStatus()
		m.treeGitRefreshAt = now
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
	m.ws.AdvanceAgentState(now)
	for _, n := range m.ws.AgentMgr().AllNodes() {
		if n.State != nil {
			m.syncAgentMessages(n.ID, false)
		}
	}
	m.subPaneProjections.TickAll(spinFrame)
	m.syncAgentWindowTabs()
	var diskCmds []tea.Cmd
	for _, tab := range m.EditorState.Tabs {
		if tab == nil || tab.Editor == nil {
			continue
		}
		if state := m.editorSaves[tab.Editor.BufferID()]; state != nil && state.inFlight {
			continue
		}
		if tab.Editor.DiskChanged() {
			diskCmds = append(diskCmds, m.refreshEditorDiskCmd(tab.Editor))
		}
	}
	diskSyncCmd = tea.Batch(diskCmds...)

	m.syncLSPStatusIssues()
	m.syncAgentStatusInventory()
	m.syncPerfAttention(now)
	perfCmd := m.requestPerfSnapshot(false)
	var launcherCmd tea.Cmd
	if m.capabilityObservationVisible() {
		launcherCmd = m.launcherCapabilitiesCmd(now)
	} else if m.launcherCapabilityCancel != nil {
		m.launcherCapabilityCancel()
		m.launcherCapabilityCancel = nil
		m.launcherCapabilitySequence++
	}

	// Status bar animations and popup reveals need their own content
	// re-rendered even when main content hasn't changed.
	if m.wantsFastTick() {
		perf.SentinelTick()
		return m, tea.Batch(diskSyncCmd, gitCmd, headerGitCmd, treeGitCmd, launcherCmd, completionCmd, perfCmd, streamingTickCmd())
	}
	perf.SentinelTick()
	return m, tea.Batch(diskSyncCmd, gitCmd, headerGitCmd, treeGitCmd, launcherCmd, completionCmd, perfCmd, tickCmd())
}

// wantsFastTick reports whether the 16 ms streaming tick is justified right
// now: an agent/sub-pane is actively streaming, a workspace load is pending,
// or a popup reveal is mid-flight. An idle launcher does NOT qualify; it has no 60 Hz
// animation, and a permanent 16 ms tick there repaints full 160x40 frames
// (roughly a quarter CPU on an idle floating terminal).
func (m *App) wantsFastTick() bool {
	if m.headerScreen != nil {
		state := m.connectionHealth().brand
		if state == shellheader.ConnectionConnecting || state == shellheader.ConnectionReconnecting {
			return true
		}
	}
	if m.launcherScreen != nil && m.launcherScreen.Loading() && m.launcherHostVisible() {
		return true
	}
	if m.activityListLoading() && m.sidebarHostVisible() {
		return true
	}
	// Running and stopping agents repaint at frame rate; an idle session
	// keeps the idle cadence.
	if m.anyAgentAnimating() {
		return true
	}
	// Long transcripts settle offscreen history a few milliseconds per frame.
	if m.subPaneProjections.AnyMaterializing() {
		return true
	}
	// Aeron drafting a commit message animates its progress rule.
	if m.gitPanel != nil && m.gitPanel.Writing() && m.gitPanelOpen() {
		return true
	}
	// A window showing continuous motion (settings capability search) moves
	// at frame rate; a caret blink alone keeps the idle cadence.
	if m.windowManager != nil {
		for _, fw := range m.windowManager.Windows() {
			if animated, ok := fw.Content.(ui.Animated); ok && animated.NeedsAnimation() {
				return true
			}
		}
	}
	// Popup reveal animations need the fast tick to render the slide-in.
	return len(m.reveals) > 0
}

func (m *App) handleAppMessages(msg tea.Msg) []tea.Cmd {
	var cmds []tea.Cmd

	if msg, ok := msg.(types.AgentMsg); ok {
		cmds = append(cmds, m.handleAgentEvent(types.AgentEventMsg(msg)))
	}

	switch msg := msg.(type) {
	case inputAdmissionMsg:
		cmds = append(cmds, m.applyInputAdmission(msg))
	case workspaceDeletePreviewMsg:
		m.applyWorkspaceDeletePreview(msg)
	case workspaceDeleteIntent:
		cmds = append(cmds, m.handleWorkspaceDeleteIntent(msg))
	case workspaceDeleteResultMsg:
		m.applyWorkspaceDeleteResult(msg)
	case sourceControlResultMsg:
		if cmd := m.applySourceControlResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
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
	case composerDraftLoadedMsg:
		m.applyComposerDraft(msg)
	case promptHistoryRestoredMsg:
		m.applyPromptHistory(msg)
	case attachmentPreparedMsg:
		cmds = append(cmds, m.applyAttachmentPrepared(msg))
	case attachmentOpenedMsg:
		m.applyAttachmentOpened(msg)
	case promptHistorySavedMsg:
		if msg.store == m.Prompts {
			m.syncPromptRecall()
			if msg.err != nil {
				m.Feedback.NotifyError("Could not save prompt history: " + msg.err.Error())
			}
		}
	case composerDraftSavedMsg:
		m.onComposerDraftSaved(msg)
	case directoryAsyncResultMsg:
		m.applyDirectoryAsyncResult(msg)
	case ui.SearchDebouncedMsg:
		if m.findInFiles != nil {
			if cmd := m.findInFiles.HandleSearchDebounced(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case launcher.ActivateMsg:
		if m.launcherScreen == msg.Screen {
			cmds = append(cmds, m.handleLauncherActivate(msg))
		}
	case launcher.CommandMsg:
		if m.launcherScreen == msg.Screen {
			cmds = append(cmds, m.handleLauncherCommand(msg))
		}
	case launcherWorkspacesMsg:
		// Async workspace listing. Apply only the newest kick, and only if
		// the launcher is still mounted (a session may have opened in the
		// meantime). Applying keeps the user's filter/selection: SetData
		// rebuilds items without resetting filter state.
		if msg.Gen != m.launcherLoadGen.Load() {
			break
		}
		m.applyWorkspaceListing(msg)
		m.rootHost.MarkLayout()
		m.rootHost.Layout()
	case workspaceTreeReadyMsg:
		m.applyWorkspaceTreeReady(msg)
	case workspace.LSPDiagnosticsMsg:
		m.handleLSPDiagnosticsUpdate(msg)
	case workspace.FileWatcherMsg:
		if cmd := m.handleFileWatcherEvent(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if m.Tree != nil && m.Tree.Model != nil {
			if cmd := m.Tree.Model.RefreshGitStatus(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// A working-tree write (our save, an agent edit, an external tool) can
		// change the header's change count; arm the coalesced reconcile read.
		m.requestGitSnapshot(time.Now())
	case findinfiles.SearchRequestMsg:
		if m.findInFiles != nil && m.findInFiles.AcceptsSearch(msg.Screen, msg.Generation) {
			m.findInFilesRequest = m.ws.StartProjectSearch(msg.Query, msg.Options)
			m.findInFilesGen = msg.Generation
			m.markProjectSearchDirty()
		}
	case findinfiles.SearchCancelMsg:
		if m.findInFiles == msg.Screen {
			m.ws.CancelProjectSearch()
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
	case terminalCreatedMsg:
		cmds = append(cmds, m.applyTerminalCreated(msg))
	case terminalProjectionMsg:
		cmds = append(cmds, m.applyTerminalProjection(msg))
	case shellport.TerminalFrameMsg:
		cmds = append(cmds, m.applyTerminalFrame(msg))
	case workspace.TerminalChangedMsg:
		cmds = append(cmds, m.terminalRefresh(msg.ID))
	case workspace.TerminalEffectsMsg:
		m.handleTerminalEffects(msg)
	case workspace.TerminalExitedMsg:
		m.handleTerminalExited(msg)
	case workspace.TerminalTitleMsg:
		m.applyTerminalTitle(msg.ID, msg.Title)
	case findinfiles.OpenMsg:
		if m.findInFiles == nil || !m.findInFiles.AcceptsSearch(msg.Screen, msg.Generation) {
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
	case memorybrowser.SearchMsg:
		cmds = append(cmds, m.handleMemoryBrowserSearch(msg))
	case memoryBrowserSearchResultMsg:
		m.applyMemoryBrowserSearch(msg)
	case memorybrowser.DetailMsg:
		cmds = append(cmds, m.handleMemoryBrowserDetail(msg))
	case memoryBrowserDetailResultMsg:
		m.applyMemoryBrowserDetail(msg)
	case memorybrowser.MutationMsg:
		cmds = append(cmds, m.handleMemoryBrowserMutation(msg))
	case memoryBrowserMutationResultMsg:
		cmds = append(cmds, m.applyMemoryBrowserMutation(msg))
	case memorybrowser.CancelMsg:
		m.handleMemoryBrowserCancel(msg)
	case types.ConfigReloadedMsg:
		if msg.ThemePreset != "" {
			m.applyThemePreset(msg.ThemePreset)
		}
	case composer.SubmitMsg:
		if composerScreen := m.sessionComposer(); composerScreen == nil || msg.Target != composerScreen {
			break
		}
		if agentID := m.activeAgentTabID(); msg.Text != "" && agentID != "" {
			cmds = append(cmds, m.submitComposerInput(agentID, msg.Text))
		}
	case composer.CancelMsg:
		if composerScreen := m.sessionComposer(); composerScreen != nil && msg.Target == composerScreen {
			m.overlayHost.Close(ui.OverlayComposer)
		}
	case textoverlay.SaveMsg:
		if overlay := m.sessionTextOverlay(); overlay != nil && msg.Target == overlay && msg.RefID == overlay.RefID() && msg.Epoch == overlay.OpenEpoch() {
			cmds = append(cmds, m.saveReferenceFromOverlay(msg))
		}
	case textoverlay.CancelMsg:
		if overlay := m.sessionTextOverlay(); overlay != nil && msg.Target == overlay && msg.Epoch == overlay.OpenEpoch() {
			m.setMode(ModeChat)
		}
	case lspDefinitionResultMsg:
		if m.acceptsLSPDocument(msg.generation, msg.filePath, msg.owner, msg.revision) {
			m.handleDefinitionResult(msg)
		}
	case lspHoverResultMsg:
		m.handleLSPHoverResult(msg)
	case signatureHelpResultMsg:
		m.handleSignatureHelpResult(msg)
	case lspReferencesResultMsg:
		if m.acceptsLSPDocument(msg.generation, msg.filePath, msg.owner, msg.revision) {
			m.handleReferencesResult(msg)
		}
	case lspDocumentSymbolsResultMsg:
		if m.acceptsLSPDocument(msg.generation, msg.filePath, msg.owner, msg.revision) {
			m.handleDocumentSymbolsResult(msg)
		}
	case lspRenameResultMsg:
		if m.acceptsLSPDocument(msg.generation, msg.filePath, msg.owner, msg.revision) {
			cmds = append(cmds, m.handleRenameResult(msg))
		}
	case lspRenamePreparedMsg:
		m.showLSPRenamePreview(msg)
	case lspRenameActionMsg:
		cmds = append(cmds, m.handleLSPRenameAction(msg))
	case lspRenameAppliedMsg:
		m.handleLSPRenameApplied(msg)
	case lspCompletionResultMsg:
		if m.acceptsLSPDocument(msg.generation, msg.filePath, msg.owner, msg.revision) {
			m.handleCompletionResult(msg)
		}
	case types.ErrorMsg:
		m.ws.Logger().Debug("UI", "error", "msg", string(msg))
		m.Feedback.NotifyError(string(msg))
	case slashBackendResultMsg:
		m.handleSlashBackendResult(msg)
	case types.UpdateModelMsg:
		m.ModelName = string(msg)
		m.ws.Logger().Debug("UI", "model_update", "model", string(msg))
	case types.OpenSettingsMsg:
		m.ws.Logger().Info("UI", "open_session_settings")
		m.openSessionSettings()
	case selectedSessionSettingsResult:
		m.applySelectedSessionSettings(msg)
	case rendering.ClipboardCopiedMsg:
		m.ws.Logger().Debug("CLIPBOARD", "copied", "terminal", msg.Terminal)
		if msg.Terminal {
			m.Feedback.NotifyInfo("copied via the terminal clipboard")
		} else {
			m.Feedback.NotifyInfo("copied to clipboard")
		}
	case rendering.ClipboardErrorMsg:
		m.ws.Logger().Error("CLIPBOARD", "error", "error", string(msg))
		m.Feedback.NotifyError(string(msg))
	case types.HistoryMessagesMsg:
		m.hydrateActiveMessages(msg)
	case pickerPathSearchResultMsg:
		if cmd := m.applyPickerPathSearchResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case treePathSearchResultMsg:
		if cmd := m.applyTreePathSearchResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case filepickerscreen.NavigateMsg:
		if !msg.ValidFor(m.FilePicker) {
			break
		}
		m.invalidatePathSearch(msg.Target)
		if cmd := m.beginDirectoryAsyncLoadFor(msg.Target, msg.Path, msg.Intent()); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case filepickerscreen.LoadMsg:
		if !msg.ValidFor(m.FilePicker) {
			break
		}
		if cmd := m.beginDirectoryAsyncExpandFor(msg.Target, msg.Path, msg.Intent()); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case filepickerscreen.SearchMsg:
		if cmd := m.beginPickerPathSearch(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case filepickerscreen.CreateFolderMsg:
		if cmd := m.beginPickerCreateFolder(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pickerCreateFolderResultMsg:
		if cmd := m.applyPickerCreateFolderResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case filepickerscreen.ToggleMsg:
		if !msg.ValidFor(m.FilePicker) {
			break
		}
		if m.FilePicker.TogglePath(msg.Path) {
			m.markFilePickerDirty()
		}
	case filepickerscreen.SelectMsg:
		if !msg.ValidFor(m.FilePicker) {
			break
		}
		m.invalidatePathSearch(msg.Target)
		m.invalidateDirectoryAsyncLoad(msg.Target)
		paneOwner := m.paneFilePicker
		m.paneFilePicker = nil
		m.overlayHost.Close(ui.OverlayFilePicker)
		if msg.Folder {
			cmds = append(cmds, m.launcherOpenFolder(msg.Path))
		} else {
			paths := msg.Paths
			if len(paths) == 0 && msg.Path != "" {
				paths = []string{msg.Path}
			}
			if paneOwner != nil {
				if paneOwner.generation == m.workspaceSwitchGen.Load() {
					cmds = append(cmds, m.openPaneFiles(paths, paneOwner.group))
				}
				break
			}
			for _, path := range paths {
				m.addFileReference(path)
			}
		}
	case paneFilesOpenedMsg:
		cmds = append(cmds, m.applyPaneFilesOpened(msg))
	case filepickerscreen.CancelMsg:
		if msg.ValidFor(m.FilePicker) {
			m.invalidatePathSearch(msg.Target)
			m.invalidateDirectoryAsyncLoad(msg.Target)
			m.overlayHost.Close(ui.OverlayFilePicker)
		}
	case settingsui.SaveMsg:
		m.ws.Logger().Info("CONFIG", "settings_save", "scope", string(msg.Scope))
		cmds = append(cmds, m.savePreferences(msg))
	case preferencesSavedMsg:
		m.applyPreferencesSaved(msg)
	case settingsShortcutResultMsg:
		m.applySettingsShortcut(msg)
	case recoveryRetryResultMsg:
		m.applyRecoveryRetryResult(msg)
	case recoveryCancelResultMsg:
		m.applyRecoveryCancelResult(msg)
	case settingsui.AccountChangeMsg:
		request := msg
		owner := m.ws
		generation := m.workspaceSwitchGen.Load()
		cmds = append(cmds, func() tea.Msg {
			err := owner.SaveAccountChange(request.Change)
			var settings types.AppSettings
			var refreshErr error
			if err == nil {
				if reader, ok := owner.(interface {
					RefreshOnboarding(context.Context) (types.AppSettings, error)
				}); ok {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					settings, refreshErr = reader.RefreshOnboarding(ctx)
					cancel()
				} else {
					settings = owner.AppDefaults()
				}
				if refreshErr != nil {
					settings = types.CloneAppSettings(owner.AppDefaults())
					if settings.Accounts == nil {
						settings.Accounts = make(map[string]provider.Account)
					}
					if request.Change.After == nil {
						delete(settings.Accounts, request.Change.ID)
					} else {
						settings.Accounts[request.Change.ID] = provider.CloneAccount(*request.Change.After)
					}
				}
			}
			return settingsui.AccountChangeResultMsg{Target: request.Target, Change: request.Change, Settings: settings, Err: err, RefreshError: refreshErr, WorkspaceGeneration: generation}
		})
	case capabilitySnapshotMsg:
		m.applyCapabilitySnapshot(msg)
	case launcherCapabilitiesMsg:
		m.applyLauncherCapabilities(msg)
	case capabilityPickerResultMsg:
		m.applyCapabilityPickerResult(msg)
	case toolLayoutMsg:
		if msg.generation == m.workspaceSwitchGen.Load() && msg.version == m.toolLayoutVersion && msg.err != nil {
			// A failed load carries no layout; keep the current one rather than
			// installing an empty rail. Reset navigation rewrites the stored value.
			m.Feedback.NotifyError("Workspace layout could not be loaded; using the default navigation: " + msg.err.Error())
		} else if msg.generation == m.workspaceSwitchGen.Load() && msg.version == m.toolLayoutVersion {
			m.toolLayout = msg.layout
			m.workspacePanelHeights = msg.layout.PanelHeights
			m.activityBar.SetOrder(msg.layout.Rail)
			m.setActivity(m.activityView, false)
		}
	case toolLayoutSaveMsg:
		if msg.generation == m.workspaceSwitchGen.Load() {
			m.toolLayoutSaveQueued = false
			cmds = append(cmds, m.saveToolLayoutCmd())
		}
	case toolLayoutSavedMsg:
		if msg.generation == m.workspaceSwitchGen.Load() {
			m.toolLayoutSaving = false
			m.toolLayoutSavedVersion = msg.version
			if msg.err != nil {
				m.Feedback.NotifyError("Layout was not saved: " + msg.err.Error())
			}
		}
	case capabilityPollMsg:
		if msg.generation == m.workspaceSwitchGen.Load() {
			if _, exists := m.capabilityOperations[msg.sequence]; exists {
				cmds = append(cmds, m.capabilitySnapshotCmd(), m.capabilityPollCmd(msg.generation, msg.sequence))
			}
		}
	case capabilityContextMsg:
		accepted := msg.generation == m.workspaceSwitchGen.Load() && msg.owner == m.ActiveSession && msg.agentID == m.activeAgentTabID() && msg.draft == m.Input.Value() && msg.result.Err == nil
		if accepted {
			m.Input.InsertReference(msg.reference)
		} else if msg.result.Err == nil {
			msg.result.Err = fmt.Errorf("composer ownership or draft changed; context was not attached")
		}
		if msg.result.Target == nil && msg.result.Err != nil {
			m.Feedback.NotifyError(msg.result.Err.Error())
		}
		cmds = append(cmds, m.applyCapabilityResult(msg.result))
	case boardResultMsg:
		m.applyBoardResult(msg)
	case gitChangesPreparedMsg:
		cmds = append(cmds, m.applyGitChangesPrepared(msg))
	case gitEvidenceMsg:
		m.applyGitEvidence(msg)
	case gitCommitDeltaMsg:
		m.applyCommitMessageDelta(msg)
	case gitCommitWrittenMsg:
		m.applyCommitMessageWritten(msg)
	case gitDiscardPreviewMsg:
		if msg.generation != m.workspaceSwitchGen.Load() || msg.sequence != m.gitDiscardSequence || m.GitDiscardPending == nil || m.GitDiscardPending.Path != msg.change.Path || !m.overlayHost.IsOpen(ui.OverlayTreeDelete) {
			break
		}
		if msg.err != nil {
			m.Feedback.NotifyError(msg.err.Error())
			m.overlayHost.Close(ui.OverlayTreeDelete)
			break
		}
		m.gitDiscardPreview = &msg.preview
		if window := m.overlayHost.Window(ui.OverlayTreeDelete); window != nil {
			frame := m.buildGitDiscardFrame(msg.change)
			width, height := frame.Measure(m.width, m.height)
			window.Content = frame
			window.SetSize(width, height)
			window.MarkContentDirty()
		}
	case settingsui.CapabilityRequestMsg:
		cmds = append(cmds, m.capabilityRequestCmd(msg))
	case settingsui.CapabilityResultMsg:
		if command := m.applyCapabilityResult(msg); command != nil {
			cmds = append(cmds, command)
		}
	case settingsui.CapabilitySearchTickMsg:
		if command := m.applyCapabilitySearchTick(msg); command != nil {
			cmds = append(cmds, command)
		}
	case settingsui.AccountChangeResultMsg:
		if msg.WorkspaceGeneration != m.workspaceSwitchGen.Load() {
			break
		}
		if msg.Err != nil {
			m.Feedback.NotifyError("Account not saved: " + msg.Err.Error())
		} else {
			if installer, ok := m.ws.(interface{ ApplyOnboarding(types.AppSettings) }); ok {
				installer.ApplyOnboarding(msg.Settings)
			}
			m.refreshAllSessionSettingsProjections()
			m.Feedback.NotifyInfo("Account change saved")
			if msg.RefreshError != nil {
				m.Feedback.NotifyError("Account saved. The configuration could not be refreshed: " + msg.RefreshError.Error())
			}
		}
		if fw := m.windowManager.Window(string(ui.OverlayWorkspaceSettings)); fw != nil {
			if w, ok := fw.Content.(*ui.WindowFrameWidget); ok {
				if body, ok := w.Body().(*settingsui.WorkspaceSettingsSurface); ok && body.Model() == msg.Target {
					if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
						cmds = append(cmds, cmd)
					}
					w.Host().MarkLayout()
				}
			}
		}
		cmds = append(cmds, m.continueOnboardingAccount(msg))
	case settingsui.RemoteAccessResultMsg:
		cmds = append(cmds, m.applyRemoteSettingsMessage(msg.Target, msg))
	case settingsui.RemotePairingExpiredMsg:
		cmds = append(cmds, m.applyRemoteSettingsMessage(msg.Target, msg))
	case settingsui.ScopeMsg:
		m.openSettings(msg.Scope)
	case settingsui.ResetSectionMsg:
		if m.overlayHost == nil {
			break
		}
		body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
		if body == nil || body.Model() != msg.Target || msg.Target.Scope() != msg.Scope || !msg.Target.BeginSave() {
			break
		}
		label := "Page restored to built-in defaults"
		if msg.Scope == types.SettingsScopeWorkspace {
			label = "Workspace page choices cleared"
		}
		cmds = append(cmds, m.saveSettingsShortcut(msg.Scope, msg.Edits, label, msg.Target, msg.Category))
	case settingsui.CancelMsg:
		// Live preset cycling previews via ThemePreviewMsg without persisting.
		// Cancel must restore the last saved palette across the whole UI.
		m.applyThemePreset(themePresetFromSettings(m.ws.LoadAppSettings()))
		m.clearFramePreview()
		m.overlayHost.Close(ui.OverlayWorkspaceSettings)
	case settingsui.PromptMsg:
		m.showSettingsPrompt(msg)
	case settingsui.ThemePreviewMsg:
		if msg.Preset != "" {
			m.applyThemePreset(msg.Preset)
		}
	case settingsui.FramePreviewMsg:
		// Live shell-frame preview: re-inset immediately, no persistence.
		// The overlay survives until save (dropped) or cancel (restored).
		m.applyFramePreview(msg)
	case settingsui.FetchModelsMsg:
		request := msg
		fetch := m.ws.PrepareDraftAccountModels(request.Account, request.BaseURL, request.StandardID)
		cmds = append(cmds, func() tea.Msg {
			models, err := fetch()
			if err != nil {
				return settingsui.ModelsErrorMsg{ProviderID: request.ProviderID, AccountID: request.AccountID, Generation: request.Generation, Error: err.Error()}
			}
			return settingsui.ModelsMsg{ProviderID: request.ProviderID, AccountID: request.AccountID, Generation: request.Generation, Models: models}
		})
	case settingsui.ModelsMsg, settingsui.ModelsErrorMsg, types.ModelCatalogUpdatedMsg:
		if fw := m.windowManager.Window("workspace-settings"); fw != nil {
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case historyscreen.TimelineMsg, historyscreen.PlanMsg:
		if fw := m.windowManager.Window(string(ui.OverlayLocalHistory)); fw != nil {
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case historyApplyResultMsg:
		if cmd := m.applyHistoryResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case historyscreen.AppliedMsg:
		if fw := m.windowManager.Window(string(ui.OverlayLocalHistory)); fw != nil {
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if msg.Error == "" && msg.RequestID == "" {
			if cmd := m.applyHistoryRefresh(msg.Result); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if msg.Warning != "" {
				m.Feedback.NotifyError(msg.Warning)
			}
			// Scoped restores expose their safety inverse on the canonical chat
			// boundary. A workspace snapshot has no conversation owner: retain
			// its real success view and Undo action instead of inventing one.
			if msg.Result.SessionID != "" && msg.Warning == "" && !msg.Incomplete {
				m.overlayHost.Close(ui.OverlayLocalHistory)
			}
		}
	case historyLatestMsg:
		if msg.Error != "" {
			m.Feedback.NotifyError(msg.Error)
		} else if cmd := m.openLocalHistoryForMessage(msg.SessionID, msg.MessageID, msg.AgentID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case historyCandidatesMsg:
		if msg.Error != "" {
			m.Feedback.NotifyError(msg.Error)
		} else if cmd := m.openHistoryCandidates(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case historyOperationMsg:
		if msg.Error != "" {
			m.Feedback.NotifyError(msg.Error)
		} else {
			m.Feedback.NotifyInfo(msg.Label)
		}
	case workspace.HistoryServiceStatus:
		if msg.Error != "" {
			m.ws.Logger().Error("HISTORY", "service_error", "error", msg.Error)
			m.Feedback.NotifyError("Local history: " + msg.Error)
		}
	case settingsui.ChatGPTSignInResultMsg, settingsui.ChatGPTSignOutMsg:
		// The settings owner validates account identity and generation. Auth edits
		// are staged there and persisted only by Save; closing/cancelling the
		// window makes a late callback inert, rather than resurrecting an account.
		if fw := m.windowManager.Window("workspace-settings"); fw != nil {
			if cmd := m.forwardEventToWindowContent(fw, msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case sessionsettings.SaveMsg:
		m.ws.Logger().Info("CONFIG", "session_settings_save")
		m.Feedback.NotifyInfo("saving...")
		cmd, err := m.prepareSessionSettingsSave(msg)
		if err != nil {
			m.Feedback.NotifyError(err.Error())
			break
		}
		cmds = append(cmds, cmd)
	case sessionsettings.SaveAsWorkspaceMsg:
		if owner, _ := m.sessionProjections.projectionForSettings(msg.Target); owner == nil {
			m.Feedback.NotifyError("Session settings owner is no longer available")
			break
		}
		if msg.Settings.SessionMainAgent != "" {
			m.Feedback.NotifyError("Choose Main Agent before saving agent settings as workspace defaults")
			break
		}
		// Promote the current agent settings to workspace defaults so every
		// future agent (and new session) inherits them.
		m.ws.Logger().Info("CONFIG", "session_settings_save_as_workspace")
		edits := promotedSessionEdits(msg.Settings)
		cmds = append(cmds, m.saveSettingsShortcut(types.SettingsScopeWorkspace, edits, "Agent settings saved as workspace defaults", nil, 0))
	case sessionSettingsSaveResultMsg:
		if msg.workspace != m.ws || msg.generation != m.workspaceSwitchGen.Load() {
			break
		}
		session, projection := m.sessionProjections.projectionForSettings(msg.target)
		if msg.result.Err != nil {
			m.ws.Logger().Error("CONFIG", "save_error", "error", msg.result.Err.Error())
			m.Feedback.NotifyError(msg.result.Err.Error())
		} else if m.ws.ApplySessionSettingsResult(msg.result) && projection != nil {
			current := msg.result.Snapshot.Effective
			if session.Settings != nil {
				m.refreshSessionSettingsProjection(session)
				current = session.Settings.Effective
			}
			if msg.target == m.sessionSettings() {
				m.ModelName = current.Model
				m.LastSettings = current
			}
			m.closeProjectionPanel(projection, sessionSettingsPanelID)
			m.clearThinkingOverflowLatch(session)
			m.Feedback.NotifyInfo("settings saved")
		}
	case sessionsettings.CancelMsg:
		m.ws.Logger().Info("CONFIG", "session_settings_cancel")
		_, projection := m.sessionProjections.projectionForSettings(msg.Target)
		if msg.Target != nil {
			msg.Target.RestoreSnapshot()
		}
		m.closeProjectionPanel(projection, sessionSettingsPanelID)
	case sessionsettings.ResetMsg:
		session, _ := m.sessionProjections.projectionForSettings(msg.Target)
		if session == nil {
			m.Feedback.NotifyError("Session settings owner is no longer available")
		} else {
			owner, generation := m.ws, m.workspaceSwitchGen.Load()
			op, err := owner.PrepareSessionSettingsReset(session.SessionID)
			if err != nil {
				m.Feedback.NotifyError(err.Error())
			} else {
				cmds = append(cmds, func() tea.Msg {
					return sessionSettingsSaveResultMsg{target: msg.Target, workspace: owner, generation: generation, result: op.Run()}
				})
			}
		}
	case sessionsettings.FetchModelsMsg:
		request := msg
		target := request.Target
		if _, projection := m.sessionProjections.projectionForSettings(target); projection == nil {
			break
		}
		// Resolve the selected account, including its endpoint/protocol overrides.
		// Template defaults must not overwrite the account during discovery.
		fetch := m.ws.PrepareAccountModels(request.ProviderID, request.AccountID, "", "")
		cmds = append(cmds, func() tea.Msg {
			models, err := fetch()
			return sessionSettingsModelsResultMsg{target: target, providerID: request.ProviderID, accountID: request.AccountID, generation: request.Generation, models: models, err: err}
		})
	case sessionSettingsModelsResultMsg:
		_, projection := m.sessionProjections.projectionForSettings(msg.target)
		if projection != nil {
			if msg.err != nil {
				msg.target.ApplyModelsErrorForAccount(msg.providerID, msg.accountID, msg.generation, msg.err.Error())
			} else {
				msg.target.ApplyModelsForAccount(msg.providerID, msg.accountID, msg.generation, msg.models)
			}
			projection.deckHost.MarkLayout()
		}
	case question.ResultMsg:
		if _, ok := m.ws.(shellport.QuestionReceipt); ok {
			cmd, err := m.prepareQuestionReply(msg.Target, msg.Result, false, "")
			if err != nil {
				m.Feedback.NotifyError(err.Error())
			} else {
				cmds = append(cmds, cmd)
			}
		} else {
			m.resolveAgentQuestion(msg.Target, msg.Result, false)
		}
	case question.CancelMsg:
		if _, ok := m.ws.(shellport.QuestionReceipt); ok {
			cmd, err := m.prepareQuestionReply(msg.Target, question.Result{}, true, "")
			if err != nil {
				m.Feedback.NotifyError(err.Error())
			} else {
				cmds = append(cmds, cmd)
			}
		} else {
			m.resolveAgentQuestion(msg.Target, question.Result{}, true)
		}
	case questionReplyMsg:
		m.applyQuestionReply(msg)
	case confirmation.ResultMsg:
		m.resolvePromptConfirmation(msg.Target, msg.Choice)
	case confirmation.CancelMsg:
		m.cancelPromptConfirmation(msg.Target)
	case decision.ResultMsg:
		cmds = append(cmds, m.resolveSessionDecision(msg))
	case filetree.FileSelectedMsg:
		m.ws.Logger().Info("TREE", "file_selected", "path", msg.Path)
		cmd, err := m.openEditorTab(msg.Path)
		if err != nil {
			m.ws.Logger().Error("TREE", "open_editor_error", "error", err.Error())
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.unfocus()
		m.syncRenderFocus()
	case filetree.EnterFilterModeMsg:
		m.ws.Logger().Debug("TREE", "enter_filter_mode")
		m.FocusStack.Clear()
		m.FocusStack.Push(FocusTree)
	case filetree.TreeRefreshDoneMsg:
		if msg.Err != nil {
			m.ws.Logger().Error("TREE", "refresh_error", "error", msg.Err.Error())
			m.Feedback.NotifyError("tree refresh error: " + msg.Err.Error())
		}
		if msg.Err == nil && m.Tree != nil && m.Tree.Model == msg.Model {
			if cmd := msg.Model.RefreshGitStatus(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case filetree.TreeGitChangedMsg:
		if msg.Model != nil && m.Tree != nil && m.Tree.Model == msg.Model {
			if cmd := msg.Model.RefreshGitStatus(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// Index, ref, and HEAD writes (commit, checkout, reset, whether ours or an
		// external tool's) move the branch and tracking counts: arm the header's
		// own reconciliation instead of waiting for the panel to be opened.
		m.requestGitSnapshot(time.Now())
	case filetree.TreeGitRefreshMsg:
		if msg.Model == nil || m.Tree == nil || m.Tree.Model != msg.Model {
			break
		}
		m.treeGitRefreshAt = time.Now()
		if msg.Err != nil {
			m.ws.Logger().Warn("GIT", "tree_status_refresh_failed", "error", msg.Err.Error())
		}
		if cmd := msg.Model.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if m.sidebarScreen != nil {
			m.sidebarScreen.RefreshStatus()
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
		// Revealing the sidebar must not steal launcher focus; Enter on the
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
		return tea.MouseClickMsg{X: mouse.X - offX, Y: mouse.Y - offY, Button: mm.Button, Mod: mm.Mod}
	case tea.MouseMotionMsg:
		mouse := mm.Mouse()
		return tea.MouseMotionMsg{X: mouse.X - offX, Y: mouse.Y - offY, Button: mm.Button, Mod: mm.Mod}
	case tea.MouseReleaseMsg:
		mouse := mm.Mouse()
		return tea.MouseReleaseMsg{X: mouse.X - offX, Y: mouse.Y - offY, Button: mm.Button, Mod: mm.Mod}
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg{X: mm.X - offX, Y: mm.Y - offY, Button: mm.Button, Mod: mm.Mod}
	}
	return msg
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
			return tea.Batch(result.Cmd, m.closePopupWindowByID(fw.ID))
		}
		if result.Action != nil && result.Action.ID == "window.titleaction" {
			// Terminal and surface windows use the title action as their Dock button.
			if id, ok := ui.TerminalIDFromOverlay(ui.OverlayID(fw.ID)); ok {
				m.dockTerminal(id)
				return result.Cmd
			}
			if group, ok := paneGroupFromOverlay(ui.OverlayID(fw.ID)); ok {
				if result.Action.Payload == paneWindowNewTab {
					x, y := fw.Position()
					w, _ := fw.Size()
					rect := ui.Rect{X: x + w - 3 - 2*ui.TitleActionWidth, Y: y, W: ui.TitleActionWidth, H: 1}
					return tea.Batch(result.Cmd, m.openContextMenuState(tea.Mouse{X: rect.X + 1, Y: rect.Y + 1}, m.paneWindowNewTabMenu(group)))
				}
				m.dockPaneWindow(group)
				return result.Cmd
			}
		}
		if result.Action != nil && result.Action.ID == ui.ActionTextPaste {
			target, _ := result.Action.Payload.(ui.ClipboardPasteTarget)
			deliver := nativePasteDelivery(target, func(pasted ui.EventResult) tea.Cmd {
				if m.windowManager == nil || m.windowManager.Window(fw.ID) != fw {
					return nil
				}
				fw.MarkContentDirty()
				return tea.Batch(pasted.Cmd, m.handleHostAction(windowScreenAction(fw, pasted.Action)))
			}, w.Host())
			return tea.Batch(result.Cmd, m.beginNativeClipboardPaste(target, fw.ID, deliver))
		}
		result.Action = windowScreenAction(fw, result.Action)
		cmd := m.handleHostAction(result.Action)
		return tea.Batch(result.Cmd, cmd)
	}
	// A drag that a ComponentWidget overlay's text field captured (find /
	// replace, palette filter, tree prompt) keeps its motion and release in
	// that field until the button is released. Presses and keys keep their
	// overlay-specific owners.
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok {
		switch localMsg.(type) {
		case tea.MouseWheelMsg:
			// Scrollable native popovers (including long pane-scope menus)
			// receive wheel by exact window hit, without pointer capture or
			// changing the focused shell owner.
			if cw.Host() == nil {
				return nil
			}
			result := cw.Host().Handle(localMsg)
			fw.MarkContentDirty()
			return result.Cmd
		case tea.MouseMotionMsg, tea.MouseReleaseMsg:
		default:
			return nil
		}
		if cw.Host() == nil || !cw.Host().HasPointerCapture() {
			return nil
		}
		result := cw.Host().Handle(localMsg)
		fw.MarkContentDirty()
		return result.Cmd
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
	// Release before a dirty-close modal captures its return focus. Otherwise
	// Cancel restores the tab control even though the close operation ended.
	m.releaseTabBarFocus()
	defer m.syncRootFocus()
	if tab.Resource.Kind == window.ResourceLocalDiff {
		m.closeLocalDiff(tab.ID)
		return nil
	}
	if tab.Resource.Kind == window.ResourcePanel {
		// Closing a surface's tab returns it to the dock, hidden.
		m.setPlacement(tab.Resource.ID, placeDock)
		m.Win.CloseTabByID(tab.ID)
		m.handleTabSwitch()
		m.forceFrame()
		return nil
	}
	if tab.Resource.Kind == window.ResourceGitReview {
		m.closeGitReview(tab.ID)
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
		if m.AgentView.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
			m.openLauncher()
			m.LastInteractionTime = time.Now()
			return nil
		}
		return nil
	}

	if m.isEditorTabTitle(tab.Title) {
		path := m.editorPathFromTabTitle(tab.Title)
		// If the tab has unsaved edits, prompt before destroying the buffer.
		// The prompt is the only gate: no mode or missing host may close a
		// dirty document silently. Otherwise close immediately.
		ed := m.EditorState.Editor
		if t, ok := m.EditorState.Tabs[path]; ok {
			ed = t.Editor
		}
		if ed != nil && ed.Modified() {
			m.showDirtyClosePrompt(path)
			return nil
		}
		m.closeEditorTabForce(path)
		return nil
	}

	m.Win.CloseTab(index)
	m.handleTabSwitch()
	if m.AgentView.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
		m.openLauncher()
	}
	return nil
}

// handleTabSwitch re-derives the active editor/agent view from the
// current window tab. Called on every tab change (open, close,
// switch). Both activeEditorView and activeAgentView are *always*
// cleared at the start so that closing the last tab correctly falls
// back to the launcher view; leaving them stale when no tab is
// active would break the renderer's region classification and cause
// Ctrl+Q to attempt a no-op close instead of quitting.
func (m *App) handleTabSwitch() {
	prevEditorPath := m.EditorState.ActiveView
	prevEditor := m.EditorState.Editor
	prevAgentID := m.AgentView.ActiveID()
	defer m.bindMainPaneFocus()
	defer func() {
		if m.referenceOverlay != nil && m.referenceOverlay.agentID != m.AgentView.ActiveID() {
			m.overlayHost.Close(ui.OverlayTextOverlay)
		}
		if m.attachmentOpen != nil && m.attachmentOpen.agentID != m.AgentView.ActiveID() {
			m.cancelAttachmentOpen()
		}
		if prevEditorPath != m.EditorState.ActiveView || prevEditor != m.EditorState.Editor {
			if prevEditor != nil {
				prevEditor.BreakUndoGroup()
			}
			if m.EditorState.Editor != nil {
				m.EditorState.Editor.BreakUndoGroup()
			}
		}
		if prevEditorPath != m.EditorState.ActiveView && m.ws.LSPMgr() != nil {
			m.ws.LSPMgr().SetActiveFile(m.EditorState.ActiveView)
		}
		if prevEditorPath != m.EditorState.ActiveView || prevEditor != m.EditorState.Editor || prevAgentID != m.AgentView.ActiveID() {
			m.closeCompletionPopup()
			m.closeOutlinePopup()
			m.closeReferencesPopup()
		}
	}()
	defer m.openNextAgentPrompt()
	// Always reset before re-deriving. This is the single source of
	// truth for which view is active.
	m.EditorState.ActiveView = ""
	m.AgentView.ClearActive()

	activeTab := m.Win.ActiveTab()
	if activeTab == nil {
		// Closing the last tab returns to launcher. No editor owns the
		// view anymore, so a bound popover cannot stay mounted either.
		m.closeFindReplaceForTab(prevEditorPath, "")
		return
	}
	if activeTab.Resource.Kind == window.ResourceGitReview || activeTab.Resource.Kind == window.ResourceLocalDiff || activeTab.Resource.Kind == window.ResourcePanel {
		m.closeFindReplaceForTab(prevEditorPath, "")
		return
	}

	if m.isAgentTabTitle(activeTab.Title) {
		agentID := m.agentIDFromTabTitle(activeTab.Title)
		m.AgentView.SetActive(agentID)
		m.ActivateSessionByAgent(agentID)
		m.maybeHydrateComposerDraft()
		m.OverlayState.SetYolo(m.ws.YoloForAgent(agentID))
		// Projection caches are source-version owned. Focus changes never make
		// retained message pixels stale; SyncFromWorkspace invalidates only rows
		// whose canonical conversation version changed.
		m.closeFindReplaceForTab(prevEditorPath, "")
		return
	}

	if m.isEditorTabTitle(activeTab.Title) {
		path := m.editorPathFromTabTitle(activeTab.Title)
		// Look up the tab's pre-loaded Editor. We DO NOT call
		// editor.Open() here, because that would reload the file from disk
		// and destroy cursor, scroll, undo, and any unsaved edits.
		// The Editor was created and loaded once in openEditorTab and
		// lives in the editorTabs map for the lifetime of the tab.
		tab, ok := m.EditorState.Tabs[path]
		if !ok {
			// Tab entry is missing (shouldn't normally happen, but be
			// defensive). Re-open to recover.
			if m.ws.Logger() != nil {
				m.ws.Logger().Warn("EDITOR", "tab_missing_on_switch", "path", path)
			}
			_, err := m.openEditorTab(path)
			if err != nil && m.ws.Logger() != nil {
				m.ws.Logger().Error("EDITOR", "reopen_failed", "error", err.Error())
			}
			return
		}
		m.EditorState.ActiveView = path
		m.EditorState.Editor = tab.Editor
		m.closeFindReplaceForTab(prevEditorPath, path)
		if prevEditorPath != path || prevEditor != tab.Editor {
			m.markEditorGitStaleNow()
		}
		return
	}

	// No tab recognized (e.g. launcher): the find/replace popover has no
	// owning editor view anymore, so it cannot stay open.
	m.closeFindReplaceForTab(prevEditorPath, "")
}

func (m *App) openFilePicker() {
	root := m.ws.RootPath()
	if root == "" {
		root, _ = m.workingDir()
	}
	m.openPicker(root, filepickerscreen.ModeFile)
}

func (m *App) openPicker(root string, mode filepickerscreen.Mode) {
	m.paneFilePicker = nil
	if m.FilePicker == nil {
		m.FilePicker = filepickerscreen.New(m.Theme)
	}
	m.invalidatePathSearch(m.FilePicker)
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
		m.Feedback.NotifyError("Failed to add file reference")
		return
	}
	m.Input.InsertReference(ref)
}

func (m *App) workingDir() (string, error) {
	return os.Getwd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(workspace.IdleTickInterval, func(at time.Time) tea.Msg { return types.TickMsg(at) })
}

func streamingTickCmd() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(at time.Time) tea.Msg { return types.TickMsg(at) })
}
