package session

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"crude/agent"
	"crude/collaboration"
	"crude/tools"
)

// executeClaimsTool owns claim-repair mutations that must share Session's
// lifecycle gate. The collaboration dispatcher delegates these exact names here.
func (s *Session) executeClaimsTool(ctx context.Context, actorID, name string, params map[string]any) (tools.ToolResult, bool, error) {
	if name != "agent_narrow_claims" && name != "agent_update_claims" {
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
	var paths []string
	switch values := params["paths"].(type) {
	case []string:
		paths = values
	case []any:
		for _, value := range values {
			path, valid := value.(string)
			if !valid {
				return tools.ToolResult{}, true, fmt.Errorf("paths must be an explicit array of concrete path strings")
			}
			paths = append(paths, path)
		}
	default:
		return tools.ToolResult{}, true, fmt.Errorf("paths is required; use an explicit empty array to relinquish scope")
	}
	validated, err := validateOwnershipPaths(s.WorkDir(), paths)
	if err != nil {
		return tools.ToolResult{}, true, err
	}
	versions, err := exactClaimVersions(params["expected_versions"])
	if err != nil {
		return tools.ToolResult{}, true, err
	}
	runtimeEpoch, err := exactClaimNumber(params, "runtime_epoch")
	if err != nil {
		return tools.ToolResult{}, true, err
	}
	runVersion, err := exactClaimNumber(params, "expected_run_version")
	if err != nil {
		return tools.ToolResult{}, true, err
	}
	var result collaboration.NarrowActiveClaimsResult
	if name == "agent_update_claims" {
		target := getStrParam(params, "agent_id")
		if !canControlAgent(ctx, store, actorID, target) {
			return tools.ToolResult{}, true, fmt.Errorf("agent %q cannot repair unrelated agent %q", actorID, target)
		}
		targetAgent, targetErr := store.Agent(ctx, target)
		if targetErr != nil {
			return tools.ToolResult{}, true, targetErr
		}
		request := collaboration.UpdateClaimsRequest{
			ParentID: actorID, AgentID: target, RuntimeEpoch: runtimeEpoch,
			TerminalRunID: getStrParam(params, "terminal_run_id"), ExpectedRunVersion: runVersion,
			ExpectedVersions: versions, Paths: validated,
		}
		var repaired collaboration.UpdateClaimsResult
		err = s.FileOwnership().CommitReplaceClaims(agent.AgentID(target), targetAgent.Name, validated, func() error {
			var commitErr error
			repaired, commitErr = store.UpdateClaims(ctx, request)
			return commitErr
		})
		return collaborationResult(repaired, err), true, err
	}
	request := collaboration.NarrowActiveClaimsRequest{
		AgentID: actorID, RuntimeEpoch: runtimeEpoch, RunID: getStrParam(params, "run_id"),
		ExpectedRunVersion: runVersion, ExpectedVersions: versions, Paths: validated,
	}
	err = s.FileOwnership().CommitReplaceClaims(agent.AgentID(actorID), actor.Name, validated, func() error {
		var commitErr error
		result, commitErr = store.NarrowActiveClaims(ctx, request)
		return commitErr
	})
	return collaborationResult(result, err), true, err
}

func exactClaimNumber(params map[string]any, key string) (uint64, error) {
	value, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%s is required and must be a positive exact integer", key)
	}
	return exactClaimValue(key, value)
}

func exactClaimVersions(raw any) (map[string]uint64, error) {
	versions := make(map[string]uint64)
	add := func(id string, value any) error {
		if id == "" {
			return fmt.Errorf("expected_versions contains an empty claim identity")
		}
		number, err := exactClaimValue("expected_versions["+id+"]", value)
		if err != nil {
			return err
		}
		versions[id] = number
		return nil
	}
	switch values := raw.(type) {
	case map[string]any:
		for id, value := range values {
			if err := add(id, value); err != nil {
				return nil, err
			}
		}
	case map[string]uint64:
		for id, value := range values {
			if err := add(id, value); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("expected_versions is required and must be an object of positive exact integers")
	}
	return versions, nil
}

func exactClaimValue(key string, value any) (uint64, error) {
	valid := func(number uint64) (uint64, error) {
		if number == 0 {
			return 0, fmt.Errorf("%s must be a positive exact integer", key)
		}
		return number, nil
	}
	switch number := value.(type) {
	case uint64:
		return valid(number)
	case uint32:
		return valid(uint64(number))
	case int:
		if number < 0 {
			return 0, fmt.Errorf("%s must be a positive exact integer", key)
		}
		return valid(uint64(number))
	case int64:
		if number < 0 {
			return 0, fmt.Errorf("%s must be a positive exact integer", key)
		}
		return valid(uint64(number))
	case json.Number:
		var parsed uint64
		if err := json.Unmarshal([]byte(number), &parsed); err != nil {
			return 0, fmt.Errorf("%s must be a positive exact integer", key)
		}
		return valid(parsed)
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || number < 1 || math.Trunc(number) != number || number > float64(math.MaxInt64) || number > (1<<53) {
			return 0, fmt.Errorf("%s must be a positive exact integer", key)
		}
		return valid(uint64(number))
	default:
		return 0, fmt.Errorf("%s must be a positive exact integer", key)
	}
}
