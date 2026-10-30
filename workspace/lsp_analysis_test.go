package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/document"
	"crude/fileio"
	"crude/insight/governor"
	"crude/types"
)

func TestAnalysisDefaultStaticBlocksHeadlessProbeAndManualEditorStart(t *testing.T) {
	// Deliberately bypass lifecycleManager/trustAnalysis: session feature opt-in
	// and a configured transport are not workspace execution permission.
	root := t.TempDir()
	runtime := &lifecycleRuntime{}
	m := NewLSPManager(nil, runtime)
	t.Cleanup(m.Close)
	m.SetWorkspaceRoot(root)
	m.SetSettings(editorDisabledSettings())
	probeSources(t, m, root)
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	_, settled, _, err := m.ProbeDiagnostics(t.Context(), path)
	if err == nil || !strings.Contains(err.Error(), "no workspace execution grant") || settled {
		t.Fatalf("untrusted headless probe: settled=%v err=%v", settled, err)
	}
	if got := len(runtime.snapshot()); got != 0 {
		t.Fatalf("no workspace execution grant, but headless probe constructed %d providers", got)
	}
	m.SetSettings(types.DefaultLSPSettings())
	editorSource(t, m, path, "package main\n")
	m.StartForWorkspace(root)
	if m.RestartServer("absent") || m.EditorEnabled() || m.ServesWarm(path) {
		t.Fatal("manual/session/editor control supplied execution permission")
	}
	m.Close() // Join any worker so the no-construction assertion is not a sleep.
	if len(runtime.snapshot()) != 0 || m.AnalysisPolicy().Trust != types.AnalysisStaticOnly {
		t.Fatal("default Static only constructed a provider or acquired trust")
	}
	if view := m.ProjectInsights(); view.IdleStatus != "Static only" || !strings.Contains(view.IdleReason, "Workspace settings") {
		t.Fatalf("static guidance=%+v", view)
	}
}

func TestAnalysisRequiresBindingAndCoreGovernor(t *testing.T) {
	for _, kind := range []string{"missing-binding", "invalid-binding", "missing-governor"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			r := &lifecycleRuntime{}
			m := NewLSPManager(nil, r)
			t.Cleanup(m.Close)
			m.SetWorkspaceRoot(root)
			identity, err := fileio.DirectoryIdentity(root)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing-binding":
				identity = ""
			case "invalid-binding":
				identity = "invalid\nbinding"
			}
			m.SetAnalysisPolicy(types.WorkspaceAnalysisSettings{Trust: types.AnalysisProjectExecutionAllowed}, identity)
			probeSources(t, m, root)
			_, _, _, err = m.ProbeDiagnostics(t.Context(), filepath.Join(root, "main.go"))
			if err == nil || len(r.snapshot()) != 0 || m.ProjectInsights().IdleReason == "" {
				t.Fatalf("unavailable execution admission escaped: %v", err)
			}
			if kind != "missing-governor" && m.AnalysisPolicy().Trust != types.AnalysisStaticOnly {
				t.Fatal("invalid binding appeared trusted")
			}
			if err := m.SetAnalysisGovernor(nil, "", ""); err == nil {
				t.Fatal("nil governor accepted")
			}
		})
	}
}

