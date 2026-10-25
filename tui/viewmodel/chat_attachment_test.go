package viewmodel

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/history"
	"crude/provider"
	"crude/sessiondb"
	"crude/tui/ui"
	"crude/types"
	"crude/types/attachment"
	"crude/workspace"
)

func newTranscriptAttachmentApp(t *testing.T) *App {
	t.Helper()
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.width, app.height = 120, 36
	app.AddOrchestrator("attachment-owner")
	app.ActiveSession = workspace.NewSession("attachment-owner", t.TempDir(), nil, stubLogger{})
	app.maybeHydrateComposerDraft()
	app.Input.SetInput("KEEP MY COMPOSER DRAFT")
	return app
}

func clickTranscriptAttachment(t *testing.T, app *App, label string) {
	t.Helper()
	x, y := attachmentTranscriptPoint(t, app, label)
	_, press := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, release := app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, press)
	runAppCommand(t, app, release)
}

func retainedAttachmentRows(t *testing.T, ref sessiondb.Reference, text string) []workspace.ChatMessageView {
	t.Helper()
	dir := t.TempDir()
	db := sessiondb.OpenAndMigrateAt(dir)
	if err := sessiondb.CreateSessionRow(db, "attachment-owner"); err != nil {
		t.Fatal(err)
	}
	input := provider.NewTextMessage("user", text)
	input.InternalID = "historical-input"
	input.Origin = provider.OriginHuman
	meta := sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, DisplayContent: text, ReferenceLabels: []string{ref.DisplayName()}, Snapshots: []types.ConversationMessage{{InternalID: input.InternalID, Role: types.RoleUser, Origin: provider.OriginHuman, Content: text, ReferenceLabels: []string{ref.DisplayName()}}}}
	if err := sessiondb.AppendMessageWithMeta(db, "attachment-owner", input, 1, meta); err != nil {
		t.Fatal(err)
	}
	selection := provider.NewMultiPartMessage("user", []provider.ContentPart{{Type: "text", Text: ref.AgentText()}})
	selection.InternalID = input.InternalID + "-references"
	selection.Origin, selection.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
	if err := sessiondb.AppendMessageWithMeta(db, "attachment-owner", selection, 1, sessiondb.DisplayMeta{ContextOnly: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = sessiondb.OpenAndMigrateAt(dir)
	defer db.Close()
	rows, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	return workspace.ConvertMessagesForDisplay(rows)
}

func TestHistoricalAttachmentSQLiteReopenRenderedClickUsesRetainedID(t *testing.T) {
	for _, width := range []int{54, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app := newTranscriptAttachmentApp(t)
			app.width = width
			ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "EXACT HISTORICAL CONTENT"})
			if err != nil {
				t.Fatal(err)
			}
			// Reuse the original label for unrelated current content. A label lookup
			// would open the wrong attachment; the retained header names the old ID.
			if _, ok := app.RefStore().RenameReference(ref.ID, "[renamed historical attachment]"); !ok {
				t.Fatal("rename failed")
			}
			if _, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Label: ref.Label, Content: "WRONG LABEL-COLLISION CONTENT"}); err != nil {
				t.Fatal(err)
			}
			views := retainedAttachmentRows(t, ref, "Inspect "+ref.Label)
			if len(views) != 1 || len(views[0].Attachments) != 1 || views[0].Attachments[0].ReferenceID != ref.ID {
				t.Fatalf("lost retained source identity: %+v", views)
			}
			app.hydrateActiveMessages(ChatMessagesFromViews(views))
			clickTranscriptAttachment(t, app, ref.Label)
			if screen := app.sessionTextOverlay(); !screen.ReadOnly() || screen.Value() != ref.Content {
				t.Fatalf("history opened wrong or editable content: %q", screen.Value())
			}
			_, _ = app.Update(tea.PasteMsg{Content: "MUTATION"})
			if app.sessionTextOverlay().Value() != ref.Content {
				t.Fatal("history preview mutated")
			}
			_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			if app.Input.Value() != "KEEP MY COMPOSER DRAFT" {
				t.Fatal("preview changed composer")
			}
		})
	}
}

