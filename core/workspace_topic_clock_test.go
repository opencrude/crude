package core

import (
	"fmt"
	"testing"
	"time"

	"crude/coreprotocol"
)

func TestWorkspacePublicationClockSleepsUntilAcceptedMutation(t *testing.T) {
	r := failureRuntime(t)
	c, ws := resourceConnection(t, r, t.TempDir(), "publication-clock")
	c.mu.Lock()
	a := c.attached[ws]
	c.mu.Unlock()
	if err := a.inspect(t.Context(), func() {
		if err := a.flushTopic(); err != nil {
			t.Fatal(err)
		}
		if a.topic.ready != nil {
			t.Error("idle workspace has a publication wakeup")
		}
		before := a.revision
		for i := range 100 {
			a.broadcast(coreprotocol.Change{Files: []coreprotocol.FileChange{{Path: fmt.Sprintf("accepted-%d", i)}}})
		}
		if a.revision != before+100 || len(a.topic.pending.Files) != 100 {
			t.Error("publication pacing delayed or dropped authoritative mutations")
		}
		if a.topic.ready == nil {
			t.Error("mutation did not arm its publication opportunity")
		}
		if err := a.flushTopic(); err != nil {
			t.Fatal(err)
		}
		if a.topic.ready != nil {
			t.Error("completed publication kept an idle clock armed")
		}
		entry, _, _ := a.topic.hub.next(a.topic.hub.sequence() - 1)
		var update coreprotocol.WorkspaceUpdate
		if err := coreprotocol.Decode(entry.body, &update); err != nil {
			t.Fatal(err)
		}
		if len(update.Files) != 100 {
			t.Fatal("published facts lost", len(update.Files))
		}
		for i, file := range update.Files {
			if file.Path != fmt.Sprintf("accepted-%d", i) {
				t.Fatal("publication reordered accepted facts")
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	// Exercise the real actor select, not only the timer's bookkeeping. A new
	// burst must publish without a snapshot/read command triggering a flush.
	if err := a.inspect(t.Context(), func() { a.broadcast(coreprotocol.Change{Files: []coreprotocol.FileChange{{Path: "next-burst"}}}) }); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		entry, wake, _ := a.topic.hub.next(a.topic.hub.sequence() - 1)
		var update coreprotocol.WorkspaceUpdate
		if coreprotocol.Decode(entry.body, &update) == nil && len(update.Files) == 1 && update.Files[0].Path == "next-burst" {
			break
		}
		select {
		case <-wake:
		case <-deadline.C:
			t.Fatal("on-demand publication did not reach subscriber")
		}
	}
	if err := a.inspect(t.Context(), func() {
		if a.topic.ready != nil || a.topic.dirty {
			t.Error("publication timer remained live after burst")
		}
	}); err != nil {
		t.Fatal(err)
	}
}
