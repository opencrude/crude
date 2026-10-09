package viewmodel

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/sessiondb"
	"crude/workspace"
)

// activatePasteSession gives paste journeys a real selected agent/window tab.
func activatePasteSession(app *App, sess *workspace.Session) {
	if app.ws.SessionsByAgent() == nil {
		wsOf(app).SessionsByAgent = make(map[string]*workspace.Session)
	}
	app.ws.SessionsByAgent()[sess.SessionID] = sess
	app.ActiveSession = sess
	app.AddOrchestrator(sess.SessionID)
	app.Input.SetReferenceStore(sess.RefStore)
}

// TestComposerCtrlVPastesFromSystemClipboard guards the composer paste fix:
// Ctrl+V and Ctrl+Shift+V must enqueue an image-first system-clipboard read.
// Bracketed paste cannot carry images (terminals only wrap text), which is why
// the editor always had its own explicit ctrl+v path and the composer did not.
func TestComposerCtrlVPastesFromSystemClipboard(t *testing.T) {
	app := newFullTestApp(t)
	app.ActiveSession = workspace.NewSession("paste-session", t.TempDir(), nil, stubLogger{})
	activatePasteSession(app, app.ActiveSession)
	app.Input.SetReferenceStore(app.ActiveSession.RefStore)

	for _, key := range []string{"ctrl+v", "ctrl+shift+v"} {
		var mods tea.KeyMod
		switch key {
		case "ctrl+v":
			mods = tea.ModCtrl
		case "ctrl+shift+v":
			mods = tea.ModCtrl | tea.ModShift
		}
		msg := tea.KeyPressMsg{Code: 'v', Mod: mods}
		if got := msg.String(); got != key {
			t.Fatalf("key normalization changed: %q != %q", got, key)
		}
		if _, cmd := app.dispatchChatKey(msg, nil); cmd == nil {
			t.Fatalf("%s must enqueue a clipboard read command", key)
		}
	}

	// No active session: nothing to attach into, the chord must stay inert.
	app.ActiveSession = nil
	if _, cmd := app.dispatchChatKey(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}, nil); cmd != nil {
		t.Fatal("ctrl+v without an active session must not enqueue work")
	}
}

// Windows Terminal owns Ctrl+V and delivers image-only clipboard contents as
// an empty bracketed paste, not a KeyPressMsg. Exercise Update through the
// clipboard read, asynchronous reference persistence, and draft attachment.
func TestComposerEmptyTerminalPasteAttachesClipboardImage(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.ActiveSession = workspace.NewSession("terminal-image", t.TempDir(), nil, stubLogger{})
	activatePasteSession(app, app.ActiveSession)
	app.Input.SetReferenceStore(app.ActiveSession.RefStore)
	app.Input.SetInput("keep my draft ")
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format != clipboard.FmtImage {
				t.Error("empty terminal paste must not read unrelated clipboard text")
				return []byte("unrelated clipboard text")
			}
			return encoded.Bytes()
		},
		writeText: func(string) error { return nil },
	})
	defer restore()
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })

	_, readCmd := app.Update(tea.PasteMsg{Content: ""})
	if readCmd == nil {
		t.Fatal("image-only terminal paste did not request the clipboard image")
	}
	_, persistCmd := app.Update(readCmd())
	if persistCmd == nil {
		t.Fatal("clipboard image did not start asynchronous attachment persistence")
	}
	if refs := app.ActiveSession.RefStore.ListReferences(); len(refs) != 0 {
		t.Fatal("Update persisted the image synchronously")
	}
	_, _ = app.Update(persistCmd())
	refs := app.ActiveSession.RefStore.ListReferences()
	if len(refs) != 1 || refs[0].Kind != sessiondb.KindPastedImage || refs[0].Mime != "image/png" {
		t.Fatalf("references=%+v, want exactly one pasted PNG", refs)
	}
	if got := app.Input.Value(); !strings.HasPrefix(got, "keep my draft ") || !strings.Contains(got, refs[0].Label) {
		t.Fatalf("draft=%q, want preserved text and the image reference", got)
	}
	app.paintShellFrame()
	if !strings.Contains(bufferPlain(app.mainSurf.Buffer()), refs[0].Label) {
		t.Fatal("attached image is missing from the painted composer")
	}
}

func TestComposerEmptyTerminalPasteWithoutImageIsSilent(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.ActiveSession = workspace.NewSession("empty-paste", t.TempDir(), nil, stubLogger{})
	activatePasteSession(app, app.ActiveSession)
	app.Input.SetInput("unchanged draft")
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format != clipboard.FmtImage {
				t.Error("empty terminal paste must not fall back to clipboard text")
				return []byte("stale clipboard text")
			}
			return nil
		},
		writeText: func(string) error { return nil },
	})
	defer restore()
	_, cmd := app.Update(tea.PasteMsg{})
	if cmd == nil {
		t.Fatal("empty paste did not check for an image")
	}
	result := cmd()
	if read := result.(clipboardAsyncResultMsg); read.operationErr != nil {
		t.Fatalf("missing clipboard image produced a spurious error: %v", read.operationErr)
	}
	_, persist := app.Update(result)
	if persist != nil || app.Input.Value() != "unchanged draft" || len(app.ActiveSession.RefStore.ListReferences()) != 0 {
		t.Fatal("empty terminal paste without an image must not mutate the draft or persist a reference")
	}
}

