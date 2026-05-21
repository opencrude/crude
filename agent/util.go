package agent

import (
	"fmt"
	"sort"
	"strings"

	"crude/provider"
	"crude/tools"
)

func getStr(params map[string]any, key string) string {
	if v, ok := params[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func toolDefsToProviderDefs(tds []tools.ToolDef) []provider.ToolDef {
	ephemeral := &provider.CacheControl{Type: "ephemeral"}
	result := make([]provider.ToolDef, len(tds))
	for i, td := range tds {
		result[i] = provider.ToolDef{
			Type: td.Type,
			Function: provider.FunctionDef{
				Name:        td.Function.Name,
				Description: td.Function.Description,
				Parameters:  td.Function.Parameters,
			},
			CacheControl: ephemeral,
		}
	}
	return result
}

func allToolDefs() []provider.ToolDef {
	ephemeral := &provider.CacheControl{Type: "ephemeral"}
	result := make([]provider.ToolDef, len(tools.AgentTools))
	for i, td := range tools.AgentTools {
		result[i] = provider.ToolDef{
			Type: td.Type,
			Function: provider.FunctionDef{
				Name:        td.Function.Name,
				Description: td.Function.Description,
				Parameters:  td.Function.Parameters,
			},
			CacheControl: ephemeral,
		}
	}
	return result
}

func containsPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

func formatToolArgs(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		v := params[k]
		switch val := v.(type) {
		case string:
			if len([]rune(val)) > 80 {
				parts = append(parts, k+": "+string([]rune(val)[:80])+"…")
			} else {
				parts = append(parts, k+": "+val)
			}
		case []any:
			var items []string
			for _, item := range val {
				if s, ok := item.(string); ok && s != "" {
					items = append(items, s)
				}
			}
			parts = append(parts, k+": "+strings.Join(items, ","))
		default:
			parts = append(parts, fmt.Sprintf("%s: %v", k, v))
		}
	}
	return strings.Join(parts, " ")
}

func CountLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
