package viewmodel

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/textoverlay"
)

type terminalImageState struct {
	probeID               uint32
	started               bool
	supported             bool
	profileKnown          bool
	trueColor             bool
	cellWidth, cellHeight int
}

type terminalImageProbeTimeoutMsg struct{ id uint32 }

// Probe the actual output terminal, not TERM/TERM_PROGRAM guesses. A virtual
// one-pixel placement paints nothing without placeholders. All writes still go
// through Bubble Tea's writer; no extra TTY reader races its input decoder.
func (m *App) probeTerminalImages() tea.Cmd {
	state := &m.terminalImages
	var cleanup string
	if state.probeID != 0 {
		cleanup = terminalImageSequence(fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", state.probeID))
	}
	state.started = true
	state.supported = false
	m.refreshReferenceGraphics()
	state.probeID = rand.Uint32()
	if state.probeID == 0 {
		state.probeID = 1
	}
	id := state.probeID
	query := fmt.Sprintf("\x1b_Ga=T,U=1,f=24,s=1,v=1,c=1,r=1,i=%d;AAAA\x1b\\", id)
	return tea.Batch(tea.Raw(cleanup+terminalImageSequence(query)+terminalImageSizeQuery()), tea.Tick(time.Second, func(time.Time) tea.Msg {
		return terminalImageProbeTimeoutMsg{id: id}
	}))
}

func terminalImageSizeQuery() string { return ansi.WindowOp(16) + ansi.WindowOp(14) }

func terminalImageSequence(sequence string) string {
	if os.Getenv("TMUX") != "" {
		return ansi.TmuxPassthrough(sequence)
	}
	return sequence
}

func (m *App) handleTerminalImageMessage(msg tea.Msg) (tea.Cmd, bool) {
	state := &m.terminalImages
	refresh := false
	switch msg := msg.(type) {
	case uv.KittyGraphicsEvent:
		if state.probeID == 0 || uint32(msg.Options.ID) != state.probeID {
			return nil, true // never expose a terminal response as composer input
		}
		id := state.probeID
		state.probeID = 0
		state.supported = strings.TrimSpace(string(msg.Payload)) == "OK"
		m.refreshReferenceGraphics()
		return tea.Raw(terminalImageSequence(fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id))), true
	case terminalImageProbeTimeoutMsg:
		if state.probeID != msg.id {
			return nil, true
		}
		state.probeID = 0
		state.supported = false
		m.refreshReferenceGraphics()
		// A nonresponsive or passthrough-blocked terminal keeps its cell preview.
		return tea.Raw(terminalImageSequence(fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", msg.id))), true
	case uv.CellSizeEvent:
		if msg.Width > 0 && msg.Height > 0 && msg.Width <= 1024 && msg.Height <= 1024 {
			state.cellWidth, state.cellHeight = msg.Width, msg.Height
			refresh = true
		}
	case uv.PixelSizeEvent:
		if m.width > 0 && m.height > 0 && msg.Width >= m.width && msg.Height >= m.height {
			state.cellWidth, state.cellHeight = msg.Width/m.width, msg.Height/m.height
			refresh = true
		}
	case tea.ColorProfileMsg:
		// Foreground RGB encodes image identity, not a visual theme color.
		// Downsampling (including NO_COLOR) must not turn it into another ID.
		state.profileKnown, state.trueColor = true, msg.Profile == colorprofile.TrueColor
		refresh = true
	case tea.ResumeMsg:
		return m.probeTerminalImages(), true
	default:
		return nil, false
	}
	if refresh {
		m.refreshReferenceGraphics()
	}
	return nil, true
}

func (m *App) configureReferenceGraphics(screen *textoverlay.Screen) {
	state := m.terminalImages
	if screen.SetImageGraphics(state.supported && (!state.profileKnown || state.trueColor) && state.cellWidth > 0 && state.cellHeight > 0, state.cellWidth, state.cellHeight) {
		m.markReferenceOverlay()
	}
}

func (m *App) refreshReferenceGraphics() {
	if m.referenceOverlay != nil {
		m.configureReferenceGraphics(m.referenceOverlay.screen)
	}
}
