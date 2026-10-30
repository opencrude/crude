package remote

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/document"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

func TestInsightQueuedShellMessagesFenceResumeReopenAndDetach(t *testing.T) {
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "test", types.Settings{}, nil)
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}, Memory: types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}})
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	root := t.TempDir()
	path := filepath.Join(root, "shared.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		server, client := net.Pipe()
		go func() { _ = core.ServeConnection(t.Context(), runtime, server) }()
		return coreclient.Dial(ctx, coreclient.SingleCarrier(client), id)
	}
	port, err := Attach(t.Context(), Options{Root: root, Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	defer port.Close()
	messages := make(chan any, 128)
	bus := workspace.NewEventBus()
	bus.SetSink(pushSink{messages})
	port.SetEventBus(bus)
	port.StartReplica()
	snapshot, err := port.OpenDocument(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	next := func() shellport.DocumentInsightMsg {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case message := <-messages:
				switch msg := message.(type) {
				case shellport.DocumentInsightMsg:
					if msg.Valid() {
						return msg
					}
					msg.Done(document.ErrOwner)
				case shellport.LiveDocumentsChangedMsg:
					msg.Done(nil)
				case shellport.ReplicaChangesMsg:
					port.ApplyReplicaChanges(msg)
				}
			case <-timer.C:
				t.Fatal("missing valid pushed insight")
				return shellport.DocumentInsightMsg{}
			}
		}
	}
	held := next()
	if held.Owner != port || held.View.DocumentID != snapshot.ID || held.View.Revision != 1 || held.View.Generation == "" {
		t.Fatalf("wrong shell/document publication: %+v", held.View)
	}
	// A message already delivered but not yet installed by Update cannot survive
	// a workspace generation boundary, even when the same port resumes.
	port.FenceDocumentTopics()
	if held.Valid() {
		t.Fatal("fenced insight remained installable")
	}
	port.ResumeDocumentTopics()
	resumed := next()
	if held.Valid() {
		t.Fatal("resume resurrected the old insight token")
	}
	held.Done(document.ErrOwner)
	held.Done(nil) // completion is idempotent, including stale completions
	// Close and reopen at the *same* canonical revision still create a new
	// presentation lifetime; source revision alone cannot provide this fence.
	if err := port.CloseDocument(t.Context(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if resumed.Valid() {
		t.Fatal("closed document retained an installable insight")
	}
	reopenedSnapshot, err := port.OpenDocument(t.Context(), path)
	if err != nil || reopenedSnapshot.Revision != snapshot.Revision || reopenedSnapshot.ID != snapshot.ID {
		t.Fatalf("reopen=%+v err=%v", reopenedSnapshot, err)
	}
	reopened := next()
	if resumed.Valid() || held.Valid() {
		t.Fatal("same-revision reopen revived old message generations")
	}
	resumed.Done(document.ErrOwner)
	if err := port.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened.Valid() {
		t.Fatal("detached shell retained an installable insight")
	}
	reopened.Done(document.ErrOwner)
}
