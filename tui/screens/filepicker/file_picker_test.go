package filepicker

import (
	"os"
	"path/filepath"
	"strconv"
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
	if result.Cmd != nil || len(screen.SelectedPaths()) != 1 || screen.SelectedPaths()[0] != pickerPath("b.txt") {
		t.Fatalf("file activation result=%#v selected=%v", result, screen.SelectedPaths())
	}

	screen, host = pickerHost(t, ModeFile)
	if !host.Focus(ListID) {
		t.Fatal("could not focus tree")
	}
	result = host.Handle(tea.PasteMsg{Content: "a.txt"})
	if !result.Handled || host.Focused() != FilterID || screen.VisibleCount() != 1 || result.Cmd == nil {
		t.Fatalf("paste result=%#v focus=%q visible=%d", result, host.Focused(), screen.VisibleCount())
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if result.Cmd == nil {
		t.Fatal("first escape did not clear the active search")
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	cancel := commandMessage[CancelMsg](t, result)
	if cancel.Target != screen {
		t.Fatalf("cancel target=%p, want %p", cancel.Target, screen)
	}
}

func TestFilePickerFilterNeverUsesStaleTextSelectionAfterMouseEdit(t *testing.T) {
	screen, host := pickerHost(t, ModeFile)
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("picker filter panicked after mouse selection edit: %v", recovered)
		}
	}()
	host.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
	host.Handle(tea.MouseClickMsg{X: 2, Y: 3, Button: tea.MouseLeft})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyBackspace})
	host.Handle(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if screen == nil {
		t.Fatal("picker screen unexpectedly nil")
	}
}

func TestFilePickerDirectoryNavigationAndDoubleClick(t *testing.T) {
	screen, host := pickerHost(t, ModeFile)
	if !host.Focus(ListID) {
		t.Fatal("could not focus tree")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	load := commandMessage[LoadMsg](t, result)
	if load.Target != screen || load.Path != pickerPath("src") {
		t.Fatalf("keyboard load=%#v", load)
	}

	_, host = pickerHost(t, ModeFile)
	if !host.Focus(ListID) {
		t.Fatal("could not focus tree")
	}
	first := host.Handle(tea.MouseClickMsg{X: 2, Y: 7, Button: tea.MouseLeft})
	load = commandMessage[LoadMsg](t, first)
	if load.Path != pickerPath("src") {
		t.Fatalf("first click load=%#v", load)
	}
	second := host.Handle(tea.MouseClickMsg{X: 2, Y: 7, Button: tea.MouseLeft})
	navigate := commandMessage[NavigateMsg](t, second)
	if navigate.Path != pickerPath("src") {
		t.Fatalf("double-click navigate=%#v", navigate)
	}
}

func TestFolderPickerFiltersFilesAndSelectsCurrentDirectory(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	if screen.VisibleCount() != 1 {
		t.Fatalf("folder rows=%d, want src without parent pseudo-row", screen.VisibleCount())
	}
	if !host.Focus(ActionsID) {
		t.Fatal("could not focus actions")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	message := commandMessage[SelectMsg](t, result)
	if message.Path != pickerPath("") || !message.Folder {
		t.Fatalf("folder select=%#v", message)
	}
}

func TestFolderPickerUsesFuzzySearchAndWorkspaceChrome(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	host.Handle(tea.KeyPressMsg{Code: 's', Text: "s"})
	host.Handle(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if screen.VisibleCount() != 1 {
		t.Fatalf("fuzzy 'sc' should match src, visible=%d", screen.VisibleCount())
	}

	buf := surface.NewCellBuffer(80, 14)
	host.Paint(buf)
	painted := pickerBufferText(buf)
	for _, want := range []string{"◆ crude", "OPEN WORKSPACE", "fuzzy-search", "Open Workspace", "src"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("missing %q:\n%s", want, painted)
		}
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
	if !host.Focus(ListID) {
		t.Fatal("could not refocus tree")
	}
	if result := host.Handle(tea.KeyPressMsg{Code: ' ', Text: " "}); result.Cmd == nil {
		t.Fatal("space did not emit file selection toggle")
	} else {
		toggle := commandMessage[ToggleMsg](t, result)
		if !toggle.ValidFor(screen) {
			t.Fatal("toggle intent was not current")
		}
		screen.TogglePath(toggle.Path)
	}
	if !host.Focus(ActionsID) {
		t.Fatal("could not refocus actions")
	}
	message := commandMessage[SelectMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if message.Target != screen || message.Path != pickerPath("a.txt") || len(message.Paths) != 1 {
		t.Fatalf("attach result=%#v", message)
	}
}

func TestFilePickerPaintsNativeControlsAcrossThemesAndSizes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 60, H: 18}, {W: 100, H: 28}} {
			screen := New(th)
			screen.Open(pickerPath(""), ModeFile, pickerEntries())
			component := ui.NewComponentWidget(th, screen, size.W, size.H)
			screen.SetFocusHandler(component.Focus)
			screen.FocusFilter()
			host := component.Host()
			host.Resize(size.W, size.H)
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			painted := pickerBufferText(buf)
			for _, want := range []string{"Search files and folders", "root", "src", "a.txt", "Attach Files", "Cancel"} {
				if !strings.Contains(painted, want) {
					t.Fatalf("size=%#v missing %q:\n%s", size, want, painted)
				}
			}
		}
	}
}

