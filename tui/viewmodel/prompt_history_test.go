package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/history"
	"crude/sessiondb"
	"crude/tui/input"
	"crude/workspace"
)

// TestRecordPromptPersistsGlobalHistory verifies the full submit → record →
// save chain: submitting a prompt folds it into the global store, refreshes
// composer recall, and the returned command writes the file to disk.
func TestRecordPromptPersistsGlobalHistory(t *testing.T) {
	app := newTestApp(t)
	store := history.New()
	app.SetPromptHistory(store)
	path := filepath.Join(t.TempDir(), "history.json")
	app.promptHistoryPath = path

	cmds := app.handleInputEvent(input.SubmitEvent{Text: "check the async deck now"})
	if len(cmds) == 0 || cmds[0] == nil {
		t.Fatal("expected a persistence cmd from submit")
	}
	if got := app.Prompts.Recall(); len(got) != 1 || got[0] != "check the async deck now" {
		t.Fatalf("recall after submit = %v", got)
	}
	if app.Input.History()[0].Text != "check the async deck now" {
		t.Fatalf("input recall = %v", app.Input.History())
	}

	// Run the persistence command like bubbletea would.
	if msg := cmds[0]().(promptHistorySavedMsg); msg.err != nil {
		t.Fatalf("persistence failed: %v", msg.err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("history file not written: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("history file is empty")
	}
}

// TestRecordPromptFoldsRepeats verifies that resubmitting the same prompt
// does not grow the recall list — it folds into one entry.
func TestRecordPromptFoldsRepeats(t *testing.T) {
	app := newTestApp(t)
	app.SetPromptHistory(history.New())
	app.promptHistoryPath = filepath.Join(t.TempDir(), "history.json")

	for range 3 {
		if cmd := app.recordPrompt("hello"); cmd != nil {
			cmd()
		}
	}
	if app.Prompts.Len() != 1 {
		t.Fatalf("repeats should fold to 1 entry, got %d", app.Prompts.Len())
	}
	if app.Prompts.Entries()[0].Count != 3 {
		t.Fatalf("count = %d, want 3", app.Prompts.Entries()[0].Count)
	}
}

// TestRecordPromptSkipsBlank verifies blank submits don't touch history.
func TestRecordPromptSkipsBlank(t *testing.T) {
	app := newTestApp(t)
	app.SetPromptHistory(history.New())
	app.promptHistoryPath = filepath.Join(t.TempDir(), "history.json")

	if cmd := app.recordPrompt("   \n\t "); cmd != nil {
		t.Fatal("blank prompt must not schedule persistence")
	}
	if app.Prompts.Len() != 0 {
		t.Fatalf("blank prompt recorded: %v", app.Prompts.Recall())
	}
}

func TestPromptHistoryRecallsAttachmentsAcrossSessionsAndRestoresDraft(t *testing.T) {
	app := newFullTestApp(t)
	app.SetPromptHistory(history.New())
	app.promptHistoryPath = filepath.Join(t.TempDir(), "history.json")
	app.ActiveSession = workspace.NewSession("first", t.TempDir(), nil, stubLogger{})
	app.Input.SetReferenceStore(app.RefStore())
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, []byte("retained image"), 0600); err != nil {
		t.Fatal(err)
	}
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, FilePath: path, Mime: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	app.Input.SetInput("inspect " + ref.DisplayName())
	updated, _, event := app.Input.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	app.Input = updated
	commands := app.handleInputEvent(event)
	_, _ = app.Update(commands[0]())
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	app.ActiveSession = workspace.NewSession("second", t.TempDir(), nil, stubLogger{})
	app.Input.SetReferenceStore(app.RefStore())
	app.Input.SetInput("inspect")
	updated, _, event = app.Input.Update(tea.KeyPressMsg{Code: tea.KeyUp}, false, false)
	app.Input = updated
	commands = app.handleInputEvent(event)
	if len(commands) != 1 || commands[0] == nil {
		t.Fatal("multimodal recall must run outside Update")
	}
	result := commands[0]()
	_, _ = app.Update(result)
	refs := app.Input.PromptReferences()
	if len(refs) != 1 || refs[0].ID == ref.ID || refs[0].SessionID != "second" {
		t.Fatalf("recall did not restore a destination-owned attachment: %+v", refs)
	}
	data, err := os.ReadFile(refs[0].FilePath)
	if err != nil || string(data) != "retained image" {
		t.Fatalf("recall lost image bytes: %q %v", data, err)
	}
	app.Input, _, _ = app.Input.Update(tea.KeyPressMsg{Code: tea.KeyDown}, false, false)
	if app.Input.Value() != "inspect" || len(app.Input.PromptReferences()) != 0 {
		t.Fatalf("Down did not restore the draft: %q", app.Input.Value())
	}
	// A late result must not replace a newer edit, even in the same session.
	cmd := app.recallPrompt(app.Input.RecallPrompt(app.Prompts.Entries()[0]))
	result = cmd()
	app.Input.SetInput("newer draft")
	_, _ = app.Update(result)
	if app.Input.Value() != "newer draft" {
		t.Fatal("stale history result overwrote an edit")
	}
}

func TestTextOnlyHistoryCannotCaptureAnotherSessionsAttachment(t *testing.T) {
	app, ref := newAttachmentTestApp(t)
	text := "Discuss the literal label " + ref.DisplayName()
	app.recallPrompt(app.Input.RecallPrompt(history.Entry{Text: text}))
	if len(app.Input.PromptReferences()) != 0 {
		t.Fatal("text-only recall captured a same-named attachment")
	}
	app.Input.SetReferenceStore(app.RefStore())
	app.Input, _, _ = app.Input.Update(tea.KeyPressMsg{Code: 'x', Text: "x"}, false, false)
	if len(app.Input.RefRanges()) != 0 {
		t.Fatal("editing or reconfiguring a recalled draft rebound a literal label")
	}
	// Explicitly attaching a reference still works in a pinned draft.
	app.Input.InsertReference(ref)
	if len(app.Input.PromptReferences()) != 1 {
		t.Fatal("pinned history draft rejected an explicit attachment")
	}
}
