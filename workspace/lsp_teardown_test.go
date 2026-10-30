package workspace

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLSPUnverifiedTeardownNeverReleasesProcessAdmission(t *testing.T) {
	for _, reason := range []string{"stop", "connection-death", "initialization-failure"} {
		t.Run(reason, func(t *testing.T) {
			m, r, root := lifecycleManager(t)
			settings := m.Settings()
			settings.MaxServers = 1
			m.SetSettings(settings)
			r.stopErr = errors.New("owned process identity cleanup could not be verified")
			if reason == "initialization-failure" {
				r.startErrs = []error{errors.New("initialize failed")}
			}
			first := filepath.Join(root, "main.go")
			editorSource(t, m, first, "package main")
			awaitLSP(t, func() bool {
				clients := r.snapshot()
				return len(clients) == 1 && (reason == "initialization-failure" || len(clients[0].snapshot()) == 1)
			})
			switch reason {
			case "stop":
				m.StopServer(m.ServerHealthSnapshots()[0].ID)
			case "connection-death":
				close(r.snapshot()[0].done)
			}
			awaitLSP(t, func() bool {
				health := m.ServerHealthSnapshots()
				return len(health) == 1 && strings.Contains(health[0].Error, "teardown is unverified")
			})
			health := m.ServerHealthSnapshots()[0]
			if health.Status != "Failed" || health.Connected || health.Starting {
				t.Fatalf("unproven cleanup looked stopped or ready: %+v", health)
			}
			// Neither disabled admission nor an explicit restart can turn a
			// failed cleanup into a successful release of a real process slot.
			m.Stop()
			if connected, state, detail := m.Status(); connected || state != LSPFailed || !strings.Contains(detail, "process slot is retained") {
				t.Fatalf("Stop hid an unverified lifetime: %v %v %q", connected, state, detail)
			}
			m.RestartServer(health.ID)
			closeEditor(t, m, first)
			editorSource(t, m, filepath.Join(root, "next.py"), "print('next')")
			awaitLSP(t, func() bool {
				for _, server := range m.ServerHealthSnapshots() {
					if server.Status == "Waiting for slot" {
						return true
					}
				}
				return false
			})
			if len(r.snapshot()) != 1 {
				t.Fatal("replacement provider started against an uncertain resource budget")
			}
			m.mu.Lock()
			if m.liveCountLocked() != 1 || m.states[health.ID].client != r.snapshot()[0] {
				t.Error("failed exact owner was replaced or forgotten")
			}
			m.mu.Unlock()
			r.snapshot()[0].emit(r.snapshot()[0].publication(first, []LSPDiagnostic{{Severity: DiagError, Message: "late after failed stop"}}))
			for _, batch := range m.AllDiagnostics() {
				if batch.ID != "" || batch.HasExactSource() {
					t.Fatalf("retired provider resurrected findings: %+v", batch)
				}
			}
			closed := make(chan struct{})
			go func() { m.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("uncertain cleanup caused a worker retry loop on shutdown")
			}
			if !m.ProjectInsights().ServerFailed {
				t.Fatal("closed manager manufactured verified cleanup")
			}
		})
	}
}
