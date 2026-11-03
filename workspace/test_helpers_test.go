package workspace

import "testing"

func setTestHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestDocumentFixtureJoinsLSPWorker(t *testing.T) {
	var manager *LSPManager
	var done <-chan struct{}
	if !t.Run("fixture", func(t *testing.T) {
		service, _, _, _ := documentFixture(t, "retained")
		manager = service.lsp
		// Opening through DocumentService starts the real manager worker even
		// when analysis is restricted and no server process is admitted.
		manager.mu.Lock()
		done = manager.workerDone
		manager.mu.Unlock()
		if done == nil {
			t.Fatal("document fixture did not start its LSP worker")
		}
	}) {
		return
	}
	// Always join on regression failure as well, so this test cannot itself
	// retain the worker it detects. Subtest cleanup must already have joined it.
	defer manager.Close()
	select {
	case <-done:
	default:
		t.Fatal("document fixture cleanup left its LSP worker running")
	}
}
