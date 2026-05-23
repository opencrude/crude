package agent

import (
	"encoding/json"

	"crude/tools"
)

var SubAgentManagementTools = []string{
	"run_sub_agent",
	"poll_sub_agent",
	"cancel_sub_agent",
	"list_sub_agents",
}

type CategoryToolConfig struct {
	AllowedTools []string
	DeniedTools  []string
}

func CategoryToolDefaults(category string) CategoryToolConfig {
	switch category {
	case CatResearcher:
		return CategoryToolConfig{
			AllowedTools: []string{
				"read_file", "glob_files", "grep_files", "list_directory",
				"web_search", "web_fetch", "arxiv_search", "run_javascript",
				"execute_command",
				"write_report", "question",
				"plan", "mark_step_complete", "mark_step_doing", "mark_step_failed",
				"report_blocker", "resolve_blocker", "list_goal_status",
				"wait", "reply",
			},
			DeniedTools: []string{
				"write_file", "edit_file", "delete_file", "move_file", "copy_file",
				"async_execute_command",
			},
		}
	case CatCrafter:
		return CategoryToolConfig{
			AllowedTools: nil,
			DeniedTools:  []string{"delete_file"},
		}
	case CatAeron:
		return CategoryToolConfig{
			AllowedTools: []string{
				"read_file", "write_file", "edit_file", "glob_files", "grep_files",
				"list_directory",
				"write_report", "reply", "wait",
			},
			DeniedTools: []string{
				"delete_file", "move_file", "copy_file",
				"execute_command", "async_execute_command",
				"web_search", "web_fetch", "arxiv_search",
				"run_javascript",
			},
		}
	default:
		return CategoryToolConfig{}
	}
}

func FilterTools(allTools []tools.ToolDef, specAllowed []string, specDenied []string, category string, isSubAgent bool) []tools.ToolDef {
	catCfg := CategoryToolDefaults(category)
	filtered := allTools

	if len(catCfg.DeniedTools) > 0 {
		denied := makeStringSet(catCfg.DeniedTools)
		filtered = filterToolSlice(filtered, func(t tools.ToolDef) bool {
			return !denied[t.Function.Name]
		})
	}

	if len(catCfg.AllowedTools) > 0 {
		allowed := makeStringSet(catCfg.AllowedTools)
		filtered = filterToolSlice(filtered, func(t tools.ToolDef) bool {
			return allowed[t.Function.Name]
		})
	}

	if len(specDenied) > 0 {
		denied := makeStringSet(specDenied)
		filtered = filterToolSlice(filtered, func(t tools.ToolDef) bool {
			return !denied[t.Function.Name]
		})
	}

	if len(specAllowed) > 0 {
		allowed := makeStringSet(specAllowed)
		filtered = filterToolSlice(filtered, func(t tools.ToolDef) bool {
			return allowed[t.Function.Name]
		})
	}

	if isSubAgent {
		filtered = denyTools(filtered, SubAgentManagementTools)
		filtered = ensureToolExists(filtered, "write_report")
	}

	return filtered
}

func ApplyMainAgentOverrides(filtered []tools.ToolDef) []tools.ToolDef {
	descs := mainAgentToolDescriptions()
	result := make([]tools.ToolDef, len(filtered))
	for i, t := range filtered {
		if desc, ok := descs[t.Function.Name]; ok {
			result[i] = t
			result[i].Function.Description = desc
		} else {
			result[i] = t
		}
	}
	return result
}

