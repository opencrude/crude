package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/provider"
	"crude/sessiondb"
	"crude/tui/screens/textoverlay"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func TestLargeMultilinePasteBecomesAttachmentAcrossClipboardRoutes(t *testing.T) {
	for _, route := range []string{"terminal", "ctrl-v", "ctrl-shift-v"} {
		t.Run(route, func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			app.AddOrchestrator("paste-owner")
			app.ActiveSession = workspace.NewSession("paste-owner", t.TempDir(), nil, stubLogger{})
			app.maybeHydrateComposerDraft()
			app.Input.SetInput("Investigate this:\n")
			content := strings.Repeat("goroutine stack frame\n", 60) + "diagnostic-tail"
			var command tea.Cmd
			if route == "terminal" {
				_, command = app.Update(tea.PasteMsg{Content: content})
			} else {
				restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
					read: func(format clipboard.Format) []byte {
						if format == clipboard.FmtText {
							return []byte(content)
						}
						return nil
					},
					writeText: func(string) error { return nil },
				})
				defer restore()
				mod := tea.ModCtrl
				if route == "ctrl-shift-v" {
					mod |= tea.ModShift
				}
				_, read := app.Update(tea.KeyPressMsg{Code: 'v', Mod: mod})
				if read == nil {
					t.Fatal("Ctrl+V did not read the clipboard")
				}
				_, command = app.Update(read())
			}
			if strings.Contains(app.Input.Value(), "goroutine stack frame") {
				t.Fatal("multiline wall of text flooded the composer instead of becoming an attachment")
			}
			if command == nil {
				t.Fatal("paste promotion did not schedule off-UI storage")
			}
			if refs := app.RefStore().ListReferences(); len(refs) != 0 {
				t.Fatal("paste reference was persisted synchronously in Update")
			}
			_, _ = app.Update(command())
			refs := app.RefStore().ListReferences()
			if len(refs) != 1 || refs[0].Kind != sessiondb.KindPastedText || refs[0].Content != content {
				t.Fatalf("paste content was not preserved as one attachment: %+v", refs)
			}
			app.paintShellFrame()
			if got := app.Input.Value(); !strings.HasPrefix(got, "Investigate this:\n") || !strings.Contains(got, refs[0].Label) || len(got) > 100 {
				t.Fatalf("composer is not compact/preserved: %q", got)
			}
			if !strings.Contains(bufferPlain(app.mainSurf.Buffer()), refs[0].Label) {
				t.Fatal("pasted-text attachment was not painted")
			}
		})
	}
}

type pastedTextJourneySender struct{ events chan types.AgentMsg }

func (s *pastedTextJourneySender) SendAgentEvent(typ string, data any) {
	s.SendAgentEventWithID(typ, data, "")
}

func (s *pastedTextJourneySender) SendAgentEventWithID(typ string, data any, id string) {
	s.events <- types.AgentMsg{Type: typ, Data: data, AgentID: id}
}

