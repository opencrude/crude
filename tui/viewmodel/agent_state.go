package viewmodel

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	ttypes "crude/tui/types"
	"crude/tui/widgets"
)

const MainAgentID = "main"

type AgentState struct {
	ID            string
	MsgList       *widgets.MessageList
	Streaming     bool
	Thinking      bool
	TokenBuf      string
	LastTokenTime time.Time
	SpinFrame     int
	ActiveTyping  bool

	ThinkingPaneID string

	// Index of the last blocking question message in the message list,
	// so it can be updated in-place when the user confirms/denies.
	// Set to -1 when no blocking message was appended (new compact-tag mode).
	blockingMsgIdx int

	// pendingPermTag stores a permission result that should be attached to the
	// next tool_call's message as a PermTag suffix. Values: "allowed",
	// "always", "auto". Cleared after being applied.
	pendingPermTag string
	// pendingPermTool stores the tool name that the pending tag applies to,
	// so it's only applied to the correct tool message.
	pendingPermTool string
}

func NewAgentState(id string, msgList *widgets.MessageList) *AgentState {
	return &AgentState{
		ID:      id,
		MsgList: msgList,
	}
}

func (a *AgentState) Tick(spinFrame int) tea.Cmd {
	if a.TokenBuf != "" {
		a.MsgList.AppendToken(a.TokenBuf)
		a.TokenBuf = ""
		a.LastTokenTime = time.Now()
	}
	a.SpinFrame = spinFrame
	a.MsgList.InvalidateCache()
	if a.Streaming || a.Thinking {
		return streamingTickCmd()
	}
	return tickCmd()
}

func (a *AgentState) ComputeActiveTyping() bool {
	return (a.Streaming || a.Thinking) && !a.LastTokenTime.IsZero() && time.Since(a.LastTokenTime) < 400*time.Millisecond
}

func (a *AgentState) HandleEvent(evt ttypes.AgentEventMsg, thinkingTarget *widgets.MessageList, onStatusLine func(string), onThinking func(thinking, streaming, blocking bool), onBlocking func(), onConfirmReset func(question, operation, tool, target, risk, reason, suggestion string), onConfirmDismiss func()) tea.Cmd {
	target := a.MsgList
	thinkTarget := thinkingTarget
	if thinkTarget == nil {
		thinkTarget = target
	}

	switch evt.Type {
	case "phase":
		if phase, ok := evt.Data.(string); ok {
			var statusLine string
			switch phase {
			case "grounding":
				statusLine = "Gathering context"
			case "planning":
				statusLine = "Planning"
			case "executing":
				statusLine = "Executing"
			default:
				statusLine = phase
			}
			onStatusLine(statusLine)
		}

	case "thinking":
		if s, ok := evt.Data.(string); ok && s != "" {
			thinkTarget.AppendThinking(s)
			a.Thinking = true
			a.LastTokenTime = time.Now()
			onThinking(true, false, false)
		}

	case "stream_token":
		if s, ok := evt.Data.(string); ok && s != "" {
			if !a.Streaming {
				target.MarkAllThinkingDone()
				if thinkTarget != target {
					thinkTarget.MarkAllThinkingDone()
				}
			}
			a.TokenBuf += s
			a.Streaming = true
			onThinking(true, true, false)
			msgs := target.Messages()
			if len(msgs) == 0 || msgs[len(msgs)-1].Role != ttypes.RoleAssistant {
				target.AppendMessage(ttypes.ChatMessage{
					Role:    ttypes.RoleAssistant,
					Content: "",
					Time:    time.Now(),
				})
			}
		}

	case "tool_call_streaming":
		if a.Streaming {
			if a.TokenBuf != "" {
				target.AppendToken(a.TokenBuf)
				a.TokenBuf = ""
			}
			target.MarkAllThinkingDone()
			target.EndStreamingCursor()
			if thinkTarget != target {
				thinkTarget.MarkAllThinkingDone()
				thinkTarget.EndStreamingCursor()
			}
			a.Streaming = false
		}
		return a.handleToolCallStreaming(evt, target, onStatusLine)

	case "tool_call":
		if a.Streaming {
			if a.TokenBuf != "" {
				target.AppendToken(a.TokenBuf)
				a.TokenBuf = ""
			}
			target.MarkAllThinkingDone()
			target.EndStreamingCursor()
			if thinkTarget != target {
				thinkTarget.MarkAllThinkingDone()
				thinkTarget.EndStreamingCursor()
			}
			a.Streaming = false
		}
		return a.handleToolCall(evt, target, onStatusLine)

	case "tool_progress":
		return a.handleToolProgress(evt, target, onStatusLine)

	case "tool_result":
		return a.handleToolResult(evt, target, onStatusLine)

	case "blocking":
		return a.handleBlocking(evt, target, thinkTarget, onBlocking, onConfirmReset)

	case "blocking_auto":
		return a.handleBlockingAuto(evt, target, thinkTarget)

	case "blocking_complete":
		onConfirmDismiss()
		onThinking(false, false, false)

	case "done":
		return a.handleDone(evt, target, thinkTarget, onThinking, onStatusLine)

	case "error":
		a.TokenBuf = ""
		target.MarkAllThinkingDone()
		target.ClearStreamingTools()
		if thinkTarget != target {
			thinkTarget.MarkAllThinkingDone()
			thinkTarget.ClearStreamingTools()
		}
		errMsg := formatErr(evt.Data)
		target.AppendMessage(ttypes.ChatMessage{Role: ttypes.RoleError, Content: errMsg, Time: time.Now()})
		a.Thinking = false
		a.Streaming = false
		onThinking(false, false, false)
		target.ScrollToBottom()
		return a.errorResultCmd(errMsg)
	}

	return nil
}

func (a *AgentState) SubmitInput(text string) {
	a.Thinking = true
	a.Streaming = false
	a.TokenBuf = ""
	a.MsgList.ScrollToBottom()
}

func (a *AgentState) IsActive() bool {
	return a.Streaming || a.Thinking
}

type agentDoneResultMsg struct {
	Reason  string
	AgentID string
}

type agentErrorResultMsg struct {
	Err     string
	AgentID string
}

type agentRetryResultMsg struct {
	Msg     string
	AgentID string
}

func (a *AgentState) doneResultCmd(reason string) tea.Cmd {
	return func() tea.Msg {
		return agentDoneResultMsg{Reason: reason, AgentID: a.ID}
	}
}

func (a *AgentState) errorResultCmd(errMsg string) tea.Cmd {
	return func() tea.Msg {
		return agentErrorResultMsg{Err: errMsg, AgentID: a.ID}
	}
}

func (a *AgentState) retryResultCmd(msg string) tea.Cmd {
	return func() tea.Msg {
		return agentRetryResultMsg{Msg: msg, AgentID: a.ID}
	}
}
