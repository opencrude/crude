package workspace

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type eventBusRecordingSink struct {
	mu       sync.Mutex
	messages []any
}

func (s *eventBusRecordingSink) Send(msg any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
}

func (s *eventBusRecordingSink) snapshot() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]any(nil), s.messages...)
}

func TestEventBusNeutralSinkReplacementAndClear(t *testing.T) {
	bus := NewEventBus()
	bus.Send("before attachment")
	first, second := &eventBusRecordingSink{}, &eventBusRecordingSink{}
	bus.SetSink(first)
	msg := FileWatcherMsg{FilePath: "example.go", ChangeType: 2}
	bus.Send(msg)
	bus.SetSink(second)
	bus.Send(42)
	bus.SetSink(nil)
	bus.Send("after clear")

	if got := first.snapshot(); !reflect.DeepEqual(got, []any{msg}) {
		t.Fatalf("first sink received %#v, want only %#v", got, msg)
	}
	if got := second.snapshot(); !reflect.DeepEqual(got, []any{42}) {
		t.Fatalf("second sink received %#v, want only 42", got)
	}
	events := bus.RecentEvents(10)
	if len(events) != 4 {
		t.Fatalf("logged %d events, want 4 including unattached sends", len(events))
	}
	if !strings.Contains(events[0], "string") || !strings.Contains(events[2], "file=example.go change=2") {
		t.Fatalf("unexpected newest-first event details: %v", events)
	}
}

type eventBusReentrantSink struct {
	bus         *EventBus
	replacement MessageSink
	logged      []string
}

func (s *eventBusReentrantSink) Send(any) {
	s.logged = s.bus.RecentEvents(1)
	s.bus.SetSink(s.replacement)
	s.bus.Send("nested")
}

func TestEventBusSinkMayReenter(t *testing.T) {
	bus := NewEventBus()
	replacement := &eventBusRecordingSink{}
	sink := &eventBusReentrantSink{bus: bus, replacement: replacement}
	bus.SetSink(sink)
	done := make(chan struct{})
	go func() {
		bus.Send(FileWatcherMsg{FilePath: "outer.go"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sink reentry blocked; delivery must not hold the bus lock")
	}
	if len(sink.logged) != 1 || !strings.Contains(sink.logged[0], "file=outer.go") {
		t.Fatalf("outer message was not logged before delivery: %v", sink.logged)
	}
	if got := replacement.snapshot(); !reflect.DeepEqual(got, []any{"nested"}) {
		t.Fatalf("replacement received %#v, want nested message", got)
	}
}
