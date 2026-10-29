package workspace

import (
	"fmt"
	"slices"
	"time"

	"crude/provider"
	"crude/types"
	"crude/types/attachment"
	"crude/types/reasoning"
	"crude/types/recovery"
)

// AgentReduction describes a committed change, not another event to reduce.
// Callers own presentation scheduling. No mutable runtime pointer escapes here.
type AgentReduction struct {
	Accepted       bool
	Revision       uint64
	AgentID        string
	Added          bool
	ChildCompleted bool
	// EvidenceOnly marks settlement of existing evidence (collaboration
	// delivery). It is not inventory/navigation and must never activate or
	// hydrate a draft in any shell.
	EvidenceOnly  bool
	ChangedAgents []string
	Effects       AgentEffects
}

// RegisterAgentRoot establishes inventory independently of any rendered shell.
// Workspace lifecycle and all methods in this file require serialized callers.
func (w *Workspace) RegisterAgentRoot(id string) *AgentNode {
	if w == nil || w.AgentMgr == nil || id == "" {
		return nil
	}
	node := w.AgentMgr.Get(id)
	if node != nil && node.State != nil {
		return node
	}
	conversation := NewConversation()
	if w.PaneMgr != nil {
		conversation = w.PaneMgr.GetOrCreate(id, PaneAgent, id).Messages
	}
	state := NewAgentState(id, conversation, w.Logger)
	if node == nil {
		node = w.AgentMgr.AddOrchestrator(id, state)
	} else {
		node.State = state
	}
	node.Category, node.Model, node.Label = "default", w.InitialModelName, w.AgentNameFor(id)
	if owner := w.SessionForAgent(id); owner != nil {
		owner.AgentState = state
		if owner.Settings != nil {
			node.Model = owner.Settings.Effective.Model
		}
	}
	if pane := w.agentPane(id); pane != nil {
		pane.Title = node.Label
	}
	w.agentRevision++
	w.SyncAgentStatusInventory()
	return node
}

