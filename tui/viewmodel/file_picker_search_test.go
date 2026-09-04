package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestFilePickerSearchFindsUnloadedDescendant(t *testing.T) {
	app := newTestApp(t)
	root := t.TempDir()
	nested := filepath.Join(root, "src", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(nested, "main.go")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app.FilePicker = filepickerscreen.New(app.Theme)
	app.FilePicker.Open(root, filepickerscreen.ModeFile, []filepickerscreen.Entry{})
	component := ui.NewComponentWidget(theme.DefaultDark(), app.FilePicker, 96, 28)
	app.FilePicker.SetFocusHandler(component.Focus)
	app.FilePicker.FocusFilter()
	host := component.Host()
	host.Resize(96, 28)
	host.Layout()

	result := host.Handle(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if result.Cmd == nil {
		t.Fatal("typing a path query did not schedule search")
	}
	search, ok := result.Cmd().(filepickerscreen.SearchMsg)
	if !ok {
		t.Fatalf("search command returned %T", result.Cmd())
	}
	cmds := app.handleAppMessages(search)
	if len(cmds) != 1 || cmds[0] == nil {
		t.Fatalf("search start commands=%d", len(cmds))
	}
	app.handleAppMessages(cmds[0]())

	paths := app.FilePicker.VisiblePaths()
	seen := false
	for _, path := range paths {
		if path == target {
			seen = true
			break
		}
	}
	if !seen {
		t.Fatalf("recursive search paths=%v, missing %q", paths, target)
	}
}

func TestFilePickerToggleMessageKeepsPickerOpen(t *testing.T) {
	app := newTestApp(t)
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app.FilePicker = filepickerscreen.New(app.Theme)
	app.FilePicker.Open(root, filepickerscreen.ModeFile, []filepickerscreen.Entry{{Name: "main.go", Path: path}})
	result := app.FilePicker.HandleAction(ui.EventContext{}, ui.Action{
		ID:      "filepicker.entry.activate",
		Payload: ui.ListItem{Payload: filepickerscreen.Entry{Name: "main.go", Path: path}},
	})
	if result.Cmd == nil {
		t.Fatal("file activation did not emit toggle message")
	}
	toggle, ok := result.Cmd().(filepickerscreen.ToggleMsg)
	if !ok {
		t.Fatalf("file activation command returned %T", result.Cmd())
	}
	app.handleAppMessages(toggle)
	if !app.FilePicker.IsOpen() || len(app.FilePicker.SelectedPaths()) != 1 || app.FilePicker.SelectedPaths()[0] != path {
		t.Fatalf("toggle closed or failed selection: open=%v paths=%v", app.FilePicker.IsOpen(), app.FilePicker.SelectedPaths())
	}
}

func TestFilePickerQueryChangesReuseActivePathWalk(t *testing.T) {
	app := newTestApp(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.go", "module.go"} {
		if err := os.WriteFile(filepath.Join(root, "src", name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app.FilePicker = filepickerscreen.New(app.Theme)
	app.FilePicker.Open(root, filepickerscreen.ModeFile, []filepickerscreen.Entry{})
	component := ui.NewComponentWidget(theme.DefaultDark(), app.FilePicker, 96, 28)
	app.FilePicker.SetFocusHandler(component.Focus)
	app.FilePicker.FocusFilter()
	host := component.Host()
	host.Resize(96, 28)
	host.Layout()

	firstResult := host.Handle(tea.KeyPressMsg{Code: 'm', Text: "m"})
	firstSearch := firstResult.Cmd().(filepickerscreen.SearchMsg)
	firstCmds := app.handleAppMessages(firstSearch)
	if len(firstCmds) != 1 {
		t.Fatalf("first search commands=%d", len(firstCmds))
	}
	state := asyncIOStateFor(app)
	state.mu.Lock()
	firstGeneration := state.latestPathSearch[app.FilePicker]
	state.mu.Unlock()

	secondResult := host.Handle(tea.KeyPressMsg{Code: 'o', Text: "o"})
	secondSearch := secondResult.Cmd().(filepickerscreen.SearchMsg)
	if cmds := app.handleAppMessages(secondSearch); len(cmds) != 0 {
		t.Fatalf("query change restarted active walk: commands=%d", len(cmds))
	}
	state.mu.Lock()
	secondGeneration := state.latestPathSearch[app.FilePicker]
	running := state.pathSearchRunning[app.FilePicker]
	state.mu.Unlock()
	if firstGeneration != secondGeneration || !running {
		t.Fatalf("active search state changed: first=%d second=%d running=%v", firstGeneration, secondGeneration, running)
	}
}

func TestFilePickerBreadcrumbNavigatesToSelectedParent(t *testing.T) {
	app := newTestApp(t)
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "parent.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app.FilePicker = filepickerscreen.New(app.Theme)
	app.FilePicker.Open(child, filepickerscreen.ModeFile, []filepickerscreen.Entry{})
	result := app.FilePicker.HandleAction(ui.EventContext{}, ui.Action{
		ID:      "filepicker.breadcrumb.navigate",
		Payload: parent,
	})
	if result.Cmd == nil {
		t.Fatal("parent breadcrumb did not emit navigation")
	}
	navigate, ok := result.Cmd().(filepickerscreen.NavigateMsg)
	if !ok {
		t.Fatalf("breadcrumb command returned %T", result.Cmd())
	}
	cmds := app.handleAppMessages(navigate)
	if len(cmds) != 1 || cmds[0] == nil {
		t.Fatalf("breadcrumb navigation commands=%d", len(cmds))
	}
	app.handleAppMessages(cmds[0]())
	if got := app.FilePicker.Current(); got != parent {
		t.Fatalf("breadcrumb current=%q want %q", got, parent)
	}
	if paths := app.FilePicker.VisiblePaths(); len(paths) != 2 || paths[1] != filepath.Join(parent, "parent.txt") {
		t.Fatalf("breadcrumb rows=%v", paths)
	}
}

func TestFilePickerDoubleClickFolderAdvancesBreadcrumb(t *testing.T) {
	app := newTestApp(t)
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	app.FilePicker = filepickerscreen.New(app.Theme)
	app.FilePicker.Open(parent, filepickerscreen.ModeFolder, []filepickerscreen.Entry{{Name: "child", Path: child, IsDir: true}})
	component := ui.NewComponentWidget(theme.DefaultDark(), app.FilePicker, 96, 28)
	app.FilePicker.SetFocusHandler(component.Focus)
	host := component.Host()
	host.Resize(96, 28)
	host.Layout()
	if !host.Focus(filepickerscreen.ListID) {
		t.Fatal("could not focus picker tree")
	}
	first := host.Handle(tea.MouseClickMsg{X: 3, Y: 7, Button: tea.MouseLeft})
	if first.Cmd == nil {
		t.Fatal("first folder click did not start child loading")
	}
	if _, ok := first.Cmd().(filepickerscreen.LoadMsg); !ok {
		t.Fatalf("first folder click command=%T", first.Cmd())
	}
	second := host.Handle(tea.MouseClickMsg{X: 3, Y: 7, Button: tea.MouseLeft})
	if second.Cmd == nil {
		t.Fatal("double-click did not emit navigation")
	}
	navigate, ok := second.Cmd().(filepickerscreen.NavigateMsg)
	if !ok {
		t.Fatalf("double-click command=%T", second.Cmd())
	}
	cmds := app.handleAppMessages(navigate)
	if len(cmds) != 1 || cmds[0] == nil {
		t.Fatalf("double-click navigation commands=%d", len(cmds))
	}
	app.handleAppMessages(cmds[0]())
	if got := app.FilePicker.Current(); got != child {
		t.Fatalf("double-click current=%q want %q", got, child)
	}
}
