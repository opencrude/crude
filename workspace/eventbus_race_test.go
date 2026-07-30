package workspace

import (
	"fmt"
	"sync"
	"testing"
)

// TestEventBusConcurrentSendAndRecentEvents exercises the workspace→TUI ingress
// path under -race. Callback goroutines must not corrupt the ring-buffer log.
func TestEventBusConcurrentSendAndRecentEvents(t *testing.T) {
	bus := NewEventBus()
	const workers = 32
	const perWorker = 50

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				bus.Send(FileWatcherMsg{
					FilePath:   fmt.Sprintf("/tmp/w%d-%d.go", id, i),
					ChangeType: i % 3,
				})
				_ = bus.RecentEvents(16)
			}
		}(w)
	}
	wg.Wait()

	events := bus.RecentEvents(eventLogCapacity)
	if len(events) == 0 {
		t.Fatal("expected logged events after concurrent Send")
	}
	if len(events) > eventLogCapacity {
		t.Fatalf("RecentEvents returned %d entries, capacity is %d", len(events), eventLogCapacity)
	}
}

// TestEventBusConcurrentSetProgramAndSend verifies SetProgram does not race Send.
func TestEventBusConcurrentSetProgramAndSend(t *testing.T) {
	bus := NewEventBus()
	var wg sync.WaitGroup
	const n = 100

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			bus.SetProgram(nil)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			bus.Send(FileWatcherMsg{FilePath: "x", ChangeType: 1})
			_ = bus.RecentEvents(8)
		}
	}()
	wg.Wait()
}
