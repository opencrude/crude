package workspace

import (
	"context"
	model "crude/types"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type loadProbeClient struct {
	reapableClient
	mu     sync.Mutex
	opened []string
}

func (c *loadProbeClient) OpenFile(_ context.Context, path, _, _ string, _ model.InsightBoundary) (int, error) {
	c.mu.Lock()
	c.opened = append(c.opened, path)
	c.mu.Unlock()
	return 1, nil
}

type loadProbeRuntime struct{ client *loadProbeClient }

func (r loadProbeRuntime) NewClient(model.LSPServerSpec, string) LSPClient { return r.client }

func TestLSPEnabledUnusedWorkspaceDoesNotOpenSources(t *testing.T) {
	root := t.TempDir()
	createFile(t, filepath.Join(root, "go.mod"), "module example.test/load\n\ngo 1.25\n")
	for _, path := range []string{"main.go", "platform_windows.go", "third_party/dependency/module.go", "testdata/broken.go"} {
		mkdir(t, filepath.Dir(filepath.Join(root, path)))
		createFile(t, filepath.Join(root, path), "package main\n")
	}
	client := &loadProbeClient{}
	m := NewLSPManager(nil, loadProbeRuntime{client})
	t.Cleanup(m.Close)
	m.StartForWorkspace(root)
	time.Sleep(100 * time.Millisecond)
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.opened) != 0 {
		t.Fatalf("enabling unused LSP falsely opens %d editor documents: %v", len(client.opened), client.opened)
	}
}
