package coreclient

import (
	"crude/coreprotocol"
	"crude/workspace"
	"errors"
	"strings"
	"testing"
)

func TestPublicationCandidateSessionsAtomic(t *testing.T) {
	v := newProjection(t, projectionFixture())
	owner := v.Sessions[0]
	root := v.AgentMgr.Get("root")
	sessions := coreprotocol.SessionSnapshotList{{ID: "s", AgentID: "root"}, {ID: "next", AgentID: "next-root"}}
	added := coreprotocol.AgentDelta{State: workspace.AgentSnapshot{ID: "next-root", SessionID: "next", ConversationRevision: 1}, Conversation: workspace.ConversationDelta{Reset: true, Revision: 1, Order: []string{}}}
	invalid := coreprotocol.ConversationUpdate{AgentID: "root", Delta: workspace.ConversationDelta{BaseRevision: 1, Revision: 2, Appends: []workspace.ConversationTextAppend{{InternalID: "m", Offset: 99, Text: "bad"}}}}
	u := coreprotocol.WorkspaceUpdate{Changes: coreprotocol.AgentChangesResult{Revision: 2, Agents: []coreprotocol.AgentDelta{added}}, Sessions: &sessions, Conversations: []coreprotocol.ConversationUpdate{invalid}}
	if _, err := v.ApplyPublication(u); !errors.Is(err, ErrResync) {
		t.Fatalf("invalid append accepted: %v", err)
	}
	if v.Revision() != 1 || len(v.Sessions) != 1 || v.Sessions[0] != owner || v.AgentMgr.Get("next-root") != nil || root.State.Messages.Messages()[0].Content != "a" {
		t.Fatal("failed candidate partially changed replica")
	}
	u.Conversations = nil
	if _, err := v.ApplyPublication(u); err != nil {
		t.Fatal(err)
	}
	if v.SessionsByID["next"] == nil || v.SessionForAgent("next-root") != v.SessionsByID["next"] || v.SessionsByID["s"] != owner {
		t.Fatal("new session/root did not commit atomically retaining existing identity")
	}
}

func TestPublicationLongTranscriptAppendAndNewDomain(t *testing.T) {
	v := newProjection(t, projectionFixture())
	node := v.AgentMgr.Get("root")
	conversation := node.State.Messages
	owner := v.Sessions[0]
	const count = 1500
	for i := 1; i <= count; i++ {
		revision := uint64(i + 1)
		_, err := v.ApplyPublication(coreprotocol.WorkspaceUpdate{Changes: coreprotocol.AgentChangesResult{Revision: revision}, Conversations: []coreprotocol.ConversationUpdate{{AgentID: "root", Delta: workspace.ConversationDelta{BaseRevision: revision - 1, Revision: revision, Appends: []workspace.ConversationTextAppend{{InternalID: "m", Offset: uint64(i), Text: "x"}}}}}})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if got := conversation.Messages()[0].Content; got != "a"+strings.Repeat("x", count) {
		t.Fatalf("long stream wrong length %d", len(got))
	}
	snapshot := projectionFixture()
	snapshot.Sessions[0].Full = &coreprotocol.SessionSettingsResult{Revision: 10}
	if err := v.InstallPublicationSnapshot(snapshot, true); err != nil {
		t.Fatal(err)
	}
	snapshot = projectionFixture()
	snapshot.Sessions[0].Full = &coreprotocol.SessionSettingsResult{Revision: 1}
	snapshot.Agents[1].Messages[0].Content = "cold core text"
	if err := v.InstallPublicationSnapshot(snapshot, true); err != nil {
		t.Fatal(err)
	}
	if v.AgentMgr.Get("root") != node || node.State.Messages != conversation || v.Sessions[0] != owner || conversation.Version() != 1 || conversation.Messages()[0].Content != "cold core text" || owner.SettingsRevision() != 1 {
		t.Fatal("new domain lost identities or refused lower revision")
	}
	bad := projectionFixture()
	bad.Agents[1].Messages[0].InternalID = ""
	if err := v.InstallPublicationSnapshot(bad, true); !errors.Is(err, ErrResync) {
		t.Fatalf("malformed new domain accepted: %v", err)
	}
	if conversation.Messages()[0].Content != "cold core text" || conversation.Version() != 1 {
		t.Fatal("failed new domain cleared canonical revision")
	}
}
