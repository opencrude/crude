package workspace

import (
	"path/filepath"
	"testing"
	"time"

	"crude/types"
)

func TestLSPHealthInstanceTracksStartStopAndReplacement(t *testing.T) {
	m, runtime, root := lifecycleManager(t)
	gate := make(chan struct{})
	runtime.startGate = gate
	editorSource(t, m, filepath.Join(root, "main.go"), "package main\n")

	await := func(match func(types.LSPServerHealth) bool) types.LSPServerHealth {
		t.Helper()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		for {
			_, changed := m.Changed()
			health := m.ServerHealthSnapshots()
			for _, item := range health {
				if match(item) {
					return item
				}
			}
			select {
			case <-changed:
			case <-deadline.C:
				t.Fatalf("language-server health did not settle: %+v", health)
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			}
		}
	}
	starting := await(func(h types.LSPServerHealth) bool { return h.Starting })
	if starting.InstanceID == "" {
		t.Fatal("starting producer has no exact instance")
	}
	close(gate)
	first := await(func(h types.LSPServerHealth) bool { return h.ID == starting.ID && h.Connected })
	if first.InstanceID != starting.InstanceID {
		t.Fatalf("start changed the admitted producer identity: %+v -> %+v", starting, first)
	}
	if !m.StopServer(first.ID) {
		t.Fatal("source server Stop was not admitted")
	}
	for _, health := range m.ServerHealthSnapshots() {
		if health.ID == first.ID && (health.InstanceID != "" || health.Connected) {
			t.Fatalf("stopping producer remained authoritative: %+v", health)
		}
	}
	stopped := await(func(h types.LSPServerHealth) bool { return h.ID == first.ID && h.Status == "Stopped" })
	if stopped.InstanceID != "" || stopped.Connected {
		t.Fatalf("stopped producer retained a live identity: %+v", stopped)
	}
	if !m.RestartServer(first.ID) {
		t.Fatal("stopped launch key could not be restarted")
	}
	replacement := await(func(h types.LSPServerHealth) bool {
		return h.ID == first.ID && h.Connected && h.InstanceID != ""
	})
	if replacement.InstanceID == first.InstanceID {
		t.Fatalf("replacement reused a retired producer identity: %+v -> %+v", first, replacement)
	}
}
