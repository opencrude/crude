package session

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/safety"
	"crude/tools"
	captypes "crude/types/capability"
)

func (session *Session) SetCapabilities(runtime captypes.Runtime, ownership *agent.FileOwnership) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.capabilities = runtime
	if ownership != nil {
		session.fileOwnership = ownership
	}
}

func (session *Session) FileOwnership() *agent.FileOwnership {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.fileOwnership == nil {
		session.fileOwnership = agent.NewFileOwnership()
	}
	return session.fileOwnership
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

func (session *Session) bindAgentRuntime(ctx context.Context, live *agent.Agent, actor captypes.RuntimeActor, name string) {
	session.mu.Lock()
	runtime := session.capabilities
	session.mu.Unlock()
	actor.Effects = effectsForPolicy(live.Policy)
	previousPrepare := live.PrepareStep
	previousFn := live.ToolFn
	var boundEpoch uint64
	var boundRunID string
	if store := session.CollaborationStore(); store != nil {
		session.mu.Lock()
		boundEpoch = session.collaborationAgents[actor.ID].RuntimeEpoch
		session.mu.Unlock()
		if participant, err := store.Agent(ctx, actor.ID); err == nil {
			boundRunID = participant.CurrentRunID
		}
	}
	var mu sync.RWMutex
	snapshot := captypes.RuntimeSnapshot{}
	live.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		var definitions []tools.ToolDef
		var notices []provider.ChatMessage
		if previousPrepare != nil {
			definitions, notices = previousPrepare()
		} else {
			definitions = append([]tools.ToolDef(nil), live.FilteredTools...)
		}
		if runtime != nil {
			prepared, err := runtime.Prepare(ctx, actor)
			if err != nil {
				session.logger.Warn("CAPABILITY", "prepare_failed", "error", err.Error())
			} else {
				mu.Lock()
				snapshot = prepared
				mu.Unlock()
				for _, tool := range prepared.Tools {
					if !actorToolSelected(actor, tool.Name) || !agent.PolicyAllowsMetadata(live.Policy, tools.ToolMetadata{Name: tool.Name, Effect: tool.Effect, Scope: tool.Scope, Fingerprint: tool.Fingerprint, Dynamic: true}) {
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
		for _, definition := range session.collaborationDefinitions(actor.ID) {
			effect := collaborationEffect(definition.Function.Name)
			selected := actorToolSelected(actor, definition.Function.Name) || definition.Function.Name == "publish_report" && actor.ParentID != ""
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
			participant, err := session.CollaborationStore().Agent(callCtx, actor.ID)
			if err != nil {
				return tools.ToolResult{}, err
			}
			if participant.RuntimeEpoch != boundEpoch || participant.CurrentRunID != boundRunID ||
				participant.Presence != collaboration.RuntimeAttached {
				return tools.ToolResult{}, collaboration.ErrStale
			}
		}
		if slices.Contains(actor.DeniedTools, tool) || len(actor.AllowedTools) > 0 && !slices.Contains(actor.AllowedTools, tool) && (tool != "publish_report" || actor.ParentID == "") {
			return tools.ToolResult{}, fmt.Errorf("tool %q is excluded by the effective agent selection", tool)
		}
		if result, handled, err := session.executeCollaborationTool(callCtx, actor.ID, tool, params); handled {
			return result, err
		}
		if runtime != nil && (strings.HasPrefix(tool, "mcp__") || strings.HasPrefix(tool, "capability_") || tool == "mcp_read_resource" || tool == "mcp_get_prompt" || tool == "mcp_request_access") {
			if !strings.HasPrefix(tool, "mcp__") && !runtimeToolSelected(actor, tool) {
				return tools.ToolResult{}, fmt.Errorf("capability operation %q is excluded by this agent", tool)
			}
			mu.RLock()
			requestSnapshot := snapshot
			mu.RUnlock()
			result, err := runtime.Execute(callCtx, actor, requestSnapshot, tool, params)
			var text strings.Builder
			for _, content := range result.Content {
				if content.Text != "" {
					text.WriteString(content.Text)
					text.WriteByte('\n')
				} else if content.Data != "" {
					data, decodeErr := base64.StdEncoding.DecodeString(content.Data)
					if decodeErr != nil {
						text.WriteString("Binary content returned; its byte size is unavailable.\n")
					} else {
						fmt.Fprintf(&text, "Binary %s · %s · %d bytes", content.Type, content.MIMEType, len(data))
						if content.URI != "" {
							fmt.Fprintf(&text, " · %s", content.URI)
						}
						text.WriteByte('\n')
					}
				}
			}
			if len(result.Structured) > 0 {
				text.Write(result.Structured)
			}
			if result.Unknown {
				text.WriteByte('\n')
				text.WriteString("Result unknown: the remote operation may have taken effect. Do not retry blindly.")
			}
			if result.IsError && err == nil {
				err = errors.New("MCP server reported a tool failure")
			}
			return tools.ToolResult{Content: strings.TrimSpace(text.String())}, err
		}
		if !agent.PolicyAllows(live.Policy, tool) && tool != "multi_tool_use.parallel" {
			return tools.ToolResult{}, fmt.Errorf("tool %q is denied by the agent policy", tool)
		}
		session.resolveRelativeParams(tool, params)
		ownership := session.FileOwnership()
		if tool == "read_file" {
			ownership.RecordRead(agent.AgentID(actor.ID), getStrParam(params, "path"))
		}
		if isWriteTool(tool) {
			store := session.CollaborationStore()
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
			if identity.ToolCallID == "" {
				return tools.ToolResult{}, errors.New("filesystem mutation requires an originating tool-call identity")
			}
			for _, path := range writeToolTargets(tool, params) {
				if err := ownership.CheckOrClaim(agent.AgentID(actor.ID), name, path, true); err != nil {
					return tools.ToolResult{}, err
				}
			}
			if _, err := store.AcquireClaims(callCtx, collaboration.AcquireClaimsRequest{
				AgentID: actor.ID, RunID: participant.CurrentRunID,
				RequestID: identity.ToolCallID, Paths: writeToolTargets(tool, params),
			}); err != nil {
				return tools.ToolResult{}, err
			}
		}
		return previousFn(callCtx, tool, params)
	}
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
