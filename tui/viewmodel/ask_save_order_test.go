package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent/sessiondb"
	"crude/internal/providersim"
	"crude/tui/editor"
	"crude/types/statuscenter"
)

const (
	askOrderFile     = "example.txt"
	askOrderOriginal = "value = 1\n"
	askOrderAsked    = "value = 12\n"
	askOrderProblem  = "Value is too large."
	askOrderNotice   = "Crude did not send the request because example.txt could not be saved. Save the file, then try again."
)

type askOrderJourney struct {
	shell remoteJourneyShell
	ed    *editor.Editor
	path  string
	calls atomic.Int32
}

func newAskOrderJourney(t *testing.T) *askOrderJourney {
	t.Helper()
	j := &askOrderJourney{}
	server := providersim.New(t)
	server.Respond(func(providersim.Request) providersim.Turn {
		j.calls.Add(1)
		return simulatedAnswer("Answer retained.")
	})
	runtime := remoteJourneyRuntime(t, server.BaseURL(providersim.Chat))
	root := t.TempDir()
	j.path = filepath.Join(root, askOrderFile)
	if err := os.WriteFile(j.path, []byte(askOrderOriginal), 0o600); err != nil {
		t.Fatal(err)
	}
	j.shell = newRemoteJourneyShell(t, runtime, root)
	app := j.shell.app
	j.ed = liveTestOpen(t, app, j.path)
	j.ed.SetCursorPosition(0, len(askOrderOriginal)-1)
	_, cmd := app.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	runAppCommand(t, app, cmd)
	liveTestSync(t, app)
	snapshot := j.ed.DocumentSnapshot()
	if !j.ed.Modified() || j.ed.Value() != askOrderAsked || string(snapshot.Text) != askOrderAsked || string(snapshot.Saved) != askOrderOriginal {
		t.Fatalf("setup needs a real unsaved document edit: editor=%q text=%q saved=%q", j.ed.Value(), snapshot.Text, snapshot.Saved)
	}
	j.checkDisk(t, askOrderOriginal)
	return j
}

func (j *askOrderJourney) begin(t *testing.T, kind askKind, laterKey tea.KeyPressMsg) (agentID string, heldDocumentWork tea.Cmd) {
	t.Helper()
	app := j.shell.app
	var cmds []tea.Cmd
	if kind == askFinish {
		// Take the real menu action and its actual edit history from the
		// connected core. Split the completion from App.Update only so
		// the test can control when its document command is executed.
		history := make(chan finishChangeMsg, 1)
		restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) {
			if edits, ok := msg.(finishChangeMsg); ok {
				history <- edits
			} else {
				j.shell.fifo <- msg
			}
		})
		defer restore()
		menu := app.buildEditorContextMenu(tea.Mouse{})
		var finish func()
		for _, section := range menu.Sections {
			for _, item := range section.Items {
				if item.Label == "Finish this change" {
					finish = item.Action
				}
			}
		}
		if finish == nil {
			t.Error("Finish this change was not available after a real edit")
			// Preserve the menu failure and still test the handler's
			// save ordering independently.
			finish = app.runFinishChange
		}
		finish()
		select {
		case result := <-history:
			if result.err != nil {
				t.Fatal(result.err)
			}
			cmds = app.applyFinishChange(result)
		case <-time.After(8 * time.Second):
			t.Fatal("Finish this change did not read the connected edit history")
		}
	} else {
		cmds = app.askCrude(kind, false, askOrderProblem)
	}
	if laterKey.Code != 0 {
		// The original request is already captured. Input can still
		// arrive before the chat opens or the save takes its source.
		j.ed.HandleEvent(laterKey)
	}
	heldDocumentWork = app.drainLiveEditorCommands()
	if heldDocumentWork == nil {
		t.Fatal("Ask did not schedule the live document work")
	}
	runAppCommand(t, app, tea.Batch(cmds...))
	remoteJourneyUntil(t, []remoteJourneyShell{j.shell}, func() bool {
		agentID = app.AgentView.ActiveID()
		if agentID == "" || app.Win.TabByTitle(app.agentTabTitle(agentID)) == nil {
			return false
		}
		if kind == askAttach {
			return len(app.Input.ReferencedIDs()) != 0
		}
		// Wait until the prepared prompt has reached the admission boundary.
		// A regression can cross it early: do not wait only for a held send.
		return len(app.askSends) > 0 || len(app.pendingInputAdmissions) > 0 ||
			len(j.userMessages(agentID)) > 0 || j.hasErrorNotice()
	})
	if messages := j.userMessages(agentID); len(messages) != 0 || j.calls.Load() != 0 || len(app.pendingInputAdmissions) != 0 {
		t.Fatalf("Ask submitted before save settled: saved=%q messages=%v model calls=%d",
			j.ed.DocumentSnapshot().Saved, messages, j.calls.Load())
	}
	if j.hasErrorNotice() {
		t.Fatal("Ask reported a save failure before the scheduled save ran")
	}
	if got := string(j.ed.DocumentSnapshot().Saved); got != askOrderOriginal {
		t.Fatalf("before release saved=%q, want %q", got, askOrderOriginal)
	}
	j.checkDisk(t, askOrderOriginal)
	return agentID, heldDocumentWork
}

