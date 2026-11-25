package viewmodel

import (
	"slices"

	"crude/workspace"
)

// AgentViewState is one shell's navigation over a shared inventory. Exact node
// identities prevent a replacement with the same ID inheriting old tab choices.
// It owns no lifecycle, execution, conversation or status state.
//
// Root agents are shown unless minimized. Sub-agents start minimized: they
// appear in their parent's transcript and the Agents list, and own a tab only
// after someone opens them.
type AgentViewState struct {
	inventory *workspace.AgentManager
	selected  *workspace.AgentNode
	choices   map[*workspace.AgentNode]agentTabChoice
	// remembered holds this client's saved choices for agents it has not seen
	// in this attachment yet, by durable agent ID. A node adopts its choice the
	// first time the view observes it; until then the choice is kept so it
	// survives being saved again.
	remembered map[string]agentTabChoice
	revision   uint64 // changes whenever a saved choice changes
}

type agentTabChoice uint8

const (
	defaultAgentTab agentTabChoice = iota
	openAgentTab
	minimizeAgentFamily
	minimizeAgentTab
)

func NewAgentViewState(inventory *workspace.AgentManager) *AgentViewState {
	return &AgentViewState{inventory: inventory, choices: make(map[*workspace.AgentNode]agentTabChoice)}
}

// maxRememberedAgents bounds the memory of agents this client no longer sees.
const maxRememberedAgents = 512

// Remember installs saved choices before inventory arrives.
func (v *AgentViewState) Remember(minimized, opened, tabsOnly []string) {
	if v == nil {
		return
	}
	v.remembered = make(map[string]agentTabChoice, len(minimized)+len(opened)+len(tabsOnly))
	for _, id := range opened {
		v.remembered[id] = openAgentTab
	}
	for _, id := range minimized {
		v.remembered[id] = minimizeAgentFamily
	}
	for _, id := range tabsOnly {
		v.remembered[id] = minimizeAgentTab
	}
}

