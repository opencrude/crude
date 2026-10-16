package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/tui/window"
	ttypes "crude/types"
)

func paneJourneySize(app *App, width, height int) {
	app.Tree.Visible = false
	_, _ = app.Update(tea.WindowSizeMsg{Width: width, Height: height})
	_ = app.View()
}

func paneJourneyKey(app *App, code rune, mod tea.KeyMod) {
	key := tea.KeyPressMsg{Code: code, Mod: mod}
	if mod == 0 && unicode.IsPrint(code) {
		key.Text = string(code)
	}
	_, _ = app.Update(key)
	_ = app.View()
}

func paneJourneyText(app *App, rect ui.Rect) string {
	var text strings.Builder
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		for x := rect.X; x < rect.X+rect.W; x++ {
			c := app.mainSurf.Buffer().Get(x, y)
			if c.Width > 0 && c.Char != 0 {
				text.WriteRune(c.Char)
			}
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func paneJourneyRequireText(t *testing.T, app *App, group window.GroupID, want string) {
	t.Helper()
	g := app.mainPanes.groups[group]
	if g == nil || !strings.Contains(paneJourneyText(app, g.body), want) {
		t.Fatalf("pane %d does not paint %q in its body", group, want)
	}
}

func paneJourneyDrag(t *testing.T, app *App, tab window.TabID, x, y int) {
	t.Helper()
	g := app.mainPanes.groups[app.Win.GroupOf(tab)]
	r, ok := app.rootHost.ScreenRect(g.tabs.TabsControl().ID())
	if !ok {
		t.Fatal("source tab control is not mounted")
	}
	index := -1
	for i, candidate := range app.Win.GroupTabs(g.id) {
		if candidate.ID == tab {
			index = i
		}
	}
	pressX := -1
	for local := 0; local < r.W; local++ {
		i, closeHit := g.tabs.TabsControl().HitTest(local)
		if i == index && !closeHit {
			pressX = r.X + local
			break
		}
	}
	if pressX < 0 {
		t.Fatal("source tab has no visible non-close hit target")
	}
	_, _ = app.Update(tea.MouseClickMsg{X: pressX, Y: r.Y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
	_ = app.View()
}

func TestMainPaneJourneyEditorDragPreviewCancelAndCommit(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "left_marker.go", "right_marker.go")
	_ = app.View()
	original := app.Win.ActiveGroup()
	tab := app.Win.TabByTitle(app.editorTabTitle(paths[1])).ID
	body := app.mainPanes.groups[original].body
	x, y := body.X+body.W-2, body.Y+body.H/2
	before := app.Win.GroupTabs(original)
	paneJourneyDrag(t, app, tab, x, y)
	if len(app.Win.GroupIDs()) != 1 || !reflect.DeepEqual(app.Win.GroupTabs(original), before) {
		t.Fatal("preview changed topology or tab membership before release")
	}
	if !bufferContainsPlain(app.mainSurf.Buffer(), "Split right") {
		t.Fatal("right-edge drag did not paint its split preview")
	}
	preview := app.mainSurf.Buffer().Get(x, y)
	if preview.BG != app.rootHost.Tokens().Color(ui.ColorHover) {
		t.Fatal("drop preview does not use themed hover fill")
	}
	paneJourneyKey(app, tea.KeyEscape, 0)
	_, _ = app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	_ = app.View()
	if len(app.Win.GroupIDs()) != 1 || !reflect.DeepEqual(app.Win.GroupTabs(original), before) || bufferContainsPlain(app.mainSurf.Buffer(), "Split right") {
		t.Fatal("Escape did not cancel preview without changing membership")
	}
	paneJourneyDrag(t, app, tab, x, y)
	_, _ = app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	_ = app.View()
	ids := app.Win.GroupIDs()
	if len(ids) != 2 || app.Win.GroupOf(tab) != ids[1] {
		t.Fatalf("right-edge release did not move existing tab into right pane: %v", ids)
	}
	paneJourneyRequireText(t, app, ids[0], "package left_marker.go")
	paneJourneyRequireText(t, app, ids[1], "package right_marker.go")
	if strings.Contains(paneJourneyText(app, app.mainPanes.groups[ids[0]].body), "package right_marker.go") {
		t.Fatal("right editor leaked into left pane")
	}
}

func TestMainPaneJourneyEmptySplitFocusEditingAndMerge(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "retained_marker.go")
	_ = app.View()
	original := app.Win.ActiveGroup()
	ed := app.EditorState.Editor
	saved := ed.Value()
	paneJourneyKey(app, 'x', 0)
	edited, line, col := ed.Value(), ed.CursorLine(), ed.CursorCol()
	if edited == saved || !ed.Modified() {
		t.Fatal("production key routing did not create a dirty editor")
	}
	paneJourneyKey(app, tea.KeyRight, tea.ModCtrl|tea.ModAlt)
	empty := app.Win.ActiveGroup()
	if empty == original || app.Win.ActiveTab() != nil || len(app.Win.GroupIDs()) != 2 {
		t.Fatal("split shortcut did not focus a genuinely empty pane")
	}
	paneJourneyRequireText(t, app, empty, "New Session")
	paneJourneyRequireText(t, app, empty, "Open File")
	paneJourneyKey(app, '[', tea.ModAlt)
	if app.Win.ActiveGroup() != original || app.EditorState.Editor != ed || app.focusTarget() != FocusEditor {
		t.Fatal("previous-pane shortcut did not restore original editor focus")
	}
	paneJourneyKey(app, 'y', 0)
	if ed.Value() == edited {
		t.Fatal("typing after pane focus did not edit original document")
	}
	paneJourneyKey(app, 'z', tea.ModCtrl)
	if ed.Value() != edited {
		t.Fatal("undo after pane focus did not affect original document")
	}
	tab := app.Win.TabByTitle(app.editorTabTitle(paths[0])).ID
	if !app.Win.MoveTabToGroup(tab, empty, 0) {
		t.Fatal("move failed")
	}
	app.focusMainGroup(empty)
	_ = app.View()
	if app.EditorState.Editor != ed || ed.Value() != edited || !ed.Modified() || ed.CursorLine() != line || ed.CursorCol() != col {
		t.Fatal("moving tab lost live editor identity, bytes, dirty state or cursor")
	}
	app.Win.SplitGroup(empty, ui.Vertical, false)
	app.Win.MergeAllGroups()
	app.focusMainGroup(app.Win.ActiveGroup())
	_ = app.View()
	if app.EditorState.Editor != ed || ed.Value() != edited || !ed.Modified() {
		t.Fatal("merging recreated or saved the live editor")
	}
	paneJourneyKey(app, 'z', tea.ModCtrl)
	if ed.Value() != saved || ed.Modified() {
		t.Fatal("pre-move undo/savepoint did not survive move and merge")
	}
}

func TestMainPaneJourneyNestedDividersAndCompactKeyboard(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 50)
	for _, marker := range []string{"west", "north", "south"} {
		path := filepath.Join(app.ws.RootPath(), marker+"_marker.go")
		if err := os.WriteFile(path, []byte(marker+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paths := openEditorTabs(t, app, "west_marker.go", "north_marker.go", "south_marker.go")
	first := app.Win.ActiveGroup()
	right := app.Win.SplitGroup(first, ui.Horizontal, false)
	app.Win.MoveTabToGroup(app.Win.TabByTitle(app.editorTabTitle(paths[1])).ID, right, 0)
	bottom := app.Win.SplitGroup(right, ui.Vertical, false)
	app.Win.MoveTabToGroup(app.Win.TabByTitle(app.editorTabTitle(paths[2])).ID, bottom, 0)
	app.focusMainGroup(bottom)
	_ = app.View()
	root := app.Win.PaneRoot()
	if root.Split.Dir() != ui.Horizontal || root.Second.Split == nil || root.Second.Split.Dir() != ui.Vertical {
		t.Fatal("nested side-by-side and stacked topology was lost")
	}
	for _, node := range []*window.PaneNode{root, root.Second} {
		r, ok := app.rootHost.ScreenRect(app.mainPanes.dividers[node.ID].ID())
		if !ok {
			t.Fatal("nested divider is not mounted")
		}
		_, _ = app.Update(tea.MouseClickMsg{X: r.X, Y: r.Y, Button: tea.MouseLeft})
		_, _ = app.Update(tea.MouseMotionMsg{X: 1000, Y: 1000, Button: tea.MouseLeft})
		_, _ = app.Update(tea.MouseReleaseMsg{X: 1000, Y: 1000, Button: tea.MouseLeft})
		_ = app.View()
		for _, id := range app.Win.GroupIDs() {
			placement := app.mainPanes.placements[id]
			if placement.W < 24 || placement.H < 7 {
				t.Fatalf("divider dragged pane below usable bounds: %+v", placement)
			}
		}
	}
	ids := app.Win.GroupIDs()
	paneJourneySize(app, 42, 18)
	if !reflect.DeepEqual(app.Win.GroupIDs(), ids) || !app.mainPanes.compact {
		t.Fatal("narrow resize destroyed topology instead of compacting")
	}
	for range len(ids) {
		group := app.Win.ActiveGroup()
		selected := app.Win.GroupActiveTab(group)
		if selected == nil {
			t.Fatal("compact pane lost its selected resource")
		}
		marker := strings.TrimSuffix(selected.DisplayTitle, "_marker.go")
		paneJourneyRequireText(t, app, group, marker)
		paneJourneyKey(app, ']', tea.ModAlt)
	}
	paneJourneySize(app, 180, 50)
	paneJourneyRequireText(t, app, first, "west")
	paneJourneyRequireText(t, app, right, "north")
	paneJourneyRequireText(t, app, bottom, "south")
}

func TestMainPaneJourneyAgentsAndEditorsKeepIndependentSelection(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	app.AddOrchestrator("alpha")
	app.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: "ALPHA_TRANSCRIPT_MARKER", Time: time.Unix(1, 0)})
	alpha := app.Win.ActiveTab().ID
	left := app.Win.ActiveGroup()
	paths := openEditorTabs(t, app, "DOCUMENT_MARKER.go")
	doc := app.Win.TabByTitle(app.editorTabTitle(paths[0])).ID
	right := app.Win.SplitGroup(left, ui.Horizontal, false)
	app.Win.MoveTabToGroup(doc, right, 0)
	app.focusMainGroup(right)
	_ = app.View()
	paneJourneyRequireText(t, app, left, "ALPHA_TRANSCRIPT_MARKER")
	paneJourneyRequireText(t, app, right, "DOCUMENT_MARKER")
	app.syncAgentWindowTabs()
	_ = app.View()
	if app.Win.ActiveTab().ID != doc || app.Win.GroupActiveTab(left).ID != alpha {
		t.Fatal("painting/background sync stole an independently selected resource")
	}
	app.AddOrchestrator("beta")
	app.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: "BETA_TRANSCRIPT_MARKER", Time: time.Unix(2, 0)})
	beta := app.Win.ActiveTab().ID
	_ = app.View()
	paneJourneyRequireText(t, app, left, "ALPHA_TRANSCRIPT_MARKER")
	paneJourneyRequireText(t, app, right, "BETA_TRANSCRIPT_MARKER")
	paneJourneyKey(app, '[', tea.ModAlt)
	app.syncAgentWindowTabs()
	_ = app.View()
	if app.Win.ActiveTab().ID != alpha || app.Win.GroupActiveTab(right).ID != beta || app.AgentView.ActiveID() != "alpha" {
		t.Fatal("inactive agent paint/sync changed focused agent or other pane selection")
	}
}

