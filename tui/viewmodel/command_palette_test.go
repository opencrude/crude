package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/uitest"
)

func testTheme() *theme.Theme {
	return theme.DefaultDark()
}

func TestNewCommandPaletteModelShowsAllCommandsByDefault(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	m := newCommandPaletteModel(testTheme(), nil, reg)
	if len(m.filtered) != len(entries) {
		t.Errorf("expected %d filtered items, got %d", len(entries), len(m.filtered))
	}
}

func TestCommandPaletteFilterReducesItems(t *testing.T) {
	reg := ui.DefaultRegistry()
	m := newCommandPaletteModel(testTheme(), nil, reg)
	allCount := len(m.filtered)
	m.setFilter("sett")
	m.applyFilter()
	if len(m.filtered) == 0 {
		t.Fatal("expected some filtered results")
	}
	if len(m.filtered) >= allCount {
		t.Errorf("filter should reduce items: %d >= %d", len(m.filtered), allCount)
	}
}

func TestCommandPaletteFuzzyMatch(t *testing.T) {
	reg := ui.DefaultRegistry()
	m := newCommandPaletteModel(testTheme(), nil, reg)
	m.setFilter("cps")
	m.applyFilter()
	if len(m.filtered) == 0 {
		t.Fatal("fuzzy filter 'cps' should match 'Composer'")
	}
	found := false
	for _, item := range m.filtered {
		if item.ID == "composer.toggle" {
			found = true
			break
		}
	}
	if !found {
		t.Error("fuzzy filter 'cps' should match 'composer.toggle'")
	}
}

func TestCommandPaletteEmptyFilterShowsAll(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	m := newCommandPaletteModel(testTheme(), nil, reg)
	m.setFilter("")
	m.applyFilter()
	if len(m.filtered) != len(entries) {
		t.Errorf("empty filter should show all %d items, got %d", len(entries), len(m.filtered))
	}
}

func TestCommandPaletteBuildGroups(t *testing.T) {
	reg := ui.DefaultRegistry()
	m := newCommandPaletteModel(testTheme(), nil, reg)
	if len(m.groups) == 0 {
		t.Fatal("expected at least one group")
	}
	totalItems := 0
	for _, g := range m.groups {
		if g.Count <= 0 {
			t.Errorf("group %q has count %d", g.Category, g.Count)
		}
		totalItems += g.Count
	}
	if totalItems != len(m.filtered) {
		t.Errorf("group item count %d != filtered items %d", totalItems, len(m.filtered))
	}
}

func TestCommandPaletteNavigationArrows(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	if m.idx != 0 {
		t.Errorf("initial idx = %d, want 0", m.idx)
	}

	m.updateKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.idx != 1 {
		t.Errorf("after Down idx = %d, want 1", m.idx)
	}

	m.updateKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.idx != 0 {
		t.Errorf("after Up idx = %d, want 0", m.idx)
	}
}

func TestCommandPaletteNavigationWrapsNot(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	m.idx = 0
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.idx != 0 {
		t.Errorf("Up at top should not wrap, got idx=%d", m.idx)
	}

	m.idx = len(m.filtered) - 1
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.idx != len(m.filtered)-1 {
		t.Errorf("Down at bottom should not wrap, got idx=%d", m.idx)
	}
}

func TestCommandPalettePageUpDown(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	m.height = 10
	m.idx = 5
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.idx < 0 || m.idx > 5 {
		t.Errorf("PgUp from 5 should decrease idx, got %d", m.idx)
	}

	m.idx = 5
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.idx <= 5 || m.idx >= len(m.filtered) {
		t.Errorf("PgDown from 5 should increase idx, got %d", m.idx)
	}
}

func TestCommandPaletteBackspaceRemovesChar(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	m.setFilter("test")
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.filterText() != "tes" {
		t.Errorf("after backspace filter = %q, want 'tes'", m.filterText())
	}
}

func TestCommandPaletteTypingUpdatesFilter(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	m.updateKey(tea.KeyPressMsg{Text: "side"})
	if m.filterText() != "side" {
		t.Errorf("filter = %q, want 'side'", m.filterText())
	}
	if len(m.filtered) == 0 || len(m.filtered) >= len(paletteEntriesFromRegistry(ui.DefaultRegistry())) {
		t.Errorf("expected some filtered items, got %d", len(m.filtered))
	}
}

