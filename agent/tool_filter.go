package agent

import (
	"crude/codexio"
	"crude/tools"
)

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
				"publish_report", "question",
				"update_plan", "get_plan",
				"wait", "reply",
				"capability_search", "capability_activate", "mcp_request_access", "mcp_read_resource", "mcp_get_prompt",
				"agent_list", "agent_inspect", "agent_send", "agent_respond",
				"artifact_read", "agent_wait", "board_read",
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
				"publish_report", "reply", "wait",
				"capability_search", "capability_activate", "mcp_request_access", "mcp_read_resource", "mcp_get_prompt",
				"agent_list", "agent_inspect", "agent_send", "agent_respond",
				"artifact_read", "agent_wait", "board_read",
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

	return filtered
}

func ApplyOrchestratorOverrides(filtered []tools.ToolDef) []tools.ToolDef {
	return append([]tools.ToolDef(nil), filtered...)
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
