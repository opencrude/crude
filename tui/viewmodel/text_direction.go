package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/bidi"
	"crude/tui/termview"
	"crude/types"
)

// Right-to-left text has one owner per decision (docs/reference/bidi.md):
// the Interface setting chooses who arranges it, the host terminal's DECRPM
// report can veto Crude arranging it, and this file publishes the result to
// the process-wide policy every painter reads. Editors take their line
// direction from the Editor settings (applyEditorSettings).

// hostBidi records what the host terminal said about its own BiDi mode and
// what Crude last asked of it.
type hostBidi struct {
	// permanent is true when the terminal reported BDSM permanently set: it
	// always arranges text itself and cannot be asked to stop.
	permanent bool
	requested bool
	explicit  bool
}

// applyTextDirectionSettings publishes the right-to-left preferences and
// asks the host terminal for explicit BiDi mode when Crude arranges text.
func (m *App) applyTextDirectionSettings(settings types.AppSettings) {
	choice := settings.UI.RightToLeftOrDefault()
	crude := choice != types.RightToLeftTerminal && !m.hostBidi.permanent
	bidi.SetPresentation(bidi.Presentation{Arrange: crude, Join: crude && choice == types.RightToLeftArrange})
	bidi.SetVisualCaret(settings.UI.CaretMovementOrDefault() == types.CaretMovementVisual)
	switch settings.Terminal.RightToLeftOrDefault() {
	case types.TerminalRightToLeftAll:
		termview.SetArrangement(termview.ArrangeAll)
	case types.TerminalRightToLeftOff:
		termview.SetArrangement(termview.ArrangeNone)
	default:
		termview.SetArrangement(termview.ArrangeShell)
	}
	// The terminal must not arrange cells a second time. "Terminal" gives it
	// its own behavior back.
	explicit := choice != types.RightToLeftTerminal
	if m.hostBidi.requested && m.hostBidi.explicit == explicit {
		return
	}
	m.hostBidi.requested, m.hostBidi.explicit = true, explicit
	m.runDetachedAsyncCmd(tea.SetHostBidiExplicit(explicit))
}

// handleHostBidiReport applies the terminal's answer to the startup BDSM
// query. A terminal that always arranges right-to-left text makes Crude
// leave its cells in logical order; arranging them as well would reverse
// them twice.
func (m *App) handleHostBidiReport(msg tea.ModeReportMsg) bool {
	if msg.Mode != ansi.ModeBiDirectionalSupport {
		return false
	}
	permanent := msg.Value == ansi.ModePermanentlySet
	if permanent != m.hostBidi.permanent {
		m.hostBidi.permanent = permanent
		if m.ws != nil {
			m.applyTextDirectionSettings(m.ws.LoadAppSettings())
		}
	}
	return true
}
