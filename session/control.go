package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"crude/agent"
	"crude/provider"
	"crude/safety"
	"crude/tools"
	captypes "crude/types/capability"
	"crude/types/control"
)

type controlTargetKey struct{}

func controlTargetFromContext(ctx context.Context) *control.Target {
	if target, ok := ctx.Value(controlTargetKey{}).(control.Target); ok {
		copy := target.Clone()
		return &copy
	}
	return nil
}

func (s *Session) SetControlRuntime(runtime control.Runtime) {
	s.mu.Lock()
	s.controlRuntime = runtime
	s.mu.Unlock()
}

func (s *Session) revokeCapabilityRun(actor captypes.RuntimeActor) {
	if actor.RunID == "" {
		return
	}
	s.mu.Lock()
	runtime := s.capabilities
	s.mu.Unlock()
	if revoker, ok := runtime.(captypes.RunRevoker); ok {
		revoker.RevokeRun(actor)
	}
}

func controlEffectAllowed(policy safety.Policy, effect control.Effect) bool {
	switch effect {
	case control.Read:
		return policy.IsAllowed(safety.OpRead)
	case control.Write:
		return policy.IsAllowed(safety.OpUpdate)
	case control.Delete:
		return policy.IsAllowed(safety.OpDelete)
	case control.Execute:
		return policy.IsAllowed(safety.OpExecute)
	case control.Communication:
		return policy.IsAllowed(safety.OpRead)
	default:
		return false
	}
}

func controlDescriptorAllowed(policy safety.Policy, descriptor control.Descriptor) bool {
	return !descriptor.HumanOnly && controlEffectAllowed(policy, descriptor.Effect)
}

// bindAgentControl retains the admitted shell identity for the complete turn and
// its children. The runtime rechecks the actual caller/run and target at effects.
func (s *Session) bindAgentControl(ctx context.Context, live *agent.Agent, target *control.Target) {
	s.mu.Lock()
	runtime := s.controlRuntime
	s.mu.Unlock()
	if target != nil {
		copy := target.Clone()
		live.ControlTarget = &copy
	}
	if runtime == nil {
		return
	}
	previousPrepare, previousFn := live.PrepareStep, live.ToolFn
	selected := func(name string) bool { return s.controlToolSelected(live, name) }
	live.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		var definitions []tools.ToolDef
		var notices []provider.ChatMessage
		if previousPrepare != nil {
			definitions, notices = previousPrepare()
		} else {
			definitions = append(definitions, live.FilteredTools...)
		}
		for _, def := range tools.ControlTools {
			if selected(def.Function.Name) && live.Policy.IsAllowed(safety.OpRead) {
				definitions = appendUniqueDefinition(definitions, def)
			}
		}
		return definitions, notices
	}
	live.ToolFn = func(callCtx context.Context, name string, params map[string]any) (tools.ToolResult, error) {
		if live.ControlTarget != nil {
			callCtx = context.WithValue(callCtx, controlTargetKey{}, live.ControlTarget.Clone())
		}
		if name != "crude_commands" && name != "crude_control" {
			return previousFn(callCtx, name, params)
		}
		if !selected(name) || !live.Policy.IsAllowed(safety.OpRead) {
			return tools.ToolResult{}, errors.New("crude control tool is excluded by this role")
		}
		caller, err := s.controlCaller(callCtx, live)
		if err != nil {
			return tools.ToolResult{}, err
		}
		bound := control.Target{SessionID: s.sessionID}
		if live.ControlTarget != nil {
			bound = live.ControlTarget.Clone()
		}
		if name == "crude_commands" {
			for key := range params {
				if key != "query" {
					return tools.ToolResult{}, fmt.Errorf("unknown command discovery field %q", key)
				}
			}
			descriptors, err := runtime.Describe(callCtx, caller, bound, getStrParam(params, "query"))
			if err != nil {
				return tools.ToolResult{}, err
			}
			var allowed []control.Descriptor
			for _, descriptor := range descriptors {
				if !descriptor.HumanOnly && (descriptor.Effect == "" || controlEffectAllowed(live.Policy, descriptor.Effect)) {
					allowed = append(allowed, descriptor)
				}
			}
			raw, err := json.Marshal(allowed)
			return tools.ToolResult{Content: string(raw)}, err
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return tools.ToolResult{}, err
		}
		var input struct {
			Command       string                `json:"command"`
			Arguments     json.RawMessage       `json:"arguments"`
			Target        *control.Target       `json:"target,omitempty"`
			Preconditions control.Preconditions `json:"preconditions,omitempty"`
		}
		if err := strictControlJSON(raw, &input); err != nil {
			return tools.ToolResult{}, err
		}
		if input.Target != nil {
			if input.Target.WorkspaceID != "" || input.Target.Shell != nil {
				return tools.ToolResult{}, errors.New("agent control cannot replace its admitted workspace or shell binding")
			}
			if input.Target.SessionID != "" {
				bound.SessionID = input.Target.SessionID
			}
			if input.Target.ResourceID != "" {
				bound.ResourceID = input.Target.ResourceID
			}
			if input.Target.ExecutionID != "" {
				if live.Depth > 0 && input.Target.ExecutionID != bound.ExecutionID && input.Command != "execution.review" && input.Command != "execution.apply" {
					return tools.ToolResult{}, errors.New("child control cannot replace its bound execution")
				}
				bound.ExecutionID = input.Target.ExecutionID
			}
		}
		identity, _ := callCtx.Value(tools.ExecutionIdentityKey{}).(tools.ExecutionIdentity)
		if identity.ToolCallID == "" || identity.RunID != caller.RunID {
			return tools.ToolResult{}, errors.New("crude operation requires originating tool-call identity")
		}
		digest := sha256.Sum256([]byte(caller.AgentID + "\x00" + caller.RunID + "\x00" + identity.ToolCallID))
		request := control.Request{ID: "control-" + hex.EncodeToString(digest[:]), CommandID: input.Command,
			Caller: caller, Target: bound, Input: input.Arguments, Preconditions: input.Preconditions}
		descriptor, err := runtime.Resolve(callCtx, request)
		if err != nil {
			return tools.ToolResult{}, err
		}
		if !controlDescriptorAllowed(live.Policy, descriptor) {
			return tools.ToolResult{}, errors.New("resolved Crude operation exceeds this agent's effect ceiling or requires human authority")
		}
		result, err := runtime.Execute(callCtx, request)
		if result.RequestID == "" && err != nil {
			return tools.ToolResult{}, err
		}
		encoded, encodeErr := json.Marshal(result)
		return tools.ToolResult{Content: string(encoded)}, errors.Join(err, encodeErr)
	}
	_ = ctx // Lifetime is carried by call contexts and exact runtime run checks.
}

