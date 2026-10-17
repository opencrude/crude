package workspace

import (
	"encoding/hex"
	"time"
	"unicode/utf8"

	"crude/collaboration"
	"crude/provider"
	ttypes "crude/types"
	"crude/types/command"
	"crude/types/reasoning"
	"crude/types/recovery"
)

const IdleTickInterval = 400 * time.Millisecond

type AgentState struct {
	ID        string
	Messages  *Conversation
	Streaming bool
	Thinking  bool
	// Running remains true across provider waits, permission prompts, and tool
	// execution. Streaming/Thinking describe output shape; Running owns the
	// turn lifecycle and prevents tool gaps from being reported as idle.
	Running          bool
	Cancelling       bool
	ActiveCompaction TurnStatus
	Recovery         recovery.Status
	TokenBuf         string
	LastTokenTime    time.Time
	SpinFrame        int
	ActiveTyping     bool
	Completed        bool
	Status           string
	Logger           Logger

	ReasoningMode reasoning.Kind

	// Index of the last blocking question message in the message list,
	// so it can be updated in-place when the user confirms/denies.
	// Set to -1 when no blocking message was appended (new compact-tag mode).
	BlockingMsgIdx int

	// PendingPermTag stores a permission result that should be attached to the
	// next tool_call's message as a PermTag suffix. Values: "allowed",
	// "always", "auto". Cleared after being applied.
	PendingPermTag string
	// PendingPermTool stores the tool name that the pending tag applies to,
	// so it's only applied to the correct tool message.
	PendingPermTool string
	PendingBlockID  string
	TurnIdentity    ttypes.TurnIdentity
	runtimeIdentity ttypes.RuntimeEventIdentity
	runtimeEventIDs map[string]struct{}
	terminal        bool
}

func NewAgentState(id string, messages *Conversation, lg Logger) *AgentState {
	if messages == nil {
		messages = NewConversation()
	}
	return &AgentState{
		ID:              id,
		Messages:        messages,
		Logger:          lg,
		runtimeEventIDs: make(map[string]struct{}),
	}
}

// AcceptRuntimeEvent admits one exact backend envelope. Same-run semantic
// evidence remains admissible after terminal UI state, while duplicates,
// replaced runs, wrong owners and non-monotonic sequences fail closed.
func (a *AgentState) AcceptRuntimeEvent(id ttypes.RuntimeEventIdentity) bool {
	if a == nil || !id.Valid() || id.AgentID != a.ID || id.OwnerID == "" {
		return false
	}
	if _, duplicate := a.runtimeEventIDs[id.EventID]; duplicate {
		return false
	}
	current := a.runtimeIdentity
	if current.Valid() {
		if id.OwnerID != current.OwnerID || id.RuntimeEpoch < current.RuntimeEpoch || id.Generation < current.Generation {
			return false
		}
		if id.RuntimeEpoch == current.RuntimeEpoch && id.Generation == current.Generation {
			if id.RunID != current.RunID || id.Sequence <= current.Sequence {
				return false
			}
		}
	}
	if current.Valid() && (id.RuntimeEpoch > current.RuntimeEpoch || id.Generation > current.Generation) {
		a.terminal, a.Completed, a.Cancelling = false, false, false
	}
	a.runtimeIdentity = id
	if len(a.runtimeEventIDs) >= 256 {
		a.runtimeEventIDs = make(map[string]struct{})
	}
	a.runtimeEventIDs[id.EventID] = struct{}{}
	return true
}

func (a *AgentState) Tick(spinFrame int) {
	if a.Messages != nil {
		a.Messages.AdvanceToolLifecycles(time.Now())
	}
	if a.TokenBuf != "" {
		a.Messages.AppendToken(a.TokenBuf)
		a.TokenBuf = ""
		a.LastTokenTime = time.Now()
	}
	a.SpinFrame = spinFrame
}

func (a *AgentState) ComputeActiveTyping() bool {
	return (a.Streaming || a.Thinking) && !a.LastTokenTime.IsZero() && time.Since(a.LastTokenTime) < 400*time.Millisecond
}

