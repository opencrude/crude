package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/ui"
)

func TestOpenHelpOpensModalWithoutChatDump(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	app.openHelp()
	if app.overlayHost.Window(ui.OverlayHelp) == nil {
		t.Fatal("help overlay must open")
	}
	if app.helpScreen == nil || !app.helpScreen.IsOpen() {
		t.Fatal("help screen was not opened")
	}

	host := ui.NewHost(app.Theme, app.helpScreen)
	host.Resize(72, 20)
	host.Layout()
	buf := surface.NewCellBuffer(72, 20)
	host.Paint(buf)
	var b strings.Builder
	for y := 0; y < 20; y++ {
		for x := 0; x < 72; x++ {
			if c := buf.Get(x, y); c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		b.WriteByte('\n')
	}
	text := b.String()
	if !strings.Contains(text, "Using Crude") || !strings.Contains(text, "Search help") {
		t.Fatalf("help overlay missing usage content:\n%s", text)
	}

	app.Logger = stubLogger{}
	app.executeSlashCommand("/help")
	if app.overlayHost.Window(ui.OverlayHelp) == nil {
		t.Fatal("/help should keep or reopen the help overlay")
	}
}

func TestHelpF1TogglesOverlay(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyF1, Text: "f1"})
	if app.overlayHost.Window(ui.OverlayHelp) == nil {
		t.Fatal("F1 should open help")
	}
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyF1, Text: "f1"})
	if app.overlayHost.Window(ui.OverlayHelp) != nil {
		t.Fatal("F1 should close help when already open")
	}
}

func TestHelpEscClosesOverlay(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	app.openHelp()
	_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			app.Update(msg)
		}
	}
	if app.overlayHost.Window(ui.OverlayHelp) != nil {
		t.Fatal("Esc should close help")
	}
}