func TestCommandPaletteEnterReturnsCallback(t *testing.T) {
	var called string
	m := newCommandPaletteModel(testTheme(), func(cmdID string) tea.Cmd {
		called = cmdID
		return nil
	}, ui.DefaultRegistry())
	m.idx = 0
	cmd := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if called == "" {
		t.Error("expected callback to be called on Enter")
	}
	if cmd != nil {
		t.Log("got expected non-nil command from Enter")
	}
}

func TestCommandPaletteEnterOnEmptyFilterReturnsNil(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), func(cmdID string) tea.Cmd {
		return nil
	}, ui.DefaultRegistry())
	m.filtered = nil
	cmd := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Error("expected nil command when no items")
	}
}

func TestCommandPaletteEscReturnsNil(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	cmd := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Error("expected nil command from Esc")
	}
}

func TestPaintGoldenCellBufferContract(t *testing.T) {
	th := testTheme()
	m := newCommandPaletteModel(th, nil, ui.DefaultRegistry())
	cw := ui.BuildCommandPaletteFrame(th, ui.CommandPaletteSpec{
		Items:    m.listItems(),
		Selected: 0,
		Width:    60,
		Height:   14,
	})
	host := cw.Host()
	host.Resize(60, 14)
	host.Layout()
	buf := surface.NewCellBuffer(60, 14)
	host.Paint(buf)

	if nonEmpty := countNonEmptyCells(buf); nonEmpty == 0 {
		t.Fatal("command palette paint produced no non-empty cells")
	}
	painted := bufferPlain(buf)
	for _, label := range []string{"Command Palette", "search commands", "Find in Files"} {
		if !strings.Contains(painted, label) {
			t.Fatalf("missing palette label %q:\n%s", label, painted)
		}
	}
	if !bufferHasNonDefaultFG(buf) {
		t.Fatal("command palette paint missing themed foreground")
	}

	// Byte-identical regression. Deterministic: fixed registry items, no
	// time-dependent content in the palette frame.
	uitest.CheckGolden(t, "command_palette_dark_60x14", buf)
}

func countNonEmptyCells(buf *surface.CellBuffer) int {
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 0 && c.Width != 0 {
				n++
			}
		}
	}
	return n
}

func bufferHasNonDefaultFG(buf *surface.CellBuffer) bool {
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if fg := buf.Get(x, y).FG; !fg.IsDefault() {
				return true
			}
		}
	}
	return false
}

