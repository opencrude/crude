package viewmodel

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/shelltree"
)

const interactiveFrameInterval = 16 * time.Millisecond

type interactiveFrameMsg struct{ generation uint64 }

type frameGate struct {
	lastPresented    time.Time
	deferred         bool
	due              bool
	wakePending      bool
	generation       uint64
	presented        uint64
	cadence          interface{ DisplayInterval() time.Duration }
	cadenceWorkspace shellport.Workspace
	cadenceInterval  time.Duration
}

// SetDisplayCadence connects the renderer's measured write cost to nonblocking
// replica pacing. The shell remains the only presentation-clock authority.
func (m *App) SetDisplayCadence(source interface{ DisplayInterval() time.Duration }) {
	m.frameGate.cadence = source
	m.frameGate.cadenceWorkspace = nil
}

func (m *App) syncDisplayCadence() {
	g := &m.frameGate
	interval := interactiveFrameInterval
	if g.cadence != nil {
		interval = max(interval, min(g.cadence.DisplayInterval(), 250*time.Millisecond))
	}
	if g.cadenceWorkspace == m.ws && g.cadenceInterval == interval {
		return
	}
	g.cadenceWorkspace, g.cadenceInterval = m.ws, interval
	if pacer, ok := m.ws.(shellport.DisplayPacer); ok {
		pacer.SetDisplayInterval(interval)
	}
}

func (m *App) mainViewResizeActive() bool {
	if m.sidebarResizeActive() {
		return true
	}
	if m.rootHost == nil {
		return false
	}
	id, _, ok := m.rootHost.CapturedPointer()
	return ok && strings.HasPrefix(string(id), "main-panes/divider-")
}

func (m *App) requestCoalescedFrame(now time.Time) tea.Cmd {
	g := &m.frameGate
	if g.lastPresented.IsZero() || now.Sub(g.lastPresented) >= interactiveFrameInterval {
		g.due = true
		g.deferred = false
		return nil
	}
	g.deferred = true
	if g.wakePending {
		return nil
	}
	g.wakePending = true
	g.generation++
	generation := g.generation
	delay := interactiveFrameInterval - now.Sub(g.lastPresented)
	if delay < time.Millisecond {
		delay = time.Millisecond
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return interactiveFrameMsg{generation: generation} })
}

func (m *App) handleInteractiveFrame(msg interactiveFrameMsg) {
	g := &m.frameGate
	if msg.generation != g.generation {
		return
	}
	g.wakePending = false
	g.deferred = false
	g.due = true
}

func (m *App) forceFrame() {
	if m.rootHost != nil {
		m.rootHost.MarkPaint()
	}
	m.presentImmediateFrame()
}

// Frame urgency is not source damage. Update's message/interaction owners
// invalidate their components separately; a key must not repaint every sibling
// merely because its next presentation is immediate.
func (m *App) presentImmediateFrame() {
	g := &m.frameGate
	g.generation++
	g.wakePending = false
	g.deferred = false
	g.due = true
}

func (m *App) shouldDeferFrame() bool {
	return m.framePresented && m.frameGate.deferred && !m.frameGate.due
}

func (m *App) noteFramePresented(now time.Time) {
	m.syncDisplayCadence()
	g := &m.frameGate
	g.lastPresented = now
	g.due = false
	g.deferred = false
	g.presented++
	m.framePresented = true
}

func (m *App) sidebarResizeActive() bool {
	if m.rootHost == nil {
		return false
	}
	owner, _, ok := m.rootHost.CapturedPointer()
	if !ok {
		return false
	}
	return owner == activitylist.DividerID || owner == shelltree.DividerID
}