func TestFilePickerBreadcrumbSegmentEmitsNavigation(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Open(pickerPath("src/nested"), ModeFolder, []Entry{})
	component := ui.NewComponentWidget(theme.DefaultDark(), screen, 100, 28)
	screen.SetFocusHandler(component.Focus)
	host := component.Host()
	host.Resize(100, 28)
	host.Layout()
	if len(screen.breadcrumb.parts) < 3 {
		t.Fatalf("breadcrumb parts=%v", screen.breadcrumb.parts)
	}
	buf := surface.NewCellBuffer(100, 28)
	host.Paint(buf)
	sep := string(ui.FirstRune(screen.tokens.Glyphs().BreadcrumbSep, '›'))
	if painted := pickerBufferText(buf); !strings.Contains(painted, sep) {
		t.Fatalf("breadcrumb separators were not painted:\n%s", painted)
	}
	part := screen.breadcrumb.parts[1]
	result := host.Handle(tea.MouseClickMsg{
		X:      screen.breadcrumb.Bounds().X + part.x + 1,
		Y:      screen.breadcrumb.Bounds().Y,
		Button: tea.MouseLeft,
	})
	navigate := commandMessage[NavigateMsg](t, result)
	if navigate.Path != part.path {
		t.Fatalf("breadcrumb navigation=%#v want path=%q", navigate, part.path)
	}
}

func TestFilePickerChildExpansionUsesLoadMessage(t *testing.T) {
	root := pickerPath("")
	screen, host := pickerHost(t, ModeFile)
	screen.Open(root, ModeFile, []Entry{{Name: "src", Path: filepath.Join(root, "src"), IsDir: true}})
	host.Resize(80, 14)
	host.Layout()
	if !host.Focus(ListID) {
		t.Fatal("could not focus tree")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	load := commandMessage[LoadMsg](t, result)
	if !screen.BeginAsyncDirectoryExpandFor(load.Path, load.Intent()) {
		t.Fatal("child expansion did not enter loading state")
	}
	if !screen.CompleteAsyncDirectoryLoadFor(load.Intent(), load.Path, []Entry{{Name: "main.go", Path: filepath.Join(load.Path, "main.go")}}) {
		t.Fatal("child expansion result was not applied")
	}
	paths := screen.VisiblePaths()
	if len(paths) != 2 || paths[0] != filepath.Join(root, "src") || paths[1] != filepath.Join(root, "src", "main.go") {
		t.Fatalf("expanded paths=%v", paths)
	}
}

func TestFilePickerTreeUsesPopupSurfaceAndInitialScrollbarGeometry(t *testing.T) {
	root := pickerPath("")
	entries := make([]Entry, 0, 32)
	for i := 0; i < 32; i++ {
		name := "folder-" + strconv.Itoa(i)
		entries = append(entries, Entry{Name: name, Path: filepath.Join(root, name), IsDir: true})
	}
	screen := New(theme.DefaultDark())
	screen.Open(root, ModeFolder, entries)
	component := ui.NewComponentWidget(theme.DefaultDark(), screen, 96, 28)
	screen.SetFocusHandler(component.Focus)
	host := component.Host()
	host.Resize(96, 28)
	host.Layout()
	if got := screen.scrollbar.Bounds().W; got == 0 {
		t.Fatal("picker scrollbar was not allocated during initial layout")
	}
	buf := surface.NewCellBuffer(96, 28)
	host.Paint(buf)
	wantBG := ui.NewTokenSet(theme.DefaultDark()).Color(ui.ColorPopupBg)
	if got := buf.Get(4, 20).BG; got != wantBG {
		t.Fatalf("tree background=%v, want popup background=%v screen=%#v tree=%#v", got, wantBG, screen.Bounds(), screen.tree.Bounds())
	}
}

func pickerHost(t *testing.T, mode Mode) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	screen.Open(pickerPath(""), mode, pickerEntries())
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
		{Name: "b.txt", Path: pickerPath("b.txt")},
		{Name: "src", Path: pickerPath("src"), IsDir: true},
		{Name: "a.txt", Path: pickerPath("a.txt")},
		{Name: ".hidden", Path: pickerPath(".hidden")},
	}
}

func pickerPath(relative string) string {
	return filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), "root", filepath.FromSlash(relative))
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
