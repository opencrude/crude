package composer

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestComposerOpenCloseAndValue(t *testing.T) {
	screen := New(theme.DefaultDark())
	if screen.IsOpen() {
		t.Fatal("new composer should be closed")
	}
	screen.Open("hello")
	if !screen.IsOpen() || screen.Value() != "hello" {
		t.Fatalf("open value=%q open=%v", screen.Value(), screen.IsOpen())
	}
	screen.SetValue("world")
	if screen.Value() != "world" {
		t.Fatalf("set value=%q", screen.Value())
	}
	screen.Close()
	if screen.IsOpen() || screen.Value() != "" {
		t.Fatalf("close value=%q open=%v", screen.Value(), screen.IsOpen())
	}
}

func TestComposerCtrlEnterSubmitsAndEnterInsertsNewline(t *testing.T) {
	screen, host := composerHost(t)
	screen.Open("line1")
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !result.Handled || screen.Value() != "line1\n" {
		t.Fatalf("enter result=%#v value=%q", result, screen.Value())
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	submit := commandMessage[SubmitMsg](t, result)
	if submit.Target != screen || submit.Text != "line1\n" {
		t.Fatalf("submit=%#v", submit)
	}
}

func TestComposerEscClearsSelectionThenCancels(t *testing.T) {
	screen, host := composerHost(t)
	screen.Open("abcdef")
	screen.input.area.SetSelection(1, 4)
	if !screen.HasActiveSelection() {
		t.Fatal("expected selection")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !result.Handled || result.Cmd != nil || screen.HasActiveSelection() {
		t.Fatalf("first esc should clear selection: %#v selected=%v", result, screen.HasActiveSelection())
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	cancel := commandMessage[CancelMsg](t, result)
	if cancel.Target != screen {
		t.Fatalf("cancel target=%p want %p", cancel.Target, screen)
	}
}

func TestComposerActionBarSubmitAndCancel(t *testing.T) {
	screen, host := composerHost(t)
	screen.Open("prompt")
	if !host.Focus(ActionsID) {
		t.Fatal("focus actions")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	submit := commandMessage[SubmitMsg](t, result)
	if submit.Text != "prompt" {
		t.Fatalf("actionbar submit=%#v", submit)
	}

	screen, host = composerHost(t)
	screen.Open("x")
	if !host.Focus(ActionsID) {
		t.Fatal("focus actions")
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	cancel := commandMessage[CancelMsg](t, result)
	if cancel.Target != screen {
		t.Fatalf("actionbar cancel=%#v", cancel)
	}
}

func TestComposerPaintsNativeControlsAcrossThemesAndSizes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 40, H: 10}, {W: 80, H: 20}} {
			screen := New(th)
			screen.Open("hello composer")
			component := ui.NewComponentWidget(th, screen, size.W, size.H)
			screen.SetFocusHandler(component.Focus)
			screen.FocusInput()
			host := component.Host()
			host.Resize(size.W, size.H)
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			painted := bufferText(buf)
			if !strings.Contains(painted, "hello composer") {
				t.Fatalf("theme light=%v size=%#v missing body:\n%s", th == theme.DefaultLight(), size, painted)
			}
			if !strings.Contains(painted, "Submit") || !strings.Contains(painted, "Cancel") {
				t.Fatalf("theme light=%v size=%#v missing action bar:\n%s", th == theme.DefaultLight(), size, painted)
			}
		}
	}
}

func TestComposerDragSelectionCopiesOnRelease(t *testing.T) {
	screen, host := composerHost(t)
	screen.Open("")
	result := host.Handle(tea.PasteMsg{Content: "pasted"})
	if !result.Handled || screen.Value() != "pasted" {
		t.Fatalf("paste result=%#v value=%q", result, screen.Value())
	}
	var copied []string
	screen.input.area.SetClipboardCopy(func(text string) tea.Cmd {
		return func() tea.Msg {
			copied = append(copied, text)
			return nil
		}
	})
	host.Handle(tea.MouseClickMsg{X: 3, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: 7, Y: 0, Button: tea.MouseLeft})
	if !screen.HasActiveSelection() || screen.SelectedText() == "" {
		t.Fatalf("expected a live selection during the drag, got %q selecting=%v", screen.SelectedText(), screen.Selecting())
	}
	released := host.Handle(tea.MouseReleaseMsg{X: 7, Y: 0, Button: tea.MouseLeft})
	// Copy-on-release: the drag writes the selection and drops it, so the composer
	// never keeps selection state the pointer no longer owns.
	if screen.HasActiveSelection() || screen.Selecting() {
		t.Fatalf("release kept selection %q selecting=%v", screen.SelectedText(), screen.Selecting())
	}
	if released.Cmd == nil {
		t.Fatal("release returned no clipboard command")
	}
	if len(copied) != 0 {
		t.Fatalf("clipboard write ran synchronously: copied=%q", copied)
	}
	_ = released.Cmd()
	if len(copied) != 1 || copied[0] != "ted" {
		t.Fatalf("copied=%q want [ted]", copied)
	}
}

func composerHost(t *testing.T) (*Screen, *ui.Host) {
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
