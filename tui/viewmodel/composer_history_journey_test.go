package viewmodel

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/history"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/tui/input"
	"crude/types"
	"crude/workspace"
)

func TestComposerSessionSwitchOwnsDraftAndReferenceLifecycle(t *testing.T) {
	app := newFullTestApp(t)
	first := workspace.NewSession("first", t.TempDir(), nil, nil)
	second := workspace.NewSession("second", t.TempDir(), nil, nil)
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"first": first, "second": second}
	app.AddOrchestrator("first")
	if app.composerHydratedFor != first {
		t.Fatal("real session activation did not bind the composer lifecycle")
	}
	ref, err := first.RefStore.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "first exact attachment"})
	if err != nil {
		t.Fatal(err)
	}
	app.Input.InsertReference(ref)
	app.Input, _, _ = app.Input.Update(tea.PasteMsg{Content: " first draft"}, false, false)
	text := app.Input.Value()
	app.Input.SetCursorFlat(3)
	app.Input.SetSelection(1, 3)
	app.AddOrchestrator("second")
	if app.Input.Value() != "" || len(app.Input.PromptReferences()) != 0 {
		t.Fatal("new session inherited another session's draft or attachments")
	}
	app.Input.SetInput("second draft")
	app.switchToAgentView("first")
	start, end := app.Input.InputSelectionRange()
	if app.Input.Value() != text || app.Input.CursorFlat() != 3 || start != 1 || end != 3 || !slices.Equal(app.Input.ReferencedIDs(), []string{ref.ID}) {
		t.Fatalf("switch lost first draft state: text=%q cursor=%d selection=%d:%d refs=%v", app.Input.Value(), app.Input.CursorFlat(), start, end, app.Input.ReferencedIDs())
	}
	// Repaint and background tab synchronization must not load an older draft.
	app.syncAgentWindowTabs()
	app.paintShellFrame()
	if app.Input.Value() != text {
		t.Fatal("background sync replaced retained draft")
	}
	app.Input.ClearInputSelection()
	app.Input, _, _ = app.Input.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}, false, false)
	if app.Input.Value() != ref.DisplayName() {
		t.Fatalf("session activation destroyed undo history: %q", app.Input.Value())
	}
	app.switchToAgentView("second")
	if app.Input.Value() != "second draft" {
		t.Fatal("returning lost the second draft")
	}
}

