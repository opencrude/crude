package viewmodel

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"

	"crude/coreclient"
	"crude/provider"
	"crude/sessiondb"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// Submit through the actual connected composer and core, then preview from a
// newly attached viewer. This covers canonical admission, wire projection and
// hydration rather than seeding the new attachment fields in a UI fixture.
func TestConnectedPostedAttachmentsSurviveViewerReattach(t *testing.T) {
	var calls atomic.Int32
	output := recordDesktopLauncher(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"connected attachments accepted\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider.SetModelCap("test-model", true, true)
	runtime := remoteJourneyRuntime(t, server.URL, func(ws *workspace.Workspace) {
		app := ws.AppDefaults()
		app.UI.AttachmentOpen = types.AttachmentOpenCrude
		ws.SetAppDefaults(app)
	})
	root := t.TempDir()
	first := newRemoteJourneyShell(t, runtime, root)
	agentID := remoteJourneyCreate(t, first)
	app := first.app
	if _, ok := app.RefStore().(*coreclient.References); !ok {
		t.Fatal("journey is not using the connected reference owner")
	}
	const content = "Complete connected attachment content\n\nEXACT CONNECTED TAIL"
	text, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 1, color.NRGBA{B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	media, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, Mime: "image/png", Data: encoded.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	app.Input.SetInput("Inspect " + text.DisplayName() + " and " + media.DisplayName())
	app.Input.UseReferenceIDs([]string{text.ID, media.ID})
	app.setFocusInput()
	_, command := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, command)
	remoteJourneyUntil(t, []remoteJourneyShell{first}, func() bool {
		return strings.Contains(app.View().RenderContent(), "connected attachments accepted")
	})

	second := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyUntil(t, []remoteJourneyShell{first, second}, func() bool {
		return second.app.AgentView.ActiveID() == agentID && strings.Contains(second.app.View().RenderContent(), "connected attachments accepted")
	})
	second.app.Input.SetInput("PEER DRAFT MUST SURVIVE PREVIEWS")
	clickTranscriptAttachment(t, second.app, text.DisplayName())
	if screen := second.app.sessionTextOverlay(); !screen.ReadOnly() || screen.Value() != content || screen.RefID() != text.ID {
		t.Fatalf("reattached transcript preview: id=%q want=%q readonly=%v content=%q", screen.RefID(), text.ID, screen.ReadOnly(), screen.Value())
	}
	if retained, ok := second.app.RefStore().GetReference(text.ID); !ok || retained.Content != content {
		t.Fatal("reading a transfer projection replaced the canonical reference")
	}
	_, _ = second.app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	clickTranscriptAttachment(t, second.app, media.DisplayName())
	if screen := second.app.sessionTextOverlay(); !screen.ReadOnly() || screen.RefID() != media.ID || screen.Value() != "" {
		t.Fatalf("reattached image preview: id=%q want=%q readonly=%v value=%q open=%v", screen.RefID(), media.ID, screen.ReadOnly(), screen.Value(), screen.IsOpen())
	}
	// NewApp animates popup reveal on ordinary product ticks (local stand-in
	// fixtures do not). Drive that real lifecycle, then inspect its presented
	// body rather than the initial collapsed animation frame.
	remoteJourneyUntil(t, []remoteJourneyShell{first, second}, func() bool {
		_, _ = second.app.Update(types.TickMsg(time.Now()))
		return strings.Contains(second.app.View().RenderContent(), "▀")
	})
	second.app.probeTerminalImages()
	second.app.Update(uv.CellSizeEvent{Width: 9, Height: 24})
	second.app.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: int(second.app.terminalImages.probeID)}, Payload: []byte("OK")})
	if view := second.app.View(); len(view.Images) != 1 || !strings.ContainsRune(view.RenderContent(), kitty.Placeholder) || strings.ContainsRune(view.RenderContent(), '▀') {
		t.Fatal("reattached connected media did not use native terminal graphics")
	}
	_, _ = second.app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if second.app.Input.Value() != "PEER DRAFT MUST SURVIVE PREVIEWS" || calls.Load() != 1 {
		t.Fatal("attachment preview consumed a draft or replayed model work")
	}
	reader := second.app.RefStore().(*coreclient.References)
	got, err := reader.ReadReferenceMedia(t.Context(), media)
	if err != nil || !bytes.Equal(got, encoded.Bytes()) {
		t.Fatalf("native preview changed connected image bytes: %v", err)
	}

	// A real Settings save must republish this ordinary sparse preference, then
	// the very same hydrated link opens in the OS without a restart or new turn.
	second.app.openWorkspaceSettings()
	body := second.app.settingsSurface(second.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	model := body.Model()
	for index, category := range model.Categories() {
		if category.Form != nil && category.Form.Field("attachment-open") != nil {
			model.SelectCategory(index)
			model.SetValueForTest(index, "attachment-open", types.AttachmentOpenSystem)
		}
	}
	save := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
	if save.Cmd == nil {
		t.Fatal("attachment preference did not prepare Save")
	}
	saveMsg, ok := save.Cmd().(settingsui.SaveMsg)
	if !ok {
		t.Fatal("attachment preference emitted no ordinary settings save")
	}
	runAppCommand(t, second.app, func() tea.Msg { return saveMsg })
	remoteJourneyUntil(t, []remoteJourneyShell{first, second}, func() bool {
		return first.app.ws.LoadAppSettings().UI.AttachmentOpen == types.AttachmentOpenSystem && !body.Model().Saving()
	})
	second.app.overlayHost.Close(ui.OverlayWorkspaceSettings)
	clickTranscriptAttachment(t, second.app, media.DisplayName())
	path := desktopAttachmentFile(t, output, false)
	copy, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(copy, encoded.Bytes()) || path == media.FilePath || second.app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatalf("connected System app destination lost original media or still opened native preview: %q %v", path, err)
	}
	third := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyUntil(t, []remoteJourneyShell{first, second, third}, func() bool {
		// Registry selection and settings are independent of transcript
		// hydration. A human cannot click the reattached link until it paints.
		return third.app.AgentView.ActiveID() == agentID &&
			third.app.ws.LoadAppSettings().UI.AttachmentOpen == types.AttachmentOpenSystem &&
			strings.Contains(third.app.View().RenderContent(), media.DisplayName())
	})
	clickTranscriptAttachment(t, third.app, media.DisplayName())
	if third.app.overlayHost.IsOpen(ui.OverlayTextOverlay) || calls.Load() != 1 || second.app.Input.Value() != "PEER DRAFT MUST SURVIVE PREVIEWS" {
		t.Fatal("saved OS destination did not survive reattach or replayed inference/consumed draft")
	}

	// History media belongs to the global immutable snapshot, not to this new
	// session's reference namespace. Import/open only the selected image.
	remoteJourneyCreate(t, third)
	third.app.Input.SetInput("NEW SESSION DRAFT")
	runAppCommand(t, third.app, third.app.loadRemotePromptHistory())
	key := ""
	for _, entry := range third.app.Prompts.Entries() {
		for index, selected := range entry.Attachments {
			if selected.Reference.ID == media.ID {
				key = entry.ID + ":" + strconv.Itoa(index)
			}
		}
	}
	if key == "" {
		t.Fatal("connected history did not retain the submitted image")
	}
	third.app.renderPromptHistory()
	runAppCommand(t, third.app, third.app.openHistoricalAttachment(key))
	path = desktopAttachmentFile(t, output, false)
	copy, err = os.ReadFile(path)
	imported := third.app.RefStore().ListReferences()
	if err != nil || !bytes.Equal(copy, encoded.Bytes()) || len(imported) != 1 || imported[0].ID == media.ID || third.app.Input.Value() != "NEW SESSION DRAFT" || calls.Load() != 1 || third.app.overlayHost.IsOpen(ui.OverlayPromptHistory) {
		t.Fatalf("connected history did not open just the exact immutable image without changing the draft: refs=%+v err=%v", imported, err)
	}
}
