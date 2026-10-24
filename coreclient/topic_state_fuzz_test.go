package coreclient

import (
	"errors"
	"fmt"
	"testing"

	"crude/coreprotocol"
	"crude/workspace"
)

// This oracle deliberately uses no Conversation, reducer, ChangesSince or
// replica export. IDs, order, text and revisions are independent plain values.
type topicFuzzRow struct{ id, text string }
type topicFuzzAuthority struct {
	revision                    uint64
	rootRevision, childRevision uint64
	rows                        []topicFuzzRow
	child                       bool
}

func (a topicFuzzAuthority) snapshot() coreprotocol.WorkspaceSnapshot {
	messages := make([]workspace.Message, len(a.rows))
	for i, row := range a.rows {
		messages[i] = topicFuzzMessage(row)
	}
	s := coreprotocol.WorkspaceSnapshot{ID: "fuzz-workspace", Root: "/fuzz-project", Revision: a.revision,
		Sessions: []coreprotocol.SessionSnapshot{{ID: "session", AgentID: "root"}},
		Agents:   []workspace.AgentSnapshot{{ID: "root", SessionID: "session", Label: "Root", ConversationRevision: a.rootRevision, Messages: messages}}}
	if a.child {
		s.Agents = append(s.Agents, workspace.AgentSnapshot{ID: "child", ParentID: "root", SessionID: "session", Label: "Child", ConversationRevision: a.childRevision, Messages: []workspace.Message{topicFuzzMessage(topicFuzzRow{"child-message", "child text"})}})
	}
	return s
}

func topicFuzzMessage(row topicFuzzRow) workspace.Message {
	return workspace.Message{InternalID: row.id, Role: workspace.MessageRoleAssistant, Content: row.text, ReferenceLabels: []string{"detached-label"}}
}

