package viewmodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"crude/tui/ui"
)

func writePaneFile(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRememberedPaneLayoutRestoresOpenEditors(t *testing.T) {
	app := newTestApp(t)
	root := t.TempDir()
	app.ws = &diagnosticFixtureWorkspace{Workspace: app.ws, service: app.ws.LSPMgr(), root: root}
	first := writePaneFile(t, root, "first.go", "package main\n")
	second := writePaneFile(t, root, "second.go", "package main\n")
	third := writePaneFile(t, root, "third.go", "package main\n")

	rootGroup := app.Win.ActiveGroup()
	open := app.openPaneFiles([]string{first, second}, rootGroup)
	if open == nil {
		t.Fatal("no open command")
	}
	app.applyPaneFilesOpened(open().(paneFilesOpenedMsg))
	app.Win.SelectGroupTab(rootGroup, app.Win.GroupTabByTitle(rootGroup, app.editorTabTitle(first)).ID)

	rightGroup := app.Win.SplitGroup(rootGroup, ui.Horizontal, false)
	if rightGroup == 0 {
		t.Fatal("split was not created")
	}
	open = app.openPaneFiles([]string{third}, rightGroup)
	if open == nil {
		t.Fatal("no right open command")
	}
	app.applyPaneFilesOpened(open().(paneFilesOpenedMsg))
	app.Win.FocusGroup(rightGroup)
	app.Win.PaneScopes(rightGroup)[1].Split.SetFraction(0.35)

	// Persist and reload the whole record: the restore must work from the
	// bytes a later client reads, not from in-process fields.
	captured := app.captureViewMemory()
	raw, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded viewMemory
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}
	if !validViewPanes(reloaded) {
		t.Fatalf("persisted pane record is not usable: %s", raw)
	}
	if len(reloaded.Panes) != 2 || reloaded.Panes[0].Active != first || reloaded.ActivePane != 1 {
		t.Fatalf("persisted panes = %+v", reloaded)
	}
	panes, layout, active := reloaded.Panes, reloaded.PaneLayout, reloaded.ActivePane
	if len(panes) != 2 || layout == nil || !layout.Split {
		t.Fatalf("captured panes=%d layout=%+v", len(panes), layout)
	}
	if panes[0].Active != first || len(panes[0].Files) != 2 || len(panes[1].Files) != 1 || panes[1].Files[0] != third {
		t.Fatalf("captured panes = %+v", panes)
	}
	if active != 1 {
		t.Fatalf("active pane = %d, want 1", active)
	}

	restored := newTestApp(t)
	restored.ws = &diagnosticFixtureWorkspace{Workspace: restored.ws, service: restored.ws.LSPMgr(), root: root}
	restored.restoreViewPanes(reloaded)
	if len(restored.Win.GroupIDs()) != 2 {
		t.Fatalf("restored groups = %d, want 2", len(restored.Win.GroupIDs()))
	}
	queued := restored.editorCommands
	restored.editorCommands = nil
	for _, cmd := range queued {
		restored.applyPaneFilesOpened(cmd().(paneFilesOpenedMsg))
	}
	groups := restored.Win.GroupIDs()
	if got := restored.Win.GroupTabs(groups[0]); len(got) != 2 {
		t.Fatalf("left pane tabs = %d, want 2", len(got))
	}
	if got := restored.Win.GroupActiveTab(groups[0]); got == nil || got.Resource.ID != first {
		t.Fatalf("left pane selected = %+v, want first.go", got)
	}
	if got := restored.Win.GroupTabs(groups[1]); len(got) != 1 || got[0].Resource.ID != third {
		t.Fatalf("right pane tabs = %+v", got)
	}
	if got := restored.Win.ActiveGroup(); got != groups[1] {
		t.Fatalf("focused group = %d, want right pane %d", got, groups[1])
	}
	scopes := restored.Win.PaneScopes(groups[1])
	if len(scopes) < 2 || scopes[1].Split == nil {
		t.Fatal("restored pane has no split")
	}
	if got := scopes[1].Split.Fraction(); got < 0.34 || got > 0.36 {
		t.Fatalf("restored split fraction = %v, want 0.35", got)
	}
}
