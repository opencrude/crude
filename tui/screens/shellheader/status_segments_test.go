package shellheader

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestStatusSegmentsKeepCriticalAndActionableAtNarrowWidth(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.SetProjection(Projection{
		LeftSegments: []Segment{{Text: "crude", Role: ui.ColorAccent}},
		RightSegments: []Segment{
			{Text: "Waiting 8s", Role: ui.ColorFg, Action: "tabs", Priority: 1},
			{Text: "Context 11%", Role: ui.ColorMuted, Priority: 4},
			{Text: "IDE: LSP failed", Role: ui.ColorError, Action: "workspace", Priority: 0},
			{Text: "Help", Role: ui.ColorMuted, Action: "help", Priority: 2},
		},
	})
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 50, H: 2})
	buf := surface.NewCellBuffer(50, 2)
	screen.Paint(buf, 0, 0, ui.PaintContext{})
	var line strings.Builder
	for x := range 50 {
		line.WriteRune(buf.Get(x, 0).Char)
	}
	if !strings.Contains(line.String(), "LSP failed") || !strings.Contains(line.String(), "Waiting") {
		t.Fatalf("narrow header hid critical context: %q", line.String())
	}
	var hit segmentHit
	for _, candidate := range screen.hits {
		if candidate.action == "workspace" {
			hit = candidate
		}
	}
	left, _ := ui.ShellContentRail(ui.NewTokenSet(theme.DefaultDark()), 50)
	result := screen.Handle(ui.EventContext{LocalX: left + hit.start, LocalY: 0}, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if result.Cmd == nil {
		t.Fatal("critical segment was not clickable")
	}
	if msg, ok := result.Cmd().(ActionMsg); !ok || msg.Action != "workspace" {
		t.Fatalf("action=%#v", result.Cmd())
	}
}
