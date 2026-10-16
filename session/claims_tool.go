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
	target := actor
	if name == "agent_update_claims" {
		id := getStrParam(params, "agent_id")
		if !canControlAgent(ctx, store, actorID, id) {
			return tools.ToolResult{}, true, fmt.Errorf("agent %q cannot repair unrelated agent %q", actorID, id)
		}
		if target, err = store.Agent(ctx, id); err != nil {
			return tools.ToolResult{}, true, err
		}
	}
	runtimeEpoch, runID, runVersion, versions, err := resolveClaimPreimage(ctx, store, target, params)
	if err != nil {
		return tools.ToolResult{}, true, err
	}
	root := s.WorkDir()
	// The execution service retains the child's exact checkout across active
	// and interrupted attempts. Never bind relative repairs to the parent's root.
	if target.ParentID != "" {
		if service, serviceErr := s.executionService(); serviceErr == nil {
			found := false
			for _, execution := range service.List() {
				if execution.Owner.AgentID != target.ID {
					continue
				}
				if !execution.Ready || execution.Removed || execution.LogicalRoot != s.WorkDir() {
					return tools.ToolResult{}, true, fmt.Errorf("child execution checkout is unavailable or foreign")
				}
				if found {
					return tools.ToolResult{}, true, fmt.Errorf("child execution checkout is ambiguous")
				}
				found = true
				root = execution.Root
			}
		}
	}
	validated, err := validateOwnershipPaths(root, paths)
	if err != nil {
		return tools.ToolResult{}, true, err
	}
	if root != s.WorkDir() {
		if err := validateExecutionWriteTargets(root, "", validated); err != nil {
			return tools.ToolResult{}, true, err
		}
	}
	var result collaboration.NarrowActiveClaimsResult
	if name == "agent_update_claims" {
		request := collaboration.UpdateClaimsRequest{
			ParentID: actorID, AgentID: target.ID, RuntimeEpoch: runtimeEpoch,
			RunID: runID, ExpectedRunVersion: runVersion,
			ExpectedVersions: versions, Paths: validated,
		}
		var repaired collaboration.UpdateClaimsResult
		err = s.FileOwnership().CommitReplaceClaims(agent.AgentID(target.ID), target.Name, validated, func() error {
			var commitErr error
			repaired, commitErr = store.UpdateClaims(ctx, request)
			return commitErr
		})
		return collaborationResult(repaired, err), true, err
	}
	request := collaboration.NarrowActiveClaimsRequest{
		AgentID: actorID, RuntimeEpoch: runtimeEpoch, RunID: runID,
		ExpectedRunVersion: runVersion, ExpectedVersions: versions, Paths: validated,
	}
	err = s.FileOwnership().CommitReplaceClaims(agent.AgentID(actorID), actor.Name, validated, func() error {
		var commitErr error
		result, commitErr = store.NarrowActiveClaims(ctx, request)
		return commitErr
	})
	return collaborationResult(result, err), true, err
}

// resolveClaimPreimage returns the identities a claim change is fenced on.
// Values the caller passes are exact preimages (as read from agent_inspect);
// omitted ones are read from the target's current state, so the common case
// needs only the new paths. The store still rejects the change if any of them
// moves before it commits.
func resolveClaimPreimage(ctx context.Context, store *collaboration.Store, target collaboration.Agent, params map[string]any) (epoch uint64, runID string, runVersion uint64, versions map[string]uint64, err error) {
	epoch = target.RuntimeEpoch
	if _, given := params["runtime_epoch"]; given {
		if epoch, err = exactClaimNumber(params, "runtime_epoch"); err != nil {
			return
		}
	}
	runID = getStrParam(params, "run_id")
	if runID == "" {
		runID = target.CurrentRunID
	}
	if runID == "" {
		err = fmt.Errorf("agent %q has no current run; pass the run_id of its interrupted run from agent_inspect", target.ID)
		return
	}
	if _, given := params["expected_run_version"]; given {
		if runVersion, err = exactClaimNumber(params, "expected_run_version"); err != nil {
			return
		}
	} else {
		run, runErr := store.Run(ctx, runID)
		if runErr != nil {
			err = runErr
			return
		}
		runVersion = run.Version
	}
	if raw, given := params["expected_versions"]; given {
		versions, err = exactClaimVersions(raw)
		return
	}
	claims, claimsErr := store.Claims(ctx, target.ID, true)
	if claimsErr != nil {
		err = claimsErr
		return
	}
	versions = make(map[string]uint64, len(claims))
	for _, claim := range claims {
		versions[claim.ID] = claim.Version
	}
	return
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
