package coreclient

import (
	"context"
	"testing"
	"time"
)

func TestNativeCoreBuildComesFromHandshakeNotClient(t *testing.T) {
	f := &fakeCore{t: t, buildID: "core-native-0123456789"}
	c := dial(t, f)
	if c.CoreBuildID() != f.buildID {
		t.Fatal("client invented the core image identity", c.CoreBuildID())
	}
	f.mu.Lock()
	f.buildID = "replacement-core-abcdef"
	f.mu.Unlock()
	f.dropCarriers()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-c.Done():
		if c.CoreBuildID() != "core-native-0123456789" {
			t.Fatal("old session was silently relabelled with a new core")
		}
	case <-ctx.Done():
		t.Fatal("changed native core identity incorrectly resumed the old session")
	}
}
