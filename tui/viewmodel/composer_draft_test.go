package viewmodel

import (
	"context"
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
// SQLite session DB: text plus chip spans persist. Missing backing references
// remain visible with their exact IDs/spans until explicit repair or detach.
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
	app.composerDraftEpoch = 1
	app.composerHydratedFor = sess
	app.draftOwner(sess).epoch = 1
	app.draftOwner(sess).loadRevision = app.Input.DraftRevision()
	if _, ok := app.saveComposerDraftMsg(sess, db, 1, snap).(composerDraftSavedMsg); !ok {
		t.Fatal("save must acknowledge with composerDraftSavedMsg")
	}

	// The ghost attachment is deleted elsewhere before rehydration.
	if err := sess.RefStore.DeleteReference(refGhost.ID); err != nil {
		t.Fatal(err)
	}

	loaded := app.loadComposerDraftMsg(sess, 1, app.Input.DraftRevision())
	lm, ok := loaded.(composerDraftLoadedMsg)
	if !ok {
		t.Fatalf("load returned %T, want composerDraftLoadedMsg", loaded)
	}
	app.applyComposerDraft(lm)

	want := draft
	if got := app.Input.Value(); got != want {
		t.Fatalf("hydrated draft = %q, want exact unresolved text %q", got, want)
	}
	ids := app.Input.SnapshotReferenceIDs()
	if len(ids) != 2 || ids[0] != refKeep.ID || ids[1] != refGhost.ID {
		t.Fatalf("hydrated references = %v, want exact original IDs", ids)
	}
	if cursor := app.Input.CursorFlat(); cursor < 0 || cursor > len([]rune(want)) {
		t.Fatalf("hydrated caret %d out of bounds for %d runes", cursor, len([]rune(want)))
	}
}

func TestComposerDraftLateSaveAndLoadCannotCrossOwnerEpoch(t *testing.T) {
	app := newFullTestApp(t)
	first := workspace.NewSession("first", t.TempDir(), nil, nil)
	second := workspace.NewSession("second", t.TempDir(), nil, nil)
	app.ActiveSession = second
	app.composerHydratedFor = second
	app.composerDraftEpoch = 2
	app.draftOwner(first).epoch = 1
	app.draftOwner(first).inFlight = []byte("old")
	app.Input.SetInput("new owner draft")

	app.onComposerDraftSaved(composerDraftSavedMsg{session: first, epoch: 1, raw: []byte("other")})
	if len(app.draftOwner(first).inFlight) == 0 || app.Input.Value() != "new owner draft" {
		t.Fatal("stale save result mutated replacement owner state")
	}
	app.applyComposerDraft(composerDraftLoadedMsg{session: first, epoch: 1, text: "old owner draft", revision: 1})
	if app.Input.Value() != "new owner draft" {
		t.Fatal("stale load result overwrote replacement owner draft")
	}
}

func TestComposerDraftHydrationRejectsEditClearABA(t *testing.T) {
	app := newFullTestApp(t)
	sess := workspace.NewSession("owner", t.TempDir(), nil, nil)
	app.ActiveSession = sess
	app.composerHydratedFor = sess
	app.composerDraftEpoch = 4
	owner := app.draftOwner(sess)
	owner.epoch = 4
	owner.loadRevision = app.Input.DraftRevision()
	requestRevision := owner.loadRevision
	app.Input.SetInput("typed then cleared")
	app.Input.ClearInput()
	app.applyComposerDraft(composerDraftLoadedMsg{session: sess, epoch: 4, requestRevision: requestRevision, text: "stale"})
	if app.Input.Value() != "" {
		t.Fatal("late hydration overwrote an explicit edit/clear ABA")
	}
}

func TestComposerDraftCursorSelectionScrollOnlyChangesPersistAndFlush(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.Create(db, sessiondb.SessionMeta{ID: "owner", ProjectPath: t.TempDir(), ProjectName: "test", Model: "test", Provider: "test", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	app := newFullTestApp(t)
	sess := workspace.NewSession("owner", t.TempDir(), db, nil)
	app.ActiveSession, app.composerHydratedFor, app.composerDraftEpoch = sess, sess, 1
	app.draftOwner(sess).epoch = 1
	app.Input.SetInput("one two three four five")
	app.Input.SetCursorFlat(7)
	app.Input.SetSelection(4, 7)
	app.Input.SetScrollOffset(1)
	app.flushComposerDraft()
	raw, err := sessiondb.LoadRuntimeState(context.Background(), db, composerDraftStateKey)
	if err != nil {
		t.Fatal(err)
	}
	var got composerDraftSnapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != app.Input.Value() || got.Cursor != 7 || got.SelectionFrom != 4 || got.SelectionTo != 7 {
		t.Fatalf("incomplete full-editor snapshot: %+v", got)
	}
}
