package workspace

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/toolstore"
	"crude/types"
)

func managedLSPFixture(t *testing.T) (*toolstore.Store, toolstore.Tool) {
	t.Helper()
	root := t.TempDir()
	digest := strings.Repeat("a", 64)
	dir := filepath.Join(root, "fixture-server", "1-"+digest[:12])
	executable := filepath.Join(dir, "files", "server")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	createFile(t, executable, "fixture transport is supplied by lifecycleRuntime")
	manifest := map[string]any{
		"id": "fixture-server/1-" + digest[:12], "package": "fixture-server", "version": "1",
		"sha256": digest, "digest_origin": "expected", "executable": "server",
		"installed_at": time.Now().UTC(), "installed_by": "user", "approval": "fixture-approval",
		"archive": "raw", "url": "https://example.invalid/tool",
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	// These tests supply a transport, not an installer. Model an immutable,
	// finalized publication so delayed Windows creation metadata is not a
	// concurrent filesystem edit between the independent removal previews.
	var paths []string
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		if !entry.IsDir() {
			return os.Chmod(path, 0444)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := len(paths) - 1; i >= 0; i-- {
		if err := os.Chtimes(paths[i], stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() {
				_ = os.Chmod(path, 0600)
			}
			return nil
		})
	})
	store, err := toolstore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := store.Lookup("fixture-server", "1")
	if !ok {
		t.Fatal("managed fixture was not recognized")
	}
	return store, tool
}

func managedLifecycleManager(t *testing.T, store *toolstore.Store, executable string) (*LSPManager, *lifecycleRuntime, string) {
	t.Helper()
	m, r, root := lifecycleManager(t)
	m.SetToolStore(store)
	settings := m.Settings()
	settings.Languages["lease-fixture"] = types.LSPLanguageSettings{
		Enabled: true, Command: executable, Extensions: []string{".leasefixture"}, LanguageID: "lease-fixture",
	}
	m.SetSettings(settings)
	return m, r, filepath.Join(root, "main.leasefixture")
}

func requireManagedRemovalBlocked(t *testing.T, store *toolstore.Store, tool toolstore.Tool) {
	t.Helper()
	preview, err := store.PreviewRemove(t.Context(), []string{tool.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(t.Context(), preview, "user"); !errors.Is(err, toolstore.ErrExecutableInUse) {
		t.Fatalf("running exact executable was removable: %v", err)
	}
}

func removeManagedFixture(t *testing.T, store *toolstore.Store, tool toolstore.Tool) {
	t.Helper()
	preview, err := store.PreviewRemove(t.Context(), []string{tool.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(t.Context(), preview, "user"); err != nil {
		t.Fatal(err)
	}
}

func TestLSPManagedLeasePinsUntilExactStopJoinsAndRestartReacquires(t *testing.T) {
	store, tool := managedLSPFixture(t)
	m, r, path := managedLifecycleManager(t, store, tool.Executable)
	stopGate := make(chan struct{})
	r.stopGate = stopGate
	// Ensure cleanup cannot deadlock if a blocked-removal assertion fails.
	defer func() {
		select {
		case <-stopGate:
		default:
			close(stopGate)
		}
	}()
	editorSource(t, m, path, "fixture")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	requireManagedRemovalBlocked(t, store, tool)
	id := m.ServerHealthSnapshots()[0].ID
	if !m.StopServer(id) {
		t.Fatal("stop refused")
	}
	requireManagedRemovalBlocked(t, store, tool)
	close(stopGate)
	awaitLSP(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.states[id] != nil && m.states[id].client == nil && !m.states[id].closing
	})
	if !m.RestartServer(id) {
		t.Fatal("restart refused")
	}
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 2 && len(cs[1].snapshot()) == 1 })
	requireManagedRemovalBlocked(t, store, tool)
	m.Close()
	removeManagedFixture(t, store, tool)
}

func TestLSPManagedLeaseSharedAcrossTwoWorkspaceManagers(t *testing.T) {
	store, tool := managedLSPFixture(t)
	first, a, firstPath := managedLifecycleManager(t, store, tool.Executable)
	second, b, secondPath := managedLifecycleManager(t, store, tool.Executable)
	editorSource(t, first, firstPath, "first")
	editorSource(t, second, secondPath, "second")
	awaitLSP(t, func() bool {
		x, y := a.snapshot(), b.snapshot()
		return len(x) == 1 && len(y) == 1 && len(x[0].snapshot()) == 1 && len(y[0].snapshot()) == 1
	})
	current, ok := store.Lookup(tool.Package, tool.Version)
	if !ok || len(current.Workspaces) != 2 || current.LastUsed.IsZero() {
		t.Fatalf("actual launches did not record both workspaces: %+v", current)
	}
	first.Close()
	requireManagedRemovalBlocked(t, store, tool)
	second.Close()
	removeManagedFixture(t, store, tool)
}

func TestLSPManagedLeaseRetainedAfterUnverifiedTeardown(t *testing.T) {
	store, tool := managedLSPFixture(t)
	m, r, path := managedLifecycleManager(t, store, tool.Executable)
	r.stopErr = errors.New("exact process teardown is unverified")
	editorSource(t, m, path, "fixture")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	m.Close()
	requireManagedRemovalBlocked(t, store, tool)
	if health := m.ServerHealthSnapshots(); len(health) != 1 || !strings.Contains(health[0].Error, "unverified") {
		t.Fatalf("failed teardown lost its owner: %+v", health)
	}
	// The fake transport can now positively join; the exact retained wrapper
	// releases its pin, not a manager key that a restart could have replaced.
	client := r.snapshot()[0]
	client.mu.Lock()
	client.stopErr = nil
	client.mu.Unlock()
	id := m.ServerHealthSnapshots()[0].ID
	m.mu.Lock()
	exact := m.states[id].client
	m.mu.Unlock()
	if err := exact.Stop(); err != nil {
		t.Fatal(err)
	}
	removeManagedFixture(t, store, tool)
}

func TestLSPStaleManagedExecutableNeverConstructsRuntime(t *testing.T) {
	store, tool := managedLSPFixture(t)
	removeManagedFixture(t, store, tool)
	m, r, path := managedLifecycleManager(t, store, tool.Executable)
	editorSource(t, m, path, "fixture")
	awaitLSP(t, func() bool {
		health := m.ServerHealthSnapshots()
		return len(health) == 1 && health[0].Status == "Failed"
	})
	if len(r.snapshot()) != 0 {
		t.Fatal("fake runtime bypassed missing managed executable refusal")
	}
	current, _, err := store.List(t.Context())
	if err != nil || len(current) != 0 {
		t.Fatalf("stale launch changed the store: %+v, %v", current, err)
	}
}
