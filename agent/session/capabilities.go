package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"crude/agent/collaboration"
	"crude/agent/provider"
	"crude/agent/safety"
	"crude/agent/tools"
	"crude/agent/turnloop"
	captypes "crude/types/capability"
)

// SetCapabilities records the runtime capability handle and, when given, the file ownership scope.
func (s *Session) SetCapabilities(runtime captypes.Runtime, ownership *turnloop.FileOwnership) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.capabilities = runtime
	if ownership != nil {
		s.fileOwnership = ownership
	}
}

// FileOwnership returns the session's claim registry, and creates it on first use.
func (s *Session) FileOwnership() *turnloop.FileOwnership {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileOwnership == nil {
		s.fileOwnership = turnloop.NewFileOwnership()
	}
	return s.fileOwnership
}

func effectsForPolicy(policy safety.Policy) []captypes.Effect {
	var effects []captypes.Effect
	if policy.IsAllowed(safety.OpRead) {
		effects = append(effects, captypes.EffectRead, captypes.EffectCommunication)
	}
	if policy.IsAllowed(safety.OpUpdate) {
		effects = append(effects, captypes.EffectWrite)
	}
	if policy.IsAllowed(safety.OpDelete) {
		effects = append(effects, captypes.EffectDelete)
	}
	if policy.IsAllowed(safety.OpExecute) {
		effects = append(effects, captypes.EffectExecute)
	}
	return effects
}

