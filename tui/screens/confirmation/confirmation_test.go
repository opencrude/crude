package confirmation

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestConfirmationQuickKeysUseTypedActions(t *testing.T) {
	for _, test := range []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{key: tea.KeyPressMsg{Code: 'y', Text: "y"}, want: "allow"},
		{key: tea.KeyPressMsg{Code: 'a', Text: "a"}, want: "always"},
		{key: tea.KeyPressMsg{Code: 'n', Text: "n"}, want: "deny"},
	} {
		screen, host := testHost(t, Details{Question: "Proceed?", Operation: "write", Tool: "apply_patch"})
		result := host.Handle(test.key)
		if result.Cmd == nil {
			t.Fatalf("key %q did not emit a result", test.key.String())
		}
		message, ok := result.Cmd().(ResultMsg)
		if !ok || message.Target != screen || message.Choice != test.want {
			t.Fatalf("key %q result=%#v", test.key.String(), message)
		}
	}
}

// The Auto Review choice appears only when offered, keeps the three answers in
// place, and never appears for a hard-blocked request or while it is already on.
func TestAutoReviewChoiceOfferedOnlyWhenAppropriate(t *testing.T) {
	base := Choices(false)
	if len(base) != 3 || base[0].ID != ChoiceAllow || base[2].ID != ChoiceDeny {
		t.Fatalf("base choices changed: %#v", base)
	}
	offered := Choices(true)
	if len(offered) != 4 || offered[3].ID != ChoiceAutoReview {
		t.Fatalf("auto review choice not appended last: %#v", offered)
	}
	if !OffersAutoReview(false, "project-write") {
		t.Fatal("auto review should be offered while it is off")
	}
	if OffersAutoReview(true, "project-write") {
		t.Fatal("auto review must not be offered while it is already on")
	}
	if OffersAutoReview(false, "blocked") {
		t.Fatal("auto review must not be offered for a hard-blocked request")
	}
}

// Choosing Auto Review does not answer the request: it emits its result without
// the panel.close action, so the confirmation stays on screen while the user
// turns the reviewer on. Deny still closes the panel.
func TestAutoReviewChoiceKeepsRequestOnScreen(t *testing.T) {
	// The shortcut reaches the choice through the real host.
	_, host := testHost(t, Details{Question: "Proceed?", Operation: "write", Tool: "apply_patch", OfferAutoReview: true})
	if result := host.Handle(tea.KeyPressMsg{Code: 'r', Text: "r"}); result.Cmd == nil || result.Cmd().(ResultMsg).Choice != ChoiceAutoReview {
		t.Fatalf("r did not select auto review: %#v", result)
	}
	// The screen's own result decides whether the panel closes.
	screen := New(theme.DefaultDark())
	screen.Reset(Details{Question: "Proceed?", OfferAutoReview: true})
	auto := screen.HandleAction(ui.EventContext{}, ui.Action{ID: choiceActionPrefix + ChoiceAutoReview})
	if auto.Cmd == nil || auto.Cmd().(ResultMsg).Choice != ChoiceAutoReview {
		t.Fatalf("auto review action result=%#v", auto)
	}
	if auto.Action != nil && auto.Action.ID == "panel.close" {
		t.Fatal("auto review closed the confirmation panel")
	}
	deny := screen.HandleAction(ui.EventContext{}, ui.Action{ID: choiceActionPrefix + ChoiceDeny})
	if deny.Action == nil || deny.Action.ID != "panel.close" {
		t.Fatalf("deny should close the panel: %#v", deny)
	}
	// Without the offer, the auto review action is not an answer.
	plain := New(theme.DefaultDark())
	plain.Reset(Details{Question: "Proceed?"})
	if got := plain.HandleAction(ui.EventContext{}, ui.Action{ID: choiceActionPrefix + ChoiceAutoReview}); got.Cmd != nil {
		t.Fatal("auto review answered a confirmation that did not offer it")
	}
}

func TestConfirmationEscapeCancelsTurnWithoutDenyingPermission(t *testing.T) {
	screen, host := testHost(t, Details{Question: "Proceed?", Operation: "write", Tool: "apply_patch"})
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if result.Cmd == nil {
		t.Fatal("Escape did not emit a cancellation")
	}
	message, ok := result.Cmd().(CancelMsg)
	if !ok || message.Target != screen {
		t.Fatalf("Escape result = %#v, want CancelMsg for owning screen", message)
	}
}

func TestConfirmationPaintsRiskMetadataAndMouseActions(t *testing.T) {
	_, host := testHost(t, Details{
		Question: "Allow this operation?", Operation: "delete", Tool: "bash", Target: "/project/out",
		Risk: "destructive", Reason: "Removes generated files", Suggestion: "Review the target",
	})
	buf := surface.NewCellBuffer(100, 12)
	host.Paint(buf)
	for _, want := range []string{"Confirmation required", "DANGEROUS", "delete", "/project/out", "Review the target"} {
		if !bufferContains(buf, want) {
			t.Fatalf("confirmation paint missing %q:\n%s", want, bufferText(buf))
		}
	}

	click := findPainted(buf, "Allow (y)")
	if click.x < 0 {
		t.Fatalf("Allow button not painted:\n%s", bufferText(buf))
	}
	result := host.Handle(tea.MouseClickMsg{X: click.x, Y: click.y, Button: tea.MouseLeft})
	if result.Cmd == nil || result.Cmd().(ResultMsg).Choice != "allow" {
		t.Fatalf("mouse allow result=%#v", result)
	}
}

