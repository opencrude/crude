package viewmodel

import (
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/screens/shellchat"
	"crude/tui/ui"
	"crude/workspace"
)

// workspaceSwitchState retains only the old projections needed to finish a
// successful switch or restore the old file watcher after validation fails.
type workspaceSwitchState struct {
	tree         *filetree.FileTreeModel
	agentIDs     []string
	workspaceGen uint64
	gitGen       uint64
	editorGitGen uint64
	launcherGen  uint64
}

type workspaceTreeReadyMsg struct {
	generation uint64
	rootPath   string
	model      *filetree.FileTreeModel
}

func sameWorkspaceRoot(current, candidate string) bool {
	if current == "" || candidate == "" {
		return false
	}
	currentAbs, currentErr := filepath.Abs(current)
	candidateAbs, candidateErr := filepath.Abs(candidate)
	if currentErr != nil || candidateErr != nil {
		return filepath.Clean(current) == filepath.Clean(candidate)
	}
	return filepath.Clean(currentAbs) == filepath.Clean(candidateAbs)
}

// beginWorkspaceSwitch establishes the async generation boundary before the
// domain starts tearing down. The old tree watcher is stopped synchronously,
// while git and launcher work already in flight becomes stale immediately.
func (m *App) beginWorkspaceSwitch() workspaceSwitchState {
	state := workspaceSwitchState{
		workspaceGen: m.workspaceSwitchGen.Load(),
		gitGen:       m.gitRefreshGen.Load(),
		editorGitGen: m.editorGitGen.Load(),
		launcherGen:  m.launcherLoadGen.Load(),
	}
	if m.Tree != nil {
		state.tree = m.Tree.Model
	}
	if m.AgentMgr != nil {
		for _, node := range m.AgentMgr.AllNodes() {
			if node != nil && node.ID != "" {
				state.agentIDs = append(state.agentIDs, node.ID)
			}
		}
	}

	m.workspaceSwitchGen.Add(1)
	m.resetLSPRequestContext()
	m.gitRefreshGen.Add(1)
	m.editorGitGen.Add(1)
	m.launcherLoadGen.Add(1)
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
	if state.tree != nil {
		state.tree.SetOnFileChanged(nil)
		state.tree.StopWatcher()
	}
	return state
}

func (state workspaceSwitchState) restore(m *App) {
	if m == nil {
		return
	}
	// Bubble Tea cannot apply background completions while SwitchProject is
	// running synchronously. Restoring these generations lets old-workspace
	// Git/launcher work that was already queued remain valid after validation
	// rejects the candidate project.
	m.workspaceSwitchGen.Store(state.workspaceGen)
	m.resetLSPRequestContext()
	m.gitRefreshGen.Store(state.gitGen)
	m.editorGitGen.Store(state.editorGitGen)
	m.launcherLoadGen.Store(state.launcherGen)
	m.wireLSPDiagnostics()
	if state.tree != nil {
		m.wireAndStartWorkspaceTreeWatcher(state.tree)
	}
}