// ApplyEvent mutates canonical agent state and returns presentation-neutral effects.
func (a *AgentState) ApplyEvent(evt AgentEvent) AgentEffects {
	var effects AgentEffects
	target := a.Messages
	if !a.acceptCompactionEvent(evt) {
		return effects
	}
	if evt.Type == "connection_changed" {
		identity, ok := parseTurnIdentity(evt.Data)
		if !ok || !target.SwitchConnection(identity) {
			return effects
		}
		if a.TurnIdentity.ProviderID != identity.ProviderID || a.TurnIdentity.AccountID != identity.AccountID || a.TurnIdentity.ModelID != identity.ModelID {
			a.ReasoningMode = reasoning.Unknown
		}
		a.TurnIdentity = identity
		return effects
	}
	if evt.Type == "turn_started" {
		identity, ok := parseTurnIdentity(evt.Data)
		if !ok || identity.Generation < a.TurnIdentity.Generation {
			return effects
		}
		if !target.BeginTurn(identity) {
			return effects
		}
		if identity.Generation > a.TurnIdentity.Generation {
			a.terminal, a.Completed, a.Cancelling = false, false, false
		}
		if a.TurnIdentity.ProviderID != identity.ProviderID || a.TurnIdentity.AccountID != identity.AccountID || a.TurnIdentity.ModelID != identity.ModelID {
			a.ReasoningMode = reasoning.Unknown
		}
		a.TurnIdentity = identity
		return effects
	}
	if a.TurnIdentity.Generation > 0 {
		target.BeginTurn(a.TurnIdentity)
	}
	// Events already queued before a cancel request must not reactivate the
	// turn while its terminal done/error event catches up through the UI pump.
	cancelledRecovery := false
	if status, ok := evt.Data.(recovery.Status); ok && evt.Type == "recovery" {
		cancelledRecovery = status.State == recovery.Cancelled
	}
	inactive := a.Cancelling || a.terminal || a.Completed
	// Manual compaction is a separate session operation, not a new model turn.
	// Keep the old turn terminal so late output cannot animate again, but admit
	// the new operation's lifecycle even after that turn completed or stopped.
	manualCompaction := !a.Cancelling && !a.Completed && isManualCompactionEvent(evt)
	if inactive && !manualCompaction && evt.Type != "done" && evt.Type != "error" && evt.Type != "usage" &&
		evt.Type != "stream_token" && evt.Type != "thinking" && evt.Type != "tool_result" &&
		evt.Type != "context_compaction_cancelled" && evt.Type != "context_compaction_failed" &&
		evt.Type != "context_compacted" && evt.Type != "collaboration_message" && evt.Type != "reasoning_classified" && !cancelledRecovery {
		return effects
	}

	switch evt.Type {
	case "collaboration_message":
		event, ok := evt.Data.(collaboration.Event)
		if !ok || event.ID == "" {
			return effects
		}
		attribution := &provider.CollaborationAttribution{
			EventID: event.ID, ThreadID: event.ThreadID, SenderID: event.SenderID,
			SenderName: event.SenderName, Kind: string(event.Kind), RequestID: event.RequestID,
			RequestRevision: event.RequestRevision, RunID: event.RunID,
			Command: command.Clone(event.Command),
		}
		for _, recipient := range event.Recipients {
			attribution.RecipientIDs = append(attribution.RecipientIDs, recipient.ID)
			attribution.RecipientNames = append(attribution.RecipientNames, recipient.Name)
		}
		for _, reference := range event.References {
			if reference.Kind == "report" {
				attribution.ArtifactID, attribution.ArtifactRevision = reference.ID, reference.Revision
				break
			}
		}
		target.AppendMessageIfAbsent(ttypes.ConversationMessage{
			InternalID: event.ID, Role: MessageRoleUser, Origin: provider.OriginPeer,
			Content: event.Body, Collaboration: attribution, Time: event.CreatedAt,
		})

	case "context_compaction_started":
		status := parseCompactionStatus("compaction_preparing", evt.Data)
		a.ActiveCompaction = status
		a.Running = true
		a.Thinking = true
		a.Streaming = false
		a.Cancelling = false
		target.UpdateTurnStatus(status)
		effects.setStatus("Preparing context compaction")
		effects.setThinking(true, false, false)

	case "context_compaction_stage":
		status := parseCompactionStatus("compaction_extracting", evt.Data)
		a.ActiveCompaction = status
		a.Running = true
		a.Thinking = true
		a.Streaming = false
		target.UpdateTurnStatus(status)
		effects.setStatus(status.StageName)
		effects.setThinking(true, false, false)

	case "context_compaction_retrying":
		status := parseCompactionStatus("compaction_retrying", evt.Data)
		a.ActiveCompaction = status
		a.Running = true
		a.Thinking = true
		a.Streaming = false
		target.UpdateTurnStatus(status)
		effects.setStatus("Recovering context compaction")
		effects.setThinking(true, false, false)

	case "context_compaction_committing":
		status := parseCompactionStatus("compaction_committing", evt.Data)
		a.ActiveCompaction = status
		a.Running = true
		a.Thinking = true
		a.Streaming = false
		target.UpdateTurnStatus(status)
		effects.setStatus("Saving compacted context")
		effects.setThinking(true, false, false)

	case "context_compacted":
		status := parseCompactionStatus("compaction_completed", evt.Data)
		target.UpdateTurnStatus(status)
		if status.Trigger == "manual" {
			a.Running = false
			a.Thinking = false
			a.Streaming = false
			effects.setThinking(false, false, false)
		}
		a.Cancelling = false
		a.ActiveCompaction = TurnStatus{}
		effects.setStatus("Context compacted")

	case "context_compaction_cancelled":
		status := parseCompactionStatus("compaction_cancelled", evt.Data)
		target.UpdateTurnStatus(status)
		a.Running = false
		a.Thinking = false
		a.Streaming = false
		a.Cancelling = false
		a.ActiveCompaction = TurnStatus{}
		effects.setStatus("Context compaction cancelled")
		effects.setThinking(false, false, false)

	case "context_compaction_failed":
		status := parseCompactionStatus("compaction_failed", evt.Data)
		target.UpdateTurnStatus(status)
		if values, _ := evt.Data.(map[string]any); values != nil {
			if deferred, _ := values["deferred"].(bool); deferred {
				// The run keeps its full context and continues; the boundary row
				// records why, and nothing stays active or needs attention.
				a.ActiveCompaction = TurnStatus{}
				a.Cancelling = false
				effects.setStatus("Compaction deferred")
				break
			}
		}
		if status.Trigger == "manual" {
			a.Running = false
			a.Thinking = false
			a.Streaming = false
			effects.setThinking(false, false, false)
			a.ActiveCompaction = TurnStatus{}
		} else {
			a.ActiveCompaction = status
		}
		a.Cancelling = false
		effects.setStatus("Compaction failed")

	case "phase":
		status, ok := ParseAgentPhase(evt.Data)
		if ok {
			a.Running = true
			a.Thinking = true
			a.Streaming = false
			if !a.Cancelling {
				switch status.Phase {
				case "preparing", "waiting_provider":
					target.UpdateTurnStatus(status)
				case "executing":
					target.ClearTurnStatus()
				}
			}
			effects.setThinking(true, false, false)
		}

	case "retry":
		if data, ok := evt.Data.(map[string]any); ok && !a.Cancelling {
			reason, _ := data["reason"].(string)
			backoff, _ := data["backoff"].(float64)
			target.UpdateTurnStatus(TurnStatus{Phase: "retrying", Detail: reason, RetryAttempt: int(phaseInt64(data["attempt"])) + 1, RetryLimit: int(phaseInt64(data["limit"])), RetryAt: time.Now().Add(time.Duration(backoff * float64(time.Second)))})
			effects.setStatus("Recovering provider connection")
		}
	case "recovery":
		status, ok := evt.Data.(recovery.Status)
		if !ok || status.ID == "" || status.AgentID != "" && status.AgentID != a.ID ||
			status.Generation > 0 && status.Generation < a.TurnIdentity.Generation {
			return effects
		}
		if a.TokenBuf != "" {
			target.AppendToken(a.TokenBuf)
			a.TokenBuf = ""
		}
		target.MarkAllThinkingInterrupted()
		target.MarkStreamingToolsCancelled()
		target.ClearTurnStatus()
		target.UpdateRecovery(status)
		a.Recovery = status
		a.ActiveCompaction = TurnStatus{}
		a.Thinking, a.Streaming = false, false
		// Waiting for configuration is still an owned, unfinished turn.
		a.Running = status.State != recovery.Interrupted && status.State != recovery.Cancelled
		a.Cancelling, a.Completed = false, false
		effects.setStatus(status.Summary)
		effects.setThinking(a.Running, false, false)
	case "thinking":
		if delta, ok := evt.Data.(reasoning.Delta); ok && delta.Text != "" && delta.Kind != reasoning.Encrypted {
			a.logIfInvalidUTF8("thinking", delta.Text)
			if inactive {
				// Reasoning queued behind Stop or completion is not accepted
				// evidence: it must not open a new thought (and shell pane) per
				// delta or reactivate the stopped turn. The thought already
				// retained keeps its interrupted/settled state.
				break
			}
			target.ClearTurnStatus()
			delta.Text = SanitizeForTerminal(delta.Text)
			target.AppendReasoning(delta)
			if delta.Kind.IsSummary() || delta.Kind.IsRaw() {
				a.ReasoningMode = delta.Kind
			}
			a.Running, a.Thinking = true, true
			effects.setStatus("Thinking")
			effects.setThinking(true, false, false)
			a.LastTokenTime = time.Now()
		}
	case "reasoning_classified":
		if kind, ok := evt.Data.(reasoning.Kind); ok {
			target.SetLatestThinkingKind(kind)
			if kind.IsSummary() || kind.IsRaw() {
				a.ReasoningMode = kind
			}
		}

	case "stream_token":
		if s, ok := evt.Data.(string); ok && s != "" {
			a.logIfInvalidUTF8("stream_token", s)
			s = SanitizeForTerminal(s)
			if !a.Streaming && !inactive {
				target.MarkAllThinkingDone()
			}
			a.TokenBuf += s
			if !inactive {
				a.Running, a.Streaming = true, true
				effects.setStatus("Receiving response")
				effects.setThinking(true, true, false)
			}
			target.EnsureAssistant()
		}

	case "tool_call_streaming":
		a.Running = true
		a.Thinking = true
		finishThinkingBlocks(target)
		if a.Streaming {
			if a.TokenBuf != "" {
				target.AppendToken(a.TokenBuf)
				a.TokenBuf = ""
			}
			a.Streaming = false
		}
		effects.setThinking(true, false, false)
		a.handleToolCallStreaming(evt, target, &effects)
		return effects

	case "tool_call":
		a.Running = true
		a.Thinking = true
		finishThinkingBlocks(target)
		if a.Streaming {
			if a.TokenBuf != "" {
				target.AppendToken(a.TokenBuf)
				a.TokenBuf = ""
			}
			a.Streaming = false
		}
		effects.setThinking(true, false, false)
		a.handleToolCall(evt, target, &effects)
		return effects

	case "tool_progress":
		a.Running = true
		a.Thinking = true
		finishThinkingBlocks(target)
		effects.setThinking(true, false, false)
		a.handleToolProgress(evt, target, &effects)
		return effects

	case "tool_result":
		if !inactive {
			a.Running, a.Thinking = true, true
			finishThinkingBlocks(target)
			effects.setThinking(true, false, false)
		}
		a.handleToolResult(evt, target, &effects)
		return effects

	case "permission_resolved":
		a.Running = true
		a.Thinking = true
		finishThinkingBlocks(target)
		effects.setThinking(true, false, false)
		a.handlePermissionResolved(evt, target, &effects)
		return effects

	case "blocking":
		a.Running = true
		finishThinkingBlocks(target)
		a.handleBlocking(evt, target, &effects)
		return effects

	case "blocking_auto":
		a.handleBlockingAuto(evt, target)
		return effects

	case "blocking_complete":
		blockID := ""
		switch data := evt.Data.(type) {
		case map[string]string:
			blockID = data["block_id"]
		case map[string]any:
			blockID, _ = data["block_id"].(string)
		}
		if blockID != "" && a.PendingBlockID != "" && blockID != a.PendingBlockID {
			return effects
		}
		effects.DismissPrompt, effects.DismissBlockID = true, blockID
		a.PendingBlockID = ""
		a.Running = true
		a.Thinking = true
		effects.setThinking(true, false, false)
		a.BlockingMsgIdx = -1

	case "done":
		a.terminal = true
		a.handleDone(evt, target, &effects)
		return effects

	case "error":
		a.terminal = true
		compactionFailure := a.ActiveCompaction.OperationID != "" && a.ActiveCompaction.Phase == "compaction_failed"
		if a.TokenBuf != "" {
			target.AppendToken(a.TokenBuf)
			a.TokenBuf = ""
		}
		target.MarkAllThinkingInterrupted()
		target.MarkStreamingToolsCancelled()
		errMsg := FormatErr(evt.Data)
		if !compactionFailure {
			target.MarkError(errMsg)
		}
		a.Thinking = false
		a.Streaming = false
		a.Running = false
		a.Cancelling = false
		a.PendingPermTag = ""
		a.PendingPermTool = ""
		a.PendingBlockID = ""
		a.BlockingMsgIdx = -1
		a.ActiveCompaction = TurnStatus{}
		effects.setThinking(false, false, false)
		effects.Error = &AgentErrorResult{AgentID: a.ID, Err: errMsg}
		return effects
	}

	return effects
}