// ApplyAgentEvent is the single semantic ingress for already-owned backend
// events. It never uses focus, terminal width, a renderer or a client's clock.
// Identity-free service events are admitted only for existing owners, except
// explicit child registration under an existing parent.
func (w *Workspace) ApplyAgentEvent(evt AgentEvent) AgentReduction {
	result := AgentReduction{AgentID: evt.AgentID}
	if w == nil || w.AgentMgr == nil || evt.AgentID == "" || w.StaleAgentUpload(evt) {
		return result
	}
	data, _ := evt.Data.(map[string]string)
	if (evt.Type == "sub_agent_started" || evt.Type == "sub_agent_completed") &&
		(data == nil || data["agent_id"] != evt.AgentID) {
		return result
	}
	node := w.AgentMgr.Get(evt.AgentID)
	parent := w.AgentMgr.Get(data["parent_id"])
	structuralStart := evt.Type == "sub_agent_started" || evt.Type == "start" && parent != nil
	if structuralStart && node != nil && (node.Parent == nil || node.Parent != parent) {
		return result
	}
	identified := evt.Identity != (types.RuntimeEventIdentity{})
	if identified && (!evt.Identity.Valid() || evt.Identity.AgentID != evt.AgentID ||
		!w.agentEventOwnerMatches(node, parent, evt.Identity)) {
		return result
	}
	if node == nil {
		if !structuralStart || parent == nil || data["parent_id"] == evt.AgentID {
			return result
		}
		label := data["agent_name"]
		if label == "" {
			label = evt.AgentID
		}
		conversation := NewConversation()
		if w.PaneMgr != nil {
			pane := w.PaneMgr.GetOrCreate(evt.AgentID, PaneAgent, SubAgentActivityLabel(label, data["category"], ""))
			conversation, pane.Status = pane.Messages, "running"
		}
		state := NewAgentState(evt.AgentID, conversation, w.Logger)
		node = w.AgentMgr.AddSubAgent(evt.AgentID, parent.ID, label, data["model"], data["category"], state)
		if assignment := data["prompt"]; assignment != "" {
			conversation.AppendMessage(Message{Role: MessageRoleAssignment, Content: assignment, Time: time.Now()})
		}
		result.Added = true
		if parent.State != nil && parent.State.Messages != nil &&
			parent.State.Messages.AttachSubPaneToTool(data["parent_tool_id"], node.ID, SubAgentActivityLabel(label, data["category"], data["task"])) {
			result.ChangedAgents = append(result.ChangedAgents, parent.ID)
		}
	}
	if node.State == nil {
		if node.Parent != nil {
			return result
		}
		w.RegisterAgentRoot(node.ID)
	}
	state := node.State
	if state.applySessionFact(evt) {
		w.agentRevision++
		result.ChangedAgents = append(result.ChangedAgents, node.ID)
		result.Accepted, result.Revision = true, w.agentRevision
		return result
	}
	if !state.acceptCompactionEvent(evt) {
		return result
	}
	if identified && !state.AcceptRuntimeEvent(evt.Identity) {
		return result
	}
	if state.Completed {
		semantic := identified && (evt.Type == "tool_result" || evt.Type == "stream_token" || evt.Type == "thinking" || evt.Type == "reasoning_classified" || evt.Type == "usage")
		turn, valid := parseTurnIdentity(evt.Data)
		fresh := evt.Type == "turn_started" && valid && turn.Generation > state.TurnIdentity.Generation
		if !semantic && !fresh && !state.newerTurnRecovery(evt) {
			return result
		}
	}
	if evt.Type == "blocking_complete" && state.PendingBlockID != "" {
		block := agentEventString(evt.Data, "block_id")
		if block != "" && block != state.PendingBlockID {
			return result
		}
	}
	switch evt.Type {
	case "sub_agent_started", "start":
		if !structuralStart {
			result.Effects = state.ApplyEvent(evt)
			break
		}
		if node.Parent == nil || parent != nil && node.Parent != parent {
			return result
		}
		if data["agent_name"] != "" {
			node.Label = data["agent_name"]
		}
		if data["model"] != "" {
			node.Model = data["model"]
		}
		if data["category"] != "" {
			node.Category = data["category"]
		}
		state.Status = "running"
		if pane := w.agentPane(node.ID); pane != nil {
			pane.Title, pane.Status = SubAgentActivityLabel(node.Label, node.Category, ""), "running"
		}
	case "sub_agent_completed":
		if node.Parent == nil || data["status"] == "" || data["status"] == "running" {
			return result
		}
		state.Status = data["status"]
		if state.Status == "completed" {
			state.Messages.MarkAllThinkingDone()
		} else {
			state.Messages.MarkAllThinkingInterrupted()
			state.Messages.MarkStreamingToolsCancelled()
		}
		state.Completed, state.terminal = true, true
		state.Running, state.Thinking, state.Streaming, state.Cancelling = false, false, false, false
		state.PendingBlockID = ""
		result.ChildCompleted = true
		result.Effects.DismissPrompt = true
		if pane := w.agentPane(node.ID); pane != nil {
			pane.Status = state.Status
			pane.Collapse()
		}
	case "connection_changed":
		if identity, ok := parseTurnIdentity(evt.Data); ok && identity.ModelID != "" {
			node.Model = identity.ModelID
		}
		result.Effects = state.ApplyEvent(evt)
	case "input_queued":
		data, _ := evt.Data.(map[string]any)
		id, _ := data["input_id"].(string)
		text, _ := data["text"].(string)
		display, _ := data["display_text"].(string)
		state.Messages.AppendMessageIfAbsent(queuedInputMessage(id, text, display, time.Now()))
		result.EvidenceOnly = true
	case "queued_input_admitted":
		state.Messages.ConsumePendingSteering(agentEventString(evt.Data, "input_id"))
		result.EvidenceOnly = true
	case "collaboration_delivery_admitted":
		state.Messages.ConsumePendingSteering(agentEventString(evt.Data, "history_message_id"))
		result.EvidenceOnly = true
	case "collaboration_delivery_rejected":
		state.Messages.SettlePendingSteeringMessages([]string{agentEventString(evt.Data, "history_message_id")})
		result.EvidenceOnly = true
	case "sub_agent_question":
		if node.Parent == nil || node.Parent.State == nil || data["question"] == "" {
			return result
		}
		node.Parent.State.Messages.AppendMessage(Message{Role: MessageRoleTool,
			Content: fmt.Sprintf("[%s:%s] asks: %s", node.Category, node.Label, data["question"]), Time: time.Now()})
		result.ChangedAgents = append(result.ChangedAgents, node.Parent.ID)
	default:
		result.Effects = state.ApplyEvent(evt)
	}
	if prompt := result.Effects.Prompt; prompt != nil {
		if w.agentPrompts == nil {
			w.agentPrompts = make(map[string]AgentPrompt)
		}
		w.agentPrompts[node.ID] = *prompt
	}
	if prompt, ok := w.agentPrompts[node.ID]; ok && prompt.BlockID != state.PendingBlockID {
		delete(w.agentPrompts, node.ID)
	}
	if evt.Type == "usage" {
		if usage, ok := w.RecordAgentUsage(node.ID, evt.Data); ok {
			if pane := w.agentPane(node.ID); pane != nil {
				pane.TokenCount = int(usage.InputTotal.Value + usage.Output.Value)
			}
		}
	}
	if result.Effects.Done != nil || result.Effects.Error != nil {
		if pane := w.agentPane(node.ID); pane != nil {
			pane.Collapse()
		}
	}
	w.ApplyAgentStatusEvent(evt)
	result.ChangedAgents = append(result.ChangedAgents, node.ID)
	w.agentRevision++
	result.Accepted, result.Revision = true, w.agentRevision
	return result
}