func mainAgentToolDescriptions() map[string]string {
	return map[string]string{
		"read_file": "Emergency-only file reader for the orchestrator. " +
			"Delegate ALL file reading to a researcher sub-agent. " +
			"Using this means your delegation pipeline failed — you are doing reconnaissance that should be handled by a specialist.",

		"write_file": "Emergency-only file writer for the orchestrator. " +
			"Delegate ALL file writing to a crafter or aeron sub-agent. " +
			"Using this means you are doing implementation work that should be delegated.",

		"edit_file": "Emergency-only file editor for the orchestrator. " +
			"Delegate ALL file editing to a crafter or aeron sub-agent. " +
			"Using this means you are doing implementation work that should be delegated.",

		"delete_file": "Emergency-only. Delegate to a crafter sub-agent. " +
			"Using this means you are doing file operations that should be delegated.",

		"move_file": "Emergency-only. Delegate to a crafter sub-agent. " +
			"Using this means you are doing file operations that should be delegated.",

		"copy_file": "Emergency-only. Delegate to a crafter sub-agent. " +
			"Using this means you are doing file operations that should be delegated.",

		"glob_files": "Emergency-only file finder for the orchestrator. " +
			"Delegate ALL file search to a researcher sub-agent. " +
			"Using this means you failed to delegate reconnaissance.",

		"grep_files": "Emergency-only code search for the orchestrator. " +
			"Delegate ALL code search to a researcher sub-agent. " +
			"Using this means you failed to delegate reconnaissance.",

		"list_directory": "Emergency-only directory listing for the orchestrator. " +
			"Delegate to a researcher sub-agent. " +
			"Using this means you failed to delegate reconnaissance.",

		"web_search": "Emergency-only web search for the orchestrator. " +
			"Delegate ALL web research to a researcher sub-agent. " +
			"Using this means you failed to delegate research work.",

		"web_fetch": "Emergency-only web fetch for the orchestrator. " +
			"Delegate ALL web research to a researcher sub-agent. " +
			"Using this means you failed to delegate research work.",

		"arxiv_search": "Emergency-only academic search for the orchestrator. " +
			"Delegate ALL research to a researcher sub-agent. " +
			"Using this means you failed to delegate research work.",

		"run_sub_agent": "Spawn a leaf agent for a well-defined subtask. " +
			"Sub-agents run ASYNCHRONOUSLY — the system auto-wakes you on completion. " +
			"Launch, then either continue other work or end your turn. " +
			"NEVER actively wait or poll for results. " +
			"Required: category, description. " +
			"Optional: instructions, policy, allowed_tools, denied_tools, file_ownership, max_steps, timeout_seconds, wait. " +
			"Model and provider are resolved automatically from your configuration — do NOT specify them.",

		"poll_sub_agent": "DISASTER RECOVERY ONLY — do NOT use during normal operation. " +
			"Check a sub-agent that appears genuinely stalled, unresponsive, or broken. " +
			"The system AUTO-WAKES you when sub-agents complete — you never need to poll for results. " +
			"If you reach for this tool and there is no real emergency, something is wrong with your approach. " +
			"End your turn instead and let the system notify you.",

		"list_sub_agents": "TROUBLESHOOTING ONLY — not for routine status checks. " +
			"Diagnose stalled, unresponsive, or conflicting sub-agents. " +
			"The system auto-wakes you on completion — you never need to check who is running. " +
			"Use only when something appears to have gone wrong.",

		"cancel_sub_agent": "Cancel a sub-agent that is wrong, stale, or unresponsive. " +
			"Use only for genuine course corrections — never as a routine workflow tool.",
	}
}

func denyTools(toolList []tools.ToolDef, names []string) []tools.ToolDef {
	denied := makeStringSet(names)
	return filterToolSlice(toolList, func(t tools.ToolDef) bool {
		return !denied[t.Function.Name]
	})
}

func makeStringSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}

func filterToolSlice(tds []tools.ToolDef, pred func(tools.ToolDef) bool) []tools.ToolDef {
	var result []tools.ToolDef
	for _, t := range tds {
		if pred(t) {
			result = append(result, t)
		}
	}
	return result
}

func ensureToolExists(toolList []tools.ToolDef, name string) []tools.ToolDef {
	for _, t := range toolList {
		if t.Function.Name == name {
			return toolList
		}
	}
	toolList = append(toolList, tools.ToolDef{
		Type: "function",
		Function: tools.ToolFuncDef{
			Name:        name,
			Description: "Auto-included tool for sub-agents.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	})
	return toolList
}
