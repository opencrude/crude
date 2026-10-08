package viewmodel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/termdock"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

type cmdCategory string

const (
	catFile   cmdCategory = "file"
	catEdit   cmdCategory = "edit"
	catView   cmdCategory = "view"
	catTabs   cmdCategory = "tabs"
	catTools  cmdCategory = "tools"
	catSystem cmdCategory = "system"
	catHelp   cmdCategory = "help"
	catAgent  cmdCategory = "agent"
	catChat   cmdCategory = "chat"
	catApp    cmdCategory = "app"
)

type cmdEntry struct {
	ID       string
	Label    string
	Detail   string
	Category cmdCategory
	Icon     string
}

func paletteEntriesFromRegistry(registry *ui.ActionRegistry) []cmdEntry {
	all := registry.All()
	entries := make([]cmdEntry, 0, len(all))
	for _, a := range all {
		// Skip self-reference and all display-only fragments (status-bar hints).
		if a.ID == "palette.open" || a.DisplayOnly {
			continue
		}
		icon := a.Icon
		if icon == "" {
			icon = " "
		}
		entries = append(entries, cmdEntry{
			ID:       string(a.ID),
			Label:    a.Label,
			Detail:   a.Description,
			Category: cmdCategory(strings.ToLower(a.Category)),
			Icon:     icon,
		})
	}
	order := map[cmdCategory]int{catFile: 0, catEdit: 1, catView: 2, catTabs: 3, catAgent: 4, catTools: 5, catSystem: 6, catHelp: 7, catChat: 8, catApp: 9}
	sort.SliceStable(entries, func(first, second int) bool { return order[entries[first].Category] < order[entries[second].Category] })
	return entries
}

type commandPaletteModel struct {
	th       *theme.Theme
	items    []cmdEntry
	filtered []cmdEntry
	groups   []cmdGroup
	filterEd *ui.TextEdit // shared rune-safe filter engine
	idx      int
	callback func(cmdID string) tea.Cmd
	width    int
	height   int
}

// filterText returns the current filter query.
func (m *commandPaletteModel) filterText() string {
	if m.filterEd == nil {
		return ""
	}
	return m.filterEd.Value()
}

// setFilter replaces the filter query (tests and programmatic open).
func (m *commandPaletteModel) setFilter(s string) {
	if m.filterEd == nil {
		m.filterEd = ui.NewTextEdit()
	}
	m.filterEd.SetValue(s)
}

type cmdGroup struct {
	Category cmdCategory
	Label    string
	Start    int
	Count    int
}

func categoryLabel(c cmdCategory) string {
	switch c {
	case catFile:
		return "File"
	case catEdit:
		return "Edit"
	case catView:
		return "View"
	case catTabs:
		return "Tabs"
	case catTools:
		return "Tools"
	case catSystem:
		return "System"
	case catHelp:
		return "Help"
	case catAgent:
		return "Agent"
	case catChat:
		return "Chat"
	case catApp:
		return "App"
	}
	return string(c)
}

func newCommandPaletteModel(th *theme.Theme, callback func(cmdID string) tea.Cmd, registry *ui.ActionRegistry) *commandPaletteModel {
	m := &commandPaletteModel{
		th:       th,
		items:    paletteEntriesFromRegistry(registry),
		filterEd: ui.NewTextEdit(),
		callback: callback,
	}
	m.applyFilter()
	return m
}

func (m *commandPaletteModel) applyFilter() {
	q := m.filterText()
	if q == "" {
		m.filtered = append([]cmdEntry(nil), m.items...)
	} else {
		m.filtered = nil
		lower := strings.ToLower(q)
		for _, item := range m.items {
			if fuzzyMatch(item, lower) {
				m.filtered = append(m.filtered, item)
			}
		}
	}
	m.buildGroups()
	if m.idx >= len(m.filtered) {
		m.idx = 0
	}
}

func fuzzyMatch(item cmdEntry, filter string) bool {
	return fuzzyContains(item.Label, filter) ||
		fuzzyContains(item.Detail, filter) ||
		fuzzyContains(item.ID, filter)
}

func fuzzyContains(text, filter string) bool {
	if filter == "" {
		return true
	}
	lower := strings.ToLower(text)
	fi := 0
	for _, r := range lower {
		if fi < len(filter) && byte(r) == filter[fi] {
			fi++
		}
	}
	return fi == len(filter)
}

