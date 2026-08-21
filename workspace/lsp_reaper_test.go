package workspace

import (
	"context"
	"testing"
	"time"
)

// reapableClient is an LSPClient fake whose Done channel the test controls,
// letting the reaper test exercise the restart-on-death lifecycle.
type reapableClient struct {
	done chan struct{}
}

func (c *reapableClient) OnDiagnostics(func(string, []LSPDiagnostic))      {}
func (c *reapableClient) Start(context.Context) error                      { return nil }
func (c *reapableClient) Stop()                                            {}
func (c *reapableClient) Done() <-chan struct{}                            { return c.done }
func (c *reapableClient) OpenFile(context.Context, string, string, string) {}
func (c *reapableClient) UpdateFile(context.Context, string, string)       {}
func (c *reapableClient) CloseFile(context.Context, string)                {}
func (c *reapableClient) NotifyWatchedFilesChanged(context.Context, string, int) {
}
func (c *reapableClient) SyncFileFromDisk(context.Context, string, int) {}
func (c *reapableClient) RequestDefinition(context.Context, string, int, int) []LSPLocation {
	return nil
}
func (c *reapableClient) FindReferences(context.Context, string, int, int, bool) []LSPLocation {
	return nil
}
func (c *reapableClient) RequestDocumentSymbols(context.Context, string) []LSPSymbol {
	return nil
}
func (c *reapableClient) RequestHover(context.Context, string, int, int) (string, error) {
	return "", nil
}
func (c *reapableClient) RequestRename(context.Context, string, int, int, string) (interface{}, error) {
	return nil, nil
}
func (c *reapableClient) RequestCompletions(context.Context, string, int, int) []CompletionItem {
	return nil
}

// TestLSPManager_ReapsDeadClient verifies the restart-on-death lifecycle:
// when a client's Done closes (server died / pipe wedged), the manager
// removes it from the registry and marks it failed so the next OpenFile
// starts a fresh server.
func TestLSPManager_ReapsDeadClient(t *testing.T) {
	client := &reapableClient{done: make(chan struct{})}
	mgr := NewLSPManager(testLogger{})

	root := t.TempDir()
	key := clientKey("file://"+root, "fake")
	mgr.mu.Lock()
	mgr.clients[key] = client
	mgr.states[key] = lspClientState{rootURI: "file://" + root, cmd: "fake", status: LSPConnected}
	mgr.mu.Unlock()

	mgr.reapOnDone(key, client, "file://"+root, "fake")

	// Kill the client; the reaper must observe it.
	close(client.done)
	deadline := time.Now().Add(3 * time.Second)
	for {
		mgr.mu.Lock()
		_, stillRegistered := mgr.clients[key]
		state, hasState := mgr.states[key]
		mgr.mu.Unlock()
		if !stillRegistered {
			if !hasState || state.status != LSPFailed {
				t.Fatalf("reaped client state=%+v want LSPFailed", state)
			}
			if state.err == "" {
				t.Fatal("reaped client must carry a failure reason")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("dead client was not reaped")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestLSPManager_ReaperNoOpAfterGracefulStop verifies a graceful Stop
// (which removes the client first) makes the reaper a no-op.
func TestLSPManager_ReaperNoOpAfterGracefulStop(t *testing.T) {
	client := &reapableClient{done: make(chan struct{})}
	mgr := NewLSPManager(testLogger{})

	root := t.TempDir()
	key := clientKey("file://"+root, "fake")
	mgr.mu.Lock()
	mgr.clients[key] = client
	mgr.states[key] = lspClientState{rootURI: "file://" + root, cmd: "fake", status: LSPConnected}
	mgr.mu.Unlock()

	mgr.reapOnDone(key, client, "file://"+root, "fake")

	// Graceful stop removes the client; the subsequent Done must not
	// resurrect a failed state.
	mgr.removeClient(client)
	close(client.done)
	time.Sleep(50 * time.Millisecond)

	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if _, ok := mgr.clients[key]; ok {
		t.Fatal("client must stay removed after graceful stop + Done")
	}
	state, ok := mgr.states[key]
	if ok && state.status == LSPFailed {
		t.Fatal("reaper must not overwrite a gracefully-stopped client state")
	}
}
