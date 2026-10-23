package viewmodel

import (
	"crude/tui/screens/gitpanel"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/termdock"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
)

// paletteCategories orders the palette's groups. Every palette entry belongs
// to one of them; an action with another category is a registry mistake and
// is left out rather than shown under an unlabeled group.
var paletteCategories = []string{"Session", "File", "View", "Git", "App"}

type cmdEntry struct {
	ID       string
	Label    string
	Shortcut string
	Category string
	Icon     string
}

// paletteEntriesFromRegistry lists the commands that make sense from
// anywhere, with the live shortcut that also runs them. Surface-bound actions
// (DisplayOnly hints, Contextual or FocusContext actions) stay with their
// surface's keys and menus.
func paletteEntriesFromRegistry(registry *ui.ActionRegistry) []cmdEntry {
	order := make(map[string]int, len(paletteCategories))
	for i, category := range paletteCategories {
		order[category] = i
	}
	var entries []cmdEntry
	for _, a := range registry.All() {
		if a.ID == "palette.open" || a.DisplayOnly || a.Contextual || a.FocusContext != "" {
			continue
		}
		if _, ok := order[a.Category]; !ok {
			continue
		}
		icon := a.Icon
		if icon == "" {
			icon = " "
		}
		entries = append(entries, cmdEntry{
			ID:       string(a.ID),
			Label:    a.Label,
			Shortcut: ui.FormatShortcut(registry.HintFor(a.ID)),
			Category: a.Category,
			Icon:     icon,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool { return order[entries[i].Category] < order[entries[j].Category] })
	return entries
}

type commandPaletteModel struct {
	th       *theme.Theme
	items    []cmdEntry
	filtered []cmdEntry
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

// applyFilter keeps category order for an empty query. A query matches only
// command labels (and the group name), never descriptions, and results are
// ranked so the obvious command is first: exact, prefix, word prefix,
// substring, multi-word, group name, then initials or an in-order
// subsequence of the label.
func (m *commandPaletteModel) applyFilter() {
	query := strings.ToLower(strings.TrimSpace(m.filterText()))
	if query == "" {
		m.filtered = append(m.filtered[:0], m.items...)
	} else {
		type ranked struct {
			entry cmdEntry
			score int
		}
		var matches []ranked
		for _, item := range m.items {
			if score, ok := paletteMatch(item, query); ok {
				matches = append(matches, ranked{item, score})
			}
		}
		sort.SliceStable(matches, func(i, j int) bool { return matches[i].score < matches[j].score })
		m.filtered = m.filtered[:0]
		for _, match := range matches {
			m.filtered = append(m.filtered, match.entry)
		}
	}
	if m.idx >= len(m.filtered) {
		m.idx = 0
	}
}

func paletteMatch(item cmdEntry, query string) (int, bool) {
	label := strings.ToLower(item.Label)
	words := strings.Fields(label)
	switch {
	case label == query:
		return 0, true
	case strings.HasPrefix(label, query):
		return 1, true
	}
	for _, word := range words {
		if strings.HasPrefix(word, query) {
			return 2, true
		}
	}
	if strings.Contains(label, query) {
		return 3, true
	}
	// "git hist", "file save": every term starts a word of the group or label.
	if terms := strings.Fields(query); len(terms) > 1 {
		candidates := append(words, strings.ToLower(item.Category))
		all := true
		for _, term := range terms {
			found := false
			for _, word := range candidates {
				if strings.HasPrefix(word, term) {
					found = true
					break
				}
			}
			if !found {
				all = false
				break
			}
		}
		if all {
			return 4, true
		}
	}
	if strings.HasPrefix(strings.ToLower(item.Category), query) {
		return 5, true
	}
	var initials strings.Builder
	for _, word := range words {
		initials.WriteString(word[:1])
	}
	if strings.HasPrefix(initials.String(), query) {
		return 6, true
	}
	if paletteSubsequence(label, query) {
		return 7, true
	}
	return 0, false
}

func paletteSubsequence(text, query string) bool {
	q := []rune(query)
	i := 0
	for _, r := range text {
		if i < len(q) && r == q[i] {
			i++
		}
	}
	return i == len(q)
}

// listItems groups the empty-query list under category headers; a filtered
// list is one ranked run with no headers, so the best match is always first.
func (m *commandPaletteModel) listItems() []ui.ListItem {
	items := make([]ui.ListItem, 0, len(m.filtered)+2*len(paletteCategories))
	grouped := strings.TrimSpace(m.filterText()) == ""
	current := ""
	for _, entry := range m.filtered {
		if grouped && entry.Category != current {
			if current != "" {
				items = append(items, ui.ListItem{Separator: true, Label: "─"})
			}
			items = append(items, ui.ListItem{Header: true, Label: entry.Category})
			current = entry.Category
		}
		items = append(items, ui.ListItem{
			ID:      entry.ID,
			Label:   entry.Label,
			Detail:  entry.Shortcut,
			Icon:    entry.Icon,
			Payload: entry,
		})
	}
	return items
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

	// Labels and shortcuts are short; half the screen, clamped to [52, 72].
	palW := m.width / 2
	if palW < 52 {
		palW = 52
	}
	if palW > 72 {
		palW = 72
	}
	if palW > m.width-4 {
		palW = m.width - 4
	}

	// Height scales with the command count; chrome eats 4 rows (title,
	// search, divider and a breath of margin).
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
	// The search field edits the model's filter TextEdit directly.
	if search := ui.CommandPaletteSearch(frame); search != nil {
		search.BindEdit(model.filterEd)
	}

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

// handleCommandPaletteInput routes a keypress or bracketed paste to the
// command palette frame. Navigation keys and Enter go to the List; text
// editing, clipboard chords and pastes go to the search TextField through the
// shared overlay text path. Returns whether the frame should be refreshed and
// any command emitted by an activated palette action or a clipboard chord.
func (m *App) handleCommandPaletteInput(input tea.Msg) (bool, tea.Cmd) {
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

	msg, isKey := input.(tea.KeyPressMsg)
	if !isKey {
		return m.commandPaletteText(fw, search, list, input)
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

	return m.commandPaletteText(fw, search, list, msg)
}

// commandPaletteText edits the palette query through the search TextField
// (bound to the model's filter TextEdit) and re-filters on change. A Ctrl+V
// result re-enters here while the same palette is still open.
func (m *App) commandPaletteText(fw *wm.FloatingWindow, search *ui.TextField, list *ui.List, msg tea.Msg) (bool, tea.Cmd) {
	model := m.commandPaletteModel
	deliver := func(content string) tea.Cmd {
		if m.commandPaletteModel != model {
			return nil
		}
		_, cmd := m.handleCommandPaletteInput(tea.PasteMsg{Content: content})
		return cmd
	}
	changed, cmd := m.routeOverlayText(fw, search, msg, deliver)
	if changed {
		model.applyFilter()
		list.SetItems(model.listItems())
	}
	return changed, cmd
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
func (m *App) cycleNextThemePreset() tea.Cmd {
	names := theme.AllPresetNames
	if len(names) == 0 {
		return nil
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
	before := m.ws.AppDefaults()
	s := types.CloneAppSettings(before)
	s.Theme.Preset = next
	return m.saveSettingsShortcut(types.SettingsScopeApplication, types.SettingsEdits(before, s), "Theme: "+next, nil, 0)
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
	case "lsp.restart":
		m.StopLSP()
		m.startLSPForWorkspace()
	case "lsp.problems":
		m.toggleWorkspacePanel(termdock.PanelProblems)

	case "app.sessions":
		m.openSessionPicker()

	case "memory.browse":
		_, command := m.toggleMemoryBrowser()
		return command

	case "performance.open":
		return m.togglePerformancePanel()

	case "history.open":
		return m.openLocalHistory()
	case "history.snapshot":
		return m.captureHistoryCmd()

	case "view.toggle.sidebar":
		m.ToggleSidebar()

	case "view.activity.workspaces":
		return m.toggleActivity(activityWorkspaces)
	case "terminal.picker":
		m.openTerminalPicker()
	case "board.browse":
		return m.openBoardBrowser()
	case "view.activity.git":
		return m.toggleGitPanel()
	case "git.review":
		return m.openGitReview("", "")
	case "git.history":
		return m.openGitPanel(gitpanel.ModeHistory)
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
		dir := m.ws.RootPath()
		if m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen() {
			if fp := m.EditorState.Editor.FilePath(); fp != "" {
				dir = filepath.Dir(fp)
			}
		}
		m.startNewFileInTree(dir)

	case "editor.privateRecovery":
		return m.OpenEditorRecoveryBrowser()

	case "file.close":
		if m.hostTabActive() {
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

	case "lsp.outline":
		return m.requestDocumentSymbolsCmd()

	case "agent.newSession":
		return m.launcherNewSession()

	case "search.project.open":
		m.toggleFindInFiles()

	case "search.project.replace":
		m.toggleReplaceInFiles()

	case "app.toggleTheme":
		cmds = append(cmds, m.cycleNextThemePreset())

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