func isManualCompactionEvent(evt AgentEvent) bool {
	switch evt.Type {
	case "context_compaction_started", "context_compaction_stage", "context_compaction_retrying", "context_compaction_committing",
		"context_compacted", "context_compaction_cancelled", "context_compaction_failed":
		return agentEventString(evt.Data, "trigger") == "manual" && agentEventString(evt.Data, "request_id") != ""
	}
	return false
}

// Session operations have their own identity; they must neither inherit the
// previous model turn's terminal fence nor reopen a settled operation on a late
// stage/start publication. Cancellation still shields active progress.
func (a *AgentState) acceptCompactionEvent(evt AgentEvent) bool {
	if !isManualCompactionEvent(evt) {
		return true
	}
	id := agentEventString(evt.Data, "request_id")
	if a.Completed || a.ActiveCompaction.OperationID != "" && a.ActiveCompaction.OperationID != id {
		return false
	}
	if row := a.Messages.activeTurnStatus(id); row != nil {
		switch row.LifecyclePhase {
		case "compaction_completed", "compaction_cancelled", "compaction_failed", "compaction_interrupted":
			return false
		}
	}
	if evt.Type == "context_compaction_started" {
		return !a.Cancelling
	}
	return a.ActiveCompaction.OperationID == id
}

func parseCompactionStatus(phase string, data any) TurnStatus {
	status := TurnStatus{Phase: phase}
	values, ok := data.(map[string]any)
	if !ok {
		return status
	}
	status.BeforeTokens = int(phaseInt64(values["before_tokens"]))
	status.AfterTokens = int(phaseInt64(values["after_tokens"]))
	status.OperationID, _ = values["request_id"].(string)
	status.Trigger, _ = values["trigger"].(string)
	status.StageName, _ = values["stage_label"].(string)
	status.StageIndex = int(phaseInt64(values["stage"]))
	status.Completed = int(phaseInt64(values["completed_stages"]))
	status.StageCount = int(phaseInt64(values["stage_count"]))
	status.RetryAttempt = int(phaseInt64(values["retry"]))
	status.RetryLimit = int(phaseInt64(values["retry_limit"]))
	status.ContextWindow = int(phaseInt64(values["context_window"]))
	status.Detail, _ = values["error"].(string)
	status.AccountID, _ = values["account_id"].(string)
	status.ProviderID, _ = values["provider_id"].(string)
	status.ModelID, _ = values["model_id"].(string)
	if status.StageCount == 0 {
		status.StageCount = 4
	}
	if status.RetryLimit == 0 {
		status.RetryLimit = 3
	}
	return status
}

