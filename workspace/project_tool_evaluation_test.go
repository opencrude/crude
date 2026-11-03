package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/insight/governor"
	"crude/internal/fileuri"
	"crude/toolstore"
	"crude/types"
)

func TestProjectToolEvaluationPinsExactExecutableUntilJoinedStop(t *testing.T) {
	for _, mode := range []string{"passed", "cancelled", "revoked", "start_failed", "unverified"} {
		t.Run(mode, func(t *testing.T) {
			store, tool := managedLSPFixture(t)
			g := analysisGovernor(t, 2)
			stopGate := make(chan struct{})
			defer func() {
				select {
				case <-stopGate:
				default:
					close(stopGate)
				}
			}()
			r := &lifecycleRuntime{stopGate: stopGate}
			if mode == "cancelled" {
				r.startGate = make(chan struct{})
			}
			if mode == "start_failed" {
				r.startErrs = []error{errors.New("initialize failed")}
			}
			if mode == "unverified" {
				r.stopErr = errors.New("the process is still owned")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var revoked atomic.Bool
			type outcome struct {
				status   string
				retained *projectToolEvaluationRetained
				err      error
			}
			done := make(chan outcome, 1)
			go func() {
				status, retained, err := evaluateProjectTool(ctx, store, r, types.LSPServerSpec{Command: tool.Executable},
					g, governor.Owner{Workspace: "evaluation", Generation: "generation", Producer: "fixture"}, 2,
					func(ctx context.Context) error {
						if revoked.Load() {
							return errors.New("the consumed decision is no longer current")
						}
						return ctx.Err()
					})
				done <- outcome{status, retained, err}
			}()
			awaitLSP(t, func() bool { return len(r.snapshot()) == 1 })
			if mode == "revoked" {
				// Revoke after initialize, while the exact Stop still cannot join.
				awaitLSP(t, func() bool {
					c := r.snapshot()[0]
					c.mu.Lock()
					defer c.mu.Unlock()
					return c.started
				})
			}
			requireManagedRemovalBlocked(t, store, tool)
			if reserved := g.Snapshot().Counts.Reserved(); reserved != 1 {
				t.Fatalf("check bypassed shared process admission: %d", reserved)
			}
			if mode == "cancelled" {
				cancel()
			}
			if mode == "revoked" {
				revoked.Store(true)
			}
			select {
			case result := <-done:
				t.Fatalf("check returned before exact Stop joined: %+v", result)
			default:
			}
			requireManagedRemovalBlocked(t, store, tool)
			close(stopGate)
			var result outcome
			select {
			case result = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("check did not join")
			}
			r.mu.Lock()
			root, err := fileuri.ToPath(r.roots[0])
			r.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unverified" {
				if result.status != toolEvaluationTeardownUnverified || result.err == nil || result.retained == nil ||
					g.Snapshot().Counts.Unverified != 1 {
					t.Fatalf("uncertain teardown released ownership: %+v %+v", result, g.Snapshot())
				}
				requireManagedRemovalBlocked(t, store, tool)
				if _, err := os.Stat(root); err != nil {
					t.Fatalf("uncertain process root was removed: %v", err)
				}
				// Exact retained owner can release only after positive verification.
				c := r.snapshot()[0]
				c.mu.Lock()
				c.stopErr = nil
				c.mu.Unlock()
				if err := result.retained.client.Stop(); err != nil {
					t.Fatal(err)
				}
				result.retained.lease.Release()
				if err := os.RemoveAll(result.retained.root); err != nil {
					t.Fatal(err)
				}
			} else {
				want := toolEvaluationFailed
				switch mode {
				case "passed":
					want = toolEvaluationPassed
				case "cancelled":
					want = toolEvaluationCancelled
				}
				if result.status != want || (result.err == nil) != (mode == "passed") || result.retained != nil {
					t.Fatalf("check outcome: %+v; want %s", result, want)
				}
				if _, err := os.Stat(root); !os.IsNotExist(err) {
					t.Fatalf("joined root was not removed: %v", err)
				}
			}
			if reserved := g.Snapshot().Counts.Reserved(); reserved != 0 {
				t.Fatalf("joined check leaked admission: %d", reserved)
			}
			removeManagedFixture(t, store, tool)
		})
	}
}

