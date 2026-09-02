package viewmodel

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shelltree"
	"crude/tui/screens/sourcecontrol"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

func (m *App) buildEditorContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		return nil
	}
	st := &ui.ContextMenuState{Title: "Editor"}

	// Cut/Copy/Paste (no section label: the popup title "Editor"
	// already provides context, and the items speak for themselves)
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		Items: []ui.ContextMenuItem{
			{Label: "Copy", Detail: "Ctrl+C", Disabled: !m.EditorState.Editor.HasSelection(), Action: func() {
				m.copyEditorSelection()
			}},
			{Label: "Cut", Detail: "Ctrl+X", Disabled: !m.EditorState.Editor.HasSelection(), Action: func() {
				m.cutEditorSelection()
			}},
			{Label: "Paste", Detail: "Ctrl+V", Action: func() {
				m.pasteIntoEditor()
			}},
		},
	})

	// Diagnostic- and symbol-aware items.
	// Use the current cursor position to find diagnostics at point.
	// Future: translate mouse X/Y to (line, col) for accuracy.
	pos := struct{ Line, Col int }{Line: m.EditorState.Editor.CursorLine(), Col: m.EditorState.Editor.CursorCol()}
	// Use the diagnostics helper on the editor. We don't expose
	// it, but the editor has a diagnosticsAt function via
	// SetDiagnostics/LSPDiagnostic; we just need the existence
	// check.
	diags := m.EditorState.Editor.DiagnosticsAt(pos.Line, pos.Col)
	hasLSP := m.LSPMgr != nil && m.LSPMgr.ServerReady()

	lspSection := ui.ContextMenuSection{SectionLabel: "Code"}
	if hasLSP {
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: "Go to definition", Detail: "Alt+.", Action: func() {
				_, _ = m.requestDefinitionCmd(), 0
			},
		})
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: "Find references", Detail: "Alt+;", Action: func() {
				_, _ = m.requestReferencesCmd(), 0
			},
		})
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: "Rename symbol", Detail: "F2", Action: func() {
				m.startRename()
			},
		})
	}
	if len(diags) > 0 {
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: fmt.Sprintf("Show %d diagnostic(s)", len(diags)), Action: func() {
				m.showDiagnosticAt(pos.Line, pos.Col)
			},
		})
	}
	if len(lspSection.Items) > 0 {
		st.Sections = append(st.Sections, lspSection)
	}

	blame, hasBlame := m.EditorState.Editor.BlameAtLine(m.EditorState.Editor.CursorLine())
	blameHash := blame.Hash
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Git",
		Items: []ui.ContextMenuItem{
			{Label: "Copy blame commit", Disabled: !hasBlame || blameHash == "" || blame.Uncommitted, Action: func() {
				m.copyToClipboard(blameHash)
			}},
		},
	})

	// View actions.
	viewSection := ui.ContextMenuSection{
		SectionLabel: "View",
		Items: []ui.ContextMenuItem{
			{Label: "Find", Detail: "Ctrl+F", Action: func() {
				m.openFindReplace(false)
			}},
			{Label: "Find & replace", Detail: "Ctrl+H", Action: func() {
				m.openFindReplace(true)
			}},
			{Label: "Document outline", Detail: "Alt+O", Disabled: !hasLSP, Action: func() {
				_, _ = m.requestDocumentSymbolsCmd(), 0
			}},
		},
	}
	st.Sections = append(st.Sections, viewSection)

	return st
}

// buildActivityRegionContextMenu dispatches the left rail and sidebar body to
// the destination that actually owns the clicked cells. The activity region
// is not synonymous with the file tree: Agents, Terminals, and Source Control
// each expose their own item-aware actions.
func (m *App) buildActivityRegionContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	if m.rootHost == nil {
		return nil
	}
	if rect, ok := m.rootHost.ScreenRect(activitybar.ScreenID); ok && rect.Contains(mouse.X, mouse.Y) {
		if activitybar.SettingsAt(mouse.Y-rect.Y, rect.H) {
			return m.buildActivitySettingsContextMenu()
		}
		view := m.activityView
		if hit, hitOK := activitybar.ViewAt(mouse.Y - rect.Y); hitOK {
			view = hit
		}
		return m.buildActivityRailContextMenu(view)
	}
	switch m.activityView {
	case activityAgents:
		return m.buildAgentSidebarContextMenu(mouse)
	case activityTerminals:
		return m.buildTerminalSidebarContextMenu(mouse)
	case activityGit:
		return m.buildSourceControlContextMenu(mouse)
	default:
		return m.buildSidebarContextMenu(mouse)
	}
}