func parseTurnIdentity(data any) (ttypes.TurnIdentity, bool) {
	identity, ok := data.(ttypes.TurnIdentity)
	if ok {
		return identity, identity.Generation > 0 && identity.ModelID != ""
	}
	values, ok := data.(map[string]any)
	if !ok {
		return ttypes.TurnIdentity{}, false
	}
	identity.AccountID, _ = values["account_id"].(string)
	identity.ProviderID, _ = values["provider_id"].(string)
	identity.ModelID, _ = values["model_id"].(string)
	identity.Generation = uint64(phaseInt64(values["generation"]))
	return identity, identity.Generation > 0 && identity.ModelID != ""
}

func (a *AgentState) SubmitInput(text string) {
	a.Running = true
	a.Cancelling = false
	a.Thinking = true
	a.Streaming = false
	a.TokenBuf = ""
	a.PendingPermTag = ""
	a.PendingPermTool = ""
	a.PendingBlockID = ""
	a.BlockingMsgIdx = -1
	a.Messages.UpdateTurnStatus(TurnStatus{Phase: "preparing"})
}

// BeginCancellation transitions live presentation immediately while the
// backend context shuts down. Terminal acknowledgement still owns final turn
// completion, but queued pre-cancel events can no longer resume animation.
func (a *AgentState) BeginCancellation() {
	if a == nil {
		return
	}
	if a.TokenBuf != "" && a.Messages != nil {
		a.Messages.AppendToken(a.TokenBuf)
		a.TokenBuf = ""
	}
	if a.Messages != nil {
		a.Messages.MarkAllThinkingInterrupted()
		a.Messages.MarkStreamingToolsCancelled()
		if a.ActiveCompaction.OperationID != "" {
			status := a.ActiveCompaction
			status.Phase = "compaction_cancelling"
			status.Detail = ""
			a.Messages.UpdateTurnStatus(status)
		} else {
			a.Messages.UpdateTurnStatus(TurnStatus{Phase: "cancelling"})
		}
	}
	a.Running = false
	a.Thinking = false
	a.Streaming = false
	a.Cancelling = true
	a.PendingPermTag = ""
	a.PendingPermTool = ""
	a.PendingBlockID = ""
	a.BlockingMsgIdx = -1
}

