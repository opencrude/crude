package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestPanelDeckUsesShellBackground(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	shell := tokens.Color(ColorBg)
	popup := tokens.Color(ColorPopupBg)
	if shell.Equal(popup) {
		t.Fatal("dark theme must distinguish shell bg from popup bg")
	}
	deck := NewPanelDeck("deck", tokens)
	form := NewDockedForm("deck/form", nil, "Settings", "Per-session", tokens)
	deck.Open(PanelEntry{ID: "settings", Title: "Settings", Content: form, Closable: true, Policy: PanelInteractive, PreferredHeight: 6})
	host := NewHost(th, deck)
	host.Resize(40, 8)
	host.Layout()
	buf := surface.NewCellBuffer(40, 8)
	host.Paint(buf)
	if got := buf.Get(0, 0).BG; !got.Equal(shell) || got.Equal(popup) {
		t.Fatalf("deck chrome bg=%v want shell %v (not popup %v)", got, shell, popup)
	}
}

func TestPanelDeckRetainsPanelsAndRoutesTabs(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	deck := NewPanelDeck("deck", tokens)
	first := NewButton("deck/first", "First action", "first.activate")
	second := NewButton("deck/second", "Second action", "second.activate")
	deck.Open(PanelEntry{ID: "first", Title: "First", Content: first, Closable: true, Policy: PanelInteractive, PreferredHeight: 4})
	deck.Open(PanelEntry{ID: "second", Title: "Second", Content: second, Closable: true, Attention: true, Policy: PanelInteractive, PreferredHeight: 5})

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

func TestPanelDeckBlockingPanelCannotBeBackgrounded(t *testing.T) {
	deck := NewPanelDeck("deck", NewTokenSet(theme.DefaultDark()))
	first := NewLabel("deck/first", "First", theme.DefaultDark())
	blocking := NewLabel("deck/blocking", "Blocking", theme.DefaultDark())
	deck.Open(PanelEntry{ID: "first", Title: "First", Content: first, Closable: true, Policy: PanelInteractive})
	deck.Open(PanelEntry{ID: "blocking", Title: "Blocking", Content: blocking, Policy: PanelBlocking})

	result := deck.HandleAction(EventContext{}, Action{ID: "tabs.activate", Payload: "first"})
	if !result.Handled || deck.Active().ID != "blocking" {
		t.Fatalf("blocking panel was backgrounded: result=%#v active=%q", result, deck.Active().ID)
	}
	result = deck.HandleAction(EventContext{}, Action{ID: "tabs.close", Payload: "first"})
	if !result.Handled || deck.Count() != 2 {
		t.Fatalf("blocking panel allowed tab close: result=%#v count=%d", result, deck.Count())
	}
	if deck.Activate("first") {
		t.Fatal("blocking panel was programmatically backgrounded")
	}
	deck.Open(PanelEntry{ID: "third", Title: "Third", Content: NewLabel("deck/third", "Third", theme.DefaultDark()), Policy: PanelInteractive})
	if deck.Active().ID != "blocking" {
		t.Fatalf("opening another panel backgrounded blocking panel: active=%q", deck.Active().ID)
	}
	if !deck.panels[2].Attention {
		t.Fatal("panel queued behind a blocking panel did not request attention")
	}
	deck.Close("blocking")
	if deck.Active().ID != "third" || deck.Active().Attention {
		t.Fatalf("queued panel activation did not clear attention: active=%#v", deck.Active())
	}
}

func TestPanelDeckPassivePanelDoesNotDisplaceInteractivePanel(t *testing.T) {
	deck := NewPanelDeck("deck", NewTokenSet(theme.DefaultDark()))
	deck.Open(PanelEntry{ID: "settings", Title: "Settings", Content: NewLabel("deck/settings", "Settings", theme.DefaultDark()), Policy: PanelInteractive})
	deck.Open(PanelEntry{ID: "jobs", Title: "Jobs", Content: NewLabel("deck/jobs", "Jobs", theme.DefaultDark()), Policy: PanelPassive, Attention: true})

	if deck.Active().ID != "settings" {
		t.Fatalf("passive panel displaced interactive panel: active=%q", deck.Active().ID)
	}
	if !deck.panels[1].Attention {
		t.Fatal("queued passive panel lost its requested attention")
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
