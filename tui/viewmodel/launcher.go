package viewmodel

import (
	"context"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/shellport"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/screens/launcher"
	"crude/tui/ui"
	"crude/workspace"
)

// launcherHelpSegments is rendered in the bottom-right status region while the
// launcher is open.
func (m *App) launcherHelpSegments() []ui.HintSegment {
	if m.launcherScreen == nil {
		return nil
	}
	if m.launcherScreen.FilterActive() {
		return []ui.HintSegment{
			ui.TextSeg("type search"),
			ui.ActionSeg("list.navigate"),
			ui.ActionSeg("list.open"),
			ui.ActionSeg("launcher.filter.clear"),
		}
	}
	segs := []ui.HintSegment{
		ui.ActionSeg("list.navigate"),
		ui.ActionSeg("list.open"),
		ui.ActionSeg("launcher.filter"),
		ui.ActionSeg("view.focus.sidebar"),
	}
	if m.actionRegistry != nil && m.actionRegistry.HintFor("palette.open") != "" {
		segs = append(segs, ui.ActionSeg("palette.open"))
	}
	return segs
}

// handleLauncherActivate handles ActivateMsg from the launcher screen.
func (m *App) handleLauncherActivate(msg launcher.ActivateMsg) tea.Cmd {
	if m.launcherScreen != msg.Screen {
		return nil
	}
	switch msg.Kind {
	case launcher.ItemOpenFolder:
		m.openFolderPicker()
		return nil
	case launcher.ItemNewSession:
		return m.launcherNewSession()
	case launcher.ItemWorkspace:
		return m.launcherOpenFolder(msg.WorkspacePath)
	}
	return nil
}

// handleLauncherCommand accepts only deliberate, owner-tagged home actions.
func (m *App) handleLauncherCommand(msg launcher.CommandMsg) tea.Cmd {
	if m.launcherScreen != msg.Screen {
		return nil
	}
	switch msg.Action {
	case "launcher.retry":
		m.refreshLauncherAsync(m.ws.RootPath())
	case "skills.manage":
		return m.openCapabilityManager(activitySkills, "", msg.TargetID)
	case "mcp.manage":
		return m.openCapabilityManager(activityMCP, "", msg.TargetID)
	}
	return nil
}

// launcherNewSession creates a new session from the launcher.
func (m *App) launcherNewSession() tea.Cmd {
	if _, connected := m.ws.(shellport.Replica); connected {
		return m.remoteSessionCommand("")
	}
	if m.ws.Logger() != nil {
		m.ws.Logger().Info("SESSION", "new_session_from_launcher")
	}
	result := m.ws.NewAgentSession()
	if result != nil && result.Error != "" {
		if result.NeedsAccount {
			return m.openAISetup()
		}
		if result.NeedsModel {
			return m.openApplicationMainAgent()
		}
		m.showOperationFailure("Session could not be created", result.Error)
		return nil
	}
	if result != nil {
		m.AddOrchestrator(result.OrchID)
	}
	if err := m.ws.LastRegistryError(); err != nil && m.Feedback != nil {
		m.Feedback.NotifyError("Session created but registry update failed: " + err.Error())
	}
	return nil
}

// launcherOpenFolder switches to a workspace and refreshes the launcher.
func (m *App) launcherOpenFolder(projectDir string) tea.Cmd {
	return m.requestWorkspaceOpen(projectDir, "")
}

// openWorkspaceConfirmed is reached only after an exact shell-owned navigation
// decision; the connected lifecycle retains all existing dirty/draft checks.
func (m *App) openWorkspaceConfirmed(projectDir, resumeID string) tea.Cmd {
	if _, connected := m.ws.(shellport.Connection); connected {
		return m.attachRemoteWorkspace(projectDir, resumeID)
	}
	if m.ws.Logger() != nil {
		m.ws.Logger().Info("WORKSPACE", "open_from_launcher", "path", projectDir)
	}
	if err := m.SwitchProject(projectDir); err != nil {
		m.showOperationFailure("Workspace could not be opened", err.Error())
		return nil
	}
	// SwitchProject already starts the generation-owned async listing. Never
	// run a second synchronous scan on the UI goroutine here.
	if m.Feedback != nil {
		m.Feedback.NotifyInfo("Opened " + filepath.Base(projectDir))
	}
	if resumeID != "" {
		return m.launcherResumeSession(resumeID, nil)
	}
	return nil
}

