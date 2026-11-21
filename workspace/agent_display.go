package workspace

import (
	"strings"

	"crude/agent/session"
	"crude/types"
)

// AgentDisplayLabel returns a human-readable presentation label for an agent
// node. Internal IDs are never returned; callers must omit the UI parenthetical
// / tab text when this is empty.
//
// Fallback order:
//  1. the reserved Errand role label, or node.Label for other roles
//  2. CategoryDisplayName(node.Category) (role/profile name)
//  3. empty string
func AgentDisplayLabel(node *AgentNode) string {
	if node == nil {
		return ""
	}
	if name := types.AgentNameForRole(node.Category, node.Label); name != "" {
		return name
	}
	return CategoryDisplayName(node.Category)
}

// SessionIsErrand reports whether the session's root agent is the Errand role:
// the workspace's errand runner for tool setup and similar jobs.
func SessionIsErrand(s *Session) bool {
	return s != nil && s.Settings != nil && s.Settings.Effective.SessionMainAgent == session.CategoryErrand
}

// CategoryDisplayName maps an agent category/role key to its UI name.
// Unknown empty categories yield "" so presentation can omit the label.
func CategoryDisplayName(category string) string {
	switch category {
	case session.CategoryDefault:
		return "Main Agent"
	case session.CategoryResearcher:
		return "Researcher"
	case session.CategoryCrafter:
		return "Crafter"
	case session.CategoryErrand:
		return types.ErrandName
	default:
		return ""
	}
}

// SubAgentActivityLabel is the canonical compact identity stored on the
// parent's tool row. Persisted display snapshots retain this exact label, so
// hydrated chat cannot drift from the live projection.
func SubAgentActivityLabel(name, category, task string) string {
	label := strings.TrimSpace(name)
	if category == types.ErrandRoleID {
		label = ""
	}
	if role := CategoryDisplayName(category); role != "" {
		if label != "" {
			label += " · " + role
		} else {
			label = role
		}
	}
	if task = strings.TrimSpace(task); task != "" {
		if label != "" {
			label += " — " + task
		} else {
			label = task
		}
	}
	return label
}

// agentIdentityLabel projects the owner before status is published. Toasts,
// prompt chrome and status panels consume the same label as the agent tabs.
func (w *Workspace) agentIdentityLabel(node *AgentNode) string {
	if node.Parent == nil && SessionIsErrand(w.SessionForAgent(node.ID)) {
		return types.ErrandName
	}
	if label := AgentDisplayLabel(node); label != "" {
		return label
	}
	return "Main Agent"
}