func (m *App) buildActivityRailContextMenu(view activitybar.View) *ui.ContextMenuState {
	title := view.Glyph() + " " + view.Label()
	visible := m.Tree != nil && m.Tree.Visible
	active := m.activityView == view
	showLabel := "Show " + view.Label()
	if active && visible {
		showLabel = "Hide " + view.Label()
	}
	st := &ui.ContextMenuState{
		Title: title,
		Sections: []ui.ContextMenuSection{{
			SectionLabel: "View",
			Items: []ui.ContextMenuItem{{
				Label:  showLabel,
				Detail: activityShortcut(view),
				Action: func() {
					m.startSourceControlMutation(m.toggleActivity(view))
				},
			}},
		}},
	}

	switch view {
	case activitybar.ViewFiles:
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Explorer",
			Items: []ui.ContextMenuItem{
				{Label: "Focus search", Detail: "/", Action: func() {
					m.setActivity(activityFiles, true)
					m.setFocusTree()
					if m.rootHost != nil {
						m.rootHost.Focus(shelltree.FilterID)
					}
				}},
				{Label: "New file", Action: func() {
					if m.Tree != nil && m.Tree.Model != nil {
						m.startNewFileInTree(m.RootPath)
					}
				}},
				{Label: "New folder", Action: func() {
					if m.Tree != nil && m.Tree.Model != nil {
						m.startNewFolderInTree(m.RootPath)
					}
				}},
				{Label: "Refresh", Detail: "Ctrl+R", Action: func() {
					if m.Tree != nil && m.Tree.Model != nil {
						m.startSourceControlMutation(m.Tree.Model.RefreshFromDisk())
					}
				}},
			},
		})
	case activitybar.ViewAgents:
		activeNode := m.AgentMgr.ActiveNode()
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Agents",
			Items: []ui.ContextMenuItem{
				{Label: "New session", Action: func() { m.startSourceControlMutation(m.launcherNewSession()) }},
				{Label: "Manage sessions…", Action: m.openSessionPicker},
				{Label: "Cancel active agent", Disabled: activeNode == nil || !activeNode.IsActive(), Action: func() {
					if node := m.AgentMgr.ActiveNode(); node != nil {
						m.cancelAgentTurn(node.ID)
					}
				}},
			},
		})
	case activitybar.ViewTerminals:
		hasTerminals := m.termMgr() != nil && len(m.termMgr().List()) > 0
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Terminals",
			Items: []ui.ContextMenuItem{
				{Label: "New terminal", Detail: "Ctrl+Shift+`", Action: func() { _ = m.openNewTerminal() }},
				{Label: "Focus terminal", Detail: "Ctrl+`", Disabled: !hasTerminals, Action: func() { _ = m.toggleTerminalFocus() }},
				{Label: "Close all terminals", Disabled: !hasTerminals, Action: m.closeAllTerminalsFromMenu},
			},
		})
	case activitybar.ViewGit:
		hasChanges, hasStaged := m.sourceControlCounts()
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Repository",
			Items: []ui.ContextMenuItem{
				{Label: "Refresh", Detail: "Ctrl+R", Action: func() {
					m.startSourceControlMutation(m.refreshSourceControl())
				}},
				{Label: "Stage all changes", Disabled: !hasChanges, Action: func() {
					m.startSourceControlMutation(m.stageAllGitChanges())
				}},
				{Label: "Unstage all changes", Disabled: !hasStaged, Action: func() {
					m.startSourceControlMutation(m.unstageAllGitChanges())
				}},
			},
		})
	}
	return st
}

func (m *App) buildActivitySettingsContextMenu() *ui.ContextMenuState {
	return &ui.ContextMenuState{
		Title: activitybar.SettingsIcon(false) + " Settings",
		Sections: []ui.ContextMenuSection{{
			SectionLabel: "Settings",
			Items: []ui.ContextMenuItem{
				{Label: "Application Settings", Action: m.openApplicationSettings},
				{Label: "Workspace Settings", Action: m.openWorkspaceSettings},
			},
		}},
	}
}

func activityShortcut(view activitybar.View) string {
	switch view {
	case activitybar.ViewAgents:
		return "Ctrl+Shift+A"
	case activitybar.ViewTerminals:
		return "Ctrl+Shift+U"
	case activitybar.ViewGit:
		return "Ctrl+Shift+G"
	default:
		return "Ctrl+Shift+E"
	}
}

func (m *App) activityItemAtMouse(mouse tea.Mouse) (ui.ListItem, bool) {
	if m.activityList == nil || m.rootHost == nil {
		return ui.ListItem{}, false
	}
	rect, ok := m.rootHost.ScreenRect(activitylist.ScreenID)
	if !ok || !rect.Contains(mouse.X, mouse.Y) {
		return ui.ListItem{}, false
	}
	return m.activityList.ItemAtLocalY(mouse.Y - rect.Y)
}

