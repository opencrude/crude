package session

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"crude/agent"
	"crude/board"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	"crude/tools"
	captypes "crude/types/capability"
)

type boardAcceptance struct {
	Actor   string
	Through int64
	BoardID string
	Store   *board.Store
}

func (session *Session) SetCapabilities(runtime captypes.Runtime, ownership *agent.FileOwnership, store *board.Store, boardID, projectID string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.capabilities, session.boardStore = runtime, store
	session.boardID, session.boardProject = boardID, projectID
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
	runtime, store, boardID, projectID := session.capabilities, session.boardStore, session.boardID, session.boardProject
	session.mu.Unlock()
	actor.Effects = effectsForPolicy(live.Policy)
	if store != nil {
		if err := store.Join(ctx, boardID, board.Member{ID: actor.ID, Name: name, ParentID: actor.ParentID, Availability: board.AvailabilityActive}); err != nil {
			session.logger.Warn("BOARD", "join_failed", "error", err.Error())
			store = nil
		}
	}
	previousPrepare := live.PrepareStep
	previousFn := live.ToolFn
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
		if store != nil {
			for _, definition := range tools.BoardTools {
				if runtimeToolSelected(actor, definition.Function.Name) {
					definitions = appendUniqueDefinition(definitions, definition)
				}
			}
			if notice, err := store.PendingNotice(ctx, boardID, actor.ID); err == nil && notice != "" {
				notices = append(notices, runtimeMessage(notice))
			}
		}
		return definitions, notices
	}
	live.ToolMetadata = func(name string) (tools.ToolMetadata, bool) {
		if metadata, native := tools.NativeMetadata(name); native {
			return metadata, true
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
		if slices.Contains(actor.DeniedTools, tool) || len(actor.AllowedTools) > 0 && !slices.Contains(actor.AllowedTools, tool) && tool != "write_report" {
			return tools.ToolResult{}, fmt.Errorf("tool %q is excluded by the effective agent selection", tool)
		}
		if tool == "board_read" || tool == "board_post" {
			if store == nil || !runtimeToolSelected(actor, tool) {
				return tools.ToolResult{}, errors.New("this agent has no collaboration-board grant")
			}
			identity, _ := callCtx.Value(tools.ExecutionIdentityKey{}).(tools.ExecutionIdentity)
			if identity.ToolCallID == "" {
				return tools.ToolResult{}, errors.New("board operation needs an originating tool-call identity")
			}
			if tool == "board_post" {
				var references []board.Reference
				if raw, err := json.Marshal(params["references"]); err == nil {
					_ = json.Unmarshal(raw, &references)
				}
				result, err := store.Post(callCtx, board.PostRequest{BoardID: boardID, ProjectID: projectID, SenderID: actor.ID, ProviderMessageID: firstString(identity.MessageID, identity.ToolCallID), ToolCallID: identity.ToolCallID, To: getStrParam(params, "to"), Type: getStrParam(params, "type"), Body: getStrParam(params, "body"), ReplyTo: getStrParam(params, "reply_to"), References: references})
				if err != nil {
					return tools.ToolResult{}, err
				}
				encoded, _ := json.Marshal(result)
				return tools.ToolResult{Content: string(encoded)}, err
			}
			page, err := store.Read(callCtx, board.ReadRequest{BoardID: boardID, ProjectID: projectID, AgentID: actor.ID, Since: int64(getIntParam(params, "since")), Thread: getStrParam(params, "thread"), Limit: getIntParam(params, "limit")})
			if err != nil {
				return tools.ToolResult{}, err
			}
			encoded, err := json.Marshal(page)
			if err != nil {
				return tools.ToolResult{}, err
			}
			session.mu.Lock()
			if session.boardPending == nil {
				session.boardPending = make(map[string]boardAcceptance)
			}
			session.boardPending[identity.ToolCallID] = boardAcceptance{Actor: actor.ID, Through: page.AcceptThrough, BoardID: boardID, Store: store}
			session.mu.Unlock()
			return tools.ToolResult{Content: string(encoded)}, nil
		}
		if runtime != nil && (strings.HasPrefix(tool, "mcp__") || strings.HasPrefix(tool, "capability_") || tool == "mcp_read_resource" || tool == "mcp_get_prompt") {
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
		if actor.ParentID == "" {
			ownership := session.FileOwnership()
			if tool == "read_file" {
				ownership.RecordRead(agent.AgentID(actor.ID), getStrParam(params, "path"))
			}
			if isWriteTool(tool) {
				for _, target := range writeToolTargets(tool, params) {
					if err := ownership.CheckOrClaim(agent.AgentID(actor.ID), name, target, true); err != nil {
						return tools.ToolResult{}, err
					}
				}
			}
		}
		return previousFn(callCtx, tool, params)
	}
}

func (session *Session) bindBoardAdmission(live *agent.Agent, db *sql.DB, sessionID, contextID string, generation func() uint64) {
	live.ToolResultAccepted = func(name, id string, messages []provider.ChatMessage) {
		if name != "board_read" || db == nil {
			return
		}
		session.mu.Lock()
		pending, exists := session.boardPending[id]
		session.mu.Unlock()
		if !exists || pending.Through == 0 {
			return
		}
		if err := sessiondb.SaveContextState(db, sessionID, contextID, sessiondb.ContextState{AgentID: contextID, Generation: generation(), Reason: "tool_admission", Messages: messages}); err != nil {
			session.logger.Warn("BOARD", "context_admission_failed", "error", err.Error())
			return
		}
		if err := pending.Store.AcceptRead(context.Background(), pending.BoardID, pending.Actor, pending.Through); err == nil {
			session.mu.Lock()
			delete(session.boardPending, id)
			session.mu.Unlock()
		}
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

func firstString(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

func (session *Session) acceptBoardResults(ctx context.Context, db *sql.DB, agentID string, messages []provider.ChatMessage) {
	if db == nil {
		return
	}
	for _, message := range messages {
		if message.Role != "tool" || message.ToolError {
			continue
		}
		session.mu.Lock()
		pending, exists := session.boardPending[message.ToolCallID]
		session.mu.Unlock()
		if !exists || agentID != "" && pending.Actor != agentID || pending.Through == 0 {
			continue
		}
		var saved int
		if db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE tool_call_id = ? AND tool_error = 0`, message.ToolCallID).Scan(&saved) != nil || saved == 0 {
			continue
		}
		if pending.Store == nil {
			continue
		}
		if err := pending.Store.AcceptRead(ctx, pending.BoardID, pending.Actor, pending.Through); err == nil {
			session.mu.Lock()
			delete(session.boardPending, message.ToolCallID)
			session.mu.Unlock()
		}
	}
}

func (session *Session) finishedBoardAgent(ctx context.Context, id, name, parent string) {
	session.mu.Lock()
	store, boardID := session.boardStore, session.boardID
	session.mu.Unlock()
	if store != nil {
		_ = store.Join(ctx, boardID, board.Member{ID: id, Name: name, ParentID: parent, Availability: board.AvailabilityFinished})
	}
	session.FileOwnership().Release(agent.AgentID(id))
}