func TestProjectToolSetupOperationalFailureNeverPublishesOrActivates(t *testing.T) {
	for _, mode := range []string{"fail_initialize", "stall_initialize"} {
		t.Run(mode, func(t *testing.T) {
			w, o, _, owner := setupServiceFixture(t)
			w.LSPMgr.SetRuntime(NewLSPRuntime())
			trace := filepath.Join(t.TempDir(), "initialize.txt")
			settings := w.LSPMgr.Settings()
			settings.Languages["a"] = types.LSPLanguageSettings{Enabled: true,
				Environment: map[string]string{"CRUDE_FIXTURE_MODE": mode, "CRUDE_FIXTURE_TRACE": trace}}
			w.LSPMgr.SetSettings(settings)
			serveSetupCatalogArtifact(t)
			store, err := toolstore.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			o.store = store
			w.LSPMgr.SetToolStore(store)
			review := setupReview(t, o, owner)
			var selected string
			for _, item := range review.Review.Items {
				if item.Package == "setup-alpha" {
					selected = item.ID
				}
			}
			if selected == "" {
				t.Fatal("missing operational fixture")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			type outcome struct {
				result types.ProjectToolSetupResult
				err    error
			}
			done := make(chan outcome, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(15 * time.Second):
						t.Error("tool setup worker did not join before cleanup")
					}
				}
			}()
			go func() {
				result, err := o.Decide(ctx, owner, "check-failure", review.ID, []string{selected})
				done <- outcome{result, err}
			}()
			// Wait for the actual child initialize boundary across download and
			// extraction, not awaitLSP's short in-memory lifecycle budget.
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()
			var got outcome
		initialize:
			for {
				data, _ := os.ReadFile(trace)
				if len(data) != 0 {
					break
				}
				select {
				case early := <-done:
					joined = true
					// A rejected initialize can complete before the trace read.
					if data, _ := os.ReadFile(trace); len(data) != 0 {
						got = early
						break initialize
					}
					t.Fatalf("setup ended before the real initialize boundary: %+v %v", early.result, early.err)
				case <-ctx.Done():
					t.Fatal("the fixture process did not reach initialize")
				case <-tick.C:
				}
			}
			if mode == "stall_initialize" {
				cancel()
			}
			if !joined {
				select {
				case got = <-done:
					joined = true
				case <-time.After(15 * time.Second):
					t.Fatal("failed operational check did not join")
				}
			}
			want := toolEvaluationFailed
			if mode == "stall_initialize" {
				want = toolEvaluationCancelled
			}
			if got.err == nil || got.result.DecisionID == "" || len(got.result.Items) != 1 ||
				got.result.Items[0].Installed || got.result.Items[0].Evaluation != want {
				t.Fatalf("failed check reported installation: %+v %v", got.result, got.err)
			}
			if _, ok := store.Lookup("setup-alpha", "v1"); ok {
				t.Fatal("failed initialize published the executable")
			}
			reopened, err := toolstore.Open(store.Root())
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := reopened.Lookup("setup-alpha", "v1"); ok {
				t.Fatal("reopen promoted failed operational evidence")
			}
			if unknown, err := reopened.Unrecognized(t.Context()); err != nil || len(unknown) != 0 {
				t.Fatalf("joined failed check retained scratch: %v %v", unknown, err)
			}
			staging, err := os.ReadDir(filepath.Join(store.Root(), ".staging"))
			if err != nil || len(staging) != 0 {
				t.Fatalf("joined failed check leaked staged bytes: %v %v", staging, err)
			}
			w.LSPMgr.SetToolStore(reopened)
			writeTree(t, filepath.Join(w.RootPath, "main.setup-a"), "ERROR\n")
			w.LSPMgr.RefreshAssembly()
			plan := awaitAssembly(t, w.LSPMgr, func(p types.AssemblyPlan) bool {
				language, ok := assemblyLanguage(p, "a")
				return p.State == types.AssemblyReady && ok && language.Status == types.ToolMissing
			})
			if language, _ := assemblyLanguage(plan, "a"); language.Source == types.ToolSourceStore {
				t.Fatalf("failed operational check became an activation candidate: %+v", language)
			}
			if health := w.LSPMgr.ServerHealthSnapshots(); len(health) != 0 {
				t.Fatalf("failed tool activated after refresh: %+v", health)
			}
			if usage, _, err := store.List(t.Context()); err != nil || len(usage) != 0 {
				t.Fatalf("failed check recorded workspace use: %+v %v", usage, err)
			}
			if again := setupReview(t, o, owner); again.NeedsDecision {
				t.Fatalf("failed candidate entered an automatic retry loop: %+v", again)
			}
		})
	}
}

func TestProjectToolSetupUnverifiedStagedCheckKeepsExactOwnerAndBytes(t *testing.T) {
	w, o, _, owner := setupServiceFixture(t)
	runtime := &lifecycleRuntime{stopErr: errors.New("exact process teardown is unverified")}
	w.LSPMgr.SetRuntime(runtime)
	serveSetupCatalogArtifact(t)
	store, err := toolstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o.store = store
	review := setupReview(t, o, owner)
	result, err := o.Decide(t.Context(), owner, "uncertain-check", review.ID, setupInstallable(review)[:1])
	var unverified *toolstore.EvaluationUnverifiedError
	if !errors.As(err, &unverified) || len(result.Items) != 1 || result.Items[0].Installed ||
		result.Items[0].Evaluation != toolEvaluationTeardownUnverified {
		t.Fatalf("uncertain staged check: %+v %v", result, err)
	}
	o.Close() // shutdown must not manufacture a successful Stop or release
	o.mu.Lock()
	retained := append([]*projectToolEvaluationRetained(nil), o.retained...)
	o.mu.Unlock()
	if len(retained) != 1 {
		t.Fatalf("lost exact staged process owner: %d", len(retained))
	}
	runtime.mu.Lock()
	executable := runtime.specs[0].Command
	runtime.mu.Unlock()
	if _, err := os.Stat(executable); err != nil {
		t.Fatalf("uncertain process bytes were removed: %v", err)
	}
	reopened, err := toolstore.Open(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := reopened.Unrecognized(t.Context())
	if err != nil || len(unknown) != 1 {
		t.Fatalf("uncertain staged bytes not inspectable: %v %v", unknown, err)
	}
	if tools, _, err := reopened.List(t.Context()); err != nil || len(tools) != 0 {
		t.Fatalf("uncertain check became installed/resolvable: %v %v", tools, err)
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatalf("reopen pruned possibly live executable bytes: %v", err)
	}
	m := w.LSPMgr
	m.mu.Lock()
	g := m.analysisGovernor
	m.mu.Unlock()
	if g.Snapshot().Counts.Unverified != 1 {
		t.Fatal("uncertain staged check released its process slot")
	}
	// Test-only positive teardown, using the exact retained transport owner.
	client := runtime.snapshot()[0]
	client.mu.Lock()
	client.stopErr = nil
	client.mu.Unlock()
	if err := retained[0].client.Stop(); err != nil {
		t.Fatal(err)
	}
	retained[0].lease.Release()
	if err := os.RemoveAll(retained[0].root); err != nil {
		t.Fatal(err)
	}
}
