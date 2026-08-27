package workspace

import (
	"crude/provider"
)

// AgentNode is one node in the agent tree. An orchestrator is a root node
// with children (sub-agents). A sub-agent has a non-nil Parent pointing to
// its orchestrator. All per-agent state lives here — no parallel maps.
type AgentNode struct {
	ID       string
	Parent   *AgentNode
	Children []*AgentNode
	State    *AgentState
	Label    string
	Model    string
	Category string
	// Hidden marks an agent that the user dismissed from the tab bar. The
	// orchestrator keeps running and every buffer stays intact; only the
	// surfaced tab is gone. Restore it from the Agents panel or the
	// sessions picker.
	Hidden bool

	InputChan      chan<- provider.UserInput
	BtwChan        chan<- provider.UserInput
	CancelChan     chan<- struct{}
	BlockResolveCh chan string
}

// IsOrchestrator returns true for root nodes (Parent == nil).
func (n *AgentNode) IsOrchestrator() bool { return n.Parent == nil }

// IsActive reports whether the agent owns an unfinished turn, including
// provider waits, permission prompts, and tool execution gaps.
func (n *AgentNode) IsActive() bool {
	return n.State != nil && n.State.IsActive()
}

// IsCompleted returns true if the agent finished (success, error, or cancel).
func (n *AgentNode) IsCompleted() bool {
	return n.State != nil && n.State.Completed
}

// AgentManager owns the agent forest and the flat ID index. Each orchestrator
// is a root so multiple workspace sessions remain independently reachable.
// It is the single source of truth for which agents exist and which is active.
// All methods assume single-goroutine use (Bubble Tea main goroutine).
type AgentManager struct {
	roots  []*AgentNode
	byID   map[string]*AgentNode
	active string
}

// NewAgentManager creates an empty manager.
func NewAgentManager() *AgentManager {
	return &AgentManager{
		byID: make(map[string]*AgentNode),
	}
}

// AddOrchestrator creates a root node and registers it.
func (m *AgentManager) AddOrchestrator(
	id string,
	inputCh, btwCh chan<- provider.UserInput,
	cancelCh chan<- struct{},
	blockResolveCh chan string,
	state *AgentState,
) *AgentNode {
	node := &AgentNode{
		ID:             id,
		State:          state,
		InputChan:      inputCh,
		BtwChan:        btwCh,
		CancelChan:     cancelCh,
		BlockResolveCh: blockResolveCh,
	}
	if existing := m.byID[id]; existing != nil {
		m.Remove(id)
	}
	m.roots = append(m.roots, node)
	m.byID[id] = node
	m.active = id
	return node
}

// AddSubAgent creates a child node under the given parent.
func (m *AgentManager) AddSubAgent(id, parentID, label, model, category string, state *AgentState) *AgentNode {
	parent := m.byID[parentID]
	if parent == nil {
		return nil
	}
	node := &AgentNode{
		ID:       id,
		Parent:   parent,
		State:    state,
		Label:    label,
		Model:    model,
		Category: category,
	}
	parent.Children = append(parent.Children, node)
	m.byID[id] = node
	return node
}

// Get returns the node for the given ID, or nil if not found.
func (m *AgentManager) Get(id string) *AgentNode {
	return m.byID[id]
}

// Remove deletes a node and all its descendants from the tree and index.
func (m *AgentManager) Remove(id string) {
	node := m.byID[id]
	if node == nil {
		return
	}
	m.removeTree(node)
	if node.Parent == nil {
		for i, root := range m.roots {
			if root == node {
				m.roots = append(m.roots[:i], m.roots[i+1:]...)
				break
			}
		}
	}
	if m.active == id || m.byID[m.active] == nil {
		m.active = ""
	}
}

func (m *AgentManager) removeTree(node *AgentNode) {
	children := append([]*AgentNode(nil), node.Children...)
	for _, child := range children {
		m.removeTree(child)
	}
	m.removeNode(node)
}

func (m *AgentManager) removeNode(node *AgentNode) {
	delete(m.byID, node.ID)
	if node.Parent != nil {
		for i, c := range node.Parent.Children {
			if c == node {
				node.Parent.Children = append(node.Parent.Children[:i], node.Parent.Children[i+1:]...)
				break
			}
		}
	}
}

