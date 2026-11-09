package viewmodel

import (
	"crude/tui/screens/gitdiff"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shelltree"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/types"
	"crude/workspace"
)

func (m *App) buildEditorContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		return nil
	}
	ed := m.EditorState.Editor
	diagnosticPosition := editor.Position{Line: ed.CursorLine(), Col: ed.CursorCol()}
	if m.rootHost != nil {
		if rect, ok := m.screenRectOf(m.editorScreen.ID()); ok && rect.Contains(mouse.X, mouse.Y) {
			if pos, valid := ed.PositionAt(mouse.X-rect.X-ed.PadL, mouse.Y-rect.Y); valid {
				diagnosticPosition = pos
				if !ed.HasSelection() {
					ed.SetCursorPosition(pos.Line, pos.Col)
				}
			}
		}
	}
	st := &ui.ContextMenuState{Title: "Editor"}

	// Cut/Copy/Paste (no section label: the popup title "Editor"
	// already provides context, and the items speak for themselves)
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		Items: []ui.ContextMenuItem{
			{Label: "File encoding", Disabled: ed.ReadOnly(), Action: func() {
				m.runDetachedAsyncCmd(m.openEncodingMenu(ed))
			}},
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

	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Edit",
		Items: []ui.ContextMenuItem{
			{Label: "Format document", Detail: "Shift+Alt+F", Disabled: ed.ReadOnly(), Action: func() {
				m.runDetachedAsyncCmd(m.requestActiveEditorFormat())
			}},
			{Label: "Format project", Disabled: ed.ReadOnly(), Action: func() {
				m.runDetachedAsyncCmd(m.requestProjectFormat())
			}},
		},
	})

	// Inspect the clicked source point even when a private selection retains
	// the caret elsewhere. A stale menu cannot inspect a newer publication.
	pos := diagnosticPosition
	factID, _ := ed.DiagnosticCursor()
	diags := m.EditorState.Editor.DiagnosticsAt(pos.Line, pos.Col)
	hasLSP := m.ws.LSPMgr() != nil && m.ws.LSPMgr().ServerReady()
	canRequest := hasLSP || m.manualLSPAvailable()

	lspSection := ui.ContextMenuSection{SectionLabel: "Code"}
	if canRequest {
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: "Go to definition", Detail: "Alt+.", Action: func() {
				m.runDetachedAsyncCmd(m.requestNavigationCmd(types.LSPNavigationDefinition))
			},
		})
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: "Find references", Detail: "Alt+;", Action: func() {
				m.runDetachedAsyncCmd(m.requestNavigationCmd(types.LSPNavigationReferences))
			},
		})
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: "Go to type definition", Detail: "Alt+T", Action: func() {
				m.runDetachedAsyncCmd(m.requestNavigationCmd(types.LSPNavigationTypeDefinition))
			},
		}, ui.ContextMenuItem{
			Label: "Go to implementation", Detail: "Alt+M", Action: func() {
				m.runDetachedAsyncCmd(m.requestNavigationCmd(types.LSPNavigationImplementation))
			},
		})
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: "Rename symbol", Detail: "F2", Disabled: !hasLSP, Action: func() {
				m.startRename()
			},
		})
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: "Parameter information", Detail: "Alt+I", Disabled: !hasLSP, Action: func() {
				m.runDetachedAsyncCmd(m.requestSignatureHelpCmd())
			},
		})
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: "Code actions", Detail: ui.FormatShortcut(ui.CodeActionsChord), Action: func() {
				m.runDetachedAsyncCmd(m.requestCodeActionsCmd())
			},
		})
	}
	if len(diags) > 0 {
		lspSection.Items = append(lspSection.Items, ui.ContextMenuItem{
			Label: "Show diagnostics", Action: func() {
				m.showDiagnosticAt(ed, factID, pos.Line, pos.Col)
			},
		})
	}
	if len(lspSection.Items) > 0 {
		st.Sections = append(st.Sections, lspSection)
	}

	blame, hasBlame := ed.BlameAtLine(ed.CursorLine())
	blameHash := blame.Hash
	line := ed.CursorLine()
	_, _, hasHunk := ed.GitHunkAtLine(line)
	fileScope, fileChanges, _ := m.gitChangesUnder(ed.FilePath())
	fileChanged, fileReveal := ed.FilePath() != "" && len(fileChanges) > 0, ""
	if fileChanged {
		fileReveal = gitdiff.ChangeKey(fileChanges[0])
	}
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Git",
		Items: []ui.ContextMenuItem{
			{Label: "Show Git changes", Disabled: !fileChanged, Action: func() {
				m.startSourceControlMutation(m.openGitReview(fileScope, fileReveal))
			}},
			{Label: "Inspect change", Disabled: !hasHunk, Action: func() { m.inspectEditorChange(ed, line) }},
			{Label: "Compare with disk", Action: func() { m.startSourceControlMutation(m.compareEditorWithDisk(ed)) }},
			{Label: "Compare with saved version", Action: func() { m.runDetachedAsyncCmd(m.openLocalDiff(ed, localDiffSaved)) }},
			{Label: "Resolve shared changes", Disabled: m.liveEditors[ed.Primary()] == nil, Action: func() { m.startSourceControlMutation(m.OpenLiveEditorConflict(ed)) }},
			{Label: "Next change", Disabled: len(ed.GitHunks()) == 0, Action: func() { m.jumpEditorChange(1) }},
			{Label: "Previous change", Disabled: len(ed.GitHunks()) == 0, Action: func() { m.jumpEditorChange(-1) }},
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
			{Label: "Find and replace", Detail: "Ctrl+H", Action: func() {
				m.openFindReplace(true)
			}},
			{Label: "Go to line", Detail: "Ctrl+G", Action: func() {
				m.openGoToLine()
			}},
			{Label: "Show document outline", Detail: "Alt+O", Disabled: !hasLSP, Action: func() {
				m.runDetachedAsyncCmd(m.requestDocumentSymbolsCmd())
			}},
			{Label: "Show inlay hints", Detail: "Alt+J", Disabled: !hasLSP, Action: func() {
				m.runDetachedAsyncCmd(m.openInlayInspectorCmd())
			}},
		},
	}
	if view := m.inlayViews[ed]; view != nil && view.hidden {
		viewSection.Items = append(viewSection.Items, ui.ContextMenuItem{Label: "Show inlay hints", Action: func() {
			m.setInlayViewHidden(false)
			m.runDetachedAsyncCmd(m.syncInlayHints())
		}})
	} else {
		viewSection.Items = append(viewSection.Items, ui.ContextMenuItem{Label: "Hide inlay hints", Action: func() {
			m.setInlayViewHidden(true)
		}})
	}
	st.Sections = append(st.Sections, viewSection)

	return st
}