// finishWorkspaceSwitch releases every project-owned TUI projection before
// constructing replacements. Theme, compositor, root Host, action registry,
// history, and application-global overlays stay alive.
func (m *App) finishWorkspaceSwitch(state workspaceSwitchState) {
	m.discardTerminalUI()
	m.closeWorkspaceBoundOverlays()
	m.closeWorkspaceEditors()

	if m.Win != nil {
		m.Win.CloseAll()
	}
	if m.StatusBar != nil {
		for _, id := range state.agentIDs {
			m.StatusBar.ClearAgentState(id)
		}
		m.StatusBar.SetCurrentAgent("")
		m.StatusBar.SetProjectInsights(editor.ProjectInsights{})
		m.StatusBar.ClearRetry()
		m.StatusBar.SetPhase("")
	}

	if m.sessionProjections != nil {
		m.sessionProjections.items = nil
	}
	if m.subPaneProjections != nil {
		m.subPaneProjections.projections = nil
	}
	m.sessionProjections = newSessionProjectionRegistry(m.Theme)
	m.subPaneProjections = newSubPaneProjectionRegistry(m.Theme)
	m.agentChatScroll = make(map[string]agentMainChatScrollSnapshot)
	m.chatScrollAgent = ""
	m.chatScrollNeedsRestore = false

	if m.chatScreen != nil {
		m.chatScreen.CancelPointerCapture()
		m.chatScreen.SetMessages(nil, shellchat.PaintParams{})
		scroll := m.chatScreen.ScrollModel()
		scroll.SetAutoScroll(true)
		scroll.ScrollTo(0)
	}
	m.Input.SetReferenceStore(nil)
	m.Input.ClearInput()

	m.BufferMgr = editor.NewBufferManager()
	m.EditorState = NewEditorState()
	m.EditorState.Editor = editor.New(m.Theme)
	if m.editorScreen != nil {
		m.editorScreen.Bind(m.EditorState.Editor)
	}

	// A real tree constructs its Git resolver and reads the root directory.
	// Build it off the Bubble Tea goroutine; the placeholder keeps every
	// projection non-nil while that project-bound I/O is pending.
	treeModel := filetree.NewPlaceholderFileTreeModel(m.RootPath)
	m.Tree = NewTreeState(treeModel, m.Theme.Tree.Config.DefaultWidth)
	if m.sidebarScreen != nil {
		m.sidebarScreen.SetModel(treeModel)
	}
	m.beginWorkspaceTreeLoad(m.RootPath)

	if m.LSPMgr != nil {
		m.LSPMgr.SetRuntime(newEditorLSPRuntime())
	}
	m.wireLSPDiagnostics()
	m.syncFileTreeDiagnosticsFromLSP()

	if m.sourceControl != nil {
		m.sourceControl.SetSnapshot(workspace.GitSnapshot{})
		m.sourceControl.ClearCommitMessage()
	}
	if m.activityBar != nil {
		m.activityBar.SetBadge(activityAgents, 0)
		m.activityBar.SetBadge(activityTerminals, 0)
		m.activityBar.SetBadge(activityGit, 0)
	}
	m.refreshActivityList()

	if m.launcherScreen != nil {
		m.launcherScreen.SetData(m.RootPath, nil)
		m.launcherScreen.SyncFocus()
		m.refreshLauncherAsync(m.RootPath)
	}
	m.cachedOutput = ""
}

func (m *App) beginWorkspaceTreeLoad(rootPath string) {
	if m == nil || rootPath == "" {
		return
	}
	generation := m.workspaceSwitchGen.Load()
	m.runDetachedAsyncCmd(func() tea.Msg {
		return workspaceTreeReadyMsg{
			generation: generation,
			rootPath:   rootPath,
			model:      filetree.NewFileTreeModel(rootPath),
		}
	})
}

func (m *App) applyWorkspaceTreeReady(msg workspaceTreeReadyMsg) {
	if m == nil || msg.model == nil ||
		msg.generation != m.workspaceSwitchGen.Load() ||
		!sameWorkspaceRoot(m.RootPath, msg.rootPath) {
		if msg.model != nil {
			msg.model.StopWatcher()
		}
		return
	}
	if m.Tree != nil && m.Tree.Model != nil && m.Tree.Model != msg.model {
		m.Tree.Model.SetOnFileChanged(nil)
		m.Tree.Model.StopWatcher()
	}
	m.Tree = NewTreeState(msg.model, m.Theme.Tree.Config.DefaultWidth)
	if m.sidebarScreen != nil {
		m.sidebarScreen.SetModel(msg.model)
	}
	m.wireAndStartWorkspaceTreeWatcherAsync(msg.model, msg.generation)
	m.syncFileTreeDiagnosticsFromLSP()
}

