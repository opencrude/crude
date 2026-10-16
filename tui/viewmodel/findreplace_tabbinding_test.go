package viewmodel

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindReplaceClosesOnTabSwitch guards the core defect: the find/replace
// popover is bound to the editor tab that summoned it. It operates on that
// tab's buffer via closures captured at open time, so letting it survive a tab
// switch would leave it mutating the previous tab's editor while the user sees
// the new one. handleTabSwitch closes it; the query/options live in each
// editor's SearchState and are restored on re-open.
func TestFindReplaceClosesOnTabSwitch(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.go")
	pathB := filepath.Join(dir, "b.go")
	if err := os.WriteFile(pathA, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newEditorPaintApp(t, dir)
	if _, err := app.openEditorTab(pathA); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(pathB); err != nil {
		t.Fatal(err)
	}
	owner := app.EditorState.Editor // b's editor (most recently opened)

	app.openFindReplace(false)
	if app.FindReplace == nil || app.windowManager.Window("find-replace") == nil {
		t.Fatal("find/replace popup did not open")
	}

	// Switch back to a via the tab bar path.
	tab := app.Win.TabByTitle(app.editorTabTitle(pathA))
	if tab == nil {
		t.Fatal("missing tab a")
	}
	app.Win.SwitchTo(app.Win.IndexOf(tab.ID))
	app.handleTabSwitch()

	if app.FindReplace != nil {
		t.Fatal("find/replace state survived an editor-tab switch")
	}
	if app.windowManager.Window("find-replace") != nil {
		t.Fatal("find/replace window still mounted after tab switch")
	}

	// The active editor must now be a's; the old popup's closures pointed at b.
	if app.EditorState.Editor == owner {
		t.Fatal("active editor did not change after tab switch")
	}

	// Re-opening binds a fresh popup to a's editor and restores a's query.
	app.openFindReplace(false)
	if app.FindReplace == nil {
		t.Fatal("find/replace did not reopen on the second tab")
	}
}

// TestFindReplaceClosesOnAgentTabSwitch confirms switching to a non-editor
// (agent) tab also closes the popup: the agent branch leaves ActiveView
// empty, so the popover has no owning editor view.
func TestFindReplaceClosesOnAgentTabSwitch(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "main.go")
	if err := os.WriteFile(pathA, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newEditorPaintApp(t, dir)
	if _, err := app.openEditorTab(pathA); err != nil {
		t.Fatal(err)
	}

	app.openFindReplace(false)
	if app.FindReplace == nil {
		t.Fatal("find/replace popup did not open")
	}

	// Switch to an agent tab.
	app.Win.OpenTab(app.agentTabTitle("agent-1"), "◆", true)
	tab := app.Win.TabByTitle(app.agentTabTitle("agent-1"))
	if tab == nil {
		t.Fatal("missing agent tab")
	}
	app.Win.SwitchTo(app.Win.IndexOf(tab.ID))
	app.handleTabSwitch()

	if app.FindReplace != nil {
		t.Fatal("find/replace state survived a switch to an agent tab")
	}
	if app.windowManager.Window("find-replace") != nil {
		t.Fatal("find/replace window still mounted after agent-tab switch")
	}
}

// TestFindReplaceSurvivesSameTabRederivation is the regression guard for the
// popup-closing-on-itself defect: handleTabSwitch is called from ~40 sites for
// steady-state re-derivations (opening the tab you're already on, tick/agent
// syncs, tab-bar enter, command-palette switches), and closing the popup
// unconditionally made it vanish the instant it opened. Only a genuine
// owner change (prev != next) may close it.
func TestFindReplaceSurvivesSameTabRederivation(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.go")
	if err := os.WriteFile(pathA, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newEditorPaintApp(t, dir)
	if _, err := app.openEditorTab(pathA); err != nil {
		t.Fatal(err)
	}

	app.openFindReplace(false)
	if app.FindReplace == nil || app.windowManager.Window("find-replace") == nil {
		t.Fatal("find/replace popup did not open")
	}

	// Steady-state re-derivations must NOT close the popup: the same-tab
	// open path, an explicit handleTabSwitch, and openEditorTab on the
	// current tab all keep ActiveView == pathA.
	for i, step := range []func(){
		func() { app.handleTabSwitch() },
		func() { app.handleTabSwitch() },
		func() {
			if _, err := app.openEditorTab(pathA); err != nil {
				t.Fatal(err)
			}
		},
	} {
		step()
		if app.FindReplace == nil {
			t.Fatalf("step %d: popup closed on same-tab re-derivation", i)
		}
		if app.windowManager.Window("find-replace") == nil {
			t.Fatalf("step %d: window unmounted on same-tab re-derivation", i)
		}
	}
}