// buildLauncherContextMenu creates the menu for a right-click on a launcher
// workspace row. The launcher region is only reachable when no editor or
// agents are showing; a right-click anywhere that is not a workspace row
// (hero cards, empty pane, search row) yields no menu.
func (m *App) buildLauncherContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	if m.launcherScreen == nil || m.rootHost == nil {
		return nil
	}
	rect, ok := m.rootHost.ScreenRect(launcher.WorkspaceListID)
	if !ok || rect.Empty() || !rect.Contains(mouse.X, mouse.Y) {
		return nil
	}
	ws := m.launcherScreen.WorkspaceItemAt(mouse.Y - rect.Y)
	if ws == nil {
		return nil
	}
	// Capture by value: the screen rebuilds its item slice freely, so the
	// closures below must not alias the live list.
	entry := *ws
	st := ui.QuickMenu("",
		ui.ContextMenuItem{Label: "Open", Action: func() { m.launcherOpenFolder(entry.Path) }},
		ui.ContextMenuItem{Label: "Copy path", Action: func() { m.copyToClipboard(entry.Path) }},
	)
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		Items: []ui.ContextMenuItem{
			{Label: "Remove from list", Action: func() { m.removeLauncherWorkspace(entry) }},
		},
	})
	return st
}

// removeLauncherWorkspace forgets a workspace from the recents list: the
// persisted workspace record (sessions included) is deleted and the launcher
// listing rebuilds. The project directory itself is never touched.
func (m *App) removeLauncherWorkspace(ws launcher.WorkspaceEntry) {
	target := ws.ID
	if target == "" {
		target = ws.Path
	}
	if target != "" {
		if m.Logger != nil {
			m.Logger.Info("WORKSPACE", "deleted_from_launcher", "id", ws.ID, "path", ws.Path)
		}
		_ = workspace.DeleteWorkspace(target)
	}
	if m.launcherScreen != nil {
		m.launcherScreen.RemoveWorkspace(ws.ID, ws.Path)
	}
	if m.StatusBar != nil {
		m.StatusBar.SetInfo("Removed " + ws.Name + " from recent workspaces")
	}
}

func (m *App) buildAgentSidebarContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	item, hit := m.activityItemAtMouse(mouse)
	agentID := ""
	if hit && strings.HasPrefix(item.ID, "agent:") {
		agentID = strings.TrimPrefix(item.ID, "agent:")
	}
	node := m.AgentMgr.Get(agentID)
	title := activitybar.ViewAgents.Glyph() + " Agents"
	if node != nil {
		label := workspace.AgentDisplayLabel(node)
		if label == "" {
			label = item.Label
		}
		title = activitybar.ViewAgents.Glyph() + " " + label
	}
	st := &ui.ContextMenuState{Title: title}
	if node != nil {
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Agent",
			Items: []ui.ContextMenuItem{
				{Label: "Open agent", Action: func() {
					m.restoreAgent(node.ID)
				}},
				{Label: "Minimize agent", Disabled: node.Hidden, Detail: "keeps running", Action: func() {
					m.minimizeAgent(node.ID)
				}},
				{Label: "Cancel agent", Disabled: !node.IsActive(), Action: func() {
					m.cancelAgentTurn(node.ID)
				}},
				{Label: "Terminate agent", Detail: "stops work", Action: func() {
					m.terminateAgent(node.ID)
				}},
				{Label: "Copy agent name", Action: func() { m.copyToClipboard(item.Label) }},
				{Label: "Copy model", Disabled: node.Model == "", Action: func() { m.copyToClipboard(node.Model) }},
			},
		})
	}
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Sessions",
		Items: []ui.ContextMenuItem{
			{Label: "New session", Action: func() { m.startSourceControlMutation(m.launcherNewSession()) }},
			{Label: "Manage sessions…", Action: m.openSessionPicker},
		},
	})
	return st
}

func (m *App) buildTerminalSidebarContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	item, hit := m.activityItemAtMouse(mouse)
	id := ""
	if hit && strings.HasPrefix(item.ID, "term:") {
		id = strings.TrimPrefix(item.ID, "term:")
	}
	info, found := workspace.TerminalInfo{}, false
	if mgr := m.termMgr(); mgr != nil && id != "" {
		info, found = mgr.Get(id)
	}
	title := "▸ Terminals"
	if found {
		title = "▸ " + item.Label
	}
	st := &ui.ContextMenuState{Title: title}
	if found {
		placementLabel := "Move to panel"
		placementAction := func() { m.dockTerminal(id) }
		if info.Placement == workspace.TerminalDocked {
			placementLabel = "Move to window"
			placementAction = func() { m.promoteTerminal(id) }
		}
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Terminal",
			Items: []ui.ContextMenuItem{
				{Label: "Focus terminal", Action: func() { m.focusTerminal(id) }},
				{Label: placementLabel, Action: placementAction},
				{Label: "Copy working directory", Disabled: info.CWD == "", Action: func() { m.copyToClipboard(info.CWD) }},
				{Label: "Close terminal", Action: func() { m.closeTerminal(id) }},
			},
		})
	}
	hasTerminals := m.termMgr() != nil && len(m.termMgr().List()) > 0
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Panel",
		Items: []ui.ContextMenuItem{
			{Label: "New terminal", Detail: "Ctrl+Shift+`", Action: func() { _ = m.openNewTerminal() }},
			{Label: "Close all terminals", Disabled: !hasTerminals, Action: m.closeAllTerminalsFromMenu},
		},
	})
	return st
}

