package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/insight/governor"
	"crude/types/insight"
	"crude/types/setting"
	"crude/workspace"
	"crude/workspace/fileio"
)

// watchFixtureClient keeps the real manager and fact store in the test. It
// behaves like a project server that rechecks its input on a file notification.
type watchFixtureClient struct {
	*semanticLSPClient
	muWatch sync.Mutex
	path    string
	publish func(string, insight.DiagnosticPublication)
	seq     uint64
	opened  int
	events  chan string
}

func (c *watchFixtureClient) OnUnopenedDiagnostics(fn func(string, insight.DiagnosticPublication)) {
	c.muWatch.Lock()
	c.publish = fn
	c.muWatch.Unlock()
}

func (c *watchFixtureClient) OpenFile(ctx context.Context, path, language, text string, boundary insight.Boundary) (int, error) {
	c.muWatch.Lock()
	c.opened++
	c.muWatch.Unlock()
	return c.semanticLSPClient.OpenFile(ctx, path, language, text, boundary)
}

func (c *watchFixtureClient) NotifyWatchedFilesChanged(ctx context.Context, path string, _ int) {
	if c.events != nil {
		c.events <- path
	}
	if ctx.Err() == nil && path == c.path {
		c.check()
	}
}

type watchMultiRuntime struct {
	mu      sync.Mutex
	path    string
	clients []*watchFixtureClient
}

func (r *watchMultiRuntime) NewClient(_ setting.LSPServerSpec, _ string, admit func() error) workspace.LSPClient {
	c := &watchFixtureClient{semanticLSPClient: &semanticLSPClient{admit: admit}, path: r.path, events: make(chan string, 4096)}
	r.mu.Lock()
	r.clients = append(r.clients, c)
	r.mu.Unlock()
	return c
}

func (r *watchMultiRuntime) snapshot() []*watchFixtureClient {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*watchFixtureClient(nil), r.clients...)
}

func watchAwait(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func watchManagerFixture(t *testing.T, root, path string) (*workspace.Workspace, *watchMultiRuntime) {
	t.Helper()
	setCoreTestHome(t, t.TempDir())
	w := workspace.NewWorkspace(root, "test", "tester", setting.Settings{}, coreTestLogger{})
	t.Cleanup(w.Close)
	w.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "off"}})
	r := &watchMultiRuntime{path: path}
	m := w.LSPMgr
	m.SetRuntime(r)
	g, err := governor.New(governor.Limits{MaxProcesses: 16, MaxWaiting: 64})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetAnalysisGovernor(g, "watch-test", "generation"); err != nil {
		t.Fatal(err)
	}
	settings := m.Settings()
	settings.Enabled, settings.StartOnOpen = true, true
	m.SetSettings(settings)
	identity, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	m.SetAnalysisPolicy(setting.WorkspaceAnalysis{Trust: setting.AnalysisTrusted}, identity)
	// These tests supply the producer directly. Starting assembly here races
	// that producer with watch-recovery discovery, which correctly replaces it.
	// The installed-server recovery test covers discovery plus native watching.
	return w, r
}

func installWatchProducer(m *workspace.LSPManager, root string) {
	m.SetProjectProducers([]insight.LSPProjectProducer{{Root: root, Spec: setting.LSPServerSpec{Command: "fixture-server", DiagnosticScope: insight.CoverageProject}}})
}

func TestWatchGapFencesInflightCallbackFromReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "input")
	if err := os.WriteFile(path, []byte("ERROR"), 0600); err != nil {
		t.Fatal(err)
	}
	w, r := watchManagerFixture(t, root, path)
	m := w.LSPMgr
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	watch, err := startCoreFileWatch(root, nil, func(ctx context.Context, p string, k int) {
		if p == path {
			once.Do(func() { close(entered); <-release })
		}
		m.NotifyWatchedFilesChanged(ctx, p, k)
	}, m.SetProjectWatchReliable)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	<-watch.ready
	installWatchProducer(m, root)
	watchAwait(t, "first producer", func() bool { connected, _, _ := m.Status(); return connected && len(r.snapshot()) == 1 })
	old := r.snapshot()[0]
	old.check()
	watchAwait(t, "initial fact", func() bool {
		f, ok := m.FileDiagnostics(path)
		return ok && f.FromProjectCheck() && len(f.Diagnostics) == 1
	})
	if err := os.WriteFile(path, []byte("fixed"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("callback not blocked")
	}
	// A subtree larger than the watch bound forces a gap while the old
	// callback is in flight. An ordinary new directory is not a gap.
	big := filepath.Join(t.TempDir(), "big")
	if err := os.MkdirAll(big, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= coreWatchSubtreeLimit; i++ {
		if err := os.WriteFile(filepath.Join(big, fmt.Sprintf("f-%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(big, filepath.Join(root, "big")); err != nil {
		t.Fatal(err)
	}
	watchAwait(t, "replacement producer", func() bool { connected, _, _ := m.Status(); return connected && len(r.snapshot()) >= 2 })
	fresh := r.snapshot()[len(r.snapshot())-1]
	fresh.check()
	watchAwait(t, "replacement fact", func() bool {
		f, ok := m.FileDiagnostics(path)
		return ok && f.FromProjectCheck() && len(f.Diagnostics) == 0
	})
	releaseOnce.Do(func() { close(release) })
	// A later marker proves the worker has passed the blocked callback.
	marker := filepath.Join(root, "marker")
	if err := os.WriteFile(marker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	watchAwait(t, "later marker", func() bool {
		select {
		case p := <-fresh.events:
			if p == path {
				t.Error("stale event reached replacement")
			}
			return p == marker
		default:
			return false
		}
	})
	if err := os.WriteFile(path, []byte("ERROR"), 0600); err != nil {
		t.Fatal(err)
	}
	watchAwait(t, "subsequent diagnostic", func() bool {
		f, ok := m.FileDiagnostics(path)
		return ok && f.FromProjectCheck() && len(f.Diagnostics) == 1
	})
}

func TestWatchRootLossRemainsUnreliable(t *testing.T) {
	for _, operation := range []string{"rename", "remove", "delete-first", "replace"} {
		t.Run(operation, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "project")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "input")
			if err := os.WriteFile(path, []byte("ERROR"), 0600); err != nil {
				t.Fatal(err)
			}
			w, r := watchManagerFixture(t, root, path)
			m := w.LSPMgr
			states := make(chan bool, 100)
			notify := func(ctx context.Context, changed string, kind int) {
				// Removing a nonempty root also reports child changes. Hold
				// the child's callback in this case so root loss must fence
				// the retained fact, regardless of native scheduling.
				if operation == "remove" && changed == path {
					<-ctx.Done()
				}
				m.NotifyWatchedFilesChanged(ctx, changed, kind)
			}
			watch, err := startCoreFileWatch(root, nil, notify, func(v bool) { m.SetProjectWatchReliable(v); states <- v })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(watch.Close)
			<-watch.ready
			if <-states || !<-states {
				t.Fatal("startup was not fenced and admitted")
			}
			installWatchProducer(m, root)
			watchAwait(t, "producer", func() bool { connected, _, _ := m.Status(); return connected && len(r.snapshot()) == 1 })
			r.snapshot()[0].check()
			watchAwait(t, "initial fact", func() bool { f, ok := m.FileDiagnostics(path); return ok && f.FromProjectCheck() })
			replacement := filepath.Join(parent, "replacement")
			if operation == "replace" {
				if err := os.MkdirAll(filepath.Join(replacement, "nested"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if operation == "delete-first" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				watchAwait(t, "deleted file's problem to clear", func() bool {
					f, _ := m.FileDiagnostics(path)
					return len(f.Diagnostics) == 0
				})
			}
			if operation == "remove" || operation == "delete-first" {
				if err := os.RemoveAll(root); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(root, filepath.Join(parent, "old")); err != nil {
					t.Fatal(err)
				}
			}
			if operation == "replace" {
				if err := os.Rename(replacement, root); err != nil {
					t.Fatal(err)
				}
			}
			watchAwait(t, "root loss", func() bool {
				select {
				case v := <-states:
					return !v
				default:
					return false
				}
			})
			if err := os.MkdirAll(filepath.Join(root, "nested"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("fixed"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "nested", "edit"), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			if operation != "delete-first" {
				watchAwait(t, "stale diagnostics", func() bool {
					f, ok := m.FileDiagnostics(path)
					return ok && f.State == insight.Stale && len(f.Diagnostics) == 1 && f.Diagnostics[0].Message == "fixture error"
				})
			}
			watch.Close()
			if operation == "delete-first" {
				// The server may report an empty batch after the confirmed
				// deletion. Root loss must not make that evidence current.
				if f, ok := m.FileDiagnostics(path); ok && (f.State != insight.Stale || len(f.Diagnostics) != 0) {
					t.Fatalf("root loss revived evidence for a previously deleted file: %+v", f)
				}
			}
			close(states)
			for v := range states {
				if v {
					t.Error("root loss restored reliability without admission")
				}
			}
			if len(r.snapshot()) != 1 {
				t.Fatal("project producer restarted without root admission")
			}
		})
	}
}

func (c *watchFixtureClient) check() {
	content, err := os.ReadFile(c.path)
	var diagnostics []insight.LSPDiagnostic
	if err == nil && strings.Contains(string(content), "ERROR") {
		diagnostics = []insight.LSPDiagnostic{{Severity: insight.DiagError, Message: "fixture error"}}
	}
	c.muWatch.Lock()
	c.seq++
	publish, sequence := c.publish, c.seq
	c.muWatch.Unlock()
	if publish != nil {
		publish(c.path, insight.DiagnosticPublication{
			Sequence: sequence, WireVersion: 1, Diagnostics: diagnostics,
		})
	}
}

type watchFixtureRuntime struct{ client *watchFixtureClient }

func (r watchFixtureRuntime) NewClient(_ setting.LSPServerSpec, _ string, admit func() error) workspace.LSPClient {
	r.client.mu.Lock()
	r.client.admit = admit
	r.client.mu.Unlock()
	return r.client
}

func TestRetainedCoreRefreshesUnopenedDiagnosticAfterDiskReplacement(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "unopened.fixture")
	if err := os.WriteFile(path, []byte("ERROR\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &watchFixtureClient{semanticLSPClient: &semanticLSPClient{}, path: path}
	r, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "test", "tester", setting.Settings{}, coreTestLogger{})
		w.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "off"}})
		w.LSPMgr.SetRuntime(watchFixtureRuntime{client})
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
	a, err := r.workspace(t.Context(), "watch-fixture", root)
	if err != nil {
		t.Fatal(err)
	}
	if a.fileWatch == nil {
		t.Fatal("retained core watcher is unavailable")
	}
	<-a.fileWatch.ready
	manager := a.ws.LSPMgr
	settings := manager.Settings()
	settings.Enabled, settings.StartOnOpen = true, true
	manager.SetSettings(settings)
	identity, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetAnalysisPolicy(setting.WorkspaceAnalysis{Trust: setting.AnalysisTrusted}, identity)

	// Let initial discovery settle before installing this independent producer.
	await := func(what string, ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if ready() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s; assembly=%+v; policy=%+v", what, manager.Assembly(), manager.AnalysisPolicy())
	}
	await("assembly", func() bool { return manager.Assembly().State == insight.AssemblyReady })
	manager.SetProjectProducers([]insight.LSPProjectProducer{{
		Root: root, Spec: setting.LSPServerSpec{Command: "fixture-server", DiagnosticScope: insight.CoverageProject},
	}})
	await("project server", func() bool {
		connected, _, _ := manager.Status()
		return connected
	})
	client.check()
	await("initial project error", func() bool {
		fact, ok := manager.FileDiagnostics(path)
		return ok && fact.FromProjectCheck() && len(fact.Diagnostics) == 1 && manager.ProjectInsights().Errors == 1
	})
	if err := a.inspect(t.Context(), func() {
		if len(a.subscribers) != 0 {
			t.Error("fixture unexpectedly relies on a shell")
		}
	}); err != nil {
		t.Fatal(err)
	}

	// Replace the file atomically while no shell is attached. A shell-owned
	// watcher cannot deliver this event.
	replacement := filepath.Join(root, "replacement.tmp")
	if err := os.WriteFile(replacement, []byte("fixed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	await("cleared project error", func() bool {
		fact, ok := manager.FileDiagnostics(path)
		return ok && fact.FromProjectCheck() && len(fact.Diagnostics) == 0 && manager.ProjectInsights().Errors == 0
	})
	client.muWatch.Lock()
	opened := client.opened
	client.muWatch.Unlock()
	if opened != 0 {
		t.Fatalf("project checking opened the file %d times", opened)
	}
}
