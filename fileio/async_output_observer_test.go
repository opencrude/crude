package fileio

import (
	"errors"
	"testing"
	"time"
)

func TestOutputObserverDoesNotConsumeAndFencesProcessLifetime(t *testing.T) {
	m := NewAsyncCommandManager()
	info := AsyncCmdInfo{Handle: "build", PID: 42, StartedAt: time.Now(), Status: AsyncRunning}
	state := &asyncCmdState{info: info}
	m.commands[info.Handle] = state
	ref := info.Ref()
	_, _, _, changed, err := m.ObserveOutput(ref, 0, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("output did not wake the viewer")
	}
	data, total, _, settled, err := m.ObserveOutput(ref, 0, 1024)
	if err != nil || string(data) != "first\n" || total != 6 {
		t.Fatalf("observed %q, total %d: %v", data, total, err)
	}
	if state.readCursor != 0 || state.outputCursor != 0 {
		t.Fatal("watching consumed the model's output")
	}
	state.mu.Lock()
	state.finishOutcome(nil, nil)
	state.mu.Unlock()
	select {
	case <-settled:
	default:
		t.Fatal("a silent completion did not wake the viewer")
	}
	_, _, final, removed, err := m.ObserveOutput(ref, total, 1024)
	if err != nil || final.Status != AsyncCompleted {
		t.Fatal("observer did not see settled status")
	}
	m.Cleanup(info.Handle)
	select {
	case <-removed:
	default:
		t.Fatal("removal did not wake the viewer")
	}
	info.StartedAt = info.StartedAt.Add(time.Second)
	m.commands[info.Handle] = &asyncCmdState{info: info}
	if _, _, _, _, err := m.ObserveOutput(ref, 0, 1024); !errors.Is(err, ErrAsyncCommandReplaced) {
		t.Fatal("a reused handle accepted an old observer")
	}
}