func (m *App) closeAllTerminalsFromMenu() {
	mgr := m.termMgr()
	if mgr == nil {
		return
	}
	for _, terminal := range mgr.List() {
		m.closeTerminal(terminal.ID)
	}
}

func (m *App) buildSourceControlContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	var item sourcecontrol.Item
	hit := false
	if m.sourceControl != nil && m.rootHost != nil {
		if rect, ok := m.rootHost.ScreenRect(sourcecontrol.ScreenID); ok && rect.Contains(mouse.X, mouse.Y) {
			item, hit = m.sourceControl.ItemAtLocalY(mouse.Y - rect.Y)
		}
	}
	title := "⑂ Source Control"
	st := &ui.ContextMenuState{Title: title}
	if hit {
		switch item.Kind {
		case sourcecontrol.ItemChange:
			change := item.Change
			st.Title = "⑂ " + change.Path
			stageLabel := "Stage changes"
			stageAction := func() { m.startSourceControlMutation(m.stageGitChange(change)) }
			if change.Area == workspace.GitAreaStaged {
				stageLabel = "Unstage changes"
				stageAction = func() { m.startSourceControlMutation(m.unstageGitChange(change)) }
			}
			changeItems := []ui.ContextMenuItem{
				{Label: "Open file", Disabled: change.AbsolutePath == "", Action: func() {
					if change.AbsolutePath != "" {
						_, _ = m.openEditorTab(change.AbsolutePath)
					}
				}},
				{Label: stageLabel, Action: stageAction},
				{Label: "Reveal in Explorer", Disabled: change.AbsolutePath == "", Action: func() {
					m.setActivity(activityFiles, true)
					if m.Tree != nil && m.Tree.Model != nil {
						m.Tree.Model.ExpandToPath(change.AbsolutePath)
					}
				}},
				{Label: "Copy relative path", Action: func() { m.copyToClipboard(change.Path) }},
			}
			if change.Area == workspace.GitAreaWorking {
				changeItems = append(changeItems, ui.ContextMenuItem{
					Label: "Discard changes…", Action: func() { m.startDiscardGitChange(change) },
				})
			}
			st.Sections = append(st.Sections, ui.ContextMenuSection{
				SectionLabel: change.Description,
				Items:        changeItems,
			})
		case sourcecontrol.ItemCommit:
			commit := item.Commit
			st.Title = "● " + commit.ShortHash
			st.Sections = append(st.Sections, ui.ContextMenuSection{
				SectionLabel: "Commit",
				Items: []ui.ContextMenuItem{
					{Label: "Copy commit hash", Action: func() { m.copyToClipboard(commit.Hash) }},
					{Label: "Copy commit message", Action: func() { m.copyToClipboard(commit.Subject) }},
					{Label: "Copy author", Disabled: commit.Author == "", Action: func() { m.copyToClipboard(commit.Author) }},
				},
			})
		case sourcecontrol.ItemBranch:
			branch := item.Branch
			st.Title = "⑂ " + branch.Name
			st.Sections = append(st.Sections, ui.ContextMenuSection{
				SectionLabel: "Branch",
				Items: []ui.ContextMenuItem{
					{Label: "Checkout branch", Disabled: branch.Current, Action: func() {
						m.startSourceControlMutation(m.sourceControlMutation(
							"Switched to "+branch.Name, false,
							func(ctx context.Context, service *workspace.GitService) error {
								return service.Checkout(ctx, branch.Name)
							},
						))
					}},
					{Label: "Copy branch name", Action: func() { m.copyToClipboard(branch.Name) }},
				},
			})
		}
	}
	hasChanges, hasStaged := m.sourceControlCounts()
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Repository",
		Items: []ui.ContextMenuItem{
			{Label: "Refresh", Detail: "Ctrl+R", Action: func() {
				m.startSourceControlMutation(m.refreshSourceControl())
			}},
			{Label: "Stage all changes", Disabled: !hasChanges, Action: func() {
				m.startSourceControlMutation(m.stageAllGitChanges())
			}},
			{Label: "Unstage all changes", Disabled: !hasStaged, Action: func() {
				m.startSourceControlMutation(m.unstageAllGitChanges())
			}},
			{Label: "Copy branch name", Disabled: m.sourceControl == nil || m.sourceControl.Snapshot().Branch == "", Action: func() {
				m.copyToClipboard(m.sourceControl.Snapshot().Branch)
			}},
		},
	})
	return st
}

func (m *App) sourceControlCounts() (hasChanges, hasStaged bool) {
	if m.sourceControl == nil {
		return false, false
	}
	for _, change := range m.sourceControl.Snapshot().Changes {
		if change.Area == workspace.GitAreaStaged {
			hasStaged = true
		} else {
			hasChanges = true
		}
	}
	return
}

