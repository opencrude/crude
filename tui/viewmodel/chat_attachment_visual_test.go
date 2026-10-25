package viewmodel

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/sessiondb"
	"crude/types"
	"crude/types/attachment"
)

// Opt-in fixture for scripts/tuishot.sh with the compiled viewmodel test binary:
// CRUDE_VISUAL_CHAT_ATTACHMENTS=1 ... -test.run=^TestChatAttachmentVisualAcceptance$
// F2 composer text (editable), F3 posted text (Close), F4 posted image,
// F5 cold SQLite historical text (Close), F9 quit. Esc closes each preview.
// Normal pointer clicks still enter through the actual painted App transcript.
func TestChatAttachmentVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_CHAT_ATTACHMENTS") != "1" {
		t.Skip("requires the repository terminal capture workflow")
	}
	app := newTranscriptAttachmentApp(t)
	text, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "Complete retained diagnostic\n\nThe composer version is editable. Posted and historical versions are immutable previews with Close.\n\nEXACT CONTENT TAIL"})
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 3), G: uint8(y * 6), B: uint8(255 - x*2), A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	media, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, Mime: "image/png", Data: data.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	posted := "Posted attachments: " + text.Label + " and " + media.Label
	app.appendActiveMessage(types.ChatMessage{InternalID: "visual-posted", Role: types.RoleUser, Content: posted, Attachments: attachment.Locate(posted, []attachment.Target{{ReferenceID: text.ID, Label: text.Label}, {ReferenceID: media.ID, Label: media.Label}})})
	historical, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "COLD SQLITE HISTORICAL CONTENT\n\nThis identity came from the retained input preparation, not a guessed session label."})
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range retainedAttachmentRows(t, historical, "Historical attachment: "+historical.Label) {
		app.appendActiveMessage(chatMessageFromCanonical(view))
	}
	app.Input.SetInput("Composer draft retained underneath " + text.Label + " " + media.Label)
	model := &attachmentVisualModel{App: app, t: t, text: text, image: media, historical: historical}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type attachmentVisualModel struct {
	*App
	t                       *testing.T
	text, image, historical sessiondb.Reference
}

func (m *attachmentVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *attachmentVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			return m, m.beginReferenceOverlay(m.text.ID)
		case tea.KeyF3:
			return m, m.click(m.text.Label)
		case tea.KeyF4:
			return m, m.click(m.image.Label)
		case tea.KeyF5:
			return m, m.click(m.historical.Label)
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, command := m.App.Update(msg)
	return m, command
}
func (m *attachmentVisualModel) click(label string) tea.Cmd {
	x, y := attachmentTranscriptPoint(m.t, m.App, label)
	_, press := m.App.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, release := m.App.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	return tea.Batch(press, release)
}
