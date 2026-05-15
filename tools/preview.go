package tools

import (
	"fmt"
	"strings"
)

type PreviewSpec struct {
	Keys   []string
	Format func(pascalName string, params map[string]string) string
}

func (s PreviewSpec) ExtractStrings(raw map[string]any) map[string]string {
	out := make(map[string]string, len(s.Keys))
	for _, k := range s.Keys {
		out[k] = strVal(raw, k)
	}
	return out
}

var previewRegistry = map[string]PreviewSpec{
	"write_file": {
		Keys: []string{"path", "content"},
		Format: func(name string, p map[string]string) string {
			if p["path"] != "" && p["content"] != "" {
				return fmt.Sprintf("%s %s (%d lines)", name, p["path"], countLines(p["content"]))
			}
			if p["path"] != "" {
				return fmt.Sprintf("%s %s \u2026", name, p["path"])
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"edit_file": {
		Keys: []string{"path", "old_string", "new_string", "new_text"},
		Format: func(name string, p map[string]string) string {
			newStr := p["new_string"]
			if newStr == "" {
				newStr = p["new_text"]
			}
			if p["path"] != "" && p["old_string"] != "" && newStr != "" {
				return fmt.Sprintf("%s %s (\u2212%d/+%d lines)", name, p["path"], countLines(p["old_string"]), countLines(newStr))
			}
			if p["path"] != "" {
				return fmt.Sprintf("%s %s \u2026", name, p["path"])
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"read_file": {
		Keys: []string{"path"},
		Format: func(name string, p map[string]string) string {
			if p["path"] != "" {
				return fmt.Sprintf("%s %s", name, p["path"])
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"execute_command": {
		Keys: []string{"command"},
		Format: func(name string, p map[string]string) string {
			if p["command"] != "" {
				return fmt.Sprintf("%s %s", name, trunc(p["command"], 50))
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"move_file": {
		Keys: []string{"source", "destination"},
		Format: func(name string, p map[string]string) string {
			if p["source"] != "" && p["destination"] != "" {
				return fmt.Sprintf("%s %s \u2192 %s", name, p["source"], p["destination"])
			}
			if p["source"] != "" {
				return fmt.Sprintf("%s %s \u2192 \u2026", name, p["source"])
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"copy_file": {
		Keys: []string{"source", "destination"},
		Format: func(name string, p map[string]string) string {
			if p["source"] != "" && p["destination"] != "" {
				return fmt.Sprintf("%s %s \u2192 %s", name, p["source"], p["destination"])
			}
			if p["source"] != "" {
				return fmt.Sprintf("%s %s \u2192 \u2026", name, p["source"])
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"delete_file": {
		Keys: []string{"path"},
		Format: func(name string, p map[string]string) string {
			if p["path"] != "" {
				return fmt.Sprintf("%s %s", name, p["path"])
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"list_directory": {
		Keys: []string{"path"},
		Format: func(name string, p map[string]string) string {
			if p["path"] != "" {
				return fmt.Sprintf("%s %s", name, p["path"])
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"glob_files": {
		Keys: []string{"pattern"},
		Format: func(name string, p map[string]string) string {
			if p["pattern"] != "" {
				return fmt.Sprintf("%s %s", name, p["pattern"])
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"grep_files": {
		Keys: []string{"pattern", "path"},
		Format: func(name string, p map[string]string) string {
			if p["pattern"] != "" {
				return fmt.Sprintf("%s %s", name, trunc(p["pattern"], 50))
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"web_search": {
		Keys: []string{"query"},
		Format: func(name string, p map[string]string) string {
			if p["query"] != "" {
				return fmt.Sprintf("%s %s", name, trunc(p["query"], 50))
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"web_fetch": {
		Keys: []string{"url"},
		Format: func(name string, p map[string]string) string {
			if p["url"] != "" {
				return fmt.Sprintf("%s %s", name, trunc(p["url"], 50))
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"arxiv_search": {
		Keys: []string{"query"},
		Format: func(name string, p map[string]string) string {
			if p["query"] != "" {
				return fmt.Sprintf("%s %s", name, trunc(p["query"], 50))
			}
			return fmt.Sprintf("%s \u2026", name)
		},
	},
	"async_execute_command": {
		Keys: []string{"command", "handle"},
		Format: func(name string, p map[string]string) string {
			if p["command"] != "" && p["handle"] != "" {
				return fmt.Sprintf("%s %s [%s]", name, trunc(p["command"], 40), p["handle"])
			}
			if p["command"] != "" {
				return fmt.Sprintf("%s %s", name, trunc(p["command"], 50))
			}
			if p["handle"] != "" {
				return fmt.Sprintf("%s [%s]", name, p["handle"])
			}
			return name
		},
	},
	"check_async_command": {
		Keys: []string{"handle"},
		Format: func(name string, p map[string]string) string {
			if p["handle"] != "" {
				return fmt.Sprintf("%s %s", name, p["handle"])
			}
			return name
		},
	},
	"kill_async_command": {
		Keys: []string{"handle"},
		Format: func(name string, p map[string]string) string {
			if p["handle"] != "" {
				return fmt.Sprintf("%s %s", name, p["handle"])
			}
			return name
		},
	},
	"read_async_output": {
		Keys: []string{"handle"},
		Format: func(name string, p map[string]string) string {
			if p["handle"] != "" {
				return fmt.Sprintf("%s %s", name, p["handle"])
			}
			return name
		},
	},
	"wait": {
		Keys: []string{"seconds"},
		Format: func(name string, p map[string]string) string {
			if p["seconds"] != "" {
				return fmt.Sprintf("%s %ss", name, p["seconds"])
			}
			return name
		},
	},
	"mark_step_doing": {
		Keys: []string{"step_id"},
		Format: func(name string, p map[string]string) string {
			if p["step_id"] != "" {
				return fmt.Sprintf("%s %s", name, p["step_id"])
			}
			return name
		},
	},
	"mark_step_complete": {
		Keys: []string{"step_id", "activate_steps"},
		Format: func(name string, p map[string]string) string {
			if p["step_id"] != "" {
				label := fmt.Sprintf("%s %s", name, p["step_id"])
				if p["activate_steps"] != "" {
					label += fmt.Sprintf(" \u2192 %s", p["activate_steps"])
				}
				return label
			}
			return name
		},
	},
	"mark_step_failed": {
		Keys: []string{"step_id"},
		Format: func(name string, p map[string]string) string {
			if p["step_id"] != "" {
				return fmt.Sprintf("%s %s", name, p["step_id"])
			}
			return name
		},
	},
	"consult_oracle": {
		Keys: []string{"task", "decision_point"},
		Format: func(name string, p map[string]string) string {
			return name
		},
	},
	"run_javascript": {
		Keys: []string{"code"},
		Format: func(name string, p map[string]string) string {
			return name
		},
	},
	"plan": {
		Keys: []string{"objective"},
		Format: func(name string, p map[string]string) string {
			return name
		},
	},
	"list_goal_status": {
		Keys: []string{},
		Format: func(name string, p map[string]string) string {
			return name
		},
	},
	"report_blocker": {
		Keys: []string{"blocker_type", "message"},
		Format: func(name string, p map[string]string) string {
			return name
		},
	},
	"resolve_blocker": {
		Keys: []string{"message"},
		Format: func(name string, p map[string]string) string {
			return name
		},
	},
	"reply": {
		Keys: []string{"content"},
		Format: func(name string, p map[string]string) string {
			return name
		},
	},
}

func FormatPreview(toolName string, params map[string]any) string {
	pascalName := PascalCase(toolName)
	spec, ok := previewRegistry[toolName]
	if !ok {
		return defaultPreview(pascalName, params)
	}
	return spec.Format(pascalName, spec.ExtractStrings(params))
}

func PreviewKeys(toolName string) []string {
	if spec, ok := previewRegistry[toolName]; ok {
		return spec.Keys
	}
	return []string{"path", "command", "url", "query", "source", "destination", "pattern", "handle", "seconds"}
}

func defaultPreview(name string, params map[string]any) string {
	if path := strVal(params, "path"); path != "" {
		return fmt.Sprintf("%s %s", name, path)
	}
	if cmd := strVal(params, "command"); cmd != "" {
		return fmt.Sprintf("%s %s", name, trunc(cmd, 50))
	}
	return name
}

func PascalCase(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func strVal(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch sv := v.(type) {
		case string:
			return sv
		case []any:
			var parts []string
			for _, item := range sv {
				if s, ok := item.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			return strings.Join(parts, ",")
		case nil:
			return ""
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\u2026"
}
