package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/filepicker"
	"crude/tui/ui"
)

func TestConnectedWorkspacePickerCreatesFolderOutsideCurrentProject(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	project, parent := t.TempDir(), t.TempDir()
	shell := newRemoteJourneyShell(t, rt, project)
	app, old := shell.app, shell.app.ws
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
	app.openFolderPicker()
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !app.FilePicker.AsyncDirectoryLoading() })
	app.FilePicker.Open(parent, filepicker.ModeFolder, nil)
	runAppCommand(t, app, app.beginDirectoryAsyncLoad(app.FilePicker, parent))
	fw := app.overlayHost.Window(ui.OverlayFilePicker)
	frame := fw.Content.(*ui.WindowFrameWidget)
	w, h := fw.Size()
	frame.Host().Resize(w, h)
	frame.Host().MarkLayout()
	frame.Host().Layout()
	frame.Host().Focus(filepicker.ActionsID)
	run := func(msg tea.Msg) { _, cmd := app.Update(msg); runAppCommand(t, app, cmd); app.View() }
	run(tea.KeyPressMsg{Code: tea.KeyTab})
	run(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !app.FilePicker.CreatingFolder() || frame.Host().Focused() != filepicker.FolderNameID {
		t.Fatalf("New Folder did not focus its form, focus=%s", frame.Host().Focused())
	}
	run(tea.PasteMsg{Content: "New project"})
	run(tea.KeyPressMsg{Code: tea.KeyEnter})
	want := filepath.Join(parent, "New project")
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("core did not create the external folder: %v %v", info, err)
	}
	if app.ws != old || app.workspaceOpenPrompt != nil || app.FilePicker.Current() != want {
		t.Fatalf("creation switched workspace or missed new folder: current=%q", app.FilePicker.Current())
	}
	if paint := app.View().RenderContent(); !strings.Contains(paint, "Open Workspace") {
		t.Fatal("created folder is not presented for opening")
	}
	run(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if app.ws != old || app.workspaceOpenPrompt == nil || app.workspaceOpenPrompt.root != want {
		t.Fatal("opening created folder bypassed the exact-destination consent")
	}
	run(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.ws != old || app.workspaceOpenPrompt != nil {
		t.Fatal("cancelling consent changed workspace")
	}
}

func TestPickerCreateResultCannotChangeReopenedPicker(t *testing.T) {
	app := newTestApp(t)
	parent := t.TempDir()
	picker := filepicker.New(app.Theme)
	picker.Open(parent, filepicker.ModeFolder, nil)
	app.FilePicker = picker
	component := ui.NewComponentWidget(app.Theme, picker, 80, 14)
	picker.SetFocusHandler(component.Focus)
	host := component.Host()
	host.Resize(80, 14)
	host.Layout()
	picker.HandleAction(ui.EventContext{}, ui.Action{ID: "filepicker.new-folder"})
	host.Layout()
	host.Handle(tea.PasteMsg{Content: "first"})
	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Cmd == nil {
		t.Fatal("missing create request")
	}
	request := res.Cmd().(filepicker.CreateFolderMsg)
	cmd := app.beginPickerCreateFolder(request)
	if cmd == nil {
		t.Fatal("create not admitted")
	}
	result := cmd().(pickerCreateFolderResultMsg)
	other := t.TempDir()
	picker.Close()
	picker.Open(other, filepicker.ModeFolder, nil)
	if cmd := app.applyPickerCreateFolderResult(result); cmd != nil || picker.Current() != other || picker.CreatingFolder() {
		t.Fatal("old result changed the reopened picker")
	}
}
