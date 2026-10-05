package session

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"crude/agent"
	rootprompt "crude/prompt"
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
	_ = enabled
	return agent.ApplyOrchestratorOverrides(agent.FilterTools(tools.AgentTools, nil, nil, "", false))
}

func configuredOrchestratorTools(cfg *Config, enabled bool, model string) []tools.ToolDef {
	category := cfg.GetActiveDefaultCategory()
	return agent.FilterToolsForModel(orchestratorTools(enabled), category.AllowedTools, category.DeniedTools, "", false, model)
}

func constrainConfiguredTools(spec *agent.SubAgentSpec, allowed, denied []string) {
	spec.DeniedTools = append(spec.DeniedTools, denied...)
	if len(allowed) > 0 {
		definitions := append(append([]tools.ToolDef(nil), tools.AgentTools...), tools.CollaborationTools...)
		for _, tool := range definitions {
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
	var profiles []rootprompt.AgentProfile
	for _, name := range keys {
		p := cfg.Agents.AgentProfiles[name]
		if p.Category != CategoryResearcher && p.Category != CategoryCrafter && p.Category != CategoryAeron {
			continue // Root/background profiles are not child-launch choices.
		}
		if p.Enabled != nil && !*p.Enabled {
			continue
		}
		if role := cfg.GetCategoryConfig(p.Category); role.Enabled != nil && !*role.Enabled {
			continue
		}
		profiles = append(profiles, rootprompt.AgentProfile{Name: name, Role: p.Category, Description: p.Description})
	}
	return rootprompt.AgentProfileCatalog(profiles)
}

func delegationDisabledResult() tools.ToolResult {
	content, _ := json.Marshal(map[string]string{"status": "disabled_by_user", "reason": "user_disabled_multi_agent", "message": rootprompt.DelegationDisabledNotice()})
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
	text := rootprompt.DelegationDisabledNotice()
	if after {
		text = rootprompt.DelegationEnabledNotice()
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
