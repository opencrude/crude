package remote

import (
	"context"
	"errors"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/workspace"
)

// A terminal closed by Crude control or another shell disappears from the core
// inventory; the replica must unmount it rather than keep a dead dock tab.
func TestTerminalReplicaUnmountsTerminalRemovedFromInventory(t *testing.T) {
	replica := &terminalReplica{}
	replica.install([]workspace.TerminalInfo{{ID: "t1", Alive: true}, {ID: "t2", Alive: true}})
	follow := replica.install([]workspace.TerminalInfo{{ID: "t2", Alive: true}})
	if len(follow) != 1 {
		t.Fatalf("follow = %#v", follow)
	}
	exited, ok := follow[0].(workspace.TerminalExitedMsg)
	if !ok || exited.ID != "t1" {
		t.Fatalf("removed terminal was not unmounted: %#v", follow)
	}
	if _, known := replica.Get("t1"); known {
		t.Fatal("removed terminal retained in replica")
	}
}

func TestTerminalInventoryBarrierRequiresAppliedReplacement(t *testing.T) {
	reset := make(chan struct{})
	w := &Workspace{ctx: t.Context(), topics: map[string]*replicaTopic{
		coreprotocol.TopicTerminalInventory: {applied: true, cancel: func() { close(reset) }},
	}}
	replica := &terminalReplica{w: w, infos: []workspace.TerminalInfo{{ID: "already-known"}}}
	done := make(chan error, 1)
	go func() { done <- replica.AwaitInventory(t.Context()) }()
	select {
	case <-reset:
	case <-time.After(time.Second):
		t.Fatal("inventory barrier did not fence the old applied subscription")
	}
	w.topicMu.Lock()
	replacement := &replicaTopic{cancel: func() {}}
	w.topics[coreprotocol.TopicTerminalInventory] = replacement
	w.notifyTopicUpdated()
	w.topicMu.Unlock()
	select {
	case <-done:
		t.Fatal("a new subscription alone completed the inventory barrier")
	case <-time.After(20 * time.Millisecond):
	}
	w.topicMu.Lock()
	replacement.applied = true
	w.notifyTopicUpdated()
	w.topicMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("applied inventory did not release its waiter")
	}
	if got := replica.List(); len(got) != 1 || got[0].ID != "already-known" {
		t.Fatal("inventory barrier became a second replica writer")
	}
}

func TestTerminalInventoryBarrierCancelsWithCallerOrAttachment(t *testing.T) {
	for _, attachment := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "attachment"}[attachment], func(t *testing.T) {
			owner, stopOwner := context.WithCancel(t.Context())
			defer stopOwner()
			caller, stopCaller := context.WithCancel(t.Context())
			defer stopCaller()
			reset := make(chan struct{})
			w := &Workspace{ctx: owner, topics: map[string]*replicaTopic{
				coreprotocol.TopicTerminalInventory: {cancel: func() { close(reset) }},
			}}
			done := make(chan error, 1)
			go func() { done <- (&terminalReplica{w: w}).AwaitInventory(caller) }()
			select {
			case <-reset:
			case <-time.After(time.Second):
				t.Fatal("barrier never began")
			}
			if attachment {
				stopOwner()
			} else {
				stopCaller()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled barrier result: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled inventory barrier waited on UI")
			}
		})
	}
}
