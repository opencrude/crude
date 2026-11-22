package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/core/shellport"
	"crude/tui/bidi"
	"crude/tui/termview"
	"crude/types/setting"
)

// Saved right-to-left settings reach every painter, and a host terminal
// that always arranges text itself stops Crude from arranging it twice.
func TestRightToLeftSettingsAndHostReportPublishThePolicy(t *testing.T) {
	defer bidi.SetPresentation(bidi.Current())
	defer bidi.SetVisualCaret(bidi.VisualCaret())
	defer termview.SetArrangement(termview.CurrentArrangement())
	app := newTestApp(t)
	save := func(edit func(*setting.App)) {
		t.Helper()
		settings := app.ws.LoadAppSettings()
		edit(&settings)
		if err := wsOf(app).SaveAppSettings(settings); err != nil {
			t.Fatal(err)
		}
		app.Update(shellport.SettingsChangedMsg{})
	}

	save(func(s *setting.App) {})
	if got := bidi.Current(); got != (bidi.Presentation{Arrange: true, Join: true}) || !bidi.VisualCaret() || termview.CurrentArrangement() != termview.ArrangeShell {
		t.Fatalf("defaults: presentation %+v visual caret %v terminal %d", got, bidi.VisualCaret(), termview.CurrentArrangement())
	}
	save(func(s *setting.App) {
		s.UI.RightToLeft = setting.RightToLeftUnjoined
		s.UI.CaretMovement = setting.CaretMovementLogical
		s.Terminal.RightToLeft = setting.TerminalRightToLeftAll
	})
	if got := bidi.Current(); got != (bidi.Presentation{Arrange: true}) || bidi.VisualCaret() || termview.CurrentArrangement() != termview.ArrangeAll {
		t.Fatalf("explicit choices: presentation %+v visual caret %v terminal %d", got, bidi.VisualCaret(), termview.CurrentArrangement())
	}
	save(func(s *setting.App) { s.UI.RightToLeft = setting.RightToLeftTerminal })
	if bidi.Current().Arrange {
		t.Fatal("Terminal choice still arranges text in Crude")
	}

	save(func(s *setting.App) { s.UI.RightToLeft = "" })
	app.Update(tea.ModeReportMsg{Mode: ansi.ModeBiDirectionalSupport, Value: ansi.ModePermanentlySet})
	if bidi.Current().Arrange {
		t.Fatal("a terminal that always arranges text gets it arranged twice")
	}
	app.Update(tea.ModeReportMsg{Mode: ansi.ModeBiDirectionalSupport, Value: ansi.ModeReset})
	if !bidi.Current().Arrange {
		t.Fatal("a terminal in explicit mode did not get arranged text")
	}
}

// runStartCmds executes a start-up command batch and returns its messages.
func runStartCmds(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, runStartCmds(c)...)
	}
	return out
}

// The explicit-mode request used to be sent while the App was built, before
// any program could receive it, and was then never sent again: VTE
// terminals (Tilix, GNOME Terminal) kept their implicit mode and reversed
// Crude's arranged text a second time. The request now starts with the
// program, and later changes reach the running program.
func TestHostBidiRequestReachesTheProgramThatStarts(t *testing.T) {
	defer bidi.SetPresentation(bidi.Current())
	app := newTestApp(t)
	sent := make(chan tea.Msg, 16)
	defer installDetachedMessageSinkForTest(app, func(msg tea.Msg) { sent <- msg })()

	app.applyTextDirectionSettings(app.ws.LoadAppSettings())
	select {
	case msg := <-sent:
		t.Fatalf("host mode sent before the program runs: %v", msg)
	case <-time.After(50 * time.Millisecond):
	}
	start := runStartCmds(app.hostBidiStartCmd())
	explicit := tea.SetHostBidiExplicit(true)()
	found := false
	for _, msg := range start {
		if msg == explicit {
			found = true
		}
	}
	if !found {
		t.Fatalf("program start does not ask the terminal for explicit mode: %#v", start)
	}

	settings := app.ws.LoadAppSettings()
	settings.UI.RightToLeft = setting.RightToLeftTerminal
	app.applyTextDirectionSettings(settings)
	select {
	case msg := <-sent:
		if msg != tea.SetHostBidiExplicit(false)() {
			t.Fatalf("choosing Terminal sent %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("choosing Terminal did not give the terminal its own mode back")
	}
}

// Konsole reorders every row itself and has no sequence to stop it. With the
// automatic choice Crude leaves the cells in logical order there; an
// explicit choice still arranges them (the user may have turned Konsole's
// own BiDi rendering off).
func TestKonsoleKeepsItsOwnReorderingUnlessTheUserChoosesCrude(t *testing.T) {
	defer bidi.SetPresentation(bidi.Current())
	app := newTestApp(t)
	defer installDetachedMessageSinkForTest(app, func(tea.Msg) {})()
	app.applyTextDirectionSettings(app.ws.LoadAppSettings())
	if !bidi.Current().Arrange {
		t.Fatal("default does not arrange text")
	}
	app.Update(tea.TerminalVersionMsg{Name: "Konsole 23.08.5"})
	if bidi.Current().Arrange {
		t.Fatal("Konsole gets arranged text that it reverses again")
	}
	settings := app.ws.LoadAppSettings()
	settings.UI.RightToLeft = setting.RightToLeftArrange
	if err := wsOf(app).SaveAppSettings(settings); err != nil {
		t.Fatal(err)
	}
	app.Update(shellport.SettingsChangedMsg{})
	if got := bidi.Current(); !got.Arrange || !got.Join {
		t.Fatalf("explicit Join letters ignored in Konsole: %+v", got)
	}
	for _, name := range []string{"kitty(0.50.0)", "ghostty 1.3.1", "Kwi (libghostty 1.0.1)", "VTE(7600)", "WezTerm 20240203-110809-5046fc22", "XTerm(390)"} {
		if terminalAlwaysReorders(name) {
			t.Fatalf("%s treated as a terminal that always reorders", name)
		}
	}
}
