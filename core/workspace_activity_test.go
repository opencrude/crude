package core

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"crude/core/client"
	"crude/core/protocol"
	"crude/workspace"
	"crude/workspace/document"
)

func TestSavedWorkspaceActivityObservesDetachedWorkWithoutOpeningColdProjects(t *testing.T) {
	runtime, root := sharedDocumentRuntime(t)
	observerRoot, coldRoot := t.TempDir(), t.TempDir()
	cold, err := workspace.OpenWorkspaceDB(coldRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := cold.Close(); err != nil {
		t.Fatal(err)
	}
	const source = "work.txt"
	if err := os.WriteFile(filepath.Join(root, source), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	author, authorID := topicResourceConnection(t, runtime, root, "activity-author")
	observer, observerID := topicResourceConnection(t, runtime, observerRoot, "activity-observer")
	view := &coreclient.WorkspaceView{Client: observer, ID: observerID}
	rootID, coldID := workspace.WorkspaceID(root), workspace.WorkspaceID(coldRoot)
	read := func(wantAgents, wantTerminals, wantEdits int) {
		t.Helper()
		counts, err := view.ReadWorkspaceActivity(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		got := counts[rootID]
		if _, found := counts[coldID]; found || len(runtime.actors()) != 2 {
			t.Fatalf("reading activity opened or guessed state for a cold workspace: %+v", counts)
		}
		if !got.Agents.Observed || got.Agents.Value != wantAgents ||
			!got.Terminals.Observed || got.Terminals.Value != wantTerminals ||
			!got.Unsaved.Observed || got.Unsaved.Value != wantEdits {
			t.Fatalf("workspace activity = %+v, want observed (%d, %d, %d)", got, wantAgents, wantTerminals, wantEdits)
		}
		if current := counts[workspace.WorkspaceID(observerRoot)]; !current.Unsaved.Observed || current.Unsaved.Value != 0 {
			t.Fatalf("background activity leaked into the attached workspace: %+v", current)
		}
	}
	read(0, 0, 0)

	var snapshot document.Snapshot
	resourceTopicCall(t, author, authorID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: source}, &snapshot)
	var edit document.Receipt
	resourceTopicCall(t, author, authorID, coreprotocol.DocumentEdit, &coreprotocol.DocumentEditRequest{
		ID: snapshot.ID, Revision: snapshot.Revision,
		Edits: []document.Edit{{Start: 0, End: len("before"), Text: "after"}},
	}, &edit)
	if !edit.Accepted {
		t.Fatalf("expected accepted unsaved text: %+v", edit)
	}
	t.Setenv("CRUDE_TEST_SILENT_TERMINAL_CHILD", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var terminal workspace.TerminalInfo
	resourceTopicCall(t, author, authorID, coreprotocol.TerminalCreate, &workspace.TerminalCreateOpts{
		Command: executable, Args: []string{"-test.run=^TestResourceEventsSilentTerminalChild$"}, Cols: 40, Rows: 4,
	}, &terminal)
	if terminal.ID == "" || !terminal.Alive {
		t.Fatalf("terminal did not mount: %+v", terminal)
	}
	a := deletionActor(t, runtime, authorID)
	inspectDeletionActor(t, a, func() {
		running := workspace.NewAgentState("running", nil, a.ws.Logger)
		running.Running = true
		a.ws.AgentMgr.AddOrchestrator("running", running)
		a.ws.AgentMgr.AddOrchestrator("idle", workspace.NewAgentState("idle", nil, a.ws.Logger))
	})
	resourceTopicCall(t, author, authorID, coreprotocol.Detach, &coreprotocol.Empty{}, nil)
	read(1, 1, 1)

	author, authorID = topicResourceConnection(t, runtime, root, "activity-author-returned")
	var reopen document.Snapshot
	resourceTopicCall(t, author, authorID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: source}, &reopen)
	if reopen.ID != snapshot.ID || !reopen.Dirty {
		t.Fatalf("return did not reopen the retained document: %+v", reopen)
	}
	var saved document.Snapshot
	resourceTopicCall(t, author, authorID, coreprotocol.DocumentSave, &coreprotocol.DocumentRevisionRequest{
		ID: reopen.ID, Revision: reopen.Revision,
	}, &saved)
	if saved.Dirty || string(saved.Text) != "after" {
		t.Fatalf("saved text still reported dirty: %+v", saved)
	}
	resourceTopicCall(t, author, authorID, coreprotocol.TerminalClose, &coreprotocol.TerminalIDRequest{ID: terminal.ID}, nil)
	inspectDeletionActor(t, a, func() { a.ws.AgentMgr.Remove("running") })
	read(0, 0, 0)

	// A long step on the actor (a busy turn, a slow publication) used to drop
	// the workspace from the overview, so its badges blinked off. The busy
	// actor now reports its last sample.
	inspectDeletionActor(t, a, func() {
		running := workspace.NewAgentState("running", nil, a.ws.Logger)
		running.Running = true
		a.ws.AgentMgr.AddOrchestrator("running", running)
	})
	read(1, 0, 0)
	release, busy := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// A failed read must not leave the actor blocked for runtime cleanup.
	t.Cleanup(unblock)
	go func() {
		_ = a.inspect(context.Background(), func() {
			close(busy)
			<-release
		})
	}()
	<-busy
	read(1, 0, 0)
	unblock()
	inspectDeletionActor(t, a, func() { a.ws.AgentMgr.Remove("running") })
	read(0, 0, 0)
}
