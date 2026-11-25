package viewmodel

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"crude/core"
	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/core/shellport/remote"
)

func TestClientTreeCollapsedListingsSurviveReconnect(t *testing.T) {
	// Top-level scopes watch only browsed directories. A recursive project
	// watcher would continue to send events and hide a missing subscription.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("CRUDE_HOME", t.TempDir())
	parent := filepath.Join(root, "parent")
	nested := filepath.Join(parent, "nested")
	unopened := filepath.Join(parent, "unopened")
	for _, dir := range []string{nested, unopened} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	serverCtx, stop := context.WithCancel(context.Background())
	var servers sync.WaitGroup
	t.Cleanup(func() { stop(); servers.Wait() })
	var first net.Conn
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		server, client := net.Pipe()
		if first == nil {
			first = server
		}
		servers.Add(1)
		go func() { defer servers.Done(); _ = core.ServeConnection(serverCtx, runtime, server) }()
		return coreclient.Dial(ctx, coreclient.SingleCarrier(client), id)
	}
	port, err := remote.Attach(t.Context(), remote.Options{Root: root, Entry: coreprotocol.EntryIntent{Explicit: true}, Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(port, nil)
	fifo := make(chan any, 1024)
	registerLiveTestFIFO(t, app, fifo)
	app.eventBus.SetSink(remoteJourneySink{fifo})
	port.StartReplica()
	t.Cleanup(func() { app.StopAsyncIO(); _ = app.CloseWorkspaceAttachment() })
	shells := []remoteJourneyShell{{app, fifo}}
	tree := app.Tree.Model
	remoteJourneyUntil(t, shells, func() bool { return tree.NodeByPath(parent) != nil })
	runAppCommand(t, app, tree.Expand(parent))
	runAppCommand(t, app, tree.Expand(nested))
	if node := tree.NodeByPath(nested); node == nil || node.Children == nil {
		t.Fatal("empty nested listing was not loaded")
	}
	tree.Collapse(nested)
	tree.Collapse(parent)

	before := port.DocumentOwner()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// This root entry proves the reattached shell has applied a new listing,
	// not just reestablished the carrier.
	marker := filepath.Join(root, "after-disconnect.txt")
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	remoteJourneyUntil(t, shells, func() bool {
		return port.DocumentOwner().Epoch > before.Epoch && tree.NodeByPath(marker) != nil
	})
	if slices.Contains(tree.VisibleRows(), nested) {
		t.Fatal("reconnect expanded a collapsed directory")
	}

	fresh := filepath.Join(nested, "external.txt")
	if err := os.WriteFile(fresh, nil, 0600); err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, app, tree.Expand(parent))
	runAppCommand(t, app, tree.Expand(nested))
	remoteJourneyUntil(t, shells, func() bool { return slices.Contains(tree.VisibleRows(), fresh) })
	// A peer may browse other directories. Its file events do not authorize
	// this shell to start reading subtrees it has never opened.
	_, cmd := app.Update(shellport.ClientFilesChangedMsg{Owner: port, Changes: []coreprotocol.FileChange{{Path: filepath.Join(unopened, "peer.txt")}}})
	runAppCommand(t, app, cmd)
	if node := tree.NodeByPath(unopened); node == nil || node.Children != nil {
		t.Fatal("loaded an unopened subtree")
	}
}