func TestPostedAttachmentDragLiteralForeignAndStaleOwners(t *testing.T) {
	for _, scenario := range []string{"drag-back", "shift-selection", "literal", "foreign", "closed", "workspace", "session", "pane"} {
		t.Run(scenario, func(t *testing.T) {
			app := newTranscriptAttachmentApp(t)
			ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "PRIVATE CONTENT"})
			if err != nil {
				t.Fatal(err)
			}
			text := "Inspect " + ref.Label
			targets := attachment.Locate(text, []attachment.Target{{ReferenceID: ref.ID, Label: ref.Label}})
			if scenario == "literal" {
				targets = nil
			}
			if scenario == "foreign" {
				ref.SessionID = "foreign-session"
				app.RefStore().AddExisting(ref)
			}
			app.appendActiveMessage(types.ChatMessage{InternalID: "negative", Role: types.RoleUser, Content: text, Attachments: targets})
			x, y := attachmentTranscriptPoint(t, app, ref.Label)
			mod := tea.KeyMod(0)
			if scenario == "shift-selection" {
				mod = tea.ModShift
			}
			_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft, Mod: mod})
			if scenario == "drag-back" {
				_, _ = app.Update(tea.MouseMotionMsg{X: x + 4, Y: y, Button: tea.MouseLeft})
				_, _ = app.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
			}
			_, command := app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			if scenario == "drag-back" || scenario == "shift-selection" || scenario == "literal" {
				runAppCommand(t, app, command)
				if app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
					t.Fatal("selection or literal prose opened attachment")
				}
				return
			}
			if scenario == "foreign" {
				runAppCommand(t, app, command)
				if !strings.Contains(app.sessionTextOverlay().Value(), "unavailable") || strings.Contains(app.sessionTextOverlay().Value(), ref.Content) {
					t.Fatal("foreign reference leaked")
				}
				return
			}
			screen := app.sessionTextOverlay()
			switch scenario {
			case "closed":
				app.overlayHost.Close(ui.OverlayTextOverlay)
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			case "session":
				app.ActiveSession = workspace.NewSession("new-session", t.TempDir(), nil, stubLogger{})
			case "pane":
				app.AddOrchestrator("other-pane")
				app.switchToAgentView("other-pane")
			}
			runAppCommand(t, app, command)
			if screen.Value() == ref.Content {
				t.Fatal("stale result populated changed owner")
			}
		})
	}
}

func TestHistoricalImageRenderedClickUsesNativePixels(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(40 + x*50), G: uint8(y * 240), B: 120, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, Mime: "image/png", Data: data.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	views := retainedAttachmentRows(t, ref, "Inspect "+ref.Label)
	app.hydrateActiveMessages(ChatMessagesFromViews(views))
	clickTranscriptAttachment(t, app, ref.Label)
	if !app.sessionTextOverlay().ReadOnly() || !strings.Contains(app.View().RenderContent(), "▀") {
		t.Fatal("historical image opened no actual native pixels")
	}
}

func TestPostedFileBackedPastedTextIsReadOnlyNativeContent(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	path := t.TempDir() + "/retained.txt"
	content := strings.Repeat("Full original text retained without truncation\n", 2000) + "EXACT END"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedTextFile, FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	app.appendActiveMessage(types.ChatMessage{InternalID: "file-post", Role: types.RoleUser, Content: ref.Label, Attachments: []attachment.Target{{Start: 0, End: len([]rune(ref.Label)), ReferenceID: ref.ID, Label: ref.Label}}})
	clickTranscriptAttachment(t, app, ref.Label)
	if screen := app.sessionTextOverlay(); !screen.ReadOnly() || screen.Value() != content {
		t.Fatal("file-backed sent text opened externally, truncated or editable")
	}
}

