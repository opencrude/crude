package workspace

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/types"
	"crude/types/reasoning"
)

func newIngressWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w := &Workspace{AgentMgr: NewAgentManager(), PaneMgr: NewPaneManager(), Status: NewStatusCenter(nil, nil),
		SessionsByAgent: map[string]*Session{"root": {SessionID: "session", AgentID: "root"}}}
	t.Cleanup(w.Status.Close)
	w.RegisterAgentRoot("root")
	return w
}

func ingressEnvelope(agentID, ownerID, kind string, sequence uint64, data any) AgentEvent {
	return AgentEvent{AgentID: agentID, Type: kind, Data: data, Identity: types.RuntimeEventIdentity{
		EventID: fmt.Sprintf("%s-%d", agentID, sequence), OwnerID: ownerID, AgentID: agentID,
		RunID: "run-" + agentID, RuntimeEpoch: 1, Generation: 1, Sequence: sequence,
	}}
}

func TestAgentIngressAdvancesRootAndChildWithoutShell(t *testing.T) {
	w := newIngressWorkspace(t)
	root := w.AgentMgr.Get("root")
	for _, event := range []AgentEvent{
		ingressEnvelope("root", "session", "turn_started", 1, types.TurnIdentity{Generation: 1, ModelID: "model"}),
		ingressEnvelope("root", "session", "tool_call", 2, map[string]any{"tool_id": "launch", "internal_id": "launch-row", "name": "agent_start", "args": `{"category":"crafter"}`}),
		{AgentID: "child", Type: "sub_agent_started", Data: map[string]string{"agent_id": "child", "parent_id": "root", "agent_name": "Child", "category": "crafter", "prompt": "bounded assignment", "parent_tool_id": "launch"}},
		ingressEnvelope("child", "child", "thinking", 1, reasoning.Delta{Text: "reasoned once", Kind: reasoning.Raw}),
		ingressEnvelope("child", "child", "stream_token", 2, "child response"),
		ingressEnvelope("root", "session", "stream_token", 3, "root response"),
	} {
		if result := w.ApplyAgentEvent(event); !result.Accepted {
			t.Fatalf("event rejected: %+v", event)
		}
	}
	if root.State.LastTokenTime.IsZero() || w.AgentMgr.Get("child").State.LastTokenTime.IsZero() {
		t.Fatal("semantic output still requires a shell tick")
	}
	snapshot := w.SnapshotAgents()
	if len(snapshot.Agents) != 2 || snapshot.Agents[1].SessionID != "session" || snapshot.Agents[1].ParentID != "root" {
		t.Fatalf("headless inventory = %+v", snapshot)
	}
	childMessages := snapshot.Agents[1].Messages
	if len(childMessages) != 3 || childMessages[0].Content != "bounded assignment" || childMessages[1].Content != "reasoned once" || childMessages[2].Content != "child response" {
		t.Fatalf("headless child evidence = %+v", childMessages)
	}
	if got := snapshot.Agents[0].Messages; len(got) != 2 || got[0].SubPaneID != "child" || got[1].Content != "root response" {
		t.Fatalf("headless root evidence = %+v", got)
	}
	w.ApplyAgentEvent(AgentEvent{AgentID: "child", Type: "sub_agent_completed", Data: map[string]string{"agent_id": "child", "status": "completed"}})
	child := w.AgentMgr.Get("child")
	if !child.State.Completed || child.State.IsActive() {
		t.Fatal("headless completion did not settle child")
	}
	status, ok := w.Status.Agent("child")
	if !ok || status.Phase != types.AgentCompleted || status.Ref.SessionID != "session" {
		t.Fatalf("completion lost retained shared status: %+v", status)
	}
}

func TestAgentIngressRejectsInvalidOwnersBeforeMutation(t *testing.T) {
	for _, invalid := range []string{"first-owner", "wrong-agent", "incomplete", "unknown-root", "unknown-parent", "child-payload"} {
		t.Run(invalid, func(t *testing.T) {
			w := newIngressWorkspace(t)
			event := ingressEnvelope("root", "session", "stream_token", 1, "must not appear")
			switch invalid {
			case "first-owner":
				event.Identity.OwnerID = "foreign"
			case "wrong-agent":
				event.Identity.AgentID = "other"
			case "incomplete":
				event.Identity.Generation = 0
			case "unknown-root":
				event.AgentID, event.Identity.AgentID = "missing", "missing"
			case "unknown-parent":
				event = AgentEvent{Type: "sub_agent_started", AgentID: "child", Data: map[string]string{"agent_id": "child", "parent_id": "missing"}}
			case "child-payload":
				event = AgentEvent{Type: "sub_agent_started", AgentID: "child", Data: map[string]string{"agent_id": "different", "parent_id": "root"}}
			}
			before := w.SnapshotAgents()
			if result := w.ApplyAgentEvent(event); result.Accepted {
				t.Fatal("invalid owner admitted")
			}
			if after := w.SnapshotAgents(); !reflect.DeepEqual(after, before) {
				t.Fatal("rejection changed inventory or evidence")
			}
		})
	}
}

