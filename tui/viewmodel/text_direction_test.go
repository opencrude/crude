package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/shellport"
	"crude/tui/bidi"
	"crude/tui/termview"
	"crude/types"
)

// Saved right-to-left settings reach every painter, and a host terminal
// that always arranges text itself stops Crude from arranging it twice.
func TestRightToLeftSettingsAndHostReportPublishThePolicy(t *testing.T) {
	defer bidi.SetPresentation(bidi.Current())
	defer bidi.SetVisualCaret(bidi.VisualCaret())
	defer termview.SetArrangement(termview.CurrentArrangement())
	app := newTestApp(t)
	save := func(edit func(*types.AppSettings)) {
		t.Helper()
		settings := app.ws.LoadAppSettings()
		edit(&settings)
		if err := wsOf(app).SaveAppSettings(settings); err != nil {
			t.Fatal(err)
		}
		app.Update(shellport.SettingsChangedMsg{})
	}

	save(func(s *types.AppSettings) {})
	if got := bidi.Current(); got != (bidi.Presentation{Arrange: true, Join: true}) || !bidi.VisualCaret() || termview.CurrentArrangement() != termview.ArrangeShell {
		t.Fatalf("defaults: presentation %+v visual caret %v terminal %d", got, bidi.VisualCaret(), termview.CurrentArrangement())
	}
	save(func(s *types.AppSettings) {
		s.UI.RightToLeft = types.RightToLeftUnjoined
		s.UI.CaretMovement = types.CaretMovementLogical
		s.Terminal.RightToLeft = types.TerminalRightToLeftAll
	})
	if got := bidi.Current(); got != (bidi.Presentation{Arrange: true}) || bidi.VisualCaret() || termview.CurrentArrangement() != termview.ArrangeAll {
		t.Fatalf("explicit choices: presentation %+v visual caret %v terminal %d", got, bidi.VisualCaret(), termview.CurrentArrangement())
	}
	save(func(s *types.AppSettings) { s.UI.RightToLeft = types.RightToLeftTerminal })
	if bidi.Current().Arrange {
		t.Fatal("Terminal choice still arranges text in Crude")
	}

	save(func(s *types.AppSettings) { s.UI.RightToLeft = "" })
	app.Update(tea.ModeReportMsg{Mode: ansi.ModeBiDirectionalSupport, Value: ansi.ModePermanentlySet})
	if bidi.Current().Arrange {
		t.Fatal("a terminal that always arranges text gets it arranged twice")
	}
	app.Update(tea.ModeReportMsg{Mode: ansi.ModeBiDirectionalSupport, Value: ansi.ModeReset})
	if !bidi.Current().Arrange {
		t.Fatal("a terminal in explicit mode did not get arranged text")
	}
}