func TestTerminalPasteOnlyComposerCanReadClipboardImage(t *testing.T) {
	for _, owner := range []string{"text paste", "launcher", "tree", "editor", "tab bar", "popup", "no session"} {
		t.Run(owner, func(t *testing.T) {
			var app *App
			switch owner {
			case "launcher":
				app, _ = setupLauncherRoutingApp(t, nil)
			case "tree":
				app, _, _ = setupTreeRoutingApp(t)
			default:
				app = newFullTestApp(t)
			}
			app.ActiveSession = workspace.NewSession("owned-paste", t.TempDir(), nil, stubLogger{})
			if owner != "launcher" {
				activatePasteSession(app, app.ActiveSession)
			}
			app.Input.SetInput("unchanged draft")
			message := tea.PasteMsg{}
			if owner != "launcher" {
				app.launcherScreen = nil
			}
			switch owner {
			case "text paste":
				message.Content = "terminal text"
			case "tree":
				app.Tree.Visible = true
				app.setFocusTree()
			case "editor":
				openTestEditor(t, app)
			case "tab bar":
				app.FocusStack.Push(FocusTabBar)
			case "popup":
				openTestEditor(t, app)
				app.openFindReplace(false)
			case "no session":
				app.ActiveSession = nil
			}
			state := asyncIOStateFor(app)
			_, _ = app.Update(message)
			state.mu.Lock()
			requests := state.nextClipboard
			state.mu.Unlock()
			if requests != 0 {
				t.Fatalf("%s paste started %d clipboard requests", owner, requests)
			}
			want := "unchanged draft"
			if owner == "text paste" {
				want += "terminal text"
			}
			if got := app.Input.Value(); got != want {
				t.Fatalf("draft=%q want %q", got, want)
			}
		})
	}
}

func TestComposerEmptyTerminalPasteKeepsInitiatingSession(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	first := workspace.NewSession("first-paste", t.TempDir(), nil, stubLogger{})
	second := workspace.NewSession("second-paste", t.TempDir(), nil, stubLogger{})
	activatePasteSession(app, first)
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read:      func(clipboard.Format) []byte { return []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR") },
		writeText: func(string) error { return nil },
	})
	defer restore()
	_, cmd := app.Update(tea.PasteMsg{})
	if cmd == nil {
		t.Fatal("empty paste did not capture its session")
	}
	result := cmd()
	app.ActiveSession = second
	_, persist := app.Update(result)
	if persist != nil || len(first.RefStore.ListReferences()) != 0 || len(second.RefStore.ListReferences()) != 0 || app.Input.Value() != "" {
		t.Fatal("late image paste crossed the session-switch boundary")
	}
}

func TestBlockingDeckOwnsEmptyTerminalPaste(t *testing.T) {
	app := newFullTestApp(t)
	app.ActiveSession = workspace.NewSession("blocked-paste", t.TempDir(), nil, stubLogger{})
	activatePasteSession(app, app.ActiveSession)
	app.launcherScreen = nil
	app.Input.SetInput("unchanged draft")
	projection := app.sessionProjections.Get(app.ActiveSession)
	openTestAgentPrompt(t, app, projection, agentPromptRequest{
		kind: agentPromptQuestions, questionsJSON: `[{"question":"Pick one","options":["yes","no"]}]`,
	})
	focused := app.rootHost.Focused()
	state := asyncIOStateFor(app)
	_, _ = app.Update(tea.PasteMsg{})
	state.mu.Lock()
	requests := state.nextClipboard
	state.mu.Unlock()
	if requests != 0 || app.Input.Value() != "unchanged draft" {
		t.Fatal("empty terminal paste escaped the blocking deck into the composer")
	}
	// Retained focus remains inside the deck, not the underlying composer.
	if app.rootHost.Focused() != focused {
		t.Fatal("empty paste stole focus from the blocking deck")
	}
}

// TestFilePickerKeepsComposerMounted guards the Ctrl+O visibility fix: the
// picker is a modal FloatingWindow over the chat, so the composer must stay
// laid out while it is open (outside clicks are consumed by the overlay
// policy). Every other overlay mode still owns the whole frame.
func TestFilePickerKeepsComposerMounted(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 100
	app.height = 30
	activatePasteSession(app, workspace.NewSession("picker-session", t.TempDir(), nil, stubLogger{}))

	app.OverlayState.Set(workspace.ModeFilePicker)
	if lay := app.computeContentLayout(); !lay.ShowMainInput {
		t.Fatal("file picker overlay must not hide the composer")
	}
	if lay := app.computeContentLayout(); lay.InputLineCount < 1 {
		t.Fatalf("composer rows must be reserved during picker mode, got %d", lay.InputLineCount)
	}

	app.OverlayState.Set(workspace.ModeTextOverlay)
	if lay := app.computeContentLayout(); !lay.ShowMainInput {
		t.Fatal("attachment viewer must preserve the composer under its floating frame")
	}
}
