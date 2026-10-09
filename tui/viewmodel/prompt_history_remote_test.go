package viewmodel

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"crude/history"
	"crude/sessiondb"
	"crude/shellport"
	"crude/workspace"
)

type promptHistoryRemoteFixture struct {
	shellport.Workspace
	entries          []history.Entry
	sessionID, text  string
	attachments      []history.Attachment
	listed, recorded int
}

func (f *promptHistoryRemoteFixture) ListPromptHistory(context.Context, string) ([]history.Entry, error) {
	f.listed++
	return f.entries, nil
}
func (f *promptHistoryRemoteFixture) RecordPromptHistory(_ context.Context, sessionID, text string, attachments []history.Attachment) ([]history.Entry, error) {
	f.recorded++
	f.sessionID, f.text, f.attachments = sessionID, text, attachments
	return f.entries, nil
}

func TestRemotePromptHistoryRecordUsesCoreSnapshotWithoutLocalWriter(t *testing.T) {
	app := newTestApp(t)
	f := &promptHistoryRemoteFixture{Workspace: app.ws, entries: []history.Entry{{ID: "core-id", Text: "send [Pasted IMG1]", Count: 9, FirstSeen: 11, LastSeen: 22}}}
	app.ws = f
	app.ActiveSession = &workspace.Session{SessionID: "exact-session"}
	app.SetPromptHistory(history.New())
	app.promptHistoryPath = filepath.Join(t.TempDir(), "must-not-exist.json")
	ref := sessiondb.Reference{ID: "exact-image", Label: "[Pasted IMG1]", SessionID: "exact-session", FilePath: "/ssh-server-only/image.png"}
	cmd, handled := app.recordRemotePrompt(" send [Pasted IMG1] ", []sessiondb.Reference{ref})
	if !handled || cmd == nil || f.recorded != 0 || app.Prompts.Len() != 0 {
		t.Fatal("record ran on Update or mutated local history")
	}
	msg := cmd().(remotePromptHistoryMsg)
	if f.recorded != 1 || f.sessionID != "exact-session" || len(f.attachments) != 1 || f.attachments[0].Reference != ref || f.attachments[0].Start != 5 || f.attachments[0].End != 18 {
		t.Fatalf("wrong immutable record: %#v", f)
	}
	app.applyRemotePromptHistory(msg)
	if !reflect.DeepEqual(app.Prompts.Entries(), f.entries) || !reflect.DeepEqual(app.Input.History(), f.entries) {
		t.Fatal("core identity/count/times did not reach recall")
	}
	if _, err := os.Stat(app.promptHistoryPath); !os.IsNotExist(err) {
		t.Fatalf("shell wrote history: %v", err)
	}
}

func TestRemotePromptHistoryLoadRejectsLateOwners(t *testing.T) {
	app := newTestApp(t)
	f := &promptHistoryRemoteFixture{Workspace: app.ws, entries: []history.Entry{{ID: "core-id", Text: "remote", Count: 4}}}
	app.ws = f
	app.SetPromptHistory(history.New())
	cmd := app.loadRemotePromptHistory()
	if cmd == nil || f.listed != 0 {
		t.Fatal("load was not deferred")
	}
	msg := cmd().(remotePromptHistoryMsg)
	app.ws = &promptHistoryRemoteFixture{Workspace: f.Workspace}
	app.applyRemotePromptHistory(msg)
	if app.Prompts.Len() != 0 {
		t.Fatal("late workspace result installed")
	}
	app.ws = f
	app.workspaceSwitchGen.Add(1)
	app.applyRemotePromptHistory(msg)
	if app.Prompts.Len() != 0 {
		t.Fatal("late generation result installed")
	}
	app.SetPromptHistory(history.New())
	cmd = app.loadRemotePromptHistory()
	msg = cmd().(remotePromptHistoryMsg)
	app.SetPromptHistory(history.New())
	app.applyRemotePromptHistory(msg)
	if app.Prompts.Len() != 0 {
		t.Fatal("late store result installed")
	}
}

func TestRemotePromptHistoryMissingSessionNeverFallsBack(t *testing.T) {
	app := newTestApp(t)
	f := &promptHistoryRemoteFixture{Workspace: app.ws}
	app.ws = f
	app.SetPromptHistory(history.New())
	cmd, handled := app.recordRemotePrompt("text-only prompt", nil)
	if !handled || cmd == nil || cmd().(remotePromptHistoryMsg).err == nil || f.recorded != 0 {
		t.Fatal("missing exact session did not fail closed")
	}
}
