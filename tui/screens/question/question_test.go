package question

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// newTestHost mounts the screen in a blocking deck like the live session
// shell so host-level focus routing and pointer hit-testing are exercised.
func newTestHost(t *testing.T, screen *Screen) *ui.Host {
	t.Helper()
	deck := ui.NewPanelDeck("deck", screen.tokens)
	deck.Open(ui.PanelEntry{ID: "question", Title: "Questions", Content: screen, Policy: ui.PanelBlocking})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 12)
	host.Layout()
	return host
}

func TestScreenSelectsOptionsAndReturnsAllAnswers(t *testing.T) {
	screen := New(theme.DefaultDark())
	if got := screen.Reset(`[
		{"question":"Choose a color","header":"Color","options":["red","blue"]},
		{"question":"Add a note"}
	]`); got != 2 {
		t.Fatalf("Reset()=%d, want 2", got)
	}
	host := newTestHost(t, screen)
	host.Focus(OptionsID)

	// The highlighted stacked row commits on Enter, with no dropdown open step.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if screen.Current() != 1 || screen.Answer(0) != "red" {
		t.Fatalf("after option commit index=%d answer=%q", screen.Current(), screen.Answer(0))
	}
	if host.Focused() != InputID {
		t.Fatalf("text-only question entry focus=%v, want %v", host.Focused(), InputID)
	}
	// A human may leave a question unanswered, so the empty write-in reaches
	// the review instead of being blocked with a hint line.
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.next"})
	if result.Cmd != nil || !screen.Reviewing() {
		t.Fatal("unanswered text question could not reach the review")
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.previous"})
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.previous"})
	selected, _ := screen.options.Selected()
	if screen.Current() != 0 || selected.ID != "option:0" {
		t.Fatalf("back navigation lost option highlight: index=%d selected=%q",
			screen.Current(), selected.ID)
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.next"})
	if !screen.Reviewing() {
		t.Fatal("next navigation did not restore the review pass")
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.rail.select", Payload: ui.ListItem{ID: "rail/1", Payload: 1}})
	if screen.Current() != 1 || screen.Reviewing() {
		t.Fatalf("rail selection stayed on index %d", screen.Current())
	}
	host.Focus(InputID)

	for _, r := range "note" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	// The write-in has the composer's keys: the newline chord breaks the line
	// and Enter goes to the next step. On the last answer that is the review,
	// never the submission.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	for _, r := range "more" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := screen.Answer(1); got != "note\nmore" || host.Focused() != InputID || screen.Reviewing() {
		t.Fatalf("newline chord left the write-in: answer=%q focus=%v", got, host.Focused())
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd != nil || !screen.Reviewing() {
		t.Fatal("Enter on the final answer must open the review without submitting")
	}
	result = submitReview(t, host, screen)
	message, ok := result.Cmd().(ResultMsg)
	if !ok {
		t.Fatalf("result message=%T, want ResultMsg", result.Cmd())
	}
	if got, want := message.Result.Answers["1"], "red"; got != want {
		t.Fatalf("first answer=%q, want %q", got, want)
	}
	if got, want := message.Result.Answers["2"], "note\nmore"; got != want {
		t.Fatalf("second answer=%q, want %q", got, want)
	}
}

func bufferContains(buf *surface.CellBuffer, want string) bool {
	_, _, ok := bufferFind(buf, want)
	return ok
}

// bufferFind locates the first pixel of a painted run of text, returning its
// cell coordinates. Used to compute mouse hit targets whose geometry depends
// on the live layout (rail width, pane offset, option rows).
func bufferFind(buf *surface.CellBuffer, want string) (int, int, bool) {
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
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

func bufferText(buf *surface.CellBuffer) string {
	var result strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			result.WriteRune(r)
		}
		result.WriteByte('\n')
	}
	return result.String()
}

func TestScreenMultiSelectValidatesAndReturnsSelectedOptions(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[{"question":"Choose all","options":["alpha","beta"],"multiple":true}]`)
	host := newTestHost(t, screen)
	host.Focus(OptionsID)

	// Space marks rows in place and arrows move the highlight; the deck stays
	// on the question until Enter accepts the marks.
	host.Handle(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if screen.Current() != 0 || screen.Reviewing() {
		t.Fatalf("multi toggle advanced to %d", screen.Current())
	}
	if got := screen.choices[0]; len(got) != 2 || got[0] != "option:0" || got[1] != "option:1" {
		t.Fatalf("multi choices=%v", got)
	}

	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	painted := bufferText(buf)
	for _, want := range []string{"[x]", "alpha", "beta", "[ ] Type your own answer"} {
		if !bufferContains(buf, want) {
			t.Fatalf("stack missing %q:\n%s", want, painted)
		}
	}
	if strings.Contains(painted, "[ ]") == false && strings.Count(painted, "[x]") != 2 {
		t.Fatalf("expected two checked rows:\n%s", painted)
	}
	if strings.ContainsAny(painted, "▾▴❯") {
		t.Fatalf("stack painted a dropdown/marker glyph:\n%s", painted)
	}

	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd != nil || !screen.Reviewing() {
		t.Fatal("Enter must accept the marks and open the review, never submit")
	}
	result = submitReview(t, host, screen)
	message := result.Cmd().(ResultMsg)
	if got, want := message.Result.Answers["1"], "alpha, beta"; got != want {
		t.Fatalf("multi-select answer=%q, want %q", got, want)
	}
	if !bufferContains(buf, "Choose all") {
		t.Fatal("question detail not painted")
	}
}

func TestScreenCustomAnswerAcceptsPaste(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[{"question":"Choose or type","options":["known"]}]`)
	host := newTestHost(t, screen)
	host.Focus(OptionsID)

	// The write-in is the row below the last choice: Down moves into it.
	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	_, knownY, _ := bufferFind(buf, "○ known")
	_, writeY, ok := bufferFind(buf, "○ Type your own answer")
	if !ok || writeY != knownY+1 {
		t.Fatalf("write-in is not the row below the choices:\n%s", bufferText(buf))
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if host.Focused() != InputID {
		t.Fatalf("Down from the last choice focus=%v, want %v", host.Focused(), InputID)
	}

	host.Handle(tea.PasteMsg{Content: "pasted answer"})
	if got := screen.choices[0]; len(got) != 1 || got[0] != customOptionID {
		t.Fatalf("pasted text did not choose the own answer: %v", got)
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd != nil || !screen.Reviewing() {
		t.Fatal("pasted custom answer bypassed review")
	}
	result = submitReview(t, host, screen)
	message := result.Cmd().(ResultMsg)
	if got, want := message.Result.Answers["1"], "pasted answer"; got != want {
		t.Fatalf("custom answer=%q, want %q", got, want)
	}
}

// Typing on a choice starts an own answer, like typing in the composer, and
// Up on its first line returns to the choices.
func TestScreenTypingOnAChoiceWritesOwnAnswer(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[{"question":"Choose or type","options":["alpha","beta"]},{"question":"Next"}]`)
	host := newTestHost(t, screen)
	host.Focus(OptionsID)
	for _, r := range "Hi" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if host.Focused() != InputID || screen.input.Value() != "Hi" || screen.Answer(0) != "Hi" || screen.Current() != 0 {
		t.Fatalf("typing on a choice: focus=%v value=%q answer=%q", host.Focused(), screen.input.Value(), screen.Answer(0))
	}
	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	if !bufferContains(buf, "● Hi") {
		t.Fatalf("the typed answer is not marked as the choice:\n%s", bufferText(buf))
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if selected, _ := screen.options.Selected(); host.Focused() != OptionsID || selected.ID != "option:1" {
		t.Fatalf("Up from the write-in focus=%v selected=%q", host.Focused(), selected.ID)
	}
	host.Focus(InputID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if screen.Current() != 1 || screen.Answer(0) != "Hi" {
		t.Fatalf("Enter in the write-in did not go to the next question: index=%d", screen.Current())
	}
}

// A stray keystroke in the write-in must never trap the question: clearing the
// text releases exactly the write-in claim, the checked options survive, and
// the deck still reaches its review.
func TestScreenClearingWriteInReleasesOnlyTheWriteInClaim(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[{"question":"Choose all","options":["alpha","beta"],"multiple":true}]`)
	host := newTestHost(t, screen)
	host.Focus(OptionsID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	host.Focus(InputID)
	for _, r := range "accidental" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := screen.Answer(0); !strings.Contains(got, "alpha") || !strings.Contains(got, "accidental") {
		t.Fatalf("typing did not claim the write-in answer: %q", got)
	}
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := screen.Answer(0); got != "alpha" {
		t.Fatalf("cleared write-in stayed in the answer: %q", got)
	}
	if containsCustom(screen.choices[0]) {
		t.Fatalf("cleared write-in kept its selection: %v", screen.choices[0])
	}
	if result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.next"}); result.Cmd != nil || !screen.Reviewing() {
		t.Fatal("a released write-in blocked the review step")
	}
}

func TestScreenOptionMouseSelectionMatchesKeyboardSelection(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[
		{"question":"First","options":["mouse choice"]},
		{"question":"Second"}
	]`)
	host := newTestHost(t, screen)
	host.Focus(OptionsID)

	// The option row is always painted; clicking it commits directly, no
	// dropdown open/close dance.
	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	x, y, ok := bufferFind(buf, "mouse choice")
	if !ok {
		t.Fatalf("option row not painted:\n%s", bufferText(buf))
	}
	host.Handle(tea.MouseClickMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	if screen.Current() != 1 || screen.Answer(0) != "mouse choice" {
		t.Fatalf("mouse selection index=%d answer=%q", screen.Current(), screen.Answer(0))
	}
}

func TestScreenRailShowsAnswerStateAndSelectsQuestions(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[
		{"question":"Color","header":"Color","options":["red","blue"]},
		{"question":"Note"}
	]`)
	host := newTestHost(t, screen)

	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	painted := bufferText(buf)
	for _, want := range []string{"1. Color", "2. Note", "red", "blue", "Type your own answer"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("pane missing %q:\n%s", want, painted)
		}
	}
	if strings.ContainsAny(painted, "▾▴❯") {
		t.Fatalf("deck painted a dropdown/marker glyph:\n%s", painted)
	}
	// Stacked rows sit on consecutive lines, with every answer visible at once.
	_, redY, _ := bufferFind(buf, "red")
	_, blueY, _ := bufferFind(buf, "blue")
	if blueY != redY+1 {
		t.Fatalf("option rows not stacked: red y=%d blue y=%d\n%s", redY, blueY, painted)
	}

	// One Enter answers Q1; the rail icon flips to ✓ and the deck advances.
	host.Focus(OptionsID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if screen.Current() != 1 || screen.Answer(0) != "red" {
		t.Fatalf("after option commit index=%d answer=%q", screen.Current(), screen.Answer(0))
	}
	railItems := screen.railRows.Items()
	if len(railItems) != 3 || railItems[0].Icon != "✓" || railItems[2].Icon != "○" {
		t.Fatalf("rail icons after answering: %+v", railItems)
	}
	// The rail indexes questions only; review is reached through the actions,
	// never as an unnumbered extra row.
	for _, item := range railItems {
		if item.Label == "Review answers" || item.ID == "rail/review" {
			t.Fatalf("rail painted an out-of-contract review row: %+v", railItems)
		}
	}

	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.rail.select", Payload: ui.ListItem{ID: "rail/0", Label: "1. Color", Payload: 0}})
	if screen.Current() != 0 {
		t.Fatalf("rail select stayed on index %d, want 0", screen.Current())
	}
	if got := screen.choices[0]; len(got) != 1 || got[0] != "option:0" {
		t.Fatalf("rail select lost the prior answer: %v", got)
	}
}

