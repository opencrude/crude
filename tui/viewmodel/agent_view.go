package viewmodel

import "crude/workspace"

// AgentViewState is one shell's navigation over a shared inventory. Exact node
// identities prevent a replacement with the same ID inheriting old tab choices.
// It owns no lifecycle, execution, conversation or status state.
type AgentViewState struct {
	inventory *workspace.AgentManager
	selected  *workspace.AgentNode
	hidden    map[*workspace.AgentNode]bool
}

func NewAgentViewState(inventory *workspace.AgentManager) *AgentViewState {
	return &AgentViewState{inventory: inventory, hidden: make(map[*workspace.AgentNode]bool)}
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
	for node := v.inventory.Get(id); node != nil; node = node.Parent {
		if v.hidden[node] {
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
	if hidden {
		v.hidden[node] = true
	} else {
		delete(v.hidden, node)
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
		if node == nil || v.hidden[node] {
			return
		}
		count++
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
	delete(v.hidden, node)
	if v.selected == node {
		v.ClearActive()
	}
	for _, child := range node.Children {
		v.forgetSubtree(child)
	}
}

// MinimizeAgent changes only this shell's tab choice; backend work is untouched.
func (m *App) MinimizeAgent(id string) { m.AgentView.SetHidden(id, true) }

// RestoreAgent reveals only the selected path, preserving archived siblings.
func (m *App) RestoreAgent(id string) {
	if m.AgentMgr == nil {
		return
	}
	node := m.AgentMgr.Get(id)
	if node == nil {
		return
	}
	for current := node; current != nil; current = current.Parent {
		m.AgentView.SetHidden(current.ID, false)
	}
	m.AgentView.SetActive(id)
}

// AgentTabViews returns the current agent tabs in forest order. Window IDs,
// layout, hover, and close interaction remain TUI concerns.
func (m *App) AgentTabViews() []workspace.TabView {
	if m == nil || m.AgentMgr == nil {
		return nil
	}
	nodes := m.AgentMgr.AllNodes()
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
		label := workspace.AgentDisplayLabel(node)
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
