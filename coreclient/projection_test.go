package coreclient

import (
	"errors"
	"testing"

	"crude/coreprotocol"
	"crude/provider"
	"crude/sessiondb"
	"crude/types"
	"crude/types/activity"
	"crude/workspace"
)

func projectionFixture() coreprotocol.WorkspaceSnapshot {
	pad := 3
	return coreprotocol.WorkspaceSnapshot{ID: "w", Root: "/project", Revision: 1,
		Settings: coreprotocol.ShellSettings{Theme: types.ThemeSettings{PadLeft: &pad}},
		Sessions: []coreprotocol.SessionSnapshot{{ID: "s", AgentID: "root", Settings: coreprotocol.SessionSettings{Model: "next-model"},
			References: []sessiondb.Reference{{ID: "ref", SessionID: "s", Kind: sessiondb.KindPastedImage, Content: "not-decoded-or-written", Label: "[image]"}}}},
		Agents: []workspace.AgentSnapshot{
			{ID: "child", ParentID: "root", SessionID: "s", Label: "Child", ConversationRevision: 1,
				Messages: []workspace.Message{{InternalID: "c", Role: workspace.MessageRoleAssistant, Content: "child text"}}},
			{ID: "root", SessionID: "s", Label: "Root", Running: true, Yolo: true, ConversationRevision: 1,
				Messages: []workspace.Message{{InternalID: "m", Role: workspace.MessageRoleAssistant, Content: "a", ReferenceLabels: []string{"original"},
					Collaboration: &provider.CollaborationAttribution{RecipientIDs: []string{"recipient"}}}},
				Activity: types.AgentActivityView{Active: true, Plan: &activity.Plan{Steps: []activity.Step{{Text: "original"}}}}},
		}}
}

