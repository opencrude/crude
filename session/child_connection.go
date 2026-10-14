package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"crude/agent"
	"crude/sessiondb"
	"crude/tools"
	ttypes "crude/types"
)

// childConnection is a child's account, model and thinking-effort selection.
// A parent may choose it at launch or replace it later; it takes effect at
// the child's next model request, never mid-request.
type childConnection struct {
	AccountID      string `json:"account_id"`
	Model          string `json:"model"`
	ThinkingEffort string `json:"thinking_effort"`
}

func connectionOf(spec agent.SubAgentSpec) childConnection {
	return childConnection{AccountID: spec.AccountID, Model: spec.Model, ThinkingEffort: spec.ThinkingEffort}
}

// childProviderConfig is the complete selection every child provider is built
// from: launch, each supervised attempt, continuation and request-boundary
// switches. Building from account and model alone silently dropped the
// configured thinking effort and sampling.
func childProviderConfig(spec agent.SubAgentSpec) CategoryConfig {
	return CategoryConfig{
		AccountID: spec.AccountID, Model: spec.Model, ThinkingEffort: spec.ThinkingEffort,
		ModelTemperature: spec.ModelTemperature, ModelTopP: spec.ModelTopP,
		ModelContextWindow: spec.ModelContextWindow,
	}
}

// applyConnection installs next into spec. Capability and sampling values
// describe one model; a different model resolves its own from the registry.
func applyConnection(spec *agent.SubAgentSpec, next childConnection) {
	if next.Model != spec.Model || next.AccountID != spec.AccountID {
		spec.ModelContextWindow, spec.ModelMaxOutput = 0, 0
		spec.ModelInputPrice, spec.ModelOutputPrice, spec.ModelCachePrice = 0, 0, 0
		spec.ModelModalities = nil
		spec.ModelTemperature, spec.ModelTopP = nil, nil
	}
	spec.AccountID, spec.Model, spec.ThinkingEffort = next.AccountID, next.Model, next.ThinkingEffort
}

// connectionRequest is a parent's requested change; unset fields keep the
// child's current value. thinking_effort "default" restores the role's
// configured effort.
type connectionRequest struct {
	accountID, model, effort        string
	hasAccount, hasModel, hasEffort bool
	effortDefault                   bool
}

func parseConnectionRequest(params map[string]any) (connectionRequest, error) {
	var request connectionRequest
	if value, ok := params["account_id"].(string); ok && strings.TrimSpace(value) != "" {
		request.accountID, request.hasAccount = strings.TrimSpace(value), true
	}
	if value, ok := params["model"].(string); ok && strings.TrimSpace(value) != "" {
		request.model, request.hasModel = strings.TrimSpace(value), true
	}
	if value, ok := params["thinking_effort"].(string); ok && strings.TrimSpace(value) != "" {
		value = strings.ToLower(strings.TrimSpace(value))
		switch {
		case value == "default":
			request.effortDefault, request.hasEffort = true, true
		case ttypes.ValidThinkingLevel(value):
			request.effort, request.hasEffort = value, true
		default:
			return request, fmt.Errorf("thinking_effort %q is not one of default, %s", value, strings.Join(ttypes.ThinkingLevels, ", "))
		}
	}
	return request, nil
}

func (request connectionRequest) empty() bool {
	return !request.hasAccount && !request.hasModel && !request.hasEffort
}

func (request connectionRequest) resolve(current childConnection, roleDefaultEffort string) childConnection {
	next := current
	if request.hasAccount {
		next.AccountID = request.accountID
	}
	if request.hasModel {
		next.Model = request.model
	}
	if request.effortDefault {
		next.ThinkingEffort = roleDefaultEffort
	} else if request.hasEffort {
		next.ThinkingEffort = request.effort
	}
	return next
}

// currentConfig is the configuration child providers resolve against: the
// live runtime profile, else this orchestrator's effective config, else the
// session's.
func (r *SubAgentRunner) currentConfig() *Config {
	if r.session.runtimeProfile != nil {
		if cfg := r.session.runtimeProfile.GetEffectiveConfig(r.orchestratorID); cfg != nil {
			return cfg
		}
	}
	if r.effectiveCfg != nil {
		return r.effectiveCfg
	}
	r.session.mu.Lock()
	defer r.session.mu.Unlock()
	return copyConfig(r.session.cfg)
}

