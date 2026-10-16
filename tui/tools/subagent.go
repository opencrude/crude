package tools

import (
	"encoding/json"
	"strings"

	"crude/types"
)

// An agent_start result identifies the newly running workspace agent. Terminal
// outcomes and report revisions arrive as separate collaboration events.
type subAgentResult struct {
	name, id, category, status string
}

func subAgentOutcome(content string) (subAgentResult, bool) {
	var result struct {
		Name     string `json:"name"`
		AgentID  string `json:"agent_id"`
		Status   string `json:"status"`
		Category string `json:"category"`
	}
	if json.Unmarshal([]byte(content), &result) == nil && result.Status != "" {
		return subAgentResult{name: result.Name, id: result.AgentID, category: result.Category, status: result.Status}, true
	}
	return subAgentResult{}, false
}

func subAgentIdentityLabel(name, id, category string) string {
	if structuredToolLabel(name) || embeddedStructuredLabel(name) {
		name = ""
	}
	if structuredToolLabel(id) || embeddedStructuredLabel(id) {
		id = ""
	}
	if structuredToolLabel(category) || embeddedStructuredLabel(category) {
		category = ""
	}
	if category = categoryDisplayLabel(category); category != "" {
		if name != "" {
			name += " · " + category
		} else {
			name = category
		}
	}
	if name == "" {
		return id
	}
	if id == "" {
		return name
	}
	return name + " (" + id + ")"
}

func categoryDisplayLabel(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return ""
	}
	words := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(category))
	for i, word := range words {
		if word != "" {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

// StartedAgentID is the agent a successful agent_start launched, or "".
func StartedAgentID(msg types.ChatMessage) string {
	if msg.ToolName != "agent_start" || msg.ToolStatus != types.ToolStatusSuccess {
		return ""
	}
	if outcome, ok := subAgentOutcome(msg.Content); ok && !structuredToolLabel(outcome.id) {
		return outcome.id
	}
	return ""
}
