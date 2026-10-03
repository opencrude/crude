package fileio

import (
	"errors"
	"testing"
	"time"
)

func waitAsyncCommand(t *testing.T, manager *AsyncCommandManager, handle string) AsyncCmdInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, ok := manager.Check(handle)
		if !ok {
			t.Fatalf("command %q disappeared", handle)
		}
		if info.Status != AsyncRunning {
			return *info
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("command %q remained running", handle)
	return AsyncCmdInfo{}
}

func TestKillRefStopsOnlySelectedCommand(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	selected, err := manager.Start("selected", asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	neighbor, err := manager.Start("neighbor", asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.KillRef(selected.Ref()); err != nil {
		t.Fatal(err)
	}
	if info := waitAsyncCommand(t, manager, selected.Handle); info.Status != AsyncKilled {
		t.Fatalf("selected command status=%s, want killed", info.Status)
	}
	if info, ok := manager.Check(neighbor.Handle); !ok || info.Status != AsyncRunning {
		t.Fatalf("neighbor was affected: %+v", info)
	}
	if _, err := manager.Start("new-command", "exit 0", t.TempDir(), 0); err != nil {
		t.Fatalf("terminating one command closed its manager: %v", err)
	}
	if info := waitAsyncCommand(t, manager, "new-command"); info.Status != AsyncCompleted {
		t.Fatalf("manager cannot run follow-up commands: %+v", info)
	}
}

func TestKillRefRejectsWrongAndReplacedIdentity(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	first, err := manager.Start("reused", asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []AsyncCmdRef{
		{Handle: first.Handle, PID: first.PID + 1, StartedAt: first.StartedAt},
		{Handle: first.Handle, PID: first.PID, StartedAt: first.StartedAt.Add(time.Second)},
		{Handle: first.Handle},
	} {
		if err := manager.KillRef(ref); !errors.Is(err, ErrAsyncCommandReplaced) {
			t.Fatalf("invalid identity returned %v", err)
		}
		if info, _ := manager.Check(first.Handle); info.Status != AsyncRunning {
			t.Fatalf("invalid identity killed command: %+v", info)
		}
	}
	if err := manager.KillRef(first.Ref()); err != nil {
		t.Fatal(err)
	}
	waitAsyncCommand(t, manager, first.Handle)
	manager.Cleanup(first.Handle)
	replacement, err := manager.Start(first.Handle, asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.KillRef(first.Ref()); !errors.Is(err, ErrAsyncCommandReplaced) {
		t.Fatalf("stale reference returned %v", err)
	}
	if info, _ := manager.Check(replacement.Handle); info.Status != AsyncRunning {
		t.Fatalf("replacement was killed by stale row: %+v", info)
	}
}

func TestKillRefRefusesCompletedOrMissingCommands(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	started, err := manager.Start("done", "exit 0", t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	completed := waitAsyncCommand(t, manager, started.Handle)
	if err := manager.KillRef(started.Ref()); !errors.Is(err, ErrAsyncCommandNotRunning) {
		t.Fatalf("completed reference returned %v", err)
	}
	if err := manager.Kill(started.Handle); err != nil {
		t.Fatalf("idempotent tool kill returned %v", err)
	}
	if got, _ := manager.Check(started.Handle); got.Status != completed.Status || !got.EndedAt.Equal(completed.EndedAt) {
		t.Fatalf("completed command was mutated: %+v", got)
	}
	if err := manager.KillRef(AsyncCmdRef{Handle: "missing"}); !errors.Is(err, ErrAsyncCommandNotFound) {
		t.Fatalf("missing reference returned %v", err)
	}
}

func TestKillRefCannotTargetAnotherManager(t *testing.T) {
	first, second := NewAsyncCommandManager(), NewAsyncCommandManager()
	defer first.CloseAndWait()
	defer second.CloseAndWait()
	left, err := first.Start("same", asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.Start("same", asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.KillRef(left.Ref()); !errors.Is(err, ErrAsyncCommandReplaced) {
		t.Fatalf("wrong manager returned %v", err)
	}
	if info, _ := second.Check(right.Handle); info.Status != AsyncRunning {
		t.Fatalf("wrong manager killed another command: %+v", info)
	}
}

func TestSettledCommandIsNeverSignalledAgain(t *testing.T) {
	manager := NewAsyncCommandManager()
	started, err := manager.Start("done", "exit 0", t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	state := manager.commands[started.Handle]
	manager.mu.Unlock()
	<-state.done
	signals := 0
	state.tree = &ownedCommandTree{kill: func() error { signals++; return nil }}
	if err := manager.Kill(started.Handle); err != nil {
		t.Fatal(err)
	}
	manager.CloseAndWait()
	if signals != 0 {
		t.Fatalf("settled command received %d stale signals", signals)
	}
}