func TestMainPaneJourneyOverflowKeepsNewSessionReachable(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 64, 24)
	for i := range 18 {
		app.AddOrchestrator(fmt.Sprintf("overflow-agent-%d", i))
	}
	_ = app.View()
	g := app.mainPanes.groups[app.Win.ActiveGroup()]
	r, ok := app.rootHost.ScreenRect(g.tabs.ID())
	if !ok {
		t.Fatal("overflowing strip is not mounted")
	}
	plus := -1
	for x := r.X; x < r.X+r.W; x++ {
		if app.mainSurf.Buffer().Get(x, r.Y).Char == '+' {
			plus = x
		}
	}
	if plus < 0 || g.tabs.TabsControl().ScrollOffset() == 0 {
		t.Fatal("overflowing strip must retain a painted plus alongside scrollable tabs")
	}
	before := app.Win.TabCount()
	_, _ = app.Update(tea.MouseClickMsg{X: plus, Y: r.Y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: plus, Y: r.Y, Button: tea.MouseLeft})
	_ = app.View()
	if app.Win.TabCount() != before+1 || app.Win.GroupOf(app.Win.ActiveTab().ID) != g.id || app.focusTarget() != FocusInput {
		t.Fatal("overflow plus did not create and focus a session in its owning pane")
	}
}

