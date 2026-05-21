package agent

import (
	"encoding/json"

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
			"web_search", "web_fetch", "arxiv_search", "run_javascript",
			"write_report", "question",
			"plan", "mark_step_complete", "mark_step_doing", "mark_step_failed",
			"report_blocker", "resolve_blocker", "list_goal_status",
			"wait", "reply",
		},
			DeniedTools: []string{
				"write_file", "edit_file", "delete_file", "move_file", "copy_file",
				"execute_command", "async_execute_command",
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
				"write_report", "reply",
				"list_sub_agents", "poll_sub_agent", "wait",
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

func FilterTools(allTools []tools.ToolDef, specAllowed []string, specDenied []string, category string, depth int, maxDepth int) []tools.ToolDef {
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

	if depth >= maxDepth {
		filtered = filterToolSlice(filtered, func(t tools.ToolDef) bool {
			return t.Function.Name != "run_sub_agent"
		})
	}

	if depth >= 2 {
		filtered = filterToolSlice(filtered, func(t tools.ToolDef) bool {
			return t.Function.Name != "execute_command" && t.Function.Name != "delete_file"
		})
	}

	if depth > 0 {
		filtered = ensureToolExists(filtered, "write_report")
	}

	return filtered
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
