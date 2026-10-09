package viewmodel

import (
	"fmt"
	"sync"
	"testing"

	"crude/workspace"
)

// TestEventBusConcurrentSendAndRecentEventsDuringTeardown mirrors the
// workspace ingress path used by App callbacks while SetSink toggles.
func TestEventBusConcurrentSendAndRecentEventsDuringTeardown(t *testing.T) {
	bus := workspace.NewEventBus()
	const workers = 24
	const perWorker = 40

	var wg sync.WaitGroup
	wg.Add(workers + 1)
	go func() {
		defer wg.Done()
		for i := 0; i < perWorker; i++ {
			bus.SetSink(nil)
		}
	}()
	for w := 0; w < workers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				bus.Send(workspace.FileWatcherMsg{
					FilePath:   fmt.Sprintf("/tmp/vm-%d-%d.go", id, i),
					ChangeType: i % 3,
				})
				_ = bus.RecentEvents(16)
			}
		}(w)
	}
	wg.Wait()

	if events := bus.RecentEvents(32); len(events) == 0 {
		t.Fatal("expected logged events after concurrent Send during SetSink toggles")
	}
}
