package viewmodel

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/gitreview"
	"crude/tui/screens/launcher"
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
	ed := m.EditorState.Editor
	if m.rootHost != nil {
		if rect, ok := m.rootHost.ScreenRect(m.editorScreen.ID()); ok && rect.Contains(mouse.X, mouse.Y) {
			if pos, valid := ed.PositionAt(mouse.X-rect.X, mouse.Y-rect.Y); valid && !ed.HasSelection() {
				ed.SetCursorPosition(pos.Line, pos.Col)
			}
		}
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
	pos := struct{ Line, Col int }{Line: ed.CursorLine(), Col: ed.CursorCol()}
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

	blame, hasBlame := ed.BlameAtLine(ed.CursorLine())
	blameHash := blame.Hash
	line := ed.CursorLine()
	_, _, hasHunk := ed.GitHunkAtLine(line)
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Git",
		Items: []ui.ContextMenuItem{
			{Label: "Inspect change", Disabled: !hasHunk, Action: func() { m.inspectEditorChange(ed, line) }},
			{Label: "Compare with disk", Action: func() { m.startSourceControlMutation(m.compareEditorWithDisk(ed)) }},
			{Label: "Compare with saved version", Action: func() { m.runDetachedAsyncCmd(m.openLocalDiff(ed, localDiffSaved)) }},
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
		if hit, hitOK := m.activityBar.ViewAt(mouse.Y - rect.Y); hitOK {
			view = hit
		}
		return m.buildActivityRailContextMenu(view)
	}
	switch m.activityView {
	case activityAgents:
		return m.buildAgentSidebarContextMenu(mouse)
	case activitySkills, activityMCP:
		return m.buildActivityRailContextMenu(m.activityView)
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
	case activitybar.ViewSkills, activitybar.ViewMCP:
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: view.Label(),
			Items: []ui.ContextMenuItem{
				{Label: "Manage " + view.Label(), Action: func() { m.startSourceControlMutation(m.openCapabilityManager(view, "", "")) }},
				{Label: "Refresh state", Action: func() { m.startSourceControlMutation(m.capabilitySnapshotCmd()) }},
			},
		})
	case activitybar.ViewGit:
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Repository",
			Items: []ui.ContextMenuItem{
				{Label: "Refresh", Detail: "Ctrl+R", Action: func() {
					m.startSourceControlMutation(m.refreshSourceControl())
				}},
				{Label: "Review changes", Action: func() { m.startSourceControlMutation(m.openGitReview(gitreview.ModeChanges, nil)) }},
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
	case activitybar.ViewGit:
		return "Ctrl+Shift+G"
	case activitybar.ViewFiles:
		return "Ctrl+Shift+E"
	}
	return ""
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
	ws := m.launcherScreen.WorkspaceItemAt(mouse.X-rect.X, mouse.Y-rect.Y)
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
	st.Sections = append(st.Sections, ui.ContextMenuSection{Items: []ui.ContextMenuItem{
		{Label: "Delete workspace…", Action: func() { m.openWorkspaceDelete(entry) }},
	}})
	return st
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
				{Label: "Continue run", Disabled: !m.canRetryAgentRecovery(node.ID), Detail: "from saved work", Action: func() {
					m.runDetachedAsyncCmd(m.retryAgentRecovery(node.ID))
				}},
				{Label: "Minimize agent", Disabled: node.IsHidden(), Detail: "keeps running", Action: func() {
					m.minimizeAgent(node.ID)
				}},
				{Label: "Cancel agent", Disabled: !node.IsActive() && !m.canRetryAgentRecovery(node.ID), Action: func() {
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
			stageLabel := "Review to stage…"
			stageAction := func() { m.startSourceControlMutation(m.openGitReview(gitreview.ModeChanges, &change)) }
			if change.Area == workspace.GitAreaStaged {
				stageLabel = "Review to unstage…"
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
		}
	}
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Repository",
		Items: []ui.ContextMenuItem{
			{Label: "Refresh", Detail: "Ctrl+R", Action: func() {
				m.startSourceControlMutation(m.refreshSourceControl())
			}},
			{Label: "Review changes", Action: func() { m.startSourceControlMutation(m.openGitReview(gitreview.ModeChanges, nil)) }},
			{Label: "Copy branch name", Disabled: m.sourceControl == nil || m.sourceControl.Snapshot().Branch == "", Action: func() {
				m.copyToClipboard(m.sourceControl.Snapshot().Branch)
			}},
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
	st.Sections = append(st.Sections, m.paneMenuSection(tab.ID))

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
				{Label: "Continue run", Disabled: !m.canRetryAgentRecovery(agentID), Detail: "from saved work", Action: func() {
					m.runDetachedAsyncCmd(m.retryAgentRecovery(agentID))
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
		items = append(items,
			ui.ContextMenuItem{Label: "Open with default app", Action: func() { m.runDetachedAsyncCmd(m.openAttachment(ref.ID, false)) }},
			ui.ContextMenuItem{Label: "Show in file manager", Action: func() { m.runDetachedAsyncCmd(m.openAttachment(ref.ID, true)) }},
		)
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
	agentID := m.AgentMgr.ActiveID()
	if m.canRetryAgentRecovery(agentID) {
		status := m.AgentMgr.Get(agentID).State.RecoveryStatus()
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Run recovery",
			Items: []ui.ContextMenuItem{
				{Label: "Continue run", Detail: "from saved work", Action: func() { m.runDetachedAsyncCmd(m.retryAgentRecovery(agentID)) }},
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
					m.Feedback.NotifyInfo("No code block in last assistant message")
					return
				}
				m.copyToClipboard(code)
			}},
		},
	})
	if m.chatScreen != nil && m.rootHost != nil && m.ActiveSession != nil && m.HistoryService != nil {
		if rect, ok := m.rootHost.ScreenRect(m.chatScreen.VirtualList().ID()); ok {
			if row, ok := m.chatScreen.MessageAtLocalY(mouse.Y - rect.Y); ok {
				role := ""
				var canonical workspace.Message
				canonicalFound := false
				if node := m.AgentMgr.ActiveNode(); node != nil && node.State != nil && node.State.Messages != nil {
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
					st.Sections = append(st.Sections, ui.ContextMenuSection{SectionLabel: "Local History", Items: []ui.ContextMenuItem{
						{Label: "Undo this restore", Detail: "opens safety preview", Action: func() { m.runDetachedAsyncCmd(m.openLocalHistoryForSnapshot(snapshotID)) }},
						{Label: "Open workspace history", Action: func() { m.runDetachedAsyncCmd(m.openLocalHistory()) }},
					}})
				}
				if role == workspace.MessageRoleUser {
					sessionID, messageID := m.ActiveSession.SessionID, row.ID
					agentID := m.AgentMgr.ActiveID()
					active := agentID != "" && m.AgentTurnActive(agentID)
					open := func() { cmd := m.openLocalHistoryForMessage(sessionID, messageID, agentID); m.runDetachedAsyncCmd(cmd) }
					st.Sections = append(st.Sections, ui.ContextMenuSection{SectionLabel: "Local History", Items: []ui.ContextMenuItem{
						{Label: "Preview this agent's changes", Disabled: active, Detail: map[bool]string{true: "turn active", false: ""}[active], Action: open},
						{Label: "Revert this agent's files", Disabled: active, Detail: "conversation retained", Action: open},
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