// buildActivityRegionContextMenu dispatches the left rail and sidebar body to
// the destination that actually owns the clicked cells. The activity region
// is not synonymous with the file tree: Agents and Terminals
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
		if hit, hitOK := m.activityBar.ViewAt(mouse.Y - rect.Y); hitOK {
			view = hit
		}
		return m.buildActivityRailContextMenu(view)
	}
	switch m.activityView {
	case activityAgents:
		return m.buildAgentSidebarContextMenu(mouse)
	case activityWorkspaces:
		return m.buildWorkspaceSidebarContextMenu(mouse)
	case activityRemotes:
		return m.buildRemoteSidebarContextMenu(mouse)
	case activitySkills, activityMCP:
		return m.buildActivityRailContextMenu(m.activityView)
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
						m.startNewFileInTree(m.ws.RootPath())
					}
				}},
				{Label: "New folder", Action: func() {
					if m.Tree != nil && m.Tree.Model != nil {
						m.startNewFolderInTree(m.ws.RootPath())
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
		activeNode := m.AgentView.ActiveNode()
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Agents",
			Items: []ui.ContextMenuItem{
				{Label: "New session", Action: func() { m.startSourceControlMutation(m.launcherNewSession()) }},
				{Label: "Manage sessions", Action: m.openSessionPicker},
				{Label: "Stop current run", Disabled: activeNode == nil || !m.canCancelAgent(activeNode.ID), Action: func() {
					if node := m.AgentView.ActiveNode(); node != nil {
						m.cancelAgentTurn(node.ID)
					}
				}},
			},
		})
	case activitybar.ViewWorkspaces:
		st.Sections = append(st.Sections, m.workspaceNavigationActions())
	case activitybar.ViewRemotes:
		st.Sections = append(st.Sections, m.remoteNavigationActions())
	case activitybar.ViewSkills, activitybar.ViewMCP:
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: view.Label(),
			Items: []ui.ContextMenuItem{
				{Label: "Manage " + view.Label(), Action: func() { m.startSourceControlMutation(m.openCapabilityManager(view, "", "")) }},
				{Label: "Refresh state", Action: func() { m.startSourceControlMutation(m.capabilitySnapshotCmd()) }},
			},
		})
	}
	st.Sections = append(st.Sections, m.navigationActions(view))
	return st
}

