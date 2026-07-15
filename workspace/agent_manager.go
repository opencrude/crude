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

	InputChan      chan<- provider.UserInput
	BtwChan        chan<- provider.UserInput
	CancelChan     chan<- struct{}
	BlockResolveCh chan string
}

// IsOrchestrator returns true for root nodes (Parent == nil).
func (n *AgentNode) IsOrchestrator() bool { return n.Parent == nil }

// IsActive returns true if the agent is streaming or thinking.
func (n *AgentNode) IsActive() bool {
	return n.State != nil && (n.State.Streaming || n.State.Thinking)
}

// IsCompleted returns true if the agent finished (success, error, or cancel).
func (n *AgentNode) IsCompleted() bool {
	return n.State != nil && n.State.Completed
}

// AgentManager owns the agent tree and the flat ID index. It is the single
// source of truth for "which agents exist" and "what is the active agent".
// All methods assume single-goroutine use (Bubble Tea main goroutine).
type AgentManager struct {
	root   *AgentNode
	byID   map[string]*AgentNode
	active string
}

// NewAgentManager creates an empty manager.
func NewAgentManager() *AgentManager {
	return &AgentManager{
		byID: make(map[string]*AgentNode),
	}
}

// AddOrchestrator creates the root node and registers it.
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
	m.root = node
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
	for _, child := range node.Children {
		m.removeNode(child)
	}
	m.removeNode(node)
	if m.root == node {
		m.root = nil
	}
	if m.active == id {
		m.active = ""
	}
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

// AllNodes returns every node in the tree (root + all descendants).
func (m *AgentManager) AllNodes() []*AgentNode {
	if m.root == nil {
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
	walk(m.root)
	return out
}

// Orchestrator returns the root node (nil if no orchestrator exists).
func (m *AgentManager) Orchestrator() *AgentNode { return m.root }

// SubAgents returns the direct children of the orchestrator (nil if none).
func (m *AgentManager) SubAgents() []*AgentNode {
	if m.root == nil {
		return nil
	}
	return m.root.Children
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

// Count returns the total number of agents (orchestrator + sub-agents).
func (m *AgentManager) Count() int {
	if m.root == nil {
		return 0
	}
	return len(m.AllNodes())
}

// TabOrder returns the agent IDs in the order they should appear as tabs:
// sub-agents first (by pane order), then the orchestrator.
func (m *AgentManager) TabOrder() []string {
	var out []string
	for _, child := range m.SubAgents() {
		if !child.IsCompleted() {
			out = append(out, child.ID)
		}
	}
	if m.root != nil {
		out = append(out, m.root.ID)
	}
	return out
}