func TestConfirmationPaintsAcrossThemesAndSizes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 42, H: 8}, {W: 120, H: 20}} {
			screen := New(th)
			screen.Reset(Details{Question: "Proceed?", Risk: "network", Tool: "fetch"})
			deck := ui.NewPanelDeck("deck", ui.NewTokenSet(th))
			deck.Open(ui.PanelEntry{ID: "confirmation", Title: "Confirmation", Content: screen, Policy: ui.PanelBlocking})
			host := ui.NewHost(th, deck)
			host.Resize(size.W, size.H)
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			if !bufferContains(buf, "Confirmation") {
				t.Fatalf("confirmation did not paint at %#v", size)
			}
		}
	}
}

func bufferText(buf *surface.CellBuffer) string {
	text := ""
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			text += string(r)
		}
		text += "\n"
	}
	return text
}

func testHost(t *testing.T, details Details) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	screen.Reset(details)
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(th))
	deck.Open(ui.PanelEntry{ID: "confirmation", Title: "Confirmation", Content: screen, Policy: ui.PanelBlocking})
	host := ui.NewHost(th, deck)
	host.Resize(100, 12)
	host.Layout()
	if !host.Focus(ActionsID) {
		t.Fatal("could not focus confirmation actions")
	}
	return screen, host
}

func bufferContains(buf *surface.CellBuffer, want string) bool {
	runes := []rune(want)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x <= buf.Width()-len(runes); x++ {
			matched := true
			for index, r := range runes {
				if buf.Get(x+index, y).Char != r {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
	}
	return false
}

type paintedAt struct{ x, y int }

func findPainted(buf *surface.CellBuffer, want string) paintedAt {
	runes := []rune(want)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x <= buf.Width()-len(runes); x++ {
			matched := true
			for index, r := range runes {
				if buf.Get(x+index, y).Char != r {
					matched = false
					break
				}
			}
			if matched {
				return paintedAt{x: x, y: y}
			}
		}
	}
	return paintedAt{x: -1, y: -1}
}

func TestInvestigationHostConsentIsNotAnOrdinaryApproval(t *testing.T) {
	for _, tc := range []struct {
		key  rune
		want string
	}{{'y', "allow_investigation"}, {'n', "deny"}, {'a', ""}, {'r', ""}} {
		_, host := testHost(t, Details{Question: "Run investigation commands on this computer?", Operation: "plan_investigation_host", OfferAutoReview: true})
		result := host.Handle(tea.KeyPressMsg{Code: tc.key, Text: string(tc.key)})
		if tc.want == "" {
			if result.Cmd != nil {
				t.Fatalf("%q answered the host consent", tc.key)
			}
			continue
		}
		if result.Cmd == nil || result.Cmd().(ResultMsg).Choice != tc.want {
			t.Fatalf("%q did not send %q", tc.key, tc.want)
		}
	}
}

func TestNarrowInvestigationConsentShowsRiskAndPagesExactDetails(t *testing.T) {
	_, host := testHost(t, Details{
		Question: "Allow commands for this investigation?", Operation: "plan_investigation_host", OfferAutoReview: true,
		Reason: "Commands run on this computer, outside a sandbox. They can access files outside the investigation folder.",
		Target: "go test -count=1 -run TestValue .", Directory: "/tmp/" + strings.Repeat("long-directory/", 20) + "checkout-tail",
	})
	host.Resize(41, 11)
	host.Layout()
	buf := surface.NewCellBuffer(41, 11)
	host.Paint(buf)
	for _, want := range []string{"outside a sandbox", "Allow for this plan (y)", "Do not run (n)"} {
		if !bufferContains(buf, want) {
			t.Fatalf("narrow consent does not show %q:\n%s", want, bufferText(buf))
		}
	}
	for _, unwanted := range []string{"Always allow", "Auto Review", "checkout-tail"} {
		if bufferContains(buf, unwanted) {
			t.Fatalf("unexpected %q in first page:\n%s", unwanted, bufferText(buf))
		}
	}
	commandSeen := bufferContains(buf, "Command: go test")
	for i := 0; i < 5 && !bufferContains(buf, "checkout-tail"); i++ {
		result := host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
		if !result.Handled || result.Cmd != nil {
			t.Fatalf("Page Down did not browse the details without answering: %+v", result)
		}
		host.Paint(buf)
		commandSeen = commandSeen || bufferContains(buf, "Command: go test")
	}
	if !commandSeen || !bufferContains(buf, "checkout-tail") || !bufferContains(buf, "Do not run (n)") {
		t.Fatalf("command, exact folder or safe action is unreachable:\n%s", bufferText(buf))
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil || result.Cmd().(ResultMsg).Choice != ChoiceDeny {
		t.Fatalf("Enter approved without choosing Allow: %+v", result)
	}
}