func TestPastedTextSendsCompleteContentButKeepsChatCompact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	content := strings.Repeat("Complete pasted diagnostic.\n", 1000) + "UNIQUE-DIAGNOSTIC-TAIL"
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var text strings.Builder
		for _, message := range request.Messages {
			if message.Role == "user" {
				text.WriteString(message.ContentString())
			}
		}
		requests <- text.String()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Received the complete diagnostic.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	wsOf(app).RootPath, wsOf(app).WorkspaceDir = t.TempDir(), t.TempDir()
	wsOf(app).SessionsByID, wsOf(app).SessionsByAgent = make(map[string]*workspace.Session), make(map[string]*workspace.Session)
	wsOf(app).SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	cfg := testAccountConfig()
	account := cfg.Accounts["test"]
	template := account.Connection
	template.ProviderURL = server.URL
	account.Connection = template
	cfg.Accounts["test"] = account
	cfg.Agents.Enabled = false
	owner, err := wsOf(app).CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	sender := &pastedTextJourneySender{events: make(chan types.AgentMsg, 512)}
	orch, err := wsOf(app).StartOrchestrator(owner.SessionID, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer app.ws.CloseAgent(orch.ID)
	app.AddOrchestrator(orch.ID)
	app.maybeHydrateComposerDraft()
	_, command := app.Update(tea.PasteMsg{Content: content})
	if command == nil {
		t.Fatal("paste did not promote")
	}
	_, _ = app.Update(command())
	_, submit := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if submit == nil {
		t.Fatal("Enter did not schedule admission")
	}
	runAppCommand(t, app, submit)
	requestTimer := time.NewTimer(5 * time.Second)
	defer requestTimer.Stop()
	for waiting := true; waiting; {
		select {
		case text := <-requests:
			if !strings.Contains(text, content) {
				t.Fatal("provider request omitted or truncated the pasted attachment")
			}
			waiting = false
		case event := <-sender.events:
			_, _ = app.Update(event)
			if event.Type == "error" || event.Type == "done" {
				t.Fatalf("turn settled before provider request: type=%s data=%+v", event.Type, event.Data)
			}
		case <-requestTimer.C:
			node := app.ws.AgentMgr().Get(orch.ID)
			var messages []workspace.Message
			if node != nil && node.State != nil && node.State.Messages != nil {
				messages = node.State.Messages.Messages()
			}
			t.Fatalf("composer did not submit its pasted-text attachment: input=%q refs=%v messages=%+v", app.Input.Value(), app.Input.ReferencedIDs(), messages)
		}
	}
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-sender.events:
			_, _ = app.Update(event)
			if event.Type == "done" {
				app.paintShellFrame()
				text := bufferPlain(app.mainSurf.Buffer())
				if strings.Contains(text, "Complete pasted diagnostic.") || !strings.Contains(text, "[Pasted Text1]") {
					t.Fatal("sent transcript dumped the attachment body or lost its compact label")
				}
				return
			}
		case <-timeout.C:
			t.Fatal("attached-input turn did not complete")
		}
	}
}

func TestPastedTextReferencesStayOrderedAndRestoreTheSavedDraft(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("owner")
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "owner"); err != nil {
		t.Fatal(err)
	}
	app.ActiveSession = workspace.NewSession("owner", t.TempDir(), db, stubLogger{})
	app.composerHydratedFor = app.ActiveSession
	app.composerDraftEpoch = 1
	app.draftOwner(app.ActiveSession).epoch = 1
	app.Input.SetReferenceStore(app.RefStore())
	first := strings.Repeat("first payload with its complete diagnostic context\n", 25)
	second := strings.Repeat("second payload with its complete diagnostic context\n", 25)
	_, one := app.Update(tea.PasteMsg{Content: first})
	_, two := app.Update(tea.PasteMsg{Content: second})
	if one == nil || two == nil {
		t.Fatal("paste operations were not scheduled")
	}
	_, _ = app.Update(two())
	if app.Input.Value() != "" {
		t.Fatal("out-of-order storage reordered the paste gestures")
	}
	_, _ = app.Update(one())
	ids := app.Input.ReferencedIDs()
	if len(ids) != 2 {
		t.Fatal("ordered paste attachments missing")
	}
	ref1, _ := app.RefStore().GetReference(ids[0])
	ref2, _ := app.RefStore().GetReference(ids[1])
	if ref1.Content != first || ref2.Content != second {
		t.Fatal("paste order did not match the user's gestures")
	}
	raw, err := json.Marshal(composerDraftSnapshot{
		Text: app.Input.Value(), RefIDs: ids, Cursor: app.Input.CursorFlat(), Spans: composerDraftRefSpans(app.Input.RefRanges()),
	})
	if err != nil {
		t.Fatal(err)
	}
	configureComposerDraftTest(t, app)
	_ = app.saveComposerDraftMsg(app.ActiveSession, app.composerDraftEpoch, raw)
	app.Input.ClearInput()
	app.draftOwner(app.ActiveSession).loadRevision = app.Input.DraftRevision()
	requestRevision := app.Input.DraftRevision()
	_, _ = app.Update(app.loadComposerDraftMsg(app.ActiveSession, app.composerDraftEpoch, requestRevision))
	if len(app.Input.ReferencedIDs()) != 2 || app.Input.Value() != ref1.Label+ref2.Label {
		t.Fatal("draft resume lost pasted-text attachment identities")
	}
}

