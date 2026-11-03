package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	model "crude/types"
)

func TestProjectWatchLossAndRecovery(t *testing.T) {
	m, r, root, producer := projectFixture(t)
	m.SetProjectProducers([]model.LSPProjectProducer{producer})
	old := startedProject(t, m, r)
	path := filepath.Join(root, "util.go")
	old.emitUnopened(path, LSPDiagnostic{Message: "old problem", Severity: model.DiagError})
	m.SetProjectWatchReliable(false)
	m.SetProjectWatchReliable(false)
	batch, ok := m.FileDiagnostics(path)
	if !ok || batch.State != model.InsightStale || len(batch.Diagnostics) != 1 {
		t.Fatalf("lost evidence after watch failure: %+v", batch)
	}
	if summary := m.ProjectInsights(); summary.DiagnosticsLimited == 0 || !strings.Contains(summary.CoverageDetail, "incomplete") {
		t.Fatalf("watch failure not visible: %+v", summary)
	}
	old.emitUnopened(path)
	if batch, _ = m.FileDiagnostics(path); batch.State != model.InsightStale || len(batch.Diagnostics) != 1 {
		t.Fatalf("old callback replaced stale evidence: %+v", batch)
	}
	m.SetProjectWatchReliable(true)
	m.SetProjectWatchReliable(true)
	awaitLSP(t, func() bool {
		clients := r.snapshot()
		if len(clients) != 2 {
			return false
		}
		clients[1].mu.Lock()
		defer clients[1].mu.Unlock()
		return clients[1].started && clients[1].unopened != nil
	})
	old.emitUnopened(path)
	if batch, _ = m.FileDiagnostics(path); batch.State != model.InsightStale || len(batch.Diagnostics) != 1 {
		t.Fatalf("retired callback replaced evidence: %+v", batch)
	}
	r.snapshot()[1].emitUnopened(path, LSPDiagnostic{Message: "new problem", Severity: model.DiagError})
	if batch, _ = m.FileDiagnostics(path); !batch.FromProjectCheck() || len(batch.Diagnostics) != 1 || batch.Diagnostics[0].Message != "new problem" {
		t.Fatalf("replacement producer did not publish: %+v", batch)
	}
	m.Stop()
	m.SetProjectWatchReliable(false)
	m.SetProjectWatchReliable(true)
	m.mu.Lock()
	paused := m.paused
	m.mu.Unlock()
	if !paused {
		t.Fatal("recovery resumed paused language servers")
	}
	m.Close()
	if got := len(r.snapshot()); got != 2 {
		t.Fatalf("recovery started a producer while paused: %d", got)
	}
	m.SetProjectWatchReliable(false)
}

func TestProjectWatchChangeDuringStartRestartsIndex(t *testing.T) {
	m, r, root, producer := projectFixture(t)
	gate := make(chan struct{})
	r.startGate = gate
	m.SetProjectWatchReliable(false)
	m.SetProjectProducers([]model.LSPProjectProducer{producer})
	if len(r.snapshot()) != 0 {
		t.Fatal("producer started while initial observation was unavailable")
	}
	m.SetProjectWatchReliable(true)
	awaitLSP(t, func() bool { return len(r.snapshot()) == 1 })
	path := filepath.Join(root, "util.go")
	m.NotifyWatchedFilesChanged(context.Background(), path, 2)
	close(gate)
	awaitLSP(t, func() bool {
		clients := r.snapshot()
		if len(clients) < 2 {
			return false
		}
		clients[1].mu.Lock()
		defer clients[1].mu.Unlock()
		return clients[1].started
	})
	first := r.snapshot()[0]
	first.emitUnopened(path, LSPDiagnostic{Message: "obsolete"})
	if _, ok := m.FileDiagnostics(path); ok {
		t.Fatal("initializing producer published after its index was invalidated")
	}
}