func TestComposerColdLoadAndInactiveFlushRespectLatestDraft(t *testing.T) {
	app := newFullTestApp(t)
	configureComposerDraftTest(t, app)
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "draft.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.composerDraftWrites.Wait(); _ = db.Close() })
	if err := sessiondb.Create(db, sessiondb.SessionMeta{ID: "first", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	first := workspace.NewSession("first", t.TempDir(), db, nil)
	second := workspace.NewSession("second", t.TempDir(), nil, nil)
	saved, err := json.Marshal(composerDraftSnapshot{Text: "saved draft", Cursor: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.draftOwner(first).record.save(app.composerDraftClient, saved); err != nil {
		t.Fatal(err)
	}
	app.ActiveSession = first
	load := app.hydrateComposerDraftCmd()
	old := load().(composerDraftLoadedMsg)
	app.ActiveSession = second
	app.hydrateComposerDraftCmd()
	app.flushComposerDraft()
	raw, err := app.draftOwner(first).record.load(app.composerDraftClient)
	if err != nil || !bytes.Equal(raw, saved) {
		t.Fatal("unresolved cold load overwrote the saved draft with an empty projection")
	}
	app.ActiveSession = first
	load = app.hydrateComposerDraftCmd()
	app.applyComposerDraft(old)
	if app.Input.Value() != "" {
		t.Fatal("previous activation's load overwrote replacement owner")
	}
	app.applyComposerDraft(load().(composerDraftLoadedMsg))
	if app.Input.Value() != "saved draft" {
		t.Fatal("cold load was not retried after returning to the session")
	}
	// Leave a newer save queued behind a completed but not UI-acknowledged write.
	acknowledgements := make(chan tea.Msg, 8)
	restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { acknowledgements <- msg })
	defer restore()
	app.Input.SetInput("first edit")
	app.persistComposerDraftIfChanged()
	app.composerDraftWrites.Wait()
	app.Input.SetInput("latest exact edit")
	app.persistComposerDraftIfChanged()
	app.ActiveSession = second
	app.hydrateComposerDraftCmd()
	app.flushComposerDraft()
	raw, err = app.draftOwner(first).record.load(app.composerDraftClient)
	var snapshot composerDraftSnapshot
	if err != nil || json.Unmarshal(raw, &snapshot) != nil || snapshot.Text != "latest exact edit" {
		t.Fatalf("teardown lost inactive queued edit: %s err=%v", raw, err)
	}
	select {
	case msg := <-acknowledgements:
		app.onComposerDraftSaved(msg.(composerDraftSavedMsg))
	default:
	}
	if len(app.draftOwner(first).inFlight) != 0 || len(app.draftOwner(first).queued) != 0 {
		t.Fatal("late acknowledgement restarted a flushed save")
	}
}

func TestComposerFailedSaveKeepsNewerQueuedSnapshot(t *testing.T) {
	app := newFullTestApp(t)
	sess := workspace.NewSession("owner", t.TempDir(), nil, nil)
	owner := app.draftOwner(sess)
	owner.epoch, owner.inFlight, owner.queued = 1, []byte("old"), []byte("new")
	app.onComposerDraftSaved(composerDraftSavedMsg{session: sess, epoch: 1, raw: []byte("old"), err: errors.New("disk full")})
	if string(owner.queued) != "new" {
		t.Fatal("failed older save overwrote newer queued draft")
	}
}

func TestComposerSessionSwitchDoesNotConsumeAnotherPendingPaste(t *testing.T) {
	for _, oldFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(oldFirst), func(t *testing.T) {
			app := newFullTestApp(t)
			first := workspace.NewSession("first", t.TempDir(), nil, nil)
			second := workspace.NewSession("second", t.TempDir(), nil, nil)
			wsOf(app).SessionsByAgent = map[string]*workspace.Session{"first": first, "second": second}
			app.AddOrchestrator("first")
			app.Input.SetInput("old draft ")
			_, old := app.Update(tea.PasteMsg{Content: strings.Repeat("old paste\n", 600)})
			app.AddOrchestrator("second")
			app.Input.SetInput("keep this ")
			_, next := app.Update(tea.PasteMsg{Content: strings.Repeat("new paste\n", 600)})
			if old == nil || next == nil {
				t.Fatal("fixture did not produce independent pending pastes")
			}
			if oldFirst {
				_, _ = app.Update(old())
				_, _, event := app.Input.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
				if _, ok := event.(input.PastePendingEvent); !ok {
					t.Fatal("stale result consumed the new session's pending paste/send barrier")
				}
				_, _ = app.Update(next())
			} else {
				_, _ = app.Update(next())
				_, _ = app.Update(old())
			}
			refs := app.Input.PromptReferences()
			if len(refs) != 1 || refs[0].SessionID != "second" || !strings.HasPrefix(app.Input.Value(), "keep this ") {
				t.Fatalf("replacement paste lost its owner: text=%q refs=%+v", app.Input.Value(), refs)
			}
		})
	}
}

func TestPromptHistoryRecallFencesPointerEditsAndColdHydration(t *testing.T) {
	for _, kind := range []string{"cursor", "selection", "cold load"} {
		t.Run(kind, func(t *testing.T) {
			app := newFullTestApp(t)
			app.ActiveSession = workspace.NewSession("owner", t.TempDir(), nil, nil)
			app.hydrateComposerDraftCmd()
			app.Input.SetInput("original draft")
			prior := composerDraftLoadedMsg{session: app.ActiveSession, epoch: app.composerDraftEpoch, requestRevision: app.Input.DraftRevision(), text: "old saved draft"}
			app.draftOwner(app.ActiveSession).loadRevision = prior.requestRevision
			entry := history.Entry{Text: "[notes]", Attachments: []history.Attachment{{Start: 0, End: 7, Reference: sessiondb.Reference{ID: "notes", Kind: sessiondb.KindPastedText, Label: "[notes]", Content: "exact notes"}}}}
			cmd := app.recallPrompt(app.Input.RecallPrompt(entry))
			result := cmd().(promptHistoryRestoredMsg)
			switch kind {
			case "cursor":
				app.Input.SetCursorFlat(2)
			case "selection":
				app.Input.SetSelection(1, 4)
			case "cold load":
				app.applyComposerDraft(prior)
			}
			app.applyPromptHistory(result)
			if kind == "cold load" {
				if len(app.Input.PromptReferences()) != 1 {
					t.Fatal("old disk hydration cancelled the explicit history recall")
				}
			} else if app.Input.Value() != "original draft" {
				t.Fatal("late recall overwrote pointer-modified draft")
			}
		})
	}
}

func TestHistoryRecallToActualProviderRetainsMixedContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(t.TempDir(), "source.png")
	if err := os.WriteFile(imagePath, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	entry := history.Entry{Text: "inspect [image] and [notes] with literal [Pasted IMG1]"}
	entry.Attachments = []history.Attachment{
		{Start: 8, End: 15, Reference: sessiondb.Reference{ID: "image", Kind: sessiondb.KindPastedImage, Label: "[image]", Mime: "image/png", FilePath: imagePath}},
		{Start: 20, End: 27, Reference: sessiondb.Reference{ID: "notes", Kind: sessiondb.KindPastedText, Label: "[notes]", Content: "EXACT-NOTES-CONTENT"}},
	}
	historyPath := filepath.Join(home, "history.json")
	store := history.New()
	store.Record(entry.Text, time.Now(), entry.Attachments...)
	if err := store.Save(historyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(imagePath); err != nil {
		t.Fatal(err)
	}
	store, err := history.LoadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan provider.ChatRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		requests <- req
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Received exact media.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	wsOf(app).RootPath, wsOf(app).WorkspaceDir = t.TempDir(), t.TempDir()
	wsOf(app).SessionsByID, wsOf(app).SessionsByAgent = make(map[string]*workspace.Session), make(map[string]*workspace.Session)
	wsOf(app).SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	cfg := testAccountConfig()
	account := cfg.Accounts["test"]
	account.Connection.ProviderURL = server.URL
	cfg.Accounts["test"] = account
	cfg.Agents.Enabled = false
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	category := profile.Categories[session.CategoryDefault]
	category.ModelModalities = []string{provider.ModalityImage}
	profile.Categories[session.CategoryDefault] = category
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	owner, err := wsOf(app).CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	defer wsOf(app).CloseAllSessions()
	sender := &recoveryJourneySender{events: make(chan types.AgentMsg, 512)}
	orch, err := wsOf(app).StartOrchestrator(owner.SessionID, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer app.ws.CloseAgent(orch.ID)
	app.AddOrchestrator(orch.ID)
	app.SetPromptHistory(store)
	_, recall := app.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	runAppCommand(t, app, recall)
	app.paintShellFrame()
	if len(app.Input.PromptReferences()) != 2 {
		t.Fatal("history was not recalled through actual Up entry point")
	}
	_, edit := app.Update(tea.PasteMsg{Content: " edited"})
	runAppCommand(t, app, edit)
	_, send := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, send)
	var request provider.ChatRequest
	select {
	case request = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("recalled prompt did not reach the actual provider")
	}
	var text strings.Builder
	images := 0
	for _, message := range request.Messages {
		if message.Role != "user" {
			continue
		}
		text.WriteString(message.ContentString())
		for _, part := range message.Content.Parts() {
			if part.Type == "image_url" && part.ImageURL != nil {
				images++
				want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
				if part.ImageURL.URL != want {
					t.Fatal("provider received altered media bytes")
				}
			}
		}
	}
	if images != 1 || !strings.Contains(text.String(), "EXACT-NOTES-CONTENT") || !strings.Contains(text.String(), "literal [Pasted IMG1] edited") {
		t.Fatalf("mixed-content submission degraded: images=%d exactNotes=%t", images, strings.Contains(text.String(), "EXACT-NOTES-CONTENT"))
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-sender.events:
			_, _ = app.Update(event)
			if event.Type == "done" {
				entries := app.Prompts.Entries()
				if len(entries[len(entries)-1].Attachments) != 2 {
					t.Fatal("accepted resubmission lost attachment history")
				}
				return
			}
		case <-deadline:
			t.Fatal("recalled request did not finish")
		}
	}
}