// wireAndStartWorkspaceTreeWatcherAsync keeps the recursive watcher walk off
// the update goroutine. A generation change during startup stops the newly
// created watcher before it can outlive its project.
func (m *App) wireAndStartWorkspaceTreeWatcherAsync(model *filetree.FileTreeModel, generation uint64) {
	if m == nil || model == nil {
		return
	}
	bus := m.eventBus
	model.SetOnFileChanged(func(filePath string, changeType int) {
		if m.workspaceSwitchGen.Load() != generation || bus == nil {
			return
		}
		bus.Send(workspace.FileWatcherMsg{FilePath: filePath, ChangeType: changeType})
	})
	go func() {
		err := model.StartWatcher()
		if m.workspaceSwitchGen.Load() != generation {
			model.SetOnFileChanged(nil)
			model.StopWatcher()
			return
		}
		if err != nil && m.Logger != nil {
			m.Logger.Warn("FILETREE", "watcher_start_failed", "error", err.Error())
		}
	}()
}

// wireAndStartWorkspaceTreeWatcher is the single startup path used by Init and
// project switching. The captured generation prevents a stopped old model from
// publishing a late callback into the new workspace.
func (m *App) wireAndStartWorkspaceTreeWatcher(model *filetree.FileTreeModel) {
	if m == nil || model == nil {
		return
	}
	generation := m.workspaceSwitchGen.Load()
	bus := m.eventBus
	model.SetOnFileChanged(func(filePath string, changeType int) {
		if m.workspaceSwitchGen.Load() != generation || bus == nil {
			return
		}
		bus.Send(workspace.FileWatcherMsg{FilePath: filePath, ChangeType: changeType})
	})
	if err := model.StartWatcher(); err != nil && m.Logger != nil {
		m.Logger.Warn("FILETREE", "watcher_start_failed", "error", err.Error())
	}
}

// closeWorkspaceEditors closes each editor projection exactly once. The active
// editor is normally also present in Tabs, but tests and transitional states
// may leave a distinct active projection outside the map.
func (m *App) closeWorkspaceEditors() {
	if m == nil || m.EditorState == nil {
		return
	}
	seen := make(map[*editor.Editor]struct{}, len(m.EditorState.Tabs)+1)
	closeEditor := func(ed *editor.Editor) {
		if ed == nil {
			return
		}
		if _, ok := seen[ed]; ok {
			return
		}
		seen[ed] = struct{}{}
		ed.SetOnContentChanged(nil)
		ed.Close()
	}
	for _, tab := range m.EditorState.Tabs {
		if tab != nil {
			closeEditor(tab.Editor)
		}
	}
	closeEditor(m.EditorState.Editor)
}

// closeWorkspaceBoundOverlays closes project/session/editor surfaces while
// preserving application-global Help and Command Palette state.
func (m *App) closeWorkspaceBoundOverlays() {
	search := m.findInFiles
	if m.overlayHost != nil {
		for _, entry := range m.overlayHost.All() {
			if entry == nil {
				continue
			}
			switch entry.ID {
			case ui.OverlayHelp, ui.OverlayCommandPalette:
				continue
			}
			m.stopReveal(string(entry.ID))
			m.overlayHost.Close(entry.ID)
		}
	}
	m.CancelProjectSearch()
	if search != nil && m.findInFiles == search {
		search.Close()
	}
	m.findInFiles = nil
	m.findInFilesRequest = 0
	m.findInFilesGen = 0

	m.FindReplace = nil
	m.LastContextMenu = nil
	m.ContextMenuOpenedAt = time.Time{}
	m.TreePrompt = nil
	m.TreeDeletePending = ""
	m.TreeDeleteChoice = 0
	m.GitDiscardPending = nil
	m.DirtyPromptPath = ""
	m.DirtyPromptChoice = 0
	m.DiagPopupID = ""
	m.sessionPicker = nil
	m.memoryBrowser = nil
	m.LSPPopup = NewLSPPopupState()
	m.OverlayState = NewOverlayState()

	for id := range m.reveals {
		if id != string(ui.OverlayHelp) && id != string(ui.OverlayCommandPalette) {
			delete(m.reveals, id)
		}
	}
}
