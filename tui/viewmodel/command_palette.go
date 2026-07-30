package viewmodel

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/input"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/tui/widgets"
	"crude/tui/wm"
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

	frame := widgets.BuildCommandPaletteFrame(m.Theme, widgets.CommandPaletteSpec{
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
	search := widgets.CommandPaletteSearch(cw)
	list := widgets.CommandPaletteList(cw)
	if search == nil || list == nil {
		return false, nil
	}

	// Navigation keys → List
	switch msg.Code {
	case tea.KeyUp:
		list.Handle(ui.EventContext{}, msg)
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyDown:
		list.Handle(ui.EventContext{}, msg)
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyPgUp:
		list.Handle(ui.EventContext{}, msg)
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyPgDown:
		list.Handle(ui.EventContext{}, msg)
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyHome:
		list.Handle(ui.EventContext{}, msg)
		m.syncPaletteModelFromList(list)
		return true, nil
	case tea.KeyEnd:
		list.Handle(ui.EventContext{}, msg)
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
	search.Handle(ui.EventContext{}, msg)
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
	if err := m.SaveAppSettings(s); err != nil && m.StatusBar != nil {
		m.StatusBar.SetError("theme save failed: " + err.Error())
	}
	if m.StatusBar != nil {
		m.StatusBar.SetInfo("Theme: " + next)
	}
}

func (m *App) executePaletteCommand(cmdID string) tea.Cmd {
	var cmds []tea.Cmd

	switch cmdID {
	case "composer.toggle":
		m.toggleComposer()
		return nil

	case "settings.open":
		m.openWorkspaceSettings()

	case "agent.orchestrator.settings":
		m.openOrchestratorSettings()

	case "agent.orchestrator.reset":
		m.resetOrchestratorOverrides()

	case "app.sessions":
		m.openSessionPicker()

	case "help.show":
		var b strings.Builder
		b.WriteString("Available commands:\n")
		for _, c := range input.AvailableCmds() {
			b.WriteString("  " + "/" + c.Label + " - " + c.Desc + "\n")
		}
		b.WriteString("\nKeyboard Shortcuts:\n")
		if m.actionRegistry != nil {
			for _, a := range m.actionRegistry.All() {
				hint := m.actionRegistry.HintFor(a.ID)
				if hint == "" || a.ID == "palette.open" || a.DisplayOnly {
					continue
				}
				b.WriteString("  " + hint)
				b.WriteString(strings.Repeat(" ", max(0, 20-len(hint))))
				b.WriteString("- " + a.Label + "\n")
			}
		}
		if m.AgentMgr.ActiveID() != "" {
			m.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
		}

	case "system.undo":
		go m.HandleUndo("/undo")

	case "safety.yolo.toggle":
		if m.OverlayState.IsYolo() {
			m.OverlayState.SetYolo(false)
			m.StatusBar.SetInfo("YOLO mode disabled")
			go m.SetYolo(false)
		} else {
			m.openSessionYoloPanel()
		}

	case "view.toggle.sidebar":
		m.ToggleSidebar()

	case "view.focus.sidebar":
		m.toggleFocusTree()

	case "view.focus.editor":
		// Real focus-editor path: only valid when an editor tab is open.
		// Clearing the stack and pushing FocusEditor makes the intent
		// explicit; focusTarget() returns FocusEditor.
		if m.EditorState == nil || m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
			if m.StatusBar != nil {
				m.StatusBar.SetInfo("No editor open")
			}
			return nil
		}
		m.setFocusEditor()

	case "view.focus.chat":
		// Real focus-chat path: push FocusInput so focusTarget() returns
		// FocusInput even when an editor is open. This is the fix for the
		// old behavior where "Focus Chat" was a no-op with an editor open.
		m.setFocusInput()

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
		if m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen() {
			// Save() returns an error on write failure; surface it in
			// the status bar rather than silently dropping it. Mirrors
			// the dirty-prompt save path (dirty_prompt.go:63).
			if err := m.EditorState.Editor.Save(); err != nil {
				if m.StatusBar != nil {
					m.StatusBar.SetError(fmt.Sprintf("Save failed: %v", err))
				}
			} else if m.StatusBar != nil {
				m.StatusBar.SetInfo("Saved")
			}
		} else if m.StatusBar != nil {
			m.StatusBar.SetInfo("No file open to save")
		}

	case "file.close":
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
			if m.StatusBar != nil {
				m.StatusBar.SetInfo("No file open to search")
			}
		} else {
			m.openFindReplace(false)
		}

	case "edit.replace":
		if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
			if m.StatusBar != nil {
				m.StatusBar.SetInfo("No file open for replace")
			}
		} else {
			m.openFindReplace(true)
		}

	case "edit.undo":
		// Editor undo — legitimate palette command when an editor is
		// open. Mirrors the Ctrl+Z keyboard path (keyboard.go:146).
		if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
			if m.StatusBar != nil {
				m.StatusBar.SetInfo("No file open to undo")
			}
		} else {
			m.EditorState.Editor.Undo()
		}

	case "edit.redo":
		// Editor redo — mirrors the Ctrl+Y keyboard path (keyboard.go:150).
		if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
			if m.StatusBar != nil {
				m.StatusBar.SetInfo("No file open to redo")
			}
		} else {
			m.EditorState.Editor.Redo()
		}

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
			go m.CancelAgent(id)
			if m.StatusBar != nil {
				m.StatusBar.SetInfo("Cancelling agent " + id + "…")
			}
		}

	case "agent.newSession":
		return m.launcherNewSession()

	case "search.project.open":
		m.toggleFindInFiles()

	case "agent.toggle.multiagent":
		s := m.LastSettings
		s.Agents.Enabled = !s.Agents.Enabled
		m.StatusBar.SetInfo("Toggling multi-agent mode...")
		cmds = append(cmds, func() tea.Msg {
			err := m.HandleSettingsSave(s)
			return ttypes.SettingsSaveResultMsg{Settings: s, Err: err}
		})

	case "app.toggleTheme":
		m.cycleNextThemePreset()

	case "app.copyLastAssistant":
		ml := m.activeMsgList()
		if ml == nil {
			if m.StatusBar != nil {
				m.StatusBar.SetInfo("No active chat to copy from")
			}
			break
		}
		text := ml.LastAssistantText()
		if text == "" {
			if m.StatusBar != nil {
				m.StatusBar.SetInfo("No assistant message yet")
			}
			break
		}
		m.copyToClipboard(text)
		if m.StatusBar != nil {
			m.StatusBar.SetInfo(fmt.Sprintf("Copied %d chars", len(text)))
		}

	case "app.quit":
		m.Quit = true
		return tea.Quit
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}
