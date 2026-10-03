package tools

import (
	"encoding/json"
	"strings"
)

// A launch call may return a running handle or the child's terminal report.
// The tool transport completing does not mean the child completed its task.
type subAgentResult struct {
	name, id, category, status string
}

func subAgentOutcome(content string) (subAgentResult, bool) {
	var result struct {
		Name     string `json:"name"`
		HandleID string `json:"handle_id"`
		AgentID  string `json:"agent_id"`
		Status   string `json:"status"`
		Category string `json:"category"`
	}
	if json.Unmarshal([]byte(content), &result) == nil && result.Status != "" {
		id := result.HandleID
		if id == "" {
			id = result.AgentID
		}
		return subAgentResult{name: result.Name, id: id, category: result.Category, status: result.Status}, true
	}
	if !strings.HasPrefix(content, "## Sub-Agent Result: ") {
		if rest, ok := strings.CutPrefix(content, `Sub-agent "`); ok {
			if name, _, found := strings.Cut(rest, `"`); found {
				return subAgentResult{name: name}, false // a status check, not a completed child result
			}
		}
		return subAgentResult{}, false
	}
	first, _, _ := strings.Cut(content, "\n")
	name := strings.TrimPrefix(first, "## Sub-Agent Result: ")
	category := ""
	if index := strings.LastIndex(name, " ["); index >= 0 && strings.HasSuffix(name, "]") {
		category = name[index+2 : len(name)-1]
		name = name[:index]
	}
	status := ""
	id := ""
	for _, line := range strings.Split(content, "\n") {
		if value, ok := strings.CutPrefix(line, "**Agent:** "); ok {
			id = strings.TrimSpace(value)
		}
		if !strings.HasPrefix(line, "**Status:** ") {
			continue
		}
		value := strings.TrimPrefix(line, "**Status:** ")
		switch {
		case strings.HasPrefix(value, "OK"):
			status = "completed"
		case strings.HasPrefix(value, "CANCELLED"):
			status = "cancelled"
		case strings.HasPrefix(value, "TIMEOUT"):
			status = "failed_timeout"
		case strings.HasPrefix(value, "CRASH"):
			status = "crashed"
		case strings.HasPrefix(value, "QUOTA"):
			status = "failed_quota"
		case strings.HasPrefix(value, "ERROR"):
			status = "failed_error"
		case strings.HasPrefix(value, "POLICY"):
			status = "failed_policy"
		default:
			status = "failed_error"
		}
		break
	}
	if status == "" {
		return subAgentResult{}, false
	}
	return subAgentResult{name: name, id: id, category: category, status: status}, true
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

// SubAgentResultFailed recognizes a returned child failure even when an older
// transcript recorded the successful delivery of the tool result as success.
func SubAgentResultFailed(content string) bool {
	outcome, ok := subAgentOutcome(content)
	return ok && outcome.status != "running" && outcome.status != "completed" && outcome.status != "cancelled"
}

// SubAgentResultSettled distinguishes a returned child result from a launch
// failure, whose diagnostic text still belongs in the visible error body.
func SubAgentResultSettled(content string) bool {
	outcome, ok := subAgentOutcome(content)
	return ok && outcome.status != "running"
}

func SubAgentResultCancelled(content string) bool {
	outcome, ok := subAgentOutcome(content)
	return ok && outcome.status == "cancelled"
}