func TestAgentIngressDuplicateAndLateTerminalEvidence(t *testing.T) {
	w := newIngressWorkspace(t)
	w.ApplyAgentEvent(ingressEnvelope("root", "session", "turn_started", 1, types.TurnIdentity{Generation: 1, ModelID: "model"}))
	w.ApplyAgentEvent(ingressEnvelope("root", "session", "stream_token", 2, "before"))
	w.ApplyAgentEvent(ingressEnvelope("root", "session", "done", 3, map[string]any{"reason": "cancelled"}))
	late := ingressEnvelope("root", "session", "stream_token", 4, " after")
	if !w.ApplyAgentEvent(late).Accepted {
		t.Fatal("same-owner terminal evidence was lost")
	}
	before := w.SnapshotAgents()
	if w.ApplyAgentEvent(late).Accepted || !reflect.DeepEqual(before, w.SnapshotAgents()) {
		t.Fatal("duplicate evidence changed accepted state")
	}
	state := w.AgentMgr.Get("root").State
	status, _ := w.Status.Agent("root")
	if state.IsActive() || status.Phase != types.AgentCancelled {
		t.Fatal("late evidence reactivated terminal runtime")
	}
	var body string
	for _, message := range state.Messages.Messages() {
		if message.Role == MessageRoleAssistant {
			body += message.Content
		}
	}
	if strings.Count(body, "after") != 1 {
		t.Fatalf("retained response = %q", body)
	}
}

func TestAgentIngressRetainsExactPromptForObservers(t *testing.T) {
	w := newIngressWorkspace(t)
	first := w.ApplyAgentEvent(AgentEvent{AgentID: "root", Type: "blocking", Data: map[string]string{"block_id": "block", "tool": "execute_command", "question": "Run exact command?"}})
	if !first.Accepted || first.Effects.Prompt == nil {
		t.Fatal("prompt was not reduced")
	}
	first.Effects.Prompt.Question = "observer changed effect"
	snapshot := w.SnapshotAgents()
	if snapshot.Agents[0].Prompt == nil || snapshot.Agents[0].Prompt.Question != "Run exact command?" {
		t.Fatal("observer changed pending decision")
	}
	snapshot.Agents[0].Prompt.Question = "observer changed snapshot"
	if w.ApplyAgentEvent(AgentEvent{AgentID: "root", Type: "blocking_complete", Data: map[string]string{"block_id": "other"}}).Accepted {
		t.Fatal("stale completion resolved replacement block")
	}
	if w.SnapshotAgents().Agents[0].Prompt.Question != "Run exact command?" {
		t.Fatal("snapshot or stale completion changed prompt")
	}
	w.ApplyAgentEvent(AgentEvent{AgentID: "root", Type: "blocking_complete", Data: map[string]string{"block_id": "block"}})
	if w.SnapshotAgents().Agents[0].Prompt != nil {
		t.Fatal("resolved block remains available to reconnecting observer")
	}
}

func TestAgentIngressInputReceiptPublishesOnce(t *testing.T) {
	w := newIngressWorkspace(t)
	intent := &InputAdmissionIntent{SessionID: "session", AgentID: "root", InputID: "input", DraftRevision: 7, OwnerEpoch: 1, TurnGeneration: 1}
	result := InputAdmissionResult{Status: InputAdmissionAccepted, Receipt: InputAdmissionReceipt{
		SessionID: "session", AgentID: "root", InputID: "input", DraftRevision: 7, OwnerEpoch: 1, TurnGeneration: 1, ContextRevision: 1, AcceptedAt: time.Now(),
	}}
	input := provider.PlainInput("accepted human request")
	if !w.ApplyAgentInputAdmission(intent, result, input, []string{"image"}) {
		t.Fatal("receipt rejected")
	}
	revision := w.agentRevision
	if !w.ApplyAgentInputAdmission(intent, result, input, []string{"image"}) || revision != w.agentRevision {
		t.Fatal("duplicate receipt was not idempotent")
	}
	count := 0
	for _, message := range w.SnapshotAgents().Agents[0].Messages {
		if message.Role == MessageRoleUser {
			count++
			if message.Content != input.Text || message.Origin != provider.OriginHuman {
				t.Fatal("input lost content or origin")
			}
		}
	}
	if count != 1 {
		t.Fatalf("input appeared %d times", count)
	}
}

