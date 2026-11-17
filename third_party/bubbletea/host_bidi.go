package tea

import "github.com/charmbracelet/x/ansi"

// Host terminal BiDi (Crude addition).
//
// A program that arranges right-to-left text itself must stop the terminal
// from arranging it a second time. The terminal-wg BiDi recommendation
// (https://terminal-wg.pages.freedesktop.org/bidi/) uses ECMA-48 BDSM for
// this: CSI 8 l selects explicit mode, where the terminal shows cells in the
// order the program wrote them, and SCP (CSI 1 SP k) fixes the line direction
// to left-to-right. Terminals without BiDi ignore both sequences.
//
// The program asks for the mode with DECRQM at startup. A terminal that
// reports BDSM as permanently set always arranges text itself; Bubble Tea
// then never writes the mode, and the application learns this from the
// ModeReportMsg it also receives. On exit, suspend or when the application
// gives the choice back, the terminal returns to its default direction and,
// unless it reported explicit mode to begin with, to implicit mode.

const (
	selectCharacterPathLTR     = "\x1b[1 k"
	selectCharacterPathDefault = "\x1b[0 k"
)

type hostBidiMsg bool

// SetHostBidiExplicit asks the terminal to show cells exactly in the order
// the program writes them (explicit BiDi mode), or gives the terminal its
// own BiDi behavior back. Use it when the program arranges bidirectional
// text itself.
func SetHostBidiExplicit(explicit bool) Cmd {
	return func() Msg { return hostBidiMsg(explicit) }
}

// setHostBidi implements renderer.
func (s *cursedRenderer) setHostBidi(explicit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bidiWanted = explicit
	s.syncHostBidiLocked(false)
}

// hostBidiReport implements renderer: the DECRPM reply for BDSM.
func (s *cursedRenderer) hostBidiReport(value ansi.ModeSetting) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bidiHost, s.bidiKnown = value, true
	// A terminal in implicit mode keeps arranging lines it drew before it
	// switched; redraw everything under the explicit mode once.
	s.syncHostBidiLocked(value == ansi.ModeSet)
}

func (s *cursedRenderer) syncHostBidiLocked(repaint bool) {
	want := s.bidiWanted && !(s.bidiKnown && s.bidiHost == ansi.ModePermanentlySet)
	switch {
	case want && !s.bidiApplied:
		_, _ = s.scr.WriteString(ansi.ResetModeBiDirectionalSupport + selectCharacterPathLTR)
		s.bidiApplied = true
	case !want && s.bidiApplied:
		s.restoreHostBidiLocked()
	default:
		if !repaint || !s.bidiApplied {
			return
		}
	}
	// Send the change with the next frame even when the view is unchanged.
	s.bidiPending = true
	if s.bidiKnown && (s.bidiHost == ansi.ModeSet || s.bidiHost == ansi.ModePermanentlySet) {
		// Erase in Display gives every cleared line the current BiDi mode
		// (terminal-wg recommendation); the next frame redraws in full.
		s.scr.MoveTo(0, 0)
		s.scr.Erase()
	}
}

// restoreHostBidiLocked gives the terminal its default direction back, and
// implicit mode unless it reported explicit mode before the program started.
func (s *cursedRenderer) restoreHostBidiLocked() {
	if !s.bidiApplied {
		return
	}
	restore := selectCharacterPathDefault
	if !s.bidiKnown || s.bidiHost != ansi.ModeReset && s.bidiHost != ansi.ModePermanentlyReset {
		restore += ansi.SetModeBiDirectionalSupport
	}
	_, _ = s.scr.WriteString(restore)
	s.bidiApplied = false
}
