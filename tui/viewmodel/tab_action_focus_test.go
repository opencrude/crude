package viewmodel

import (
	"fmt"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/screens/shelltabs"
	"crude/tui/screens/shelltree"
	"crude/tui/ui"
	"crude/workspace"
)

// These checks drive the production Update/View path and inspect the painted
// baseline as well as the next keystroke's owner. Hiding the focus decoration
// without returning keyboard input to the content must not pass.
func TestTabCloseReturnsFocusToContent(t *testing.T) {
	for _, width := range []int{160, 64} {
		for _, scenario := range []string{"active file", "inactive file", "file to agent", "active agent", "inactive agent", "last file", "last agent"} {
			t.Run(fmt.Sprintf("%d/%s", width, scenario), func(t *testing.T) {
				app, ws := newRealShellApp(t)
				defer ws.Close()
				_, _ = app.Update(tea.WindowSizeMsg{Width: width, Height: 30})
				var closeTitle, remainingTitle string
				wantFocus := FocusEditor
				switch scenario {
				case "active file", "inactive file":
					paths := openEditorTabs(t, app, "main.go", "notes.txt")
					closeTitle, remainingTitle = app.editorTabTitle(paths[1]), app.editorTabTitle(paths[0])
					if scenario == "inactive file" {
						closeTitle, remainingTitle = remainingTitle, closeTitle
					}
				case "file to agent":
					app.AddOrchestrator("alpha")
					path := openEditorTabs(t, app, "main.go")[0]
					closeTitle, remainingTitle = app.editorTabTitle(path), app.agentTabTitle("alpha")
					wantFocus = FocusInput
				case "active agent":
					app.AddOrchestrator("alpha")
					app.AddOrchestrator("beta")
					closeTitle, remainingTitle = app.agentTabTitle("beta"), app.agentTabTitle("alpha")
					wantFocus = FocusInput
				case "inactive agent":
					app.AddOrchestrator("alpha")
					path := openEditorTabs(t, app, "main.go")[0]
					closeTitle, remainingTitle = app.agentTabTitle("alpha"), app.editorTabTitle(path)
				case "last file":
					path := openEditorTabs(t, app, "main.go")[0]
					closeTitle = app.editorTabTitle(path)
					wantFocus = FocusLauncher
				case "last agent":
					app.AddOrchestrator("alpha")
					closeTitle = app.agentTabTitle("alpha")
					wantFocus = FocusLauncher
				}

				clickMainTab(t, app, closeTitle, true)
				if app.Win.TabByTitle(closeTitle) != nil {
					t.Fatal("closed tab is still visible")
				}
				if remainingTitle != "" {
					assertSelectedTab(t, app, remainingTitle)
				}
				assertMainTabContentFocus(t, app, wantFocus)
				if scenario == "active agent" || scenario == "inactive agent" || scenario == "last agent" {
					id := app.agentIDFromTabTitle(closeTitle)
					if node := app.AgentMgr.Get(id); node == nil || !node.Hidden {
						t.Fatal("tab close must minimize the agent without destroying it")
					}
				}
				assertNextTabContentKey(t, app, wantFocus)
			})
		}
	}
}

func TestTabClickActivatesContentWithoutRetainingStripFocus(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.AddOrchestrator("alpha")
	path := openEditorTabs(t, app, "main.go")[0]
	for _, target := range []struct {
		title string
		focus FocusTarget
	}{
		{app.agentTabTitle("alpha"), FocusInput},
		{app.editorTabTitle(path), FocusEditor},
	} {
		// Clicking a tab commits a selection, even if keyboard strip navigation
		// was active beforehand.
		app.setFocusTabBar()
		clickMainTab(t, app, target.title, false)
		assertSelectedTab(t, app, target.title)
		assertMainTabContentFocus(t, app, target.focus)
		assertNextTabContentKey(t, app, target.focus)
	}
}

func TestTabHoverDoesNotMoveKeyboardFocus(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	openEditorTabs(t, app, "main.go", "notes.txt")
	_ = app.View()
	before := app.rootHost.Focused()
	rect, ok := app.rootHost.ScreenRect(shelltabs.TabsID)
	if !ok {
		t.Fatal("tab strip is not mounted")
	}
	_, _ = app.Update(tea.MouseMotionMsg{X: rect.X + 2, Y: rect.Y})
	if got := app.rootHost.Focused(); got != before {
		t.Fatalf("hover moved Host focus from %q to %q", before, got)
	}
	assertMainTabContentFocus(t, app, FocusEditor)
}