func TestAgentIngressCompletedChildRetainsLateUsageAndClassification(t *testing.T) {
	w := newIngressWorkspace(t)
	w.ApplyAgentEvent(AgentEvent{AgentID: "child", Type: "sub_agent_started", Data: map[string]string{"agent_id": "child", "parent_id": "root"}})
	w.ApplyAgentEvent(ingressEnvelope("child", "child", "thinking", 1, reasoning.Delta{Text: "accepted reasoning", Kind: reasoning.Provisional}))
	usage := map[string]any{"request_id": "request", "reported_at": int64(1), "prompt": 90, "completion": 10}
	w.ApplyAgentEvent(ingressEnvelope("child", "child", "usage", 2, usage))
	w.ApplyAgentEvent(AgentEvent{AgentID: "child", Type: "sub_agent_completed", Data: map[string]string{"agent_id": "child", "status": "cancelled"}})
	usage["reported_at"], usage["completion"] = int64(2), 20
	if !w.ApplyAgentEvent(ingressEnvelope("child", "child", "usage", 3, usage)).Accepted {
		t.Fatal("completed child rejected late authoritative usage revision")
	}
	w.ApplyAgentEvent(ingressEnvelope("child", "child", "reasoning_classified", 4, reasoning.InferredSummary))
	child := w.AgentMgr.Get("child").State
	summary, _ := w.UsageSummary("root", types.UsageSession)
	status, _ := w.Status.Agent("child")
	if summary.Output.Value != 20 || w.PaneMgr.Get("child").TokenCount != 110 || status.Phase != types.AgentCancelled || child.IsActive() {
		t.Fatalf("late revision lost accounting or revived lifecycle: usage=%+v status=%+v", summary, status)
	}
	if got := child.Messages.Messages()[0].ThinkingDisplay.Kind; got != reasoning.InferredSummary {
		t.Fatalf("terminal classification correction lost: %s", got)
	}
}

func TestManualCompactionAfterTerminalTurnKeepsIndependentLifecycle(t *testing.T) {
	for _, reason := range []string{"completed", "cancelled"} {
		t.Run(reason, func(t *testing.T) {
			w := newIngressWorkspace(t)
			w.ApplyAgentEvent(ingressEnvelope("root", "session", "turn_started", 1, types.TurnIdentity{Generation: 1, ModelID: "model"}))
			w.ApplyAgentEvent(ingressEnvelope("root", "session", "done", 2, map[string]any{"reason": reason}))
			state := w.AgentMgr.Get("root").State
			data := map[string]any{"request_id": "manual-after-turn", "trigger": "manual", "stage_count": 4, "stage_label": "Capturing intent"}
			w.ApplyAgentEvent(AgentEvent{AgentID: "root", Type: "context_compaction_started", Data: data})
			w.ApplyAgentEvent(AgentEvent{AgentID: "root", Type: "context_compaction_stage", Data: data})
			status, _ := w.Status.Agent("root")
			if state.ActiveCompaction.Phase != "compaction_extracting" || !state.IsActive() || status.Phase != types.AgentCompacting {
				t.Fatalf("real terminal fence hid new compaction: active=%+v status=%+v", state.ActiveCompaction, status)
			}
			// The independent operation must not reopen animation for old accepted
			// model output; that turn is still terminal until a new generation.
			w.ApplyAgentEvent(ingressEnvelope("root", "session", "thinking", 3, reasoning.Delta{Text: "late old thought", Kind: reasoning.Raw}))
			if _, active := state.Messages.ActiveThinking(); active {
				t.Fatal("manual operation reactivated the terminal model turn")
			}
			w.ApplyAgentEvent(AgentEvent{AgentID: "root", Type: "context_compacted", Data: data})
			status, _ = w.Status.Agent("root")
			if state.ActiveCompaction.Phase != "" || state.IsActive() || status.Phase != types.AgentCompactionResult {
				t.Fatalf("manual operation did not settle: active=%+v status=%+v", state.ActiveCompaction, status)
			}
			for _, kind := range []string{"context_compaction_started", "context_compaction_stage"} {
				if w.ApplyAgentEvent(AgentEvent{AgentID: "root", Type: kind, Data: data}).Accepted || state.IsActive() {
					t.Fatal("late lifecycle reopened settled compaction")
				}
			}
		})
	}
}