// Choices returns the choices to save: live nodes first, then remembered
// agents this attachment has not seen, in a stable order.
func (v *AgentViewState) Choices() (minimized, opened, tabsOnly []string) {
	if v == nil {
		return nil, nil, nil
	}
	add := func(id string, choice agentTabChoice) {
		switch choice {
		case minimizeAgentFamily:
			minimized = append(minimized, id)
		case minimizeAgentTab:
			tabsOnly = append(tabsOnly, id)
		case openAgentTab:
			opened = append(opened, id)
		}
	}
	live := make(map[string]bool)
	if v.inventory != nil {
		for _, node := range v.inventory.AllNodes() {
			if node == nil {
				continue
			}
			add(node.ID, v.choice(node))
			live[node.ID] = true
		}
	}
	ids := make([]string, 0, len(v.remembered))
	for id := range v.remembered {
		if !live[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	if extra := len(minimized) + len(opened) + len(tabsOnly) + len(ids) - maxRememberedAgents; extra > 0 {
		ids = ids[min(extra, len(ids)):]
	}
	for _, id := range ids {
		add(id, v.remembered[id])
	}
	return minimized, opened, tabsOnly
}

// Chosen reports whether this client already decided the agent's
// visibility, now or in a remembered attachment. Defaults never override it.
func (v *AgentViewState) Chosen(id string) bool {
	if v == nil || v.inventory == nil {
		return false
	}
	node := v.inventory.Get(id)
	if node == nil {
		_, remembered := v.remembered[id]
		return remembered
	}
	return v.choice(node) != defaultAgentTab
}

// Revision changes whenever a choice worth saving changes.
func (v *AgentViewState) Revision() uint64 {
	if v == nil {
		return 0
	}
	return v.revision
}

func (v *AgentViewState) choice(node *workspace.AgentNode) agentTabChoice {
	if chosen, ok := v.remembered[node.ID]; ok {
		delete(v.remembered, node.ID)
		if chosen != openAgentTab || node.Parent != nil {
			v.choices[node] = chosen
		}
	}
	return v.choices[node]
}

// familyHidden reports whether this node's choice also hides its descendants.
func familyHidden(node *workspace.AgentNode, choice agentTabChoice) bool {
	return choice == minimizeAgentFamily || choice == defaultAgentTab && node.Parent != nil
}

func (v *AgentViewState) ActiveNode() *workspace.AgentNode {
	if v == nil || v.inventory == nil || v.selected == nil || v.inventory.Get(v.selected.ID) != v.selected {
		return nil
	}
	return v.selected
}
func (v *AgentViewState) ActiveID() string {
	if node := v.ActiveNode(); node != nil {
		return node.ID
	}
	return ""
}
func (v *AgentViewState) SetActive(id string) {
	if v != nil && v.inventory != nil {
		if node := v.inventory.Get(id); node != nil && !v.IsHidden(id) {
			v.selected = node
		}
	}
}
func (v *AgentViewState) ClearActive() {
	if v != nil {
		v.selected = nil
	}
}
func (v *AgentViewState) IsHidden(id string) bool {
	if v == nil || v.inventory == nil {
		return false
	}
	target := v.inventory.Get(id)
	for node := target; node != nil; node = node.Parent {
		choice := v.choice(node)
		if familyHidden(node, choice) || node == target && choice == minimizeAgentTab {
			return true
		}
	}
	return false
}
func (v *AgentViewState) SetHidden(id string, hidden bool) {
	if v == nil || v.inventory == nil {
		return
	}
	node := v.inventory.Get(id)
	if node == nil {
		return
	}
	choice := defaultAgentTab
	if hidden {
		choice = minimizeAgentFamily
	} else if node.Parent != nil {
		choice = openAgentTab
	}
	v.setChoice(node, choice)
}

func (v *AgentViewState) setChoice(node *workspace.AgentNode, choice agentTabChoice) {
	if v.choice(node) != choice {
		v.revision++
		if choice == defaultAgentTab {
			delete(v.choices, node)
		} else {
			v.choices[node] = choice
		}
	}
	if v.IsHidden(v.ActiveID()) {
		v.ClearActive()
	}
}
func (v *AgentViewState) TabOrder() []string {
	if v == nil || v.inventory == nil {
		return nil
	}
	var ids []string
	for _, node := range v.inventory.AllNodes() {
		if node != nil && !v.IsHidden(node.ID) {
			ids = append(ids, node.ID)
		}
	}
	return ids
}
func (v *AgentViewState) VisibleCount() int {
	if v == nil || v.inventory == nil {
		return 0
	}
	count := 0
	var visit func(*workspace.AgentNode)
	visit = func(node *workspace.AgentNode) {
		if node == nil {
			return
		}
		choice := v.choice(node)
		if familyHidden(node, choice) {
			return
		}
		if choice != minimizeAgentTab {
			count++
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	for _, node := range v.inventory.Orchestrators() {
		visit(node)
	}
	return count
}

// forgetSubtree releases shell choices when inventory is explicitly removed.
func (v *AgentViewState) forgetSubtree(node *workspace.AgentNode) {
	if v == nil || node == nil {
		return
	}
	delete(v.choices, node)
	if v.selected == node {
		v.ClearActive()
	}
	for _, child := range node.Children {
		v.forgetSubtree(child)
	}
}

// MinimizeAgent changes only this shell's tab choice; backend work is untouched.
func (m *App) MinimizeAgent(id string) { m.AgentView.SetHidden(id, true) }

// RestoreAgent reveals the selected path, preserving archived siblings and
// ancestors whose tabs were independently minimized.
func (m *App) RestoreAgent(id string) {
	if m.ws.AgentMgr() == nil {
		return
	}
	node := m.ws.AgentMgr().Get(id)
	if node == nil {
		return
	}
	for current := node; current != nil; current = current.Parent {
		if current == node || familyHidden(current, m.AgentView.choice(current)) {
			m.AgentView.SetHidden(current.ID, false)
		}
	}
	m.AgentView.SetActive(id)
}

// AgentTabViews returns the current agent tabs in forest order. Window IDs,
// layout, hover, and close interaction remain TUI concerns.
func (m *App) AgentTabViews() []workspace.TabView {
	if m == nil || m.ws.AgentMgr() == nil {
		return nil
	}
	nodes := m.ws.AgentMgr().AllNodes()
	views := make([]workspace.TabView, 0, len(nodes))
	for _, node := range nodes {
		if node == nil || m.AgentView.IsHidden(node.ID) {
			continue
		}
		state := ""
		if node.State != nil {
			state = node.State.Status
		}
		kind := "orchestrator"
		if node.Parent != nil {
			kind = "sub-agent"
		}
		label := m.agentLabel(node)
		views = append(views, workspace.TabView{
			ID:           node.ID,
			Title:        "agent:" + node.ID,
			DisplayTitle: label,
			Kind:         kind,
			State:        state,
			Closable:     true,
			Active:       node.ID == m.AgentView.ActiveID(),
		})
	}
	return views
}
