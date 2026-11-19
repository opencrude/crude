package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/bidi"
	"crude/tui/termview"
	"crude/types"
)

// Right-to-left text has one owner per decision (docs/reference/bidi.md):
// the Interface setting chooses who arranges it, the host terminal's DECRPM
// report and its XTVERSION identity can veto Crude arranging it, and this
// file publishes the result to the process-wide policy every painter reads.
// Editors take their line direction from the Editor settings
// (applyEditorSettings).

// hostBidi records what the host terminal said about its own BiDi behavior
// and which mode Crude handed to the renderer.
type hostBidi struct {
	// permanent is true when the terminal reported BDSM permanently set: it
	// always arranges text itself and cannot be asked to stop.
	permanent bool
	// reorders is true when the terminal identified itself as one that
	// always reorders right-to-left text and has no control sequence to stop
	// it (Konsole). Unlike permanent, an explicit setting overrides it: the
	// user may have turned the terminal's own BiDi rendering off.
	reorders bool
	// want is the explicit mode Crude needs; sent is the mode last handed to
	// the renderer, valid once delivered.
	want, sent, delivered bool
	// running is set when the program's event loop starts (Init). Before
	// that no message can reach the renderer, so Init delivers the first
	// request with the program's start-up commands.
	running bool
}

// applyTextDirectionSettings publishes the right-to-left preferences and
// asks the host terminal for explicit BiDi mode when Crude arranges text.
func (m *App) applyTextDirectionSettings(settings types.AppSettings) {
	choice := settings.UI.RightToLeftOrDefault()
	hostArranges := m.hostBidi.permanent || choice == types.RightToLeftAuto && m.hostBidi.reorders
	crude := choice != types.RightToLeftTerminal && !hostArranges
	bidi.SetPresentation(bidi.Presentation{Arrange: crude, Join: crude && choice != types.RightToLeftUnjoined})
	bidi.SetVisualCaret(settings.UI.CaretMovementOrDefault() == types.CaretMovementVisual)
	switch settings.Terminal.RightToLeftOrDefault() {
	case types.TerminalRightToLeftAll:
		termview.SetArrangement(termview.ArrangeAll)
	case types.TerminalRightToLeftOff:
		termview.SetArrangement(termview.ArrangeNone)
	default:
		termview.SetArrangement(termview.ArrangeShell)
	}
	// The terminal must not arrange cells a second time. When it arranges
	// them itself, it gets its own behavior back.
	m.hostBidi.want = crude
	m.deliverHostBidi()
}

// deliverHostBidi hands the wanted host mode to the renderer once the
// program runs. A request made earlier would have nowhere to go.
func (m *App) deliverHostBidi() {
	h := &m.hostBidi
	if !h.running || h.delivered && h.sent == h.want {
		return
	}
	h.sent, h.delivered = h.want, true
	m.runDetachedAsyncCmd(tea.SetHostBidiExplicit(h.want))
}

// hostBidiStartCmd starts host BiDi negotiation with the program: it sends
// the mode the saved settings need and asks the terminal for its name, which
// tells Crude about terminals that reorder text without reporting BDSM.
func (m *App) hostBidiStartCmd() tea.Cmd {
	h := &m.hostBidi
	h.running = true
	h.sent, h.delivered = h.want, true
	return tea.Batch(tea.SetHostBidiExplicit(h.want), tea.RequestTerminalVersion)
}

// handleHostBidiReport applies the terminal's answer to the startup BDSM
// query. A terminal that always arranges right-to-left text makes Crude
// leave its cells in logical order; arranging them as well would reverse
// them twice.
func (m *App) handleHostBidiReport(msg tea.ModeReportMsg) bool {
	if msg.Mode != ansi.ModeBiDirectionalSupport {
		return false
	}
	m.setHostArranges(msg.Value == ansi.ModePermanentlySet, m.hostBidi.reorders)
	return true
}

// handleTerminalVersion applies the terminal's XTVERSION answer. Konsole
// reorders every row with its own BiDi rendering (on by default) and has no
// control sequence to stop it, so arranged cells would be reversed again.
func (m *App) handleTerminalVersion(msg tea.TerminalVersionMsg) {
	m.setHostArranges(m.hostBidi.permanent, terminalAlwaysReorders(msg.Name))
}

// terminalAlwaysReorders reports whether an XTVERSION name belongs to a
// terminal that reorders right-to-left text and cannot be told to stop.
func terminalAlwaysReorders(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), "Konsole")
}

func (m *App) setHostArranges(permanent, reorders bool) {
	if permanent == m.hostBidi.permanent && reorders == m.hostBidi.reorders {
		return
	}
	m.hostBidi.permanent, m.hostBidi.reorders = permanent, reorders
	if m.ws != nil {
		m.applyTextDirectionSettings(m.ws.LoadAppSettings())
	}
}