func TestPastedTextFileChipOpensCompleteEditableOverlayAndSaves(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.width, app.height = 80, 24
	app.AddOrchestrator("paste-owner")
	app.ActiveSession = workspace.NewSession("paste-owner", t.TempDir(), nil, stubLogger{})
	app.maybeHydrateComposerDraft()
	content := strings.Repeat("Complete diagnostic paragraph.\n", 2000) + "EXACT-TAIL"
	_, command := app.Update(tea.PasteMsg{Content: content})
	if command == nil {
		t.Fatal("huge paste was not promoted")
	}
	_, _ = app.Update(command())
	refs := app.RefStore().ListReferences()
	if len(refs) != 1 || refs[0].Kind != sessiondb.KindPastedTextFile {
		t.Fatalf("paste is not file-backed: %+v", refs)
	}
	draft := app.Input.Value()
	app.paintShellFrame()
	chips := app.Input.ReferenceChips(app.inputRect.W)
	if len(chips) != 1 {
		t.Fatal("paste has no attachment chip")
	}
	messages := make(chan tea.Msg, 2)
	restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { messages <- msg })
	defer restore()
	x, y := app.inputRect.X+chips[0].Body.X+1, app.inputRect.Y+chips[0].Body.Y
	_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	select {
	case msg := <-messages:
		_, _ = app.Update(msg)
	case <-time.After(3 * time.Second):
		t.Fatal("click did not load the pasted-text viewer")
	}
	screen := app.sessionTextOverlay()
	if screen == nil || screen.ReadOnly() || screen.Value() != content {
		t.Fatal("file-backed pasted text did not open completely in the native editor")
	}
	window := app.overlayHost.Window(ui.OverlayTextOverlay)
	w, h := window.Size()
	if w > app.width || h > app.height || !app.computeContentLayout().ShowMainInput || app.Input.Value() != draft {
		t.Fatal("attachment viewer broke terminal bounds or hid/replaced the composer")
	}
	_, _ = app.Update(tea.PasteMsg{Content: "EDITED "})
	if screen.Value() != "EDITED "+content {
		t.Fatal("native pasted-text editor did not edit complete content")
	}
	_, save := app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil {
		t.Fatal("Ctrl+S did not request save")
	}
	_, write := app.Update(save())
	if write == nil || !app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("save did not retain the editor while storage runs")
	}
	_, _ = app.Update(write())
	saved, readOnly, err := app.RefStore().ReadReferenceContent(refs[0].ID)
	if err != nil || readOnly || saved != "EDITED "+content || app.overlayHost.IsOpen(ui.OverlayTextOverlay) || app.Input.Value() != draft {
		t.Fatalf("save did not keep exact text and draft: readonly=%v err=%v", readOnly, err)
	}
}

