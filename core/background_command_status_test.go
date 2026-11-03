package core

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/fileio"
	"crude/types"
	"crude/workspace"
)

func TestBackgroundCommandStatusCanonicalAcrossShellsAndReconnect(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	r, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
		ws.SessionsByAgent["agent"] = &workspace.Session{SessionID: "session", AgentID: "agent"}
		ws.RegisterAgentRoot("agent")
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	root := t.TempDir()
	attach := func(name string) (*Connection, string) {
		t.Helper()
		c, err := r.Connect(name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Close)
		result := callCore(t, c, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root})
		var attached coreprotocol.AttachResult
		if err := coreprotocol.Decode(result.Payload, &attached); err != nil {
			t.Fatal(err)
		}
		return c, attached.WorkspaceID
	}
	first, id := attach("first-shell")
	second, secondID := attach("second-shell")
	if id != secondID {
		t.Fatal("shells did not share the canonical workspace")
	}
	r.mu.Lock()
	a := r.workspaces[id]
	r.mu.Unlock()
	now := time.Now()
	command := fileio.AsyncCmdInfo{Handle: "same-handle", PID: 123, StartedAt: now.Add(-time.Second), EndedAt: now,
		Status: fileio.AsyncFailed, Command: "TestAppFixtureClosesOriginalWorkspaceAfterPortReplacement", Error: "exit status 1"}
	publish := func(count int) {
		t.Helper()
		if err := a.inspect(t.Context(), func() {
			for range count {
				notice, ok := workspace.NoticeFromEvent(workspace.AgentEvent{Type: workspace.NoticeAsyncCommandStatus, AgentID: "agent", Data: []fileio.AsyncCmdInfo{command}})
				if !ok {
					t.Error("command status did not produce a typed notice")
					return
				}
				a.notice(notice)
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	status := func(c *Connection) coreprotocol.StatusSnapshot {
		t.Helper()
		result := callCore(t, c, "status", id, coreprotocol.StatusSnapshotMethod, coreprotocol.Empty{})
		var snapshot coreprotocol.StatusSnapshot
		if err := coreprotocol.Decode(result.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	publish(1)
	initial := status(first)
	if len(initial.Notifications) != 1 || initial.Notifications[0].Occurrences != 1 {
		t.Fatalf("canonical ingress did not record one failure without shell writes: %+v", initial)
	}
	publish(200)
	if got := status(second); !reflect.DeepEqual(initial, got) {
		t.Fatalf("repeated complete snapshots changed canonical status:\nbefore=%+v\nafter=%+v", initial, got)
	}
	noticeID := initial.Notifications[0].ID
	callCore(t, second, "dismiss", id, coreprotocol.StatusMutateMethod, coreprotocol.StatusMutation{Op: coreprotocol.StatusDismiss, ID: noticeID})
	dismissed := status(first)
	first.Close()
	reconnected, sameID := attach("first-shell")
	if sameID != id {
		t.Fatal("reattach replaced the canonical workspace")
	}
	publish(200)
	if got := status(reconnected); !reflect.DeepEqual(dismissed, got) || !got.Notifications[0].Read || !got.Notifications[0].Dismissed {
		t.Fatalf("reconnect or repeated status reset dismissed evidence: %+v", got)
	}
	command.StartedAt = command.StartedAt.Add(time.Second)
	command.EndedAt = command.EndedAt.Add(time.Second)
	publish(200)
	if got := status(second); len(got.Notifications) != 2 || got.Notifications[0].Occurrences != 1 ||
		got.Notifications[0].Read || got.Notifications[0].ID == noticeID {
		t.Fatalf("new execution reusing a handle lost its own failure: %+v", got)
	}
}

func TestServiceNoticeFeedbackCanonicalAcrossShells(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	r, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
		ws.SessionsByAgent["agent"] = &workspace.Session{SessionID: "session", AgentID: "agent"}
		ws.RegisterAgentRoot("agent")
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	root := t.TempDir()
	clients := make([]*Connection, 2)
	var id string
	for i := range clients {
		c, err := r.Connect([]string{"first", "second"}[i])
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		clients[i] = c
		result := callCore(t, c, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root})
		var attached coreprotocol.AttachResult
		if err := coreprotocol.Decode(result.Payload, &attached); err != nil {
			t.Fatal(err)
		}
		id = attached.WorkspaceID
	}
	r.mu.Lock()
	a := r.workspaces[id]
	r.mu.Unlock()
	notices := []workspace.AgentNotice{
		{Type: workspace.NoticeAttachmentExcluded, AgentID: "agent", Reason: "Attachment could not be read"},
		{Type: workspace.NoticeMemorySaved, AgentID: "agent", Count: 1, Titles: []string{"Saved fact"}},
		{Type: workspace.NoticeResponseContinuationUnavailable, AgentID: "agent", Reason: "response_not_stored"},
	}
	for i := range notices {
		notices[i].Identity = types.RuntimeEventIdentity{EventID: notices[i].Type, OwnerID: "session", AgentID: "agent", RuntimeEpoch: 1, Generation: 1, Sequence: uint64(i + 1)}
	}
	publish := func(count int) {
		t.Helper()
		if err := a.inspect(t.Context(), func() {
			for range count {
				for _, notice := range notices {
					a.notice(notice)
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	status := func(c *Connection) coreprotocol.StatusSnapshot {
		t.Helper()
		result := callCore(t, c, "status", id, coreprotocol.StatusSnapshotMethod, coreprotocol.Empty{})
		var snapshot coreprotocol.StatusSnapshot
		if err := coreprotocol.Decode(result.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	publish(1)
	first := status(clients[0])
	if len(first.Notifications) != 3 {
		t.Fatalf("canonical service feedback missing: %+v", first)
	}
	publish(200)
	second := status(clients[1])
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("multiple shells or repeated exact event changed canonical feedback: %+v", second)
	}
	for _, notice := range second.Notifications {
		if notice.Occurrences != 1 || notice.Owner.SessionID != "session" || notice.Owner.ID != "agent" {
			t.Fatalf("canonical service feedback lost exact source attribution: %+v", notice)
		}
	}
}
