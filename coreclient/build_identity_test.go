package coreclient

import (
	"context"
	"testing"
	"time"
)

func TestNativeCoreBuildComesFromHandshakeNotClient(t *testing.T) {
	f := &fakeCore{t: t, buildID: "core-native-0123456789", version: "v0.7.0"}
	c := dial(t, f)
	if c.CoreBuildID() != f.buildID || c.CoreVersion() != f.version {
		t.Fatalf("client did not retain the core build: %s / %s", c.CoreVersion(), c.CoreBuildID())
	}
	f.mu.Lock()
	f.buildID, f.version = "replacement-core-abcdef", "v0.8.0-rc.1"
	f.mu.Unlock()
	f.dropCarriers()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-c.Done():
		if c.CoreBuildID() != "core-native-0123456789" || c.CoreVersion() != "v0.7.0" {
			t.Fatal("old session was silently relabelled with a new core")
		}
	case <-ctx.Done():
		t.Fatal("changed native core identity incorrectly resumed the old session")
	}
	replacement := dial(t, f)
	if replacement.CoreBuildID() != "replacement-core-abcdef" || replacement.CoreVersion() != "v0.8.0-rc.1" {
		t.Fatalf("fresh handshake did not observe the new core: %s / %s", replacement.CoreVersion(), replacement.CoreBuildID())
	}
}
