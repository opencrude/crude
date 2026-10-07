package session

import (
	"context"
	"fmt"

	"crude/agent"
	"crude/collaboration"
	"crude/tools"
)

// executeClaimsTool owns claim-repair mutations that must share Session's
// lifecycle gate. The collaboration dispatcher delegates these exact names here.
func (s *Session) executeClaimsTool(ctx context.Context, actorID, name string, params map[string]any) (tools.ToolResult, bool, error) {
	if name != "agent_narrow_claims" {
		return tools.ToolResult{}, false, nil
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if _, err := collaborationExecutionIdentity(ctx); err != nil {
		return tools.ToolResult{}, true, err
	}
	store := s.CollaborationStore()
	if store == nil {
		return tools.ToolResult{}, true, fmt.Errorf("workspace collaboration runtime is unavailable")
	}
	actor, err := store.Agent(ctx, actorID)
	if err != nil {
		return tools.ToolResult{}, true, err
	}
	paths := toStringSlice(params["paths"])
	validated, err := validateOwnershipPaths(s.WorkDir(), paths)
	if err != nil {
		return tools.ToolResult{}, true, err
	}
	versions := claimVersions(params["expected_versions"])
	request := collaboration.NarrowActiveClaimsRequest{
		AgentID: actorID, RuntimeEpoch: uint64(getIntParam(params, "runtime_epoch")),
		RunID: getStrParam(params, "run_id"), ExpectedRunVersion: uint64(getIntParam(params, "expected_run_version")),
		ExpectedVersions: versions, Paths: validated,
	}
	var result collaboration.NarrowActiveClaimsResult
	err = s.FileOwnership().CommitReplaceClaims(agent.AgentID(actorID), actor.Name, validated, func() error {
		var commitErr error
		result, commitErr = store.NarrowActiveClaims(ctx, request)
		return commitErr
	})
	return collaborationResult(result, err), true, err
}

func claimVersions(raw any) map[string]uint64 {
	versions := make(map[string]uint64)
	values, _ := raw.(map[string]any)
	for id, value := range values {
		switch number := value.(type) {
		case float64:
			if number > 0 {
				versions[id] = uint64(number)
			}
		case int:
			if number > 0 {
				versions[id] = uint64(number)
			}
		case uint64:
			if number > 0 {
				versions[id] = number
			}
		}
	}
	return versions
}
