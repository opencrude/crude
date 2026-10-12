package viewmodel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/clientstate"
	"crude/document"
)

func TestEditorRecoveryShutdownAndReopen(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "draft.txt")
	if err := os.WriteFile(path, []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	one := newRemoteJourneyShell(t, runtime, root)
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "private.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	a, err := store.Acquire(context.Background(), "test-profile", clientstate.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = one.app.ConfigureComposerDrafts(store, a, "test-workspace"); err != nil {
		t.Fatal(err)
	}
	ed := liveTestOpen(t, one.app, path)
	ed.InsertRune('X')
	one.app.liveEditors[ed].err = errors.New("injected unresolved conflict")
	one.app.StopAsyncIO()
	key := clientstate.EditorRecoveryKey{ClientID: a.ClientID, WorkspaceID: "test-workspace", Path: path}
	r, err := store.LoadEditorRecovery(context.Background(), a, key)
	if err != nil || len(r.Payload) == 0 {
		t.Fatalf("no private retention: %+v %v", r, err)
	}
	two := newRemoteJourneyShell(t, runtime, root)
	if err = two.app.ConfigureComposerDrafts(store, a, "test-workspace"); err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, two.app, two.app.openPaneFiles([]string{filepath.ToSlash(path)}, two.app.Win.ActiveGroup()))
	reopened := two.app.EditorState.Tabs[path].Editor
	// Reopening exercises the actual pane-load recovery route, not a second
	// helper restore over a draft already installed by that same route.
	if reopened.Value() != "Xbase" || reopened.DocumentState().Conflict == nil {
		t.Fatalf("recovered: %q %+v", reopened.Value(), reopened.DocumentState())
	}
	if _, err = reopened.CaptureDocumentEdits(document.Owner{ClientID: "other", Epoch: 99}, "new", "group"); err == nil {
		t.Fatal("recovery automatically replayable")
	}
	// A released attachment makes retention fail; shutdown must not destroy
	// the still-live in-memory draft or wait forever on private persistence.
	if err = store.Release(context.Background(), a.ClientID, a.Epoch); err != nil {
		t.Fatal(err)
	}
	two.app.StopAsyncIO()
	if !reopened.IsOpen() || reopened.Value() != "Xbase" {
		t.Fatal("persistence failure dropped in-memory draft")
	}
}