func (m *App) buildActivitySettingsContextMenu() *ui.ContextMenuState {
	return &ui.ContextMenuState{
		Title: activitybar.SettingsIcon(false) + " Settings",
		Sections: []ui.ContextMenuSection{{
			SectionLabel: "Settings",
			Items: []ui.ContextMenuItem{
				{Label: "Application settings", Action: m.openApplicationSettings},
				{Label: "Workspace settings", Action: m.openWorkspaceSettings},
			},
		}},
	}
}

func activityShortcut(view activitybar.View) string {
	switch view {
	case activitybar.ViewAgents:
		return "Ctrl+Shift+A"
	case activitybar.ViewFiles:
		return "Ctrl+Shift+E"
	}
	return ""
}

func (m *App) activityItemAtMouse(mouse tea.Mouse) (ui.ListItem, bool) {
	if m.activityList == nil || m.rootHost == nil {
		return ui.ListItem{}, false
	}
	rect, ok := m.rootHost.ScreenRect(activitylist.ListID)
	if !ok || !rect.Contains(mouse.X, mouse.Y) {
		return ui.ListItem{}, false
	}
	pane, ok := m.rootHost.ScreenRect(activitylist.ScreenID)
	if !ok {
		return ui.ListItem{}, false
	}
	return m.activityList.ItemAtLocalY(mouse.Y - pane.Y)
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
	ws := m.launcherScreen.WorkspaceItemAt(mouse.X-rect.X, mouse.Y-rect.Y)
	if ws == nil {
		return nil
	}
	// Capture by value: the screen rebuilds its item slice freely, so the
	// closures below must not alias the live list.
	entry := *ws
	return m.workspaceResourceMenu(entry)
}

// Home and the sidebar expose the same resource operations and lifetime fence.
// Neither list owns deletion; both enter the one core-owned review/Undo flow.
func (m *App) workspaceResourceMenu(entry launcher.WorkspaceEntry) *ui.ContextMenuState {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	valid := func() bool { return m.ws == owner && generation == m.workspaceSwitchGen.Load() }
	st := ui.QuickMenu("",
		ui.ContextMenuItem{Label: "Open", Disabled: sameWorkspaceRoot(m.ws.RootPath(), entry.Path), Action: func() {
			if valid() {
				m.runDetachedAsyncCmd(m.launcherOpenFolder(entry.Path))
			}
		}},
		ui.ContextMenuItem{Label: "Copy path", Action: func() {
			if valid() {
				m.copyToClipboard(entry.Path)
			}
		}},
	)
	st.Title = entry.Name
	st.Sections = append(st.Sections, ui.ContextMenuSection{Items: []ui.ContextMenuItem{
		{Label: "Delete workspace", Disabled: sameWorkspaceRoot(m.ws.RootPath(), entry.Path), Action: func() {
			if valid() {
				m.openWorkspaceDelete(entry)
			}
		}},
	}})
	return st
}