// ActiveID returns the currently-viewed agent ID ("" if none).
func (m *AgentManager) ActiveID() string { return m.active }

// ActiveNode returns the currently-viewed agent node (nil if none).
func (m *AgentManager) ActiveNode() *AgentNode {
	if m.active == "" {
		return nil
	}
	return m.byID[m.active]
}

// SetActive sets the active agent by ID. No-op if the ID is unknown.
func (m *AgentManager) SetActive(id string) {
	if _, ok := m.byID[id]; ok {
		m.active = id
	}
}

// ClearActive unsets the active agent.
func (m *AgentManager) ClearActive() { m.active = "" }

// AllNodes returns every node in the forest (roots + all descendants).
func (m *AgentManager) AllNodes() []*AgentNode {
	if len(m.roots) == 0 {
		return nil
	}
	var out []*AgentNode
	var walk func(n *AgentNode)
	walk = func(n *AgentNode) {
		out = append(out, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, root := range m.roots {
		walk(root)
	}
	return out
}

// Orchestrator returns the active agent's root, or the latest root when no
// agent is active.
func (m *AgentManager) Orchestrator() *AgentNode {
	if node := m.ActiveNode(); node != nil {
		for node.Parent != nil {
			node = node.Parent
		}
		return node
	}
	if len(m.roots) == 0 {
		return nil
	}
	return m.roots[len(m.roots)-1]
}

// OrchestratorFor returns the root orchestrator that owns id.
func (m *AgentManager) OrchestratorFor(id string) *AgentNode {
	node := m.byID[id]
	for node != nil && node.Parent != nil {
		node = node.Parent
	}
	return node
}

// Orchestrators returns all root orchestrators in creation order.
func (m *AgentManager) Orchestrators() []*AgentNode {
	return append([]*AgentNode(nil), m.roots...)
}

// SubAgents returns the direct children of every orchestrator.
func (m *AgentManager) SubAgents() []*AgentNode {
	var out []*AgentNode
	for _, root := range m.roots {
		out = append(out, root.Children...)
	}
	return out
}

// AnyActive returns true if any agent in the tree is streaming or thinking.
func (m *AgentManager) AnyActive() bool {
	for _, n := range m.AllNodes() {
		if n.IsActive() {
			return true
		}
	}
	return false
}

// Count returns the total number of agents (orchestrator + sub-agents),
// including minimized ones.
func (m *AgentManager) Count() int {
	return len(m.byID)
}

// VisibleCount returns the number of agents the user can currently see as
// tabs. Shell gates that decide between the launcher, the chat, and the
// editor use this: a workspace whose agents are all minimized must fall back
// to the launcher even though the orchestrators are still alive.
func (m *AgentManager) VisibleCount() int {
	n := 0
	for _, node := range m.byID {
		if node != nil && !node.Hidden {
			n++
		}
	}
	return n
}

// SetHidden minimizes or restores an agent subtree. Hiding a root hides its
// sub-agents with it so a restored orchestrator comes back whole.
func (m *AgentManager) SetHidden(id string, hidden bool) {
	node := m.byID[id]
	if node == nil {
		return
	}
	var walk func(*AgentNode)
	walk = func(n *AgentNode) {
		n.Hidden = hidden
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(node)
	if hidden && m.active == id {
		m.active = ""
	}
}

// IsHidden reports whether an agent is minimized. Unknown IDs are not hidden.
func (m *AgentManager) IsHidden(id string) bool {
	node := m.byID[id]
	return node != nil && node.Hidden
}

// HiddenOrchestrators returns the minimized root agents in creation order.
func (m *AgentManager) HiddenOrchestrators() []*AgentNode {
	var out []*AgentNode
	for _, root := range m.roots {
		if root != nil && root.Hidden {
			out = append(out, root)
		}
	}
	return out
}

// TabOrder returns the agent IDs in the order they should appear as tabs:
// sub-agents first (by pane order), then the orchestrator. Minimized agents
// have no tab.
func (m *AgentManager) TabOrder() []string {
	var out []string
	for _, child := range m.SubAgents() {
		if !child.IsCompleted() && !child.Hidden {
			out = append(out, child.ID)
		}
	}
	for _, root := range m.roots {
		if root.Hidden {
			continue
		}
		out = append(out, root.ID)
	}
	return out
}
