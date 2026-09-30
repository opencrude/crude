package agent

import (
	"encoding/json"

	"crude/codexio"
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
	return filterToolsWithCategory(allTools, specAllowed, specDenied, CategoryToolDefaults(category), isSubAgent)
}

// FilterToolsForModel resolves tool identity before applying allow/deny lists.
// A patch is one complete tool, never a set of selectively disabled operations.
func FilterToolsForModel(allTools []tools.ToolDef, specAllowed, specDenied []string, category string, isSubAgent bool, model string) []tools.ToolDef {
	if !codexio.IsGPT(model) {
		return FilterTools(allTools, specAllowed, specDenied, category, isSubAgent)
	}
	cat := CategoryToolDefaults(category)
	cat.AllowedTools = patchToolNames(cat.AllowedTools)
	cat.DeniedTools = patchToolNames(cat.DeniedTools)
	return filterToolsWithCategory(tools.WithModelCommands(tools.ForModel(allTools, model), model), patchToolNames(specAllowed), patchToolNames(specDenied), cat, isSubAgent)
}

func patchToolNames(names []string) []string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		if name == "write_file" || name == "edit_file" {
			name = "apply_patch"
		}
		result = append(result, name)
		if name == "execute_command" {
			result = append(result, "exec_command", "write_stdin")
		}
	}
	return result
}

func filterToolsWithCategory(allTools []tools.ToolDef, specAllowed, specDenied []string, catCfg CategoryToolConfig, isSubAgent bool) []tools.ToolDef {
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
		"run_sub_agent": "Optionally delegate a well-defined subtask when useful; direct tool use is equally supported. " +
			"Context handoff: pass the file paths of any previous agent reports (.md) in the `instructions` parameter so the new agent has context. " +
			"wait=true (default) returns the terminal result in this call; use it for a blocking dependency. " +
			"wait=false returns a handle immediately; continue independent work or end your turn and completion will notify you. " +
			"Assign exclusive owned paths, acceptance criteria, and a stopping condition. Agree shared interfaces before parallel edits. " +
			"Children run focused checks; the parent owns final integrated tests and lint. If sibling work blocks a child, it must report partial progress and stop, not repeatedly check changing files. " +
			"Batch independent launches together when parallel tool calls are available. " +
			"Do not actively wait or poll for routine results. " +
			"Required: description. " +
			"Optional: name (task/profile hint; personal identity is allocated automatically), category, instructions, policy, allowed_tools, denied_tools, file_ownership, wait. " +
			"Model and provider are resolved automatically from your configuration; do not specify them.",

		"poll_sub_agent": "Check a child status or recover a result when it is needed for a decision or user request. " +
			"Check a sub-agent that appears genuinely stalled, unresponsive, or broken. " +
			"The system auto-wakes you when sub-agents complete, so routine polling is unnecessary. " +
			"Avoid repeated polling while waiting for normal completion.",

		"list_sub_agents": "List active children (and completed children when requested) for user visibility, coordination, or troubleshooting. " +
			"Diagnose stalled, unresponsive, or conflicting sub-agents. " +
			"Completion notifications make repetitive status polling unnecessary.",

		"cancel_sub_agent": "Cancel a child at the user's request or when work is unwanted, misrouted, stale, or unresponsive. " +
			"Batch independent cancellations in one parallel call. Cancellation is not successful completion.",
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
	result := make([]tools.ToolDef, 0, len(tds))
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