func (m *commandPaletteModel) buildGroups() {
	m.groups = nil
	var current cmdCategory
	var start int
	for i, item := range m.filtered {
		if item.Category != current {
			if i > start {
				m.groups = append(m.groups, cmdGroup{Category: current, Start: start, Count: i - start})
			}
			current = item.Category
			start = i
		}
	}
	if len(m.filtered) > start {
		m.groups = append(m.groups, cmdGroup{Category: current, Start: start, Count: len(m.filtered) - start})
	}
}

func (m *commandPaletteModel) visibleHeight() int {
	if m.height < 3 {
		return 3
	}
	// Account for search field row + divider + chrome.
	vh := m.height - 4
	if vh < 1 {
		vh = 1
	}
	return vh
}

// listItems converts the filtered commands into ui.ListItem with category headers
// and section dividers, matching the old buildContentLines layout.
func (m *commandPaletteModel) listItems() []ui.ListItem {
	var items []ui.ListItem
	for gi, g := range m.groups {
		if g.Count <= 0 {
			continue
		}
		if gi > 0 {
			items = append(items, ui.ListItem{Separator: true, Label: "─"})
		}
		items = append(items, ui.ListItem{Header: true, Label: categoryLabel(g.Category)})
		for i := g.Start; i < g.Start+g.Count; i++ {
			entry := m.filtered[i]
			icon := " "
			if entry.Icon != "" {
				icon = entry.Icon
			}
			items = append(items, ui.ListItem{
				ID:      entry.ID,
				Label:   entry.Label,
				Detail:  entry.Detail,
				Icon:    icon,
				Payload: entry,
			})
		}
	}
	return items
}

func (m *commandPaletteModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return nil
}

func (m *commandPaletteModel) updateKey(keyMsg tea.KeyPressMsg) tea.Cmd {
	switch keyMsg.Code {
	case tea.KeyEsc:
		return nil

	case tea.KeyEnter:
		if len(m.filtered) > 0 && m.idx >= 0 && m.idx < len(m.filtered) {
			item := m.filtered[m.idx]
			if m.callback != nil {
				return m.callback(item.ID)
			}
		}
		return nil

	case tea.KeyUp:
		if m.idx > 0 {
			m.idx--
		}

	case tea.KeyDown:
		if m.idx < len(m.filtered)-1 {
			m.idx++
		}

	case tea.KeyPgUp:
		vh := m.visibleHeight()
		m.idx -= vh
		if m.idx < 0 {
			m.idx = 0
		}

	case tea.KeyPgDown:
		vh := m.visibleHeight()
		m.idx += vh
		if m.idx >= len(m.filtered) {
			m.idx = len(m.filtered) - 1
		}

	case tea.KeyBackspace, tea.KeyDelete, tea.KeyHome, tea.KeyEnd:
		if m.filterEd == nil {
			m.filterEd = ui.NewTextEdit()
		}
		if ui.HandleFilterKey(m.filterEd, keyMsg) {
			m.applyFilter()
		}

	default:
		if m.filterEd == nil {
			m.filterEd = ui.NewTextEdit()
		}
		if ui.HandleFilterKey(m.filterEd, keyMsg) {
			m.applyFilter()
		}
	}

	return nil
}