func TestMainPaneJourneyPreviewMatchesSourceCollapse(t *testing.T) {
	for _, edge := range []string{"center", "bottom"} {
		t.Run(edge, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 160, 40)
			paths := openEditorTabs(t, app, "left.go", "right.go")
			left := app.Win.ActiveGroup()
			right := app.Win.SplitGroup(left, ui.Horizontal, false)
			tab := app.Win.TabByTitle(app.editorTabTitle(paths[1])).ID
			app.Win.MoveTabToGroup(tab, right, 0)
			app.focusMainGroup(right)
			_ = app.View()
			body := app.mainPanes.groups[left].body
			x, y := body.X+body.W/2, body.Y+body.H/2
			if edge == "bottom" {
				y = body.Y + body.H - 2
			}
			paneJourneyDrag(t, app, tab, x, y)
			if app.mainPanes.drop == nil {
				t.Fatal("expected drop preview")
			}
			preview := app.mainPanes.drop.rect
			if len(app.Win.GroupIDs()) != 2 {
				t.Fatal("preview mutated topology")
			}
			_, _ = app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			_ = app.View()
			g := app.mainPanes.groups[app.Win.GroupOf(tab)]
			actual := g.screen(ui.Rect{W: g.Bounds().W, H: g.Bounds().H})
			if actual != preview {
				t.Fatalf("preview=%+v actual=%+v after source collapse", preview, actual)
			}
		})
	}
}

