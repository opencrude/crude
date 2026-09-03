package workspace

import (
	"encoding/hex"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

const IdleTickInterval = 400 * time.Millisecond

func TickCmd() tea.Cmd {
	return tea.Tick(IdleTickInterval, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

func StreamingTickCmd() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// TickMsg is the workspace-owned tick message. The TUI converts it to
// ttypes.TickMsg at the boundary.
type TickMsg time.Time

type AgentDoneResultMsg struct {
	Reason  string
	AgentID string
}

type AgentErrorResultMsg struct {
	Err     string
	AgentID string
}

type AgentRetryResultMsg struct {
	Msg     string
	AgentID string
}

func DoneResultCmd(agentID, reason string) tea.Cmd {
	return func() tea.Msg {
		return AgentDoneResultMsg{Reason: reason, AgentID: agentID}
	}
}

func ErrorResultCmd(agentID, errMsg string) tea.Cmd {
	return func() tea.Msg {
		return AgentErrorResultMsg{Err: errMsg, AgentID: agentID}
	}
}

func RetryResultCmd(agentID, msg string) tea.Cmd {
	return func() tea.Msg {
		return AgentRetryResultMsg{Msg: msg, AgentID: agentID}
	}
}

type AgentState struct {
	ID        string
	Messages  *Conversation
	Streaming bool
	Thinking  bool
	// Running remains true across provider waits, permission prompts, and tool
	// execution. Streaming/Thinking describe output shape; Running owns the
	// turn lifecycle and prevents tool gaps from being reported as idle.
	Running       bool
	Cancelling    bool
	TokenBuf      string
	LastTokenTime time.Time
	SpinFrame     int
	ActiveTyping  bool
	Completed     bool
	Status        string
	Logger        Logger

	ThinkingPaneID string

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
	SafetyAlert     string
	SafetyRisk      string
}

func NewAgentState(id string, messages *Conversation, lg Logger) *AgentState {
	if messages == nil {
		messages = NewConversation()
	}
	return &AgentState{
		ID:       id,
		Messages: messages,
		Logger:   lg,
	}
}

func (a *AgentState) Tick(spinFrame int) tea.Cmd {
	if a.Messages != nil {
		a.Messages.AdvanceToolLifecycles(time.Now())
	}
	if a.TokenBuf != "" {
		a.Messages.AppendToken(a.TokenBuf)
		a.TokenBuf = ""
		a.LastTokenTime = time.Now()
	}
	a.SpinFrame = spinFrame
	if a.Streaming || a.Thinking || (a.Messages != nil && a.Messages.HasPendingToolLifecycles()) {
		return StreamingTickCmd()
	}
	return TickCmd()
}

func (a *AgentState) ComputeActiveTyping() bool {
	return (a.Streaming || a.Thinking) && !a.LastTokenTime.IsZero() && time.Since(a.LastTokenTime) < 400*time.Millisecond
}

func (a *AgentState) HandleEvent(evt AgentEvent, thinkingTarget *Conversation, onStatusLine func(string), onThinking func(thinking, streaming, blocking bool), onBlocking func(), onConfirmReset func(question, operation, tool, target, risk, reason, suggestion string), onQuestionReset func(questionsJSON string), onConfirmDismiss func()) tea.Cmd {
	target := a.Messages
	thinkTarget := thinkingTarget
	if thinkTarget == nil {
		thinkTarget = target
	}
	// Events already queued before a cancel request must not reactivate the
	// turn while its terminal done/error event catches up through the UI pump.
	if a.Cancelling && evt.Type != "done" && evt.Type != "error" && evt.Type != "usage" {
		return nil
	}

	switch evt.Type {
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
				}
			}
			onThinking(true, false, false)
		}

	case "thinking":
		if s, ok := evt.Data.(string); ok && s != "" {
			a.logIfInvalidUTF8("thinking", s)
			target.ClearTurnStatus()
			if thinkTarget != target {
				thinkTarget.ClearTurnStatus()
			}
			thinkTarget.AppendThinking(SanitizeForTerminal(s))
			a.Running = true
			a.Thinking = true
			a.LastTokenTime = time.Now()
			onStatusLine("Thinking")
			onThinking(true, false, false)
		}

	case "stream_token":
		if s, ok := evt.Data.(string); ok && s != "" {
			a.logIfInvalidUTF8("stream_token", s)
			s = SanitizeForTerminal(s)
			if !a.Streaming {
				target.MarkAllThinkingDone()
				if thinkTarget != target {
					thinkTarget.MarkAllThinkingDone()
				}
			}
			a.TokenBuf += s
			a.Running = true
			a.Streaming = true
			onStatusLine("Receiving response")
			onThinking(true, true, false)
			target.EnsureAssistant()
		}

	case "tool_call_streaming":
		a.Running = true
		a.Thinking = true
		finishThinkingBlocks(target, thinkTarget)
		if a.Streaming {
			if a.TokenBuf != "" {
				target.AppendToken(a.TokenBuf)
				a.TokenBuf = ""
			}
			a.Streaming = false
		}
		onThinking(true, false, false)
		return a.handleToolCallStreaming(evt, target, onStatusLine)

	case "tool_call":
		a.Running = true
		a.Thinking = true
		finishThinkingBlocks(target, thinkTarget)
		if a.Streaming {
			if a.TokenBuf != "" {
				target.AppendToken(a.TokenBuf)
				a.TokenBuf = ""
			}
			a.Streaming = false
		}
		onThinking(true, false, false)
		return a.handleToolCall(evt, target, onStatusLine)

	case "tool_progress":
		a.Running = true
		a.Thinking = true
		finishThinkingBlocks(target, thinkTarget)
		onThinking(true, false, false)
		return a.handleToolProgress(evt, target, onStatusLine)

	case "tool_result":
		a.Running = true
		a.Thinking = true
		finishThinkingBlocks(target, thinkTarget)
		onThinking(true, false, false)
		return a.handleToolResult(evt, target, onStatusLine)

	case "permission_resolved":
		a.Running = true
		a.Thinking = true
		finishThinkingBlocks(target, thinkTarget)
		onThinking(true, false, false)
		return a.handlePermissionResolved(evt, target, onStatusLine)

	case "blocking":
		a.Running = true
		finishThinkingBlocks(target, thinkTarget)
		return a.handleBlocking(evt, target, thinkTarget, onBlocking, onConfirmReset, onQuestionReset)

	case "blocking_auto":
		return a.handleBlockingAuto(evt, target, thinkTarget)

	case "blocking_complete":
		onConfirmDismiss()
		a.PendingBlockID = ""
		a.Running = true
		a.Thinking = true
		onThinking(true, false, false)
		a.BlockingMsgIdx = -1

	case "safety_alert":
		if data, ok := evt.Data.(map[string]any); ok {
			a.SafetyRisk, _ = data["risk"].(string)
			a.SafetyAlert, _ = data["reason"].(string)
		}

	case "done":
		return a.handleDone(evt, target, thinkTarget, onThinking, onStatusLine)

	case "error":
		if a.TokenBuf != "" {
			target.AppendToken(a.TokenBuf)
			a.TokenBuf = ""
		}
		target.MarkAllThinkingInterrupted()
		target.MarkStreamingToolsCancelled()
		if thinkTarget != target {
			thinkTarget.MarkAllThinkingInterrupted()
			thinkTarget.MarkStreamingToolsCancelled()
		}
		errMsg := FormatErr(evt.Data)
		target.MarkError(errMsg)
		a.Thinking = false
		a.Streaming = false
		a.Running = false
		a.Cancelling = false
		a.PendingPermTag = ""
		a.PendingPermTool = ""
		a.PendingBlockID = ""
		a.BlockingMsgIdx = -1
		onThinking(false, false, false)
		return ErrorResultCmd(a.ID, errMsg)
	}

	return nil
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
	a.SafetyAlert, a.SafetyRisk = "", ""
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
		a.Messages.UpdateTurnStatus(TurnStatus{Phase: "cancelling"})
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

func finishThinkingBlocks(target, thinkTarget *Conversation) {
	if target != nil {
		target.MarkAllThinkingDone()
	}
	if thinkTarget != nil && thinkTarget != target {
		thinkTarget.MarkAllThinkingDone()
	}
}

// ParseAgentPhase decodes both legacy string phases and structured progress
// phases emitted by the request transport.
func ParseAgentPhase(data any) (status TurnStatus, ok bool) {
	switch value := data.(type) {
	case string:
		status.Phase = value
		return status, value != ""
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
