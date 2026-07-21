package filepicker

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestFilePickerKeyboardFilterPasteAndTypedResults(t *testing.T) {
	screen, host := pickerHost(t, ModeFile)
	result := host.Handle(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if !result.Handled || screen.VisibleCount() != 1 {
		t.Fatalf("filter result=%#v visible=%d", result, screen.VisibleCount())
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	message := commandMessage[SelectMsg](t, result)
	if message.Target != screen || message.Path != "/root/b.txt" || message.Folder {
		t.Fatalf("select message=%#v", message)
	}

	screen, host = pickerHost(t, ModeFile)
	if !host.Focus(ListID) {
		t.Fatal("could not focus list")
	}
	result = host.Handle(tea.PasteMsg{Content: "a.txt"})
	if !result.Handled || host.Focused() != FilterID || screen.VisibleCount() != 1 {
		t.Fatalf("paste result=%#v focus=%q visible=%d", result, host.Focused(), screen.VisibleCount())
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	cancel := commandMessage[CancelMsg](t, result)
	if cancel.Target != screen {
		t.Fatalf("cancel target=%p, want %p", cancel.Target, screen)
	}
}

func TestFilePickerDirectoryNavigationAndDoubleClick(t *testing.T) {
	screen, host := pickerHost(t, ModeFile)
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	navigate := commandMessage[NavigateMsg](t, result)
	if navigate.Target != screen || navigate.Path != "/root/src" {
		t.Fatalf("keyboard navigate=%#v", navigate)
	}

	screen, host = pickerHost(t, ModeFile)
	first := host.Handle(tea.MouseClickMsg{X: 2, Y: 3, Button: tea.MouseLeft})
	if first.Cmd != nil || first.Action != nil {
		t.Fatalf("first click activated entry: %#v", first)
	}
	second := host.Handle(tea.MouseClickMsg{X: 2, Y: 3, Button: tea.MouseLeft})
	navigate = commandMessage[NavigateMsg](t, second)
	if navigate.Path != "/root/src" {
		t.Fatalf("double-click navigate=%#v", navigate)
	}
}

func TestFolderPickerFiltersFilesAndSelectsCurrentDirectory(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	if screen.VisibleCount() != 2 {
		t.Fatalf("folder rows=%d, want parent plus src", screen.VisibleCount())
	}
	if !host.Focus(ActionsID) {
		t.Fatal("could not focus actions")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	message := commandMessage[SelectMsg](t, result)
	if message.Path != "/root" || !message.Folder {
		t.Fatalf("folder select=%#v", message)
	}
}

func TestFilePickerPrimaryActionTracksSelectedFile(t *testing.T) {
	screen, host := pickerHost(t, ModeFile)
	if !host.Focus(ActionsID) {
		t.Fatal("could not focus actions")
	}
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil {
		selected, _ := screen.Selected()
		t.Fatalf("attach action was enabled for a directory: selected=%#v result=%#v", selected, result)
	}
	if !host.Focus(FilterID) {
		t.Fatal("could not focus filter")
	}
	host.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if !host.Focus(ActionsID) {
		t.Fatal("could not refocus actions")
	}
	message := commandMessage[SelectMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if message.Target != screen || message.Path != "/root/a.txt" {
		t.Fatalf("attach result=%#v", message)
	}
}

func TestFilePickerPaintsNativeControlsAcrossThemesAndSizes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 42, H: 9}, {W: 100, H: 20}} {
			screen := New(th)
			screen.Open("/root", ModeFile, pickerEntries())
			component := ui.NewComponentWidget(th, screen, size.W, size.H)
			screen.SetFocusHandler(component.Focus)
			screen.FocusFilter()
			host := component.Host()
			host.Resize(size.W, size.H)
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			painted := pickerBufferText(buf)
			for _, want := range []string{"Search files", "/root | 3 items", "src", "a.txt", "Attach", "Cancel"} {
				if !strings.Contains(painted, want) {
					t.Fatalf("size=%#v missing %q:\n%s", size, want, painted)
				}
			}
		}
	}
}

func pickerHost(t *testing.T, mode Mode) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	screen.Open("/root", mode, pickerEntries())
	component := ui.NewComponentWidget(th, screen, 80, 14)
	screen.SetFocusHandler(component.Focus)
	screen.FocusFilter()
	host := component.Host()
	host.Resize(80, 14)
	host.Layout()
	return screen, host
}

func pickerEntries() []Entry {
	return []Entry{
		{Name: "b.txt", Path: "/root/b.txt"},
		{Name: "src", Path: "/root/src", IsDir: true},
		{Name: "a.txt", Path: "/root/a.txt"},
		{Name: ".hidden", Path: "/root/.hidden"},
	}
}

func commandMessage[T any](t *testing.T, result ui.EventResult) T {
	t.Helper()
	if result.Cmd == nil {
		t.Fatalf("result did not emit a command: %#v", result)
	}
	message, ok := result.Cmd().(T)
	if !ok {
		t.Fatalf("command message=%T", result.Cmd())
	}
	return message
}

func pickerBufferText(buf *surface.CellBuffer) string {
	var result strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			result.WriteRune(r)
		}
		result.WriteByte('\n')
	}
	return result.String()
}
