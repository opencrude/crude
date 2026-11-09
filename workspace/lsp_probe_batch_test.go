package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"crude/types"
)

type batchProbeClient struct {
	lifecycleClient
	entered chan string
	release chan struct{}
}

func (c *batchProbeClient) AwaitDiagnostics(ctx context.Context, path string) (types.InsightDiagnosticPublication, bool) {
	publication := c.publication(path, nil)
	c.entered <- path
	select {
	case <-ctx.Done():
		return types.InsightDiagnosticPublication{}, false
	case <-c.release:
		return publication, true
	}
}

type batchProbeRuntime struct{ client *batchProbeClient }

func (r batchProbeRuntime) NewClient(types.LSPServerSpec, string, func() error) LSPClient {
	return r.client
}

func batchProbeFixture(t *testing.T) (*LSPManager, *batchProbeClient, []string) {
	t.Helper()
	root := t.TempDir()
	client := &batchProbeClient{
		lifecycleClient: lifecycleClient{runtime: &lifecycleRuntime{}, reapableClient: reapableClient{done: make(chan struct{})}},
		entered:         make(chan string, 10), release: make(chan struct{}),
	}
	m := NewLSPManager(nil, batchProbeRuntime{client})
	t.Cleanup(m.Close)
	probeSources(t, m, root)
	m.SetWorkspaceRoot(root)
	trustAnalysis(t, m, root)
	m.SetSettings(editorDisabledSettings())
	var paths []string
	for i := range 10 {
		path := filepath.Join(root, fmt.Sprintf("file%d.go", i))
		createFile(t, path, "package example\n")
		paths = append(paths, path)
	}
	return m, client, paths
}

func TestProbeDiagnosticsMultipleFilesKeepTheirResults(t *testing.T) {
	m, client, paths := batchProbeFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan []types.LSPDiagnosticsResult, 1)
	go func() { done <- m.ProbeDiagnosticsBatch(ctx, paths) }()
	for range paths {
		select {
		case <-client.entered:
		case <-ctx.Done():
			t.Fatal("diagnostic requests did not start", ctx.Err())
		}
	}
	close(client.release)
	results := <-done
	for i, result := range results {
		if result.Error != "" || !result.Settled || result.Diagnostics.ID == "" ||
			len(result.Diagnostics.Diagnostics) != 0 || result.View.Source.Path != paths[i] ||
			result.View.SourceText != "package example\n" || result.Diagnostics.Boundary.Source != result.View.Source {
			t.Errorf("%s: invalid clean-file result: %+v", filepath.Base(paths[i]), result)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.documents) != 0 {
		t.Fatal("completed batch retained borrowed document views")
	}
}

func TestProbeDiagnosticsBatchKeepsPerFileFailuresAndDuplicateOrder(t *testing.T) {
	m, client, paths := batchProbeFixture(t)
	close(client.release)
	input := []string{paths[2], filepath.Join(filepath.Dir(paths[0]), "missing.go"), paths[0], paths[2]}
	results := m.ProbeDiagnosticsBatch(t.Context(), input)
	if len(results) != 4 || results[1].Error == "" || results[1].Diagnostics.ID != "" ||
		results[0].Diagnostics.ID == "" || results[0].Diagnostics.ID != results[3].Diagnostics.ID {
		t.Fatalf("missing file or duplicate lost its place: %+v", results)
	}
	for _, i := range []int{0, 2, 3} {
		if results[i].Error != "" || !results[i].Settled || results[i].View.Source.Path != input[i] {
			t.Fatalf("valid file %d failed with its missing sibling: %+v", i, results[i])
		}
	}
}

func TestProbeDiagnosticsBatchRejectsChangedSourceOrAdmission(t *testing.T) {
	for _, change := range []string{"edit", "cancel", "disable"} {
		t.Run(change, func(t *testing.T) {
			m, client, paths := batchProbeFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan []types.LSPDiagnosticsResult, 1)
			go func() { done <- m.ProbeDiagnosticsBatch(ctx, paths) }()
			for range paths {
				select {
				case <-client.entered:
				case <-ctx.Done():
					t.Fatal("batch did not reach the server", ctx.Err())
				}
			}
			switch change {
			case "edit":
				probeEditor(t, m, paths[0], "package changed\n")
			case "cancel":
				cancel()
			case "disable":
				settings := m.Settings()
				settings.Diagnostics = false
				m.SetSettings(settings)
			}
			close(client.release)
			for _, result := range <-done {
				if result.Error == "" || result.Settled || result.Diagnostics.ID != "" {
					t.Fatalf("%s allowed an invalid result: %+v", change, result)
				}
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			for path, doc := range m.documents {
				if doc.probes != 0 {
					t.Fatalf("%s retained %d probe references for %s", change, doc.probes, path)
				}
			}
		})
	}
}
