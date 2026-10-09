package viewmodel

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/clientstate"
	"crude/tui/ui"
)

func conflictPrompt(t *testing.T, m *App) *liveConflictPrompt {
	t.Helper()
	fw := m.overlayHost.Window(liveConflictOverlay)
	if fw == nil {
		t.Fatal("no native conflict dialog")
	}
	return fw.Content.(*ui.WindowFrameWidget).Body().(*liveConflictPrompt)
}
func TestLiveEditorConflictDefaultCompareAndFencedUseShared(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "draft.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "private.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	a, err := store.Acquire(context.Background(), "conflict-profile", clientstate.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = two.app.ConfigureComposerDrafts(store, a, "workspace"); err != nil {
		t.Fatal(err)
	}
	x, y := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
	x.InsertRune('X')
	y.InsertRune('Y')
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	liveTestSync(t, two.app)
	runAppCommand(t, two.app, two.app.drainLiveEditorCommands())
	p := conflictPrompt(t, two.app)
	if !strings.Contains(two.app.View().Content, "Keep draft") {
		t.Fatal("safe default missing")
	}
	two.app.applyLiveConflict(liveConflictMsg{p: p, action: "keep"})
	if y.Value() != "Yabc" || x.Value() != "Xabc" {
		t.Fatal("keep mutated text")
	}
	runAppCommand(t, two.app, two.app.OpenLiveEditorConflict(y))
	p = conflictPrompt(t, two.app)
	runAppCommand(t, two.app, two.app.applyLiveConflict(liveConflictMsg{p: p, action: "compare"}))
	if two.app.activeLocalDiff() == nil || y.Value() != "Yabc" {
		t.Fatal("compare consumed draft")
	}
	runAppCommand(t, two.app, two.app.OpenLiveEditorConflict(y))
	p = conflictPrompt(t, two.app)
	two.app.applyLiveConflict(liveConflictMsg{p: p, action: "use"})
	x.SetCursorPosition(0, 4)
	x.InsertRune('Z')
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	runAppCommand(t, two.app, two.app.applyLiveConflict(liveConflictMsg{p: p, action: "use"}))
	if y.Value() != "Yabc" || !strings.Contains(p.detail.Value(), "shared text") {
		t.Fatal("changed shared preimage not refused")
	}
	runAppCommand(t, two.app, two.app.OpenLiveEditorConflict(y))
	p = conflictPrompt(t, two.app)
	two.app.applyLiveConflict(liveConflictMsg{p: p, action: "use"})
	runAppCommand(t, two.app, two.app.applyLiveConflict(liveConflictMsg{p: p, action: "use"}))
	if y.Value() != "XabcZ" || y.DocumentState().PendingEdits != 0 || y.DocumentState().Conflict != nil || two.app.liveEditors[y].err != nil {
		t.Fatalf("explicit resolution failed: %q %+v", y.Value(), y.DocumentState())
	}
	files, err := store.LoadEditorRecovery(context.Background(), a, clientstate.EditorRecoveryKey{ClientID: a.ClientID, WorkspaceID: "workspace", Path: path})
	if err != nil && !errors.Is(err, clientstate.ErrNotFound) {
		t.Fatal(err)
	}
	if len(files.Payload) > 0 {
		t.Fatal("unexpected source recovery")
	}
}

func TestLiveEditorRecoveredResolutionClearsExactRecordAndRetainsBackup(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "recover.txt")
	if err := os.WriteFile(path, []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "private.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	a, err := store.Acquire(context.Background(), "recover-profile", clientstate.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = shell.app.ConfigureComposerDrafts(store, a, "workspace"); err != nil {
		t.Fatal(err)
	}
	ed := liveTestOpen(t, shell.app, path)
	ed.InsertRune('X')
	recovery, _ := ed.ExportDocumentRecovery()
	raw, err := json.Marshal(recovery)
	if err != nil {
		t.Fatal(err)
	}
	key := clientstate.EditorRecoveryKey{ClientID: a.ClientID, WorkspaceID: "workspace", Path: path}
	record, err := store.SaveEditorRecovery(context.Background(), a, key, 0, raw)
	if err != nil {
		t.Fatal(err)
	}
	// Reopen the real file path through a new shell with the same private identity.
	restored := newRemoteJourneyShell(t, runtime, root)
	if err = restored.app.ConfigureComposerDrafts(store, a, "workspace"); err != nil {
		t.Fatal(err)
	}
	r := liveTestOpen(t, restored.app, path)
	if r.Value() != "Xbase" {
		t.Fatal("private recovery missing")
	}
	p := conflictPrompt(t, restored.app)
	// Enter must activate the safe default, not discard.
	event := ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
	out := p.actions.Handle(ui.EventContext{Focused: true}, event)
	if out.Action == nil || out.Action.ID != "keep" {
		t.Fatalf("unsafe default: %+v", out)
	}
	restored.app.applyLiveConflict(liveConflictMsg{p: p, action: "use"})
	cmd := restored.app.applyLiveConflict(liveConflictMsg{p: p, action: "use"})
	msg := cmd().(liveConflictMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	backup, err := store.LoadEditorRecovery(context.Background(), a, msg.backupKey)
	if err != nil || !bytes.Equal(backup.Payload, raw) {
		t.Fatalf("backup missing exact draft: %v", err)
	}
	runAppCommand(t, restored.app, restored.app.applyLiveConflict(msg))
	cleared, err := store.LoadEditorRecovery(context.Background(), a, key)
	if err != nil || len(cleared.Payload) != 0 || cleared.Revision != record.Revision+1 {
		t.Fatalf("record not tombstoned: %+v %v", cleared, err)
	}
	if r.Value() != "base" || r.DocumentState().Conflict != nil {
		t.Fatalf("recovered resolution failed: %q", r.Value())
	}
}
