package remote

import (
	"context"
	"errors"
	"testing"
	"time"

	"crude/workspace"
)

type documentMessageSink struct{ messages chan any }

func (sink documentMessageSink) Send(msg any) { sink.messages <- msg }

func TestDocumentDeadlinesRejectReplacementCancellationAndClose(t *testing.T) {
	s := newDocumentService(nil)
	t.Cleanup(s.Close)
	s.Schedule("buffer", "old-path", 1, time.Hour)
	first := s.deadlines["buffer"].due
	s.Schedule("buffer", "new-path", 2, time.Hour)
	second := s.deadlines["buffer"].due
	if s.AcceptDue(first) {
		t.Fatal("accepted replaced deadline")
	}
	wrong := second
	wrong.Revision++
	if s.AcceptDue(wrong) {
		t.Fatal("accepted mismatched deadline")
	}
	if !s.AcceptDue(second) || s.AcceptDue(second) {
		t.Fatal("current deadline was not consumed exactly once")
	}
	s.Schedule("buffer", "new-path", 3, time.Hour)
	cancelled := s.deadlines["buffer"].due
	s.Cancel("buffer")
	if s.AcceptDue(cancelled) {
		t.Fatal("accepted cancelled deadline")
	}
	s.Schedule("buffer", "new-path", 4, time.Hour)
	closed := s.deadlines["buffer"].due
	s.Close()
	s.Schedule("buffer", "new-path", 5, time.Hour)
	if s.AcceptDue(closed) || len(s.deadlines) != 0 {
		t.Fatal("closed service retained deadline")
	}
	if result := s.Save(context.Background(), workspace.DocumentSaveRequest{}); !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("closed service attempted RPC: %v", result.Err)
	}
}

func TestDocumentTimerPostsOrdinaryDueMessageWithoutRPC(t *testing.T) {
	bus := workspace.NewEventBus()
	messages := make(chan any, 4)
	bus.SetSink(documentMessageSink{messages})
	w := &Workspace{busSet: make(chan struct{}), stop: make(chan struct{})}
	w.SetEventBus(bus)
	w.StartReplica()
	s := newDocumentService(w)
	t.Cleanup(s.Close)
	// There is deliberately no client: scheduling must work without an RPC.
	s.Schedule("buffer", "remote-path", 9, time.Millisecond)
	select {
	case msg := <-messages:
		due, ok := msg.(workspace.DocumentAutosaveDueMsg)
		if !ok || due.BufferID != "buffer" || due.Path != "remote-path" || due.Revision != 9 || !s.AcceptDue(due) {
			t.Fatalf("incorrect timer delivery: %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timer did not deliver due message")
	}
}
