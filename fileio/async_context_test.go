package fileio

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestAsyncContextSnapshotPreservesIdentityStatusAndObservedCursor(t *testing.T) {
	manager := NewAsyncCommandManager()
	started := time.Unix(123, 456)
	state := &asyncCmdState{
		cmd:    exec.CommandContext(context.Background(), "ignored"),
		output: *bytes.NewBufferString("0123456789"),
		info: AsyncCmdInfo{Handle: "build-owned", Command: "go test ./...", PID: 42,
			Status: AsyncRunning, StartedAt: started},
	}
	state.cmd.Dir = "/workspace"
	manager.commands[state.info.Handle] = state
	if output, next, err := manager.ReadOutput(state.info.Handle, 4); err != nil || output != "456789" || next != 10 {
		t.Fatalf("read output=%q next=%d err=%v", output, next, err)
	}
	before := state.readCursor
	snapshot := manager.ContextSnapshot()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	command := snapshot[0]
	if command.Handle != "build-owned" || command.Command != "go test ./..." || command.CWD != "/workspace" ||
		command.PID != 42 || command.Status != AsyncRunning || command.ExitCode != nil || command.ReadCursor == nil ||
		*command.ReadCursor != 10 || command.OutputBytes != 10 || !command.StartedAt.Equal(started) {
		t.Fatalf("command context lost live identity: %+v", command)
	}
	if state.readCursor != before {
		t.Fatal("context snapshot advanced the output cursor")
	}
	state.mu.Lock()
	state.info.Status = AsyncCompleted
	state.info.ExitCode = 7
	state.mu.Unlock()
	command = manager.ContextSnapshot()[0]
	if command.Status != AsyncCompleted || command.ExitCode == nil || *command.ExitCode != 7 {
		t.Fatalf("settled command state was not refreshed: %+v", command)
	}
}
