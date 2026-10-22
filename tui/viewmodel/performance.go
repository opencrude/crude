package viewmodel

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/metrics"
	"crude/shellport"
	"crude/tui/rendering"
	"crude/tui/screens/perfdash"
	"crude/tui/screens/shellfooter"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
)

// perfState owns the Performance surface's refresh lifecycle. Snapshots are
// taken off the UI goroutine and only while the surface is visible; a hidden
// dashboard costs nothing.
type perfState struct {
	screen      *perfdash.Screen
	generation  uint64
	inFlight    bool
	requestedAt time.Time
	// attention is the footer's recent-stall summary, refreshed once a
	// second from the shell's own event ring.
	attention       perfdash.Severity
	attentionSecond int64
	attentionSeq    uint64
}

type perfSnapshotMsg struct {
	owner        shellport.Workspace
	generation   uint64
	workspaceGen uint64
	data         perfdash.Data
}

const perfRefreshInterval = time.Second

// perfDashboard returns the screen, mounting it in the dock when needed.
func (m *App) perfDashboard() *perfdash.Screen {
	if m.perf.screen == nil {
		m.perf.screen = perfdash.New(m.Theme)
	}
	if m.termDock != nil && m.termDock.Panel(termdock.PanelPerformance) != m.perf.screen {
		m.termDock.SetPanel(termdock.PanelPerformance, m.perf.screen)
	}
	return m.perf.screen
}

// togglePerformancePanel shows or hides the dashboard wherever it is placed.
func (m *App) togglePerformancePanel() tea.Cmd {
	if m.termDock == nil {
		return nil
	}
	m.perfDashboard()
	m.toggleWorkspacePanel(termdock.PanelPerformance)
	return m.requestPerfSnapshot(true)
}

// requestPerfSnapshot starts one refresh when the dashboard is visible and
// the previous one finished. force skips the interval (open, window change).
func (m *App) requestPerfSnapshot(force bool) tea.Cmd {
	screen := m.perf.screen
	if screen == nil || screen.Paused() || !m.panelShown(termdock.PanelPerformance) {
		return nil
	}
	now := time.Now()
	if !force && (m.perf.inFlight || now.Sub(m.perf.requestedAt) < perfRefreshInterval) {
		return nil
	}
	m.perf.generation++
	m.perf.inFlight, m.perf.requestedAt = true, now
	owner, generation, workspaceGen := m.ws, m.perf.generation, m.workspaceSwitchGen.Load()
	query := screen.Query()
	source, _ := owner.(shellport.MetricsSource)
	return func() tea.Msg {
		data := perfdash.Data{Shell: metrics.Take(query)}
		if source != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			core, err := source.CoreMetrics(ctx, query)
			cancel()
			if err != nil {
				data.CoreErr = err.Error()
			} else {
				data.Core = &core
			}
		}
		data.Fetched = time.Now()
		return perfSnapshotMsg{owner: owner, generation: generation, workspaceGen: workspaceGen, data: data}
	}
}

func (m *App) applyPerfSnapshot(msg perfSnapshotMsg) {
	if msg.generation != m.perf.generation {
		return
	}
	m.perf.inFlight = false
	if msg.owner != m.ws || msg.workspaceGen != m.workspaceSwitchGen.Load() || m.perf.screen == nil {
		return
	}
	m.perf.screen.SetData(msg.data)
	m.syncPerfAttentionFrom(m.perf.screen.Findings())
}

// handlePerfAction routes dashboard actions that need the shell.
func (m *App) handlePerfAction(action ui.Action) (tea.Cmd, bool) {
	switch action.ID {
	case perfdash.ActionRefresh:
		return m.requestPerfSnapshot(true), true
	case perfdash.ActionCopy:
		text, _ := action.Payload.(string)
		if m.Feedback != nil {
			m.Feedback.NotifyInfo("Performance report copied")
		}
		return rendering.CopyToClipboardCmd(text), true
	}
	return nil, false
}

// syncPerfAttention refreshes the footer pill's state at most once a second
// from the shell's event ring; the dashboard's full diagnosis replaces it
// while visible.
func (m *App) syncPerfAttention(now time.Time) {
	second := now.Unix()
	if second == m.perf.attentionSecond {
		return
	}
	m.perf.attentionSecond = second
	if m.perf.screen != nil && m.panelShown(termdock.PanelPerformance) {
		return
	}
	seq := metrics.EventSequence()
	if seq == m.perf.attentionSeq && m.perf.attention == perfdash.SeverityOK {
		return
	}
	m.perf.attentionSeq = seq
	m.perf.attention = perfdash.SeverityOK
	for _, event := range metrics.EventsSince(now.Add(-time.Minute)) {
		switch event.Series {
		case "ui.update", "ui.view", "ui.blocking":
			m.perf.attention = perfdash.SeverityCrit
		case "ui.input.wait", "render.flush":
			m.perf.attention = max(m.perf.attention, perfdash.SeverityWarn)
		}
	}
}

func (m *App) syncPerfAttentionFrom(findings []perfdash.Finding) {
	m.perf.attention = perfdash.SeverityOK
	if len(findings) > 0 {
		m.perf.attention = findings[0].Severity
	}
}

// performanceFooterSegment is the app dashboard's permanent footer control.
// Recent stalls change its severity, never whether it is available.
func (m *App) performanceFooterSegment(active string) shellfooter.Segment {
	segment := shellfooter.Segment{Text: "Performance", FillRole: ui.ColorMuted, Pill: true,
		Active: active == termdock.PanelPerformance, Action: "performance", Priority: 0}
	switch m.perf.attention {
	case perfdash.SeverityCrit:
		segment.Text, segment.FillRole, segment.Attention, segment.Priority = "✕ Performance", ui.ColorError, true, 1
	case perfdash.SeverityWarn:
		segment.Text, segment.FillRole, segment.Attention, segment.Priority = "⚠ Performance", ui.ColorWarning, true, 1
	}
	return segment
}
