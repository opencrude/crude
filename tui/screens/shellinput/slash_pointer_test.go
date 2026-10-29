package shellinput

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/input"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func slashHost(t *testing.T, query string, extraRows int) (*input.Model, *ui.Host, *surface.CellBuffer) {
	t.Helper()
	th := theme.DefaultDark()
	model := input.NewModel(th)
	model.SetInput(query)
	model.UseReferenceIDs([]string{"unresolved-draft-reference"})
	screen := New(th)
	screen.Bind(&model)
	host := ui.NewHost(th, screen)
	height := screen.ContentHeight(72) + extraRows
	host.Resize(72, height)
	host.Layout()
	buf := surface.NewCellBuffer(72, height)
	host.Paint(buf)
	return &model, host, buf
}

func slashLabelPoint(buf *surface.CellBuffer, label string) (x, y int, found bool) {
	for y = 0; y < buf.Height(); y++ {
		line := PlainRow(buf, y, buf.Width())
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == label {
			return strings.Index(line, label), y, true
		}
	}
	return 0, 0, false
}

func TestSlashPointerActivatesRenderedSuggestion(t *testing.T) {
	for _, test := range []struct {
		name, query, label string
		scroll             bool
	}{
		{"different row", "/", "sessions", false},
		{"filtered row", "/se", "sessions", false},
		{"filtered loop", "/lo", "loop", false},
		{"scrolled row", "/", "skills", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			model, host, buf := slashHost(t, test.query, 0)
			if test.scroll {
				if _, _, found := slashLabelPoint(buf, test.label); found {
					t.Fatal("scrolled target must start outside the rendered viewport")
				}
				x, y, found := slashLabelPoint(buf, "plan")
				if !found {
					t.Fatal("initial slash suggestions were not painted")
				}
				for i := 0; i < 2; i++ {
					result := host.Handle(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
					if !result.Handled || result.Action != nil {
						t.Fatalf("wheel must only browse suggestions: %+v", result)
					}
				}
				host.Paint(buf)
			}
			x, y, found := slashLabelPoint(buf, test.label)
			if !found {
				t.Fatalf("target %q is not rendered:\n%s", test.label, bufferPlain(buf))
			}
			before := model.DraftRevision()
			result := host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if !result.Handled || result.Action == nil || result.Action.ID != input.ActionSlashCommand {
				t.Fatalf("click did not emit a composer slash command: %+v", result)
			}
			event, ok := result.Action.Payload.(input.SlashCommandEvent)
			if !ok || event.Command != "/"+test.label {
				t.Fatalf("activated payload=%#v, want /%s", result.Action.Payload, test.label)
			}
			if model.Value() != "" || model.SlashActive() || model.CursorFlat() != 0 || model.InputSelecting() || len(model.SnapshotReferenceIDs()) != 0 {
				t.Fatalf("activation did not consume the complete draft: text=%q slash=%v cursor=%d refs=%v", model.Value(), model.SlashActive(), model.CursorFlat(), model.SnapshotReferenceIDs())
			}
			if model.DraftRevision() <= before {
				t.Fatal("activation did not fence the consumed draft")
			}
			for i := 0; i < 2; i++ {
				result = host.Handle(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
				if result.Action != nil {
					t.Fatalf("release activated the command again: %+v", result)
				}
			}
			if host.HasPointerCapture() {
				t.Fatal("release left pointer capture armed")
			}
		})
	}
}

func TestSlashPointerIgnoresOtherButtonsAndBlankSpace(t *testing.T) {
	for _, test := range []struct {
		name, query string
		button      tea.MouseButton
		point       string
	}{
		{"right click", "/se", tea.MouseRight, "sessions"},
		{"middle click", "/se", tea.MouseMiddle, "sessions"},
		{"left padding", "/se", tea.MouseLeft, "padding"},
		{"separator", "/se", tea.MouseLeft, "separator"},
		{"blank row", "/fork", tea.MouseLeft, "blank"},
		{"no matches", "/zzzz-no-match", tea.MouseLeft, "empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			model, host, buf := slashHost(t, test.query, 1)
			model.SetSelection(1, 2)
			value, cursor, selected := model.Value(), model.CursorFlat(), model.SelectedText()
			refs, revision := model.SnapshotReferenceIDs(), model.DraftRevision()
			x, y, found := slashLabelPoint(buf, "sessions")
			switch test.point {
			case "sessions":
				if !found {
					t.Fatal("suggestion to right/middle click was not rendered")
				}
			case "padding":
				x = 0
			case "separator":
				x, y = 5, 0
			case "blank":
				x, y, found = slashLabelPoint(buf, "fork")
				if !found {
					t.Fatal("fork suggestion was not rendered")
				}
				y++
			case "empty":
				x, y = 5, 2
			}
			for _, message := range []tea.Msg{
				tea.MouseClickMsg{X: x, Y: y, Button: test.button},
				tea.MouseReleaseMsg{X: x, Y: y, Button: test.button},
			} {
				if result := host.Handle(message); result.Action != nil || result.Cmd != nil {
					t.Fatalf("nonactivation emitted an effect: %+v", result)
				}
			}
			if model.Value() != value || model.CursorFlat() != cursor || model.SelectedText() != selected || !model.SlashActive() || model.DraftRevision() != revision || !slices.Equal(model.SnapshotReferenceIDs(), refs) {
				t.Fatal("nonactivation changed the draft or its reference/selection state")
			}
		})
	}
}
