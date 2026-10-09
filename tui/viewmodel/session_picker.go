package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	sessionpicker "crude/tui/screens/sessionpicker"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

// openSessionPicker opens the native past-sessions overlay.
// Always allowed: the multi-agent tab model resumes into an additional tab
// (OpenAgentSession) without discarding the current agent/session.
func (m *App) openSessionPicker() {
	if m.overlayHost == nil {
		return
	}
	m.overlayHost.Close(ui.OverlaySessionPicker)

	sessions := m.ws.ListSessions()
	if m.sessionPicker == nil {
		m.sessionPicker = sessionpicker.New(m.Theme)
	}
	m.sessionPicker.Open(sessions)
	m.sessionPicker.SetLiveInfo(m.sessionLiveInfos(sessions))

	w, h := fitFloatingSize(m.width, m.height, 78, 22, 48, 12)
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "session-picker-frame"),
		ui.FrameDialog,
		"Sessions",
		m.sessionPicker,
		nil,
		true,
		w,
		h,
	)
	m.sessionPicker.SetFocusHandler(frame.Focus)
	// Open into the search field: typing filters immediately. FocusSearch
	// falls back to the screen itself when there is nothing to search.
	m.sessionPicker.FocusSearch()

	fw := wm.NewFloatingWindow(string(ui.OverlaySessionPicker), "Sessions", frame, w, h,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithClosable(true),
		wm.WithResizable(true),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithMinSize(minInt(48, w), minInt(12, h)),
		wm.WithMaxSize(maxInt(w, m.width-2), maxInt(h, m.height-2)),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	picker := m.sessionPicker
	m.overlayHost.Open(ui.OverlaySessionPicker, fw, ui.ModalDialogPolicy(), func() {
		if picker != nil {
			picker.Close()
		}
		m.sessionPicker = nil
	})
}

func (m *App) closeSessionPicker() {
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlaySessionPicker)
	}
}

// sessionLiveInfos maps each listed session to its lifecycle status and
// sub-agent count. Status comes from the live agent tree (working/minimized/
// idle wins per merge precedence); sub-agents come from the persisted parent
// linkage, grown by the live tree when it has more.
func (m *App) sessionLiveInfos(sessions []sessiondb.SessionMeta) map[string]sessionpicker.LiveInfo {
	live := make(map[string]sessionpicker.LiveInfo, len(sessions))
	for _, meta := range sessions {
		if meta.ParentSessionID.Valid && meta.ParentSessionID.String != "" {
			id := meta.ParentSessionID.String
			info := live[id]
			info.SubAgents++
			live[id] = info
		}
	}
	if m.ws.AgentMgr() != nil {
		for agentID, sess := range m.ws.SessionsByAgent() {
			node := m.ws.AgentMgr().Get(agentID)
			if node == nil || sess == nil || sess.SessionID == "" {
				continue
			}
			status := sessionpicker.StatusIdle
			switch {
			case node.IsActive():
				status = sessionpicker.StatusWorking
			case m.AgentView.IsHidden(node.ID):
				status = sessionpicker.StatusMinimized
			}
			live[sess.SessionID] = live[sess.SessionID].Merge(sessionpicker.LiveInfo{
				Status:    status,
				SubAgents: countAgentDescendants(node),
			})
		}
	}
	return live
}

func countAgentDescendants(n *workspace.AgentNode) int {
	if n == nil {
		return 0
	}
	total := 0
	for _, child := range n.Children {
		total += 1 + countAgentDescendants(child)
	}
	return total
}

func (m *App) handleSessionPickerSelect(meta sessiondb.SessionMeta) tea.Cmd {
	m.closeSessionPicker()
	if meta.ID == "" {
		return nil
	}
	return m.launcherResumeSession(meta.ID, &meta)
}
