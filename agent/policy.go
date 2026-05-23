package agent

import (
	"crude/safety"
)

func ComposePolicy(parentPolicy safety.Policy, specPolicyName string, category string) safety.Policy {
	child := categoryPolicy(category)

	if specPolicyName != "" {
		switch specPolicyName {
		case "read-only":
			child = safety.ReadOnlyPolicy()
		case "no-delete":
			child = safety.DefaultPolicy()
			child.AllowedCapabilities[safety.CanDelete] = false
		case "no-execute":
			child = safety.DefaultPolicy()
			child.AllowedCapabilities[safety.CanExecute] = false
		case "default":
			child = safety.DefaultPolicy()
		}
	}

	for cap := range child.AllowedCapabilities {
		if !parentPolicy.AllowedCapabilities[cap] {
			child.AllowedCapabilities[cap] = false
		}
	}

	catPolicy := categoryPolicy(category)
	for cap := range child.AllowedCapabilities {
		if !catPolicy.AllowedCapabilities[cap] {
			child.AllowedCapabilities[cap] = false
		}
	}

	for cap, needed := range parentPolicy.RequireConfirmation {
		if needed {
			child.RequireConfirmation[cap] = true
		}
	}

	return child
}

func categoryPolicy(category string) safety.Policy {
	switch category {
	case CatResearcher:
		return safety.ReadOnlyPolicy()
	case CatCrafter:
		p := safety.DefaultPolicy()
		p.AllowedCapabilities[safety.CanDelete] = false
		return p
	case CatAeron:
		p := safety.DefaultPolicy()
		p.AllowedCapabilities[safety.CanDelete] = false
		p.AllowedCapabilities[safety.CanExecute] = false
		return p
	case CatOrchestrator:
		return safety.OrchestratorPolicy()
	default:
		return safety.DefaultPolicy()
	}
}

func PolicyAllows(policy safety.Policy, toolName string) bool {
	switch toolName {
	case "read_file", "glob_files", "grep_files", "list_directory",
		"web_search", "web_fetch", "arxiv_search", "run_javascript",
		"reply", "question", "write_report",
		"plan", "mark_step_complete", "mark_step_doing", "mark_step_failed",
		"report_blocker", "resolve_blocker", "list_goal_status",
		"list_sub_agents", "poll_sub_agent", "wait":
		return policy.IsAllowed(safety.OpRead)
	case "write_file", "edit_file", "move_file", "copy_file":
		return policy.IsAllowed(safety.OpUpdate)
	case "delete_file":
		return policy.IsAllowed(safety.OpDelete)
	case "execute_command", "async_execute_command", "check_async_command",
		"kill_async_command", "read_async_output":
		return policy.IsAllowed(safety.OpExecute) || policy.IsAllowed(safety.OpExecuteReadOnly)
	case "run_sub_agent":
		return policy.IsAllowed(safety.OpExecute)
	default:
		return true
	}
}
