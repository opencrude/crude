package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	app := newFullTestApp(t)
	store := history.New()
	app.SetPromptHistory(store)
	path := filepath.Join(t.TempDir(), "history.json")
	app.promptHistoryPath = path
	agentID := addBackendTestOrchestrator(t, app)
	app.ActiveSession = app.ws.SessionsByAgent()[agentID]
	app.Input.SetInput("check the async deck now")

	_, command := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("expected a persistence cmd from submit")
	}
	runAppCommand(t, app, command)
	if got := app.Prompts.Recall(); len(got) != 1 || got[0] != "check the async deck now" {
		t.Fatalf("recall after submit = %v", got)
	}
	if app.Input.History()[0].Text != "check the async deck now" {
		t.Fatalf("input recall = %v", app.Input.History())
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
	agentID := addBackendTestOrchestrator(t, app)
	app.ActiveSession = app.ws.SessionsByAgent()[agentID]
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
	_, command := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("attachment submit did not schedule admission")
	}
	runAppCommand(t, app, command)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	app.ActiveSession = workspace.NewSession("second", t.TempDir(), nil, stubLogger{})
	app.Input.SetReferenceStore(app.RefStore())
	app.Input.SetInput("inspect")
	updated, _, event := app.Input.Update(tea.KeyPressMsg{Code: tea.KeyUp}, false, false)
	app.Input = updated
	commands := app.handleInputEvent(event)
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

func TestPromptHistoryRecallBindsDestinationStoreBeforeSubmitting(t *testing.T) {
	app := newFullTestApp(t)
	app.ActiveSession = workspace.NewSession("destination", t.TempDir(), nil, stubLogger{})
	// A fresh session or launcher-to-session transition can still have the
	// shell input bound to the previous projection when recall begins.
	app.Input.SetReferenceStore(workspace.NewReferenceStore("previous", t.TempDir(), nil))
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, []byte("image bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	entry := history.Entry{Text: "inspect [Pasted IMG2]", Attachments: []history.Attachment{{
		Start: 8, End: 21, Reference: sessiondb.Reference{ID: "source-image", Kind: sessiondb.KindPastedImage,
			Label: "[Pasted IMG2]", FilePath: path, Mime: "image/png"},
	}}}
	cmd := app.recallPrompt(app.Input.RecallPrompt(entry))
	if cmd == nil {
		t.Fatal("missing import command")
	}
	app.applyPromptHistory(cmd().(promptHistoryRestoredMsg))
	refs := app.Input.PromptReferences()
	if len(refs) != 1 || refs[0].SessionID != "destination" {
		t.Fatalf("imported image was not bound to composer: %+v", refs)
	}
	updated, _, event := app.Input.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	app.Input = updated
	submitted, ok := event.(input.SubmitEvent)
	if !ok || len(submitted.ReferenceIDs) != 1 || submitted.ReferenceIDs[0] != refs[0].ID {
		t.Fatalf("recall submitted literal text instead of selected image: %+v", event)
	}
}

