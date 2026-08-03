package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// TestDeckFormPaintsThroughRootHost proves a docked form surface (session
// settings) renders through the ONE root Host: shellRoot → deckMount →
// deckHost → PanelDeck → Form. Phase 8's "Root Host/compositor wiring for
// form surfaces" remaining item requires docked forms to paint into the
// single full-screen shell surface (mainSurf), not a legacy layer surface.
func TestDeckFormPaintsThroughRootHost(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	app.launcherScreen = nil
	app.ActiveSession = &workspace.Session{
		SessionID: "session",
		Settings:  workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil),
	}
	app.openSessionSettings()
	if !app.sessionDeckFocused() {
		t.Fatal("precondition: session settings deck open + focused")
	}
	app.paintShellFrame()

	if !app.shellSlotActive(shellDeckSlot) {
		t.Fatal("deck slot must be mounted in the root tree")
	}
	deck := app.sessionDeckRect
	if deck.Empty() {
		t.Fatal("deck rect must be reserved by layout")
	}
	buf := app.mainSurf.Buffer()
	if buf == nil {
		t.Fatal("mainSurf buffer missing")
	}

	// The form title, a nav field label, and a primary action must all paint
	// into the root-host shell surface INSIDE the deck slot.
	for _, want := range []string{"Session AI Settings", "Main Agent", "Save"} {
		if !deckRegionContains(buf, deck, want) {
			t.Fatalf("form content %q missing from the deck slot of the root-host surface", want)
		}
	}

	// The deck region must not be left transparent anywhere: deckMount blits
	// the per-session deckHost opaque into mainSurf, so the whole slot is
	// covered by the deck's fill even where the form has no glyphs.
	transparent := 0
	for y := deck.Y; y < deck.Y+deck.H && y < buf.Height(); y++ {
		for x := deck.X; x < deck.X+deck.W && x < buf.Width(); x++ {
			if buf.Get(x, y).Transparent {
				transparent++
			}
		}
	}
	if transparent > 0 {
		t.Fatalf("deck slot has %d transparent cells — deck surface not blitted through the root host", transparent)
	}
}

// deckRegionContains reports whether plain text needle appears within the
// deck rect of a painted shell surface.
func deckRegionContains(buf *surface.CellBuffer, deck ui.Rect, needle string) bool {
	if buf == nil || needle == "" {
		return false
	}
	for y := deck.Y; y < deck.Y+deck.H && y < buf.Height(); y++ {
		var b strings.Builder
		for x := deck.X; x < deck.X+deck.W && x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Width > 0 && c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		if strings.Contains(b.String(), needle) {
			return true
		}
	}
	return false
}
