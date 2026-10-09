package viewmodel

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"crude/clientstate"
	"crude/workspace"
)

func configureComposerDraftTest(t *testing.T, app *App) {
	t.Helper()
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := store.Acquire(t.Context(), "test", clientstate.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ConfigureComposerDrafts(store, attachment, "workspace"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.StopAsyncIO()
		app.composerDraftClient = nil
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
}

func TestComposerPrivateClientsSameSession(t *testing.T) {
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "clients.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess := workspace.NewSession("same-session", t.TempDir(), nil, nil)
	for _, id := range []string{"one", "two"} {
		app := newFullTestApp(t)
		attachment, err := store.Acquire(t.Context(), id, clientstate.AcquireOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := app.ConfigureComposerDrafts(store, attachment, "same-workspace"); err != nil {
			t.Fatal(err)
		}
		app.ActiveSession = sess
		cmd := app.hydrateComposerDraftCmd()
		app.applyComposerDraft(cmd().(composerDraftLoadedMsg))
		app.Input.SetInput(id + " private draft")
		app.Input.UseReferenceIDs([]string{id + "-ref"})
		app.Input.SetCursorFlat(len(id))
		app.flushComposerDraft()
		if app.composerDraftErr != nil {
			t.Fatal(app.composerDraftErr)
		}
		// Prevent test cleanup from accessing the externally-owned closed Store.
		app.composerDraftClient = nil
	}
	for _, id := range []string{"one", "two"} {
		record, err := store.Load(id, 1, clientstate.Key{ClientID: id, WorkspaceID: "same-workspace", SessionID: sess.SessionID})
		if err != nil {
			t.Fatal(err)
		}
		if record.Draft.Text != id+" private draft" || record.Draft.Cursor != len(id) || len(record.Draft.RefIDs) != 1 || record.Draft.RefIDs[0] != id+"-ref" {
			t.Fatalf("cross-client state: %+v", record)
		}
	}
}

func TestComposerPrivateCASConflictPreservesExternalDraft(t *testing.T) {
	app := newFullTestApp(t)
	configureComposerDraftTest(t, app)
	c := app.composerDraftClient
	r := c.record("session")
	if _, err := r.load(c); err != nil {
		t.Fatal(err)
	}
	_, err := c.store.Save(t.Context(), clientstate.Put{ClientID: c.attachment.ClientID, Epoch: c.attachment.Epoch, RequestID: "other", Key: r.key, Draft: clientstate.Draft{Text: "external"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(clientstate.Draft{Text: "local"})
	if err := r.save(c, raw); !errors.Is(err, clientstate.ErrConflict) {
		t.Fatalf("save = %v", err)
	}
	if err := r.save(c, raw); !errors.Is(err, clientstate.ErrConflict) {
		t.Fatalf("retry silently overwrote conflict: %v", err)
	}
	record, err := c.store.Load(c.attachment.ClientID, c.attachment.Epoch, r.key)
	if err != nil || record.Draft.Text != "external" {
		t.Fatalf("external draft lost: %+v %v", record, err)
	}
}

func TestComposerPrivateBindingRejectsOldLoadAndEpoch(t *testing.T) {
	app := newFullTestApp(t)
	configureComposerDraftTest(t, app)
	sess := workspace.NewSession("same", t.TempDir(), nil, nil)
	app.ActiveSession = sess
	cmd := app.hydrateComposerDraftCmd()
	old := cmd().(composerDraftLoadedMsg)
	app.applyComposerDraft(old)
	app.Input.SetInput("old workspace")
	if err := app.ChangeComposerDraftWorkspace("new-workspace"); err != nil {
		t.Fatal(err)
	}
	cmd = app.hydrateComposerDraftCmd()
	app.applyComposerDraft(cmd().(composerDraftLoadedMsg))
	app.Input.SetInput("new workspace")
	app.applyComposerDraft(old)
	if app.Input.Value() != "new workspace" {
		t.Fatal("old namespace load overwrote new draft")
	}
	c := app.composerDraftClient
	if _, err := c.store.Acquire(t.Context(), c.attachment.ClientID, clientstate.AcquireOptions{Takeover: true, ExpectedEpoch: c.attachment.Epoch}); err != nil {
		t.Fatal(err)
	}
	app.flushComposerDraft()
	if !errors.Is(app.composerDraftErr, clientstate.ErrStaleEpoch) {
		t.Fatalf("stale attachment saved: %v", app.composerDraftErr)
	}
	app.composerDraftClient = nil
}