func (m *App) buildAgentSidebarContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	item, hit := m.activityItemAtMouse(mouse)
	agentID := ""
	if hit && strings.HasPrefix(item.ID, "agent:") {
		agentID = strings.TrimPrefix(item.ID, "agent:")
	}
	node := m.ws.AgentMgr().Get(agentID)
	title := activitybar.ViewAgents.Glyph() + " Agents"
	if node != nil {
		label := m.agentLabel(node)
		if label == "" {
			label = item.Label
		}
		title = activitybar.ViewAgents.Glyph() + " " + label
	}
	st := &ui.ContextMenuState{Title: title}
	if node != nil {
		// Aeron opens like any agent but is permanent: it has no Terminate.
		aeron := workspace.SessionIsAeron(m.ws.SessionsByAgent()[node.ID])
		items := []ui.ContextMenuItem{
			{Label: "Open agent", Action: func() { m.restoreAgent(node.ID) }},
			{Label: "Open in window", Action: func() {
				m.openAgentInWindow(node.ID)
			}},
			{Label: "Continue run", Disabled: !m.canRetryAgentRecovery(node.ID), Action: func() {
				m.runDetachedAsyncCmd(m.retryAgentRecovery(node.ID))
			}},
			{Label: "Minimize agent", Disabled: m.AgentView.IsHidden(node.ID), Action: func() {
				m.minimizeAgent(node.ID)
			}},
			{Label: "Stop current run", Disabled: !m.canCancelAgent(node.ID), Action: func() {
				m.cancelAgentTurn(node.ID)
			}},
		}
		if !aeron {
			items = append(items,
				ui.ContextMenuItem{Label: "Terminate agent", Action: func() {
					m.terminateAgent(node.ID)
				}},
			)
		}
		items = append(items,
			ui.ContextMenuItem{Label: "Copy agent name", Action: func() { m.copyToClipboard(item.Label) }},
			ui.ContextMenuItem{Label: "Copy model", Disabled: node.Model == "", Action: func() { m.copyToClipboard(node.Model) }},
		)
		st.Sections = append(st.Sections, ui.ContextMenuSection{SectionLabel: "Agent", Items: items})
	}
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Sessions",
		Items: []ui.ContextMenuItem{
			{Label: "New session", Action: func() { m.startSourceControlMutation(m.launcherNewSession()) }},
			{Label: "Manage sessions", Action: m.openSessionPicker},
		},
	})
	return st
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
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
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
			{Label: "Open to the side", Disabled: path == "" || isDir, Action: func() {
				if path != "" && m.ws == owner && generation == m.workspaceSwitchGen.Load() {
					m.focusMainGroup(m.Win.SplitGroup(m.Win.ActiveGroup(), ui.Horizontal, false))
					_, _ = m.openFileFromTree(path)
				}
			}},
			{Label: "Open in window", Disabled: path == "" || isDir, Action: func() {
				if path != "" {
					m.runDetachedAsyncCmd(m.openFileInWindow(path))
				}
			}},
			{Label: "Expand or collapse", Disabled: path == "" || !isDir, Action: func() {
				if path != "" && isDir {
					m.Tree.Model.ToggleExpand(path)
				}
			}},
			{Label: desktopRevealLabel(), Disabled: path == "", Action: func() {
				if path != "" && m.ws == owner && generation == m.workspaceSwitchGen.Load() {
					m.runDetachedAsyncCmd(m.revealTreePath(path, isDir))
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
				m.startNewFileInTree(m.ws.RootPath())
			}},
			ui.ContextMenuItem{Label: "New folder", Action: func() {
				m.startNewFolderInTree(m.ws.RootPath())
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

	// Each item runs the same tree action as its key, so menu, keyboard and
	// drag and drop share one path.
	treeAction := func(id string) func() {
		return func() {
			if m.ws == owner && generation == m.workspaceSwitchGen.Load() {
				cmd, _ := m.handleTreeAction(&ui.Action{ID: id, Payload: path})
				m.runDetachedAsyncCmd(cmd)
			}
		}
	}
	pasteDetail := "Ctrl+V"
	if m.treeClipboardReady() {
		pasteDetail = filepath.Base(m.treeClip.path) + " · Ctrl+V"
	}
	editItems := []ui.ContextMenuItem{}
	if path != "" {
		editItems = append(editItems,
			ui.ContextMenuItem{Label: "Cut", Detail: "Ctrl+X", Action: treeAction(shelltree.ActionCut)},
			ui.ContextMenuItem{Label: "Copy", Detail: "Ctrl+C", Action: treeAction(shelltree.ActionCopy)},
		)
	}
	editItems = append(editItems, ui.ContextMenuItem{Label: "Paste", Detail: pasteDetail, Disabled: !m.treeClipboardReady(), Action: treeAction(shelltree.ActionPaste)})
	if path != "" {
		editItems = append(editItems,
			ui.ContextMenuItem{Label: "Duplicate", Detail: "Ctrl+D", Action: treeAction(shelltree.ActionDuplicate)},
			ui.ContextMenuItem{Label: "Rename", Detail: "F2", Action: treeAction(shelltree.ActionRename)},
			ui.ContextMenuItem{Label: "Delete", Detail: "Delete", Action: treeAction(shelltree.ActionDelete)},
			ui.ContextMenuItem{Label: "Copy path", Action: func() { m.copyToClipboard(path) }},
			ui.ContextMenuItem{Label: "Copy relative path", Action: func() { m.copyToClipboard(m.treeRel(path)) }},
		)
	}
	if m.treeLastMove != nil && m.treeLastMove.owner == m.ws {
		editItems = append(editItems, ui.ContextMenuItem{Label: "Undo move", Detail: "Ctrl+Z", Action: treeAction(shelltree.ActionUndo)})
	}
	st.Sections = append(st.Sections, ui.ContextMenuSection{SectionLabel: "Edit", Items: editItems})
	if section, ok := m.explorerGitSection(path, isDir); ok {
		st.Sections = append(st.Sections, section)
	}
	if path != "" && m.ws.HistoryService() != nil {
		target := path
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Local history",
			Items: []ui.ContextMenuItem{
				{Label: "Show history for this path", Action: func() { m.runDetachedAsyncCmd(m.openLocalHistoryForPath(target)) }},
				{Label: "Open workspace history", Action: func() { m.runDetachedAsyncCmd(m.openLocalHistory()) }},
			},
		})
	}

	hiddenLabel := "Show hidden files"
	if m.Tree.Model.ShowHidden() {
		hiddenLabel = "Hide hidden files"
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
			{Label: hiddenLabel, Detail: "Ctrl+.", Action: func() {
				m.runDetachedAsyncCmd(m.sidebarScreen.SetShowHidden(!m.Tree.Model.ShowHidden()))
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
	hitIndex := m.paneTabAt(mouse.X, mouse.Y)
	if hitIndex < 0 || hitIndex >= m.Win.TabCount() {
		return nil
	}
	tabs := m.Win.Tabs()
	tab := tabs[hitIndex]
	owner := m.paneOwner(tab.ID, tab.Group)

	st := &ui.ContextMenuState{Title: "Tab"}
	st.Sections = append(st.Sections, m.paneMenuSections(tab.ID)...)

	// Get tab-specific info.
	if m.isEditorTabTitle(tab.Title) || isImageTab(&tab) {
		path := tab.Resource.ID
		if path == "" {
			path = m.editorPathFromTabTitle(tab.Title)
		}
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Tab",
			Items: []ui.ContextMenuItem{
				{Label: "Close tab", Detail: m.menuShortcut("file.close", "editor.close"), Action: func() {
					m.runDetachedAsyncCmd(m.handleAgentTabClose(m.Win.IndexOf(tab.ID)))
				}},
				{Label: "Close other files in pane", Action: func() {
					m.closePaneEditorTabs(owner, false)
				}},
				{Label: "Close files to the right", Action: func() {
					m.closePaneEditorTabs(owner, true)
				}},
				{Label: "Copy path", Action: func() {
					m.copyToClipboard(path)
				}},
				{Label: "Reveal in tree", Action: func() {
					m.runDetachedAsyncCmd(m.beginUserTreeReveal(path, owner))
				}},
				{Label: desktopRevealLabel(), Action: func() {
					current := m.Win.TabByID(tab.ID)
					if current == nil || current.Title != tab.Title || current.Resource != tab.Resource {
						return
					}
					m.runDetachedAsyncCmd(m.revealTreePath(path, false))
				}},
			},
		})
	} else if m.isAgentTabTitle(tab.Title) {
		agentID := m.agentIDFromTabTitle(tab.Title)
		aeron := workspace.SessionIsAeron(m.ws.SessionsByAgent()[agentID])
		items := []ui.ContextMenuItem{
			{Label: "Minimize", Action: func() {
				m.runDetachedAsyncCmd(m.handleAgentTabClose(m.Win.IndexOf(tab.ID)))
			}},
			{Label: "Continue run", Disabled: !m.canRetryAgentRecovery(agentID), Action: func() {
				m.runDetachedAsyncCmd(m.retryAgentRecovery(agentID))
			}},
			{Label: "Stop current run", Disabled: !m.canCancelAgent(agentID), Action: func() {
				m.cancelAgentTurn(agentID)
			}},
		}
		if !aeron {
			items = append(items, ui.ContextMenuItem{Label: "Terminate agent", Action: func() {
				m.terminateAgent(agentID)
			}})
		}
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Tab",
			Items:        items,
		})
	}
	if tab.Resource.Kind == window.ResourcePanel {
		id := tab.Resource.ID
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Panel",
			Items: []ui.ContextMenuItem{
				{Label: "Open in window", Action: func() { m.openPanelAsWindow(id) }},
				{Label: "Move to main area", Action: func() { m.dockPanel(id) }},
				{Label: "Close tab", Action: func() { m.runDetachedAsyncCmd(m.handleAgentTabClose(m.Win.IndexOf(tab.ID))) }},
			},
		})
	}
	if tab.Resource.Kind == window.ResourceGitReview || tab.Resource.Kind == window.ResourceLocalDiff {
		st.Sections = append(st.Sections, ui.ContextMenuSection{SectionLabel: "Tab", Items: []ui.ContextMenuItem{
			{Label: "Close tab", Action: func() { m.runDetachedAsyncCmd(m.handleAgentTabClose(m.Win.IndexOf(tab.ID))) }},
		}})
	}

	// Resource callbacks use the same exact owner fence as pane operations.
	for si := range st.Sections {
		for ii := range st.Sections[si].Items {
			item := &st.Sections[si].Items[ii]
			if item.Action != nil {
				item.Action = owner.action(m, item.Action)
			}
		}
	}
	return st
}