func TestPastedTextPendingOwnershipAndFailureDoNotFloodDraft(t *testing.T) {
	for _, scenario := range []string{"clear", "replace", "session", "workspace", "failure", "typing"} {
		t.Run(scenario, func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			app.AddOrchestrator("paste-owner")
			app.ActiveSession = workspace.NewSession("paste-owner", t.TempDir(), nil, stubLogger{})
			app.maybeHydrateComposerDraft()
			app.Input.SetInput("original ")
			_, command := app.Update(tea.PasteMsg{Content: strings.Repeat("substantial diagnostic line\n", 30)})
			if command == nil {
				t.Fatal("large paste did not start")
			}
			_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if app.Input.Value() != "original " {
				t.Fatal("pending paste allowed send to consume the draft")
			}
			switch scenario {
			case "clear":
				app.Input.ClearInput()
			case "replace":
				app.Input.SetInput("new instruction")
			case "session":
				app.ActiveSession = workspace.NewSession("another", t.TempDir(), nil, stubLogger{})
				app.maybeHydrateComposerDraft()
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			case "typing":
				_, _ = app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			}
			result := command().(pastedTextStoredMsg)
			if scenario == "failure" {
				result.err = fmt.Errorf("storage unavailable")
			}
			before := app.Input.Value()
			_, _ = app.Update(result)
			if scenario == "typing" {
				if !strings.HasPrefix(app.Input.Value(), "original x") || len(app.Input.ReferencedIDs()) != 1 {
					t.Fatal("typing while paste stores lost the new draft or attachment")
				}
			} else if app.Input.Value() != before || len(app.Input.ReferencedIDs()) != 0 {
				t.Fatal("late/failed paste attached to a different draft or dumped its raw content")
			}
		})
	}
}

func TestReferenceOverlayRejectsReopenedLoadAndSaveKeepsErrors(t *testing.T) {
	app := newFullTestApp(t)
	app.ActiveSession = workspace.NewSession("viewer", t.TempDir(), nil, stubLogger{})
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Content: "original", Kind: sessiondb.KindPastedText})
	if err != nil {
		t.Fatal(err)
	}
	oldLoad := app.beginReferenceOverlay(ref.ID)
	newLoad := app.beginReferenceOverlay(ref.ID)
	_, _ = app.Update(oldLoad())
	if got := app.sessionTextOverlay().Value(); got != "Loading attachment…" {
		t.Fatalf("stale reopened load changed the current editor: %q", got)
	}
	_, _ = app.Update(newLoad())
	screen := app.sessionTextOverlay()
	_, _ = app.Update(tea.PasteMsg{Content: "edited "})
	command := app.saveReferenceFromOverlay(textoverlay.SaveMsg{Target: screen, RefID: ref.ID, Text: screen.Value(), Epoch: screen.OpenEpoch()})
	if command == nil {
		t.Fatal("save did not start")
	}
	if err := app.RefStore().SaveReferenceContent(ref, "original", "external edit"); err != nil {
		t.Fatal(err)
	}
	result := command().(referenceContentSavedMsg)
	if result.err == nil {
		t.Fatal("stale attachment save unexpectedly succeeded")
	}
	_, _ = app.Update(result)
	if !app.overlayHost.IsOpen(ui.OverlayTextOverlay) || screen.Value() != "edited original" {
		t.Fatal("failed save closed or cleared the editor")
	}
}

func TestLargeClipboardPasteCannotCrossDraftReplacement(t *testing.T) {
	for _, boundary := range []string{"clear", "replace", "workspace"} {
		t.Run(boundary, func(t *testing.T) {
			app := newFullTestApp(t)
			app.AddOrchestrator("owner")
			app.ActiveSession = workspace.NewSession("owner", t.TempDir(), nil, stubLogger{})
			app.maybeHydrateComposerDraft()
			app.Input.SetInput("old draft")
			restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
				read: func(format clipboard.Format) []byte {
					if format == clipboard.FmtText {
						return []byte(strings.Repeat("large clipboard diagnostic\n", 60))
					}
					return nil
				},
				writeText: func(string) error { return nil },
			})
			defer restore()
			_, read := app.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
			if read == nil {
				t.Fatal("Ctrl+V did not start its read")
			}
			result := read()
			switch boundary {
			case "clear":
				app.Input.ClearInput()
			case "replace":
				app.Input.SetInput("new draft")
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			}
			before := app.Input.Value()
			_, store := app.Update(result)
			if store != nil || app.Input.Value() != before || len(app.RefStore().ListReferences()) != 0 {
				t.Fatal("late system-clipboard result attached into a different draft")
			}
		})
	}
}
