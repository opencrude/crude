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
				"web_search", "web_fetch", "arxiv_search",
				"execute_command",
				"write_report", "question",
				"update_plan", "get_plan",
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

func ApplyOrchestratorOverrides(filtered []tools.ToolDef) []tools.ToolDef {
	descs := orchestratorToolDescriptions()
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

func orchestratorToolDescriptions() map[string]string {
	return map[string]string{
		"read_file": "Only for reading agent reports, or as a limited fallback for the orchestrator. " +
			"Delegate ALL file reading to a researcher sub-agent. " +
			"Using this for anything other than agent reports means the task should usually be routed to a specialist instead.",

		"write_file": "Fallback-only file writer for the orchestrator. " +
			"Delegate ALL file writing to a crafter or aeron sub-agent. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"edit_file": "Fallback-only file editor for the orchestrator. " +
			"Delegate ALL file editing to a crafter or aeron sub-agent. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"delete_file": "Fallback-only. Delegate to a crafter sub-agent whenever possible. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"move_file": "Fallback-only. Delegate to a crafter sub-agent whenever possible. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"copy_file": "Fallback-only. Delegate to a crafter sub-agent whenever possible. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"glob_files": "Fallback-only file finder for the orchestrator. " +
			"Delegate ALL file search to a researcher sub-agent. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"grep_files": "Fallback-only code search for the orchestrator. " +
			"Delegate ALL code search to a researcher sub-agent. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"list_directory": "Fallback-only directory listing for the orchestrator. " +
			"Delegate to a researcher sub-agent. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"web_search": "Fallback-only web search for the orchestrator. " +
			"Delegate ALL web research to a researcher sub-agent. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"web_fetch": "Fallback-only web fetch for the orchestrator. " +
			"Delegate ALL web research to a researcher sub-agent. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"arxiv_search": "Fallback-only academic search for the orchestrator. " +
			"Delegate ALL research to a researcher sub-agent. " +
			"Use this directly only when delegation is unavailable or exhausted.",

		"run_sub_agent": "Spawn a leaf agent for a well-defined subtask. " +
			"Context handoff: pass the file paths of any previous agent reports (.md) in the `instructions` parameter so the new agent has context. " +
			"Sub-agents run asynchronously; the system auto-wakes you on completion. " +
			"Launch, then either continue independent work or end your turn. " +
			"Do not actively wait or poll for routine results. " +
			"Required: description. " +
			"Optional: name (derived when omitted), category, instructions, policy, allowed_tools, denied_tools, file_ownership, timeout_seconds, wait. " +
			"Model and provider are resolved automatically from your configuration; do not specify them.",

		"poll_sub_agent": "Recovery-only status check; do not use during normal operation. " +
			"Check a sub-agent that appears genuinely stalled, unresponsive, or broken. " +
			"The system auto-wakes you when sub-agents complete, so routine polling is unnecessary. " +
			"For normal waiting, end your turn and let the system notify you.",

		"list_sub_agents": "Troubleshooting-only; not for routine status checks. " +
			"Diagnose stalled, unresponsive, or conflicting sub-agents. " +
			"The system auto-wakes you on completion — you never need to check who is running. " +
			"Use only when the asynchronous flow appears blocked.",

		"cancel_sub_agent": "Cancel a sub-agent that is misrouted, stale, or unresponsive. " +
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
