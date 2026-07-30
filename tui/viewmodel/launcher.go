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
		ui.ActionSeg("launcher.delete"),
	}
	if m.actionRegistry != nil && m.actionRegistry.HintFor("palette.open") != "" {
		segs = append(segs, ui.ActionSeg("palette.open"))
	}
	return segs
}

// launcherHelpView is flattened launcher help for tests.
func (m *App) launcherHelpView() string {
	h := ui.NewHint("launcher-help", m.actionRegistry, nil)
	h.SetSegments(m.launcherHelpSegments())
	return h.PlainText()
}

// launcherShortcuts returns the key/label rows shown in the left column.
func (m *App) launcherShortcuts() [][2]string {
	if m.actionRegistry == nil {
		return nil
	}
	ids := []ui.ActionID{
		"palette.open",
		"search.project.open",
		"view.toggle.sidebar",
		"settings.open",
		"file.attach",
		"composer.toggle",
		"app.quit",
	}
	out := make([][2]string, 0, len(ids)+3)
	for _, id := range ids {
		a := m.actionRegistry.Get(id)
		if a == nil {
			continue
		}
		hint := m.actionRegistry.HintFor(id)
		if hint == "" && len(a.Bindings) > 0 {
			hint = a.Bindings[0].Key
		}
		if hint == "" {
			continue
		}
		out = append(out, [2]string{ui.FormatShortcut(hint), a.Label})
	}
	out = append(out,
		[2]string{"Esc", "Cancel / dismiss"},
		[2]string{"/", "Search workspaces"},
		[2]string{"d", "Delete item"},
	)
	return out
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
	case launcher.ItemSession:
		return m.launcherResumeSession(msg.SessionID, msg.Session)
	}
	return nil
}

// handleLauncherDelete performs the domain delete for a launcher deletion.
func (m *App) handleLauncherDelete(msg launcher.DeleteMsg) tea.Cmd {
	if m.launcherScreen != msg.Screen {
		return nil
	}
	switch msg.Kind {
	case launcher.ItemWorkspace:
		if msg.WorkspaceID != "" {
			if m.Logger != nil {
				m.Logger.Info("WORKSPACE", "deleted_from_launcher", "id", msg.WorkspaceID)
			}
			_ = workspace.DeleteWorkspace(msg.WorkspaceID)
		}
	case launcher.ItemSession:
		if msg.SessionID != "" {
			if m.Logger != nil {
				m.Logger.Info("SESSION", "deleted_from_launcher", "id", msg.SessionID)
			}
			m.DeleteSession(msg.SessionID)
		}
	}
	return nil
}

// launcherNewSession creates a new session from the launcher.
func (m *App) launcherNewSession() tea.Cmd {
	m.startLSPForWorkspace()
	if m.Logger != nil {
		m.Logger.Info("SESSION", "new_session_from_launcher")
	}
	result := m.NewSessionFromLauncher()
	if result != nil {
		m.AddOrchestrator(result.OrchID, result.InputCh, result.BtwCh, result.CancelCh, result.BlockResolveCh)
	}
	if err := m.LastRegistryError(); err != nil && m.StatusBar != nil {
		m.StatusBar.SetError("Session created but registry update failed: " + err.Error())
	}
	return nil
}

// launcherOpenFolder switches to a workspace and refreshes the launcher.
func (m *App) launcherOpenFolder(projectDir string) tea.Cmd {
	if m.Logger != nil {
		m.Logger.Info("WORKSPACE", "open_from_launcher", "path", projectDir)
	}
	if err := m.SwitchProject(projectDir); err != nil {
		if m.StatusBar != nil {
			m.StatusBar.SetError("Failed to open workspace: " + err.Error())
		}
		return nil
	}
	// Stay on the single-phase launcher; refresh lists for the new project.
	m.launcherScreen.Refresh(projectDir, m.ListSessions())
	if m.StatusBar != nil {
		m.StatusBar.SetInfo("Opened " + filepath.Base(projectDir))
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
				if m.StatusBar != nil {
					m.StatusBar.SetError("Failed to open session project: " + err.Error())
				}
				return nil
			}
		}
	}
	if m.Logger != nil {
		m.Logger.Info("SESSION", "resume_selected", "id", sessionID)
	}
	result := m.ResumeSessionFromLauncher(sessionID)
	if result == nil {
		if m.StatusBar != nil {
			m.StatusBar.SetError("Failed to resume session")
		}
		return nil
	}
	m.startLSPForWorkspace()
	m.AddOrchestrator(result.OrchID, result.InputCh, result.BtwCh, result.CancelCh, result.BlockResolveCh)
	if err := m.LastRegistryError(); err != nil && m.StatusBar != nil {
		m.StatusBar.SetError("Session resumed but registry update failed: " + err.Error())
	}
	if m.ActiveSession != nil {
		if msgs := m.ActiveSession.TakeHydratedMessages(); len(msgs) > 0 {
			if m.AgentMgr.ActiveID() != "" {
				for _, hm := range ChatMessagesFromViews(msgs) {
					m.appendActiveMessage(hm)
				}
			}
		}
		if refs := m.ActiveSession.TakeHydratedRefs(); len(refs) > 0 {
			m.SetReferences(refs)
		}
	}
	return nil
}

// openLauncher shows the unified welcome launcher (sessions + workspaces).
func (m *App) openLauncher() {
	cwd := m.RootPath
	if cwd == "" {
		cwd = "."
	}
	if m.launcherScreen == nil {
		m.launcherScreen = launcher.New(m.Theme)
		m.launcherHost = ui.NewHost(m.Theme, m.launcherScreen)
		m.launcherScreen.SetFocusHandler(m.launcherHost.Focus)
	}
	m.launcherScreen.Refresh(cwd, m.ListSessions())
	m.LastInteractionTime = time.Now()
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

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
