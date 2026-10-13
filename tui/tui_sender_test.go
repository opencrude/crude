package tui

import (
	"reflect"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	ttypes "crude/types"
	"crude/types/reasoning"
)

func TestTUIEventSenderEnqueuesWhileDeliveryBlockedAndPreservesOrder(t *testing.T) {
	deliveryStarted := make(chan struct{})
	releaseDelivery := make(chan struct{})
	delivered := make(chan string, 3)
	var releaseOnce sync.Once

	pump := newProgramMessagePump(func(msg tea.Msg) {
		event, ok := msg.(ttypes.AgentMsg)
		if !ok {
			t.Errorf("delivered message type = %T, want types.AgentMsg", msg)
			return
		}
		if event.Type == "first" {
			close(deliveryStarted)
			<-releaseDelivery
		}
		delivered <- event.Type
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseDelivery) })
		pump.close()
		pump.wait()
	})

	ui := &TUI{outbound: pump}
	ui.SendAgentEventWithID("first", nil, "agent-1")
	select {
	case <-deliveryStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery did not start")
	}

	enqueueDone := make(chan struct{})
	go func() {
		ui.SendAgentEventWithID("second", nil, "agent-1")
		ui.SendAgentEventWithID("third", nil, "agent-1")
		close(enqueueDone)
	}()
	select {
	case <-enqueueDone:
	case <-time.After(2 * time.Second):
		t.Fatal("event enqueue waited for blocked Bubble Tea delivery")
	}

	releaseOnce.Do(func() { close(releaseDelivery) })
	var got []string
	for range 3 {
		select {
		case typ := <-delivered:
			got = append(got, typ)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for delivery; got %v", got)
		}
	}
	if want := []string{"first", "second", "third"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("delivery order = %v, want %v", got, want)
	}
}

func TestProgramMessagePumpShutdownDrainsAcceptedMessagesAndRejectsNewOnes(t *testing.T) {
	deliveryStarted := make(chan struct{})
	releaseDelivery := make(chan struct{})
	delivered := make(chan int, 3)

	pump := newProgramMessagePump(func(msg tea.Msg) {
		value := msg.(int)
		if value == 1 {
			close(deliveryStarted)
			<-releaseDelivery
		}
		delivered <- value
	})
	t.Cleanup(func() {
		pump.close()
		select {
		case <-pump.done:
		case <-time.After(2 * time.Second):
			t.Error("message pump did not stop")
		}
	})

	if !pump.enqueue(1) {
		t.Fatal("first enqueue was rejected")
	}
	select {
	case <-deliveryStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery did not start")
	}
	if !pump.enqueue(2) {
		t.Fatal("second enqueue was rejected")
	}
	pump.closeAfter(3)
	if pump.enqueue(4) {
		t.Fatal("enqueue succeeded after shutdown began")
	}

	close(releaseDelivery)
	select {
	case <-pump.done:
	case <-time.After(2 * time.Second):
		t.Fatal("message pump did not drain and stop")
	}

	var got []int
	for range 3 {
		got = append(got, <-delivered)
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shutdown delivery order = %v, want %v", got, want)
	}
}

func TestProgramMessagePumpCoalescesOnlyBackpressuredAdjacentDeltas(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	delivered := make(chan tea.Msg, 4)
	pump := newProgramMessagePump(func(msg tea.Msg) {
		if msg == "block" {
			close(started)
			<-release
		}
		delivered <- msg
	})
	t.Cleanup(func() {
		pump.close()
		pump.wait()
	})

	if !pump.enqueue("block") {
		t.Fatal("failed to enqueue blocking message")
	}
	<-started
	for _, text := range []string{"one", " two", " three"} {
		if !pump.enqueue(ttypes.AgentMsg{Type: "thinking", AgentID: "agent", Data: reasoning.Delta{Text: text}}) {
			t.Fatal("failed to enqueue thinking delta")
		}
	}
	if !pump.enqueue(ttypes.AgentMsg{Type: "done", AgentID: "agent", Data: map[string]any{"reason": "cancelled"}}) {
		t.Fatal("failed to enqueue terminal event")
	}
	close(release)

	if got := <-delivered; got != "block" {
		t.Fatalf("first delivery=%v, want block", got)
	}
	merged, ok := (<-delivered).(ttypes.AgentMsg)
	if !ok || merged.Type != "thinking" || merged.Data != (reasoning.Delta{Text: "one two three"}) {
		t.Fatalf("merged delivery=%#v", merged)
	}
	done, ok := (<-delivered).(ttypes.AgentMsg)
	if !ok || done.Type != "done" {
		t.Fatalf("terminal delivery=%#v", done)
	}
}