func (s *Session) controlCaller(ctx context.Context, live *agent.Agent) (control.Caller, error) {
	store := s.CollaborationStore()
	if store == nil || live.RunID == "" {
		return control.Caller{}, errors.New("workspace collaboration is required for Crude control")
	}
	participant, err := store.Agent(ctx, string(live.AgentID))
	if err != nil || participant.CurrentRunID != live.RunID {
		return control.Caller{}, errors.New("crude control caller has no active owned run")
	}
	run, err := store.Run(ctx, live.RunID)
	if err != nil || run.AgentID != string(live.AgentID) || run.State.Terminal() {
		return control.Caller{}, errors.New("crude control caller run has settled")
	}
	return control.Caller{Kind: control.AgentCaller, AgentID: string(live.AgentID), RunID: live.RunID}, nil
}

func (s *Session) controlToolSelected(live *agent.Agent, name string) bool {
	if live.Depth > 0 {
		if s.subAgentSup == nil {
			return false
		}
		if handle, ok := s.subAgentSup.Manager().GetActive(live.AgentID); ok {
			return !slices.Contains(handle.Spec.DeniedTools, name) && (len(handle.Spec.AllowedTools) == 0 || slices.Contains(handle.Spec.AllowedTools, name))
		}
		return false
	}
	s.mu.Lock()
	cfg := copyConfig(s.cfg)
	s.mu.Unlock()
	if s.runtimeProfile != nil {
		cfg = s.runtimeProfile.GetEffectiveConfig(string(live.AgentID))
	}
	category := cfg.GetActiveDefaultCategory()
	return !slices.Contains(category.DeniedTools, name) && (len(category.AllowedTools) == 0 || slices.Contains(category.AllowedTools, name))
}

func strictControlJSON(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("control input must contain exactly one JSON value")
	}
	return nil
}