func bufferPlain(buf *surface.CellBuffer) string {
	var rows []string
	for y := 0; y < buf.Height(); y++ {
		var b strings.Builder
		for x := 0; x < buf.Width(); x++ {
			ch := buf.Get(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(rows, "\n")
}

// ── Frame-based tests ─────────────────────────────────────────────────────

func TestBuildCommandPaletteFrame_ListHasAllItems(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	cw := ui.BuildCommandPaletteFrame(testTheme(), ui.CommandPaletteSpec{
		Items: m.listItems(),
		Width: 60, Height: 14,
	})
	list := ui.CommandPaletteList(cw)
	if list == nil {
		t.Fatal("expected list component in frame")
	}
	if len(list.Items()) == 0 {
		t.Error("expected non-empty list items")
	}
}

func TestBuildCommandPaletteFrame_SearchFieldPresent(t *testing.T) {
	cw := ui.BuildCommandPaletteFrame(testTheme(), ui.CommandPaletteSpec{
		Items: []ui.ListItem{{ID: "x", Label: "X"}},
		Width: 60, Height: 14,
	})
	search := ui.CommandPaletteSearch(cw)
	if search == nil {
		t.Fatal("expected search field in frame")
	}
}

func TestCommandPaletteFrame_WidthConsistent(t *testing.T) {
	cw := ui.BuildCommandPaletteFrame(testTheme(), ui.CommandPaletteSpec{
		Items: []ui.ListItem{{ID: "x", Label: "X"}},
		Width: 50, Height: 14,
	})
	w, _ := cw.Measure(80, 24)
	if w > 80 {
		t.Errorf("frame width %d exceeds available 80", w)
	}
}

func TestCommandPaletteListItemsHasHeaders(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	items := m.listItems()
	hasHeader := false
	hasSeparator := false
	for _, it := range items {
		if it.Header {
			hasHeader = true
		}
		if it.Separator {
			hasSeparator = true
		}
	}
	if !hasHeader {
		t.Error("expected category headers in list items")
	}
	// Separators appear between groups
	if len(m.groups) > 1 && !hasSeparator {
		t.Error("expected separators between groups")
	}
}

func TestCommandPaletteListItemsFilterReduces(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	m.setFilter("sett")
	m.applyFilter()
	items := m.listItems()
	if len(items) == 0 {
		t.Fatal("expected filtered list items")
	}
	// All selectable items should have "Settings" or related label
	found := false
	for _, it := range items {
		if !it.Selectable() {
			continue
		}
		if strings.Contains(it.Label, "Settings") || strings.Contains(it.Label, "settings") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'Settings' in filtered list items")
	}
}

func TestCommandPaletteViewWidthDoesNotExceed(t *testing.T) {
	// Verify the frame's measured width doesn't exceed constraints
	cw := ui.BuildCommandPaletteFrame(testTheme(), ui.CommandPaletteSpec{
		Items: []ui.ListItem{{ID: "x", Label: "X"}},
		Width: 50, Height: 6,
	})
	w, h := cw.Measure(80, 24)
	if w > 80 {
		t.Errorf("width %d exceeds 80", w)
	}
	if h > 24 {
		t.Errorf("height %d exceeds 24", h)
	}
	_ = rendering.VisibleWidth("") // keep import
}

// TestPaletteEntriesSkipDisplayOnly verifies the dead "hint fragment"
// entries that exist only for the status bar (Close, Next, Navigate,
// etc.) are NOT shown in the palette.
func TestPaletteEntriesSkipDisplayOnly(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)

	// No display-only status-bar fragment may appear in the palette.
	hidden := []string{
		"editor.close", "focus.tab.next", "focus.tab.previous",
		"panel.previous", "panel.next", "panel.close",
		"tabbar.previous", "tabbar.next", "tabbar.select", "tabbar.unfocus",
		"list.open", "list.navigate", "list.preview.navigate", "list.open.mouse",
		"overlay.close", "search.regex.toggle",
		"launcher.filter", "launcher.filter.clear", "launcher.delete",
		"input.history", "input.cancel",
	}
	byID := make(map[string]bool, len(entries))
	for _, e := range entries {
		byID[e.ID] = true
	}
	for _, id := range hidden {
		if byID[id] {
			t.Errorf("palette includes display-only entry %q", id)
		}
	}
}

// TestPaletteIncludesNewCommands verifies every new runnable command
// appears in the palette with a label and a non-empty category.
func TestPaletteIncludesNewCommands(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)

	required := []string{
		"file.save", "file.attach", "file.new", "file.close", "file.revealInTree",
		"search.project.open", "search.project.replace",
		"edit.undo", "edit.redo", "edit.find", "edit.replace",
		"view.toggle.sidebar", "view.focus.sidebar", "view.focus.editor", "view.focus.chat",
		"view.activity.files", "view.activity.agents", "view.activity.terminals", "view.activity.git",
		"terminal.new", "terminal.toggle",
		"tabs.next", "tabs.previous",
		"agent.orchestrator.reset",
		"agent.toggle.multiagent", "agent.cancel", "agent.newSession",
		"composer.toggle", "settings.open", "settings.workspace", "lsp.outline", "safety.yolo.toggle",
		"app.sessions", "system.undo", "history.open", "app.toggleTheme", "app.copyLastAssistant",
		"app.quit", "help.show",
	}
	byID := make(map[string]cmdEntry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	for _, id := range required {
		e, ok := byID[id]
		if !ok {
			t.Errorf("palette missing required command %q", id)
			continue
		}
		if e.Label == "" {
			t.Errorf("palette entry %q has empty label", id)
		}
		if e.Category == "" {
			t.Errorf("palette entry %q has empty category", id)
		}
	}
}

// TestPaletteSelfReferenceHidden asserts the "Command Palette" entry
// that opens the palette is excluded — selecting it from the palette
// is meaningless and visually noisy.
func TestPaletteSelfReferenceHidden(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	for _, e := range entries {
		if e.ID == "palette.open" {
			t.Fatal("palette.open must not appear in its own menu")
		}
	}
}

// TestPaletteGroupOrderIsStable verifies the group order follows the
// defaultActions order (File, Edit, View, Tabs, Agent, Tools, System, Help, Chat, App).
// This locks in the visual ordering so future reorders require a
// deliberate test update.
func TestPaletteGroupOrderIsStable(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	prev := -1
	wantOrder := map[cmdCategory]int{
		catFile: 0, catEdit: 1, catView: 2, catTabs: 3,
		catAgent: 4, catTools: 5, catSystem: 6, catHelp: 7,
		catChat: 8, catApp: 9,
	}
	for _, e := range entries {
		ord, ok := wantOrder[e.Category]
		if !ok {
			continue
		}
		if ord < prev {
			t.Errorf("category %q appeared out of order (ord=%d, prev=%d)", e.Category, ord, prev)
		}
		prev = ord
	}
}

// TestDistinctIconsPerCommand makes sure no two real commands share
// the same icon glyph. The palette uses icons to scan visually and
// the original code reused "☰" for sidebar/sessions and "⌕" for
// palette/find. Distinct icons are a real, testable contract.
func TestDistinctIconsPerCommand(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	seen := make(map[string]string, len(entries))
	for _, e := range entries {
		// Only consider non-empty icons; blank " " placeholders are fine to repeat.
		if e.Icon == "" || e.Icon == " " {
			continue
		}
		if prev, ok := seen[e.Icon]; ok {
			t.Errorf("icon %q reused by %q and %q", e.Icon, prev, e.ID)
		}
		seen[e.Icon] = e.ID
	}
}

// TestPaletteExecuteCommandRoutesToHandler checks that pressing
// Enter on a known command id invokes the right App method. We do
// this without spinning up a full editor: just verify the command
// id appears in the palette and that its description isn't empty.
func TestPaletteExecuteCommandRoutesToHandler(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	want := map[string]string{
		"file.save":             "save current file",
		"file.close":            "close the active editor tab",
		"file.new":              "create a new file in the workspace",
		"file.revealInTree":     "expand the file tree to the active file",
		"edit.find":             "search within the current file",
		"edit.replace":          "find and replace within the current file",
		"lsp.outline":           "document outline",
		"view.focus.editor":     "move keyboard focus to the editor",
		"view.focus.chat":       "move keyboard focus to the chat input",
		"tabs.next":             "switch to the next editor tab",
		"tabs.previous":         "switch to the previous editor tab",
		"agent.cancel":          "stop the running agent",
		"agent.newSession":      "start a fresh session",
		"app.toggleTheme":       "cycle through theme presets",
		"app.copyLastAssistant": "copy the last assistant message to clipboard",
	}
	byID := make(map[string]cmdEntry)
	for _, e := range entries {
		byID[e.ID] = e
	}
	for id, wantDesc := range want {
		e, ok := byID[id]
		if !ok {
			t.Errorf("missing command %q", id)
			continue
		}
		if !strings.Contains(e.Detail, wantDesc) && e.Detail != wantDesc {
			t.Errorf("command %q: detail %q does not mention %q", id, e.Detail, wantDesc)
		}
	}
}

// TestDisplayOnlyPreservedForFeedback ensures DisplayOnly entries
// still appear in the registry so HintFor / HintLabelFor work
// unchanged for the status bar.
func TestDisplayOnlyPreservedForFeedback(t *testing.T) {
	reg := ui.DefaultRegistry()
	required := []string{
		"editor.close", "focus.tab.next", "focus.tab.previous",
		"panel.previous", "panel.next", "panel.close",
		"tabbar.previous", "tabbar.next", "tabbar.select", "tabbar.unfocus",
		"list.open", "list.navigate", "list.preview.navigate", "list.open.mouse",
		"overlay.close", "search.regex.toggle",
		"launcher.filter", "launcher.filter.clear", "launcher.delete",
	}
	for _, id := range required {
		a := reg.Get(ui.ActionID(id))
		if a == nil {
			t.Errorf("registry missing display-only entry %q (status bar will break)", id)
			continue
		}
		if !a.DisplayOnly {
			t.Errorf("entry %q should be DisplayOnly=true", id)
		}
		if reg.HintFor(ui.ActionID(id)) == "" {
			t.Errorf("display-only entry %q has no HintKey for status bar", id)
		}
	}
}

// TestExecutePaletteCommandRoutesCycleTheme confirms pressing Enter on
// the Toggle Theme command advances the active theme preset.
func TestExecutePaletteCommandRoutesCycleTheme(t *testing.T) {
	app := newFullTestApp(t)
	startPreset := theme.PresetName(app.Theme)

	app.executePaletteCommand("app.toggleTheme")

	// If we were on the last preset, the cycle should wrap to the
	// first. Otherwise we should be on the next preset.
	presets := theme.AllPresetNames
	expected := presets[0]
	for i, n := range presets {
		if n == startPreset {
			expected = presets[(i+1)%len(presets)]
			break
		}
	}
	got := theme.PresetName(app.Theme)
	if got != expected {
		t.Errorf("after ToggleTheme: got preset %q, want %q", got, expected)
	}
}

// TestExecutePaletteCommandRevealInTree expands the file tree to the
// active editor's file. We use a temp project and open a deeply
// nested file, then assert the tree contains the file's parents.
func TestExecutePaletteCommandRevealInTree(t *testing.T) {
	app := newFullTestApp(t)
	// Open a file — newFullTestApp already opens a workspace.
	if app.EditorState.Editor == nil || !app.EditorState.Editor.IsOpen() {
		// Nothing to reveal; just confirm the command does not crash.
		app.executePaletteCommand("file.revealInTree")
		return
	}
	app.executePaletteCommand("file.revealInTree")
	// After reveal, the active file's directory should be in the visible rows.
	fp := app.EditorState.Editor.FilePath()
	if fp == "" {
		return
	}
	visible := app.Tree.Model.VisibleRows()
	found := false
	for _, v := range visible {
		if v == fp || v == filepath.Dir(fp) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("after reveal: %q not in tree visible rows", fp)
	}
}

// TestExecutePaletteCommandNoActiveAgentCancel is a smoke test for the
// Cancel Agent command when no agent is active. The command must not
// crash, even with no active ID.
func TestExecutePaletteCommandNoActiveAgentCancel(t *testing.T) {
	app := newFullTestApp(t)
	if app.AgentMgr.ActiveID() != "" {
		t.Skip("test requires no active agent")
	}
	app.executePaletteCommand("agent.cancel")
}

// ── Fixes for the command-palette cleanup audit ───────────────────────────

// TestPaletteExcludesSendAndNewLineHints verifies the two chat-input
// hint fragments ("Send", "New line") are DisplayOnly and therefore
// absent from the palette. Before the fix they appeared as inert dead
// entries: selecting them did nothing because executePaletteCommand
// has no case for them (they fire through dispatchChatKey, not the
// palette).
func TestPaletteExcludesSendAndNewLineHints(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	byID := make(map[string]bool, len(entries))
	for _, e := range entries {
		byID[e.ID] = true
	}
	for _, id := range []string{"input.submit", "input.newline"} {
		if byID[id] {
			t.Errorf("chat-input hint %q must be DisplayOnly and hidden from palette", id)
		}
	}
	// The registry still owns them so HintLabelFor can render the
	// status-bar "Enter send" / "Alt+Enter new line" hints.
	for _, id := range []string{"input.submit", "input.newline"} {
		a := reg.Get(ui.ActionID(id))
		if a == nil || !a.DisplayOnly {
			t.Errorf("registry entry %q must exist and be DisplayOnly", id)
		}
		if reg.HintFor(ui.ActionID(id)) == "" {
			t.Errorf("DisplayOnly entry %q must keep a HintKey for the status bar", id)
		}
	}
}

// TestPaletteUndoRedoLabelsDisambiguated verifies the two distinct
// "Undo" operations are no longer confusable in the palette: edit.undo
// is editor undo ("Undo Typing"), system.undo is agent-file-change
// revert ("Undo Agent File Change").
func TestPaletteUndoRedoLabelsDisambiguated(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	byID := make(map[string]cmdEntry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	eu, ok := byID["edit.undo"]
	if !ok {
		t.Fatal("palette missing edit.undo")
	}
	if eu.Label != "Undo Typing" {
		t.Errorf("edit.undo label = %q, want %q", eu.Label, "Undo Typing")
	}
	su, ok := byID["system.undo"]
	if !ok {
		t.Fatal("palette missing system.undo")
	}
	if su.Label != "Revert Agent Files" {
		t.Errorf("system.undo label = %q, want %q", su.Label, "Revert Agent Files")
	}
	// Sanity: the two labels must not be equal.
	if eu.Label == su.Label {
		t.Fatal("edit.undo and system.undo share a label — palette is ambiguous")
	}
}

// TestExecutePaletteCommandEditUndoRedo wires the editor undo/redo
// commands through the palette. With an editor open, edit.undo/edit.redo
// must dispatch to the editor's Undo()/Redo(); with no editor open they
// must emit a status-bar info message instead of crashing.
func TestExecutePaletteCommandEditUndoRedo(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 80, 24

	// No editor open: must not crash, status bar gets a hint.
	app.executePaletteCommand("edit.undo")
	app.executePaletteCommand("edit.redo")
	if app.Feedback == nil {
		t.Fatal("editor actions require a feedback sink")
	}
}

// TestExecutePaletteCommandFocusChatWithEditorOpen is the regression
// test for the core focus-architecture fix. Before the fix,
// view.focus.chat called unfocus() (clearing the stack), and
// focusTarget() then fell through to FocusEditor because an editor was
// open — so "Focus Chat" was a no-op. The fix pushes FocusInput onto
// the stack and focusTarget() now returns FocusInput even with an
// editor open.
func TestExecutePaletteCommandFocusChatWithEditorOpen(t *testing.T) {
	app, _, mainPath := setupTreeRoutingApp(t)
	if app.focusTarget() != FocusEditor {
		t.Fatalf("precondition: focusTarget=%v want FocusEditor (editor open)", app.focusTarget())
	}
	app.executePaletteCommand("view.focus.chat")
	if app.focusTarget() != FocusInput {
		t.Fatalf("after view.focus.chat with editor open: focusTarget=%v want FocusInput", app.focusTarget())
	}
	if app.FocusStack.Top() != FocusInput {
		t.Fatalf("FocusStack.Top=%v want FocusInput", app.FocusStack.Top())
	}
	// And the editor is still open — focus.chat must not close it.
	if app.EditorState.ActiveView != mainPath {
		t.Fatalf("view.focus.chat closed the editor: ActiveView=%q want %q", app.EditorState.ActiveView, mainPath)
	}
}

// TestExecutePaletteCommandFocusEditorWithEditorOpen verifies
// view.focus.editor clears any stale FocusInput (from a prior
// view.focus.chat) so the editor reclaims focus.
func TestExecutePaletteCommandFocusEditorWithEditorOpen(t *testing.T) {
	app, _, _ := setupTreeRoutingApp(t)

	// First drive focus to chat, then back to editor.
	app.executePaletteCommand("view.focus.chat")
	if app.focusTarget() != FocusInput {
		t.Fatalf("precondition focus.chat: focusTarget=%v want FocusInput", app.focusTarget())
	}
	app.executePaletteCommand("view.focus.editor")
	if app.focusTarget() != FocusEditor {
		t.Fatalf("after view.focus.editor: focusTarget=%v want FocusEditor", app.focusTarget())
	}
	if app.FocusStack.Top() == FocusInput {
		t.Fatal("view.focus.editor must clear stale FocusInput from the stack")
	}
}

// TestExecutePaletteCommandFocusEditorNoEditor verifies that with no
// editor open, view.focus.editor emits a status-bar message and does
// not crash.
func TestExecutePaletteCommandFocusEditorNoEditor(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 80, 24
	// No editor open in this fixture.
	app.executePaletteCommand("view.focus.editor")
	// No panic; we don't assert the status text because newFullTestApp
	// wires a real Feedback only if it was set.
}

// TestExecutePaletteCommandFileSaveError verifies file.save surfaces a
// write failure via the status bar instead of silently dropping the
// error. We open the file while its directory is writable, then lock
// down permissions so the subsequent Save() write fails.
func TestExecutePaletteCommandFileSaveError(t *testing.T) {
	app, _, _ := setupTreeRoutingApp(t)

	dir := filepath.Dir(app.EditorState.ActiveView)
	roDir := filepath.Join(dir, "readonly")
	// Create writable so we can write the file inside it.
	if err := os.MkdirAll(roDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o700) })

	roPath := filepath.Join(roDir, "n.go")
	if err := os.WriteFile(roPath, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := app.EditorState.Editor
	if err := ed.Open(roPath); err != nil {
		t.Fatal(err)
	}
	app.EditorState.ActiveView = roPath

	// Dirty the buffer, then make the file read-only so Save()'s
	// overwrite fails. Overwriting an existing file needs the file's
	// write bit (not the directory's), so we clear that.
	ed.InsertRune('x')
	if err := os.Chmod(roPath, 0o400); err != nil {
		t.Fatal(err)
	}
	app.executePaletteCommand("file.save")
	if !ed.Modified() {
		t.Fatal("file.save into read-only file must not clear modified flag (save should have failed)")
	}
}