// buildSidebarContextMenu creates the menu for right-click on a file
// tree item.
func (m *App) buildSidebarContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	if m.Tree == nil || m.Tree.Model == nil {
		return nil
	}

	// Get the path under the click.
	path := m.pathUnderMouse(mouse)
	isDir := path != "" && m.Tree.Model.IsDir(path)
	title := "▤ Explorer"
	if path != "" {
		if node := m.Tree.Model.NodeByPath(path); node != nil {
			if isDir {
				title = "▤ " + node.Name
			} else {
				title = "· " + node.Name
			}
		}
	}
	st := &ui.ContextMenuState{Title: title}

	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Actions",
		Items: []ui.ContextMenuItem{
			{Label: "Open", Disabled: path == "" || isDir, Action: func() {
				if path != "" {
					_, _ = m.openFileFromTree(path)
				}
			}},
			{Label: "Expand / collapse", Disabled: path == "" || !isDir, Action: func() {
				if path != "" && isDir {
					m.Tree.Model.ToggleExpand(path)
				}
			}},
			{Label: "Reveal in tree", Disabled: path == "", Action: func() {
				if path != "" {
					m.Tree.Model.ExpandToPath(path)
				}
			}},
		},
	})

	createSection := ui.ContextMenuSection{SectionLabel: "Create"}
	if isDir {
		createSection.Items = append(createSection.Items,
			ui.ContextMenuItem{Label: "New file", Action: func() {
				m.startNewFileInTree(path)
			}},
			ui.ContextMenuItem{Label: "New folder", Action: func() {
				m.startNewFolderInTree(path)
			}},
		)
	}
	if path == "" {
		createSection.Items = append(createSection.Items,
			ui.ContextMenuItem{Label: "New file", Action: func() {
				m.startNewFileInTree(m.RootPath)
			}},
			ui.ContextMenuItem{Label: "New folder", Action: func() {
				m.startNewFolderInTree(m.RootPath)
			}},
		)
	}
	if path != "" {
		createSection.Items = append(createSection.Items,
			ui.ContextMenuItem{Label: "New file (sibling)", Action: func() {
				m.startNewFileInTree(m.Tree.Model.ParentDir(path))
			}},
			ui.ContextMenuItem{Label: "New folder (sibling)", Action: func() {
				m.startNewFolderInTree(m.Tree.Model.ParentDir(path))
			}},
		)
	}
	if len(createSection.Items) > 0 {
		st.Sections = append(st.Sections, createSection)
	}

	if path != "" {
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Modify",
			Items: []ui.ContextMenuItem{
				{Label: "Rename", Action: func() {
					m.startRenameInTree(path)
				}},
				{Label: "Delete", Action: func() {
					m.startDeleteInTree(path)
				}},
				{Label: "Copy path", Action: func() {
					m.copyToClipboard(path)
				}},
			},
		})
	}
	if path != "" && m.HistoryService != nil {
		target := path
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Local History",
			Items: []ui.ContextMenuItem{
				{Label: "Show history for this path", Action: func() { m.runDetachedAsyncCmd(m.openLocalHistoryForPath(target)) }},
				{Label: "Open workspace history", Action: func() { m.runDetachedAsyncCmd(m.openLocalHistory()) }},
			},
		})
	}

	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Explorer",
		Items: []ui.ContextMenuItem{
			{Label: "Focus search", Detail: "/", Action: func() {
				m.setFocusTree()
				if m.rootHost != nil {
					m.rootHost.Focus(shelltree.FilterID)
				}
			}},
			{Label: "Refresh", Detail: "Ctrl+R", Action: func() {
				m.startSourceControlMutation(m.Tree.Model.RefreshFromDisk())
			}},
			{Label: "Collapse folders", Action: m.Tree.Model.CollapseAll},
			{Label: "Show hidden files", Detail: "Ctrl+.", Action: func() {
				m.Tree.Model.SetShowHidden(!m.Tree.Model.ShowHidden())
			}},
		},
	})

	return st
}

