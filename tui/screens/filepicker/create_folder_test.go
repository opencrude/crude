package filepicker

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/ui"
)

func TestCreateFolderPickerKeyboardValidationAndLifecycle(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown}) // selected folder is the parent
	host.Focus(ActionsID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Layout()
	if result.Cmd != nil || !screen.CreatingFolder() || host.Focused() != FolderNameID {
		t.Fatalf("New Folder did not open its name field: %#v focus=%s", result, host.Focused())
	}
	buf := surface.NewCellBuffer(80, 14)
	host.Paint(buf)
	text := pickerBufferText(buf)
	for _, want := range []string{"NEW FOLDER", "Create in", "src", "Folder name", "Create", "Cancel"} {
		if !strings.Contains(text, want) {
			t.Fatalf("creation screen missing %q:\n%s", want, text)
		}
	}
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil || !strings.Contains(screen.errText, "folder name") {
		t.Fatalf("empty name was not rejected: %#v %q", result, screen.errText)
	}
	host.Handle(tea.PasteMsg{Content: "../escape"})
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil || !strings.Contains(screen.errText, "single name") {
		t.Fatalf("path traversal was not rejected: %#v %q", result, screen.errText)
	}
	screen.folderName.SetValue("project")
	request := commandMessage[CreateFolderMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if !request.ValidFor(screen) || request.Parent != pickerPath("src") || request.Name != "project" {
		t.Fatalf("wrong creation request: %#v", request)
	}
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil {
		t.Fatal("pending creation was submitted twice")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !screen.CreatingFolder() {
		t.Fatal("pending creation was dismissed as if cancelled")
	}
	if !screen.CompleteCreateFolderFor(request.Intent(), errors.New("permission denied")) {
		t.Fatal("failure not accepted")
	}
	if screen.folderName.Value() != "project" || !screen.CreatingFolder() || !strings.Contains(screen.errText, "permission denied") {
		t.Fatal("failure discarded the name or its explanation")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	host.Layout()
	if screen.CreatingFolder() || screen.FocusedPath() != pickerPath("src") {
		t.Fatal("cancel did not restore the original selection")
	}
	if screen.CompleteCreateFolderFor(request.Intent(), nil) {
		t.Fatal("cancelled operation settled a later picker state")
	}
}

func TestNewFolderOnlyInWorkspacePickerAndBounded(t *testing.T) {
	for _, mode := range []Mode{ModeFile, ModeOpenFile, ModeFolder} {
		screen, host := pickerHost(t, mode)
		buf := surface.NewCellBuffer(80, 14)
		host.Paint(buf)
		if got := strings.Contains(pickerBufferText(buf), "New Folder"); got != (mode == ModeFolder) {
			t.Fatalf("mode %d: New Folder visible=%v", mode, got)
		}
		if mode != ModeFolder {
			screen.HandleAction(ui.EventContext{}, ui.Action{ID: "filepicker.new-folder"})
			if screen.CreatingFolder() {
				t.Fatal("file picker admitted folder creation")
			}
		}
	}
	for _, size := range []ui.Size{{W: 60, H: 18}, {W: 30, H: 12}, {W: 8, H: 5}, {W: 2, H: 1}} {
		screen, host := pickerHost(t, ModeFolder)
		screen.HandleAction(ui.EventContext{}, ui.Action{ID: "filepicker.new-folder"})
		host.Resize(size.W, size.H)
		host.Layout()
		for _, child := range screen.Children() {
			r := child.Bounds()
			if r.X < 0 || r.Y < 0 || r.X+r.W > size.W || r.Y+r.H > size.H {
				t.Fatalf("size=%+v child=%s bounds=%+v", size, child.ID(), r)
			}
		}
		host.Paint(surface.NewCellBuffer(size.W, size.H))
	}
}

func TestNewFolderMouseAndUnknownOutcomeDoesNotReplay(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	b, ok := screen.actions.ButtonBounds("filepicker.new-folder")
	if !ok {
		t.Fatal("New Folder button is missing")
	}
	host.Handle(tea.MouseClickMsg{X: b.X + 1, Y: screen.actions.Bounds().Y, Button: tea.MouseLeft})
	host.Layout()
	if !screen.CreatingFolder() || host.Focused() != FolderNameID {
		t.Fatal("pointer did not enter the folder name field")
	}
	host.Handle(tea.PasteMsg{Content: "project"})
	request := commandMessage[CreateFolderMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if !screen.CompleteCreateFolderFor(request.Intent(), ErrFolderCreationUnknown) {
		t.Fatal("uncertain result was not applied")
	}
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil {
		t.Fatal("uncertain creation was replayed")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if screen.CreatingFolder() {
		t.Fatal("cannot return to inspect the parent")
	}
}

func TestNewFolderReverseTabReachesCancel(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "filepicker.new-folder"})
	host.Layout()
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if host.Focused() != ActionsID {
		t.Fatal("reverse Tab did not reach the actions")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if screen.CreatingFolder() {
		t.Fatal("reverse Tab did not focus Cancel")
	}
}
