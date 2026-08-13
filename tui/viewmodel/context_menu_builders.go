package viewmodel

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/tui/rendering"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
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

	// Selection-aware items
	selSection := ui.ContextMenuSection{SectionLabel: "Selection"}
	selItems := []ui.ContextMenuItem{}
	if m.EditorState.Editor.HasSelection() {
		selItems = append(selItems, ui.ContextMenuItem{
			Label: "Format selection", Action: func() {
				// Best-effort: rely on Save+format-on-save for now.
				// This is a placeholder for a future range-format
				// hook.
			},
		})
	}
	selSection.Items = selItems
	if len(selItems) > 0 {
		st.Sections = append(st.Sections, selSection)
	}

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
						go m.CancelAgent(node.ID)
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

func (m *App) buildAgentSidebarContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	item, hit := m.activityItemAtMouse(mouse)
	agentID := ""
	if hit && strings.HasPrefix(item.ID, "agent:") {
		agentID = strings.TrimPrefix(item.ID, "agent:")
	}
	node := m.AgentMgr.Get(agentID)
	title := "◈ Agents"
	if node != nil {
		label := workspace.AgentDisplayLabel(node)
		if label == "" {
			label = item.Label
		}
		title = "◈ " + label
	}
	st := &ui.ContextMenuState{Title: title}
	if node != nil {
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Agent",
			Items: []ui.ContextMenuItem{
				{Label: "Open agent", Disabled: node.IsCompleted(), Action: func() {
					m.switchToAgentView(node.ID)
				}},
				{Label: "Cancel agent", Disabled: !node.IsActive(), Action: func() {
					go m.CancelAgent(node.ID)
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
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Tab",
			Items: []ui.ContextMenuItem{
				{Label: "Close", Detail: "Ctrl+W", Action: func() {
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
				{Label: "Close", Action: func() {
					_ = m.handleAgentTabClose(hit.TabIndex)
				}},
				{Label: "Cancel agent", Action: func() {
					go m.CancelAgent(agentID)
				}},
			},
		})
	}

	return st
}

// buildInputContextMenu creates the menu for the chat input.
func (m *App) buildInputContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
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
				lang, code, ok := rendering.ExtractLastCodeBlock(raw)
				if !ok {
					m.StatusBar.SetInfo("No code block in last assistant message")
					return
				}
				if lang != "" {
					m.StatusBar.SetInfo(fmt.Sprintf("Copied %s code block (%d chars)", lang, len(code)))
				} else {
					m.StatusBar.SetInfo(fmt.Sprintf("Copied code block (%d chars)", len(code)))
				}
				if err := clipboard.WriteText(code); err != nil {
					m.StatusBar.SetError("clipboard error: " + err.Error())
				}
			}},
		},
	})
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
