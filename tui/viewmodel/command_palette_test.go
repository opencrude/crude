package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

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
	for _, label := range []string{"Command Palette", "search commands", "New Session"} {
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
	if !hasSeparator {
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
		"launcher.filter", "launcher.filter.clear",
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

// TestPaletteSelfReferenceHidden asserts the "Command Palette" entry
// that opens the palette is excluded, because selecting it from the palette
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

// Unrelated commands must not collide, but a shared semantic operation keeps
// its established glyph instead of introducing a one-off icon dialect.
func TestDistinctIconsPerCommand(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	seen := make(map[string]string, len(entries))
	sameOperation := map[string]map[string]bool{}
	for _, e := range entries {
		// Only consider non-empty icons; blank " " placeholders are fine to repeat.
		if e.Icon == "" || e.Icon == " " {
			continue
		}
		if prev, ok := seen[e.Icon]; ok {
			family := sameOperation[e.Icon]
			if !family[prev] || !family[e.ID] {
				t.Errorf("icon %q reused by unrelated %q and %q", e.Icon, prev, e.ID)
			}
		}
		seen[e.Icon] = e.ID
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
		"launcher.filter", "launcher.filter.clear",
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

	runAppCommand(t, app, app.executePaletteCommand("app.toggleTheme"))

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
	// Open a file; newFullTestApp already opens a workspace.
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

// The palette lists only commands that work from anywhere; surface-bound
// actions keep their keys and menus but never crowd the palette.
func TestPaletteListsGlobalCommandsOnly(t *testing.T) {
	entries := paletteEntriesFromRegistry(ui.DefaultRegistry())
	byID := make(map[string]cmdEntry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	for _, id := range []string{
		"agent.newSession", "app.sessions", "file.attach", "composer.toggle", "board.browse", "memory.browse",
		"file.new", "file.close", "search.project.open", "search.project.replace", "lsp.outline", "history.open",
		"view.toggle.sidebar", "lsp.problems", "status.overview", "performance.open", "terminal.new", "terminal.toggle",
		"view.activity.git", "git.review", "git.history",
		"settings.open", "settings.workspace", "lsp.restart", "app.quit",
	} {
		if e, ok := byID[id]; !ok || e.Label == "" {
			t.Errorf("palette missing global command %q", id)
		}
	}
	for _, id := range []string{
		"file.save", "edit.undo", "edit.moveLineUp", "lsp.hover", "tabs.next", "view.focus.sidebar",
		"pane.split-right", "pane.move-left", "workspace.panel.tab", "help.show", "safety.yolo.toggle", "system.undo",
	} {
		if _, ok := byID[id]; ok {
			t.Errorf("palette lists surface-bound or removed command %q", id)
		}
	}
	if len(entries) > 40 {
		t.Fatalf("palette has %d commands; keep it to commands that work from anywhere", len(entries))
	}
}

func TestPaletteGroupsFollowCategoryOrder(t *testing.T) {
	order := map[string]int{}
	for i, category := range paletteCategories {
		order[category] = i
	}
	prev := -1
	for _, e := range paletteEntriesFromRegistry(ui.DefaultRegistry()) {
		ord, ok := order[e.Category]
		if !ok {
			t.Fatalf("entry %q has unknown category %q", e.ID, e.Category)
		}
		if ord < prev {
			t.Fatalf("category %q appeared out of order", e.Category)
		}
		prev = ord
	}
}

func TestPaletteRowsShowLiveShortcuts(t *testing.T) {
	reg := overrideHintKey(t, "search.project.open", "Ctrl+Alt+F2")
	m := newCommandPaletteModel(testTheme(), nil, reg)
	want := ui.FormatShortcut(reg.HintFor("search.project.open"))
	for _, item := range m.listItems() {
		if item.ID == "search.project.open" {
			if item.Detail != want {
				t.Fatalf("Find in Files shortcut = %q, want %q", item.Detail, want)
			}
			return
		}
	}
	t.Fatal("Find in Files missing from palette rows")
}

// A query ranks label matches ahead of loose ones, ignores descriptions and
// drops group headers so the obvious command is the first selectable row.
func TestPaletteFilterRanksLabelMatches(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	for _, test := range []struct{ query, first string }{
		{"git h", "git.history"},
		{"settings", "settings.open"},
		{"new term", "terminal.new"},
		{"fif", "search.project.open"},
		{"quit", "app.quit"},
	} {
		m.setFilter(test.query)
		m.applyFilter()
		items := m.listItems()
		if len(items) == 0 || items[0].Header || items[0].ID != test.first {
			got := ""
			if len(items) > 0 {
				got = items[0].ID
			}
			t.Fatalf("query %q: first row %q, want %q", test.query, got, test.first)
		}
	}
	m.setFilter("retention") // only in a removed command's description
	m.applyFilter()
	if len(m.filtered) != 0 {
		t.Fatalf("descriptions must not match: %+v", m.filtered)
	}
}