func newProjection(t *testing.T, snapshot coreprotocol.WorkspaceSnapshot) *WorkspaceView {
	t.Helper()
	v, err := NewWorkspaceView(nil, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWorkspaceViewIndependentStreamStablePointers(t *testing.T) {
	snapshot := projectionFixture()
	a, b := newProjection(t, snapshot), newProjection(t, snapshot)
	node, state, conversation := a.AgentMgr.Get("root"), a.AgentMgr.Get("root").State, a.AgentMgr.Get("root").State.Messages
	session, settings, refs := a.Sessions[0], a.Sessions[0].Settings, a.Sessions[0].RefStore
	if a.AgentMgr.Get("child").Parent != node || a.SessionForAgent("child") != session || session.AgentState != state {
		t.Fatal("family not installed")
	}
	for _, id := range []string{"root", "child"} {
		if pane := a.PaneMgr.Get(id); pane == nil || pane.Messages != a.AgentMgr.Get(id).State.Messages {
			t.Fatalf("%s pane is not bound to its canonical replica transcript", id)
		}
	}
	if node == b.AgentMgr.Get("root") || state == b.AgentMgr.Get("root").State {
		t.Fatal("clients share mutable state")
	}
	message := workspace.Message{InternalID: "m", Role: workspace.MessageRoleAssistant, Content: "accepted whole token body"}
	delta := coreprotocol.AgentChangesResult{Revision: 2, Agents: []coreprotocol.AgentDelta{{
		State:        workspace.AgentSnapshot{ID: "root", SessionID: "s", Label: "Root", Running: true, ConversationRevision: 2},
		Conversation: workspace.ConversationDelta{BaseRevision: 1, Revision: 2, Upserts: []workspace.Message{message}},
	}}}
	for _, view := range []*WorkspaceView{a, b} {
		changed, err := view.ApplyChanges(delta)
		if err != nil || len(changed) != 1 {
			t.Fatalf("apply: %v %v", changed, err)
		}
		if _, err := view.ApplyChanges(delta); err != nil {
			t.Fatal(err)
		}
		messages := view.AgentMgr.Get("root").State.Messages.Messages()
		if len(messages) != 1 || messages[0].Content != message.Content || view.Revisions()["root"] != 2 {
			t.Fatalf("tokens duplicated or wrong cursor: %+v", messages)
		}
	}
	if a.AgentMgr.Get("root") != node || node.State != state || state.Messages != conversation || a.Sessions[0] != session {
		t.Fatal("delta replaced stable identities")
	}
	snapshot.Revision = 3
	snapshot.Agents[1].Messages = []workspace.Message{message}
	snapshot.Agents[1].ConversationRevision = 2
	if err := a.InstallSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if a.AgentMgr.Get("root") != node || node.State != state || state.Messages != conversation || a.Sessions[0] != session || session.Settings != settings || session.RefStore != refs {
		t.Fatal("resync replaced identities")
	}
	if !a.YoloForAgent("root") || !a.AgentTurnActive("root") || a.AgentModelIdentity("root").ModelID != "next-model" {
		t.Fatal("metadata not installed")
	}
	if _, err := a.ApplyChanges(coreprotocol.AgentChangesResult{Revision: 4, Removed: []string{"root"}}); err != nil {
		t.Fatal(err)
	}
	if a.AgentMgr.Count() != 0 || a.SessionForAgent("child") != nil || len(a.Revisions()) != 0 {
		t.Fatal("removed family retained")
	}
	if len(a.PaneMgr.Panes()) != 0 {
		t.Fatal("removed family retained replica panes")
	}
	if session.AgentState != nil {
		t.Fatal("removed root retained by session")
	}
	if b.AgentMgr.Count() != 2 {
		t.Fatal("removal affected other client")
	}
}

func TestWorkspaceViewDetachedNestedData(t *testing.T) {
	snapshot := projectionFixture()
	v := newProjection(t, snapshot)
	snapshot.Agents[1].Messages[0].Collaboration.RecipientIDs[0] = "mutated"
	snapshot.Agents[1].Messages[0].ReferenceLabels[0] = "mutated"
	snapshot.Agents[1].Activity.Plan.Steps[0].Text = "mutated"
	*snapshot.Settings.Theme.PadLeft = 99
	message := v.AgentMgr.Get("root").State.Messages.Messages()[0]
	if message.Collaboration.RecipientIDs[0] != "recipient" || message.ReferenceLabels[0] != "original" {
		t.Fatal("snapshot aliases nested message data")
	}
	view := v.AgentActivity("root")
	view.Plan.Steps[0].Text = "consumer mutation"
	if v.AgentActivity("root").Plan.Steps[0].Text != "original" {
		t.Fatal("activity read exposes retained mutable slices")
	}
	settings := v.LoadAppSettings()
	*settings.Theme.PadLeft = 88
	if *v.LoadAppSettings().Theme.PadLeft != 3 {
		t.Fatal("settings alias producer or reader")
	}
	ref, ok := v.Sessions[0].RefStore.GetReference("ref")
	if !ok || ref.Content != "not-decoded-or-written" || ref.FilePath != "" {
		t.Fatal("reference cache performed attachment I/O")
	}
	delta := coreprotocol.AgentChangesResult{Revision: 2, Agents: []coreprotocol.AgentDelta{{
		State: workspace.AgentSnapshot{ID: "root", SessionID: "s", ConversationRevision: 2,
			Activity: types.AgentActivityView{Plan: &activity.Plan{Steps: []activity.Step{{Text: "delta plan"}}}}},
		Conversation: workspace.ConversationDelta{BaseRevision: 1, Revision: 2, Upserts: []workspace.Message{{InternalID: "m", ReferenceLabels: []string{"delta label"}}}},
	}}}
	if _, err := v.ApplyChanges(delta); err != nil {
		t.Fatal(err)
	}
	delta.Agents[0].State.Activity.Plan.Steps[0].Text = "changed producer"
	delta.Agents[0].Conversation.Upserts[0].ReferenceLabels[0] = "changed producer"
	if v.AgentActivity("root").Plan.Steps[0].Text != "delta plan" || v.AgentMgr.Get("root").State.Messages.Messages()[0].ReferenceLabels[0] != "delta label" {
		t.Fatal("delta retains producer-owned nested values")
	}
}

func TestWorkspaceViewRejectsGapAndInvalidBatchAtomically(t *testing.T) {
	v := newProjection(t, projectionFixture())
	root := v.AgentMgr.Get("root")
	valid := coreprotocol.AgentDelta{State: workspace.AgentSnapshot{ID: "root", SessionID: "s", ConversationRevision: 2},
		Conversation: workspace.ConversationDelta{BaseRevision: 1, Revision: 2, Upserts: []workspace.Message{{InternalID: "m", Content: "new"}}}}
	invalid := coreprotocol.AgentDelta{State: workspace.AgentSnapshot{ID: "child", ParentID: "root", SessionID: "s", ConversationRevision: 3},
		Conversation: workspace.ConversationDelta{BaseRevision: 2, Revision: 3}}
	if _, err := v.ApplyChanges(coreprotocol.AgentChangesResult{Revision: 2, Agents: []coreprotocol.AgentDelta{valid, invalid}}); !errors.Is(err, ErrResync) {
		t.Fatalf("gap accepted: %v", err)
	}
	if v.Revision() != 1 || root.State.Messages.Messages()[0].Content != "a" {
		t.Fatal("invalid batch partially installed")
	}
	valid.Conversation.Order = []string{"m", "unavailable"}
	if _, err := v.ApplyChanges(coreprotocol.AgentChangesResult{Revision: 2, Agents: []coreprotocol.AgentDelta{valid}}); !errors.Is(err, ErrResync) {
		t.Fatalf("bad order accepted: %v", err)
	}
	if _, err := v.ApplyChanges(coreprotocol.AgentChangesResult{Revision: 0, Removed: []string{"root"}}); err != nil || v.AgentMgr.Get("root") != root {
		t.Fatal("stale delta changed inventory")
	}
}

func TestWorkspaceViewUsageScopes(t *testing.T) {
	snapshot := projectionFixture()
	snapshot.Agents[1].Usage = types.UsageSummary{Requests: 2, Turns: 1, TurnsKnown: true, ContextUsed: types.UsageValue{Value: 123, Known: true},
		Output: types.UsageValue{Value: 10, Known: true, Confidence: types.ConfidenceMeasured}, Cost: types.CostValue{USD: 1, Known: true}}
	snapshot.Agents[0].Usage = types.UsageSummary{Requests: 1, Turns: 0, TurnsKnown: false,
		Output: types.UsageValue{Value: 5, Known: false}, Cost: types.CostValue{USD: 2, Known: false}}
	v := newProjection(t, snapshot)
	summary, ok := v.UsageSummary("root", types.UsageSession)
	if !ok || summary.Requests != 3 || summary.Output.Value != 15 || summary.Output.Known || summary.Cost.Known || summary.Cost.USD != 3 || summary.TurnsKnown || summary.ContextUsed.Value != 123 {
		t.Fatalf("wrong scoped accounting: %+v", summary)
	}
	child, _ := v.UsageSummary("child", types.UsageSubtree)
	if child.Requests != 1 {
		t.Fatal("subtree borrowed parent accounting")
	}
}
