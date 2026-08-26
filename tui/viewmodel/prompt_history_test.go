package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	"crude/history"
	"crude/tui/input"
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
	if got := app.History; len(got) != 1 || got[0] != "check the async deck now" {
		t.Fatalf("recall after submit = %v", got)
	}
	if app.Input.History()[0] != "check the async deck now" {
		t.Fatalf("input recall = %v", app.Input.History())
	}

	// Run the persistence command like bubbletea would.
	if msg := cmds[0](); msg != nil {
		t.Fatalf("persistence cmd should return nil msg, got %v", msg)
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
	if len(app.History) != 1 {
		t.Fatalf("repeats should fold to 1 entry, got %d: %v", len(app.History), app.History)
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
	if len(app.History) != 0 {
		t.Fatalf("blank prompt recorded: %v", app.History)
	}
}