// buildInputContextMenu creates the menu for the chat input.
func (m *App) buildInputContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	// Right-click on an attached reference chip gets attachment management
	// instead of the clipboard menu, because the block under the pointer is the
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
			// Copy/Cut act on the whole draft, matching every other text
			// control's menu. The Ctrl+C/Ctrl+X chords keep their selection
			// semantics (copy a selection, or clear the draft).
			{Label: "Copy", Detail: "Ctrl+C", Disabled: m.Input.Value() == "", Action: func() {
				m.copyToClipboard(m.Input.Value())
			}},
			{Label: "Cut", Detail: "Ctrl+X", Disabled: m.Input.Value() == "", Action: func() {
				m.copyToClipboard(m.Input.Value())
				m.Input.ClearInput()
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
	for _, chip := range m.Input.ReferenceChips(m.inputRect.W) {
		if chip.ID != "" && (chip.Body.Contains(localX, localY) || chip.Remove.Contains(localX, localY)) {
			for _, rr := range m.Input.RefRanges() {
				if rr.ID == chip.ID {
					return rr, true
				}
			}
		}
	}
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
// pill: open using the preference or an explicit destination, relabel it,
// remove it from the draft, or open file-backed attachments in the editor.
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
		{Label: "Open", Detail: "Space", Action: func() {
			m.runDetachedAsyncCmd(m.openAttachment(rr.ID, false))
		}},
		{Label: "Preview in Crude", Action: func() {
			m.openReferenceOverlay(rr.ID)
		}},
		{Label: "Rename", Action: func() {
			m.startAttachmentRename(rr.ID, title)
		}},
		{Label: "Remove", Action: func() {
			m.removeAttachment(rr)
		}},
	}
	items = append(items,
		ui.ContextMenuItem{Label: "Open with default app", Action: func() { m.runDetachedAsyncCmd(m.openAttachmentIn(ref.ID, types.AttachmentOpenSystem)) }},
		ui.ContextMenuItem{Label: "Show in file manager", Action: func() { m.runDetachedAsyncCmd(m.openAttachmentIn(ref.ID, types.AttachmentOpenFileManager)) }},
	)
	if ref.FilePath != "" {
		path := ref.FilePath
		items = append(items, ui.ContextMenuItem{Label: "Open in editor", Action: func() {
			if _, err := m.openEditorTab(path); err != nil {
				m.Feedback.NotifyError("open attachment: " + err.Error())
			}
		}})
	}
	st.Sections = append(st.Sections, ui.ContextMenuSection{Items: items})
	return st
}

