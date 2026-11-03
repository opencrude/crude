package workspace

import (
	"crypto/sha256"
	"fmt"

	"crude/fileio"
	"crude/types"
)

// Agent service notices change no conversation. Canonical ingress records their
// resource status before delivering typed presentation facts to attached shells.
const (
	NoticeResponseContinuationUnavailable = "response_continuation_unavailable"
	NoticeMemorySaved                     = "memory_saved"
	NoticeAsyncCommandStatus              = "async_command_status"
	NoticeAttachmentExcluded              = "attachment_excluded"
)

// AgentNotice is one typed service notice.
type AgentNotice struct {
	Type    string `json:"type"`
	AgentID string `json:"agent_id,omitempty"`
	// Identity preserves exact source-event identity when the producer has one.
	// Bare service events are admitted once by the canonical actor, not shells.
	Identity types.RuntimeEventIdentity `json:"identity,omitempty"`
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
	notice := AgentNotice{Type: evt.Type, AgentID: evt.AgentID, Identity: evt.Identity}
	switch evt.Type {
	case NoticeResponseContinuationUnavailable, NoticeAttachmentExcluded:
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

// ApplyAgentNotice records resource facts once at the serialized workspace
// ingress. Shells only project the notice; neither attachment nor repaint owns
// command operations or failure notifications.
func (w *Workspace) ApplyAgentNotice(notice AgentNotice) {
	if w == nil || w.Status == nil {
		return
	}
	owner := types.ResourceRef{Kind: types.ResourceWorkspace, ID: w.RootPath}
	var node *AgentNode
	if notice.AgentID != "" {
		if w.AgentMgr == nil {
			return
		}
		node = w.AgentMgr.Get(notice.AgentID)
		session := w.SessionForAgent(notice.AgentID)
		if node == nil || session == nil {
			return
		}
		owner = types.ResourceRef{Kind: types.ResourceAgent, ID: notice.AgentID, SessionID: session.SessionID}
		if node.Parent != nil {
			owner.ParentID = node.Parent.ID
		}
	}
	if notice.Identity != (types.RuntimeEventIdentity{}) &&
		(!notice.Identity.Valid() || notice.Identity.AgentID != notice.AgentID ||
			!w.agentEventOwnerMatches(node, nil, notice.Identity)) {
		return
	}
	n := types.NotificationRecord{Owner: owner}
	switch notice.Type {
	case NoticeAsyncCommandStatus:
		if node != nil {
			w.Status.recordBackgroundCommands(owner, notice.Commands)
		}
		return
	case NoticeAttachmentExcluded:
		n.DedupKey = "attachment:" + notice.AgentID + ":" + notice.Reason
		n.Severity, n.Source, n.Summary, n.Detail = types.SeverityWarning, "attachment", notice.Reason, notice.Reason
	case NoticeResponseContinuationUnavailable:
		n.Summary = "Responses delta unavailable: the endpoint returned no response ID; full uploads continue"
		if notice.Reason == "response_not_stored" {
			n.Summary = "Responses delta unavailable: the endpoint declined server storage; full uploads continue"
		}
		n.Source, n.Read, n.Transient = "feedback", true, true
	case NoticeMemorySaved:
		if notice.Count <= 0 {
			return
		}
		n.Summary = fmt.Sprintf("Memory: saved %d from this session", notice.Count)
		if len(notice.Titles) > 0 {
			n.Summary = fmt.Sprintf("Memory: saved %q", notice.Titles[0])
			if notice.Count > 1 {
				n.Summary += fmt.Sprintf(" +%d more", notice.Count-1)
			}
		}
		n.Source, n.Read, n.Transient = "feedback", true, true
	default:
		return
	}
	if n.DedupKey == "" {
		n.DedupKey = n.Source + ":" + string(owner.Kind) + ":" + owner.ID + ":" + n.Summary
	}
	if notice.Identity.Valid() {
		n.ID = "agent-notice:" + notice.Identity.EventID
		w.Status.notifyOnce(n)
	} else {
		w.Status.Notify(n)
	}
}

func backgroundCommandID(owner types.ResourceRef, command fileio.AsyncCmdInfo) string {
	// Quote variable-length fields and normalize time to its instant: local
	// timezone and monotonic-clock metadata are not execution identity.
	key := fmt.Sprintf("%q:%q:%q:%d:%d", owner.ID, owner.SessionID, command.Handle, command.PID, command.StartedAt.UnixNano())
	return fmt.Sprintf("command:%x", sha256.Sum256([]byte(key)))
}

func (c *StatusCenter) recordBackgroundCommands(owner types.ResourceRef, commands []fileio.AsyncCmdInfo) {
	c.commandMu.Lock()
	defer c.commandMu.Unlock()
	previous := c.backgroundCommands[owner.ID]
	current := make(map[string]types.OperationSnapshot, len(commands))
	for _, command := range commands {
		if command.Handle == "" || command.PID <= 0 || command.StartedAt.IsZero() {
			continue
		}
		state := types.OperationRunning
		switch command.Status {
		case fileio.AsyncRunning:
		case fileio.AsyncCompleted:
			state = types.OperationSucceeded
		case fileio.AsyncFailed:
			state = types.OperationFailed
		case fileio.AsyncKilled:
			state = types.OperationCancelled
		default:
			continue
		}
		id := backgroundCommandID(owner, command)
		op := types.OperationSnapshot{
			ID: id, Owner: owner, Kind: "background-command", Label: command.Command,
			State: state, StartedAt: command.StartedAt, EndedAt: command.EndedAt,
			Error: command.Error, OutputRef: command.Handle, Cancellable: state == types.OperationRunning,
		}
		old, observed := previous[id]
		if observed && (operationTerminal(old.State) || operationEquivalent(old, op)) {
			current[id] = old
			continue
		}
		current[id] = op
		c.SetOperation(op)
		if state == types.OperationFailed {
			c.notifyOnce(types.NotificationRecord{
				ID: id + ":failed", DedupKey: id + ":failed", Owner: owner,
				Severity: types.SeverityError, Source: "background-command",
				Summary: "Background command failed", Detail: command.Command + ": " + command.Error,
				Actions: []types.StatusAction{{ID: "status.switch", Label: "Inspect agent", Enabled: true}},
			})
		}
	}
	if len(current) == 0 {
		delete(c.backgroundCommands, owner.ID)
		return
	}
	if c.backgroundCommands == nil {
		c.backgroundCommands = make(map[string]map[string]types.OperationSnapshot)
	}
	c.backgroundCommands[owner.ID] = current
}
