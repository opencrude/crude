package workspace

import "crude/session"

// AgentDisplayLabel returns a human-readable presentation label for an agent
// node. Internal IDs are never returned — callers must omit the UI parenthetical
// / tab text when this is empty.
//
// Fallback order:
//  1. node.Label (explicit title, e.g. sub-agent name)
//  2. CategoryDisplayName(node.Category) (role/profile name)
//  3. empty string
func AgentDisplayLabel(node *AgentNode) string {
	if node == nil {
		return ""
	}
	if node.Label != "" {
		return node.Label
	}
	return CategoryDisplayName(node.Category)
}

// CategoryDisplayName maps an agent category/role key to its UI name.
// Unknown empty categories yield "" so presentation can omit the label.
func CategoryDisplayName(category string) string {
	switch category {
	case session.CategoryDefault, "main":
		return "Main Agent"
	case session.CategoryResearcher:
		return "Researcher"
	case session.CategoryCrafter:
		return "Crafter"
	case session.CategoryAeron:
		return "Aeron"
	default:
		return ""
	}
}
