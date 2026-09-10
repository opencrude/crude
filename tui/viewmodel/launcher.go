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
		ui.ActionSeg("view.focus.sidebar"),
	}
	if m.actionRegistry != nil && m.actionRegistry.HintFor("palette.open") != "" {
		segs = append(segs, ui.ActionSeg("palette.open"))
	}
	return segs
}

// launcherShortcuts returns the key/label rows shown in the left column.
func (m *App) launcherShortcuts() [][2]string {
	if m.actionRegistry == nil {
		return nil
	}
	ids := []ui.ActionID{
		"palette.open",
		"app.sessions",
		"search.project.open",
		"view.toggle.sidebar",
		"view.focus.sidebar",
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
	// The dismiss/search/delete rows keep descriptive labels but source
	// their keys from the registry (launcher.filter.clear/filter/delete)
	// so they can never drift from the active bindings.
	fixed := []struct {
		id    ui.ActionID
		label string
	}{
		{"launcher.filter.clear", "Cancel / dismiss"},
		{"launcher.filter", "Search workspaces"},
		{"launcher.delete", "Delete item"},
	}
	for _, f := range fixed {
		hint := ui.FormatShortcut(m.actionRegistry.HintFor(f.id))
		if hint == "" {
			continue
		}
		out = append(out, [2]string{hint, f.label})
	}
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
		m.AddOrchestrator(result.OrchID)
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
	m.launcherScreen.Refresh(projectDir)
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
	m.AddOrchestrator(result.OrchID)
	if err := m.LastRegistryError(); err != nil && m.StatusBar != nil {
		m.StatusBar.SetError("Session resumed but registry update failed: " + err.Error())
	}
	if m.ActiveSession != nil {
		if msgs := m.ActiveSession.TakeHydratedMessages(); len(msgs) > 0 {
			m.hydrateActiveMessages(ChatMessagesFromViews(msgs))
		}
		if refs := m.ActiveSession.TakeHydratedRefs(); len(refs) > 0 {
			m.SetReferences(refs)
		}
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
		m.shell.SetScreen(shellLauncherSlot, m.launcherScreen)
	}
	m.launcherScreen.SetData(cwd, nil)
	m.refreshLauncherAsync(cwd)
	m.LastInteractionTime = time.Now()
}

// launcherWorkspacesMsg carries an asynchronously-loaded workspace listing to
// the update goroutine. Gen matches the launcherLoadGen that started the
// load; stale generations are dropped by the handler.
type launcherWorkspacesMsg struct {
	Cwd   string
	Infos []workspace.WorkspaceInfo
	Gen   uint64
}

// refreshLauncherAsync loads the workspace list OFF the tea update goroutine
// and delivers the result through the event bus (thread-safe Send; dropped
// gracefully when no program is attached). Generation dedupe: only the
// latest kick may apply, so rapid open/close/open cycles never apply a stale
// listing for a previous directory.
func (m *App) refreshLauncherAsync(cwd string) {
	gen := m.launcherLoadGen.Add(1)
	go func() {
		if m.eventBus == nil {
			return
		}
		infos, _ := workspace.ListWorkspaceInfos()
		if m.launcherLoadGen.Load() != gen {
			return
		}
		m.eventBus.Send(launcherWorkspacesMsg{Cwd: cwd, Infos: infos, Gen: gen})
	}()
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