// buildTabBarContextMenu creates the menu for right-click on a tab.
func (m *App) buildTabBarContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	if m.Win == nil || m.Win.TabCount() == 0 {
		return nil
	}
	hitIndex, _ := m.tabBarHitTest(mouse.X)
	if hitIndex < 0 || hitIndex >= m.Win.TabCount() {
		return nil
	}
	hit := window.TabHitResult{TabIndex: hitIndex}
	tabs := m.Win.Tabs()
	tab := tabs[hit.TabIndex]

	st := &ui.ContextMenuState{Title: "Tab"}

	// Get tab-specific info.
	if m.isEditorTabTitle(tab.Title) {
		path := m.editorPathFromTabTitle(tab.Title)
		closeDetail := "Ctrl+W · Esc"
		if m.actionRegistry != nil {
			var hints []string
			for _, id := range []ui.ActionID{"file.close", "editor.close"} {
				if hint := ui.FormatShortcut(m.actionRegistry.HintFor(id)); hint != "" {
					hints = append(hints, hint)
				}
			}
			if len(hints) > 0 {
				closeDetail = strings.Join(hints, " · ")
			}
		}
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Tab",
			Items: []ui.ContextMenuItem{
				{Label: "Close tab", Detail: closeDetail, Action: func() {
					m.EditorState.ActiveView = path
					_ = m.handleAgentTabClose(hit.TabIndex)
				}},
				{Label: "Close others", Action: func() {
					m.closeAllEditorTabsExcept(path)
				}},
				{Label: "Close to the right", Action: func() {
					m.closeEditorTabsToRightOf(hit.TabIndex)
				}},
				{Label: "Copy path", Action: func() {
					m.copyToClipboard(path)
				}},
				{Label: "Reveal in tree", Action: func() {
					if m.Tree.Model != nil {
						m.Tree.Model.ExpandToPath(path)
					}
				}},
			},
		})
	} else if m.isAgentTabTitle(tab.Title) {
		agentID := m.agentIDFromTabTitle(tab.Title)
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Tab",
			Items: []ui.ContextMenuItem{
				{Label: "Minimize", Detail: "keeps running", Action: func() {
					_ = m.handleAgentTabClose(hit.TabIndex)
				}},
				{Label: "Cancel agent", Action: func() {
					m.cancelAgentTurn(agentID)
				}},
				{Label: "Terminate agent", Detail: "stops work", Action: func() {
					m.terminateAgent(agentID)
				}},
			},
		})
	}

	return st
}

// buildInputContextMenu creates the menu for the chat input.
func (m *App) buildInputContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	// Right-click on an attached reference chip gets attachment management
	// instead of the clipboard menu — the block under the pointer is the
	// object of interest.
	if rr, ok := m.refRangeAtMouse(mouse); ok {
		if st := m.buildAttachmentContextMenu(rr); st != nil {
			return st
		}
	}
	st := &ui.ContextMenuState{Title: "Input"}
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Clipboard",
		Items: []ui.ContextMenuItem{
			{Label: "Paste", Detail: "Ctrl+V", Action: func() {
				m.pasteIntoInput()
			}},
			{Label: "Copy input", Action: func() {
				m.copyToClipboard(m.Input.Value())
			}},
			{Label: "Clear input", Action: func() {
				m.Input.ClearInput()
			}},
		},
	})
	return st
}

// refRangeAtMouse maps a screen-space mouse point into the composer's flat
// rune coordinates and reports the reference-chip range under it, if any.
func (m *App) refRangeAtMouse(mouse tea.Mouse) (input.RefRange, bool) {
	if m.inputRect.Empty() || m.ActiveSession == nil {
		return input.RefRange{}, false
	}
	localX := mouse.X - m.inputRect.X
	localY := mouse.Y - m.inputRect.Y
	pos, ok := m.Input.MouseToInputPos(localX, localY)
	if !ok {
		return input.RefRange{}, false
	}
	for _, rr := range m.Input.RefRanges() {
		if pos >= rr.Start && pos < rr.End {
			return rr, true
		}
	}
	return input.RefRange{}, false
}

// buildAttachmentContextMenu is the per-chip menu behind right-click on a
// pill: preview the content, relabel it, remove it from the draft, or open
// file-backed attachments in the editor.
func (m *App) buildAttachmentContextMenu(rr input.RefRange) *ui.ContextMenuState {
	if m.RefStore() == nil {
		return nil
	}
	ref, ok := m.RefStore().GetReference(rr.ID)
	if !ok || !ref.Active() {
		return nil
	}
	title := strings.TrimSuffix(strings.TrimPrefix(ref.DisplayName(), "["), "]")
	st := &ui.ContextMenuState{Title: title}
	items := []ui.ContextMenuItem{
		{Label: "Preview", Detail: "Space", Action: func() {
			m.openReferenceOverlay(rr.ID)
		}},
		{Label: "Rename…", Action: func() {
			m.startAttachmentRename(rr.ID, title)
		}},
		{Label: "Remove", Action: func() {
			m.removeAttachment(rr)
		}},
	}
	if ref.FilePath != "" {
		path := ref.FilePath
		items = append(items, ui.ContextMenuItem{Label: "Open in editor", Action: func() {
			if _, err := m.openEditorTab(path); err != nil {
				m.StatusBar.SetError("open attachment: " + err.Error())
			}
		}})
	}
	st.Sections = append(st.Sections, ui.ContextMenuSection{Items: items})
	return st
}

// removeAttachment strips a reference token from the draft and soft-deletes
// its backing reference. The caret stays where the removal happened.
func (m *App) removeAttachment(rr input.RefRange) {
	runes := []rune(m.Input.Value())
	if rr.Start < 0 || rr.End > len(runes) || rr.Start >= rr.End {
		return
	}
	cursor := m.Input.CursorFlat()
	next := string(runes[:rr.Start]) + string(runes[rr.End:])
	m.Input.SetInput(next)
	if cursor > rr.Start {
		if cursor -= rr.End - rr.Start; cursor < rr.Start {
			cursor = rr.Start
		}
	}
	m.Input.SetCursorFlat(cursor)
	if m.ActiveSession != nil && m.ActiveSession.RefStore != nil {
		_ = m.ActiveSession.RefStore.DeleteReference(rr.ID)
	}
	if m.StatusBar != nil {
		m.StatusBar.SetInfo("Attachment removed")
	}
}