func TestPostedImageNativePreviewPreservesActualBytes(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	img.Set(1, 0, color.NRGBA{G: 255, A: 255})
	img.Set(0, 1, color.NRGBA{B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, Mime: "image/png", Data: encoded.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	app.appendActiveMessage(types.ChatMessage{InternalID: "image-post", Role: types.RoleUser, Content: ref.Label, Attachments: []attachment.Target{{Start: 0, End: len([]rune(ref.Label)), ReferenceID: ref.ID, Label: ref.Label}}})
	clickTranscriptAttachment(t, app, ref.Label)
	if screen := app.sessionTextOverlay(); !screen.ReadOnly() || screen.RefID() != ref.ID || screen.Value() != "" {
		t.Fatal("image did not open native image body")
	}
	if !strings.Contains(app.View().RenderContent(), "▀") {
		t.Fatal("native preview never painted image pixels")
	}
	raw, err := os.ReadFile(ref.FilePath)
	if err != nil || !bytes.Equal(raw, encoded.Bytes()) {
		t.Fatal("preview changed captured image bytes")
	}
}

func TestPromptHistoryInlineAttachmentUsesSameReadOnlyViewer(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	app.SetPromptHistory(history.New())
	ref := sessiondb.Reference{ID: "actual-old-id", SessionID: "old-session", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", Content: "history snapshot content"}
	app.Prompts.Record(ref.Label, time.Now(), history.Attachment{Start: 0, End: len(ref.Label), Reference: ref})
	entry := app.Prompts.Entries()[0]
	app.renderPromptHistory()
	command := app.openHistoricalAttachment(entry.ID + ":0")
	runAppCommand(t, app, command)
	if screen := app.sessionTextOverlay(); !screen.ReadOnly() || screen.Value() != ref.Content {
		t.Fatalf("inline history preview = %q", screen.Value())
	}
	if app.overlayHost.IsOpen(ui.OverlayPromptHistory) {
		t.Fatal("history modal shields native preview")
	}
}

func attachmentTranscriptPoint(t *testing.T, app *App, label string) (int, int) {
	t.Helper()
	app.paintShellFrame()
	buf := app.mainSurf.Buffer()
	for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H; y++ {
		var line strings.Builder
		for x := app.chatRect.X; x < app.chatRect.X+app.chatRect.W; x++ {
			ch := buf.Get(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			line.WriteRune(ch)
		}
		if at := strings.Index(line.String(), label); at >= 0 {
			return app.chatRect.X + at + 1, y
		}
	}
	t.Fatalf("attachment %q not painted in transcript %+v", label, app.chatRect)
	return 0, 0
}

func TestPostedAttachmentRenderedClickOpensNativePreview(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.width, app.height = 120, 36
	app.AddOrchestrator("attachment-owner")
	app.ActiveSession = workspace.NewSession("attachment-owner", t.TempDir(), nil, stubLogger{})
	app.maybeHydrateComposerDraft()
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "retained attachment content"})
	if err != nil {
		t.Fatal(err)
	}
	state := app.ws.AgentMgr().Get("attachment-owner").State
	state.Messages.AppendMessage(workspace.Message{InternalID: "posted-attachment", Role: types.RoleUser,
		Content: "Inspect " + ref.DisplayName(), ReferenceLabels: []string{ref.DisplayName()}, Attachments: attachment.Locate("Inspect "+ref.DisplayName(), []attachment.Target{{ReferenceID: ref.ID, Label: ref.DisplayName()}})})
	app.syncAgentMessages("attachment-owner", false)
	x, y := attachmentTranscriptPoint(t, app, ref.DisplayName())
	_, press := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("transcript preview opened before release")
	}
	_, release := app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, press)
	runAppCommand(t, app, release)
	if !app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("rendered posted attachment click did not open the native reference viewer")
	}
	if screen := app.sessionTextOverlay(); !screen.ReadOnly() || screen.Value() != ref.Content {
		t.Fatalf("posted attachment is not an immutable complete preview: readonly=%v value=%q", screen.ReadOnly(), screen.Value())
	}
}
