package session

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"crude/fileio"
)

func TestKillAsyncCommandLeavesSessionUsable(t *testing.T) {
	manager := fileio.NewAsyncCommandManager()
	defer manager.CloseAndWait()
	session := &Session{asyncMgr: manager}
	command := fmt.Sprintf(`CRUDE_SESSION_JOB_HELPER=1 %q -test.run=^TestAsyncSessionJobHelper$`, os.Args[0])
	if runtime.GOOS == "windows" {
		command = fmt.Sprintf(`set "CRUDE_SESSION_JOB_HELPER=1"&&"%s" -test.run=^TestAsyncSessionJobHelper$`, os.Args[0])
	}
	started, err := manager.Start("job", command, t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.KillAsyncCommand(started.Ref()); err != nil {
		t.Fatal(err)
	}
	if session.activeTurns.Load() != 0 || session.isHardCancelled() || atomic.LoadInt32(&session.closing) != 0 {
		t.Fatal("command termination changed the session lifecycle")
	}
	if _, err := manager.Start("follow-up", "exit 0", t.TempDir(), 0); err != nil {
		t.Fatalf("session command manager was shut down: %v", err)
	}
	if err := session.KillAsyncCommand(fileio.AsyncCmdRef{Handle: "missing", PID: 1, StartedAt: time.Now()}); !errors.Is(err, fileio.ErrAsyncCommandNotFound) {
		t.Fatalf("missing command returned %v", err)
	}
}

func TestAsyncSessionJobHelper(t *testing.T) {
	if os.Getenv("CRUDE_SESSION_JOB_HELPER") == "1" {
		time.Sleep(30 * time.Second)
	}
}
