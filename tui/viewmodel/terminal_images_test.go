package viewmodel

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"

	"crude/sessiondb"
	"crude/tui/ui"
	"crude/types"
	"crude/types/attachment"
)

func TestTerminalImageOwnerSwitchRemovesResourcesAndRejectsDecode(t *testing.T) {
	for _, loading := range []bool{false, true} {
		app, first, second, left, right := paneOwnershipSessions(t)
		app.terminalImages = terminalImageState{supported: true, cellWidth: 9, cellHeight: 24}
		app.focusMainGroup(left)
		ref, err := first.RefStore.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, Mime: "image/png", Data: attachmentImageBytes(t)})
		if err != nil {
			t.Fatal(err)
		}
		command := app.beginReferencePreview(ref, true)
		owner := app.referenceOverlay
		if !loading {
			runAppCommand(t, app, command)
			if len(app.View().Images) != 1 {
				t.Fatal("owner's prepared image is missing")
			}
		}
		app.focusMainGroup(right)
		if app.ActiveSession != second || len(app.View().Images) != 0 || owner.screen.IsOpen() || app.referenceOverlay != nil {
			t.Fatal("session/pane selection retained a foreign preview or image resource")
		}
		if loading {
			runAppCommand(t, app, command)
			if len(app.View().Images) != 0 || app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
				t.Fatal("previous session's late decode reopened its preview")
			}
		}
	}
}

func TestTerminalImageProbeFencesSupportAndGeometry(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	if command := app.probeTerminalImages(); command == nil || app.terminalImages.probeID == 0 {
		t.Fatal("terminal probe did not establish a reply owner")
	}
	id := app.terminalImages.probeID
	app.handleTerminalImageMessage(uv.KittyGraphicsEvent{Options: kitty.Options{ID: int(id ^ 1)}, Payload: []byte("OK")})
	if app.terminalImages.supported || app.terminalImages.probeID != id {
		t.Fatal("foreign response enabled terminal graphics")
	}
	app.handleTerminalImageMessage(terminalImageProbeTimeoutMsg{id: id ^ 1})
	if app.terminalImages.probeID != id {
		t.Fatal("stale timer cancelled a different terminal probe")
	}
	app.handleTerminalImageMessage(uv.KittyGraphicsEvent{Options: kitty.Options{ID: int(id)}, Payload: []byte("OK")})
	if !app.terminalImages.supported || app.terminalImages.probeID != 0 {
		t.Fatal("matching terminal response did not enable graphics")
	}
	app.handleTerminalImageMessage(terminalImageProbeTimeoutMsg{id: id})
	if !app.terminalImages.supported {
		t.Fatal("completed probe was disabled by its delayed timer")
	}
	app.handleTerminalImageMessage(uv.CellSizeEvent{Width: 9, Height: 24})
	if app.terminalImages.cellWidth != 9 || app.terminalImages.cellHeight != 24 {
		t.Fatal("terminal font geometry was ignored")
	}
	app.handleTerminalImageMessage(uv.CellSizeEvent{Width: -1, Height: 0})
	if app.terminalImages.cellWidth != 9 || app.terminalImages.cellHeight != 24 {
		t.Fatal("invalid geometry replaced the known terminal aspect")
	}
	for _, response := range []string{"EINVAL: unsupported virtual placement", "timeout"} {
		app.probeTerminalImages()
		id := app.terminalImages.probeID
		if response == "timeout" {
			app.handleTerminalImageMessage(terminalImageProbeTimeoutMsg{id: id})
		} else {
			app.handleTerminalImageMessage(uv.KittyGraphicsEvent{Options: kitty.Options{ID: int(id)}, Payload: []byte(response)})
		}
		if app.terminalImages.supported {
			t.Fatal("rejected/unresponsive terminal kept graphics enabled")
		}
	}
}

