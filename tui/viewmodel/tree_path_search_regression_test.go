package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
)

func TestTreePathSearchFindsRootFileThroughRealAsyncPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(root)
	app.Tree.Model = model
	app.sidebarScreen.SetModel(model)
	app.sidebarScreen.SetPathSearchHandler(app.beginTreePathSearch)
	cmd := app.beginTreePathSearch("README.md")
	if cmd == nil {
		t.Fatal("search did not start")
	}
	result, ok := cmd().(treePathSearchResultMsg)
	if !ok {
		t.Fatalf("result type=%T", cmd())
	}
	app.applyTreePathSearchResult(result)
	buf := app.sidebarScreen.PaintTestBuffer(40, 12)
	var painted strings.Builder
	for row := range 12 {
		painted.WriteString(shelltree.PlainRow(buf, row, 40))
		painted.WriteByte('\n')
	}
	if !strings.Contains(painted.String(), "README.md") {
		t.Fatalf("root file missing from search projection:\n%s", painted.String())
	}
}

func TestTreePreferenceUpdatesWithoutOverridingLocalToggle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app := newEditorPaintApp(t, root)
	app.setActivity(activityFiles, true)
	// This fixture constructs App without NewApp; initialize its preference
	// observation as a real shell does.
	app.treeHiddenSetting = app.ws.LoadAppSettings().UI.ShowHiddenFilesOrDefault()
	settings := wsOf(app).AppDefaults()
	off := false
	settings.UI.ShowHiddenFiles = &off
	wsOf(app).SetAppDefaults(settings)
	_, cmd := app.Update(shellport.SettingsChangedMsg{})
	runAppCommand(t, app, cmd)
	if app.Tree.Model.ShowHidden() {
		t.Fatal("turning the preference Off did not update the mounted tree")
	}

	runAppCommand(t, app, app.sidebarScreen.SetShowHidden(true))
	_, cmd = app.Update(shellport.SettingsChangedMsg{})
	runAppCommand(t, app, cmd)
	if !app.Tree.Model.ShowHidden() {
		t.Fatal("an unrelated settings publication discarded the local toggle")
	}

	settings.UI.ShowHiddenFiles = nil
	wsOf(app).SetAppDefaults(settings)
	_, cmd = app.Update(shellport.SettingsChangedMsg{})
	runAppCommand(t, app, cmd)
	if !app.Tree.Model.ShowHidden() {
		t.Fatal("reset did not restore the default visible state")
	}
}

func TestConnectedTreeShowsHiddenFilesAndSearchHonorsToggle(t *testing.T) {
	root := t.TempDir()
	hidden := filepath.Join(root, ".secret-config")
	if err := os.WriteFile(hidden, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, ""), root)
	app := shell.app
	app.setActivity(activityFiles, true)
	runAppCommand(t, app, app.beginClientFileTreeLoad())
	_ = app.View()
	if !strings.Contains(app.View().RenderContent(), ".secret-config") {
		t.Fatal("connected file tree did not paint the hidden file by default")
	}

	runAppCommand(t, app, app.beginTreePathSearch("secret"))
	painted := func() string {
		buf := app.sidebarScreen.PaintTestBuffer(40, 12)
		var text strings.Builder
		for row := range 12 {
			text.WriteString(shelltree.PlainRow(buf, row, 40))
		}
		return text.String()
	}
	if !strings.Contains(painted(), ".secret-config") {
		t.Fatal("recursive search did not show the hidden file")
	}
	app.setFocusTree()
	app.rootHost.Focus(shelltree.FilterID)
	_, cmd := app.Update(tea.KeyPressMsg{Code: '.', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	if app.Tree.Model.ShowHidden() || strings.Contains(painted(), ".secret-config") {
		t.Fatalf("Ctrl+. did not hide the file: visible=%v focus=%v host=%s text=%q", app.Tree.Model.ShowHidden(), app.focusTarget(), app.rootHost.Focused(), painted())
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: '.', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	if !app.Tree.Model.ShowHidden() || !strings.Contains(painted(), ".secret-config") {
		t.Fatal("Ctrl+. did not restore the hidden search result")
	}
}

func TestTreePathSearchClearsFinalLetterAndRejectsLateResult(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.go"), []byte("package p"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(root)
	app.Tree.Model = model
	app.Tree.Visible = true
	app.activityView = activityFiles
	app.sidebarScreen.SetModel(model)
	app.sidebarScreen.SetPathSearchHandler(app.beginTreePathSearch)
	app.paintShellFrame()
	app.rootHost.Focus(shelltree.TreeID)
	app.dispatchTreeKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	state := asyncIOStateFor(app).treePathSearch
	generation := state.latest[app.sidebarScreen]
	if state.query[app.sidebarScreen] != "a" || generation == 0 || model.Filter() != "" {
		t.Fatalf("first keystroke created conflicting search owners: query=%q generation=%d model=%q", state.query[app.sidebarScreen], generation, model.Filter())
	}
	app.dispatchTreeKey(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if state.query[app.sidebarScreen] != "ab" {
		t.Fatalf("paced typing lost the latest draft while the initial scan was active: %q", state.query[app.sidebarScreen])
	}
	app.dispatchTreeKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if state.query[app.sidebarScreen] != "a" {
		t.Fatalf("first Backspace did not settle on the latest draft: %q", state.query[app.sidebarScreen])
	}
	app.dispatchTreeKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if state.query[app.sidebarScreen] != "" || model.Filter() != "" {
		t.Fatalf("last letter survived Backspace: state=%q model=%q", state.query[app.sidebarScreen], model.Filter())
	}
	app.applyTreePathSearchResult(treePathSearchResultMsg{target: app.sidebarScreen, generation: generation, query: "a"})
	buf := app.sidebarScreen.PaintTestBuffer(40, 12)
	if text := shelltree.PlainRow(buf, 0, 40); strings.Contains(text, "a⌕") || !strings.Contains(text, "Search") {
		t.Fatalf("stale async search restored deleted text: %q", text)
	}
}

func TestTreePathSearchDenseWorkspacePublishesLatestQuery(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(root)
	app.Tree.Model = model
	app.sidebarScreen.SetModel(model)
	app.sidebarScreen.SetPathSearchHandler(app.beginTreePathSearch)
	start := time.Now()
	cmd := app.beginTreePathSearch("README.md")
	result := cmd().(treePathSearchResultMsg)
	t.Logf("workspace catalog and rank: %s, query=%q, %d entries, %d matches", time.Since(start), result.query, len(result.catalog.Entries), len(result.matches))
	if len(result.matches) == 0 {
		t.Fatal("exact root filename had no ranked matches")
	}
	app.applyTreePathSearchResult(result)
	buf := app.sidebarScreen.PaintTestBuffer(40, 12)
	var painted strings.Builder
	for row := range 12 {
		painted.WriteString(shelltree.PlainRow(buf, row, 40))
		painted.WriteByte('\n')
	}
	if !strings.Contains(painted.String(), "README.md") {
		t.Fatalf("dense workspace result was not published:\n%s", painted.String())
	}
}