func TestPromptHistoryRecallMixedReferencesThroughEditAndResubmit(t *testing.T) {
	for _, steering := range []bool{false, true} {
		t.Run(map[bool]string{false: "send", true: "steer"}[steering], func(t *testing.T) {
			app := newFullTestApp(t)
			app.ActiveSession = workspace.NewSession("destination", t.TempDir(), nil, stubLogger{})
			app.Input.SetReferenceStore(nil)
			kinds := []sessiondb.Kind{sessiondb.KindPastedImage, sessiondb.KindPastedText,
				sessiondb.KindPastedTextFile, sessiondb.KindSelectedFile, sessiondb.KindMCPTool, sessiondb.KindMCPContext}
			entry := history.Entry{Text: "inspect "}
			for i, kind := range kinds {
				ref := sessiondb.Reference{ID: string(kind), Kind: kind, Label: "[source " + string(kind) + "]",
					Title: "exact source", Mime: "text/plain", Content: "captured content"}
				if kind == sessiondb.KindPastedImage || kind == sessiondb.KindPastedTextFile || kind == sessiondb.KindSelectedFile {
					path := filepath.Join(t.TempDir(), "media.bin")
					if err := os.WriteFile(path, []byte("immutable media"), 0600); err != nil {
						t.Fatal(err)
					}
					ref.FilePath, ref.Content = path, ""
					if i == 0 {
						ref.Mime = "image/png"
					}
				}
				start := len([]rune(entry.Text))
				entry.Text += ref.Label
				entry.Attachments = append(entry.Attachments, history.Attachment{Start: start, End: len([]rune(entry.Text)), Reference: ref})
				entry.Text += " "
			}
			// A generated-looking literal must not acquire an imported reference.
			entry.Text += "literal [Pasted IMG1] and array[0]"
			store := history.New()
			store.Record(entry.Text, time.Now(), entry.Attachments...)
			path := filepath.Join(t.TempDir(), "history.json")
			if err := store.Save(path); err != nil {
				t.Fatal(err)
			}
			loaded, err := history.LoadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			app.SetPromptHistory(loaded)
			app.promptHistoryPath = path
			cmd := app.recallPrompt(app.Input.RecallPrompt(loaded.Entries()[0]))
			app.applyPromptHistory(cmd().(promptHistoryRestoredMsg))
			if len(app.Input.ReferenceChips(160)) == 0 || len(app.Input.PromptReferences()) != len(kinds) {
				t.Fatalf("recalled mixed attachments did not render as chips: %+v", app.Input.PromptReferences())
			}
			if !strings.Contains(app.Input.Value(), "literal [Pasted IMG1] and array[0]") {
				t.Fatalf("literal prose changed: %q", app.Input.Value())
			}
			app.Input.SetCursorFlat(len([]rune(app.Input.Value())))
			app.Input, _, _ = app.Input.Update(tea.PasteMsg{Content: " edited"}, false, false)
			updated, _, event := app.Input.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, steering, false)
			app.Input = updated
			var text string
			var ids []string
			switch submitted := event.(type) {
			case input.SubmitEvent:
				text, ids = submitted.Text, submitted.ReferenceIDs
			case input.SteeringSubmitEvent:
				text, ids = submitted.Text, submitted.ReferenceIDs
			default:
				t.Fatalf("no submit event: %+v", event)
			}
			if len(ids) != len(kinds) || !strings.HasSuffix(text, " edited") {
				t.Fatalf("editing/resubmitting lost attachments: %+v", event)
			}
			for _, id := range ids {
				ref, ok := app.RefStore().GetReference(id)
				if !ok || ref.SessionID != "destination" {
					t.Fatalf("submission ID not owned by destination: %s", id)
				}
			}
			// Capture from precisely the selected IDs, never every session label.
			cmd = app.recordPrompt(text, ids...)
			if cmd == nil {
				t.Fatal("missing save command")
			}
			if saved := cmd().(promptHistorySavedMsg); saved.err != nil {
				t.Fatal(saved.err)
			}
			entries := loaded.Entries()
			if len(entries[len(entries)-1].Attachments) != len(kinds) {
				t.Fatal("resubmission lost mixed attachment snapshots")
			}
		})
	}
}

func TestPromptHistoryRecallDoesNotRebindStaleOrFailedOwner(t *testing.T) {
	for _, reason := range []string{"session switch", "edited draft", "missing media"} {
		t.Run(reason, func(t *testing.T) {
			app := newFullTestApp(t)
			owner := workspace.NewSession("owner", t.TempDir(), nil, stubLogger{})
			app.ActiveSession = owner
			previous := workspace.NewReferenceStore("previous", t.TempDir(), nil)
			ref, err := previous.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "keep"})
			if err != nil {
				t.Fatal(err)
			}
			app.Input.SetReferenceStore(previous)
			app.Input.SetInput("keep " + ref.Label)
			path := filepath.Join(t.TempDir(), "image.png")
			if reason != "missing media" {
				if err := os.WriteFile(path, []byte("media"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			entry := history.Entry{Text: "[old]", Attachments: []history.Attachment{{Start: 0, End: 5,
				Reference: sessiondb.Reference{ID: "old", Label: "[old]", Kind: sessiondb.KindPastedImage, FilePath: path}}}}
			cmd := app.recallPrompt(app.Input.RecallPrompt(entry))
			result := cmd().(promptHistoryRestoredMsg)
			switch reason {
			case "session switch":
				app.ActiveSession = workspace.NewSession("other", t.TempDir(), nil, stubLogger{})
			case "edited draft":
				app.Input.SetValuePreserveReferences("newer " + ref.Label)
			}
			before := app.Input.Value()
			app.applyPromptHistory(result)
			refs := app.Input.PromptReferences()
			if app.Input.Value() != before || len(refs) != 1 || refs[0].ID != ref.ID {
				t.Fatalf("stale/failed import changed the existing draft or reference owner: %q %+v", app.Input.Value(), refs)
			}
		})
	}
}
