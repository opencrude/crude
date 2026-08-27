package tea

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

const terminalWidthProbeTimeout = 750 * time.Millisecond

// TerminalWidthProfileMsg reports completion of asynchronous terminal-grid
// calibration. Applications normally only need it as a redraw signal.
type TerminalWidthProfileMsg struct {
	Measured               int
	Corrections            int
	AmbiguousWidth         int
	LegacyGraphemeClusters bool
	Changed                bool
}

type startTerminalWidthProbeMsg struct{}

type terminalWidthProbeTimeoutMsg struct{ generation uint64 }

type terminalWidthProbe struct {
	generation   uint64
	row          int
	clusters     []string
	measurements map[string]int
	next         int
}

func (p *Program) handleTerminalWidthProbeMessage(msg Msg) (Msg, bool) {
	switch typed := msg.(type) {
	case startTerminalWidthProbeMsg:
		p.startTerminalWidthProbe()
		return nil, true
	case CursorPositionMsg:
		if p.widthProbe == nil {
			return msg, false
		}
		consumed, complete := p.widthProbe.accept(typed)
		if !consumed {
			return msg, false
		}
		if !complete {
			return nil, true
		}
		return p.finishTerminalWidthProbe(), false
	case terminalWidthProbeTimeoutMsg:
		if p.widthProbe == nil || typed.generation != p.widthProbe.generation {
			return nil, true
		}
		return p.finishTerminalWidthProbe(), false
	default:
		return msg, false
	}
}

func newTerminalWidthProbe(generation uint64, row int, additional []string) *terminalWidthProbe {
	corpus := append([]string{}, defaultTerminalWidthProbeCorpus...)
	corpus = append(corpus, additional...)
	seen := make(map[string]struct{}, len(corpus))
	clusters := make([]string, 0, len(corpus))
	for _, cluster := range corpus {
		if cluster == "" {
			continue
		}
		if _, ok := seen[cluster]; ok {
			continue
		}
		seen[cluster] = struct{}{}
		clusters = append(clusters, cluster)
	}
	if row < 1 || len(clusters) == 0 {
		return nil
	}
	return &terminalWidthProbe{
		generation:   generation,
		row:          row,
		clusters:     clusters,
		measurements: make(map[string]int, len(clusters)),
	}
}

func (p *terminalWidthProbe) sequence() string {
	var sequence strings.Builder
	// Synchronized output keeps the probe invisible on supporting terminals.
	// Save/restore plus a final erase makes the same sequence harmless elsewhere.
	sequence.WriteString(ansi.SetModeSynchronizedOutput)
	sequence.WriteString(ansi.SaveCursor)
	sequence.WriteString(ansi.SetModeUnicodeCore)
	for _, cluster := range p.clusters {
		sequence.WriteString(ansi.CursorPosition(1, p.row))
		sequence.WriteString(ansi.EraseEntireLine)
		sequence.WriteString(cluster)
		sequence.WriteString(ansi.RequestCursorPositionReport)
	}
	sequence.WriteString(ansi.CursorPosition(1, p.row))
	sequence.WriteString(ansi.EraseEntireLine)
	sequence.WriteString(ansi.RestoreCursor)
	sequence.WriteString(ansi.ResetModeSynchronizedOutput)
	return sequence.String()
}

func (p *terminalWidthProbe) accept(position CursorPositionMsg) (consumed, complete bool) {
	if p == nil || p.next >= len(p.clusters) || position.Y != p.row-1 {
		return false, false
	}
	// Every cluster starts at one-based column 1. CursorPositionMsg is zero-based,
	// so X is exactly the terminal's measured width.
	if position.X >= 0 && position.X <= 16 {
		p.measurements[p.clusters[p.next]] = position.X
	}
	p.next++
	return true, p.next == len(p.clusters)
}

func (p *terminalWidthProbe) profile() (ansi.TerminalWidthProfile, bool) {
	if p == nil || p.measurements["x"] != 1 {
		return ansi.TerminalWidthProfile{}, false
	}
	profile := ansi.TerminalWidthProfile{Exact: make(map[string]int, len(p.measurements))}
	changed := false
	for cluster, measured := range p.measurements {
		if cluster == "x" {
			continue
		}
		guessed := ansi.GraphemeWidth.StringWidth(cluster)
		if guessed != measured {
			profile.Exact[cluster] = measured
			changed = true
		}
	}
	if width := p.measurements["·"]; width == 1 || width == 2 {
		profile.AmbiguousWidth = width
	}
	if width := p.measurements["🧑‍🌾"]; width > 2 {
		profile.LegacyGraphemeClusters = true
		changed = true
	}
	return profile, changed
}

var defaultTerminalWidthProbeCorpus = []string{
	"x",       // CPR calibration
	"·",       // East Asian ambiguous-width policy
	"○",       // East Asian ambiguous-width policy
	"☰",       // Unicode 15 -> 16 width change
	"🧑‍🌾",     // grapheme clustering vs legacy codepoint width
	"🏳️‍🌈",    // ZWJ + VS16
	"🇯🇵",      // regional indicators
	"🏻",       // standalone modifier disagreement
	"a\u0300", // combining mark
	"क्‍ष",    // complex language grapheme
	"\u200b",  // disputed format-character width
}

func (p *Program) startTerminalWidthProbe() {
	if p.widthProbe != nil || p.height < 1 {
		return
	}
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{})
	p.widthProbeGeneration++
	probe := newTerminalWidthProbe(p.widthProbeGeneration, p.height, p.widthProbeClusters)
	if probe == nil {
		return
	}
	p.widthProbe = probe
	p.execute(probe.sequence())
	// The raw probe temporarily erases its scratch row. Mark the retained view
	// for a full repaint so the same renderer tick restores it immediately.
	p.renderer.resize(p.width, p.height)
	go func(generation uint64) {
		select {
		case <-time.After(terminalWidthProbeTimeout):
			p.Send(terminalWidthProbeTimeoutMsg{generation: generation})
		case <-p.ctx.Done():
		}
	}(probe.generation)
}

func (p *Program) finishTerminalWidthProbe() TerminalWidthProfileMsg {
	probe := p.widthProbe
	if probe == nil {
		return TerminalWidthProfileMsg{}
	}
	p.widthProbe = nil
	profile, changed := probe.profile()
	ansi.SetTerminalWidthProfile(profile)
	if changed {
		// Width changes invalidate both the old UV cells and physical terminal
		// contents. A same-size resize erases and forces one clean redraw.
		p.renderer.resize(p.width, p.height)
	}
	return TerminalWidthProfileMsg{
		Measured:               len(probe.measurements),
		Corrections:            len(profile.Exact),
		AmbiguousWidth:         profile.AmbiguousWidth,
		LegacyGraphemeClusters: profile.LegacyGraphemeClusters,
		Changed:                changed,
	}
}
