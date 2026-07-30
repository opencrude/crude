package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/tui/widgets"
	"crude/tui/window"
)

func (m *App) buildEditorContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		return nil
	}
	st := &widgets.ContextMenuState{Title: "Editor"}

	// Cut/Copy/Paste (no section label: the popup title "Editor"
	// already provides context, and the items speak for themselves)
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		Items: []widgets.ContextMenuItem{
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
	selSection := widgets.ContextMenuSection{SectionLabel: "Selection"}
	selItems := []widgets.ContextMenuItem{}
	if m.EditorState.Editor.HasSelection() {
		selItems = append(selItems, widgets.ContextMenuItem{
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

	lspSection := widgets.ContextMenuSection{SectionLabel: "Code"}
	if hasLSP {
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: "Go to definition", Detail: "Alt+.", Action: func() {
				_, _ = m.requestDefinitionCmd(), 0
			},
		})
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: "Find references", Detail: "Alt+;", Action: func() {
				_, _ = m.requestReferencesCmd(), 0
			},
		})
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: "Rename symbol", Detail: "F2", Action: func() {
				m.startRename()
			},
		})
	}
	if len(diags) > 0 {
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: fmt.Sprintf("Show %d diagnostic(s)", len(diags)), Action: func() {
				m.showDiagnosticAt(pos.Line, pos.Col)
			},
		})
	}
	if len(lspSection.Items) > 0 {
		st.Sections = append(st.Sections, lspSection)
	}

	// View actions.
	viewSection := widgets.ContextMenuSection{
		SectionLabel: "View",
		Items: []widgets.ContextMenuItem{
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

// buildSidebarContextMenu creates the menu for right-click on a file
// tree item.
func (m *App) buildSidebarContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	if m.Tree.Model == nil {
		return nil
	}
	st := &widgets.ContextMenuState{Title: "File"}

	// Get the path under the click.
	path := m.pathUnderMouse(mouse)
	isDir := path != "" && m.Tree.Model.IsDir(path)

	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		SectionLabel: "Actions",
		Items: []widgets.ContextMenuItem{
			{Label: "Open", Disabled: path == "", Action: func() {
				if path != "" {
					_, _ = m.openFileFromTree(path)
				}
			}},
			{Label: "Reveal in tree", Disabled: path == "", Action: func() {
				if path != "" {
					m.Tree.Model.ExpandToPath(path)
				}
			}},
		},
	})

	createSection := widgets.ContextMenuSection{SectionLabel: "Create"}
	if isDir {
		createSection.Items = append(createSection.Items,
			widgets.ContextMenuItem{Label: "New file", Action: func() {
				m.startNewFileInTree(path)
			}},
			widgets.ContextMenuItem{Label: "New folder", Action: func() {
				m.startNewFolderInTree(path)
			}},
		)
	}
	if path != "" {
		createSection.Items = append(createSection.Items,
			widgets.ContextMenuItem{Label: "New file (sibling)", Action: func() {
				m.startNewFileInTree(m.Tree.Model.ParentDir(path))
			}},
			widgets.ContextMenuItem{Label: "New folder (sibling)", Action: func() {
				m.startNewFolderInTree(m.Tree.Model.ParentDir(path))
			}},
		)
	}
	if len(createSection.Items) > 0 {
		st.Sections = append(st.Sections, createSection)
	}

	if path != "" {
		st.Sections = append(st.Sections, widgets.ContextMenuSection{
			SectionLabel: "Modify",
			Items: []widgets.ContextMenuItem{
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

	return st
}

// buildTabBarContextMenu creates the menu for right-click on a tab.
func (m *App) buildTabBarContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
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

	st := &widgets.ContextMenuState{Title: "Tab"}

	// Get tab-specific info.
	if m.isEditorTabTitle(tab.Title) {
		path := m.editorPathFromTabTitle(tab.Title)
		st.Sections = append(st.Sections, widgets.ContextMenuSection{
			SectionLabel: "Tab",
			Items: []widgets.ContextMenuItem{
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
		st.Sections = append(st.Sections, widgets.ContextMenuSection{
			SectionLabel: "Tab",
			Items: []widgets.ContextMenuItem{
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
func (m *App) buildInputContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	st := &widgets.ContextMenuState{Title: "Input"}
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		SectionLabel: "Clipboard",
		Items: []widgets.ContextMenuItem{
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
func (m *App) buildChatContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	st := &widgets.ContextMenuState{Title: "Chat"}
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		Items: []widgets.ContextMenuItem{
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
func (m *App) buildStatusBarContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	st := &widgets.ContextMenuState{Title: "Status Bar"}

	// LSP section. Always shown when an workspace.LSPManager exists; items
	// disable themselves when not applicable.
	if m.LSPMgr != nil {
		var lspItems []widgets.ContextMenuItem

		isConfigured := m.LSPMgr.IsConfigured()
		serverReady := m.LSPMgr.ServerReady()
		ins := m.LSPMgr.ProjectInsights()
		hasErrors := ins.Errors > 0

		lspItems = append(lspItems, widgets.ContextMenuItem{
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
			lspItems = append(lspItems, widgets.ContextMenuItem{
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
			lspItems = append(lspItems, widgets.ContextMenuItem{
				Label: "Start LSP",
				Action: func() {
					m.startLSPForWorkspace()
					m.StatusBar.SetInfo("LSP starting…")
				},
			})
		}

		if hasErrors {
			lspItems = append(lspItems, widgets.ContextMenuItem{
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
			lspItems = append(lspItems, widgets.ContextMenuItem{
				Label: "Copy diagnostics summary",
				Action: func() {
					m.copyToClipboard(summary)
				},
			})
		}

		if len(lspItems) > 0 {
			st.Sections = append(st.Sections, widgets.ContextMenuSection{
				SectionLabel: "LSP",
				Items:        lspItems,
			})
		}
	}

	// Status section. Always available.
	statusItems := []widgets.ContextMenuItem{
		{
			Label: "Copy status line",
			Action: func() {
				if m.StatusBar != nil {
					m.copyToClipboard(m.StatusBar.View())
				}
			},
		},
	}
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		SectionLabel: "Status",
		Items:        statusItems,
	})

	return st
}

// closeContextMenu removes the open context menu, if any.
func (m *App) closeContextMenu() {
	m.overlayHost.Close(ui.OverlayContextMenu)
}

// findFlatItemIndex returns the index in FlatItems for the item at
// (sectionIdx, itemIdx). Returns false if the item is disabled (and
// therefore not in FlatItems).
func findFlatItemIndex(st *widgets.ContextMenuState, sectionIdx, itemIdx int) (int, bool) {
	for i := 0; i < len(st.FlatItems); i++ {
		si, ii, ok := st.FlatItemAt(i)
		if ok && si == sectionIdx && ii == itemIdx {
			return i, true
		}
	}
	return 0, false
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
		widgets.ApplyContextMenuCursor(cw, m.LastContextMenu)
		fw.MarkContentDirty()
		return
	}
	frame := widgets.BuildContextMenuFrame(m.Theme, m.LastContextMenu)
	w, h := frame.Measure(60, 40)
	if cw := widgets.ContextMenuPopupWidth(m.LastContextMenu); cw > 0 {
		w = cw
	}
	if ch := widgets.ContextMenuPopupHeight(m.LastContextMenu); ch > 0 {
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
