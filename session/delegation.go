package session

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"slices"
	"strings"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/tools"
)

type delegationOwnerKey struct{}
type delegationOwner struct {
	id         string
	generation uint64
}

func withDelegationOwner(ctx context.Context, id string, generation uint64) context.Context {
	return context.WithValue(ctx, delegationOwnerKey{}, delegationOwner{id: id, generation: generation})
}

func orchestratorTools(enabled bool) []tools.ToolDef {
	denied := []string{"write_report"}
	if !enabled {
		denied = append(denied, "run_sub_agent")
	}
	// Management stays available while disabled: existing children still need
	// cancellation/review and must not become inaccessible when a switch flips.
	return agent.ApplyOrchestratorOverrides(agent.FilterTools(tools.AgentTools, nil, denied, "", false))
}

func configuredOrchestratorTools(cfg *Config, enabled bool, model string) []tools.ToolDef {
	category := cfg.GetActiveDefaultCategory()
	return agent.FilterToolsForModel(orchestratorTools(enabled), category.AllowedTools, category.DeniedTools, "", false, model)
}

func constrainConfiguredTools(spec *agent.SubAgentSpec, allowed, denied []string) {
	spec.DeniedTools = append(spec.DeniedTools, denied...)
	if len(allowed) > 0 {
		for _, tool := range tools.AgentTools {
			if !slices.Contains(allowed, tool.Function.Name) {
				spec.DeniedTools = append(spec.DeniedTools, tool.Function.Name)
			}
		}
	}
}

func agentProfileCatalog(cfg *Config) string {
	if cfg == nil || len(cfg.Agents.AgentProfiles) == 0 {
		return ""
	}
	keys := make([]string, 0, len(cfg.Agents.AgentProfiles))
	for key := range cfg.Agents.AgentProfiles {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var out strings.Builder
	out.WriteString("\n<available_agent_profiles>Use a profile's name in run_sub_agent when its purpose matches a delegated task.\n")
	for _, name := range keys {
		p := cfg.Agents.AgentProfiles[name]
		if p.Enabled != nil && !*p.Enabled {
			continue
		}
		if role := cfg.GetCategoryConfig(p.Category); role.Enabled != nil && !*role.Enabled {
			continue
		}
		fmt.Fprintf(&out, "<profile name=\"%s\" role=\"%s\">%s</profile>\n", html.EscapeString(name), html.EscapeString(p.Category), html.EscapeString(p.Description))
	}
	out.WriteString("</available_agent_profiles>")
	return out.String()
}

const delegationDisabledNotice = "The user has disabled multi-agent mode in Settings. Do not launch new sub-agents. This is a user settings change, not an agent or execution failure. Continue the task yourself. Already-running children retain their actual status and can still be reviewed or cancelled."

func delegationDisabledResult() tools.ToolResult {
	content, _ := json.Marshal(map[string]string{"status": "disabled_by_user", "reason": "user_disabled_multi_agent", "message": delegationDisabledNotice})
	return tools.ToolResult{Content: string(content)}
}

// Settings serialize changes with launch admission. Notices are delivered as
// runtime context at the next model request, never fabricated user messages or
// failed-child results. No idle model request is needed just for a toggle.
func (s *Session) recordDelegationChange(before, after bool) {
	if before == after {
		return
	}
	s.delegationNoticeMu.Lock()
	defer s.delegationNoticeMu.Unlock()
	text := delegationDisabledNotice
	if after {
		text = "The user has enabled multi-agent mode in Settings. Sub-agent launches are available again. This is a user settings change, not recovery from an agent failure."
	}
	notice := provider.NewTextMessage("user", text)
	notice.Origin = provider.OriginRuntimeContext
	notice.Visibility = provider.VisibilityContextOnly
	notice.InternalID = fmt.Sprintf("delegation-setting-%d", time.Now().UnixNano())
	s.delegationNotices = append(s.delegationNotices, notice)
}

func (s *Session) takeDelegationNotices() []provider.ChatMessage {
	s.delegationNoticeMu.Lock()
	defer s.delegationNoticeMu.Unlock()
	notices := s.delegationNotices
	s.delegationNotices = nil
	return notices
}
