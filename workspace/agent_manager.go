package workspace

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
// It owns inventory only; client selection and visibility belong to each shell.
// All methods assume single-goroutine use (Bubble Tea main goroutine).
type AgentManager struct {
	roots []*AgentNode
	byID  map[string]*AgentNode
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
	state *AgentState,
) *AgentNode {
	node := &AgentNode{
		ID:    id,
		State: state,
	}
	if existing := m.byID[id]; existing != nil {
		m.Remove(id)
	}
	m.roots = append(m.roots, node)
	m.byID[id] = node
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
// regardless of client visibility.
func (m *AgentManager) Count() int {
	return len(m.byID)
}
