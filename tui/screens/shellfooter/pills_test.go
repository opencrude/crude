package shellfooter

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/theme"
	"crude/tui/ui"
)

func stripSegments() []Segment {
	return []Segment{
		{Text: "Problems", FillRole: ui.ColorError, Pill: true, Priority: 1, Action: "problems"},
		{Text: "Notices", FillRole: ui.ColorAccent, Pill: true, Priority: 1, Action: "notices"},
		{Text: "Terminals", FillRole: ui.ColorSuccess, Pill: true, Priority: 1, Action: "terminals"},
		{Text: "Board", FillRole: ui.ColorAccent2, Pill: true, Action: "board"},
		{Text: "Memory", FillRole: ui.ColorMuted, Pill: true, Action: "memory"},
		{Text: "Workspace", FillRole: ui.ColorAccent2, Pill: true, Action: "workspace"},
	}
}

func TestVividStripFillsAndHitTargetsAcrossThemes(t *testing.T) {
	for _, th := range theme.AllPresets() {
		t.Run(th.Name, func(t *testing.T) {
			// Every control retains its own hue when active, not one shared fill.
			for active := -1; active < len(stripSegments()); active++ {
				segments := stripSegments()
				if active >= 0 {
					segments[active].Active = true
				}
				screen, buf := paintFooterBuffer(t, th, Projection{Right: segments}, 100)
				tokens := ui.NewTokenSet(th)
				if len(screen.hits) != len(segments) {
					t.Fatalf("got %d targets, want %d", len(screen.hits), len(segments))
				}
				for index, hit := range screen.hits {
					segment := segments[index]
					fg, bg := tokens.StatusStripColors(segment.FillRole, segment.Active, segment.Attention)
					attr := cell.Attrs(0)
					if segment.Active {
						attr = cell.AttrBold
					}
					for x := hit.start; x < hit.end; x++ {
						painted := buf.Get(x, 1)
						if !painted.BG.Equal(bg) || painted.BG.Equal(tokens.Color(ui.ColorBg)) || !painted.FG.Equal(fg) || painted.Attr != attr {
							t.Fatalf("%s column %d has wrong fill/ink: %+v", segment.Text, x, painted)
						}
						res := screen.Handle(ui.EventContext{LocalX: x, LocalY: 1}, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
						if !res.Handled || res.Cmd == nil {
							t.Fatalf("padding/label column %d is not clickable", x)
						}
						if msg := res.Cmd().(ActionMsg); msg.Action != segment.Action {
							t.Fatalf("column %d action=%q want %q", x, msg.Action, segment.Action)
						}
					}
					if index > 0 {
						if screen.hits[index-1].end != hit.start {
							t.Fatal("strip contains a gap between controls")
						}
						if buf.Get(hit.start-1, 1).BG.Equal(buf.Get(hit.start, 1).BG) {
							t.Fatal("neighboring colors do not define a boundary")
						}
					}
				}
			}
		})
	}
}

func TestWorkspaceSurvivesNarrowFooter(t *testing.T) {
	for _, width := range []int{16, 24, 36, 40, 48} {
		screen, row := paintFooter(t, Projection{Right: stripSegments()}, width)
		hits := screen.hits
		if len(hits) == 0 {
			t.Fatalf("width %d lost all controls: %q", width, row)
		}
		_, right := railColumns(width)
		if hits[len(hits)-1].end != right {
			t.Fatalf("width %d: controls do not end at right rail", width)
		}
		available := make(map[string]bool)
		for _, hit := range hits {
			available[hit.action] = true
		}
		for _, segment := range screen.Overflow() {
			available[segment.Action] = true
		}
		for _, action := range []string{"problems", "notices", "terminals", "board", "memory", "workspace"} {
			if !available[action] {
				t.Fatalf("width %d lost %s, including overflow: %q", width, action, row)
			}
		}
		if len(screen.Overflow()) > 0 && !available["footer-more"] {
			t.Fatalf("width %d has unreachable overflow", width)
		}
	}
}
