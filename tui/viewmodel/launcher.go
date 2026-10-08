package viewmodel

import (
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
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
		m.refreshLauncherAsync(m.RootPath)
	case "skills.manage":
		return m.openCapabilityManager(activitySkills, "", msg.TargetID)
	case "mcp.manage":
		return m.openCapabilityManager(activityMCP, "", msg.TargetID)
	}
	return nil
}

// launcherNewSession creates a new session from the launcher.
func (m *App) launcherNewSession() tea.Cmd {
	if m.Logger != nil {
		m.Logger.Info("SESSION", "new_session_from_launcher")
	}
	result := m.NewSessionFromLauncher()
	if result != nil && result.Error != "" {
		if result.NeedsAccount {
			m.showAccountRecovery(false, result.Error)
			return nil
		}
		m.showOperationFailure("Session could not be created", result.Error)
		return nil
	}
	if result != nil {
		m.AddOrchestrator(result.OrchID)
	}
	if err := m.LastRegistryError(); err != nil && m.Feedback != nil {
		m.Feedback.NotifyError("Session created but registry update failed: " + err.Error())
	}
	return nil
}

// launcherOpenFolder switches to a workspace and refreshes the launcher.
func (m *App) launcherOpenFolder(projectDir string) tea.Cmd {
	if m.Logger != nil {
		m.Logger.Info("WORKSPACE", "open_from_launcher", "path", projectDir)
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
	return nil
}

// launcherResumeSession resumes an existing session from the launcher.
func (m *App) launcherResumeSession(sessionID string, sessionMeta *sessiondb.SessionMeta) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	// Switch project if the session belongs to another folder.
	if sessionMeta != nil && sessionMeta.ProjectPath != "" {
		abs, _ := filepath.Abs(sessionMeta.ProjectPath)
		cur, _ := filepath.Abs(m.RootPath)
		if abs != "" && abs != cur {
			if err := m.SwitchProject(abs); err != nil {
				m.showOperationFailure("Session workspace could not be opened", err.Error())
				return nil
			}
		}
	}
	if m.Logger != nil {
		m.Logger.Info("SESSION", "resume_selected", "id", sessionID)
	}
	result := m.ResumeSessionFromLauncher(sessionID)
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
	if err := m.LastRegistryError(); err != nil && m.Feedback != nil {
		m.Feedback.NotifyError("Session resumed but registry update failed: " + err.Error())
	}
	if m.ActiveSession != nil {
		if msgs := m.ActiveSession.TakeHydratedMessages(); len(msgs) > 0 {
			m.hydrateActiveMessages(ChatMessagesFromViews(msgs))
		}
		if refs := m.ActiveSession.TakeHydratedRefs(); len(refs) > 0 {
			m.SetReferences(refs)
		}
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
// goroutine — every back-to-launcher (last tab close, editor close, quit)
// used to stall input and rendering for its full duration.
func (m *App) openLauncher() {
	cwd := m.RootPath
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
	gen := m.launcherLoadGen.Add(1)
	screen := m.launcherScreen
	if screen == nil {
		return
	}
	screen.BeginLoad(cwd)
	if m.eventBus == nil {
		return
	}
	m.runDetachedAsyncCmd(func() tea.Msg {
		infos, err := workspace.ListWorkspaceInfos()
		if m.launcherLoadGen.Load() != gen {
			return nil
		}
		return launcherWorkspacesMsg{Cwd: cwd, Infos: infos, Gen: gen, Screen: screen, Err: err}
	})
}

// openFolderPicker opens the existing file-picker UI in folder-select mode.
func (m *App) openFolderPicker() {
	start := m.RootPath
	if start == "" {
		start = "."
	}
	m.openPicker(start, filepickerscreen.ModeFolder)
	// agent_tabs / setMode opens the filepicker floating window.
}
