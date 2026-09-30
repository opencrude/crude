package tools

import (
	"encoding/json"
	"strings"
)

// A launch call may return a running handle or the child's terminal report.
// The tool transport completing does not mean the child completed its task.
type subAgentResult struct {
	name, status string
}

func subAgentOutcome(content string) (subAgentResult, bool) {
	var result struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(content), &result) == nil && result.Status != "" {
		return subAgentResult{name: result.Name, status: result.Status}, true
	}
	if !strings.HasPrefix(content, "## Sub-Agent Result: ") {
		return subAgentResult{}, false
	}
	first, _, _ := strings.Cut(content, "\n")
	name := strings.TrimPrefix(first, "## Sub-Agent Result: ")
	if index := strings.LastIndex(name, " ["); index >= 0 && strings.HasSuffix(name, "]") {
		name = name[:index]
	}
	status := ""
	for _, line := range strings.Split(content, "\n") {
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
	return subAgentResult{name: name, status: status}, true
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