func analysisGovernor(t *testing.T, processes int) *governor.Governor {
	t.Helper()
	g, err := governor.New(governor.Limits{MaxProcesses: processes, MaxWaiting: 16})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func analysisManager(t *testing.T, g *governor.Governor, workspaceID, generation string) (*LSPManager, *lifecycleRuntime, string) {
	t.Helper()
	m, r, root := NewLSPManager(nil), &lifecycleRuntime{}, t.TempDir()
	m.SetRuntime(r)
	t.Cleanup(m.Close)
	if err := m.SetAnalysisGovernor(g, workspaceID, generation); err != nil {
		t.Fatal(err)
	}
	bindAnalysisRoot(t, m, root)
	return m, r, root
}

func blockAnalysisCapacity(t *testing.T, g *governor.Governor) *governor.Lease {
	t.Helper()
	ticket, err := g.Submit(governor.Request{Owner: governor.Owner{Workspace: "blocker", Generation: "core", Producer: "external"}, WorkspaceLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := ticket.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lease.Release) // This fake never creates a process.
	return lease
}

func TestAnalysisQueuedAdmissionCancelsWithoutConstructing(t *testing.T) {
	for _, action := range []string{"revoke", "last-close", "stop", "stop-server", "runtime", "settings", "root", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			g := analysisGovernor(t, 1)
			blockAnalysisCapacity(t, g)
			m, r, root := analysisManager(t, g, "workspace", "generation")
			path := filepath.Join(root, "main.go")
			editorSource(t, m, path, "package main\n")
			awaitLSP(t, func() bool { return g.Snapshot().Counts.Queued == 1 })
			if len(r.snapshot()) != 0 {
				t.Fatal("queued work constructed a provider")
			}
			switch action {
			case "revoke":
				m.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
			case "last-close":
				closeEditor(t, m, path)
			case "stop":
				m.Stop()
			case "stop-server":
				m.StopServer(m.ServerHealthSnapshots()[0].ID)
			case "runtime":
				m.SetRuntime(nil)
			case "settings":
				m.SetSettings(editorDisabledSettings())
			case "root":
				m.SetWorkspaceRoot(t.TempDir())
				m.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
			case "shutdown":
				m.Close()
			}
			// Shutdown also prevents legitimate replacement policy from queuing
			// fresh work after cancelling the exact old ticket.
			m.Close()
			if got := g.Snapshot().Counts; got.Queued != 0 || got.Reserved() != 1 || len(r.snapshot()) != 0 {
				t.Fatalf("%s failed exact cancellation: %+v", action, got)
			}
		})
	}
}

func TestAnalysisQueuedOversizedDocumentCancelsReservation(t *testing.T) {
	g := analysisGovernor(t, 1)
	blockAnalysisCapacity(t, g)
	m, r, root := analysisManager(t, g, "workspace", "generation")
	settings := m.Settings()
	settings.MaxDocumentBytes = 1024
	m.SetSettings(settings)
	path := filepath.Join(root, "main.go")
	snapshot := editorSource(t, m, path, "package main\n")
	awaitLSP(t, func() bool { return g.Snapshot().Counts.Queued == 1 })
	documentEdit(t, m.sources, document.Owner{ClientID: "fixture-editor", Epoch: 1},
		snapshot.ID, "oversize", snapshot.Revision, 0, len(snapshot.Text), strings.Repeat("x", 1025))
	awaitLSP(t, func() bool { return g.Snapshot().Counts.Queued == 0 })
	if len(r.snapshot()) != 0 {
		t.Fatal("oversized queued document constructed a provider")
	}
}

func TestAnalysisRootReplacementBeforeResolutionAndAfterQueue(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-resolution", true: "after-queue"}[queued], func(t *testing.T) {
			g := analysisGovernor(t, 1)
			var blocker *governor.Lease
			if queued {
				blocker = blockAnalysisCapacity(t, g)
			}
			m, r, root := analysisManager(t, g, "workspace", "generation")
			binding := m.analysisRootIdentity
			replace := func() {
				t.Helper()
				if err := os.Rename(root, root+"-original"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(root + "-original") })
				if err := os.Mkdir(root, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if !queued {
				replace()
			}
			editorSource(t, m, filepath.Join(root, "main.go"), "package main\n")
			if queued {
				awaitLSP(t, func() bool { return g.Snapshot().Counts.Queued == 1 })
				replace()
				blocker.Release()
			}
			awaitLSP(t, func() bool { return m.AnalysisPolicy().Trust == types.AnalysisStaticOnly })
			m.SetAnalysisPolicy(types.WorkspaceAnalysisSettings{Trust: types.AnalysisProjectExecutionAllowed}, binding)
			m.StartForWorkspace(root)
			m.Close()
			if m.AnalysisPolicy().Trust != types.AnalysisStaticOnly || len(r.snapshot()) != 0 || g.Snapshot().Counts.Reserved() != 0 {
				t.Fatal("root replacement or identical policy replay bypassed the blocked binding")
			}
			if !strings.Contains(m.ProjectInsights().IdleReason, "root identity") {
				t.Fatalf("replacement guidance=%+v", m.ProjectInsights())
			}
		})
	}
}

