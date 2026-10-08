package filepicker

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/third_party/pathwalk"
	"crude/tui/filetree"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func assertDocumentSelection(t *testing.T, screen *Screen, result ui.EventResult, path string) SelectMsg {
	t.Helper()
	msg := commandMessage[SelectMsg](t, result)
	if msg.Target != screen || !msg.ValidFor(screen) || msg.Path != path || msg.Folder || len(msg.Paths) != 0 {
		t.Fatalf("document selection=%#v, want single file %q for current owner", msg, path)
	}
	if len(screen.SelectedPaths()) != 0 {
		t.Fatalf("document open accumulated attachments: %v", screen.SelectedPaths())
	}
	return msg
}

func TestOpenFileKeyboardSelectsDocumentWithoutAttach(t *testing.T) {
	for _, focus := range []ui.ComponentID{FilterID, ListID, ActionsID} {
		t.Run(string(focus), func(t *testing.T) {
			screen, host := pickerHost(t, ModeOpenFile)
			host.Handle(tea.KeyPressMsg{Code: 'b', Text: "b"})
			if !host.Focus(focus) {
				t.Fatal("could not focus control")
			}
			assertDocumentSelection(t, screen, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}), pickerPath("b.txt"))
			if screen.TogglePath(pickerPath("b.txt")) {
				t.Fatal("document mode accepted attachment toggle")
			}
		})
	}
}

func TestOpenFileMouseArmsThenOpensExactFile(t *testing.T) {
	screen, host := pickerHost(t, ModeOpenFile)
	click := tea.MouseClickMsg{X: 3, Y: 8, Button: tea.MouseLeft}
	first := host.Handle(click)
	if first.Cmd != nil || !first.Handled || screen.FocusedPath() != pickerPath("a.txt") {
		t.Fatalf("first click must only select file: %#v path=%q", first, screen.FocusedPath())
	}
	assertDocumentSelection(t, screen, host.Handle(click), pickerPath("a.txt"))

	screen, host = pickerHost(t, ModeOpenFile)
	host.Handle(click)
	button, ok := screen.actions.ButtonBounds("filepicker.select")
	if !ok {
		t.Fatal("missing Open File button")
	}
	assertDocumentSelection(t, screen, host.Handle(tea.MouseClickMsg{
		X:      screen.actions.Bounds().X + button.X + 1,
		Y:      screen.actions.Bounds().Y + button.Y,
		Button: tea.MouseLeft,
	}), pickerPath("a.txt"))
}

func TestOpenFileDirectoriesAndEmptyStateCannotConfirm(t *testing.T) {
	for _, focus := range []ui.ComponentID{FilterID, ListID} {
		screen, host := pickerHost(t, ModeOpenFile)
		host.Focus(focus)
		load := commandMessage[LoadMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
		if load.Path != pickerPath("src") || !load.ValidFor(screen) {
			t.Fatalf("folder Enter must expand: %#v", load)
		}
	}
	screen, host := pickerHost(t, ModeOpenFile)
	host.Focus(ActionsID)
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil {
		t.Fatalf("folder enabled Open File: %#v", result)
	}
	load := commandMessage[LoadMsg](t, host.Handle(tea.MouseClickMsg{X: 3, Y: 7, Button: tea.MouseLeft}))
	if load.Path != pickerPath("src") {
		t.Fatalf("folder click did not expand: %#v", load)
	}
	nav := commandMessage[NavigateMsg](t, host.Handle(tea.MouseClickMsg{X: 3, Y: 7, Button: tea.MouseLeft}))
	if !nav.ValidFor(screen) || nav.Path != pickerPath("src") {
		t.Fatalf("folder double click did not navigate: %#v", nav)
	}
	screen.SetDirectory(pickerPath(""), []Entry{})
	host.Layout()
	host.Focus(ActionsID)
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil {
		t.Fatalf("empty browser enabled Open File: %#v", result)
	}
}

func TestOpenFilePaintAndSemanticsDescribeDocumentIntent(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, width := range []int{60, 100} {
			screen, host := pickerHost(t, ModeOpenFile)
			host.SetTheme(th)
			host.Resize(width, 18)
			host.Layout()
			buf := surface.NewCellBuffer(width, 18)
			host.Paint(buf)
			text := pickerBufferText(buf)
			for _, want := range []string{"OPEN FILE", "Choose a file to open in this pane", "Open File", "Cancel", "a.txt"} {
				if !strings.Contains(text, want) {
					t.Fatalf("width=%d missing %q:\n%s", width, want, text)
				}
			}
			if width == 100 && !strings.Contains(text, "Choose a file to open in this pane · fuzzy-search as you type") {
				t.Fatalf("missing complete subtitle:\n%s", text)
			}
			for _, unwanted := range []string{"ATTACH", "Attach", "conversation", "selected"} {
				if strings.Contains(text, unwanted) {
					t.Fatalf("document chrome contains %q:\n%s", unwanted, text)
				}
			}
			if sem := screen.Semantics(); sem.Role != ui.RoleDialog || sem.Label != "Open File" {
				t.Fatalf("document semantics=%#v", sem)
			}
		}
	}
}

