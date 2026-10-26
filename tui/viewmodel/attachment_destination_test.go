package viewmodel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/history"
	"crude/sessiondb"
	"crude/tui/input"
	"crude/tui/ui"
	"crude/types"
	"crude/types/attachment"
	"crude/workspace"
)

func setAttachmentDestination(t *testing.T, app *App, destination string) {
	t.Helper()
	settings := app.ws.AppDefaults()
	settings.UI.AttachmentOpen = destination
	wsOf(app).SetAppDefaults(settings)
}

func attachmentImageBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 30), G: uint8(y * 60), A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func desktopAttachmentFile(t *testing.T, output string, reveal bool) string {
	t.Helper()
	args, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSuffix(string(args), "\n")
	if reveal && runtime.GOOS == "darwin" {
		path = strings.TrimPrefix(path, "-R\n")
	} else if reveal {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 1 {
			t.Fatalf("file manager did not receive a folder containing the attachment: %+v %v", entries, err)
		}
		path = filepath.Join(path, entries[0].Name())
	}
	return path
}

func TestAttachmentDestinationAllEntryPoints(t *testing.T) {
	output := recordDesktopLauncher(t)
	for _, destination := range []string{"", "system", "file-manager", "crude"} {
		for _, entry := range []string{"chip", "keyboard", "posted", "sqlite", "prompt-history"} {
			t.Run(destination+"/"+entry, func(t *testing.T) {
				app := newTranscriptAttachmentApp(t)
				setAttachmentDestination(t, app, destination)
				original := attachmentImageBytes(t)
				ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, Mime: "image/png", Data: original})
				if err != nil {
					t.Fatal(err)
				}
				switch entry {
				case "chip":
					app.Input.SetInput("KEEP MY COMPOSER DRAFT " + ref.Label)
					app.paintShellFrame()
					chips := app.Input.ReferenceChips(app.inputRect.W)
					if len(chips) != 1 {
						t.Fatal("image chip is absent")
					}
					x, y := app.inputRect.X+chips[0].Body.X+1, app.inputRect.Y+chips[0].Body.Y
					_, command := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
					runAppCommand(t, app, command)
				case "keyboard":
					for _, command := range app.handleInputEvent(input.RefOpenEvent{ID: ref.ID}) {
						runAppCommand(t, app, command)
					}
				case "posted", "sqlite":
					if entry == "sqlite" {
						app.hydrateActiveMessages(ChatMessagesFromViews(retainedAttachmentRows(t, ref, "Inspect "+ref.Label)))
					} else {
						app.appendActiveMessage(types.ChatMessage{InternalID: "posted", Role: types.RoleUser, Content: ref.Label, Attachments: attachment.Locate(ref.Label, []attachment.Target{{ReferenceID: ref.ID, Label: ref.Label}})})
					}
					clickTranscriptAttachment(t, app, ref.Label)
				case "prompt-history":
					app.SetPromptHistory(history.New())
					app.Prompts.Record(ref.Label, time.Now(), history.Attachment{Start: 0, End: len(ref.Label), Reference: ref})
					app.renderPromptHistory()
					runAppCommand(t, app, app.openHistoricalAttachment(app.Prompts.Entries()[0].ID+":0"))
				}
				if destination == "crude" {
					if !app.overlayHost.IsOpen(ui.OverlayTextOverlay) || !strings.Contains(app.View().RenderContent(), "▀") {
						t.Fatal("Crude preference did not open native image preview")
					}
				} else {
					if app.overlayHost.IsOpen(ui.OverlayTextOverlay) || strings.Contains(app.View().RenderContent(), "▀") {
						t.Fatal("OS destination still opened the blocky preview")
					}
					path := desktopAttachmentFile(t, output, destination == "file-manager")
					data, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(data, original) || filepath.Ext(path) != ".png" || path == ref.FilePath {
						t.Fatalf("OS app did not receive the full original image as a private .png copy: %q %v", path, err)
					}
					if err := os.WriteFile(path, []byte("edited externally"), 0o600); err != nil {
						t.Fatal(err)
					}
					app.StopAsyncIO()
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatal("shell shutdown retained the external attachment copy")
					}
				}
				data, err := os.ReadFile(ref.FilePath)
				if err != nil || !bytes.Equal(data, original) || !strings.HasPrefix(app.Input.Value(), "KEEP MY COMPOSER DRAFT") {
					t.Fatal("opening changed the canonical image bytes or consumed the draft")
				}
			})
		}
	}
}