func TestAnalysisPendingIdentityCannotOverrideNewPolicyEpoch(t *testing.T) {
	m, r, root := lifecycleManager(t)
	binding := m.analysisRootIdentity
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	m.analysisIdentity = func(string) (string, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return "", errors.New("late identity failure from the revoked epoch")
		}
		return binding, nil
	}
	editorSource(t, m, filepath.Join(root, "main.go"), "package main\n")
	<-entered
	m.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
	m.SetAnalysisPolicy(types.WorkspaceAnalysisSettings{Trust: types.AnalysisProjectExecutionAllowed}, binding)
	close(release)
	awaitLSP(t, m.ServerReady)
	if m.AnalysisPolicy().Trust != types.AnalysisProjectExecutionAllowed || len(r.snapshot()) != 1 {
		t.Fatal("stale metadata result downgraded or launched the wrong policy epoch")
	}
}

func TestAnalysisRevocationCancelsGrantDuringLaunchIdentityIO(t *testing.T) {
	m, r, root := lifecycleManager(t)
	binding := m.analysisRootIdentity
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	m.analysisIdentity = func(string) (string, error) {
		if calls.Add(1) == 2 { // Resolution passed; grant is retained for launch.
			close(entered)
			<-release
		}
		return binding, nil
	}
	editorSource(t, m, filepath.Join(root, "main.go"), "package main\n")
	<-entered
	if got := m.analysisGovernor.Snapshot().Counts; got.Granted != 1 || len(r.snapshot()) != 0 {
		close(release)
		t.Fatalf("identity I/O did not retain an unstarted exact grant: %+v", got)
	}
	m.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
	if got := m.analysisGovernor.Snapshot().Counts; got.Reserved() != 0 || got.Queued != 0 {
		close(release)
		t.Fatalf("revocation did not synchronously cancel the unstarted grant: %+v", got)
	}
	close(release)
	m.Close()
	if len(r.snapshot()) != 0 {
		t.Fatal("stale launch identity result constructed a provider after revocation")
	}
}

func TestAnalysisAwaitRejectsRevokeRegrantEpoch(t *testing.T) {
	m, r, root := probeManager(t)
	gate := make(chan struct{})
	r.startGate = gate
	t.Cleanup(func() { close(gate) })
	binding := m.analysisRootIdentity
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, _, err := m.ProbeDiagnostics(ctx, path)
		done <- err
	}()
	awaitLSP(t, func() bool { return len(r.snapshot()) == 1 })
	m.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
	m.SetAnalysisPolicy(types.WorkspaceAnalysisSettings{Trust: types.AnalysisProjectExecutionAllowed}, binding)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "reconfigured") {
			t.Fatalf("old probe joined a replacement execution epoch: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("old probe kept awaiting the replacement policy epoch")
	}
}

func TestAnalysisSharedGovernorRetainsExactLeaseAcrossGenerations(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified-stop", true: "unverified-stop"}[uncertain], func(t *testing.T) {
			g := analysisGovernor(t, 2)
			first, r1, root1 := analysisManager(t, g, "same-workspace", "old-generation")
			settings := first.Settings()
			settings.MaxServers = 1
			first.SetSettings(settings)
			if uncertain {
				r1.stopErr = errors.New("owned stop is unverified")
			}
			editorSource(t, first, filepath.Join(root1, "first.go"), "package first\n")
			awaitLSP(t, first.ServerReady)
			second, r2, root2 := analysisManager(t, g, "same-workspace", "new-generation")
			second.SetSettings(settings) // Shares the workspace quota across generations.
			editorSource(t, second, filepath.Join(root2, "second.go"), "package second\n")
			awaitLSP(t, func() bool { return g.Snapshot().Counts.Queued == 1 })
			if len(r2.snapshot()) != 0 {
				t.Fatal("new generation ignored old generation's reservation")
			}
			if err := first.SetAnalysisGovernor(analysisGovernor(t, 2), "replacement", "generation"); err == nil {
				t.Fatal("retained governor was replaced")
			}
			first.Close()
			if uncertain {
				got := g.Snapshot()
				if got.Counts.Unverified != 1 || got.Counts.Queued != 1 || got.Entries[0].Owner.Generation != "new-generation" {
					t.Fatalf("uncertain exact lease was freed: %+v", got)
				}
				if len(r2.snapshot()) != 0 {
					t.Fatal("uncertain stop admitted replacement")
				}
			} else {
				awaitLSP(t, second.ServerReady)
				if got := g.Snapshot(); got.Counts.Started != 1 || len(got.Entries) != 1 || got.Entries[0].Owner.Generation != "new-generation" {
					t.Fatalf("released wrong lifetime: %+v", got)
				}
			}
			second.Close()
			got := g.Snapshot().Counts
			if got.Queued != 0 || got.Started != 0 || got.Unverified != map[bool]int{false: 0, true: 1}[uncertain] {
				t.Fatalf("shutdown lost exact lease accounting: %+v", got)
			}
		})
	}
}

