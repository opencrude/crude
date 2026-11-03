package remote

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/types"
	"crude/workspace"
)

func TestGitSnapshotRefreshReadsRepositoryWithoutReplicaDelivery(t *testing.T) {
	t.Setenv("CRUDE_HOME", t.TempDir())
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		command := append([]string{"-C", root, "-c", "user.name=Crude Test", "-c", "user.email=crude@example.test"}, args...)
		if out, err := exec.Command("git", command...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init")
	run("switch", "-c", "work")
	path := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "--", "tracked.txt")
	run("commit", "-m", "initial")
	if err := os.WriteFile(path, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, nil)
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}, Memory: types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}})
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	defer workers.Wait()
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		server, client := net.Pipe()
		workers.Add(1)
		go func() { defer workers.Done(); _ = core.ServeConnection(ctx, runtime, server) }()
		return coreclient.Dial(ctx, coreclient.SingleCarrier(client), id)
	}
	w, err := Attach(ctx, Options{Root: root, Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	messages := make(chan any, 256)
	bus := workspace.NewEventBus()
	bus.SetSink(pushSink{messages})
	w.SetEventBus(bus)
	w.StartReplica()
	pushUntil(t, w, messages, func() bool { return w.gitState != nil && len(w.gitState.Snapshot.Changes) == 1 })
	initial, err := w.Git().Snapshot(ctx)
	if err != nil || len(initial.Changes) != 1 {
		t.Fatalf("initial snapshot: %+v, %v", initial, err)
	}

	// An external commit advances Git while the UI is busy. Deliberately do
	// not apply any more topic batches: Refresh must request current core
	// evidence, not reread the shell's last delivered cache.
	run("add", "--", "tracked.txt")
	staged, err := w.Git().Snapshot(ctx)
	if err != nil || len(staged.StagedPaths()) != 1 {
		t.Fatalf("external staging did not reach a fresh core read: %+v, %v", staged, err)
	}
	if err := w.Git().Unstage(ctx, "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	unstaged, err := w.Git().Snapshot(ctx)
	if err != nil || len(unstaged.StagedPaths()) != 0 || len(unstaged.Changes) != 1 {
		t.Fatalf("external unstaging did not reach a fresh core read: %+v, %v", unstaged, err)
	}
	run("add", "--", "tracked.txt")
	run("commit", "-m", "external commit")
	run("switch", "-c", "other")
	fresh, err := w.Git().Snapshot(ctx)
	if err != nil || fresh.Branch != "other" || fresh.Head == initial.Head || len(fresh.Changes) != 0 {
		t.Fatalf("Refresh returned the pre-commit shell cache: %+v, %v", fresh, err)
	}
	if w.gitState.Snapshot.Head != initial.Head {
		t.Fatal("an RPC wrote the UI-owned replica behind Update")
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := w.Git().Snapshot(cancelled); err == nil {
		t.Fatal("cancelled Refresh returned cached success")
	}
}
