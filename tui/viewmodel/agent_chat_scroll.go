package viewmodel

// agentMainChatScrollSnapshot stores per-agent main-chat scroll UI state.
// Kept in the viewmodel layer; workspace business state does not own this.
type agentMainChatScrollSnapshot struct {
	Offset     int
	AutoScroll bool
}

// beginAgentChatScrollSwitch saves the outgoing agent's scroll snapshot and
// primes the shared shellchat ScrollModel for the incoming agent. Returns true
// when the active agent changed (caller should re-export after metrics restore).
func (m *App) beginAgentChatScrollSwitch(nextAgentID string) bool {
	if m.chatScreen == nil {
		return false
	}
	if nextAgentID == "" {
		m.chatScrollNeedsRestore = false
		if m.chatScrollAgent != "" {
			m.chatScrollAgent = ""
		}
		return false
	}
	if m.chatScrollAgent == nextAgentID {
		return false
	}
	scroll := m.chatScreen.ScrollModel()
	if m.chatScrollAgent != "" {
		if m.agentChatScroll == nil {
			m.agentChatScroll = make(map[string]agentMainChatScrollSnapshot)
		}
		m.agentChatScroll[m.chatScrollAgent] = agentMainChatScrollSnapshot{
			Offset:     scroll.Offset(),
			AutoScroll: scroll.AutoScroll(),
		}
	}
	m.chatScrollAgent = nextAgentID
	m.chatScrollNeedsRestore = true
	if snap, ok := m.agentChatScroll[nextAgentID]; ok {
		scroll.SetAutoScroll(snap.AutoScroll)
	} else {
		scroll.SetAutoScroll(true)
	}
	scroll.ScrollTo(0)
	return true
}

// finishAgentChatScrollMetrics applies viewport/content metrics and restores
// the incoming agent's saved offset after content height is known.
func (m *App) finishAgentChatScrollMetrics(agentID string, viewportH, contentH int) {
	if m.chatScreen == nil {
		return
	}
	if agentID == "" {
		m.chatScrollNeedsRestore = false
		return
	}
	scroll := m.chatScreen.ScrollModel()
	scroll.SetViewport(viewportH)
	scroll.SetContent(contentH)
	if m.chatScrollNeedsRestore {
		m.chatScrollNeedsRestore = false
		if snap, ok := m.agentChatScroll[agentID]; ok {
			scroll.SetAutoScroll(snap.AutoScroll)
			scroll.ScrollTo(snap.Offset)
		} else {
			scroll.SetAutoScroll(true)
			scroll.StickToBottom()
		}
		scroll.Clamp()
		return
	}
	if scroll.AutoScroll() {
		scroll.ApplyAutoScroll()
	} else {
		scroll.Clamp()
	}
}

// clearAgentChatScroll removes saved main-chat scroll state for a closed agent.
func (m *App) clearAgentChatScroll(agentID string) {
	if m.agentChatScroll != nil {
		delete(m.agentChatScroll, agentID)
	}
	if m.chatScrollAgent == agentID {
		m.chatScrollAgent = ""
		m.chatScrollNeedsRestore = false
	}
}

// agentChatScrollSnapshotForTest exposes saved scroll state for contract tests.
func (m *App) agentChatScrollSnapshotForTest(agentID string) (agentMainChatScrollSnapshot, bool) {
	if m.agentChatScroll == nil {
		return agentMainChatScrollSnapshot{}, false
	}
	snap, ok := m.agentChatScroll[agentID]
	return snap, ok
}