func TestTerminalImageRenderedAttachmentJourney(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	original := attachmentImageBytes(t)
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, Mime: "image/png", Data: original})
	if err != nil {
		t.Fatal(err)
	}
	app.appendActiveMessage(types.ChatMessage{InternalID: "graphics-post", Role: types.RoleUser, Content: ref.Label,
		Attachments: []attachment.Target{{Start: 0, End: len([]rune(ref.Label)), ReferenceID: ref.ID, Label: ref.Label}}})
	clickTranscriptAttachment(t, app, ref.Label)
	view := app.View()
	if len(view.Images) != 0 || !strings.ContainsRune(view.RenderContent(), '▀') {
		t.Fatal("unprobed terminal did not use the existing cell preview")
	}
	app.probeTerminalImages()
	id := app.terminalImages.probeID
	app.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: int(id)}, Payload: []byte("OK")})
	if len(app.View().Images) != 0 {
		t.Fatal("graphics used an invented font aspect before terminal measurement")
	}
	app.Update(uv.CellSizeEvent{Width: 9, Height: 24})
	view = app.View()
	if len(view.Images) != 1 || view.Images[0].Image == nil || !strings.ContainsRune(view.RenderContent(), kitty.Placeholder) || strings.ContainsRune(view.RenderContent(), '▀') {
		t.Fatal("real transcript attachment did not switch to original-pixel graphics")
	}
	resource := view.Images[0].Image
	originalColumns := view.Images[0].Columns
	app.frameGate.deferred, app.frameGate.due = true, false
	app.sessionTextOverlay().SetImageResource(nil)
	if deferred := app.View(); len(deferred.Images) != 1 || deferred.Images[0].Image != resource || !strings.ContainsRune(deferred.RenderContent(), kitty.Placeholder) {
		t.Fatal("coalesced cells lost their paired terminal image resource")
	}
	app.sessionTextOverlay().SetImageResource(resource)
	app.presentImmediateFrame()
	app.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	if len(app.View().Images) != 0 || !strings.ContainsRune(app.View().RenderContent(), '▀') {
		t.Fatal("downsampled image identity did not fall back safely")
	}
	app.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	if got := app.View().Images; len(got) != 1 || got[0].Image != resource {
		t.Fatal("true-color negotiation lost the already prepared image")
	}
	window := app.overlayHost.Window(ui.OverlayTextOverlay)
	window.SetPosition(3, 2)
	window.MarkContentDirty()
	moved := app.View()
	if len(moved.Images) != 1 || moved.Images[0].Image != resource {
		t.Fatal("moving a window reconstructed its immutable image")
	}
	// Resizing the window must republish virtual geometry while retaining the
	// original PNG, not resample/encode it during the UI callback.
	w, h := window.Size()
	x, y := window.Position()
	app.Update(tea.MouseClickMsg{X: x + w - 1, Y: y + h - 1, Button: tea.MouseLeft})
	app.Update(tea.MouseMotionMsg{X: x + w - 25, Y: y + h - 7, Button: tea.MouseLeft})
	app.Update(tea.MouseReleaseMsg{X: x + w - 25, Y: y + h - 7, Button: tea.MouseLeft})
	resized := app.View()
	if len(resized.Images) != 1 || resized.Images[0].Image != resource || resized.Images[0].Columns >= originalColumns {
		t.Fatalf("pointer resize did not refit the retained image: %+v", resized.Images)
	}
	// Shrinking the outer terminal clips the existing window through ordinary
	// composition, without replacing or encoding its image resource.
	app.Update(tea.WindowSizeMsg{Width: 54, Height: 20})
	clipped := app.View()
	if len(clipped.Images) != 1 || clipped.Images[0].Image != resource || clipped.Surface.Bounds().Dx() != 54 || clipped.Surface.Bounds().Dy() != 20 {
		t.Fatalf("narrow terminal did not clip the retained image: %+v", clipped.Images)
	}
	_, closeCommand := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, app, closeCommand)
	closed := app.View()
	if len(closed.Images) != 0 || strings.ContainsRune(closed.RenderContent(), kitty.Placeholder) || app.Input.Value() != "KEEP MY COMPOSER DRAFT" {
		t.Fatal("closing image left image cells/resources or consumed the composer")
	}
	// A late async decode may not resurrect the preview after close.
	command := app.beginReferencePreview(ref, true)
	state := app.referenceOverlay
	app.overlayHost.Close(ui.OverlayTextOverlay)
	runAppCommand(t, app, command)
	app.applyReferenceContent(referenceContentLoadedMsg{owner: state, image: image.NewNRGBA(image.Rect(0, 0, 1, 1)), resource: resource})
	if len(app.View().Images) != 0 || app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("closed-owner image result reopened terminal graphics")
	}
}

func TestTerminalImagePreparationNormalizesOtherFormatsOffUI(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, image.NewNRGBA(image.Rect(0, 0, 20, 10)), nil); err != nil {
		t.Fatal(err)
	}
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, Mime: "image/jpeg", Data: jpegData.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	state := &referenceOverlayState{ref: ref, store: app.RefStore(), ctx: context.Background()}
	result := loadReferencePreview(state)
	if result.err != nil || result.resource == nil || result.image == nil || result.image.Bounds().Dx() != 20 {
		t.Fatalf("JPEG was not decoded/prepared for native graphics: %v", result.err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state.ctx = ctx
	result = loadReferencePreview(state)
	if result.err == nil || result.resource != nil || result.image != nil {
		t.Fatal("cancelled owner prepared terminal image data")
	}
}
