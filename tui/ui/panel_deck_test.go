package ui

import (
	"strings"
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

func TestPanelDeckStripOwnsBaselineAndKeepsChromeRows(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	deck := NewPanelDeck("deck", tokens)
	first := NewButton("deck/first", "First action", "first.activate")
	second := NewButton("deck/second", "Second action", "second.activate")
	deck.Open(PanelEntry{ID: "first", Title: "First", Content: first, Policy: PanelInteractive, PreferredHeight: 4})
	deck.Open(PanelEntry{ID: "second", Title: "Second", Content: second, Policy: PanelInteractive, PreferredHeight: 5})

	host := NewHost(th, deck)
	host.Resize(60, 10)
	host.Layout()
	buf := surface.NewCellBuffer(60, 10)
	host.Paint(buf)

	// Deck chrome: row 0 is the deck's own top rule, rows 1-2 are the strip
	// (labels + baseline), content starts on row 3. The strip's baseline
	// replaced the deck's old row-2 rule at the same row, so panels lost no
	// height.
	if got := deck.tabs.Bounds(); got.Y != 1 || got.H != StripHeight {
		t.Fatalf("deck strip bounds=%+v want y=1 h=%d", got, StripHeight)
	}
	if got := deck.content.Y; got != 1+StripHeight {
		t.Fatalf("deck content y=%d want %d", got, 1+StripHeight)
	}
	if got := deck.PreferredHeight(20); got != 8 {
		t.Fatalf("deck preferred height=%d want 8 (content 5 + 3 chrome rows)", got)
	}
	if labels := stripRow(buf, 1, 60); !strings.Contains(labels, "First") || !strings.Contains(labels, "Second") {
		t.Fatalf("deck strip labels missing: %q", labels)
	}
	if baseline := stripRow(buf, 2, 60); strings.Trim(baseline, "─") != "" {
		t.Fatalf("deck baseline must span the deck row: %q", baseline)
	}
}

func TestPanelDeckSinglePanelHasNoStrip(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	deck := NewPanelDeck("deck", tokens)
	deck.Open(PanelEntry{ID: "only", Title: "Only", Content: NewButton("deck/only", "Only", "only.activate"), Policy: PanelInteractive, PreferredHeight: 4})

	host := NewHost(th, deck)
	host.Resize(60, 8)
	host.Layout()
	buf := surface.NewCellBuffer(60, 8)
	host.Paint(buf)

	if got := deck.content.Y; got != 1 {
		t.Fatalf("single-panel deck content y=%d want 1", got)
	}
	if !deck.tabs.Bounds().Empty() {
		t.Fatalf("single-panel deck laid out a tab strip: %+v", deck.tabs.Bounds())
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

func TestPanelDeckReadableWidthClampsContent(t *testing.T) {
	// DefaultDark PadLR=2: content keeps the theme padding on both sides,
	// then readable-width clamps what remains.
	cases := []struct {
		deckW        int
		wantContentW int
	}{
		{deckW: 100, wantContentW: 96}, // narrow: full (padded) width
		{deckW: 120, wantContentW: 58}, // wide: half of 116
		{deckW: 210, wantContentW: 68}, // ultra wide: third of 206
	}
	for _, tc := range cases {
		deck := NewPanelDeck("deck", NewTokenSet(theme.DefaultDark()))
		content := NewLabel("deck/settings", "Settings", theme.DefaultDark())
		deck.Open(PanelEntry{ID: "settings", Title: "Settings", Content: content, Policy: PanelInteractive, Width: PanelReadableWidth})
		deck.Layout(LayoutContext{}, Rect{W: tc.deckW, H: 8})
		got := content.Bounds()
		if got.W != tc.wantContentW || got.X != 2 {
			t.Fatalf("deckW=%d content bounds=%+v, want X=2 W=%d", tc.deckW, got, tc.wantContentW)
		}
	}
}

func TestPanelDeckFullWidthPanelIgnoresReadableWidth(t *testing.T) {
	deck := NewPanelDeck("deck", NewTokenSet(theme.DefaultDark()))
	content := NewLabel("deck/jobs", "Jobs", theme.DefaultDark())
	deck.Open(PanelEntry{ID: "jobs", Title: "Jobs", Content: content, Policy: PanelPassive})
	deck.Layout(LayoutContext{}, Rect{W: 210, H: 8})
	if got := content.Bounds(); got.X != 2 || got.W != 206 {
		t.Fatalf("default panel must keep the padded deck width, bounds=%+v", got)
	}
}

func TestPanelDeckReadableWidthFeedsRuleBleed(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	deck := NewPanelDeck("deck", tokens)
	model := NewFormModel()
	model.AddField(&FieldState{ID: "name", Label: "Name", Type: FieldText, Value: StringValue("x")})
	form := NewDockedForm("deck/form", model, strings.Repeat("T", 90), "", tokens)
	deck.Open(PanelEntry{ID: "settings", Title: "Settings", Content: form, Policy: PanelInteractive, Width: PanelReadableWidth})
	deck.Layout(LayoutContext{}, Rect{W: 120, H: 6})
	if form.Bounds().W != 58 || form.Bounds().X != 2 {
		t.Fatalf("readable panel form bounds=%+v, want X=2 W=58", form.Bounds())
	}
	buf := surface.NewCellBuffer(120, 6)
	form.Paint(buf, 0, 0, PaintContext{})
	// Rule row (title + 1 within the form, content starts at deck row 1)
	// must span the deck edge to edge even though content keeps the theme
	// padding and the readable clamp.
	if got := buf.Get(0, 2).Char; got != '─' {
		t.Fatalf("deck rule must reach the left edge: cell=%q", got)
	}
	if got := buf.Get(119, 2).Char; got != '─' {
		t.Fatalf("deck rule must reach the right edge: cell=%q", got)
	}
	if got := buf.Get(61, 1).Char; got == 'T' {
		t.Fatal("title must stay truncated to the readable column")
	}

	// A full-width panel publishes no bleed, so rules stay bounded.
	deck.Open(PanelEntry{ID: "jobs", Title: "Jobs", Content: NewLabel("deck/jobs", "Jobs", theme.DefaultDark()), Policy: PanelPassive})
	deck.panels[1].Attention = false
	deck.Activate("jobs")
	deck.Layout(LayoutContext{}, Rect{W: 120, H: 6})
	deck.Activate("settings")
	deck.Layout(LayoutContext{}, Rect{W: 120, H: 6})
	buf = surface.NewCellBuffer(120, 6)
	form.Paint(buf, 0, 0, PaintContext{})
	ruleY := form.Bounds().Y + 1
	if got := buf.Get(119, ruleY).Char; got != '─' {
		t.Fatalf("bleed must be re-applied when the readable panel returns: cell=%q", got)
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