func (m *App) toggleCommandPalette() (tea.Model, tea.Cmd) {
	if m.windowManager.Window("command-palette") != nil {
		m.overlayHost.Close(ui.OverlayCommandPalette)
		return m, nil
	}

	model := newCommandPaletteModel(m.Theme, func(cmdID string) tea.Cmd {
		m.overlayHost.Close(ui.OverlayCommandPalette)
		return m.executePaletteCommand(cmdID)
	}, m.actionRegistry)

	// 60 % of screen width, clamped to [60, 96]. Wider than the old
	// 50 %/80 because detail strings ("expand the file tree to the
	// active file", "search file names and contents", …) get
	// truncated at the narrower bound.
	palW := m.width * 60 / 100
	if palW < 60 {
		palW = 60
	}
	if palW > 96 {
		palW = 96
	}

	// Height scales with the number of registered commands so the
	// palette is usable without a filter even when there are 30+
	// entries. Chrome eats 4 rows (title + search + divider + a
	// breath of margin), so 26 rows renders ~22 commands.
	palH := 12 + len(model.items)
	if palH < 16 {
		palH = 16
	}
	if palH > 28 {
		palH = 28
	}
	if palH > m.height-6 {
		palH = m.height - 6
	}
	if palH < 6 {
		palH = 6
	}
	model.width = palW
	model.height = palH

	frame := ui.BuildCommandPaletteFrame(m.Theme, ui.CommandPaletteSpec{
		Items:    model.listItems(),
		Selected: 0,
		Width:    palW,
		Height:   palH,
	})

	fw := wm.NewFloatingWindow(string(ui.OverlayCommandPalette), "", frame,
		palW, palH,
		wm.WithDecoration(false),
		wm.WithResizable(false),
		wm.WithClosable(true),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	fw.SetPosition((m.width-palW)/2, 2)
	m.overlayHost.Open(ui.OverlayCommandPalette, fw, ui.DefaultOverlayPolicy(), func() {
		m.commandPaletteModel = nil
	})

	m.commandPaletteModel = model
	return m, nil
}

// refreshCommandPaletteFrame repaints the existing command-palette frame.
// Filtering updates List items at the mutation site; navigation must not call
// SetItems here because that resets selection to the first selectable row.
func (m *App) refreshCommandPaletteFrame() {
	if m.windowManager == nil || m.commandPaletteModel == nil {
		return
	}
	fw := m.windowManager.Window("command-palette")
	if fw == nil {
		return
	}
	fw.MarkContentDirty()
}

// handleCommandPaletteKey routes a keypress to the command palette frame.
// Text-editing keys go to the search TextField; navigation/Enter go to the List.
// Returns whether the frame should be refreshed and any command emitted by an
// activated palette action.
func (m *App) handleCommandPaletteKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if m.commandPaletteModel == nil {
		return false, nil
	}
	fw := m.windowManager.Window("command-palette")
	if fw == nil {
		return false, nil
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil {
		return false, nil
	}
	search := ui.CommandPaletteSearch(cw)
	list := ui.CommandPaletteList(cw)
	if search == nil || list == nil {
		return false, nil
	}

	// Navigation keys → List
	switch msg.Code {
	case tea.KeyUp:
		list.Handle(ui.EventContext{}, ui.KeyEvent(msg))
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyDown:
		list.Handle(ui.EventContext{}, ui.KeyEvent(msg))
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyPgUp:
		list.Handle(ui.EventContext{}, ui.KeyEvent(msg))
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyPgDown:
		list.Handle(ui.EventContext{}, ui.KeyEvent(msg))
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyHome:
		list.Handle(ui.EventContext{}, ui.KeyEvent(msg))
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyEnd:
		list.Handle(ui.EventContext{}, ui.KeyEvent(msg))
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyEnter:
		// Activate the selected command
		item, ok := list.Selected()
		if !ok || !item.Selectable() {
			return false, nil
		}
		entry, isEntry := item.Payload.(cmdEntry)
		if !isEntry {
			return false, nil
		}
		cb := m.commandPaletteModel.callback
		if cb != nil {
			return false, cb(entry.ID)
		}
		return false, nil
	}

	// All other keys (text editing, Backspace, etc.) → search field
	oldVal := search.Value()
	search.Handle(ui.EventContext{}, ui.KeyEvent(msg))
	if search.Value() != oldVal {
		m.commandPaletteModel.setFilter(search.Value())
		m.commandPaletteModel.applyFilter()
		list.SetItems(m.commandPaletteModel.listItems())
		return true, nil
	}
	return false, nil
}

// syncPaletteModelFromList updates m.idx from the List selection.
func (m *App) syncPaletteModelFromList(list *ui.List) {
	if list == nil || m.commandPaletteModel == nil {
		return
	}
	item, ok := list.Selected()
	if !ok {
		return
	}
	// Count selectable items before the selected one to get the flat item index.
	items := list.Items()
	count := 0
	for _, it := range items {
		if !it.Selectable() {
			continue
		}
		if it.ID == item.ID && it.Label == item.Label {
			m.commandPaletteModel.idx = count
			return
		}
		count++
	}
}

// cycleNextThemePreset advances the active theme to the next preset in
// theme.AllPresetNames and persists the choice. Wraps around. The
// applyThemePreset path also rewrites all chrome/host tokens and
// invalidates content caches, so the UI repaints with the new palette
// on the very next frame.
func (m *App) cycleNextThemePreset() {
	names := theme.AllPresetNames
	if len(names) == 0 {
		return
	}
	current := theme.PresetName(m.Theme)
	idx := 0
	for i, n := range names {
		if n == current {
			idx = i
			break
		}
	}
	next := names[(idx+1)%len(names)]
	m.applyThemePreset(next)
	// Persist the choice so the next launch uses the same preset.
	s := m.LoadAppSettings()
	s.Theme.Preset = next
	if err := m.SaveAppSettings(s); err != nil && m.Feedback != nil {
		m.Feedback.NotifyError("theme save failed: " + err.Error())
	}
	if m.Feedback != nil {
		m.Feedback.NotifyInfo("Theme: " + next)
	}
}

func (m *App) executePaletteCommand(cmdID string) tea.Cmd {
	if cmd, handled := m.handleMainPaneCommand(cmdID); handled {
		return cmd
	}
	var cmds []tea.Cmd

	switch cmdID {
	case "composer.toggle":
		m.toggleComposer()
		return nil

	case "settings.open":
		m.openApplicationSettings()

	case "settings.workspace":
		m.openWorkspaceSettings()
	case "status.overview":
		m.toggleWorkspacePanel(termdock.PanelWorkspace)
	case "lsp.start":
		m.startLSPForWorkspace()
	case "lsp.hover":
		return m.requestLSPHoverCmd()
	case "lsp.stop":
		m.StopLSP()
	case "lsp.restart":
		m.StopLSP()
		m.startLSPForWorkspace()
	case "lsp.problems":
		m.toggleWorkspacePanel(termdock.PanelProblems)

	case "agent.orchestrator.reset":
		m.resetOrchestratorOverrides()

	case "app.sessions":
		m.openSessionPicker()

	case "memory.browse":
		_, command := m.toggleMemoryBrowser()
		return command

	case "help.show":
		m.openHelp()

	case "system.undo":
		return m.executeSlashCommand("/undo")
	case "history.open":
		return m.openLocalHistory()
	case "history.verify":
		return m.verifyHistoryCmd()
	case "history.prune":
		return m.pruneHistoryCmd()
	case "history.snapshot":
		return m.captureHistoryCmd()
	case "history.location":
		return m.openHistoryLocationCmd()

	case "safety.yolo.toggle":
		return m.executeSlashCommand("/yolo")

	case "view.toggle.sidebar":
		m.ToggleSidebar()

	case "view.focus.sidebar":
		m.toggleFocusTree()

	case "view.focus.editor":
		// Real focus-editor path: only valid when an editor tab is open.
		// Clearing the stack and pushing FocusEditor makes the intent
		// explicit; focusTarget() returns FocusEditor.
		if m.EditorState == nil || m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("No editor open")
			}
			return nil
		}
		m.setFocusEditor()

	case "view.focus.chat":
		m.setFocusInput()

	case "view.activity.files":
		m.toggleActivity(activityFiles)
	case "view.activity.agents":
		m.toggleActivity(activityAgents)
	case "terminal.picker":
		m.openTerminalPicker()
	case "view.activity.skills":
		return m.toggleActivity(activitySkills)
	case "view.activity.mcp":
		return m.toggleActivity(activityMCP)
	case "skills.manage":
		return m.openCapabilityManager(activitySkills, "", "")
	case "capabilities.pick":
		m.openCapabilityPicker()
		return nil
	case "mcp.manage":
		return m.openCapabilityManager(activityMCP, "", "")
	case "board.browse":
		return m.openBoardBrowser()
	case "workspace.panel.hide":
		m.hideWorkspacePanel()
	case "workspace.panel.maximize":
		m.toggleWorkspacePanelMaximized()
	case "view.activity.git":
		m.toggleActivity(activityGit)
	case "terminal.new":
		return m.openNewTerminal()
	case "terminal.toggle":
		return m.toggleTerminalFocus()

	case "file.attach":
		m.openFilePicker()

	case "file.new":
		// startNewFileInTree requires a target directory. Prefer the
		// active editor's parent so the new file lands next to the
		// currently-open one. Fall back to the project root.
		dir := m.RootPath
		if m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen() {
			if fp := m.EditorState.Editor.FilePath(); fp != "" {
				dir = filepath.Dir(fp)
			}
		}
		m.startNewFileInTree(dir)

	case "file.save":
		return m.requestActiveEditorSave(workspace.DocumentSaveManual)
	case "editor.change.inspect":
		if ed := m.EditorState.Editor; ed != nil {
			m.inspectEditorChange(ed, ed.CursorLine())
		}
	case "editor.compareDisk", "editor.compareSaved":
		if ed := m.EditorState.Editor; ed != nil && ed.IsOpen() {
			if cmdID == "editor.compareSaved" {
				return m.openLocalDiff(ed, localDiffSaved)
			}
			return m.compareEditorWithDisk(ed)
		}
	case "editor.change.next":
		m.jumpEditorChange(1)
	case "editor.change.previous":
		m.jumpEditorChange(-1)
	case "editor.autosave.enable", "editor.autosave.disable":
		m.setWorkspaceAutosave(cmdID == "editor.autosave.enable")

	case "file.close":
		if m.isComparisonActive() {
			return m.handleAgentTabClose(m.Win.ActiveIndex())
		}
		m.closeEditorTab()

	case "file.revealInTree":
		if m.Tree.Model != nil && m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen() {
			if fp := m.EditorState.Editor.FilePath(); fp != "" {
				m.Tree.Model.ExpandToPath(fp)
				if !m.Tree.Visible {
					m.ToggleSidebar()
				}
			}
		}

	case "edit.find":
		if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("No file open to search")
			}
		} else {
			m.openFindReplace(false)
		}

	case "edit.replace":
		if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("No file open for replace")
			}
		} else {
			m.openFindReplace(true)
		}

	case "edit.undo":
		// Editor undo — legitimate palette command when an editor is
		// open. Mirrors the Ctrl+Z keyboard path (keyboard.go:146).
		if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("No file open to undo")
			}
		} else {
			m.EditorState.Editor.Undo()
		}

	case "edit.redo":
		// Editor redo — mirrors the Ctrl+Y keyboard path (keyboard.go:150).
		if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("No file open to redo")
			}
		} else {
			m.EditorState.Editor.Redo()
		}

	case "edit.moveLineUp", "edit.moveLineDown", "edit.duplicateLine", "edit.deleteLine", "edit.toggleComment":
		// Line operations forward the same chord the keyboard path uses,
		// so palette invocation and keyboard invocation cannot drift.
		if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("No file open")
			}
			return nil
		}
		var key tea.KeyPressMsg
		switch cmdID {
		case "edit.moveLineUp":
			key = tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt}
		case "edit.moveLineDown":
			key = tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt}
		case "edit.duplicateLine":
			key = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl | tea.ModShift}
		case "edit.deleteLine":
			key = tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl | tea.ModShift}
		case "edit.toggleComment":
			key = tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl}
		}
		m.EditorState.Editor.HandleEvent(key)

	case "lsp.outline":
		return m.requestDocumentSymbolsCmd()

	case "tabs.next":
		if m.Win != nil && m.Win.IsOpen() && m.Win.TabCount() > 0 {
			m.Win.Next()
			m.handleTabSwitch()
		}

	case "tabs.previous":
		if m.Win != nil && m.Win.IsOpen() && m.Win.TabCount() > 0 {
			m.Win.Prev()
			m.handleTabSwitch()
		}

	case "agent.cancel":
		if id := m.AgentMgr.ActiveID(); id != "" {
			m.cancelAgentTurn(id)
		}
	case "agent.retry":
		return m.retryAgentRecovery(m.AgentMgr.ActiveID())
	case "settings.network":
		m.openNetworkSettings()

	case "agent.newSession":
		return m.launcherNewSession()

	case "search.project.open":
		m.toggleFindInFiles()

	case "search.project.replace":
		m.toggleReplaceInFiles()

	case "agent.toggle.multiagent":
		s := m.LastSettings
		s.Agents.Enabled = !s.Agents.Enabled
		enabled := s.Agents.Enabled
		s.SessionMultiAgent = &enabled
		m.Feedback.NotifyInfo("Toggling multi-agent mode...")
		cmds = append(cmds, func() tea.Msg {
			err := m.HandleSettingsSave(s)
			return ttypes.SettingsSaveResultMsg{Settings: s, Err: err}
		})

	case "app.toggleTheme":
		m.cycleNextThemePreset()

	case "app.copyLastAssistant":
		ml := m.activeMsgList()
		if ml == nil {
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("No active chat to copy from")
			}
			break
		}
		text := ml.LastAssistantText()
		if text == "" {
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("No assistant message yet")
			}
			break
		}
		m.copyToClipboard(text)
		if m.Feedback != nil {
			m.Feedback.NotifyInfo(fmt.Sprintf("Copied %d chars", len(text)))
		}

	case "app.quit":
		// Route through the hierarchical quit ladder so it closes the
		// active tab / shows the launcher instead of hard-exiting while
		// tabs or agents are open. Same behavior as the Ctrl+Q binding.
		_, quitCmd := m.handleQuit()
		return quitCmd
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}
