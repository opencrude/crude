package workspace

import (
	"errors"
	"testing"
	"time"

	"crude/fileio"
	"crude/session"
)

func TestAsyncCommandTerminationRequiresExactLiveOwner(t *testing.T) {
	manager := NewAgentManager()
	manager.AddOrchestrator("root", &AgentState{})
	manager.AddSubAgent("child", "root", "Child", "", "", &AgentState{})
	workspace := &Workspace{AgentMgr: manager, SessionsByAgent: map[string]*Session{"root": {backend: &session.Session{}}}}
	ref := fileio.AsyncCmdRef{Handle: "job", PID: 123, StartedAt: time.Now()}
	for _, id := range []string{"", "missing", "child"} {
		if terminate, err := workspace.PrepareAsyncCommandTermination(id, ref); terminate != nil || err == nil {
			t.Fatalf("owner=%q prepared unowned termination", id)
		}
	}
	terminate, err := workspace.PrepareAsyncCommandTermination("root", ref)
	if err != nil || terminate == nil {
		t.Fatalf("live owner rejected: %v", err)
	}
	workspace.AgentMgr.Remove("root")
	workspace.SessionsByAgent = nil
	if err := terminate(); !errors.Is(err, fileio.ErrAsyncCommandNotFound) {
		t.Fatalf("captured command retargeted after teardown: %v", err)
	}
}