func TestAskPreparedPromptsSubmitOnceAfterTheConnectedSave(t *testing.T) {
	for _, tc := range []struct {
		name, promptPart, wantSaved string
		kind                        askKind
		laterKey                    tea.KeyPressMsg
	}{
		{"Explain", "Explain [example.txt:1].", askOrderAsked, askExplain, tea.KeyPressMsg{}},
		{"Fix", "Fix this problem in [example.txt:1]: Value is too large.", askOrderAsked, askFix, tea.KeyPressMsg{}},
		{"Finish this change", "Finish the change I started in example.txt.", askOrderAsked, askFinish, tea.KeyPressMsg{}},
		{"Explain after more typing", "Explain [example.txt:1].", "value = 123\n", askExplain, tea.KeyPressMsg{Code: '3', Text: "3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := newAskOrderJourney(t)
			agentID, save := j.begin(t, tc.kind, tc.laterKey)
			app := j.shell.app
			runAppCommand(t, app, save)
			remoteJourneyUntil(t, []remoteJourneyShell{j.shell}, func() bool {
				node := app.ws.AgentMgr().Get(agentID)
				if node == nil || node.State == nil {
					return false
				}
				for _, msg := range node.State.Messages.Messages() {
					if msg.Role == "assistant" && strings.Contains(msg.Content, "Answer retained.") {
						return true
					}
				}
				return false
			})
			liveTestSync(t, app)
			if got := string(j.ed.DocumentSnapshot().Saved); got != tc.wantSaved || j.ed.Modified() {
				t.Fatalf("sent prompt without the saved document: saved=%q, want %q", got, tc.wantSaved)
			}
			j.checkDisk(t, tc.wantSaved)
			messages := j.userMessages(agentID)
			if len(messages) != 1 || !strings.Contains(messages[0], tc.promptPart) || j.calls.Load() != 1 || j.hasErrorNotice() {
				t.Fatalf("after save: messages=%v, model calls=%d, save error=%t", messages, j.calls.Load(), j.hasErrorNotice())
			}
			if tc.kind == askFinish {
				if !strings.Contains(messages[0], "@@ line 1\n+2") {
					t.Fatalf("Finish lost the user's original edit: %q", messages[0])
				}
			} else {
				sess := app.ws.SessionsByAgent()[agentID]
				var selections []sessiondb.Reference
				for _, ref := range sess.RefStore.ListReferences() {
					if ref.Kind == sessiondb.KindCodeSelection {
						selections = append(selections, ref)
					}
				}
				if len(selections) != 1 || selections[0].DisplayName() != "[example.txt:1]" ||
					selections[0].Content != strings.TrimSuffix(askOrderAsked, "\n") {
					t.Fatalf("sent prompt lost the code seen at Ask time: selections=%+v", selections)
				}
			}
		})
	}
}

