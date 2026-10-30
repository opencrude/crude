package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/document"
	"crude/insight/governor"
	"crude/types"
	"crude/workspace"
)

type countedAnalysisRuntime struct {
	engine        *semanticLSPClient
	constructions atomic.Int32
	stopErr       error
}

func (r *countedAnalysisRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	r.constructions.Add(1)
	client := (semanticLSPRuntime{r.engine}).NewClient(spec, root, admit)
	if r.stopErr != nil {
		return unverifiedAnalysisClient{LSPClient: client, err: r.stopErr}
	}
	return client
}

type unverifiedAnalysisClient struct {
	workspace.LSPClient
	err error
}

func (c unverifiedAnalysisClient) Stop() error { return c.err }

// Admission and replication use real retained cores and framed clients. The
// language engine is inert; this checks process reservations, not OS containment.
func TestAnalysisCoreGovernorSpansWorkspacesAndTrustRevocation(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	roots := []string{t.TempDir(), t.TempDir()}
	for _, root := range roots {
		if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	managers := make(map[string]*workspace.LSPManager)
	engines := make(map[string]*countedAnalysisRuntime)
	r, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
		engine := &countedAnalysisRuntime{engine: &semanticLSPClient{}}
		w.LSPMgr.SetRuntime(engine)
		mu.Lock()
		managers[root], engines[root] = w.LSPMgr, engine
		mu.Unlock()
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := r.analysisGovernor.Configure(governor.Limits{MaxProcesses: 1, MaxWaiting: 4}); err != nil {
		t.Fatal(err)
	}
	one, two := newResourceWire(t, r, "analysis-author"), newResourceWire(t, r, "analysis-peer")
	var attached [2]coreprotocol.AttachResult
	one.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: roots[0]}, &attached[0])
	two.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: roots[1]}, &attached[1])
	mu.Lock()
	first, second := managers[roots[0]], managers[roots[1]]
	firstEngine, secondEngine := engines[roots[0]], engines[roots[1]]
	mu.Unlock()
	var source document.Snapshot
	one.call(t, attached[0].WorkspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: filepath.Join(roots[0], "main.go")}, &source)
	first.StartForWorkspace(roots[0]) // Explicit Start is not an execution grant.
	_, _, _, err = first.ProbeDiagnostics(t.Context(), filepath.Join(roots[0], "main.go"))
	if err == nil || !strings.Contains(err.Error(), "no workspace execution grant") ||
		firstEngine.constructions.Load() != 0 || r.analysisGovernor.Snapshot().Counts.Reserved() != 0 {
		t.Fatal("default-static editor/manual-start/probe path acquired external execution", err)
	}
	grant := coreprotocol.WorkspacePreferencesRequest{Edits: []types.SettingEdit{
		{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"project_execution_allowed"`)},
	}}
	var saved coreprotocol.WorkspacePreferencesResult
	one.call(t, attached[0].WorkspaceID, coreprotocol.WorkspaceOverridesSet, grant, &saved)
	if saved.Effective.Analysis.Trust != types.AnalysisProjectExecutionAllowed || saved.Overrides.AnalysisRootIdentity == "" {
		t.Fatal("typed grant lost its native binding")
	}
	awaitCoreAnalysis(t, first, second, func() bool {
		return firstEngine.constructions.Load() == 1 && r.analysisGovernor.Snapshot().Counts.Started == 1
	})
	two.call(t, attached[1].WorkspaceID, coreprotocol.WorkspaceOverridesSet, grant, &saved)
	two.call(t, attached[1].WorkspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: filepath.Join(roots[1], "main.go")}, &source)
	awaitCoreAnalysis(t, first, second, func() bool { return r.analysisGovernor.Snapshot().Counts.Queued == 1 })
	snapshot := r.analysisGovernor.Snapshot()
	if snapshot.Counts.Reserved() != 1 || secondEngine.constructions.Load() != 0 || len(snapshot.Workspaces) != 2 {
		t.Fatal("separate retained workspaces spent separate global budgets", snapshot)
	}
	// A persisted revoke cancels admission synchronously and positively joined
	// retirement releases the exact lease, waking the peer without polling RPCs.
	one.call(t, attached[0].WorkspaceID, coreprotocol.WorkspaceOverridesSet,
		coreprotocol.WorkspacePreferencesRequest{Edits: []types.SettingEdit{{Path: []string{"analysis", "trust"}}}}, &saved)
	if saved.Effective.Analysis.Trust != types.AnalysisStaticOnly || saved.Overrides.AnalysisRootIdentity != "" {
		t.Fatal("revoke/reset returned a residual execution grant")
	}
	awaitCoreAnalysis(t, first, second, func() bool {
		counts := r.analysisGovernor.Snapshot().Counts
		return secondEngine.constructions.Load() == 1 && counts.Started == 1 && counts.Queued == 0
	})
	first.StartForWorkspace(roots[0])
	_, _, _, err = first.ProbeDiagnostics(context.Background(), filepath.Join(roots[0], "main.go"))
	if err == nil || firstEngine.constructions.Load() != 1 {
		t.Fatal("manual/session probe revived a revoked producer", err)
	}
	// Both shell observations agree on the same retained owner, not form drafts.
	var settings coreprotocol.SettingsFetchResult
	one.call(t, attached[0].WorkspaceID, coreprotocol.SettingsFetch, coreprotocol.Empty{}, &settings)
	if settings.Application.Analysis.Trust != types.AnalysisStaticOnly || settings.Effective.Analysis.Trust != types.AnalysisStaticOnly {
		t.Fatal("revoked workspace projection inherited application execution")
	}
}

func TestAnalysisCoreCloseReportsAndRetainsUnverifiedAdmission(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	engine := &countedAnalysisRuntime{engine: &semanticLSPClient{}, stopErr: errors.New("fixture teardown cannot be verified")}
	var manager *workspace.LSPManager
	r, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
		manager = w.LSPMgr
		manager.SetRuntime(engine)
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	client := newResourceWire(t, r, "unverified-analysis")
	var attached coreprotocol.AttachResult
	client.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
	var saved coreprotocol.WorkspacePreferencesResult
	client.call(t, attached.WorkspaceID, coreprotocol.WorkspaceOverridesSet,
		coreprotocol.WorkspacePreferencesRequest{Edits: []types.SettingEdit{
			{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"project_execution_allowed"`)},
		}}, &saved)
	var source document.Snapshot
	client.call(t, attached.WorkspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &source)
	awaitCoreAnalysis(t, manager, manager, func() bool {
		return engine.constructions.Load() == 1 && r.analysisGovernor.Snapshot().Counts.Started == 1
	})
	first := r.Close()
	if first == nil || !strings.Contains(first.Error(), "could not verify 1 retained process reservation") {
		t.Fatal("core shutdown reported success with unverified analysis cleanup", first)
	}
	snapshot := r.analysisGovernor.Snapshot()
	if !snapshot.Closed || snapshot.Counts.Unverified != 1 || snapshot.Counts.Reserved() != 1 {
		t.Fatal("shutdown forgot the exact unverified lease", snapshot)
	}
	if again := r.Close(); again != first {
		t.Fatal("idempotent shutdown hid its original joined failure", again)
	}
}

func awaitCoreAnalysis(t *testing.T, one, two *workspace.LSPManager, ready func() bool) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		_, changedOne := one.Changed()
		_, changedTwo := two.Changed()
		if ready() {
			return
		}
		select {
		case <-changedOne:
		case <-changedTwo:
		case <-timer.C:
			t.Fatal("retained core analysis admission did not reach its expected owner state")
		}
	}
}
