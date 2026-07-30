package viewmodel

import (
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
	catTools  cmdCategory = "tools"
	catSystem cmdCategory = "system"
	catHelp   cmdCategory = "help"
	catAgent  cmdCategory = "agent"
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
		if a.ID == "palette.open" {
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
	case catTools:
		return "Tools"
	case catAgent:
		return "Agent"
	case catSystem:
		return "System"
	case catHelp:
		return "Help"
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

	palW := m.width * 50 / 100
	if palW < 50 {
		palW = 50
	}
	if palW > 80 {
		palW = 80
	}

	palH := 14
	if palH > m.height-8 {
		palH = m.height - 8
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
				if hint == "" || a.ID == "palette.open" {
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

	case "file.attach":
		m.openFilePicker()

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

	case "app.quit":
		m.Quit = true
		return tea.Quit
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}
