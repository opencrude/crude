package control

import (
	"encoding/json"
	"reflect"
	"strings"
)

// SettingsChangesAuthority checks the complete prospective write. Provider and
// catalog facts are not ordinary writable leaves: only the collector's exact
// account projection and old-model fact clears are admitted in context.
// Inputs may be typed settings or decoded JSON. path is their canonical prefix.
func SettingsChangesAuthority(before, after any, path string) bool {
	decode := func(value any) any {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		var result any
		if json.Unmarshal(raw, &result) != nil {
			return nil
		}
		return result
	}
	a, b := decode(before), decode(after)
	if a == nil || b == nil {
		return !reflect.DeepEqual(before, after)
	}
	root, _ := b.(map[string]any)
	var walk func(any, any, string) bool
	walk = func(old, next any, current string) bool {
		if reflect.DeepEqual(old, next) {
			return false
		}
		x, xok := old.(map[string]any)
		y, yok := next.(map[string]any)
		if xok && yok {
			keys := map[string]bool{}
			for key := range x {
				keys[key] = true
			}
			for key := range y {
				keys[key] = true
			}
			for key := range keys {
				p := key
				if current != "" {
					p = current + "." + key
				}
				if reflect.DeepEqual(x[key], y[key]) {
					continue
				}
				if agentCategoryPath(current) {
					modelChanged := x["Model"] != y["Model"]
					switch key {
					case "ModelInputPrice", "ModelOutputPrice", "ModelCachePrice":
						if modelChanged && y[key] == float64(0) {
							continue
						}
					case "ModelSupportsTools":
						if modelChanged && y[key] == false {
							continue
						}
					case "Provider":
						if x["account_id"] != y["account_id"] {
							accounts, _ := root["accounts"].(map[string]any)
							id, _ := y["account_id"].(string)
							account, exists := accounts[id].(map[string]any)
							if exists && id != "" && account["provider_id"] == y[key] {
								continue
							}
							if id == "" && y[key] == "" {
								continue
							}
						}
					}
				}
				if walk(x[key], y[key], p) {
					return true
				}
			}
			return false
		}
		return SettingsAuthority(current)
	}
	return walk(a, b, path)
}

func agentCategoryPath(path string) bool {
	parts := strings.Split(path, ".")
	return len(parts) == 3 && parts[0] == "agents" && strings.EqualFold(parts[1], "categories") || path == "memory.memory_category"
}

// OrdinaryAgentLeaf is shared by core field admission and UI draft classification.
// Model/profile instructions extend canonical policy; they do not grant tools.
func OrdinaryAgentLeaf(name string) bool {
	switch name {
	case "Model", "model", "account_id", "ThinkingEffort", "thinking_effort", "Instructions", "instructions", "Description", "description", "ModelContextWindow", "model_context_window", "ModelMaxOutput", "model_max_output", "ModelTemperature", "model_temperature", "ModelTopP", "model_top_p", "context_compaction_percent", "context_retention_tokens", "service_tier", "ModelModalities", "model_modalities":
		return true
	}
	return false
}

// SettingsAuthority classifies canonical JSON field paths, never labels. Exact
// effect owners must also check container writes and the actual changed leaves.
func SettingsAuthority(path string) bool {
	parts := strings.Split(path, ".")
	if strings.HasPrefix(path, "agents.execution_hygiene.") {
		return false
	}
	for _, part := range parts {
		switch strings.ToLower(part) {
		case "safety", "network", "accounts", "capabilities", "multi_agent", "apikey", "api_key", "providerapikey", "provider_api_key", "environment", "allowedtools", "deniedtools", "allowed_tools", "denied_tools", "skill_modes", "policy", "file_ownership", "category":
			return true
		}
	}
	if parts[0] == "lsp" && len(parts) > 1 {
		// A server definition is a program launch: its command, arguments and
		// server settings (gopls buildFlags=-toolexec=…, for example) run code
		// outside command review. Resource limits decide how hard the user's
		// machine works. Agents may switch features on and off, never these.
		if parts[1] == "languages" {
			return parts[len(parts)-1] != "enabled" || len(parts) != 4
		}
		switch parts[1] {
		case "max_servers", "server_threads", "server_memory_mb", "idle_timeout_seconds", "max_document_bytes":
			return true
		}
	}
	if parts[0] == "agents" || parts[0] == "overrides" || strings.HasPrefix(path, "memory.memory_category.") || (len(parts) > 1 && parts[0] == "value" && parts[1] == "Agents") {
		return !OrdinaryAgentLeaf(parts[len(parts)-1])
	}
	return path == "local_history.safety_snapshot_before_restore"
}