func topicFuzzDecode(t *testing.T, source coreprotocol.WorkspaceUpdate) coreprotocol.WorkspaceUpdate {
	t.Helper()
	body, err := coreprotocol.Marshal(&source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded coreprotocol.WorkspaceUpdate
	if err := coreprotocol.Decode(body, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// The boundary under test is workspace publication installation. Subscription
// generations/TopicHeader sequence admission and the seven latest-state topics
// are owned by shellport/remote, not WorkspaceView, and are not simulated here.
func FuzzWorkspaceTopicReplicaState(f *testing.F) {
	for _, seed := range [][]byte{
		{}, {0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{3, 2, 12, 22, 32, 1, 0, 6, 5, 7, 4},
		{0, 0, 0, 5, 0, 8, 3, 3, 2, 2, 9},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, actions []byte) {
		if len(actions) > 32 {
			actions = actions[:32]
		}
		a := topicFuzzAuthority{revision: 1, rootRevision: 1, childRevision: 1, rows: []topicFuzzRow{{"message", "a"}}}
		initial := a.snapshot()
		first, err := NewWorkspaceView(nil, *topicFuzzDecode(t, coreprotocol.WorkspaceUpdate{Snapshot: &initial}).Snapshot)
		if err != nil {
			t.Fatal(err)
		}
		second, err := NewWorkspaceView(nil, *topicFuzzDecode(t, coreprotocol.WorkspaceUpdate{Snapshot: &initial}).Snapshot)
		if err != nil {
			t.Fatal(err)
		}
		views := []*WorkspaceView{first, second}
		type identity struct {
			session      *workspace.Session
			node         *workspace.AgentNode
			state        *workspace.AgentState
			conversation *workspace.Conversation
			settings     *workspace.SessionSettingsState
			refs         workspace.SessionReferences
		}
		var stable [2]identity
		for i, v := range views {
			n := v.AgentMgr.Get("root")
			s := v.SessionsByID["session"]
			stable[i] = identity{s, n, n.State, n.State.Messages, s.Settings, s.RefStore}
		}
		check := func() {
			t.Helper()
			for i, v := range views {
				n, saved := v.AgentMgr.Get("root"), stable[i]
				if v.ID != "fuzz-workspace" || v.Revision() != a.revision || n == nil || n != saved.node || n.State != saved.state || n.State.Messages != saved.conversation || v.SessionsByID["session"] != saved.session {
					t.Fatal("publication lost identity or workspace revision")
				}
				if v.SessionForAgent("root") != saved.session || saved.session.AgentState != n.State || len(v.Sessions) != 1 || saved.session.DB != nil || saved.session.SessionDir != "" || saved.session.Settings != saved.settings || saved.session.RefStore != saved.refs {
					t.Fatal("replica session ownership diverged or acquired persistence")
				}
				if pane := v.PaneMgr.Get("root"); pane == nil || pane.Messages != n.State.Messages {
					t.Fatal("private pane lost canonical conversation")
				}
				messages := n.State.Messages.Messages()
				if n.State.Messages.Version() != a.rootRevision || len(messages) != len(a.rows) {
					t.Fatal("transcript revision/order length diverged")
				}
				for j, row := range a.rows {
					if messages[j].InternalID != row.id || messages[j].Content != row.text || len(messages[j].ReferenceLabels) != 1 || messages[j].ReferenceLabels[0] != "detached-label" {
						t.Fatalf("row %d: got %+v want %+v", j, messages[j], row)
					}
				}
				wantCount := 1
				child := v.AgentMgr.Get("child")
				if a.child {
					wantCount++
					if child == nil || child.Parent != n || v.SessionForAgent("child") != saved.session || child.State.Messages.Version() != a.childRevision {
						t.Fatal("child ownership diverged")
					}
					cm := child.State.Messages.Messages()
					if len(cm) != 1 || cm[0].InternalID != "child-message" || cm[0].Content != "child text" {
						t.Fatal("child transcript diverged")
					}
					if p := v.PaneMgr.Get("child"); p == nil || p.Messages != child.State.Messages {
						t.Fatal("child pane missing")
					}
				} else if child != nil || v.SessionForAgent("child") != nil || v.PaneMgr.Get("child") != nil {
					t.Fatal("removed child retained private/replica state")
				}
				if v.AgentMgr.Count() != wantCount || len(v.PaneMgr.Panes()) != wantCount {
					t.Fatal("inventory divergence")
				}
			}
			if stable[0].node == stable[1].node || stable[0].session == stable[1].session || stable[0].conversation == stable[1].conversation || first.PaneMgr.Get("root") == second.PaneMgr.Get("root") {
				t.Fatal("clients share mutable owner")
			}
			// Test nested decoded ownership, not merely top-level pointers. Restore
			// the first client's private mutation before the next publication.
			left := first.AgentMgr.Get("root").State.Messages.Messages()
			right := second.AgentMgr.Get("root").State.Messages.Messages()
			left[0].ReferenceLabels[0] = "private-client-only"
			if right[0].ReferenceLabels[0] != "detached-label" {
				t.Fatal("clients share decoded message storage")
			}
			left[0].ReferenceLabels[0] = "detached-label"
		}
		check()
		for step, b := range actions {
			next := a
			next.rows = append([]topicFuzzRow(nil), a.rows...)
			next.revision++
			u := coreprotocol.WorkspaceUpdate{Changes: coreprotocol.AgentChangesResult{Revision: next.revision}}
			d := workspace.ConversationDelta{BaseRevision: a.rootRevision, Revision: a.rootRevision + 1}
			reset, newDomain, reject := false, false, false
			switch b % 10 {
			case 0:
				text := []string{"x", "界", "e\u0301", "\n"}[int(b/10)%4]
				j := int(b/40) % len(a.rows)
				d.Appends = []workspace.ConversationTextAppend{{InternalID: a.rows[j].id, Offset: uint64(len(a.rows[j].text)), Text: text}}
				next.rows[j].text += text
				next.rootRevision++
			case 1:
				j := int(b/10) % len(a.rows)
				next.rows[j].text = fmt.Sprintf("replacement-%d-%d", step, b)
				d.Upserts = []workspace.Message{topicFuzzMessage(next.rows[j])}
				next.rootRevision++
			case 2:
				if len(a.rows) > 1 && b&16 != 0 {
					d.Removed = []string{a.rows[len(a.rows)-1].id}
					next.rows = next.rows[:len(next.rows)-1]
				} else {
					row := topicFuzzRow{fmt.Sprintf("row-%d", step), "new row"}
					next.rows = append(next.rows, row)
					d.Upserts = []workspace.Message{topicFuzzMessage(row)}
				}
				d.Order = make([]string, len(next.rows))
				for j, row := range next.rows {
					d.Order[j] = row.id
				}
				next.rootRevision++
			case 3:
				next.child = !a.child
				if a.child {
					u.Changes.Removed = []string{"child"}
				} else {
					u.Changes.Agents = []coreprotocol.AgentDelta{{State: workspace.AgentSnapshot{ID: "child", ParentID: "root", SessionID: "session", ConversationRevision: a.childRevision}, Conversation: workspace.ConversationDelta{Reset: true, Revision: a.childRevision, Order: []string{"child-message"}, Upserts: []workspace.Message{topicFuzzMessage(topicFuzzRow{"child-message", "child text"})}}}}
				}
			case 4:
				// Full snapshot resubscription at the same authority revision must
				// retain surviving client-private owners, not rebuild the view.
				next.revision = a.revision
				reset = true
			case 5:
				next.revision, next.rootRevision, next.childRevision = 1, 1, 1
				next.rows[0].text = "replacement core"
				reset, newDomain = true, true
			case 6:
				// A valid candidate inventory precedes a bad transcript: no part
				// of that candidate may commit, including session metadata.
				sessions := coreprotocol.SessionSnapshotList{{ID: "session", AgentID: "root", ConnectionIssue: "must-not-commit"}}
				u.Sessions = &sessions
				d.Appends = []workspace.ConversationTextAppend{{InternalID: a.rows[0].id, Offset: uint64(len(a.rows[0].text) + 1), Text: "bad"}}
				reject = true
			case 7:
				u.Changes.Revision = a.revision - 1
				reject = true
			case 8:
				reset, newDomain, reject = true, true, true
			case 9:
				next.revision = a.revision - 1
				reset, reject = true, true
			}
			if d.Appends != nil || d.Upserts != nil || d.Removed != nil {
				u.Conversations = []coreprotocol.ConversationUpdate{{AgentID: "root", Delta: d}}
			}
			if reset {
				s := next.snapshot()
				if reject {
					s.Agents[0].Messages[0].InternalID = ""
				}
				u = coreprotocol.WorkspaceUpdate{Snapshot: &s}
			}
			for _, v := range views {
				decoded := topicFuzzDecode(t, u)
				var err error
				if reset {
					err = v.InstallPublicationSnapshot(*decoded.Snapshot, newDomain)
				} else {
					_, err = v.ApplyPublication(decoded)
				}
				if reject {
					if !errors.Is(err, ErrResync) {
						t.Fatalf("action %d malformed/stale accepted: %v", b%10, err)
					}
				} else if err != nil {
					t.Fatalf("action %d valid update: %v", b%10, err)
				}
			}
			if !reject {
				a = next
			}
			for _, v := range views {
				if v.SessionConnectionIssue("session") != "" {
					t.Fatal("rejected batch partially installed sessions")
				}
			}
			check()
		}
	})
}