func TestKeyboardTabNavigationRetainsFocusUntilCommitted(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.AddOrchestrator("alpha")
	app.AddOrchestrator("beta")
	_ = app.View()
	_, _ = app.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	for _, key := range []rune{tea.KeyLeft, tea.KeyRight, tea.KeyHome, tea.KeyEnd} {
		_, _ = app.Update(tea.KeyPressMsg{Code: key})
		_ = app.View()
		if app.focusTarget() != FocusTabBar || app.rootHost.Focused() != shelltabs.TabsID {
			t.Fatal("keyboard browsing lost deliberate tab-strip focus")
		}
		assertMainTabBaseline(t, app, true)
	}
	_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assertMainTabContentFocus(t, app, FocusInput)
	assertNextTabContentKey(t, app, FocusInput)
}

func TestKeyboardTabCloseReleasesStripFocus(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paths := openEditorTabs(t, app, "main.go", "notes.txt")
	app.setFocusTabBar()
	_, _ = app.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	assertSelectedTab(t, app, app.editorTabTitle(paths[0]))
	assertMainTabContentFocus(t, app, FocusEditor)
	assertNextTabContentKey(t, app, FocusEditor)
}

func TestTabContextCloseReleasesStripFocus(t *testing.T) {
	for _, action := range []string{"Close tab", "Close others", "Close to the right", "Minimize", "Terminate agent"} {
		t.Run(action, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			app.AddOrchestrator("alpha")
			paths := openEditorTabs(t, app, "main.go", "notes.txt")
			target := app.editorTabTitle(paths[1])
			if action == "Close to the right" {
				target = app.editorTabTitle(paths[0])
			}
			if action == "Minimize" || action == "Terminate agent" {
				target = app.agentTabTitle("alpha")
			}
			app.setFocusTabBar()
			_ = app.View()
			rect, _ := app.rootHost.ScreenRect(shelltabs.TabsID)
			index := app.Win.IndexOf(app.Win.TabByTitle(target).ID)
			x := -1
			for local := 0; local < rect.W; local++ {
				if hit, closeHit := app.tabScreen.TabsControl().HitTest(local); hit == index && !closeHit {
					x = rect.X + local
					break
				}
			}
			if x < 0 {
				t.Fatal("context target is not visible")
			}
			_, _ = app.Update(tea.MouseClickMsg{X: x, Y: rect.Y, Button: tea.MouseRight})
			menu := app.LastContextMenu
			if menu == nil {
				t.Fatal("tab context menu did not open")
			}
			cursor := -1
			for i := range menu.FlatItems {
				section, item, ok := menu.FlatItemAt(i)
				if ok && menu.Sections[section].Items[item].Label == action {
					cursor = i
					break
				}
			}
			if cursor < 0 {
				t.Fatalf("missing menu action %q", action)
			}
			menu.Cursor = cursor
			_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if app.overlayHost.IsOpen(ui.OverlayContextMenu) {
				t.Fatal("tab action left its menu open")
			}
			assertMainTabContentFocus(t, app, FocusEditor)
			assertNextTabContentKey(t, app, FocusEditor)
		})
	}
}

func TestKeyboardAgentCloseUsesContentFocusHandoff(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.AddOrchestrator("alpha")
	app.AddOrchestrator("beta")
	_ = app.View()
	app.setFocusTabBar()
	_, _ = app.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	assertSelectedTab(t, app, app.agentTabTitle("alpha"))
	if node := app.AgentMgr.Get("beta"); node == nil || !node.Hidden {
		t.Fatal("keyboard close must minimize, not terminate, the agent")
	}
	assertMainTabContentFocus(t, app, FocusInput)
	assertNextTabContentKey(t, app, FocusInput)
}

