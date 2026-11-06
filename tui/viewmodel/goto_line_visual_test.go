package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// This fixture mounts a real shell with one long file open and focused, for
// the repository capture workflow (scripts/tuishot.sh), which presses the real
// keys and screenshots the pointer-free go-to-line flow. F9 exits.
func TestGoToLineVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_GOTOLINE") != "1" {
		t.Skip("dedicated sequential native capture")
	}
	app, _ := newRealShellApp(t)
	lines := make([]string, 0, 240)
	for i := 1; i <= 240; i++ {
		lines = append(lines, fmt.Sprintf("line %03d  // go to line target %03d", i, i))
	}
	path := filepath.Join(app.ws.RootPath(), "capture.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	app.registerOverlayHandlers()
	app.onboarding.trust.askedOwner = app.ws
	app.onboarding.trust.askedGen = app.workspaceSwitchGen.Load()
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	app.setFocusEditor()
	_ = app.View()

	program := tea.NewProgram(&visualAcceptanceModel{App: app}, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}
