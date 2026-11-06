package remote

import (
	"context"
	"testing"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

type stopFixture struct {
	t    *testing.T
	w    *Workspace
	seq  uint64
	turn types.TurnIdentity
}

func newStopFixture(t *testing.T) *stopFixture {
	t.Helper()
	turn := types.TurnIdentity{Generation: 3, ModelID: "m"}
	running := workspace.AgentSnapshot{ID: "root", Running: true, Thinking: true, TurnIdentity: turn, ConversationRevision: 1,
		Activity: types.AgentActivityView{Active: true, Status: types.AgentStatusSnapshot{Phase: types.AgentThinking, Detail: "Thinking"}}}
	view, err := coreclient.NewWorkspaceView(nil, coreprotocol.WorkspaceSnapshot{ID: "w", Root: "/project", Revision: 1, Agents: []workspace.AgentSnapshot{running}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	w := &Workspace{ctx: ctx, view: view, initialized: true, logger: nopLogger{}, topicClients: make(map[string]*coreclient.Client), topicCursors: map[string]uint64{coreprotocol.TopicWorkspace: 1}}
	return &stopFixture{t: t, w: w, seq: 1, turn: turn}
}

// publish installs one core publication of the agent's metadata through the
// shell's real replica commit path.
func (f *stopFixture) publish(state workspace.AgentSnapshot) {
	f.t.Helper()
	f.seq++
	state.ID, state.ConversationRevision = "root", 1
	batch := publicationBatch{ctx: f.w.ctx, topic: coreprotocol.TopicWorkspace, header: coreprotocol.TopicHeader{Seq: f.seq}, done: make(chan error, 1),
		update: coreprotocol.WorkspaceUpdate{Changes: coreprotocol.AgentChangesResult{Revision: f.seq, Agents: []coreprotocol.AgentDelta{{State: state, Conversation: workspace.ConversationDelta{BaseRevision: 1, Revision: 1}}}}}}
	applied := f.w.ApplyReplicaChanges(shellport.ReplicaChangesMsg{Owner: f.w, Batch: batch})
	if err := <-batch.done; err != nil {
		f.t.Fatalf("publication rejected: %v %+v", err, applied.Follow)
	}
}

func (f *stopFixture) running() workspace.AgentSnapshot {
	return workspace.AgentSnapshot{Running: true, Thinking: true, TurnIdentity: f.turn,
		Activity: types.AgentActivityView{Active: true, Status: types.AgentStatusSnapshot{Phase: types.AgentThinking, Detail: "Thinking"}}}
}

// stopping reports what the user sees: the agent's turn state and the
// activity strip's phase.
func (f *stopFixture) stopping() bool {
	f.t.Helper()
	state := f.w.view.AgentMgr.Get("root").State
	activity := f.w.AgentActivity("root")
	shown := state.Cancelling && !state.IsActive() && activity.Status.Phase == types.AgentCancelling && !activity.Active
	if !shown && (state.Cancelling || activity.Status.Phase == types.AgentCancelling) && state.IsActive() {
		f.t.Fatalf("Stop half applied: cancelling=%v active=%v phase=%s", state.Cancelling, state.IsActive(), activity.Status.Phase)
	}
	return shown
}

// Publications the core produced before it admitted Stop keep arriving while
// the turn streams. None of them may resume the turn the user just stopped.
func TestStopStaysVisibleUntilCoreAcknowledges(t *testing.T) {
	f := newStopFixture(t)
	if !f.w.BeginAgentCancellation("root") || !f.stopping() {
		t.Fatal("Stop did not show immediately")
	}
	for range 5 {
		f.publish(f.running())
		if !f.stopping() {
			t.Fatal("an in-flight publication resumed the stopped turn: Stop flashed")
		}
	}
	if f.w.BeginAgentCancellation("root") {
		t.Fatal("a second Stop was admitted while the first is in progress")
	}
	acknowledged := f.running()
	acknowledged.Running, acknowledged.Thinking, acknowledged.Cancelling = false, false, true
	acknowledged.Activity = types.AgentActivityView{Status: workspace.StoppingStatus(types.AgentStatusSnapshot{})}
	f.publish(acknowledged)
	if !f.stopping() || f.w.stops.held("root") {
		t.Fatal("core acknowledgement did not take over the stopping boundary")
	}
	idle := workspace.AgentSnapshot{TurnIdentity: f.turn, Activity: types.AgentActivityView{Status: types.AgentStatusSnapshot{Phase: types.AgentCancelled}}}
	f.publish(idle)
	if state := f.w.view.AgentMgr.Get("root").State; state.Cancelling || f.w.AgentActivity("root").Status.Phase != types.AgentCancelled {
		t.Fatal("finished agent still shows Stopping")
	}
}

// When the turn ends on its own before the core sees Stop, the core refuses
// the Stop. The shell must show the finished agent, not a stuck Stopping.
func TestStopRefusedAfterTurnEndedShowsIdle(t *testing.T) {
	f := newStopFixture(t)
	f.w.BeginAgentCancellation("root")
	f.publish(workspace.AgentSnapshot{TurnIdentity: f.turn, Activity: types.AgentActivityView{Status: types.AgentStatusSnapshot{Phase: types.AgentCompleted}}})
	if f.stopping() || f.w.view.AgentMgr.Get("root").State.Cancelling || f.w.stops.held("root") {
		t.Fatal("finished agent stays at Stopping")
	}
	next := f.running()
	next.TurnIdentity.Generation++
	f.publish(next)
	if f.stopping() || !f.w.view.AgentMgr.Get("root").State.IsActive() {
		t.Fatal("old Stop leaked into the next turn")
	}
}

// Housekeeping that reconciles every frame must not send unchanged status to
// the core: those calls queued ahead of real work on every tick.
func TestUnchangedIssuesSendNothing(t *testing.T) {
	w := &Workspace{}
	w.services.init()
	s := newStatusReplica(w)
	issue := types.IssueSnapshot{ID: "lsp:go", Owner: types.ResourceRef{Kind: types.ResourceLSP, ID: "go"}, Severity: types.SeverityError, Summary: "gopls failed", Detail: "exit 1"}
	s.SetIssue(issue)
	s.ResolveIssue("lsp:go")
	sent := 0
	for _, ok := w.services.pop(); ok; _, ok = w.services.pop() {
		sent++
	}
	if sent != 2 {
		t.Fatalf("real changes sent %d mutations, want 2", sent)
	}
	s.SetIssue(issue)
	version := s.Version()
	for range 100 {
		s.SetIssue(issue)
		s.ResolveIssue("lsp:never-reported")
	}
	s.ResolveIssue("lsp:go")
	s.ResolveIssue("lsp:go")
	sent = 0
	for _, ok := w.services.pop(); ok; _, ok = w.services.pop() {
		sent++
	}
	if sent != 2 || s.Version() != version+1 {
		t.Fatalf("frame reconciliation sent %d mutations (want reopen+resolve 2), version moved %d", sent, s.Version()-version)
	}
}