func TestScreenPaintsAcrossThemesAndSizes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 42, H: 8}, {W: 120, H: 20}} {
			screen := New(th)
			screen.Reset(`[{"question":"Choose a deployment target","options":["local","remote"]}]`)
			deck := ui.NewPanelDeck("deck", ui.NewTokenSet(th))
			deck.Open(ui.PanelEntry{ID: "question", Title: "Questions", Content: screen, Policy: ui.PanelBlocking})
			host := ui.NewHost(th, deck)
			host.Resize(size.W, size.H)
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			if !bufferContains(buf, "Question") {
				t.Fatalf("question did not paint at %#v", size)
			}
			if bufferContains(buf, "▾") || bufferContains(buf, "▴") {
				t.Fatalf("dropdown affordance painted at %#v:\n%s", size, bufferText(buf))
			}
		}
	}
}

func TestScreenEscapeReturnsToRailAndRailEscapeCancels(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[{"question":"Continue?","options":["yes","no"]}]`)
	host := newTestHost(t, screen)
	host.Focus(OptionsID)

	// Esc inside the answer pane returns to the rail; nothing is denied.
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if host.Focused() != RailID {
		t.Fatalf("esc focus=%v, want %v", host.Focused(), RailID)
	}
	if result.Cmd != nil {
		t.Fatalf("esc from options cancelled the deck: %v", result.Cmd())
	}

	// Only an explicit cancel (rail Esc or the Cancel button) denies.
	result = screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.cancel"})
	if result.Cmd == nil {
		t.Fatal("explicit cancel did not resolve the deck")
	}
	if _, ok := result.Cmd().(CancelMsg); !ok {
		t.Fatalf("cancel message=%T, want CancelMsg", result.Cmd())
	}
}

func TestScreenEscapeClimbsLadderFromWriteIn(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[{"question":"Pick or write","options":["alpha"],"multiple":true}]`)
	host := newTestHost(t, screen)
	host.Focus(InputID)

	// The write-in is the session prompt area: flat deck background, no
	// raised form fill.
	if bg, ok := screen.input.BackgroundColor(); !ok || bg != screen.tokens.Color(ui.ColorBg) {
		t.Fatal("write-in area lost the flat deck background override")
	}

	// First Esc steps back to the option rows; the edit stays intact and
	// nothing is denied.
	for _, r := range "hi" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}); result.Cmd != nil {
		t.Fatalf("esc from write-in resolved the deck: %v", result.Cmd())
	}
	if host.Focused() != OptionsID {
		t.Fatalf("esc from write-in focus=%v, want %v", host.Focused(), OptionsID)
	}
	if got := screen.Answer(0); !strings.Contains(got, "hi") {
		t.Fatalf("esc dropped the draft answer: %q", got)
	}

	// Second Esc reaches the rail; only the third denies.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if host.Focused() != RailID {
		t.Fatalf("esc from rows focus=%v, want %v", host.Focused(), RailID)
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if result.Cmd == nil {
		t.Fatal("rail esc did not resolve the deck")
	}
	if _, ok := result.Cmd().(CancelMsg); !ok {
		t.Fatalf("ladder cancel message=%T, want CancelMsg", result.Cmd())
	}
}