func (w *Workspace) agentEventOwnerMatches(node, parent *AgentNode, id types.RuntimeEventIdentity) bool {
	if node != nil && node.Parent != nil || node == nil && parent != nil {
		return id.OwnerID == id.AgentID // child session identity is allocated with its exact agent ID
	}
	if node == nil {
		return false
	}
	owner := w.SessionForAgent(node.ID)
	return owner != nil && owner.SessionID != "" && id.OwnerID == owner.SessionID
}

func agentEventString(data any, key string) string {
	switch values := data.(type) {
	case map[string]string:
		return values[key]
	case map[string]any:
		value, _ := values[key].(string)
		return value
	default:
		return ""
	}
}

func (w *Workspace) agentPane(id string) *SubPane {
	if w.PaneMgr == nil {
		return nil
	}
	return w.PaneMgr.Get(id)
}

// AdvanceAgentState advances domain-owned lifecycle transitions. A consumer may
// choose any presentation cadence without governing accepted semantic evidence.
func (w *Workspace) AdvanceAgentState(now time.Time) AgentReduction {
	var result AgentReduction
	if w == nil || w.AgentMgr == nil {
		return result
	}
	for _, node := range w.AgentMgr.AllNodes() {
		if node.State == nil || node.State.Messages == nil {
			continue
		}
		before := node.State.Messages.Version()
		node.State.Messages.AdvanceToolLifecycles(now)
		if before != node.State.Messages.Version() {
			result.ChangedAgents = append(result.ChangedAgents, node.ID)
		}
	}
	if len(result.ChangedAgents) != 0 {
		w.agentRevision++
		result.Accepted, result.Revision = true, w.agentRevision
	}
	return result
}

// BeginAgentCancellation publishes the stopping boundary independently of a
// shell. Execution cancellation is separately routed to the exact backend.
func (w *Workspace) BeginAgentCancellation(id string) bool {
	if w == nil || w.AgentMgr == nil {
		return false
	}
	node := w.AgentMgr.Get(id)
	if node == nil || node.State == nil {
		return false
	}
	node.State.BeginCancellation()
	delete(w.agentPrompts, id)
	w.agentRevision++
	w.SyncAgentStatusInventory()
	return true
}

// RejectAgentRecoveryCancellation restores only the incident whose Stop was
// rejected. A replaced recovery or newer turn is never changed by a late reply.
func (w *Workspace) RejectAgentRecoveryCancellation(id, incident string) {
	if w == nil || w.AgentMgr == nil {
		return
	}
	if node := w.AgentMgr.Get(id); node != nil && node.State != nil && node.State.RecoveryStatus().ID == incident {
		node.State.Cancelling = false
		w.agentRevision++
		w.SyncAgentStatusInventory()
	}
}

// AppendAgentMessages admits neutral evidence and optionally normalizes a cold
// stopped transcript. UI projection helpers cannot mutate Conversation directly.
func (w *Workspace) AppendAgentMessages(id string, messages []Message, stopped bool) bool {
	if w == nil || w.AgentMgr == nil || len(messages) == 0 {
		return false
	}
	node := w.AgentMgr.Get(id)
	if node == nil || node.State == nil || node.State.Messages == nil {
		return false
	}
	for _, message := range messages {
		node.State.Messages.AppendMessage(message)
	}
	if stopped {
		node.State.Messages.MarkStreamingToolsCancelled()
		node.State.Messages.MarkAllThinkingInterrupted()
	}
	w.agentRevision++
	return true
}