// startAttachmentRename opens the shared text prompt to relabel one chip.
// The chip's draft spans are captured BEFORE the store rename — once the
// store knows only the new label, the old tokens no longer resolve by scan.
func (m *App) startAttachmentRename(refID, current string) {
	if m.RefStore() == nil {
		return
	}
	spans := m.refRangesFor(refID)
	m.openTreeTextPrompt(ui.TextPromptRename, "Rename attachment",
		"label", "", current, func(value string) error {
			final, ok := m.RefStore().RenameReference(refID, value)
			if !ok {
				return fmt.Errorf("cannot rename this attachment")
			}
			m.replaceRefSpans(spans, final)
			if m.StatusBar != nil {
				m.StatusBar.SetInfo("Renamed to " + strings.TrimSuffix(strings.TrimPrefix(final, "["), "]"))
			}
			return nil
		})
}

func (m *App) refRangesFor(refID string) []input.RefRange {
	var out []input.RefRange
	for _, rr := range m.Input.RefRanges() {
		if rr.ID == refID {
			out = append(out, rr)
		}
	}
	return out
}

// replaceRefSpans swaps every captured chip span for newLabel. Spans are
// applied highest-Start-first so earlier offsets stay valid as the text
// shrinks or grows.
func (m *App) replaceRefSpans(spans []input.RefRange, newLabel string) {
	valid := make([]input.RefRange, 0, len(spans))
	runes := []rune(m.Input.Value())
	for _, rr := range spans {
		if rr.Start >= 0 && rr.End <= len(runes) && rr.Start < rr.End {
			valid = append(valid, rr)
		}
	}
	if len(valid) == 0 {
		return
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].Start > valid[j].Start })
	leftmost := valid[len(valid)-1].Start
	for _, rr := range valid {
		runes = append(runes[:rr.Start], append([]rune(newLabel), runes[rr.End:]...)...)
	}
	m.Input.SetInput(string(runes))
	caret := leftmost + len([]rune(newLabel))
	if caret > len(runes) {
		caret = len(runes)
	}
	m.Input.SetCursorFlat(caret)
}

// buildChatContextMenu creates the menu for right-click in the
// message list. (For now: clipboard + a copy-all option.)
func (m *App) buildChatContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	st := &ui.ContextMenuState{Title: "Chat"}
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		Items: []ui.ContextMenuItem{
			{Label: "Copy selection", Action: func() {
				if m.chatScreen != nil {
					text := m.chatScreen.SelectedText()
					if text != "" {
						m.copyToClipboard(text)
					}
				}
			}},
			{Label: "Copy last assistant", Action: func() {
				ml := m.activeMsgList()
				if ml != nil {
					text := ml.LastAssistantText()
					if text != "" {
						m.copyToClipboard(text)
					}
				}
			}},
			{Label: "Copy last code block", Action: func() {
				ml := m.activeMsgList()
				if ml == nil {
					return
				}
				raw := ml.LastAssistantText()
				_, code, ok := rendering.ExtractLastCodeBlock(raw)
				if !ok {
					m.StatusBar.SetInfo("No code block in last assistant message")
					return
				}
				m.copyToClipboard(code)
			}},
		},
	})
	if m.chatScreen != nil && m.rootHost != nil && m.ActiveSession != nil && m.HistoryService != nil {
		if rect, ok := m.rootHost.ScreenRect(shellchat.AreaID); ok {
			if row, ok := m.chatScreen.MessageAtLocalY(mouse.Y - rect.Y); ok {
				role := ""
				if node := m.AgentMgr.ActiveNode(); node != nil && node.State != nil && node.State.Messages != nil {
					for _, msg := range node.State.Messages.Messages() {
						if msg.InternalID == row.ID {
							role = msg.Role
							break
						}
					}
				}
				if role == workspace.MessageRoleUser {
					sessionID, messageID := m.ActiveSession.SessionID, row.ID
					active := m.AgentMgr.ActiveID() != "" && m.AgentTurnActive(m.AgentMgr.ActiveID())
					open := func() { cmd := m.openLocalHistoryForMessage(sessionID, messageID); m.runDetachedAsyncCmd(cmd) }
					st.Sections = append(st.Sections, ui.ContextMenuSection{SectionLabel: "Local History", Items: []ui.ContextMenuItem{
						{Label: "Preview changes from this message", Disabled: active, Detail: map[bool]string{true: "turn active", false: ""}[active], Action: open},
						{Label: "Undo changes from this message", Disabled: active, Detail: "opens preview", Action: open},
						{Label: "Open workspace history", Action: func() { m.runDetachedAsyncCmd(m.openLocalHistory()) }},
					}})
				}
			}
		}
	}
	return st
}