func TestScreenTextOnlyEscapeLandsOnRailDirectly(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[{"question":"Just type"}]`)
	host := newTestHost(t, screen)
	host.Focus(InputID)

	// No option rows exist, so leaving the write-in skips straight to the
	// rail instead of focusing a zero-height pane.
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}); result.Cmd != nil {
		t.Fatalf("esc from text-only write-in resolved the deck: %v", result.Cmd())
	}
	if host.Focused() != RailID {
		t.Fatalf("text-only esc focus=%v, want %v", host.Focused(), RailID)
	}
}

// The newline chord follows the terminal's keyboard profile. Alt+Enter
// survives every multiplexer, so it always breaks the line, as in the chat
// composer; it never goes to the next step.
func TestScreenWriteInBreaksLinesOnAltEnter(t *testing.T) {
	screen := New(theme.DefaultDark())
	if got := screen.Reset(`[{"question":"Add a note"}]`); got != 1 {
		t.Fatalf("Reset()=%d, want 1", got)
	}
	host := newTestHost(t, screen)
	host.Focus(InputID)
	for _, r := range "note" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	host.Handle(tea.KeyPressMsg{Mod: tea.ModAlt, Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if screen.Reviewing() || screen.input.Value() != "note\nx" {
		t.Fatalf("Alt+Enter did not break the line: %q", screen.input.Value())
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd != nil || !screen.Reviewing() {
		t.Fatal("Enter must open the review, never send it")
	}
	result = submitReview(t, host, screen)
	message, ok := result.Cmd().(ResultMsg)
	if !ok || message.Result.Answers["1"] != "note\nx" {
		t.Fatalf("commit result=%#v", result.Cmd())
	}
}

// Submitting is a distinct native button action after the review has painted.
// Neither last-option activation nor a textarea commit may send the request.
func submitReview(t *testing.T, host *ui.Host, screen *Screen) ui.EventResult {
	t.Helper()
	if !screen.Reviewing() {
		t.Fatal("submission attempted outside review")
	}
	host.MarkLayout()
	host.Layout()
	r, _ := host.ScreenRect(ActionsID)
	buf := surface.NewCellBuffer(max(1, r.X+r.W), max(1, r.Y+r.H))
	host.Paint(buf)
	if !bufferContains(buf, "Submit") || bufferContains(buf, "Ctrl+Enter") {
		t.Fatalf("review submission control missing or misleading:\n%s", bufferText(buf))
	}
	host.Focus(ActionsID)
	screen.actions.FocusButton(1)
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("explicit review Submit did not emit owner result")
	}
	return result
}

// Moving between questions of one request never shrinks the deck, so the
// transcript above it does not jump at each step.
func TestScreenDeckHeightIsStableAcrossQuestions(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[
		{"question":"Long","options":["one","two","three","four","five","six"]},
		{"question":"Short","options":["yes"]}
	]`)
	host := newTestHost(t, screen)
	host.Resize(80, 30)
	host.Layout()
	first := screen.PreferredHeight(30)
	host.Focus(OptionsID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.MarkLayout()
	host.Layout()
	if screen.Current() != 1 || screen.PreferredHeight(30) != first {
		t.Fatalf("deck height changed from %d to %d between questions", first, screen.PreferredHeight(30))
	}
	screen.Reset(`[{"question":"Short","options":["yes"]}]`)
	host.MarkLayout()
	host.Layout()
	if screen.PreferredHeight(30) >= first {
		t.Fatal("a new request kept the height of the previous one")
	}
}
