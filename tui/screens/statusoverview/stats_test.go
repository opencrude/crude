package statusoverview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestStatsSingleLineAndKeyboardDisclosure(t *testing.T) {
	screen := NewStats(theme.DefaultDark(), testProjection())
	host := ui.NewHost(theme.DefaultDark(), screen)
	screen.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	host.Resize(108, 24)
	host.Layout()
	if got := screen.Children(); len(got) != 2 || got[0] != screen.search || got[1] != screen.list {
		t.Fatalf("stats must contain search and list only: %+v", got)
	}
	if screen.search.Bounds().W != 104 || screen.list.Bounds().Y != 2 || screen.primaryActions.Bounds().W != 0 {
		t.Fatal("stats search/list layout is incorrect")
	}
	if !screen.Select("agent:one") || screen.IsExpanded("agent:one") {
		t.Fatal("selecting the header target must not expand it")
	}
	if len(screen.list.list.Items()) != 3 {
		t.Fatal("collapsed entries must occupy exactly one row")
	}
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if !screen.IsExpanded("agent:one") {
		t.Fatal("Right must expand the selected row")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	buf := surface.NewCellBuffer(108, 24)
	host.Paint(buf)
	if row := PlainRow(buf, screen.list.Bounds().Y+1, 108); !strings.Contains(row, "One") || !strings.Contains(row, "Ollama / glm") || !strings.Contains(row, "Waiting") {
		t.Fatalf("compact identity and state must share a row: %q", row)
	}
	x, y := -1, -1
	for row := 0; row < 24; row++ {
		if col := strings.Index(PlainRow(buf, row, 108), "One"); col >= 0 {
			x, y = col, row
			break
		}
	}
	if x < 0 {
		t.Fatal("target row not painted")
	}
	host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if !screen.IsExpanded("agent:one") {
		t.Fatal("click failed to expand row")
	}
	host.MarkLayout()
	host.Layout()
	host.Paint(buf)
	if !strings.Contains(bufferText(buf, 108, 24), "71.5k / 1.05M") {
		t.Fatal("clicked row details not painted")
	}
	host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if screen.IsExpanded("agent:one") {
		t.Fatal("second click failed to collapse row")
	}
	host.Focus(SearchID)
	for _, r := range "Two" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if rows := screen.list.list.Items(); len(rows) != 1 || rows[0].ID != "agent:two" {
		t.Fatalf("search failed to filter collapsed rows: %+v", rows)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	// A second Tab returns to search, not an unmounted action bar.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	host.Handle(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if len(screen.list.list.Items()) != 0 {
		t.Fatal("Tab cycle did not return to search")
	}
}
