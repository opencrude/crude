package statusoverview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestDockedCommandRowDoesNotDisclose(t *testing.T) {
	projection := testProjection()
	projection.Views[ViewTabs] = append([]Item{{ID: "sessions", Label: "Fix compiler warnings", ActivateAction: "session:resume:saved"}}, projection.Views[ViewTabs]...)
	screen := NewDocked(theme.DefaultDark(), projection)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(80, 16)
	host.Layout()
	if !screen.Select("sessions") || screen.IsExpanded("sessions") {
		t.Fatal("selecting a command must not expand it")
	}
	host.Focus(ListID)
	for _, key := range []rune{tea.KeyLeft, tea.KeyRight} {
		result := host.Handle(tea.KeyPressMsg{Code: key})
		if result.Cmd != nil || screen.IsExpanded("sessions") {
			t.Fatal("disclosure keys must not execute or expand a command")
		}
	}
	for _, key := range []rune{tea.KeyEnter, tea.KeySpace} {
		result := host.Handle(tea.KeyPressMsg{Code: key})
		if !result.Handled || result.Cmd == nil {
			t.Fatal("activation key must execute the command")
		}
		if msg, ok := result.Cmd().(NavigateMsg); !ok || msg.Action != "session:resume:saved" {
			t.Fatalf("unexpected command: %+v", msg)
		}
		if screen.IsExpanded("sessions") {
			t.Fatal("executing a command must not expand it")
		}
	}
	buf := surface.NewCellBuffer(80, 16)
	host.Paint(buf)
	for y := 0; y < 16; y++ {
		row := PlainRow(buf, y, 80)
		if strings.Contains(row, "Fix compiler warnings") && strings.Contains(row, screen.tokens.Glyphs().TreeCollapsed) {
			t.Fatalf("command row painted a disclosure chevron: %q", row)
		}
	}
	// Filtering includes commands, and a periodic projection refresh preserves them.
	screen.query = "compiler"
	screen.SetProjection(projection)
	if rows := screen.list.list.Items(); len(rows) != 1 || rows[0].ID != "sessions" {
		t.Fatalf("search/refresh lost command row: %+v", rows)
	}
}