func TestAnalysisQuotaUpdatesDoNotEvictLiveProducers(t *testing.T) {
	g := analysisGovernor(t, 4)
	m, r, root := analysisManager(t, g, "workspace", "generation")
	editorSource(t, m, filepath.Join(root, "main.go"), "package main\n")
	editorSource(t, m, filepath.Join(root, "main.py"), "print('hello')\n")
	awaitLSP(t, func() bool { return g.Snapshot().Counts.Started == 2 })
	settings := m.Settings()
	settings.MaxServers = 1
	m.SetSettings(settings)
	snapshot := g.Snapshot()
	if snapshot.Counts.Started != 2 || snapshot.Workspaces[0].Limit != 1 {
		t.Fatalf("quota reduction evicted or failed to update retained workspace: %+v", snapshot)
	}
	editorSource(t, m, filepath.Join(root, "main.rs"), "fn main() {}\n")
	awaitLSP(t, func() bool { return g.Snapshot().Counts.Queued == 1 })
	if len(r.snapshot()) != 2 {
		t.Fatal("quota reduction admitted a third process")
	}
	settings.MaxServers = 3
	m.SetSettings(settings)
	awaitLSP(t, func() bool { return g.Snapshot().Counts.Started == 3 })
	if len(r.snapshot()) != 3 {
		t.Fatal("quota increase restarted instead of retaining live producers")
	}
}

type analysisConstructorRuntime struct {
	base             lifecycleRuntime
	entered, release chan struct{}
}

func (r *analysisConstructorRuntime) NewClient(spec types.LSPServerSpec, root string, admission func() error) LSPClient {
	c := r.base.NewClient(spec, root, admission)
	close(r.entered)
	<-r.release // Inert construction is still an owned, joined operation.
	return c
}

func TestAnalysisRevokedSpawnEpochJoinsAlreadyRetainedConstructor(t *testing.T) {
	r := &analysisConstructorRuntime{entered: make(chan struct{}), release: make(chan struct{})}
	m := NewLSPManager(nil, r)
	t.Cleanup(m.Close)
	root := t.TempDir()
	trustAnalysis(t, m, root)
	editorSource(t, m, filepath.Join(root, "main.go"), "package main\n")
	<-r.entered
	m.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("shutdown abandoned an already-retained constructor")
	default:
	}
	if got := m.analysisGovernor.Snapshot().Counts; got.Started != 1 {
		t.Fatalf("revocation freed an unjoined constructor: %+v", got)
	}
	close(r.release)
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not join the revoked constructor")
	}
	client := r.base.snapshot()[0]
	client.mu.Lock()
	started, stopped := client.started, client.stopped
	client.mu.Unlock()
	if started || !stopped || m.analysisGovernor.Snapshot().Counts.Reserved() != 0 {
		t.Fatal("denied transport spawn escaped its epoch or lost verified retirement")
	}
}

func TestAnalysisRevocationRejectsLateProbePayload(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	c := &sourceAnswerClient{lifecycleClient: lifecycleClient{runtime: &lifecycleRuntime{}},
		entered: make(chan struct{}), release: make(chan struct{})}
	m := NewLSPManager(nil, sourceAnswerRuntime{c})
	t.Cleanup(m.Close)
	trustAnalysis(t, m, root)
	probeSources(t, m, root)
	type response struct {
		answer types.InsightDiagnostics
		err    error
	}
	done := make(chan response, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	go func() {
		answer, _, _, err := m.ProbeDiagnostics(ctx, path)
		done <- response{answer, err}
	}()
	<-c.entered
	m.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
	close(c.release)
	got := <-done
	if got.err == nil || got.answer.ID != "" || len(got.answer.Diagnostics) != 0 {
		t.Fatalf("revoked execution returned late payload: %+v", got)
	}
	if m.ShowDiagnosticsInEditor() || m.clientForFile(path) != nil || len(m.AllDocumentSymbols()) != 0 {
		t.Fatal("revoked execution remained available to editor consumers")
	}
}