func (s *Session) bindAgentRuntime(ctx context.Context, live *turnloop.Agent, actor captypes.RuntimeActor, name string) {
	s.mu.Lock()
	runtime := s.capabilities
	s.mu.Unlock()
	actor.Effects = effectsForPolicy(live.Policy)
	actor.AllowedTools, actor.DeniedTools = turnloop.ModelToolSelection(actor.AllowedTools, actor.DeniedTools, live.Model)
	previousPrepare := live.PrepareStep
	previousFn := live.ToolFn
	var boundEpoch uint64
	boundRunID := live.RunID
	if s.CollaborationStore() != nil {
		s.mu.Lock()
		boundEpoch = s.collaborationAgents[actor.ID].RuntimeEpoch
		s.mu.Unlock()
	}
	s.capabilitySharing.join(actor, name)
	var mu sync.RWMutex
	snapshot := captypes.RuntimeSnapshot{}
	currentActor := func() captypes.RuntimeActor {
		current := actor
		current.RunID = boundRunID
		current.RuntimeEpoch = boundEpoch
		current.TurnGeneration = live.TurnGeneration
		current.ExecutionRoot = live.WorkDir
		current.SessionDisabled = s.DisabledCapabilities()
		if live.ControlTarget != nil {
			current.WorkspaceID = live.ControlTarget.WorkspaceID
			if live.ControlTarget.Shell != nil {
				owner := *live.ControlTarget.Shell
				current.ShellOwner = &owner
			}
		}
		return current
	}
	live.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		var definitions []tools.ToolDef
		var notices []provider.ChatMessage
		if previousPrepare != nil {
			definitions, notices = previousPrepare()
		} else {
			definitions = append([]tools.ToolDef(nil), live.FilteredTools...)
		}
		for _, text := range s.capabilitySharing.drain(actor.ID) {
			notices = append(notices, runtimeMessage(text))
		}
		if runtime != nil && live.PlanInvestigation == nil {
			prepared, err := runtime.Prepare(ctx, currentActor())
			if err != nil {
				s.logger.Warn("CAPABILITY", "prepare_failed", "error", err.Error())
			} else {
				mu.Lock()
				snapshot = prepared
				mu.Unlock()
				for _, tool := range prepared.Tools {
					if !actorToolSelected(actor, tool.Name) || !turnloop.PolicyAllowsMetadata(live.Policy, tools.ToolMetadata{Name: tool.Name, Effect: tool.Effect, Scope: tool.Scope, Fingerprint: tool.Fingerprint, Dynamic: true}) {
						continue
					}
					definitions = appendUniqueDefinition(definitions, tools.ToolDef{Type: "function", Function: tools.ToolFuncDef{Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema}})
				}
				for _, text := range prepared.Notices {
					notices = append(notices, runtimeMessage(text))
				}
			}
			for _, definition := range tools.CapabilityTools {
				if runtimeToolSelected(actor, definition.Function.Name) {
					definitions = appendUniqueDefinition(definitions, definition)
				}
			}
		}
		for _, definition := range s.collaborationDefinitions(actor.ID) {
			effect := collaborationEffect(definition.Function.Name)
			selected := runtimeCollaborationToolSelected(actor, definition.Function.Name)
			if selected && slices.Contains(actor.Effects, effect) {
				definitions = appendUniqueDefinition(definitions, definition)
			}
		}
		return definitions, notices
	}
	live.ToolMetadata = func(name string) (tools.ToolMetadata, bool) {
		if metadata, native := tools.NativeMetadata(name); native {
			return metadata, true
		}
		for _, definition := range tools.CollaborationTools {
			if definition.Function.Name == name {
				return tools.ToolMetadata{Name: name, Effect: collaborationEffect(name), Scope: "workspace"}, true
			}
		}
		mu.RLock()
		defer mu.RUnlock()
		for _, tool := range snapshot.Tools {
			if tool.Name == name {
				return tools.ToolMetadata{Name: name, Effect: tool.Effect, Scope: tool.Scope, Fingerprint: tool.Fingerprint, Dynamic: true}, true
			}
		}
		return tools.ToolMetadata{}, false
	}
	live.ToolFn = func(callCtx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
		if err := callCtx.Err(); err != nil {
			return tools.ToolResult{}, err
		}
		if boundEpoch != 0 {
			participant, err := s.CollaborationStore().Agent(callCtx, actor.ID)
			if err != nil {
				return tools.ToolResult{}, err
			}
			if participant.RuntimeEpoch != boundEpoch || participant.CurrentRunID != boundRunID ||
				participant.Presence != collaboration.RuntimeAttached || participant.State != collaboration.AgentAvailable {
				return tools.ToolResult{}, collaboration.ErrStale
			}
			run, err := s.CollaborationStore().Run(callCtx, boundRunID)
			if err != nil {
				return tools.ToolResult{}, err
			}
			if run.State != collaboration.RunRunning && run.State != collaboration.RunWaitingHuman && run.State != collaboration.RunWaitingEvent {
				return tools.ToolResult{}, collaboration.ErrStale
			}
		}
		if !runtimeCollaborationToolSelected(actor, tool) {
			return tools.ToolResult{}, fmt.Errorf("tool %q is excluded by the effective agent selection", tool)
		}
		if live.PlanInvestigation != nil {
			if !planModeAllows(tool, live.ToolMetadata) {
				return tools.ToolResult{}, fmt.Errorf("plan mode does not allow %s", tool)
			}
			// These routes check their exact source/run and investigation paths
			// before any action. Ordinary source dispatch still has ReadOnlyPolicy.
			if tool == "open_plan_investigation" || planInvestigationFileTool(tool) || planInvestigationCommandTool(tool) || tool == "write_stdin" {
				return previousFn(callCtx, tool, params)
			}
		}
		if result, handled, err := s.executeCollaborationTool(callCtx, actor.ID, tool, params); handled {
			return result, err
		}
		mu.RLock()
		dynamic := false
		for _, prepared := range snapshot.Tools {
			dynamic = dynamic || prepared.Name == tool
		}
		mu.RUnlock()
		if runtime != nil && (dynamic || strings.HasPrefix(tool, "mcp__") || strings.HasPrefix(tool, "capability_") || tool == "mcp_read_resource" || tool == "mcp_get_prompt" || tool == "mcp_request_access") {
			if !dynamic && !strings.HasPrefix(tool, "mcp__") && !runtimeToolSelected(actor, tool) {
				return tools.ToolResult{}, fmt.Errorf("capability operation %q is excluded by this agent", tool)
			}
			mu.RLock()
			requestSnapshot := snapshot
			mu.RUnlock()
			result, err := runtime.Execute(callCtx, currentActor(), requestSnapshot, tool, params)
			var preparation *captypes.PreparationRequired
			if errors.As(err, &preparation) {
				prepared, outcome, prepErr := s.prepareCapabilityPackage(callCtx, live, *preparation)
				if !prepared {
					return outcome, prepErr
				}
				result, err = runtime.Execute(captypes.WithPrepared(callCtx, preparation.Key), currentActor(), requestSnapshot, tool, params)
			}
			if err == nil {
				if operation, id := sharedCapabilityID(tool, params); id != "" {
					s.capabilitySharing.share(actor.ID, operation, id)
				}
			}
			return capabilityMediaResult(result, err, live.MediaReadPolicy)
		}
		if _, native := tools.NativeMetadata(tool); !native && tool != "multi_tool_use.parallel" {
			return tools.ToolResult{}, fmt.Errorf("tool %q is not available in this request; it may belong to an MCP connection that is disabled, turned off for this session, or reconnecting", tool)
		}
		if !turnloop.PolicyAllows(live.Policy, tool) && tool != "multi_tool_use.parallel" {
			return tools.ToolResult{}, fmt.Errorf("tool %q is denied by the agent policy", tool)
		}
		resolveRelativeParamsAtRoot(live.WorkDir, tool, params)
		if isWriteTool(tool) {
			if err := s.validateNoInvestigationMutation(writeToolTargets(tool, params)); err != nil {
				return tools.ToolResult{}, err
			}
		}
		if isWriteTool(tool) && actor.ParentID == "" && live.WorkDir != s.WorkDir() {
			if err := validateExecutionWriteTargets(live.WorkDir, s.TempDir(), writeToolTargets(tool, params)); err != nil {
				return tools.ToolResult{}, err
			}
		}
		ownership := s.FileOwnership()
		if tool == "read_file" {
			ownership.RecordRead(turnloop.AgentID(actor.ID), getStrParam(params, "path"))
		}
		if isWriteTool(tool) && actor.ParentID == "" {
			store := s.CollaborationStore()
			if store == nil {
				return tools.ToolResult{}, errors.New("workspace collaboration runtime is unavailable")
			}
			participant, err := store.Agent(callCtx, actor.ID)
			if err != nil {
				return tools.ToolResult{}, err
			}
			if participant.CurrentRunID == "" {
				return tools.ToolResult{}, collaboration.ErrUnavailable
			}
			identity, _ := callCtx.Value(tools.ExecutionIdentityKey{}).(tools.ExecutionIdentity)
			if boundRunID == "" || identity.RunID != boundRunID || participant.CurrentRunID != boundRunID {
				return tools.ToolResult{}, collaboration.ErrStale
			}
			if identity.ToolCallID == "" {
				return tools.ToolResult{}, errors.New("filesystem mutation requires an originating tool-call identity")
			}
			targets := writeToolTargets(tool, params)
			if err := ownership.CommitClaimAll(turnloop.AgentID(actor.ID), name, targets, true, func() error {
				_, err := store.AcquireClaims(callCtx, collaboration.AcquireClaimsRequest{
					AgentID: actor.ID, RunID: boundRunID,
					RequestID: identity.ToolCallID, Paths: targets,
				})
				return err
			}); err != nil {
				return tools.ToolResult{}, err
			}
			release, err := ownership.BeginWrite(turnloop.AgentID(actor.ID), targets)
			if err != nil {
				return tools.ToolResult{}, err
			}
			defer release()
		}
		return previousFn(callCtx, tool, params)
	}
}

func runtimeCollaborationToolSelected(actor captypes.RuntimeActor, name string) bool {
	if slices.Contains(actor.DeniedTools, name) {
		return false
	}
	return slices.Contains(childCoordinationTools, name) && actor.ParentID != "" || actorToolSelected(actor, name)
}

func runtimeToolSelected(actor captypes.RuntimeActor, name string) bool {
	return actorToolSelected(actor, name) && slices.Contains(actor.Effects, captypes.EffectRead)
}

func actorToolSelected(actor captypes.RuntimeActor, name string) bool {
	return !slices.Contains(actor.DeniedTools, name) && (len(actor.AllowedTools) == 0 || slices.Contains(actor.AllowedTools, name))
}

func appendUniqueDefinition(definitions []tools.ToolDef, next tools.ToolDef) []tools.ToolDef {
	for _, definition := range definitions {
		if definition.Function.Name == next.Function.Name {
			return definitions
		}
	}
	return append(definitions, next)
}

func runtimeMessage(text string) provider.ChatMessage {
	message := provider.NewTextMessage("user", text)
	message.Origin = provider.OriginRuntimeContext
	message.Visibility = provider.VisibilityContextOnly
	message.InternalID = newMessageID()
	return message
}
