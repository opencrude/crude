package viewmodel

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"github.com/charmbracelet/x/ansi"
)

type slashPointerLogger struct {
	stubLogger
	commands []string
}

func (l *slashPointerLogger) Info(tag, message string, pairs ...any) {
	if tag != "INPUT" || message != "slash_command" {
		return
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] == "cmd" {
			if command, ok := pairs[i+1].(string); ok {
				l.commands = append(l.commands, command)
			}
		}
	}
}

func renderedSlashPoint(t *testing.T, app *App, label string) (x, y int, found bool) {
	t.Helper()
	r, ok := app.rootHost.ScreenRect(app.inputScreen.ID())
	if !ok || r.Empty() {
		t.Fatal("composer is not mounted in the rendered root Host")
	}
	for y = r.Y; y < r.Y+r.H; y++ {
		var row strings.Builder
		for x = r.X; x < r.X+r.W; x++ {
			c := app.mainSurf.Buffer().Get(x, y)
			if c.Char != 0 {
				row.WriteRune(c.Char)
			} else {
				row.WriteByte(' ')
			}
		}
		line := row.String()
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == label {
			x = r.X + strings.Index(line, label)
			if hit := app.rootHost.HitTest(x, y); hit != app.inputScreen.ID() {
				t.Fatalf("rendered suggestion hits %q, not the composer", hit)
			}
			return x, y, true
		}
	}
	return 0, 0, false
}

func slashAppEvent(t *testing.T, app *App, event tea.Msg) {
	t.Helper()
	model, command := app.Update(event)
	if model != app {
		t.Fatalf("input replaced App with %T", model)
	}
	runAppCommand(t, app, command)
}

func TestSlashPointerThroughAppActivatesExactRenderedCommand(t *testing.T) {
	for _, test := range []struct {
		name, query, label, visible string
		width, height               int
		scroll                      bool
		overlay                     ui.OverlayID
	}{
		{"different suggestion", "/", "sessions", "No sessions yet", 112, 40, false, ui.OverlaySessionPicker},
		{"filtered suggestion", "/se", "sessions", "No sessions yet", 112, 40, false, ui.OverlaySessionPicker},
		{"scrolled suggestion", "/", "skills", "Skills", 112, 40, true, ui.OverlayCapabilityPicker},
		{"constrained composer", "/skills", "skills", "Skills", 64, 20, false, ui.OverlayCapabilityPicker},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := setupChatInputApp(t, test.width, test.height, test.width > 80)
			logger := &slashPointerLogger{}
			wsOf(app).Logger = logger
			for _, r := range test.query {
				slashAppEvent(t, app, tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			app.paintShellFrame()
			if test.scroll {
				if _, _, found := renderedSlashPoint(t, app, test.label); found {
					t.Fatal("scrolled command is already in the initial viewport")
				}
				x, y, found := renderedSlashPoint(t, app, "plan")
				if !found {
					t.Fatal("initial suggestions were not rendered")
				}
				for i := 0; i < 2; i++ {
					slashAppEvent(t, app, tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
					app.paintShellFrame()
				}
			}
			x, y, found := renderedSlashPoint(t, app, test.label)
			if !found {
				t.Fatalf("target %q was not rendered", test.label)
			}
			revision := app.Input.DraftRevision()
			slashAppEvent(t, app, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if !slices.Equal(logger.commands, []string{"/" + test.label}) {
				t.Fatalf("click executed %v, want exactly /%s", logger.commands, test.label)
			}
			window := app.overlayHost.Window(test.overlay)
			if window == nil {
				t.Fatalf("/%s did not open its product destination", test.label)
			}
			if app.Input.Value() != "" || app.Input.SlashActive() || app.Input.CursorFlat() != 0 || app.Input.InputSelecting() || app.Input.DraftRevision() <= revision {
				t.Fatal("pointer command did not consume and fence the slash draft")
			}
			app.forceFrame()
			if rendered := ansi.Strip(app.View().Content); !strings.Contains(rendered, test.visible) {
				t.Fatalf("/%s destination was not painted:\n%s", test.label, rendered)
			}
			slashAppEvent(t, app, tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			if len(logger.commands) != 1 || app.overlayHost.Window(test.overlay) != window {
				t.Fatal("release executed or reopened the command a second time")
			}
		})
	}
}

func TestSlashAppKeyboardPointerParity(t *testing.T) {
	for _, path := range []string{"keyboard", "pointer"} {
		t.Run(path, func(t *testing.T) {
			app := setupChatInputApp(t, 100, 32, false)
			logger := &slashPointerLogger{}
			wsOf(app).Logger = logger
			app.Input.SetInput("/sessions")
			app.Input.SetSelection(1, 3)
			app.paintShellFrame()
			if path == "keyboard" {
				slashAppEvent(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
			} else {
				x, y, found := renderedSlashPoint(t, app, "sessions")
				if !found {
					t.Fatal("sessions suggestion was not rendered")
				}
				slashAppEvent(t, app, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			}
			if !slices.Equal(logger.commands, []string{"/sessions"}) || !app.overlayHost.IsOpen(ui.OverlaySessionPicker) {
				t.Fatalf("%s did not execute /sessions once: %v", path, logger.commands)
			}
			if app.Input.Value() != "" || app.Input.SlashActive() || app.Input.InputSelecting() || app.Input.CursorFlat() != 0 {
				t.Fatalf("%s retained the consumed slash draft", path)
			}
		})
	}
}

func TestSlashAppIgnoresRightClickAndComposerBlank(t *testing.T) {
	for _, point := range []string{"right click", "separator", "padding"} {
		t.Run(point, func(t *testing.T) {
			app := setupChatInputApp(t, 112, 40, true)
			logger := &slashPointerLogger{}
			wsOf(app).Logger = logger
			app.Input.SetInput("/se")
			app.Input.SetSelection(1, 2)
			app.paintShellFrame()
			x, y, found := renderedSlashPoint(t, app, "sessions")
			if !found {
				t.Fatal("sessions suggestion was not rendered")
			}
			button := tea.MouseLeft
			r, _ := app.rootHost.ScreenRect(app.inputScreen.ID())
			switch point {
			case "right click":
				button = tea.MouseRight
			case "separator":
				y = r.Y
			case "padding":
				x = r.X
			}
			value, cursor, selection, revision := app.Input.Value(), app.Input.CursorFlat(), app.Input.SelectedText(), app.Input.DraftRevision()
			slashAppEvent(t, app, tea.MouseClickMsg{X: x, Y: y, Button: button})
			slashAppEvent(t, app, tea.MouseReleaseMsg{X: x, Y: y, Button: button})
			if len(logger.commands) != 0 || app.overlayHost.IsOpen(ui.OverlaySessionPicker) {
				t.Fatal("nonactivation executed a slash command")
			}
			if app.Input.Value() != value || app.Input.CursorFlat() != cursor || app.Input.SelectedText() != selection || app.Input.DraftRevision() != revision || !app.Input.SlashActive() {
				t.Fatal("nonactivation changed the composer draft")
			}
		})
	}
}