// Removal detaches the draft, not references retained by sent messages.
func (m *App) removeAttachment(rr input.RefRange) {
	m.replaceRefSpans(m.refRangesFor(rr.ID), "")
	m.persistComposerDraftIfChanged()
	if m.Feedback != nil {
		m.Feedback.NotifyInfo("Attachment removed")
	}
}

// startAttachmentRename opens the shared text prompt to relabel one chip.
// The chip's draft spans are captured BEFORE the store rename, because once the
// store knows only the new label, the old tokens no longer resolve by scan.
func (m *App) startAttachmentRename(refID, current string) {
	if m.RefStore() == nil {
		return
	}
	spans := m.refRangesFor(refID)
	m.openTextPrompt(ui.TextPromptRename, "Rename attachment",
		"label", current, func(value string) error {
			final, ok := m.RefStore().RenameReference(refID, value)
			if !ok {
				return fmt.Errorf("cannot rename this attachment")
			}
			m.replaceRefSpans(spans, final)
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("Renamed to " + strings.TrimSuffix(strings.TrimPrefix(final, "["), "]"))
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
	m.Input.SetValuePreserveReferences(string(runes))
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
	agentID := m.AgentView.ActiveID()
	if m.canRetryAgentRecovery(agentID) {
		status := m.ws.AgentMgr().Get(agentID).State.RecoveryStatus()
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Run recovery",
			Items: []ui.ContextMenuItem{
				{Label: "Continue run", Action: func() { m.runDetachedAsyncCmd(m.retryAgentRecovery(agentID)) }},
				{Label: "Copy diagnostic", Action: func() { m.copyToClipboard(status.Detail) }},
				{Label: "Network settings", Action: m.openNetworkSettings},
			},
		})
	}
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
			{Label: "Copy last response", Action: func() {
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
					m.Feedback.NotifyInfo("No code block in last assistant message")
					return
				}
				m.copyToClipboard(code)
			}},
		},
	})
	if m.chatScreen != nil && m.rootHost != nil && m.ActiveSession != nil && m.ws.HistoryService() != nil {
		if rect, ok := m.screenRectOf(m.chatScreen.VirtualList().ID()); ok {
			if row, ok := m.chatScreen.MessageAtLocalY(mouse.Y - rect.Y); ok {
				role := ""
				var canonical workspace.Message
				canonicalFound := false
				if node := m.AgentView.ActiveNode(); node != nil && node.State != nil && node.State.Messages != nil {
					for _, msg := range node.State.Messages.Messages() {
						if msg.InternalID == row.ID {
							role = msg.Role
							canonical = msg
							canonicalFound = true
							break
						}
					}
				}
				if role == workspace.MessageRoleStatus && canonicalFound && canonical.HistorySafetySnapshot != "" {
					snapshotID := canonical.HistorySafetySnapshot
					st.Sections = append(st.Sections, ui.ContextMenuSection{SectionLabel: "Local history", Items: []ui.ContextMenuItem{
						{Label: "Undo this restore", Action: func() { m.runDetachedAsyncCmd(m.openLocalHistoryForSnapshot(snapshotID)) }},
						{Label: "Open workspace history", Action: func() { m.runDetachedAsyncCmd(m.openLocalHistory()) }},
					}})
				}
				if role == workspace.MessageRoleUser {
					sessionID, messageID := m.ActiveSession.SessionID, row.ID
					agentID := m.AgentView.ActiveID()
					active := agentID != "" && m.ws.AgentTurnActive(agentID)
					open := func() { cmd := m.openLocalHistoryForMessage(sessionID, messageID, agentID); m.runDetachedAsyncCmd(cmd) }
					st.Sections = append(st.Sections, ui.ContextMenuSection{SectionLabel: "Local history", Items: []ui.ContextMenuItem{
						{Label: "Preview this agent's changes", Disabled: active, Action: open},
						{Label: "Revert this agent's files", Disabled: active, Action: open},
						{Label: "Open workspace history", Action: func() { m.runDetachedAsyncCmd(m.openLocalHistory()) }},
					}})
				}
			}
		}
	}
	return st
}

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
		m.closeContextMenu()
		st.Activate()
		return nil
	case tea.KeyDown, tea.KeyUp, tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown:
		// The mounted native List owns navigation, disabled-row skipping and
		// scrolling. A second state-only key switch used to ignore Home/End
		// and paging, making large layout scopes effectively unreachable.
		if frame, ok := fw.Content.(*ui.ComponentWidget); ok && frame.Host() != nil {
			w, h := fw.Size()
			frame.Host().Resize(w, h)
			frame.Host().Layout()
			result := frame.Host().Handle(msg)
			ui.SyncContextMenuCursor(frame, st)
			fw.MarkContentDirty()
			return result.Cmd
		}
	}
	return nil
}

func (m *App) showContextMenuAt(x, y int) {
	msg := tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight}
	_, _ = m.dispatchMouseRightClick(msg)
}