// validateConnection proves the account resolves and a provider can be built
// before a parent's choice is accepted, so a typo never strands the child.
func (r *SubAgentRunner) validateConnection(spec agent.SubAgentSpec) error {
	cfg := r.currentConfig()
	if cfg == nil {
		return errors.New("child configuration is unavailable")
	}
	if spec.Model == "" {
		return errors.New("no model is configured for this child; pass model")
	}
	if _, err := cfg.providerForCategory(childProviderConfig(spec)); err != nil {
		return fmt.Errorf("account %q cannot serve model %q: %w", spec.AccountID, spec.Model, err)
	}
	return nil
}

func (r *SubAgentRunner) setActiveConnection(id agent.AgentID, connection childConnection) {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	if r.activeConnections == nil {
		r.activeConnections = make(map[agent.AgentID]childConnection)
	}
	r.activeConnections[id] = connection
}

func (r *SubAgentRunner) forgetConnection(id agent.AgentID) {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	delete(r.activeConnections, id)
	delete(r.pendingConnections, id)
}

// requestBoundary returns the child's per-step hook that installs a parent's
// pending connection change, rebuilds its provider, records the choice in the
// durable recovery owner (so continuation and resume keep it) and tells the UI
// which model produces the following output.
func (r *SubAgentRunner) requestBoundary(handle *agent.SubAgentHandle, state *recoveryRunState, supervisor *agent.Supervisor) func() bool {
	return func() bool {
		r.connMu.Lock()
		next, pending := r.pendingConnections[handle.Spec.ID]
		delete(r.pendingConnections, handle.Spec.ID)
		r.connMu.Unlock()
		if !pending || next == connectionOf(handle.Spec) {
			return false
		}
		spec := handle.Spec
		applyConnection(&spec, next)
		cfg := r.currentConfig()
		if cfg == nil {
			return false
		}
		p, err := cfg.providerForCategory(childProviderConfig(spec))
		if err != nil {
			r.session.logger.Warn("SUBAGENT", "connection_change_rejected", "agent_id", spec.ID, "error", err.Error())
			return false
		}
		handle.Spec = spec
		live := handle.Agent
		live.Provider, live.Model = p, p.Model
		live.MaxOutputTokens = spec.ModelMaxOutput
		live.MediaReadPolicy = &tools.MediaReadPolicy{Model: p.Model, Standard: p.StandardID(), Modalities: slices.Clone(spec.ModelModalities)}
		r.setActiveConnection(spec.ID, connectionOf(spec))
		if state != nil {
			state.mu.Lock()
			var owner childRecoveryOwner
			if json.Unmarshal(state.ownerData, &owner) == nil {
				owner.Spec = spec
				if data, encodeErr := json.Marshal(owner); encodeErr == nil {
					state.ownerData = data
				}
			}
			state.mu.Unlock()
			if supervisor != nil {
				if err := supervisor.Persist(supervisor.CheckpointStatus()); err != nil {
					r.session.logger.Warn("SUBAGENT", "connection_change_persist_failed", "agent_id", spec.ID, "error", err.Error())
				}
			}
		}
		if r.sender != nil {
			r.sender.SendAgentEventWithID("connection_changed", ttypes.TurnIdentity{
				Generation: live.TurnGeneration, ProviderID: p.ID, AccountID: spec.AccountID, ModelID: p.Model,
			}, string(spec.ID))
		}
		r.session.logger.Info("SUBAGENT", "connection_changed", "agent_id", spec.ID, "account", spec.AccountID, "model", p.Model, "effort", spec.ThinkingEffort)
		return true
	}
}