func TestSystemAttachmentTextAndSelectedFile(t *testing.T) {
	output := recordDesktopLauncher(t)
	for _, kind := range []sessiondb.Kind{sessiondb.KindPastedText, sessiondb.KindPastedTextFile, sessiondb.KindSelectedFile} {
		t.Run(string(kind), func(t *testing.T) {
			app := newTranscriptAttachmentApp(t)
			setAttachmentDestination(t, app, "")
			const content = "EXACT complete attachment\nEnd of full original text"
			req := sessiondb.CreateRequest{Kind: kind, Content: content}
			if kind == sessiondb.KindSelectedFile {
				req.FilePath = filepath.Join(t.TempDir(), "selected & untrusted.txt")
				if err := os.WriteFile(req.FilePath, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ref, err := app.RefStore().CreateReference(req)
			if err != nil {
				t.Fatal(err)
			}
			command := app.openAttachment(ref.ID, false)
			if _, err := os.Stat(output); err == nil {
				if err := os.Remove(output); err != nil {
					t.Fatal(err)
				}
			}
			if app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
				t.Fatal("opening began an in-app viewer on Update")
			}
			runAppCommand(t, app, command)
			path := desktopAttachmentFile(t, output, false)
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content || filepath.Ext(path) != ".txt" {
				t.Fatalf("text launch lost source content or file association: %q %q %v", path, data, err)
			}
			if (path == ref.FilePath) != (kind == sessiondb.KindSelectedFile) {
				t.Fatal("selected local source/captured copy ownership is wrong")
			}
		})
	}
}

type foreignAttachmentMedia struct {
	workspace.SessionReferences
	data []byte
	read func(context.Context) error
}

func (s *foreignAttachmentMedia) ReadReferenceMedia(ctx context.Context, expected sessiondb.Reference) ([]byte, error) {
	if expected.FilePath != `C:\foreign-only\CON.png` {
		return nil, fmt.Errorf("wrong reference requested")
	}
	if s.read != nil {
		if err := s.read(ctx); err != nil {
			return nil, err
		}
	}
	return s.data, nil
}

func TestForeignAttachmentUsesOriginalMediaNeverLocalPath(t *testing.T) {
	output := recordDesktopLauncher(t)
	app := newTranscriptAttachmentApp(t)
	setAttachmentDestination(t, app, "system")
	ref := sessiondb.Reference{ID: "exact-remote-image", SessionID: app.ActiveSession.SessionID, Kind: sessiondb.KindPastedImage, Mime: "image/png", FilePath: `C:\foreign-only\CON.png`}
	store := &foreignAttachmentMedia{SessionReferences: app.RefStore(), data: attachmentImageBytes(t)}
	store.AddExisting(ref)
	app.ActiveSession.RefStore = store
	app.ws = &desktopFilesPort{fakeClientFiles: fakeClientFiles{Workspace: app.ws}}
	runAppCommand(t, app, app.openAttachment(ref.ID, false))
	path := desktopAttachmentFile(t, output, false)
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, store.data) || strings.Contains(path, "foreign-only") || filepath.Base(path) != "preview-CON.png" {
		t.Fatalf("foreign image was not transferred by exact reference: %q %v", path, err)
	}
}

func TestAttachmentPreparationRejectsStaleOwnersAndReference(t *testing.T) {
	output := recordDesktopLauncher(t)
	for _, boundary := range []string{"workspace", "session", "pane", "reference", "replace", "shutdown"} {
		t.Run(boundary, func(t *testing.T) {
			app := newTranscriptAttachmentApp(t)
			setAttachmentDestination(t, app, "system")
			ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "must not launch"})
			if err != nil {
				t.Fatal(err)
			}
			command := app.openAttachment(ref.ID, false)
			prepared := command().(attachmentPreparedMsg)
			switch boundary {
			case "workspace":
				app.beginWorkspaceSwitch()
			case "session":
				app.ActiveSession = workspace.NewSession("other", t.TempDir(), nil, stubLogger{})
			case "pane":
				app.AddOrchestrator("other-pane")
				app.switchToAgentView("other-pane")
			case "reference":
				if err := app.RefStore().DeleteReference(ref.ID); err != nil {
					t.Fatal(err)
				}
			case "replace":
				app.beginReferenceOverlay(ref.ID)
			case "shutdown":
				app.StopAsyncIO()
			}
			runAppCommand(t, app, app.applyAttachmentPrepared(prepared))
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("stale preparation launched an external app")
			}
		})
	}
}

