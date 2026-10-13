package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"

	"crude/agent"
	rootprompt "crude/prompt"
	"crude/sessiondb"
)

func (s *Session) bindContinuity(live *agent.Agent, db *sql.DB, sessionID string) {
	live.LatestHumanTurnState = func(ctx context.Context) (string, bool, error) {
		_, human, err := sessiondb.LatestTurnStates(ctx, db, sessionID)
		if err != nil || human == nil {
			return "", false, err
		}
		return human.State, true, nil
	}
	live.ContinuationContext = func(ctx context.Context) (rootprompt.SessionContinuityData, error) {
		latest, human, err := sessiondb.LatestTurnStates(ctx, db, sessionID)
		if err != nil {
			return rootprompt.SessionContinuityData{}, err
		}
		// Settings publication owns cfg and the runtime profile under s.mu.
		// Detach the effective config before projecting it outside that lock.
		s.mu.Lock()
		var current *Config
		if s.runtimeProfile != nil {
			current = s.runtimeProfile.GetEffectiveConfig(string(live.AgentID))
		} else if s.cfg != nil {
			current = copyConfig(s.cfg)
		}
		s.mu.Unlock()
		state := struct {
			SessionID       string               `json:"session_id"`
			LatestTurn      *sessiondb.TurnState `json:"latest_turn"`
			LatestHumanTurn *sessiondb.TurnState `json:"latest_human_turn"`
			Model           string               `json:"model,omitempty"`
			AccountID       string               `json:"account_id,omitempty"`
			MultiAgent      bool                 `json:"multi_agent_enabled"`
			AllowedTools    []string             `json:"allowed_tools,omitempty"`
			DeniedTools     []string             `json:"denied_tools,omitempty"`
			SkillModes      map[string]string    `json:"skill_modes,omitempty"`
			WorkspaceID     string               `json:"workspace_id,omitempty"`
		}{SessionID: sessionID, LatestTurn: latest, LatestHumanTurn: human}
		if current != nil {
			category := current.GetActiveDefaultCategory()
			state.Model, state.MultiAgent = live.Model, current.IsMultiAgentEnabled()
			state.AllowedTools = append([]string(nil), category.AllowedTools...)
			state.DeniedTools = append([]string(nil), category.DeniedTools...)
			state.SkillModes = maps.Clone(category.SkillModes)
		}
		if live.Provider != nil {
			state.AccountID = live.Provider.AccountID
		}
		if store := s.CollaborationStore(); store != nil {
			state.WorkspaceID = store.WorkspaceID()
		}
		data, err := json.Marshal(state)
		if err != nil {
			return rootprompt.SessionContinuityData{}, err
		}
		result := rootprompt.SessionContinuityData{StateJSON: string(data), LatestHumanCompleted: human != nil && human.State == sessiondb.TurnCompleted, IncludePlan: live.Depth == 0}
		if live.Depth == 0 {
			plan, err := s.activePlanTurnContext(ctx)
			if err != nil {
				return rootprompt.SessionContinuityData{}, err
			}
			result.PlanContext = plan
		}
		return result, ctx.Err()
	}
}