// buildStatusBarContextMenu creates the menu for right-click on the
// bottom status bar. Items surface LSP controls, status-line copy,
// and a quick "go to first diagnostic" action.
func (m *App) buildStatusBarContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	st := &ui.ContextMenuState{Title: "Status Bar"}

	// LSP section. Always shown when an workspace.LSPManager exists; items
	// disable themselves when not applicable.
	if m.LSPMgr != nil {
		var lspItems []ui.ContextMenuItem

		isConfigured := m.LSPMgr.IsConfigured()
		serverReady := m.LSPMgr.ServerReady()
		ins := m.LSPMgr.ProjectInsights()
		hasErrors := ins.Errors > 0

		lspItems = append(lspItems, ui.ContextMenuItem{
			Label:    "Restart LSP",
			Detail:   "kill+relaunch",
			Disabled: !isConfigured,
			Action: func() {
				m.StopLSP()
				m.startLSPForWorkspace()
				m.StatusBar.SetInfo("LSP restarting…")
			},
		})

		if serverReady {
			lspItems = append(lspItems, ui.ContextMenuItem{
				Label:  "Stop LSP",
				Detail: "free port",
				Action: func() {
					m.StopLSP()
					m.StatusBar.SetInfo("LSP stopped")
				},
			})
		} else if isConfigured {
			// Server was configured but not connected (e.g. failed
			// or still starting). Offer to start it.
			lspItems = append(lspItems, ui.ContextMenuItem{
				Label: "Start LSP",
				Action: func() {
					m.startLSPForWorkspace()
					m.StatusBar.SetInfo("LSP starting…")
				},
			})
		}

		if hasErrors {
			lspItems = append(lspItems, ui.ContextMenuItem{
				Label:  "Goto first error",
				Detail: "F8",
				Action: func() {
					m.gotoFirstDiagnostic()
				},
			})
		}

		if ins.FilesTracked > 0 {
			summary := fmt.Sprintf("%s · ✕%d ⚠%d ℹ%d · %d files · %d symbols",
				ins.ServerName, ins.Errors, ins.Warnings,
				ins.Info+ins.Hints, ins.FilesAffected, ins.SymbolsCount)
			lspItems = append(lspItems, ui.ContextMenuItem{
				Label: "Copy diagnostics summary",
				Action: func() {
					m.copyToClipboard(summary)
				},
			})
		}

		if len(lspItems) > 0 {
			st.Sections = append(st.Sections, ui.ContextMenuSection{
				SectionLabel: "LSP",
				Items:        lspItems,
			})
		}
	}

	// Status section. Always available.
	statusItems := []ui.ContextMenuItem{
		{
			Label: "Copy status line",
			Action: func() {
				if m.StatusBar != nil {
					m.copyToClipboard(m.StatusBar.View())
				}
			},
		},
	}
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Status",
		Items:        statusItems,
	})

	return st
}

// closeContextMenu removes the open context menu, if any.
func (m *App) closeContextMenu() {
	m.overlayHost.Close(ui.OverlayContextMenu)
}

// handleContextMenuKey routes keypresses to the context menu. Up/
// Down navigate items, Enter activates, Esc dismisses.
func (m *App) handleContextMenuKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window("context-menu")
	if fw == nil {
		return nil
	}
	st := m.LastContextMenu
	if st == nil {
		m.closeContextMenu()
		return nil
	}
	switch msg.Code {
	case tea.KeyEsc:
		m.closeContextMenu()
		return nil
	case tea.KeyEnter:
		st.Activate()
		m.closeContextMenu()
		return nil
	case tea.KeyDown:
		st.Next()
		m.refreshContextMenu()
		return nil
	case tea.KeyUp:
		st.Prev()
		m.refreshContextMenu()
		return nil
	}
	return nil
}

// refreshContextMenu re-renders the context menu popup after the
// cursor moved (WindowFrame + ui.List).
func (m *App) refreshContextMenu() {
	if m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window("context-menu")
	if fw == nil || m.LastContextMenu == nil {
		return
	}
	// Prefer mutating selection on the existing frame when possible.
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil {
		ui.ApplyContextMenuCursor(cw, m.LastContextMenu)
		fw.MarkContentDirty()
		return
	}
	frame := ui.BuildContextMenuFrame(m.Theme, m.LastContextMenu)
	w, h := frame.Measure(60, 40)
	if cw := ui.ContextMenuPopupWidth(m.LastContextMenu); cw > 0 {
		w = cw
	}
	if ch := ui.ContextMenuPopupHeight(m.LastContextMenu); ch > 0 {
		h = ch
	}
	fw.SetSize(w, h)
	fw.SetContent(frame)
	fw.MarkContentDirty()
}

func (m *App) showContextMenuAt(x, y int) {
	msg := tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight}
	_, _ = m.dispatchMouseRightClick(msg)
}