func TestOpenFileSearchAndDirectoryIntentsRetainOwnerFences(t *testing.T) {
	screen, host := pickerHost(t, ModeOpenFile)
	search := commandMessage[SearchMsg](t, host.Handle(tea.KeyPressMsg{Code: 'b', Text: "b"}))
	if !search.ValidFor(screen) || search.ValidFor(New(nil)) || !screen.BeginPathSearch(search.Query) {
		t.Fatal("search lost exact owner")
	}
	if !screen.ApplyPathSearch(search.Query, []filetree.PathMatch{{Entry: pathwalk.Entry{Path: pickerPath("nested/b.txt"), Name: "b.txt", Relative: "nested/b.txt"}}}) {
		t.Fatal("search result not applied")
	}
	// The result projection includes ancestor rows; focus the matched file itself.
	screen.model.Select(pickerPath("nested/b.txt"))
	screen.sync()
	selected := assertDocumentSelection(t, screen, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}), pickerPath("nested/b.txt"))
	if search.ValidFor(screen) || selected.ValidFor(New(nil)) {
		t.Fatal("selection did not supersede search with exact owner")
	}
	screen.Close()
	screen.Open(pickerPath(""), ModeOpenFile, pickerEntries())
	if search.ValidFor(screen) || selected.ValidFor(screen) {
		t.Fatal("reopened picker accepted old search/selection")
	}
	host.Layout()
	host.Focus(ListID)
	load := commandMessage[LoadMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if !screen.BeginAsyncDirectoryExpandFor(load.Path, load.Intent()) {
		t.Fatal("current expansion did not start")
	}
	if result := host.Handle(tea.MouseClickMsg{X: 3, Y: 8, Button: tea.MouseLeft}); result.Cmd != nil {
		t.Fatalf("file activated during directory load: %#v", result)
	}
	screen.Close()
	screen.Open(pickerPath(""), ModeOpenFile, pickerEntries())
	if screen.CompleteAsyncDirectoryLoadFor(load.Intent(), load.Path, []Entry{{Name: "stale.go", Path: pickerPath("src/stale.go")}}) {
		t.Fatal("old directory result mutated reopened document picker")
	}
}

func TestAttachModeStillTogglesMultipleFilesOnSingleClick(t *testing.T) {
	screen, host := pickerHost(t, ModeFile)
	for _, click := range []tea.MouseClickMsg{{X: 3, Y: 8, Button: tea.MouseLeft}, {X: 3, Y: 9, Button: tea.MouseLeft}} {
		toggle := commandMessage[ToggleMsg](t, host.Handle(click))
		if !toggle.ValidFor(screen) || !screen.TogglePath(toggle.Path) {
			t.Fatal("attach click failed to toggle")
		}
	}
	host.Focus(ActionsID)
	msg := commandMessage[SelectMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if !msg.ValidFor(screen) || msg.Folder || len(msg.Paths) != 2 || msg.Paths[0] != pickerPath("a.txt") || msg.Paths[1] != pickerPath("b.txt") {
		t.Fatalf("attach multi-select changed: %#v", msg)
	}
}
