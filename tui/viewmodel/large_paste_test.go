package viewmodel

import (
	"crude/agent/turnloop"
	"crude/types/conversation"
	"crude/types/setting"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent/sessiondb"
	"crude/internal/clipboard"
	"crude/internal/providersim"
	"crude/tui/ui"
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

type pastedTextJourneySender struct{ events chan AgentMsg }

func (s *pastedTextJourneySender) SendAgentEvent(typ string, data any) {
	s.SendAgentEventWithID(typ, data, "")
}

func (s *pastedTextJourneySender) SendAgentEventWithID(typ string, data any, id string) {
	s.events <- AgentMsg{Type: typ, Data: data, AgentID: id}
}

func TestPastedTextSendsCompleteContentButKeepsChatCompact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	content := strings.Repeat("Complete pasted diagnostic.\n", 1000) + "UNIQUE-DIAGNOSTIC-TAIL"
	requests := make(chan string, 1)
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		requests <- simulatedUserText(r, "")
		return simulatedAnswer("Received the complete diagnostic.")
	})
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	wsOf(app).RootPath, wsOf(app).WorkspaceDir = t.TempDir(), t.TempDir()
	wsOf(app).SessionsByID, wsOf(app).SessionsByAgent = make(map[string]*workspace.Session), make(map[string]*workspace.Session)
	wsOf(app).SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "off"}})
	cfg := testAccountConfig()
	account := cfg.Accounts["test"]
	template := account.Connection
	template.ProviderURL = server.BaseURL(providersim.Chat)
	account.Connection = template
	cfg.Accounts["test"] = account
	cfg.Agents.Enabled = false
	owner, err := wsOf(app).CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	sender := &pastedTextJourneySender{events: make(chan AgentMsg, 512)}
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
			var messages []conversation.Message
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
	ref1, _ := app.RefStore().Reference(ids[0])
	ref2, _ := app.RefStore().Reference(ids[1])
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

func TestPastedTextFileChipOpensCompleteEditorTabAndSaves(t *testing.T) {
	app := newFullTestApp(t)
	setAttachmentDestination(t, app, setting.AttachmentOpenCrude)
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
	x, y := app.inputRect.X+chips[0].Body.X+1, app.inputRect.Y+chips[0].Body.Y
	_, open := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, open)
	ed := app.EditorState.Editor
	if ed == nil || !ed.IsOpen() || ed.ReadOnly() || ed.Value() != content || ed.FilePath() != refs[0].FilePath || app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("native open did not use the complete file in a normal editor tab")
	}
	_, cmd := app.Update(tea.PasteMsg{Content: "EDITED "})
	runAppCommand(t, app, cmd)
	if ed.Value() != "EDITED "+content {
		t.Fatal("typing did not reach the editor")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	saved, _, err := app.RefStore().ReadReferenceContent(refs[0].ID)
	if err != nil || saved != "EDITED "+content || ed.Modified() || app.Input.Value() != draft {
		t.Fatalf("attachment save did not preserve the edited text and composer: err=%v dirty=%t", err, ed.Modified())
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

// A completed read cannot open its editor after the initiating draft changes.
func TestPasteEditorRejectsSupersededOpen(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedTextFile, Content: "original"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := app.openAttachment(ref.ID, false)
	prepared := cmd()
	app.cancelAttachmentOpen()
	app.Update(prepared)
	if len(app.EditorState.Tabs) != 0 || app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("superseded paste opened on the replacement tab")
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

func (s *pastedTextJourneySender) SendAgentEventEnvelope(event turnloop.AgentEvent) {
	s.SendAgentEventWithID(event.Type, event.Data, event.AgentID)
}
