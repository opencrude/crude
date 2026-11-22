package viewmodel

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/internal/clipboard"
	"crude/tui/editor"
	"crude/workspace"
)

func TestClipboardEditorCopyThenComposerPaste(t *testing.T) {
	copyKey := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	pasteKey := tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}
	for _, unavailable := range []bool{false, true} {
		name := "system"
		if unavailable {
			name = "terminal-only"
		}
		t.Run(name, func(t *testing.T) {
			app := setupChatInputApp(t, 100, 30, false)
			wsOf(app).Logger = stubLogger{}
			activatePasteSession(app, workspace.NewSession("clipboard-transfer", t.TempDir(), nil, stubLogger{}))
			t.Cleanup(app.StopAsyncIO)
			value := "old clipboard contents"
			t.Cleanup(installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
				// X11 owners can return their text even for an image request.
				read: func(clipboard.Format) []byte {
					if !unavailable {
						return []byte(value)
					}
					return nil
				},
				writeText: func(text string) error {
					if unavailable {
						return errors.New("no desktop clipboard")
					}
					value = text
					return nil
				},
			}))
			first := editableClipboardFixture(t, app, "first.txt")
			second := editableClipboardFixture(t, app, "second.txt")
			for _, source := range []struct {
				editor *editor.Editor
				text   string
			}{{first, "first file"}, {second, "second file"}} {
				app.bindEditorClipboard(source.editor)
				source.editor.HandleEvent(tea.PasteMsg{Content: source.text})
				source.editor.HandleEvent(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
			}
			for _, source := range []struct {
				editor *editor.Editor
				text   string
			}{{first, "first file"}, {second, "second file"}, {first, "first file"}} {
				app.EditorState.Editor = source.editor
				_, copyCmd := app.dispatchEditorKey(copyKey)
				if copyCmd == nil {
					t.Fatal("editor copy returned no command")
				}
				// The next key may be handled before Bubble Tea starts the copy
				// command. Admission must already know what text was copied.
				app.Input.SetInput("")
				_, pasteCmd := app.dispatchChatKey(pasteKey, nil)
				runAppCommand(t, app, pasteCmd)
				if got := app.Input.Value(); got != source.text {
					t.Fatalf("composer pasted %q; want %q from the selected file", got, source.text)
				}
				runAppCommand(t, app, copyCmd)
			}
		})
	}
}

func TestClipboardKeepsInterleavedCopiesAndPastes(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(app.StopAsyncIO)
	target := editableClipboardFixture(t, app, "target.txt")
	app.EditorState.Editor = target

	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var text string
	t.Cleanup(installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(clipboard.Format) []byte { return []byte(text) },
		writeText: func(value string) error {
			if value == "previous" {
				close(started)
				<-release
			}
			text = value
			return nil
		},
	}))

	first := app.clipboardCopyCommand("previous")
	select {
	case <-started: // Keep both succeeding copies queued at the same time.
	case <-time.After(5 * time.Second):
		t.Fatal("clipboard worker did not start the first copy")
	}
	copyAlpha := app.clipboardCopyCommand("alpha")
	pasteAlpha := app.beginEditorClipboardPaste(target)
	copyBeta := app.clipboardCopyCommand("beta")
	pasteBeta := app.beginEditorClipboardPaste(target)
	close(release)

	// Bubble Tea may deliver the last notification first. Both pastes must
	// already have captured their own values, and each applies only once.
	runAppCommand(t, app, pasteBeta)
	for _, cmd := range []tea.Cmd{first, copyAlpha, pasteAlpha, copyBeta} {
		runAppCommand(t, app, cmd)
	}
	if got := target.Value(); got != "alphabeta" {
		t.Fatalf("pasted %q; want each clipboard value in gesture order", got)
	}
}
