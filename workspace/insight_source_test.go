package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"crude/document"
	"crude/types"
)

func TestInsightAgentProbeUsesRetainedSourceWithoutDiskOrEditorViews(t *testing.T) {
	manager, runtime, root := lifecycleManager(t)
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package disk\n")
	documents := NewDocumentService(root, nil, manager)
	t.Cleanup(documents.Close)
	owner := document.Owner{ClientID: "source-author", Epoch: 1}
	snapshot, err := documents.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	unsaved := "package retained\nvar message = \"😀 unsaved\"\n"
	documentEdit(t, documents, owner, snapshot.ID, "unsaved", snapshot.Revision, 0, len(snapshot.Text), unsaved)
	if err := documents.CloseView(owner, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, settled, view, err := manager.ProbeDiagnostics(ctx, path)
	if err != nil || !settled || !view.BufferDiffers {
		t.Fatalf("agent did not analyze retained source after the last editor closed: settled=%v view=%+v err=%v", settled, view, err)
	}
	clients := runtime.snapshot()
	if len(clients) != 1 || !slices.Contains(clients[0].snapshot(), "open:main.go:go:"+unsaved) {
		t.Fatal("agent producer did not receive the unsaved canonical bytes")
	}
}

func TestInsightSourceLeaseFencesCanonicalRevisionAndEqualTextUndo(t *testing.T) {
	sources, initial, owner := insightFixture(t, "source.txt", "canonical 😀\n")
	lease, err := sources.acquireAnalysisSource(t.Context(), initial.Path)
	if err != nil || lease.canonical == nil || string(lease.snapshot.Text) != "canonical 😀\n" {
		t.Fatal("analysis did not borrow canonical bytes", err)
	}
	if err := lease.validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	change := documentEdit(t, sources, owner, initial.ID, "changed", initial.Revision, 0, 9, "temporary")
	if _, err := sources.Undo(owner, initial.ID, "undo", change.Revision); err != nil {
		t.Fatal(err)
	}
	if err := lease.validate(t.Context()); !errors.Is(err, document.ErrRevision) {
		t.Fatal("equal-text undo revived a retired source revision", err)
	}
	latest, err := sources.acquireAnalysisSource(t.Context(), initial.Path)
	if err != nil || string(latest.snapshot.Text) != "canonical 😀\n" {
		t.Fatal("canonical undo bytes were lost", err)
	}
	latest.snapshot.Text[0] = 'X'
	if got, err := sources.Snapshot(initial.ID); err != nil || string(got.Text) != "canonical 😀\n" {
		t.Fatal("mutable analysis snapshot escaped into canonical text", err)
	}
	latest.release()
	latest.release()
	if err := latest.validate(t.Context()); !errors.Is(err, document.ErrOwner) {
		t.Fatal("released analysis lease was still active", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := sources.acquireAnalysisSource(ctx, initial.Path); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled request acquired a source", err)
	}
}

func TestInsightDiskLeaseIsBoundedIndependentAndCannotBecomeCanonical(t *testing.T) {
	root := t.TempDir()
	sources := NewDocumentService(root, nil, nil)
	t.Cleanup(sources.Close)
	path := filepath.Join(root, "disk.txt")
	if _, err := sources.acquireAnalysisSource(t.Context(), path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing disk source became an analyzed empty document", err)
	}
	createFile(t, path, "exact disk bytes\n")
	lease, err := sources.acquireAnalysisSource(t.Context(), path)
	if err != nil || lease.canonical != nil || string(lease.snapshot.Text) != "exact disk bytes\n" {
		t.Fatal("wrong disk snapshot", err)
	}
	if len(sources.docs) != 0 {
		t.Fatal("analysis request silently opened an editor/canonical document")
	}
	if err := lease.validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "replacement.txt")
	createFile(t, replacement, "exact disk bytes\n")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := lease.validate(t.Context()); !errors.Is(err, document.ErrRevision) {
		t.Fatal("equal-byte disk replacement inherited an older lease", err)
	}
	lease, err = sources.acquireAnalysisSource(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sources.Open(document.Owner{ClientID: "new-editor", Epoch: 1}, path); err != nil {
		t.Fatal(err)
	}
	if err := lease.validate(t.Context()); !errors.Is(err, document.ErrRevision) {
		t.Fatal("disk answer silently rebound to a later canonical view", err)
	}
}

func TestInsightSourceRefusesSymlinkAndCompetingAuthority(t *testing.T) {
	root := t.TempDir()
	sources := NewDocumentService(root, nil, nil)
	t.Cleanup(sources.Close)
	target, alias := filepath.Join(root, "target.txt"), filepath.Join(root, "alias.txt")
	createFile(t, target, "not alias authority")
	if err := os.Symlink(target, alias); err == nil {
		if _, err := sources.acquireAnalysisSource(t.Context(), alias); err == nil {
			t.Fatal("analysis followed a symlink outside its exact path authority")
		}
	} else {
		t.Logf("native symlink creation unavailable: %v", err)
	}
	manager, runtime, project := lifecycleManager(t)
	path := filepath.Join(project, "main.go")
	createFile(t, path, "package main\n")
	if _, _, _, err := manager.ProbeDiagnostics(t.Context(), path); err == nil || len(runtime.snapshot()) != 0 {
		t.Fatal("probe without a source authority started a producer", err)
	}
	first := NewDocumentService(project, nil, manager)
	t.Cleanup(first.Close)
	second := NewDocumentService(project, nil, manager)
	t.Cleanup(second.Close)
	if _, err := second.Open(document.Owner{ClientID: "foreign", Epoch: 1}, path); err == nil || manager.sources != first {
		t.Fatal("a competing document owner replaced canonical analysis authority", err)
	}
}

type sourceAnswerClient struct {
	lifecycleClient
	entered chan struct{}
	release chan struct{}
}

func (c *sourceAnswerClient) AwaitDiagnostics(ctx context.Context, path string) (types.InsightDiagnosticPublication, bool) {
	p := c.publication(path, []LSPDiagnostic{{Message: "finding from the original source", Severity: types.DiagError}})
	close(c.entered)
	select {
	case <-ctx.Done():
		return types.InsightDiagnosticPublication{}, false
	case <-c.release:
		return p, true
	}
}

type sourceAnswerRuntime struct{ client *sourceAnswerClient }

func (r sourceAnswerRuntime) NewClient(types.LSPServerSpec, string, func() error) LSPClient {
	return r.client
}

func TestInsightProbeNeverReturnsPayloadForReplacedSource(t *testing.T) {
	for _, kind := range []string{"canonical-edit", "disk-edit", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "main.go")
			createFile(t, path, "package initial\n")
			client := &sourceAnswerClient{lifecycleClient: lifecycleClient{runtime: &lifecycleRuntime{}}, entered: make(chan struct{}), release: make(chan struct{})}
			manager := NewLSPManager(nil, sourceAnswerRuntime{client})
			t.Cleanup(manager.Close)
			manager.SetWorkspaceRoot(root)
			trustAnalysis(t, manager, root)
			sources := probeSources(t, manager, root)
			owner := document.Owner{ClientID: "query-author", Epoch: 1}
			var initial document.Snapshot
			if kind == "canonical-edit" {
				var err error
				initial, err = sources.Open(owner, path)
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var once sync.Once
			release := func() { once.Do(func() { close(client.release) }) }
			t.Cleanup(release)
			type result struct {
				diagnostics types.InsightDiagnostics
				settled     bool
				err         error
			}
			done := make(chan result, 1)
			go func() {
				diagnostics, settled, _, err := manager.ProbeDiagnostics(ctx, path)
				done <- result{diagnostics, settled, err}
			}()
			select {
			case <-client.entered:
			case <-ctx.Done():
				t.Fatal("probe never reached the held producer answer")
			}
			switch kind {
			case "canonical-edit":
				documentEdit(t, sources, owner, initial.ID, "newer", initial.Revision, 0, len(initial.Text), "package newer\n")
			case "disk-edit":
				createFile(t, path, "package newer\n")
			case "cancelled":
				cancel()
			}
			release()
			select {
			case got := <-done:
				if got.err == nil || got.settled || len(got.diagnostics.Diagnostics) != 0 {
					t.Fatalf("old source payload escaped a rejected request: %+v", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("source invalidation stranded the owning request")
			}
		})
	}
}
