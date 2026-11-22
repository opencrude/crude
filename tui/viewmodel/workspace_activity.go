package viewmodel

import (
	"context"
	"maps"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/shellport"
	"crude/tui/screens/launcher"
	"crude/tui/ui"
	"crude/workspace"
)

// This retained observation is independent of catalog refresh: it never opens
// a workspace or scans saved storage, and it runs only for visible consumers.
type workspaceActivityReader interface {
	ReadWorkspaceActivity(context.Context) (map[string]workspace.Activity, error)
}

type workspaceActivityState struct {
	counts map[string]workspace.Activity
	cancel context.CancelFunc
	due    time.Time
	seq    uint64

	catalogOwner shellport.Workspace
	rows         workspaceRowsCache
	switcher     *ui.ContextMenuState
	switcherRows []string
}

type workspaceActivityResultMsg struct {
	owner      shellport.Workspace
	generation uint64
	seq        uint64
	counts     map[string]workspace.Activity
	// failed reports a read that did not complete (for example a timeout
	// while the core was busy). It is not an observation of zero activity.
	failed bool
}

func (m *App) cancelWorkspaceActivity() {
	s := &m.workspaceActivity
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
		s.seq++
	}
	s.due = time.Time{}
}

func (m *App) workspaceSwitcherVisible() bool {
	st := m.workspaceActivity.switcher
	return st != nil && st == m.LastContextMenu && m.overlayHost != nil && m.overlayHost.IsOpen(ui.OverlayContextMenu)
}

func (m *App) workspaceActivityVisible() bool {
	return m.activityView == activityWorkspaces && m.sidebarHostVisible() ||
		m.launcherHostVisible() || m.workspaceSwitcherVisible()
}

func (m *App) workspaceActivityCmd(now time.Time) tea.Cmd {
	s := &m.workspaceActivity
	reader, supported := m.ws.(workspaceActivityReader)
	if !supported || len(m.workspaceInfos) == 0 || !m.workspaceActivityVisible() || m.asyncIOStopped.Load() {
		m.cancelWorkspaceActivity()
		if len(s.counts) > 0 {
			m.installWorkspaceActivity(nil)
		}
		return nil
	}
	if s.cancel != nil || now.Before(s.due) {
		return nil
	}
	s.seq++
	s.due = now.Add(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	result := workspaceActivityResultMsg{owner: m.ws, generation: m.workspaceSwitchGen.Load(), seq: s.seq}
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		counts, err := reader.ReadWorkspaceActivity(ctx)
		result.counts, result.failed = counts, err != nil
		return result
	})
}

func (m *App) applyWorkspaceActivityResult(msg workspaceActivityResultMsg) {
	s := &m.workspaceActivity
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() ||
		s.cancel == nil || msg.seq != s.seq || m.asyncIOStopped.Load() {
		return
	}
	s.cancel()
	s.cancel = nil
	if !m.workspaceActivityVisible() {
		msg.counts, msg.failed = nil, false
		s.due = time.Time{}
	}
	if msg.failed {
		// Keep the badges of the last completed read; the next poll retries.
		return
	}
	if !maps.Equal(msg.counts, s.counts) {
		m.installWorkspaceActivity(msg.counts)
	}
}

func (m *App) installWorkspaceActivity(counts map[string]workspace.Activity) {
	s := &m.workspaceActivity
	s.counts = counts
	s.rows = workspaceRowsCache{}
	changed := false
	if m.launcherScreen != nil && m.launcherScreen.SetWorkspaceActivity(counts) && m.launcherHostVisible() {
		m.rootHost.MarkComponentPaint(launcher.WorkspaceListID)
		changed = true
	}
	if m.activityView == activityWorkspaces && m.sidebarHostVisible() {
		m.refreshActivityList()
		m.rootHost.MarkComponentPaint(m.activityList.ID())
		changed = true
	}
	if m.refreshWorkspaceSwitcherBadges() {
		changed = true
	}
	if changed {
		m.presentImmediateFrame()
	}
}

func (m *App) refreshWorkspaceSwitcherBadges() bool {
	if !m.workspaceSwitcherVisible() {
		return false
	}
	fw := m.overlayHost.Window(ui.OverlayContextMenu)
	if fw == nil {
		return false
	}
	frame, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		return false
	}
	list := ui.ContextMenuList(frame)
	if list == nil {
		return false
	}
	s := &m.workspaceActivity
	changed := false
	for i, item := range list.Items() {
		p, ok := item.Payload.(ui.ContextMenuPayload)
		if ok && p.SectionIdx == 0 && p.ItemIdx < len(s.switcherRows) {
			badges := launcher.WorkspaceBadges(s.counts[s.switcherRows[p.ItemIdx]])
			changed = list.SetRowBadges(i, badges) || changed
		}
	}
	if changed {
		fw.MarkContentDirty()
	}
	return changed
}