func TestAskPreparedPromptDoesNotSendOnFailedSaveConflictOrRevert(t *testing.T) {
	for _, tc := range []struct {
		name, wantDisk string
		key            tea.KeyPressMsg
		beforeSave     func(*testing.T, string)
		conflict       bool
	}{
		{"save failure", askOrderOriginal, tea.KeyPressMsg{}, func(t *testing.T, path string) {
			t.Helper()
			if err := os.Chmod(path, 0o400); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(path, 0o600); err != nil {
					t.Error(err)
				}
			})
		}, false},
		{"disk conflict", "outside = 9\n", tea.KeyPressMsg{}, func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte("outside = 9\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"revert to the old savepoint", askOrderOriginal, tea.KeyPressMsg{Code: tea.KeyBackspace}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := newAskOrderJourney(t)
			agentID, save := j.begin(t, askExplain, tc.key)
			if tc.beforeSave != nil {
				tc.beforeSave(t, j.path)
			}
			app := j.shell.app
			runAppCommand(t, app, save)
			liveTestSync(t, app)
			if got := string(j.ed.DocumentSnapshot().Saved); got != askOrderOriginal || j.ed.DiskConflict() != tc.conflict {
				t.Fatalf("fixture ended saved=%q conflict=%t, want %q conflict=%t", got, j.ed.DiskConflict(), askOrderOriginal, tc.conflict)
			}
			if want := tc.key.Code == 0; j.ed.Modified() != want {
				t.Fatalf("failure/revert left modified=%t, want %t, text=%q", j.ed.Modified(), want, j.ed.Value())
			}
			j.checkDisk(t, tc.wantDisk)
			if messages := j.userMessages(agentID); len(messages) != 0 || j.calls.Load() != 0 || len(app.pendingInputAdmissions) != 0 {
				t.Fatalf("Ask submitted after %s: messages=%v, model calls=%d", tc.name, messages, j.calls.Load())
			}
			if !j.hasErrorNotice() {
				t.Fatalf("missing error notice %q after %s; notices=%+v", askOrderNotice, tc.name, app.ws.Status().Notifications(false))
			}
		})
	}
}

func TestAskAttachShowsTheUnsentChipWhileTheSaveIsHeld(t *testing.T) {
	j := newAskOrderJourney(t)
	agentID, save := j.begin(t, askAttach, tea.KeyPressMsg{})
	if ref := askComposerRef(t, j.shell.app); ref.Kind != sessiondb.KindCodeSelection ||
		ref.Content != strings.TrimSuffix(askOrderAsked, "\n") || ref.DisplayName() != "[example.txt:1]" {
		t.Fatalf("plain Ask did not attach the unsaved code before the save: %+v", ref)
	}
	runAppCommand(t, j.shell.app, save)
	liveTestSync(t, j.shell.app)
	j.checkDisk(t, askOrderAsked)
	if got := string(j.ed.DocumentSnapshot().Saved); got != askOrderAsked {
		t.Fatalf("normal attached Ask lost its eventual save: %q", got)
	}
	if messages := j.userMessages(agentID); len(messages) != 0 || j.calls.Load() != 0 {
		t.Fatalf("attach-only Ask submitted instead of leaving a draft: messages=%v model calls=%d", messages, j.calls.Load())
	}
}

func (j *askOrderJourney) checkDisk(t *testing.T, want string) {
	t.Helper()
	data, err := os.ReadFile(j.path)
	if err != nil || string(data) != want {
		t.Fatalf("disk=%q, err=%v; want %q", data, err, want)
	}
}

func (j *askOrderJourney) userMessages(agentID string) []string {
	var messages []string
	if node := j.shell.app.ws.AgentMgr().Get(agentID); node != nil && node.State != nil {
		for _, msg := range node.State.Messages.Messages() {
			if msg.Role == "user" {
				messages = append(messages, msg.Content)
			}
		}
	}
	return messages
}

func (j *askOrderJourney) hasErrorNotice() bool {
	for _, notice := range j.shell.app.ws.Status().Notifications(false) {
		if notice.Severity == statuscenter.SeverityError && notice.Summary == askOrderNotice {
			return true
		}
	}
	return false
}
