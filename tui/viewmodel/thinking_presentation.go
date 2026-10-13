package viewmodel

import (
	"crude/tui/chatprojection"
	"crude/types"
	"crude/workspace"
)

// Exact owners fence shell-only geometry decisions across session/node replacement.
// The pane borrows canonical Messages; it never owns a second semantic store.
type thinkingPresentationKey struct {
	session *workspace.Session
	agent   *workspace.AgentState
	thought string
}

func (m *App) thinkingKey(agent *workspace.AgentState) thinkingPresentationKey {
	return thinkingPresentationKey{session: m.ws.SessionForAgent(agent.ID), agent: agent}
}
func (m *App) thinkingPaneID(agent *workspace.AgentState) string {
	if agent == nil {
		return ""
	}
	key := m.thinkingKey(agent)
	for owner, pane := range m.thinkingPresentations {
		if owner.session == key.session && owner.agent == agent {
			return pane.ID
		}
	}
	return ""
}
func (m *App) Get(id string) *workspace.SubPane {
	for _, pane := range m.thinkingPresentations {
		if pane.ID == id {
			return pane
		}
	}
	return m.ws.PaneMgr().Get(id)
}
func (m *App) thinkingOwnerWidth(id string) int {
	node := m.ws.AgentMgr().Get(id)
	if node == nil || node.State == nil {
		return 0
	}
	reg := m.subPaneProjections
	if reg.thinkingWidths == nil {
		reg.thinkingWidths = make(map[*workspace.AgentState]int)
	}
	// Prefer the exact mounted owner, then its retained geometry, then the
	// ancestor that embeds a background child. Never use selected-view geometry.
	for current := node; current != nil; current = current.Parent {
		if m.mainPanes != nil {
			for _, group := range m.mainPanes.groups {
				if group.view != nil && group.view == m.mainPanes.agentViews[current.ID] && group.chat.W > 0 {
					_, width := boundedContentGeometry(group.chat.W, m.Theme.Spacing.PadLR)
					reg.thinkingWidths[node.State] = width
					return width
				}
			}
			if view := m.mainPanes.agentViews[current.ID]; view != nil && view.reflowInnerW > 0 {
				reg.thinkingWidths[node.State] = view.reflowInnerW
				return view.reflowInnerW
			}
		}
		if width := reg.thinkingWidths[current.State]; width > 0 {
			return width
		}
	}
	// Before any owner has geometry, use the unsplit shell column only. A split
	// background owner with no embedding geometry waits until its first layout.
	if m.mainPanes != nil && len(m.mainPanes.groups) > 1 {
		return 0
	}
	sidebar := m.frameLeftCol() + m.activityChromeWidth()
	if m.Tree != nil && m.Tree.Visible && m.Tree.Model != nil && m.sidebarScreen != nil {
		sidebar += m.sidebarPanelWidth() + 1
	}
	_, width := boundedContentGeometry(max(0, m.width-m.frameRightCol()-sidebar), m.Theme.Spacing.PadLR)
	return width
}
func (m *App) dropThinkingPresentation(agent *workspace.AgentState) {
	if agent == nil {
		return
	}
	for key, pane := range m.thinkingPresentations {
		if key.agent == agent {
			delete(m.thinkingPresentations, key)
			m.subPaneProjections.Remove(pane.ID)
			m.restoreThinkingRows(m.subPaneProjections, pane.ID)
		}
	}
}

// clearThinkingPresentationOwner releases shell state for an exact removed
// agent. Workspace switches can instead discard the entire local registry.
func (m *App) clearThinkingPresentationOwner(agent *workspace.AgentState) {
	m.dropThinkingPresentation(agent)
	delete(m.subPaneProjections.thinkingWidths, agent)
	delete(m.subPaneProjections.thinkingRoots, agent)
}

func (m *App) restoreThinkingRows(reg *subPaneProjectionRegistry, paneID string) {
	for _, projection := range reg.projections {
		projection.renderer.RemoveThinkingPane(paneID)
	}
	for _, child := range reg.owners {
		m.restoreThinkingRows(child, paneID)
	}
}
func (m *App) installThinkingPresentation(agent *workspace.AgentState) {
	if agent == nil || agent.Messages == nil || m.thinkingPaneID(agent) != "" {
		return
	}
	thought, ok := agent.Messages.ActiveThinking()
	if !ok {
		return
	}
	key := m.thinkingKey(agent)
	key.thought = thought.InternalID
	if m.thinkingPresentations == nil {
		m.thinkingPresentations = make(map[thinkingPresentationKey]*workspace.SubPane)
	}
	pane := &workspace.SubPane{ID: m.nextThinkingPaneID(agent.ID), Type: workspace.PaneThinking, Title: "Thinking", Messages: agent.Messages, StartTime: thought.Time}
	m.thinkingPresentations[key] = pane
	m.subPaneProjections.Get(pane.ID, workspace.PaneThinking)
}
func (m *App) reconcileThinkingPresentation(agent *workspace.AgentState, width int) {
	if agent == nil || agent.Messages == nil {
		return
	}
	thought, active := agent.Messages.ActiveThinking()
	for key := range m.thinkingPresentations {
		if key.agent == agent && (!active || key.thought != thought.InternalID || key.session != m.ws.SessionForAgent(agent.ID)) {
			m.dropThinkingPresentation(agent)
			break
		}
	}
	if !active || m.thinkingPaneID(agent) != "" || width <= 0 {
		return
	}
	session := m.ws.SessionForAgent(agent.ID)
	root := agent
	if node := m.ws.AgentMgr().OrchestratorFor(agent.ID); node != nil {
		root = node.State
	}
	latched := m.thinkingLatch[session]
	if session == nil {
		latched = m.subPaneProjections.thinkingRoots[root]
	}
	if !latched && !chatprojection.ThinkingOverflowsInline(thought.Content, thought.ThinkingDisplay.Start, thought.Time, chatprojection.ThinkingOverflowWidth(width)) {
		return
	}
	m.installThinkingPresentation(agent)
	if session != nil {
		if m.thinkingLatch == nil {
			m.thinkingLatch = make(map[*workspace.Session]bool)
		}
		m.thinkingLatch[session] = true
	} else {
		if m.subPaneProjections.thinkingRoots == nil {
			m.subPaneProjections.thinkingRoots = make(map[*workspace.AgentState]bool)
		}
		m.subPaneProjections.thinkingRoots[root] = true
	}
}
func (m *App) presentThinking(renderer *chatprojection.Renderer, conversation *workspace.Conversation) {
	var panes map[string]string
	for key, pane := range m.thinkingPresentations {
		if key.agent.Messages == conversation {
			if panes == nil {
				panes = make(map[string]string)
			}
			panes[key.thought] = pane.ID
		}
	}
	renderer.SetThinkingPanes(panes)
}
func (m *App) syncLocalThinking(reg *subPaneProjectionRegistry, pane *workspace.SubPane) bool {
	for key, local := range m.thinkingPresentations {
		if local != pane {
			continue
		}
		thought, active := key.agent.Messages.ActiveThinking()
		if active && thought.InternalID == key.thought {
			p := reg.Get(pane.ID, workspace.PaneThinking)
			p.localThinking = true
			p.renderer.SyncMessages([]types.ChatMessage{chatMessageFromCanonical(thought)}, key.agent.Messages.Version())
		}
		return true
	}
	return false
}
