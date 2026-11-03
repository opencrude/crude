package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/fileio"
	model "crude/types"
)

func waitDiscoveryPlan(t *testing.T, m *LSPManager, after uint64, languages int) model.AssemblyPlan {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		_, changed := m.Changed()
		plan := m.Assembly()
		if plan.Generation > after && plan.State == model.AssemblyReady && len(plan.Languages) == languages {
			return plan
		}
		select {
		case <-changed:
		case <-timer.C:
			t.Fatalf("discovery did not refresh: %+v", plan)
		}
	}
}

func TestAssemblyWatchRefreshesIgnoreAndSourceTopology(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.php")
	writeTree(t, path, "<?php")
	m := NewLSPManager(nil)
	m.SetWorkspaceRoot(root)
	t.Cleanup(m.Close)
	m.RefreshAssembly()
	first := waitDiscoveryPlan(t, m, 0, 1)
	ignore := filepath.Join(root, ".gitignore")
	writeTree(t, ignore, "*.php\n")
	m.NotifyWatchedFilesChanged(context.Background(), ignore, 1)
	second := waitDiscoveryPlan(t, m, first.Generation, 0)
	writeTree(t, ignore, "# no exclusion\n")
	m.NotifyWatchedFilesChanged(context.Background(), ignore, 2)
	third := waitDiscoveryPlan(t, m, second.Generation, 1)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	m.NotifyWatchedFilesChanged(context.Background(), path, 3)
	fourth := waitDiscoveryPlan(t, m, third.Generation, 0)
	writeTree(t, path, "<?php")
	m.NotifyWatchedFilesChanged(context.Background(), path, 1)
	waitDiscoveryPlan(t, m, fourth.Generation, 1)
}

func TestAssemblyWatchRecoveryRefreshesDiscovery(t *testing.T) {
	root := t.TempDir()
	writeTree(t, filepath.Join(root, "source.php"), "<?php")
	m := NewLSPManager(nil)
	m.SetWorkspaceRoot(root)
	t.Cleanup(m.Close)
	m.RefreshAssembly()
	first := waitDiscoveryPlan(t, m, 0, 1)
	m.SetProjectWatchReliable(false)
	writeTree(t, filepath.Join(root, ".gitignore"), "*.php\n")
	m.SetProjectWatchReliable(true)
	waitDiscoveryPlan(t, m, first.Generation, 0)
}

func TestAssemblyWatchBoundaryKeepsDiscoveryProgress(t *testing.T) {
	for _, boundary := range []string{"policy", "runtime", "root"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, filepath.Join(root, "source.php"), "<?php")
			m := NewLSPManager(nil)
			m.SetWorkspaceRoot(root)
			t.Cleanup(m.Close)
			m.RefreshAssembly()
			first := waitDiscoveryPlan(t, m, 0, 1)
			// Recovery cancels the old scan and queues one generation-owned
			// refresh. A policy/runtime/root change must own its replacement,
			// not strand the published plan in "scanning".
			m.SetProjectWatchReliable(false)
			m.SetProjectWatchReliable(true)
			switch boundary {
			case "policy":
				identity, err := fileio.DirectoryIdentity(root)
				if err != nil {
					t.Fatal(err)
				}
				m.SetAnalysisPolicy(model.WorkspaceAnalysisSettings{Trust: model.AnalysisTrusted}, identity)
			case "runtime":
				m.SetRuntime(&lifecycleRuntime{})
			case "root":
				root = t.TempDir()
				writeTree(t, filepath.Join(root, "replacement.php"), "<?php")
				m.SetWorkspaceRoot(root)
			}
			plan := waitDiscoveryPlan(t, m, first.Generation, 1)
			if !pathWithin(root, plan.Languages[0].Sample) {
				t.Fatalf("replacement discovery kept the old root: %+v", plan)
			}
		})
	}
}

func TestAssemblyWatchDebouncesAndRejectsStaleDiscovery(t *testing.T) {
	root := t.TempDir()
	writeTree(t, filepath.Join(root, "source.php"), "<?php")
	m := NewLSPManager(nil)
	m.SetWorkspaceRoot(root)
	t.Cleanup(m.Close)
	m.RefreshAssembly()
	first := waitDiscoveryPlan(t, m, 0, 1)
	m.mu.Lock()
	staleDiscovery := m.discovery
	m.mu.Unlock()
	ignore := filepath.Join(root, ".gitignore")
	writeTree(t, ignore, "*.php\n")
	for range 100 {
		m.NotifyWatchedFilesChanged(context.Background(), ignore, 2)
	}
	m.publishAssembly(context.Background(), staleDiscovery, first)
	m.mu.Lock()
	if m.discovery == staleDiscovery || m.assembly.State != model.AssemblyScanning {
		m.mu.Unlock()
		t.Fatal("pre-event scan reinstalled stale evidence during debounce")
	}
	requested := m.assemblyRequested
	m.mu.Unlock()
	after := waitDiscoveryPlan(t, m, first.Generation, 0)
	if after.Generation != requested+1 {
		t.Fatalf("burst started more than one rescan: generation=%d want=%d", after.Generation, requested+1)
	}
}

func TestAssemblyWatchRejectsExpiredAndIrrelevantInputsAndCancelsOnClose(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.php")
	writeTree(t, path, "<?php")
	m := NewLSPManager(nil)
	m.SetWorkspaceRoot(root)
	defer m.Close()
	m.RefreshAssembly()
	first := waitDiscoveryPlan(t, m, 0, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.NotifyWatchedFilesChanged(ctx, filepath.Join(root, ".gitignore"), 1)
	m.NotifyWatchedFilesChanged(context.Background(), path, 2) // Content only.
	m.NotifyWatchedFilesChanged(context.Background(), filepath.Join(t.TempDir(), "outside.php"), 1)
	m.mu.Lock()
	requested, pending := m.assemblyRequested, m.assemblyInvalidationTimer
	m.mu.Unlock()
	if requested != first.Generation || pending != nil {
		t.Fatal("irrelevant/expired inputs invalidated discovery")
	}
	m.NotifyWatchedFilesChanged(context.Background(), path, 3)
	m.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.assemblyInvalidationTimer != nil {
		t.Fatal("close retained a pending discovery refresh")
	}
}
