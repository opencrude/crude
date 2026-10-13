package fileio

import (
	"testing"
	"time"
)

func settleObserver(t *testing.T, manager *AsyncCommandManager) <-chan AsyncCmdInfo {
	t.Helper()
	settled := make(chan AsyncCmdInfo, 8)
	manager.SetSettleObserver(func(info AsyncCmdInfo) { settled <- info })
	t.Cleanup(manager.CloseAndWait)
	return settled
}

func awaitSettled(t *testing.T, settled <-chan AsyncCmdInfo) AsyncCmdInfo {
	t.Helper()
	select {
	case info := <-settled:
		return info
	case <-time.After(10 * time.Second):
		t.Fatal("tracked command settled without notifying its owner")
		return AsyncCmdInfo{}
	}
}

func expectNoFurtherSettle(t *testing.T, settled <-chan AsyncCmdInfo) {
	t.Helper()
	select {
	case info := <-settled:
		t.Fatalf("unexpected settle notification: %+v", info)
	case <-time.After(300 * time.Millisecond):
	}
}

// Agents learn that background work finished from this notification instead
// of polling check_async_command; it must fire exactly once with final state.
func TestTrackedCommandReportsTerminalStateOnce(t *testing.T) {
	t.Setenv("CRUDE_ASYNC_ACTION", "fail")
	manager := NewAsyncCommandManager()
	settled := settleObserver(t, manager)
	if _, err := manager.Start("build", asyncHelperCommand(), t.TempDir(), 0); err != nil {
		t.Fatal(err)
	}
	info := awaitSettled(t, settled)
	if info.Handle != "build" || info.Status != AsyncFailed || info.ExitCode != 7 || info.OutputBytes == 0 || info.EndedAt.IsZero() {
		t.Fatalf("settle notification lacks final state: %+v", info)
	}
	expectNoFurtherSettle(t, settled)
}

func TestKilledCommandReportsKillForOwnerPolicy(t *testing.T) {
	manager := NewAsyncCommandManager()
	settled := settleObserver(t, manager)
	if _, err := manager.Start("server", asyncHelperCommand(), t.TempDir(), 0); err != nil {
		t.Fatal(err)
	}
	if err := manager.Kill("server"); err != nil {
		t.Fatal(err)
	}
	if info := awaitSettled(t, settled); info.Status != AsyncKilled || info.Error != "killed" {
		t.Fatalf("explicit kill must be distinguishable from timeout: %+v", info)
	}
}

// A command that finished inside its blocking call was already reported by
// that call's result; only promoted commands become owner notifications.
func TestOnlyPromotedUntrackedCommandsNotify(t *testing.T) {
	t.Setenv("CRUDE_ASYNC_ACTION", "utf8-binary")
	manager := NewAsyncCommandManager()
	settled := settleObserver(t, manager)
	quick, err := manager.StartUntracked(asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	<-quick.Done()
	expectNoFurtherSettle(t, settled)

	// Promotion after the process already exited models the race between the
	// caller's running check and registration.
	late, err := manager.StartUntracked(asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	<-late.Done()
	if _, err := manager.Promote(late, "late"); err != nil {
		t.Fatal(err)
	}
	if info := awaitSettled(t, settled); info.Handle != "late" || info.Status != AsyncCompleted {
		t.Fatalf("promoted command settled during promotion was lost: %+v", info)
	}
	expectNoFurtherSettle(t, settled)
}

// Retaining a finished command's large output must not report an outcome the
// blocking call already returned.
func TestRetainedSettledOutputIsNotReportedAgain(t *testing.T) {
	t.Setenv("CRUDE_ASYNC_ACTION", "large")
	manager := NewAsyncCommandManager()
	settled := settleObserver(t, manager)
	uc, err := manager.StartUntracked(asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	<-uc.Done()
	if _, err := manager.RetainSettled(uc, "retained"); err != nil {
		t.Fatal(err)
	}
	expectNoFurtherSettle(t, settled)
}
