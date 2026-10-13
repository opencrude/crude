package remote

import (
	"testing"

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
