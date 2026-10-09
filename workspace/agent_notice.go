package workspace

import "crude/fileio"

// Agent service notices are presentation facts that never enter the semantic
// reducer: they change no conversation and are delivered to every attached
// shell as typed data, so a remote shell cannot misread an untyped payload.
const (
	NoticeResponseContinuationUnavailable = "response_continuation_unavailable"
	NoticeMemorySaved                     = "memory_saved"
	NoticeAsyncCommandStatus              = "async_command_status"
)

// AgentNotice is one typed service notice.
type AgentNotice struct {
	Type    string `json:"type"`
	AgentID string `json:"agent_id,omitempty"`
	// Reason explains an unavailable Responses continuation.
	Reason string `json:"reason,omitempty"`
	// Count and Titles describe memories saved from the session.
	Count  int      `json:"count,omitempty"`
	Titles []string `json:"titles,omitempty"`
	// Commands is the owner's complete background-command snapshot.
	Commands []fileio.AsyncCmdInfo `json:"commands,omitempty"`
}

// NoticeFromEvent recognizes a service notice. It reports false for every
// semantic event, which must enter ApplyAgentEvent instead.
func NoticeFromEvent(evt AgentEvent) (AgentNotice, bool) {
	notice := AgentNotice{Type: evt.Type, AgentID: evt.AgentID}
	switch evt.Type {
	case NoticeResponseContinuationUnavailable:
		data, _ := evt.Data.(map[string]any)
		notice.Reason, _ = data["reason"].(string)
	case NoticeMemorySaved:
		data, _ := evt.Data.(map[string]any)
		notice.Count, _ = data["count"].(int)
		notice.Titles, _ = data["titles"].([]string)
	case NoticeAsyncCommandStatus:
		commands, ok := evt.Data.([]fileio.AsyncCmdInfo)
		if !ok {
			return AgentNotice{}, false
		}
		notice.Commands = append([]fileio.AsyncCmdInfo(nil), commands...)
	default:
		return AgentNotice{}, false
	}
	return notice, true
}