func TestTabOverflowBrowsingDoesNotRetainStripFocus(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	_, _ = app.Update(tea.WindowSizeMsg{Width: 64, Height: 24})
	app.Tree.Visible = false
	for i := range 12 {
		app.AddOrchestrator(fmt.Sprintf("agent-%d", i))
	}
	_ = app.View()
	selected := app.Win.ActiveTab().ID
	rect, _ := app.rootHost.ScreenRect(shelltabs.TabsID)
	left := -1
	for column := rect.X; column < rect.X+rect.W; column++ {
		if app.mainSurf.Buffer().Get(column, rect.Y).Char == '‹' {
			left = column
			break
		}
	}
	if left < 0 {
		t.Fatal("overflow browsing control is not visible")
	}
	before := app.tabScreen.TabsControl().ScrollOffset()
	_, _ = app.Update(tea.MouseClickMsg{X: left, Y: rect.Y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: left, Y: rect.Y, Button: tea.MouseLeft})
	_ = app.View()
	if app.tabScreen.TabsControl().ScrollOffset() >= before || app.Win.ActiveTab().ID != selected {
		t.Fatal("overflow browsing failed to scroll or changed the selected content")
	}
	if app.focusTarget() != FocusInput || app.rootHost.Focused() != app.inputScreen.ID() {
		t.Fatal("overflow arrow retained keyboard strip focus")
	}
	_, _ = app.Update(tea.MouseWheelMsg{X: rect.X + 2, Y: rect.Y, Button: tea.MouseWheelLeft})
	_ = app.View()
	if app.focusTarget() != FocusInput || app.rootHost.Focused() != app.inputScreen.ID() || app.Win.ActiveTab().ID != selected {
		t.Fatal("overflow wheel changed keyboard focus or selection")
	}
}

func TestBackgroundTabChangesPreserveExplicitFocus(t *testing.T) {
	for _, focus := range []FocusTarget{FocusTree, FocusTabBar} {
		t.Run(fmt.Sprint(focus), func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			app.AddOrchestrator("alpha")
			_ = app.View()
			if focus == FocusTree {
				app.setFocusTree()
				app.syncRootFocus()
			} else {
				app.setFocusTabBar()
			}
			pane := app.PaneMgr.GetOrCreate("background", workspace.PaneAgent, "Background")
			app.AgentMgr.AddSubAgent("background", "alpha", "Background", "model", "researcher",
				workspace.NewAgentState("background", pane.Messages, app.Logger))
			app.syncAgentWindowTabs()
			_ = app.View()
			app.completeSubAgent("background")
			_ = app.View()
			if app.focusTarget() != focus {
				t.Fatal("background tab lifecycle stole explicitly chosen focus")
			}
			want := shelltabs.TabsID
			if focus == FocusTree {
				want = shelltree.TreeID
			}
			if app.rootHost.Focused() != want {
				t.Fatalf("background lifecycle moved Host focus to %q", app.rootHost.Focused())
			}
		})
	}
}

func TestDirtyTabCloseDoesNotRestoreStaleStripFocus(t *testing.T) {
	for _, decision := range []string{"cancel", "discard", "save"} {
		t.Run(decision, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paths := openEditorTabs(t, app, "main.go", "notes.txt")
			ed := app.EditorState.Editor
			original, err := os.ReadFile(paths[1])
			if err != nil {
				t.Fatal(err)
			}
			ed.InsertRune('z')
			draft := ed.Value()
			clickMainTab(t, app, app.editorTabTitle(paths[1]), true)
			if app.DirtyPromptPath != paths[1] || app.focusTarget() != FocusFloatingWindow {
				t.Fatal("dirty close must keep the document and focus its decision modal")
			}
			_ = app.View()
			assertMainTabBaseline(t, app, false)
			key := map[string]rune{"cancel": 'c', "discard": 'd', "save": 's'}[decision]
			_, command := app.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
			runAppCommand(t, app, command)
			if app.DirtyPromptPath != "" {
				t.Fatal("dirty-close decision did not settle")
			}
			if decision == "cancel" {
				if !ed.IsOpen() || ed.Value() != draft || !ed.Modified() {
					t.Fatal("cancel changed or discarded the dirty document")
				}
			} else {
				if ed.IsOpen() {
					t.Fatal("confirmed close left the document open")
				}
				assertSelectedTab(t, app, app.editorTabTitle(paths[0]))
			}
			disk, err := os.ReadFile(paths[1])
			if err != nil {
				t.Fatal(err)
			}
			wantDisk := string(original)
			if decision == "save" {
				wantDisk = draft
			}
			if string(disk) != wantDisk {
				t.Fatal("close decision changed the wrong disk content")
			}
			assertMainTabContentFocus(t, app, FocusEditor)
			assertNextTabContentKey(t, app, FocusEditor)
		})
	}
}

