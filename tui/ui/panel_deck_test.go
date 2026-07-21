package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestPanelDeckRetainsPanelsAndRoutesTabs(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	deck := NewPanelDeck("deck", tokens)
	first := NewButton("deck/first", "First action", "first.activate")
	second := NewButton("deck/second", "Second action", "second.activate")
	deck.Open(PanelEntry{ID: "first", Title: "First", Content: first, Closable: true, Policy: PanelInteractive, PreferredHeight: 4})
	deck.Open(PanelEntry{ID: "second", Title: "Second", Content: second, Closable: true, Attention: true, Policy: PanelBlocking, PreferredHeight: 5})

	host := NewHost(theme.DefaultDark(), deck)
	host.Resize(60, 8)
	host.Layout()
	if deck.Count() != 2 || deck.Active().ID != "second" || deck.PreferredHeight(20) != 8 {
		t.Fatalf("deck state count=%d active=%#v height=%d", deck.Count(), deck.Active(), deck.PreferredHeight(20))
	}

	host.Focus(deck.tabs.ID())
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	if result.Action != nil || deck.Active().ID != "first" || host.Focused() != first.ID() {
		t.Fatalf("tab activation result=%#v active=%q focus=%q", result, deck.Active().ID, host.Focused())
	}

	buf := surface.NewCellBuffer(60, 8)
	host.Paint(buf)
	if buf.Get(0, 0).Char != '─' {
		t.Fatalf("deck divider=%q", buf.Get(0, 0).Char)
	}
}

func TestPanelDeckCloseEmitsEmptyAction(t *testing.T) {
	deck := NewPanelDeck("deck", NewTokenSet(theme.DefaultDark()))
	deck.Open(PanelEntry{ID: "settings", Title: "Settings", Content: NewLabel("deck/settings", "Settings", theme.DefaultDark()), Closable: true})
	result := deck.HandleAction(EventContext{}, Action{ID: "panel.close"})
	if !result.Handled || result.Action == nil || result.Action.ID != "paneldeck.empty" || deck.IsOpen() {
		t.Fatalf("close result=%#v open=%v", result, deck.IsOpen())
	}
}
