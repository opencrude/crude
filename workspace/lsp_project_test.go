package workspace

import (
	"path/filepath"
	"testing"
	"time"

	model "crude/types"
)

func (c *lifecycleClient) OnUnopenedDiagnostics(fn func(string, model.InsightDiagnosticPublication)) {
	c.mu.Lock()
	c.unopened = fn
	c.mu.Unlock()
}

// emitUnopened sends a server publication for a file that the client did not
// open, as a project-scoped server does.
func (c *lifecycleClient) emitUnopened(path string, ds ...LSPDiagnostic) {
	c.mu.Lock()
	c.sequence++
	p := model.InsightDiagnosticPublication{Sequence: c.sequence, WireVersion: 1, Diagnostics: ds}
	fn := c.unopened
	c.mu.Unlock()
	if fn != nil {
		fn(path, p)
	}
}

func projectFixture(t *testing.T) (*LSPManager, *lifecycleRuntime, string, model.LSPProjectProducer) {
	t.Helper()
	m, r, root := lifecycleManager(t)
	probeSources(t, m, root)
	for _, name := range []string{"main.go", "util.go"} {
		createFile(t, filepath.Join(root, name), "package main\n")
	}
	producer := model.LSPProjectProducer{Root: root, Spec: model.LSPServerSpec{Command: "project-check", Args: []string{"--stdio"}}}
	return m, r, root, producer
}

func startedProject(t *testing.T, m *LSPManager, r *lifecycleRuntime) *lifecycleClient {
	t.Helper()
	awaitLSP(t, func() bool {
		clients := r.snapshot()
		if len(clients) != 1 {
			return false
		}
		clients[0].mu.Lock()
		defer clients[0].mu.Unlock()
		return clients[0].started && clients[0].unopened != nil
	})
	awaitLSP(t, func() bool { _, status, _ := m.Status(); return status != LSPStarting })
	return r.snapshot()[0]
}

// A project producer is the path to problems in files nobody opened: it
// starts without a document, sends no didOpen, and its reports reach the
// shared facts that Problems and the status summary read.
func TestProjectProducerReportsUnopenedFilesWithoutOpeningThem(t *testing.T) {
	m, r, root, producer := projectFixture(t)
	m.SetProjectProducers([]model.LSPProjectProducer{producer})
	client := startedProject(t, m, r)
	if calls := client.snapshot(); len(calls) != 0 {
		t.Fatalf("a project producer sent document traffic: %v", calls)
	}
	if got := r.specs[0]; got.Command != "project-check" || r.roots[0] == "" {
		t.Fatalf("launch = %+v at %q", got, r.roots[0])
	}

	util := filepath.Join(root, "util.go")
	client.emitUnopened(util, LSPDiagnostic{Line: 0, Col: 8, EndLine: 0, EndCol: 12, Message: "undefined: x", Severity: model.DiagError})
	batch, ok := m.FileDiagnostics(util)
	if !ok || !batch.FromProjectCheck() || batch.HasExactSource() || len(batch.Diagnostics) != 1 {
		t.Fatalf("project fact = %+v present=%v", batch, ok)
	}
	if summary := m.ProjectInsights(); summary.Errors != 1 || summary.FilesAffected != 1 {
		t.Fatalf("summary does not count the unopened file: %+v", summary)
	}

	// An editor edit elsewhere changes editor context, not the project's
	// report. The server republishes what the edit affects.
	editorSource(t, m, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	if batch, _ := m.FileDiagnostics(util); !batch.FromProjectCheck() {
		t.Fatalf("an unrelated editor change made the project fact stale: %+v", batch)
	}

	// The fix clears the problem: the server reports an empty batch.
	client.emitUnopened(util)
	if batch, _ := m.FileDiagnostics(util); !batch.FromProjectCheck() || len(batch.Diagnostics) != 0 {
		t.Fatalf("clearing batch = %+v", batch)
	}
	if summary := m.ProjectInsights(); summary.Errors != 0 {
		t.Fatalf("summary kept a fixed problem: %+v", summary)
	}

	// Reports outside the project root are ignored.
	client.emitUnopened(filepath.Join(filepath.Dir(root), "elsewhere.go"), LSPDiagnostic{Message: "outside"})
	if _, ok := m.FileDiagnostics(filepath.Join(filepath.Dir(root), "elsewhere.go")); ok {
		t.Fatal("a report outside the workspace was published")
	}
}

func TestOpenDocumentOwnsItsFileAndProducerRemovalClearsFacts(t *testing.T) {
	m, r, root, producer := projectFixture(t)
	m.SetProjectProducers([]model.LSPProjectProducer{producer})
	client := startedProject(t, m, r)
	util := filepath.Join(root, "util.go")
	client.emitUnopened(util, LSPDiagnostic{Message: "from the project"})
	if batch, _ := m.FileDiagnostics(util); !batch.FromProjectCheck() {
		t.Fatalf("project fact missing: %+v", batch)
	}

	// Opening the file hands it to the document. The project view goes away
	// and later unopened reports for it are ignored while it stays open.
	editorSource(t, m, util, "package main\n")
	if batch, ok := m.FileDiagnostics(util); ok && batch.FromProjectCheck() {
		t.Fatalf("the project view survived an open document: %+v", batch)
	}
	client.emitUnopened(util, LSPDiagnostic{Message: "late project report"})
	if batch, ok := m.FileDiagnostics(util); ok && batch.FromProjectCheck() {
		t.Fatalf("an unopened report replaced an open document's facts: %+v", batch)
	}

	main := filepath.Join(root, "main.go")
	client.emitUnopened(main, LSPDiagnostic{Message: "main problem"})
	if batch, _ := m.FileDiagnostics(main); !batch.FromProjectCheck() {
		t.Fatalf("project fact missing for main.go: %+v", batch)
	}
	m.SetProjectProducers(nil)
	if _, ok := m.FileDiagnostics(main); ok {
		t.Fatal("facts of a removed producer are still visible")
	}
	awaitLSP(t, func() bool {
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.stopped
	})
}

func TestProjectProducerRespectsTrustPauseAndRoot(t *testing.T) {
	r := &lifecycleRuntime{}
	m := NewLSPManager(nil, r)
	t.Cleanup(m.Close)
	root := t.TempDir()
	m.SetWorkspaceRoot(root)
	m.SetProjectProducers([]model.LSPProjectProducer{{Root: root, Spec: model.LSPServerSpec{Command: "project-check"}}})
	time.Sleep(50 * time.Millisecond)
	if len(r.snapshot()) != 0 {
		t.Fatal("a project producer started in an untrusted folder")
	}

	m2, r2, root2, producer := projectFixture(t)
	outside := producer
	outside.Root = filepath.Dir(root2)
	m2.SetProjectProducers([]model.LSPProjectProducer{outside})
	if got := m2.ProjectProducers(); len(got) != 0 {
		t.Fatalf("a producer outside the workspace was accepted: %+v", got)
	}
	m2.Stop()
	m2.SetProjectProducers([]model.LSPProjectProducer{producer})
	time.Sleep(50 * time.Millisecond)
	if len(r2.snapshot()) != 0 {
		t.Fatal("a project producer started while language servers were stopped")
	}
	m2.StartForWorkspace(root2)
	startedProject(t, m2, r2)
}