func (a *AgentState) IsActive() bool {
	return a.Running || a.Streaming || a.Thinking
}

// HasCancellableWork reports a live turn, pending prompt or compaction that a
// cancel can stop. A turn already stopping or a finished agent has none, so
// repeated cancels never re-enter the stopping boundary of an idle agent.
// Recovery parked for retry is decided by its own incident (CanRetry).
func (a *AgentState) HasCancellableWork() bool {
	if a == nil || a.Cancelling || a.Completed {
		return false
	}
	return a.IsActive() || a.PendingBlockID != "" || a.ActiveCompaction.OperationID != ""
}

func finishThinkingBlocks(target *Conversation) {
	if target != nil {
		target.MarkAllThinkingDone()
	}
}

// ParseAgentPhase decodes the structured progress phase emitted by the agent.
func ParseAgentPhase(data any) (status TurnStatus, ok bool) {
	switch value := data.(type) {
	case map[string]any:
		status.Phase, _ = value["name"].(string)
		status.Provider, _ = value["provider"].(string)
		switch number := value["progress"].(type) {
		case int:
			status.Progress = number
		case float64:
			status.Progress = int(number)
		}
		status.UploadedBytes = phaseInt64(value["uploaded_bytes"])
		status.TotalBytes = phaseInt64(value["total_bytes"])
		return status, status.Phase != ""
	default:
		return TurnStatus{}, false
	}
}

func phaseInt64(value any) int64 {
	switch number := value.(type) {
	case int:
		return int64(number)
	case int64:
		return number
	case float64:
		return int64(number)
	default:
		return 0
	}
}

func (a *AgentState) logIfInvalidUTF8(eventType, s string) {
	if utf8.ValidString(s) {
		return
	}
	badOff := -1
	badBytes := []byte{}
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		if size == 0 {
			break
		}
		if size == 1 && s[i] < 0x80 {
			i++
			continue
		}
		rn := rune(s[i])
		if rn == utf8.RuneError {
			if badOff == -1 {
				badOff = i
			}
			end := i + 8
			if end > len(s) {
				end = len(s)
			}
			badBytes = append(badBytes, s[i:end]...)
		}
		i += size
	}
	ctx := ""
	if badOff >= 0 {
		start := badOff - 16
		if start < 0 {
			start = 0
		}
		end := badOff + 32
		if end > len(s) {
			end = len(s)
		}
		ctx = s[start:end]
	}
	a.Logger.Warn("UTF8", "invalid_runes_in_event", "event_type", eventType, "agent_id", a.ID, "string_len", len(s), "first_bad_offset", badOff, "bad_bytes_hex", hex.EncodeToString(badBytes), "context", ctx)
}