// ForgetAgentInventory removes a terminated subtree from the semantic owner.
// Call only after CloseAgent has routed cancellation with its ownership intact.
func (w *Workspace) ForgetAgentInventory(id string) {
	if w == nil || w.AgentMgr == nil {
		return
	}
	var visit func(*AgentNode)
	visit = func(node *AgentNode) {
		if node == nil {
			return
		}
		delete(w.agentPrompts, node.ID)
		if w.PaneMgr != nil {
			w.PaneMgr.Remove(node.ID)
		}
		if w.Status != nil {
			w.Status.RemoveAgent(node.ID)
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(w.AgentMgr.Get(id))
	w.AgentMgr.Remove(id)
	w.agentRevision++
}

// AgentSnapshot is detached semantic data; it includes no selected/hidden tab,
// width-dependent pane, service handle, logger or producer-owned mutable value.
type AgentSnapshot struct {
	ID, ParentID, SessionID, Label, Model, Category     string
	Streaming, Thinking, Running, Cancelling, Completed bool
	Status                                              string
	TurnIdentity                                        types.TurnIdentity
	RuntimeIdentity                                     types.RuntimeEventIdentity
	ReasoningMode                                       reasoning.Kind
	Recovery                                            recovery.Status
	Yolo                                                bool
	PlanMode                                            bool
	// Goal is the main agent's session goal; children have none.
	Goal                 *types.Goal
	Loops                []types.Loop
	ActiveCompaction     TurnStatus
	LastTokenTime        time.Time
	Messages             []Message
	ConversationRevision uint64
	Prompt               *AgentPrompt
	Usage                types.UsageSummary
	Activity             types.AgentActivityView
}

type AgentWorkspaceSnapshot struct {
	Revision uint64
	Agents   []AgentSnapshot
}

func (w *Workspace) SnapshotAgents() AgentWorkspaceSnapshot {
	return w.snapshotAgents(true)
}

// AgentStateSnapshots omits transcript bodies for incremental subscriptions.
// Conversation.ChangesSince publishes only the records each observer needs.
func (w *Workspace) AgentStateSnapshots() AgentWorkspaceSnapshot {
	return w.snapshotAgents(false)
}

func (w *Workspace) snapshotAgents(includeMessages bool) AgentWorkspaceSnapshot {
	var result AgentWorkspaceSnapshot
	if w == nil || w.AgentMgr == nil {
		return result
	}
	result.Revision = w.agentRevision
	for _, node := range w.AgentMgr.AllNodes() {
		if node.State != nil {
			result.Agents = append(result.Agents, w.snapshotAgent(node, includeMessages))
		}
	}
	return result
}

// AgentStateSnapshot publishes one changed agent without scanning inventory or
// exporting transcript bodies. Calls are serialized with semantic ingress.
func (w *Workspace) AgentStateSnapshot(id string) (AgentSnapshot, bool) {
	if w == nil || w.AgentMgr == nil {
		return AgentSnapshot{}, false
	}
	node := w.AgentMgr.Get(id)
	if node == nil || node.State == nil {
		return AgentSnapshot{}, false
	}
	return w.snapshotAgent(node, false), true
}

func (w *Workspace) snapshotAgent(node *AgentNode, includeMessages bool) AgentSnapshot {
	state := node.State
	snapshot := AgentSnapshot{ID: node.ID, Label: node.Label, Model: node.Model, Category: node.Category,
		Streaming: state.Streaming, Thinking: state.Thinking, Running: state.Running, Cancelling: state.Cancelling,
		Completed: state.Completed, Status: state.Status, TurnIdentity: state.TurnIdentity,
		RuntimeIdentity: state.runtimeIdentity, ReasoningMode: state.ReasoningMode, Recovery: state.RecoveryStatus(),
		LastTokenTime: state.LastTokenTime, ConversationRevision: state.Messages.Version(),
		Activity: w.AgentActivity(node.ID), Yolo: w.YoloForAgent(node.ID), PlanMode: w.PlanModeForAgent(node.ID), ActiveCompaction: state.ActiveCompaction}
	if goal, ok := w.GoalForAgent(node.ID); ok {
		snapshot.Goal = &goal
	}
	snapshot.Loops = w.LoopsForAgent(node.ID)
	if includeMessages {
		snapshot.Messages = state.Messages.Messages()
	}
	if node.Parent != nil {
		snapshot.ParentID = node.Parent.ID
	}
	if owner := w.SessionForAgent(node.ID); owner != nil {
		snapshot.SessionID = owner.SessionID
	}
	if prompt, ok := w.agentPrompts[node.ID]; ok && prompt.BlockID == state.PendingBlockID {
		snapshot.Prompt = &prompt
	}
	if snapshot.Activity.Plan != nil {
		plan := *snapshot.Activity.Plan
		plan.Steps = slices.Clone(plan.Steps)
		plan.Current = slices.Clone(plan.Current)
		if plan.Next != nil {
			next := *plan.Next
			plan.Next = &next
		}
		snapshot.Activity.Plan = &plan
	}
	snapshot.Usage, _ = w.UsageSummary(node.ID, types.UsageThisAgent)
	snapshot.Usage.Models = slices.Clone(snapshot.Usage.Models)
	return snapshot
}

// ApplyAgentInputAdmission commits the visible accepted input on the same owner
// as runtime output. Only the originating client consumes its draft afterward.
func (w *Workspace) ApplyAgentInputAdmission(intent *InputAdmissionIntent, result InputAdmissionResult, input provider.UserInput, labels []string) bool {
	if w != nil && w.AgentMgr != nil && intent != nil && result.Status == InputAdmissionQueued {
		receipt := result.Receipt
		node := w.AgentMgr.Get(intent.AgentID)
		owner := w.SessionForAgent(intent.AgentID)
		if receipt.InputID != intent.InputID || receipt.AgentID != intent.AgentID || receipt.SessionID != intent.SessionID ||
			receipt.OwnerEpoch != intent.OwnerEpoch || receipt.DraftRevision != intent.DraftRevision || receipt.AcceptedAt.IsZero() ||
			node == nil || node.State == nil || owner == nil || owner.SessionID != receipt.SessionID {
			return false
		}
		message := queuedInputMessage(receipt.InputID, input.Text, input.DisplayText, receipt.AcceptedAt)
		message.ReferenceLabels = labels
		message.Attachments = inputAttachmentTargets(owner, input)
		if node.State.Messages.AppendMessageIfAbsent(message) {
			w.agentRevision++
		}
		return true
	}
	if w == nil || w.AgentMgr == nil || intent == nil ||
		(result.Status != InputAdmissionAccepted && result.Status != InputAdmissionPreservationPending) {
		return false
	}
	receipt := result.Receipt
	if receipt.InputID != intent.InputID || receipt.AgentID != intent.AgentID || receipt.SessionID != intent.SessionID ||
		receipt.OwnerEpoch != intent.OwnerEpoch || receipt.TurnGeneration != intent.TurnGeneration ||
		receipt.DraftRevision != intent.DraftRevision || receipt.ContextRevision == 0 || receipt.AcceptedAt.IsZero() {
		return false
	}
	node := w.AgentMgr.Get(intent.AgentID)
	owner := w.SessionForAgent(intent.AgentID)
	if node == nil || node.State == nil || owner == nil || owner.SessionID != receipt.SessionID {
		return false
	}
	state := node.State
	if !state.Messages.AppendMessageIfAbsent(Message{InternalID: receipt.InputID, Role: MessageRoleUser,
		Origin: input.EffectiveOrigin(), Content: input.Text, ReferenceLabels: labels, Attachments: inputAttachmentTargets(owner, input), Time: receipt.AcceptedAt}) {
		return true
	}
	if result.Status == InputAdmissionAccepted && !state.Cancelling && !state.IsActive() && state.TurnIdentity.Generation < receipt.TurnGeneration {
		state.SubmitInput(input.Text)
	}
	w.agentRevision++
	w.SyncAgentStatusInventory()
	return true
}

func inputAttachmentTargets(owner *Session, input provider.UserInput) []attachment.Target {
	if owner == nil || owner.RefStore == nil {
		return nil
	}
	var selected []attachment.Target
	for _, id := range input.ReferenceIDs {
		if ref, ok := owner.RefStore.GetReference(id); ok && ref.Active() && ref.SessionID == owner.SessionID {
			selected = append(selected, attachment.Target{ReferenceID: ref.ID, Label: ref.DisplayName()})
		}
	}
	return attachment.Locate(input.Text, selected)
}

// queuedInputMessage is the pending row for a prompt waiting in the durable
// queue. Admission consumes the pending mark in place, keeping one identity.
func queuedInputMessage(id, text, display string, at time.Time) Message {
	if display != "" {
		text = display
	}
	return Message{InternalID: id, Role: MessageRoleUser, Origin: provider.OriginHuman, Content: text, Time: at, PendingSteering: true}
}
