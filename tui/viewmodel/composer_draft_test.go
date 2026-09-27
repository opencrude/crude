package viewmodel

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"crude/sessiondb"
	"crude/workspace"
)

// TestMaybeHydrateComposerDraftClearsOnSwitch guards leak prevention: the
// moment the active-session pointer changes, the composer clears BEFORE any
// async load completes, so one session's draft can never flash into another.
func TestMaybeHydrateComposerDraftClearsOnSwitch(t *testing.T) {
	app := newFullTestApp(t)
	sessA := workspace.NewSession("a", t.TempDir(), nil, nil)
	sessB := workspace.NewSession("b", t.TempDir(), nil, nil)

	app.ActiveSession = sessA
	app.Input.SetInput("session A draft")
	app.maybeHydrateComposerDraft()
	if got := app.Input.Value(); got != "" {
		t.Fatalf("hydration must clear the composer on switch, got %q", got)
	}
	if app.composerHydratedFor != sessA {
		t.Fatal("hydration watermark must track the active session")
	}

	app.maybeHydrateComposerDraft() // same pointer → no-op
	if got := app.Input.Value(); got != "" {
		t.Fatalf("repeated hydration must stay inert, got %q", got)
	}

	app.ActiveSession = sessB
	app.Input.SetInput("stale")
	app.maybeHydrateComposerDraft()
	if got := app.Input.Value(); got != "" {
		t.Fatalf("draft leaked across sessions: %q", got)
	}
}

// TestComposerDraftRoundTripWithDB exercises save→load→apply against a real
// SQLite session DB: text plus chip spans persist; on restore, chips whose
// backing reference died are removed positionally (prose brackets untouched),
// while live chips resolve and the caret clamps into bounds.
func TestComposerDraftRoundTripWithDB(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.Create(db, sessiondb.SessionMeta{ID: "roundtrip", ProjectPath: t.TempDir(), ProjectName: "test", Model: "test", Provider: "test", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}

	app := newFullTestApp(t)
	sess := workspace.NewSession("roundtrip", t.TempDir(), db, nil)
	app.ActiveSession = sess
	app.Input.SetReferenceStore(sess.RefStore)

	refKeep, err := sess.RefStore.CreateReference(sessiondb.CreateRequest{
		Kind:  sessiondb.KindSelectedFile,
		Title: "keep.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	refGhost, err := sess.RefStore.CreateReference(sessiondb.CreateRequest{
		Kind:  sessiondb.KindSelectedFile,
		Title: "ghost.txt",
	})
	if err != nil {
		t.Fatal(err)
	}

	draft := refKeep.DisplayName() + " mid " + refGhost.DisplayName() + " tail"
	app.Input.SetInput(draft)
	snap, err := json.Marshal(composerDraftSnapshot{
		Text:   app.Input.Value(),
		RefIDs: app.Input.ReferencedIDs(),
		Cursor: 4,
		Spans:  composerDraftRefSpans(app.Input.RefRanges()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := app.saveComposerDraftMsg(db, snap).(composerDraftSavedMsg); !ok {
		t.Fatal("save must acknowledge with composerDraftSavedMsg")
	}

	// The ghost attachment is deleted elsewhere before rehydration.
	if err := sess.RefStore.DeleteReference(refGhost.ID); err != nil {
		t.Fatal(err)
	}

	loaded := app.loadComposerDraftMsg(sess)
	lm, ok := loaded.(composerDraftLoadedMsg)
	if !ok {
		t.Fatalf("load returned %T, want composerDraftLoadedMsg", loaded)
	}
	app.applyComposerDraft(lm)

	want := refKeep.DisplayName() + " mid tail"
	if got := app.Input.Value(); got != want {
		t.Fatalf("hydrated draft = %q, want %q (dead chip removed)", got, want)
	}
	ids := app.Input.ReferencedIDs()
	if len(ids) != 1 || ids[0] != refKeep.ID {
		t.Fatalf("hydrated references = %v, want [%s]", ids, refKeep.ID)
	}
	if cursor := app.Input.CursorFlat(); cursor < 0 || cursor > len([]rune(want)) {
		t.Fatalf("hydrated caret %d out of bounds for %d runes", cursor, len([]rune(want)))
	}
}