// launcherResumeSession resumes an existing session from the launcher.
func (m *App) launcherResumeSession(sessionID string, sessionMeta *sessiondb.SessionMeta) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	if sessionMeta != nil && sessionMeta.ProjectPath != "" && !sameWorkspaceRoot(m.ws.RootPath(), sessionMeta.ProjectPath) {
		return m.requestWorkspaceOpen(sessionMeta.ProjectPath, sessionID)
	}
	if _, connected := m.ws.(shellport.Connection); connected {
		return m.remoteSessionCommand(sessionID)
	}
	if m.ws.Logger() != nil {
		m.ws.Logger().Info("SESSION", "resume_selected", "id", sessionID)
	}
	result := m.ws.OpenAgentSession(sessionID)
	if result == nil || result.Error != "" {
		if result != nil && result.NeedsAccount {
			m.showAccountRecovery(true, result.Error)
			return nil
		}
		detail := "Session startup returned no result."
		if result != nil {
			detail = result.Error
		}
		m.showOperationFailure("Session could not be resumed", detail)
		return nil
	}
	m.AddOrchestrator(result.OrchID)
	if err := m.ws.LastRegistryError(); err != nil && m.Feedback != nil {
		m.Feedback.NotifyError("Session resumed but registry update failed: " + err.Error())
	}
	if err := m.ActiveSessionConnectionIssue(); err != nil {
		m.showAccountRecovery(true, err.Error())
	}
	return nil
}

// openLauncher shows the workspace-centric welcome / start screen.
//
// The workspace listing is loaded ASYNCHRONOUSLY (see refreshLauncherAsync):
// the launcher paints immediately with the last-known data (or fixed actions
// only on first open) and the workspace rows arrive in a later
// launcherWorkspacesMsg. The ~200ms DB scan must never block the update
// goroutine; every back-to-launcher (last tab close, editor close, quit)
// used to stall input and rendering for its full duration.
func (m *App) openLauncher() {
	cwd := m.ws.RootPath()
	if cwd == "" {
		cwd = "."
	}
	if m.launcherScreen == nil {
		m.launcherScreen = launcher.New(m.Theme)
		m.launcherScreen.SetFocusHandler(m.rootHost.Focus)
	}
	m.refreshLauncherAsync(cwd)
	if m.capabilitySnapshot.Generation != 0 || m.capabilitySnapshot.Error != "" {
		m.projectLauncherCapabilities(m.capabilitySnapshot)
	}
	m.launcherCapabilityAt = time.Time{}
	m.runDetachedAsyncCmd(m.launcherCapabilitiesCmd(time.Now()))
	m.LastInteractionTime = time.Now()
}

// launcherWorkspacesMsg carries an asynchronously-loaded workspace listing to
// the update goroutine. Gen matches the launcherLoadGen that started the
// load; stale generations are dropped by the handler.
type launcherWorkspacesMsg struct {
	Cwd    string
	Infos  []workspace.WorkspaceInfo
	Gen    uint64
	Screen *launcher.Screen
	Err    error
}

// refreshLauncherAsync loads the workspace list OFF the tea update goroutine
// and delivers the result through the event bus (thread-safe Send; dropped
// gracefully when no program is attached). Generation dedupe: only the
// latest kick may apply, so rapid open/close/open cycles never apply a stale
// listing for a previous directory.
func (m *App) refreshLauncherAsync(cwd string) {
	if m.workspaceListCancel != nil {
		m.workspaceListCancel()
	}
	gen := m.launcherLoadGen.Add(1)
	screen := m.launcherScreen
	m.workspaceListLoading, m.workspaceListError = true, ""
	if screen != nil {
		screen.BeginLoad(cwd)
	}
	if m.activityView == activityWorkspaces {
		m.refreshActivityList()
	}
	if m.eventBus == nil {
		return
	}
	files, remote := m.ws.(shellport.ClientFiles)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.workspaceListCancel = cancel
	m.runDetachedAsyncCmd(func() tea.Msg {
		defer cancel()
		var infos []workspace.WorkspaceInfo
		var err error
		if remote {
			infos, err = files.ListSavedWorkspaces(ctx)
		} else {
			infos, err = workspace.ListWorkspaceInfosContext(ctx)
		}
		if m.launcherLoadGen.Load() != gen {
			return nil
		}
		return launcherWorkspacesMsg{Cwd: cwd, Infos: infos, Gen: gen, Screen: screen, Err: err}
	})
}

// openFolderPicker opens the existing file-picker UI in folder-select mode.
func (m *App) openFolderPicker() {
	start := m.ws.RootPath()
	if start == "" {
		start = "."
	}
	m.openPicker(start, filepickerscreen.ModeFolder)
	// agent_tabs / setMode opens the filepicker floating window.
}
