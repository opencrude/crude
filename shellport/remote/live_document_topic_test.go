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

func TestLiveDocumentTopicFenceResumeAndCloseTokens(t *testing.T) {
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
	path := filepath.Join(root, "doc.txt")
	if err = os.WriteFile(path, []byte("abc"), 0600); err != nil {
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
	ch := make(chan any, 128)
	bus := workspace.NewEventBus()
	bus.SetSink(pushSink{ch})
	port.SetEventBus(bus)
	port.StartReplica()
	snapshot, err := port.OpenDocument(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	next := func() shellport.LiveDocumentsChangedMsg {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case msg := <-ch:
				if topic, ok := msg.(shellport.LiveDocumentsChangedMsg); ok {
					return topic
				}
				if batch, ok := msg.(shellport.ReplicaChangesMsg); ok {
					port.ApplyReplicaChanges(batch)
				}
			case <-deadline.C:
				t.Fatal("missing document push")
				return shellport.LiveDocumentsChangedMsg{}
			}
		}
	}
	held := next()
	if !held.Valid() {
		t.Fatal("fresh topic not valid")
	}
	// A queued message not yet seen by Update must be invalid immediately.
	port.FenceDocumentTopics()
	if held.Valid() {
		t.Fatal("lifecycle fence retained queued token")
	}
	port.ResumeDocumentTopics()
	resumed := next()
	if !resumed.Valid() || held.Valid() {
		t.Fatal("switch-back revived old generation")
	}
	held.Done(document.ErrOwner)
	resumed.Done(nil)
	if err = port.CloseDocument(t.Context(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if resumed.Valid() {
		t.Fatal("unmounted topic remained valid")
	}
	if _, err = port.OpenDocument(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	reopened := next()
	if !reopened.Valid() || resumed.Valid() {
		t.Fatal("reopen reused unmounted token")
	}
	reopened.Done(nil)
}