func TestMainPaneJourneyModalAndOutsideDropAreNonDestructive(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "left.go", "right.go")
	_ = app.View()
	group := app.Win.ActiveGroup()
	tabs := app.Win.GroupTabs(group)
	tab := app.Win.TabByTitle(app.editorTabTitle(paths[1])).ID
	paneJourneyDrag(t, app, tab, 1, 1)
	paneJourneyKey(app, tea.KeyEsc, 0)
	_, _ = app.Update(tea.MouseReleaseMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	_ = app.View()
	if !reflect.DeepEqual(tabs, app.Win.GroupTabs(group)) {
		t.Fatal("invalid drop/Esc closed or moved tab")
	}
	app.openApplicationSettings()
	_ = app.View()
	paneJourneyKey(app, 'l', tea.ModCtrl|tea.ModAlt)
	if len(app.Win.GroupIDs()) != 1 || !app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatal("split bypassed modal Settings ownership")
	}
}

func TestMainPaneJourneyKeyboardReorderAndMergeReleaseControls(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "first.go", "second.go")
	selected := app.Win.ActiveTab().ID
	app.executePaletteCommand("pane.tab-left")
	if app.Win.GroupTabs(app.Win.ActiveGroup())[0].ID != selected {
		t.Fatal("keyboard reorder did not move tab left")
	}
	app.executePaletteCommand("pane.tab-right")
	if app.Win.ActiveTab().ID != selected || app.Win.GroupTabs(app.Win.ActiveGroup())[1].ID != selected {
		t.Fatal("keyboard reorder changed selection")
	}
	for range 4 {
		paneJourneyKey(app, 'l', tea.ModCtrl|tea.ModAlt)
		app.executePaletteCommand("pane.merge-all")
		app.forceFrame()
		_ = app.View()
		if len(app.mainPanes.dividers) != 0 || app.Win.TabByTitle(app.editorTabTitle(paths[0])) == nil {
			t.Fatal("merge retained obsolete dividers or removed document")
		}
	}
}
