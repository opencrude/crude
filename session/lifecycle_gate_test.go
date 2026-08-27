package session

import (
	"testing"
	"time"
)

func TestTurnGateRejectsSlashMutationsWithoutBlocking(t *testing.T) {
	session := &Session{}
	session.turnGate.RLock()
	defer session.turnGate.RUnlock()

	compactDone := make(chan string, 1)
	go func() { compactDone <- session.HandleCompact().Error }()
	select {
	case err := <-compactDone:
		if err == "" {
			t.Fatal("compact overlapped an active turn")
		}
	case <-time.After(time.Second):
		t.Fatal("compact blocked behind the turn instead of rejecting it")
	}

	undoDone := make(chan string, 1)
	go func() { undoDone <- session.HandleUndo("/undo").Error }()
	select {
	case err := <-undoDone:
		if err == "" {
			t.Fatal("undo overlapped an active turn")
		}
	case <-time.After(time.Second):
		t.Fatal("undo blocked behind the turn instead of rejecting it")
	}
}