func TestAttachmentRunningDownloadIsCancelledBeforeDesktopLaunch(t *testing.T) {
	output := recordDesktopLauncher(t)
	for _, boundary := range []string{"switch", "replace", "shutdown"} {
		t.Run(boundary, func(t *testing.T) {
			app := newTranscriptAttachmentApp(t)
			setAttachmentDestination(t, app, "system")
			started := make(chan struct{})
			store := &foreignAttachmentMedia{SessionReferences: app.RefStore(), read: func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}}
			ref := sessiondb.Reference{ID: "blocked", SessionID: app.ActiveSession.SessionID, Kind: sessiondb.KindPastedImage, FilePath: `C:\foreign-only\CON.png`}
			store.AddExisting(ref)
			app.ActiveSession.RefStore = store
			cmd := app.openAttachment(ref.ID, false)
			done := make(chan tea.Msg, 1)
			go func() { done <- cmd() }()
			<-started
			switch boundary {
			case "switch":
				app.beginWorkspaceSwitch()
			case "replace":
				app.openAttachment(ref.ID, false)
			case "shutdown":
				app.StopAsyncIO()
			}
			select {
			case result := <-done:
				msg := result.(attachmentPreparedMsg)
				if !errors.Is(msg.err, context.Canceled) {
					t.Fatalf("download survived owner: %v", msg.err)
				}
				runAppCommand(t, app, app.applyAttachmentPrepared(msg))
			case <-time.After(2 * time.Second):
				t.Fatal("owned download did not finish after cancellation")
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("cancelled download launched the desktop")
			}
		})
	}
}

func TestAttachmentContextMenuDestinationsIgnorePreference(t *testing.T) {
	output := recordDesktopLauncher(t)
	for _, action := range []string{"Open with default app", "Show in file manager", "Preview in Crude"} {
		t.Run(action, func(t *testing.T) {
			app := newTranscriptAttachmentApp(t)
			setAttachmentDestination(t, app, "crude")
			ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, Mime: "image/png", Data: attachmentImageBytes(t)})
			if err != nil {
				t.Fatal(err)
			}
			app.Input.SetInput(ref.Label)
			menu := app.buildAttachmentContextMenu(app.Input.RefRanges()[0])
			messages := make(chan tea.Msg, 2)
			restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { messages <- msg })
			defer restore()
			activateMenuItem(t, menu, action)
			select {
			case msg := <-messages:
				_, command := app.Update(msg)
				runAppCommand(t, app, command)
			case <-time.After(2 * time.Second):
				t.Fatal("explicit menu action did not complete")
			}
			if action == "Preview in Crude" {
				if !app.overlayHost.IsOpen(ui.OverlayTextOverlay) || !strings.Contains(app.View().RenderContent(), "▀") {
					t.Fatal("explicit native preview unavailable")
				}
			} else {
				path := desktopAttachmentFile(t, output, action == "Show in file manager")
				if _, err := os.Stat(path); err != nil || app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
					t.Fatal("explicit OS action followed the Crude preference")
				}
			}
		})
	}
}

func TestAttachmentLaunchRechecksCancellationAndReference(t *testing.T) {
	output := recordDesktopLauncher(t)
	for _, boundary := range []string{"reference", "switch", "replace"} {
		t.Run(boundary, func(t *testing.T) {
			app := newTranscriptAttachmentApp(t)
			setAttachmentDestination(t, app, "system")
			ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "never launch"})
			if err != nil {
				t.Fatal(err)
			}
			prepared := app.openAttachment(ref.ID, false)().(attachmentPreparedMsg)
			launch := app.applyAttachmentPrepared(prepared)
			switch boundary {
			case "reference":
				if err := app.RefStore().DeleteReference(ref.ID); err != nil {
					t.Fatal(err)
				}
			case "switch":
				app.beginWorkspaceSwitch()
			case "replace":
				app.beginReferenceOverlay(ref.ID)
			}
			runAppCommand(t, app, launch)
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("a queued stale launch opened the desktop")
			}
		})
	}
}