func clickMainTab(t *testing.T, app *App, title string, closeTab bool) {
	t.Helper()
	app.forceFrame()
	_ = app.View()
	rect, ok := app.rootHost.ScreenRect(shelltabs.TabsID)
	if !ok {
		t.Fatal("tab strip is not mounted")
	}
	tab := app.Win.TabByTitle(title)
	if tab == nil {
		t.Fatalf("missing tab %q", title)
	}
	index := app.Win.IndexOf(tab.ID)
	x := -1
	for local := 0; local < rect.W; local++ {
		if hit, closeHit := app.tabScreen.TabsControl().HitTest(local); hit == index && closeHit == closeTab {
			x = rect.X + local
			break
		}
	}
	if x < 0 {
		t.Fatalf("tab %q has no visible target (close=%v)", title, closeTab)
	}
	_, _ = app.Update(tea.MouseClickMsg{X: x, Y: rect.Y, Button: tea.MouseLeft})
	// The immediate frame and pointer release must not revive strip focus.
	_ = app.View()
	if app.FocusStack.Top() == FocusTabBar || app.rootHost.Focused() == shelltabs.TabsID {
		t.Fatal("the immediate tab-action frame retained strip focus")
	}
	if app.tabBarVisible() {
		assertMainTabBaseline(t, app, false)
	}
	_, _ = app.Update(tea.MouseReleaseMsg{X: x, Y: rect.Y, Button: tea.MouseLeft})
}

func assertMainTabContentFocus(t *testing.T, app *App, want FocusTarget) {
	t.Helper()
	if got := app.focusTarget(); got != want {
		t.Fatalf("keyboard focus=%v, want content focus=%v", got, want)
	}
	if app.FocusStack.Top() == FocusTabBar {
		t.Fatal("tab operation retained strip focus on the policy stack")
	}
	for frame := 0; frame < 2; frame++ {
		app.forceFrame()
		_ = app.View()
		if app.rootHost.Focused() == shelltabs.TabsID {
			t.Fatal("tab operation retained Host strip focus")
		}
		got := app.rootHost.Focused()
		correct := want == FocusEditor && got == app.editorScreen.ID() ||
			want == FocusInput && got == app.inputScreen.ID() ||
			want == FocusLauncher && app.launcherScreen != nil && app.launcherScreen.OwnsFocus(got)
		if !correct {
			t.Fatalf("content is not focused in frame %d: %q", frame, app.rootHost.Focused())
		}
		if app.tabBarVisible() {
			assertMainTabBaseline(t, app, false)
		}
	}
}

func assertMainTabBaseline(t *testing.T, app *App, focused bool) {
	t.Helper()
	rect, ok := app.rootHost.ScreenRect(shelltabs.TabsID)
	if !ok || rect.H < ui.StripHeight {
		t.Fatal("tab baseline is not mounted")
	}
	// The empty trailing column is outside the active tab's underline. Only
	// actual keyboard strip focus may make it a heavy, bold accent rule.
	painted := app.mainSurf.Buffer().Get(rect.X+rect.W-1, rect.Y+1)
	if focused {
		if painted.Char != '━' || painted.Attr&cell.AttrBold == 0 || painted.FG != app.rootHost.Tokens().Color(ui.ColorAccent) {
			t.Fatalf("deliberate tab focus is not painted: %+v", painted)
		}
	} else if painted.Char == '━' || painted.Attr&cell.AttrBold != 0 || painted.FG != app.rootHost.Tokens().Color(ui.ColorBorder) {
		t.Fatalf("tab operation left a heavy/bold/accent baseline: %+v", painted)
	}
}

func assertNextTabContentKey(t *testing.T, app *App, focus FocusTarget) {
	t.Helper()
	if focus == FocusLauncher {
		if !app.launcherHostVisible() {
			t.Fatal("last-tab close did not return to workspace home")
		}
		return
	}
	before := app.Input.Value()
	if focus == FocusEditor {
		before = app.EditorState.Editor.Value()
	}
	_, _ = app.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	after := app.Input.Value()
	if focus == FocusEditor {
		after = app.EditorState.Editor.Value()
	}
	if after == before || len(after) != len(before)+1 {
		t.Fatal("first key after the tab operation did not reach the content")
	}
}
