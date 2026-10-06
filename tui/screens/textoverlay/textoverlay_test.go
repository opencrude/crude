package textoverlay

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestTextOverlayOpenCloseAndValue(t *testing.T) {
	screen := New(theme.DefaultDark())
	if screen.IsOpen() {
		t.Fatal("new overlay should be closed")
	}
	screen.Open("ref-1", "Edit file", "hello world")
	if !screen.IsOpen() || screen.RefID() != "ref-1" || screen.Value() != "hello world" {
		t.Fatalf("open ref=%q value=%q open=%v", screen.RefID(), screen.Value(), screen.IsOpen())
	}
	screen.Close()
	if screen.IsOpen() || screen.RefID() != "" || screen.Value() != "" {
		t.Fatalf("close ref=%q value=%q open=%v", screen.RefID(), screen.Value(), screen.IsOpen())
	}
}

func TestTextOverlayCtrlSSavesAndEscCancels(t *testing.T) {
	screen, host := overlayHost(t)
	screen.Open("id", "Title", "body")
	result := host.Handle(tea.KeyPressMsg{Text: "ctrl+s", Mod: tea.ModCtrl, Code: 's'})
	save := commandMessage[SaveMsg](t, result)
	if save.Target != screen || save.RefID != "id" || save.Text != "body" {
		t.Fatalf("save=%#v", save)
	}

	screen, host = overlayHost(t)
	screen.Open("id", "Title", "body")
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	cancel := commandMessage[CancelMsg](t, result)
	if cancel.Target != screen {
		t.Fatalf("cancel=%#v", cancel)
	}
}

func TestTextOverlayEscClearsSelectionThenCancels(t *testing.T) {
	screen, host := overlayHost(t)
	screen.Open("id", "Title", "abcdef")
	screen.input.area.SetSelection(0, 3)
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !result.Handled || result.Cmd != nil || screen.HasActiveSelection() {
		t.Fatalf("first esc should clear selection: %#v", result)
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	_ = commandMessage[CancelMsg](t, result)
}

func TestTextOverlayCtrlCWithoutSelectionDoesNotCancel(t *testing.T) {
	screen, host := overlayHost(t)
	screen.Open("id", "Title", "text")
	result := host.Handle(tea.KeyPressMsg{Text: "ctrl+c", Mod: tea.ModCtrl, Code: 'c'})
	if result.Cmd != nil || !screen.IsOpen() || screen.Value() != "text" {
		t.Fatalf("ctrl+c closed or changed editor: %#v", result)
	}
}

func TestTextOverlayTypingUpdatesValue(t *testing.T) {
	screen, host := overlayHost(t)
	screen.Open("id", "Title", "")
	result := host.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if !result.Handled || screen.Value() != "a" {
		t.Fatalf("type result=%#v value=%q", result, screen.Value())
	}
}

func TestTextOverlayActionBarSaveAndCancel(t *testing.T) {
	screen, host := overlayHost(t)
	screen.Open("id", "Title", "saved")
	if !host.Focus(ActionsID) {
		t.Fatal("focus actions")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	save := commandMessage[SaveMsg](t, result)
	if save.Text != "saved" || save.RefID != "id" {
		t.Fatalf("actionbar save=%#v", save)
	}

	screen, host = overlayHost(t)
	screen.Open("id", "Title", "x")
	if !host.Focus(ActionsID) {
		t.Fatal("focus actions")
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	_ = commandMessage[CancelMsg](t, result)
}

func TestTextOverlayPaintsNativeControlsAcrossThemesAndSizes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 40, H: 10}, {W: 80, H: 24}} {
			screen := New(th)
			screen.Open("id", "Title", "overlay body")
			component := ui.NewComponentWidget(th, screen, size.W, size.H)
			screen.SetFocusHandler(component.Focus)
			screen.FocusInput()
			host := component.Host()
			host.Resize(size.W, size.H)
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			painted := bufferText(buf)
			if !strings.Contains(painted, "overlay body") {
				t.Fatalf("theme light=%v size=%#v missing body:\n%s", th == theme.DefaultLight(), size, painted)
			}
			if !strings.Contains(painted, "Save") || !strings.Contains(painted, "Cancel") {
				t.Fatalf("theme light=%v size=%#v missing action bar:\n%s", th == theme.DefaultLight(), size, painted)
			}
		}
	}
}

// TestTextOverlayReadOnlyPreview guards the file-backed attachment preview:
// content is visible but inert — no typing, no Ctrl+S save — while reading
// (navigation, selection copy, Esc dismiss) keeps working.
func TestTextOverlayReadOnlyPreview(t *testing.T) {
	screen, host := overlayHost(t)
	screen.OpenReadOnly("ref-9", "Preview: notes.txt", "binary preview body")
	if !screen.ReadOnly() {
		t.Fatal("OpenReadOnly must mark the screen read-only")
	}

	// Typing must not mutate the previewed content.
	if result := host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"}); !result.Handled || screen.Value() != "binary preview body" {
		t.Fatalf("read-only typing result=%#v value=%q", result, screen.Value())
	}
	// Enter would insert a newline in a multi-line area — also swallowed.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Contains(screen.Value(), "\n") {
		t.Fatalf("read-only enter mutated value: %q", screen.Value())
	}

	// Navigation still works: cursor moves to the end of the text.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})

	// Ctrl+S is inert — no SaveMsg may be emitted.
	result := host.Handle(tea.KeyPressMsg{Text: "ctrl+s", Mod: tea.ModCtrl, Code: 's'})
	if result.Cmd != nil || !result.Handled {
		t.Fatalf("read-only ctrl+s must be inert, got %#v", result)
	}
	if len(screen.actions.ButtonLabelsForTest()) == 0 {
		t.Fatal("read-only action bar must still render buttons")
	}
	for _, label := range screen.actions.ButtonLabelsForTest() {
		if label == "Save" {
			t.Fatal("read-only action bar must not offer Save")
		}
	}

	// Esc dismisses without persisting anything.
	cancel := commandMessage[CancelMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if cancel.Target != screen {
		t.Fatalf("cancel=%#v", cancel)
	}
}

// TestTextOverlayOpenResetsReadOnly guards reuse of the retained per-session
// overlay screen: a regular Open after a read-only preview restores editing.
func TestTextOverlayOpenResetsReadOnly(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.OpenReadOnly("a", "Preview", "content")
	if !screen.ReadOnly() {
		t.Fatal("expected read-only after OpenReadOnly")
	}
	screen.Open("b", "Edit ref", "draft text")
	if screen.ReadOnly() {
		t.Fatal("regular Open must restore editability")
	}
	labels := screen.actions.ButtonLabelsForTest()
	hasSave := false
	for _, l := range labels {
		if strings.HasPrefix(l, "Save") {
			hasSave = true
		}
	}
	if !hasSave {
		t.Fatalf("editable overlay lost its Save button: %v", labels)
	}
}

func overlayHost(t *testing.T) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	component := ui.NewComponentWidget(th, screen, 72, 16)
	screen.SetFocusHandler(component.Focus)
	screen.FocusInput()
	host := component.Host()
	host.Resize(72, 16)
	host.Layout()
	return screen, host
}

func commandMessage[T any](t *testing.T, result ui.EventResult) T {
	t.Helper()
	if result.Cmd == nil {
		t.Fatalf("result did not emit a command: %#v", result)
	}
	message, ok := result.Cmd().(T)
	if !ok {
		t.Fatalf("command message=%T %#v", result.Cmd(), result.Cmd())
	}
	return message
}

func bufferText(buf *surface.CellBuffer) string {
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
