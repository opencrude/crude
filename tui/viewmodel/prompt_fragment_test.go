package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/agent/sessiondb"
	"crude/clipboard"
	"crude/workspace"
)

// The user copies part of a prompt that shows attachments and pastes it
// elsewhere: the pasted text shows the same attachments with the same content,
// not dead labels. Copy and Cut chords, the menu, the same session and another
// session all reproduce them.
func TestCopiedPromptPartPastesWithItsAttachments(t *testing.T) {
	for _, route := range []string{"copy-same-session", "cut-same-session", "menu-copy-other-session"} {
		t.Run(route, func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			app.AddOrchestrator("source")
			db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, id := range []string{"source", "target"} {
				if err := sessiondb.CreateSessionRow(db, id); err != nil {
					t.Fatal(err)
				}
			}
			source := workspace.NewSession("source", t.TempDir(), db, stubLogger{})
			app.ActiveSession = source
			app.maybeHydrateComposerDraft()
			app.Input.SetReferenceStore(app.RefStore())
			content := strings.Repeat("panic trace line\n", 40) + "tail"
			ref, err := source.RefStore.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Title: "trace", Mime: "text/plain", Content: content, Inline: true})
			if err != nil {
				t.Fatal(err)
			}
			app.Input.SetInput("look at " + ref.Label + " and fix it")
			draft := app.Input.Value()
			selection := []rune("look at " + ref.Label + " and")
			awaitCopy := recordingClipboard(t, app)
			switch route {
			case "copy-same-session", "cut-same-session":
				app.Input.SetSelection(0, len(selection))
				code := 'c'
				if route == "cut-same-session" {
					code = 'x'
				}
				_, cmd := app.Update(tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl})
				runAppCommand(t, app, cmd)
			default:
				activateMenuItem(t, app.buildInputContextMenu(tea.Mouse{X: -1, Y: -1}), "Copy")
				selection = []rune(draft)
				target := workspace.NewSession("target", t.TempDir(), db, stubLogger{})
				app.ActiveSession = target
				app.maybeHydrateComposerDraft()
				app.Input.SetReferenceStore(app.RefStore())
			}
			copied := awaitCopy()
			if copied != string(selection) {
				t.Fatalf("system clipboard got %q, want %q", copied, string(selection))
			}
			if route == "cut-same-session" {
				if app.Input.Value() != " fix it" {
					t.Fatalf("cut left %q", app.Input.Value())
				}
				if !source.RefStore.ListReferences()[0].Active() {
					t.Fatal("cut deleted the attachment the clipboard still needs")
				}
			}
			app.Input.SetInput("before ")
			_, cmd := app.Update(tea.PasteMsg{Content: copied})
			if cmd == nil {
				t.Fatal("paste did not schedule attachment reproduction")
			}
			runAppCommand(t, app, cmd)
			ids := app.Input.ReferencedIDs()
			if len(ids) != 1 {
				t.Fatalf("pasted text has %d attachments, want 1: %q", len(ids), app.Input.Value())
			}
			pasted, ok := app.RefStore().GetReference(ids[0])
			if !ok || pasted.Content != content {
				t.Fatal("pasted attachment lost its content")
			}
			if route == "menu-copy-other-session" {
				if pasted.SessionID != "target" || pasted.ID == ref.ID {
					t.Fatal("another session's paste did not get its own copy of the attachment")
				}
			} else if pasted.ID != ref.ID {
				t.Fatal("a paste in the same session duplicated the attachment")
			}
			want := "before " + strings.Replace(string(selection), ref.Label, pasted.Label, 1)
			if app.Input.Value() != want {
				t.Fatalf("pasted draft=%q want %q", app.Input.Value(), want)
			}
			app.paintShellFrame()
			if !strings.Contains(bufferPlain(app.mainSurf.Buffer()), pasted.Label) {
				t.Fatal("pasted attachment was not painted")
			}
		})
	}
}

// Text copied elsewhere, or plain text copied from the composer, pastes as
// literal text: an older attachment copy never reappears.
func TestPlainPasteNeverReusesAnOlderAttachmentCopy(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.AddOrchestrator("source")
	app.ActiveSession = workspace.NewSession("source", t.TempDir(), nil, stubLogger{})
	app.maybeHydrateComposerDraft()
	app.Input.SetReferenceStore(app.RefStore())
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Title: "x", Mime: "text/plain", Content: "payload", Inline: true})
	if err != nil {
		t.Fatal(err)
	}
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read:      func(clipboard.Format) []byte { return nil },
		writeText: func(string) error { return nil },
	})
	defer restore()
	app.Input.SetInput(ref.Label)
	app.Input.SetSelection(0, len([]rune(ref.Label)))
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	app.Input.SetInput("plain words")
	app.Input.SetSelection(0, 5)
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	app.Input.SetInput("")
	_, cmd = app.Update(tea.PasteMsg{Content: ref.Label})
	runAppCommand(t, app, cmd)
	if app.Input.Value() != ref.Label {
		t.Fatalf("draft=%q", app.Input.Value())
	}
}
