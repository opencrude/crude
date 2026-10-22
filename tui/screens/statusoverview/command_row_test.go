package statusoverview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	ttypes "crude/types"
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

// Docked surfaces have no action bar, so an item's actions are rows of their
// own: a failed language server offers Retry where its error is shown.
func TestDockedItemActionsAreCommandRows(t *testing.T) {
	projection := testProjection()
	projection.Views[ViewTabs] = append([]Item{{ID: "lsp:lua", Label: "lua-language-server", Lines: []DetailLine{{Label: "Error", Value: "not installed"}},
		Actions: []ttypes.StatusAction{{ID: "lsp:restart", Label: "Retry", Enabled: true}, {ID: "lsp:hidden", Label: "Hidden", Enabled: false}}}}, projection.Views[ViewTabs]...)
	screen := NewDocked(theme.DefaultDark(), projection)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(80, 16)
	host.Layout()
	host.Focus(ListID)
	if !screen.Select("lsp:lua") {
		t.Fatal("item missing")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Layout()
	buf := surface.NewCellBuffer(80, 16)
	host.Paint(buf)
	retryY := -1
	for y := 0; y < 16; y++ {
		row := PlainRow(buf, y, 80)
		if strings.Contains(row, "[ Retry ]") {
			retryY = y
		}
		if strings.Contains(row, "Hidden") {
			t.Fatalf("disabled action painted: %q", row)
		}
	}
	if retryY < 0 {
		t.Fatal("expanded item did not offer its action")
	}
	click := host.Handle(tea.MouseClickMsg{X: 10, Y: retryY, Button: tea.MouseLeft})
	if click.Cmd == nil {
		t.Fatal("clicking the action row did nothing")
	}
	if msg, ok := click.Cmd().(NavigateMsg); !ok || msg.Action != "lsp:restart" {
		t.Fatalf("clicked action row sent %+v", click.Cmd())
	}
	screen.Select("lsp:lua")
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("Enter on the action row did nothing")
	}
	if msg, ok := result.Cmd().(NavigateMsg); !ok || msg.Action != "lsp:restart" || msg.Item.ID != "lsp:lua" {
		t.Fatalf("action row sent %+v", result.Cmd())
	}
}