// configureChild applies a parent's model/effort choice to one of its
// children: a live child switches at its next request; a stopped child keeps
// it for its continuation. A finished child needs a new assignment.
func (s *Session) configureChild(ctx context.Context, actorID string, params map[string]any) (map[string]any, error) {
	target := getStrParam(params, "agent_id")
	store := s.CollaborationStore()
	if store == nil {
		return nil, errors.New("workspace collaboration runtime is unavailable")
	}
	if target == "" || target == actorID || !canControlAgent(ctx, store, actorID, target) {
		return nil, fmt.Errorf("agent %q cannot configure %q: only an agent's parent can change its model", actorID, target)
	}
	request, err := parseConnectionRequest(params)
	if err != nil {
		return nil, err
	}
	if request.empty() {
		return nil, errors.New("pass at least one of model, account_id or thinking_effort")
	}
	runner := s.SubAgentRunner()
	if runner == nil {
		return nil, errors.New("no child runtime is available")
	}
	id := agent.AgentID(target)
	result := func(spec agent.SubAgentSpec, applies string) map[string]any {
		effort := spec.ThinkingEffort
		if effort == "" {
			effort = "provider default"
		}
		return map[string]any{"agent_id": target, "account_id": spec.AccountID, "model": spec.Model, "thinking_effort": effort, "applies": applies}
	}
	if _, active := runner.Manager().GetActive(id); active {
		runner.connMu.Lock()
		current, known := runner.activeConnections[id]
		if pending, queued := runner.pendingConnections[id]; queued {
			current = pending
		}
		runner.connMu.Unlock()
		if !known {
			return nil, fmt.Errorf("agent %q has no live connection to configure", target)
		}
		category := ""
		if participant, err := store.Agent(ctx, target); err == nil {
			category = participant.Category
		}
		next := request.resolve(current, runner.currentConfig().GetCategoryConfig(category).ThinkingEffort)
		probe := agent.SubAgentSpec{AccountID: next.AccountID, Model: next.Model, ThinkingEffort: next.ThinkingEffort}
		if err := runner.validateConnection(probe); err != nil {
			return nil, err
		}
		runner.connMu.Lock()
		if runner.pendingConnections == nil {
			runner.pendingConnections = make(map[agent.AgentID]childConnection)
		}
		runner.pendingConnections[id] = next
		runner.connMu.Unlock()
		return result(probe, "at the child's next model request"), nil
	}
	s.recoveryMu.Lock()
	checkpoint, stopped := s.pendingRecoveries[target]
	s.recoveryMu.Unlock()
	if !stopped || len(checkpoint.OwnerData) == 0 {
		return nil, fmt.Errorf("agent %q is not running or stopped; a finished child needs a new assignment", target)
	}
	var owner childRecoveryOwner
	if err := json.Unmarshal(checkpoint.OwnerData, &owner); err != nil || string(owner.Spec.ID) != target {
		return nil, fmt.Errorf("agent %q has an unreadable task record", target)
	}
	spec := owner.Spec
	applyConnection(&spec, request.resolve(connectionOf(spec), runner.currentConfig().GetCategoryConfig(spec.Category).ThinkingEffort))
	if err := runner.validateConnection(spec); err != nil {
		return nil, err
	}
	owner.Spec = spec
	data, err := json.Marshal(owner)
	if err != nil {
		return nil, err
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.recoveryMu.Lock()
	current, still := s.pendingRecoveries[target]
	if !still || current.Status.ID != checkpoint.Status.ID || current.Status.Generation != checkpoint.Status.Generation {
		s.recoveryMu.Unlock()
		return nil, fmt.Errorf("agent %q changed state while configuring; inspect it again", target)
	}
	current.OwnerData = data
	s.recoveryMu.Unlock()
	if err := sessiondb.SaveRecoveryCheckpoint(s.db, s.sessionID, current); err != nil {
		return nil, fmt.Errorf("record the child's new model: %w", err)
	}
	s.recoveryMu.Lock()
	if latest, ok := s.pendingRecoveries[target]; ok && latest.Status.ID == current.Status.ID && latest.Status.Generation == current.Status.Generation {
		s.pendingRecoveries[target] = current
	}
	s.recoveryMu.Unlock()
	return result(spec, "when the child continues"), nil
}
