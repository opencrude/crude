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

	// The highlighted stacked row commits on Enter — no dropdown open step.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if screen.Current() != 1 || screen.Answer(0) != "red" {
		t.Fatalf("after option commit index=%d answer=%q", screen.Current(), screen.Answer(0))
	}
	if host.Focused() != InputID {
		t.Fatalf("text-only question entry focus=%v, want %v", host.Focused(), InputID)
	}
	// Empty text answers block without painting a redundant hint line.
	if result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.next"}); result.Cmd != nil {
		t.Fatal("empty text answer bypassed validation")
	}
	if screen.Current() != 1 || screen.errMsg != "" {
		t.Fatalf("silent block drifted: index=%d errMsg=%q", screen.Current(), screen.errMsg)
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.previous"})
	selected, _ := screen.options.Selected()
	if screen.Current() != 0 || selected.ID != "option:0" {
		t.Fatalf("back navigation lost option highlight: index=%d selected=%q",
			screen.Current(), selected.ID)
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.next"})
	if screen.Current() != 1 {
		t.Fatal("next navigation did not restore the subsequent question")
	}

	for _, r := range "note" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	// Prompt-area semantics: plain Enter inserts a newline; only Ctrl+Enter
	// commits the write-in answer.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := screen.Answer(1); strings.Contains(got, "\n") {
		t.Fatalf("plain enter advanced instead of inserting a newline: %q", got)
	}
	if host.Focused() != InputID {
		t.Fatalf("newline keystroke moved focus to %v", host.Focused())
	}
	result := host.Handle(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("text submit did not emit a question result")
	}
	message, ok := result.Cmd().(ResultMsg)
	if !ok {
		t.Fatalf("result message=%T, want ResultMsg", result.Cmd())
	}
	if got, want := message.Result.Answers["1"], "red"; got != want {
		t.Fatalf("first answer=%q, want %q", got, want)
	}
	if got, want := message.Result.Answers["2"], "note"; got != want {
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

	// Stacked rows toggle in place: Enter flips membership, arrows move the
	// highlight, and the deck stays on the question until Next.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if screen.Current() != 0 {
		t.Fatalf("multi toggle advanced to %d", screen.Current())
	}
	if got := screen.choices[0]; len(got) != 2 || got[0] != "option:0" || got[1] != "option:1" {
		t.Fatalf("multi choices=%v", got)
	}

	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	painted := bufferText(buf)
	for _, want := range []string{"[x]", "alpha", "beta", "Write your response"} {
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

	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.next"})
	if result.Cmd == nil {
		t.Fatal("multi-select submit did not emit a result")
	}
	message := result.Cmd().(ResultMsg)
	if got, want := message.Result.Answers["1"], "alpha, beta"; got != want {
		t.Fatalf("multi-select answer=%q, want %q", got, want)
	}
	if !bufferContains(buf, "Choose all") {
		t.Fatal("question detail not painted")
	}
}

func TestScreenCustomAnswerRequiresTextAndAcceptsPaste(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[{"question":"Choose or type","options":["known"]}]`)
	host := newTestHost(t, screen)
	host.Focus(OptionsID)

	// Rows: known / rule / Write your response. Down lands on the write-in
	// row; Enter selects it and hands focus to the text line.
	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	if !bufferContains(buf, "Write your response") {
		t.Fatalf("write-in row not painted:\n%s", bufferText(buf))
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if host.Focused() != InputID {
		t.Fatalf("custom row focus=%v, want %v", host.Focused(), InputID)
	}
	if got := screen.choices[0]; len(got) != 1 || got[0] != customOptionID {
		t.Fatalf("custom not selected: %v", got)
	}

	// Empty write-in still blocks submit, but silently: the placeholder in
	// the prompt area already asks for text, so no hint repeats underneath.
	if result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.next"}); result.Cmd != nil {
		t.Fatal("empty custom answer bypassed validation")
	}
	if screen.Current() != 0 || screen.errMsg != "" {
		t.Fatalf("blocked advance drifted: index=%d errMsg=%q", screen.Current(), screen.errMsg)
	}
	buf = surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	painted := bufferText(buf)
	for _, redundant := range []string{"Type your response", "Type an answer to continue"} {
		if strings.Contains(painted, redundant) {
			t.Fatalf("redundant text-input validation painted:\n%s", painted)
		}
	}

	host.Handle(tea.PasteMsg{Content: "pasted answer"})
	result := host.Handle(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("pasted custom answer did not submit")
	}
	message := result.Cmd().(ResultMsg)
	if got, want := message.Result.Answers["1"], "pasted answer"; got != want {
		t.Fatalf("custom answer=%q, want %q", got, want)
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

	// The option row is always painted — clicking it commits directly, no
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
	for _, want := range []string{"1. Color", "2. Note", "red", "blue", "Write your response"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("pane missing %q:\n%s", want, painted)
		}
	}
	if strings.ContainsAny(painted, "▾▴❯") {
		t.Fatalf("deck painted a dropdown/marker glyph:\n%s", painted)
	}
	// Stacked rows sit on consecutive lines — every answer visible at once.
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

	// First Esc steps back to the option rows — the edit stays intact and
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

// Under tmux without extended keys Ctrl+Enter arrives as plain Enter, so the
// write-in also commits on Alt+Enter, which survives every multiplexer.
func TestScreenWriteInCommitsOnAltEnter(t *testing.T) {
	screen := New(theme.DefaultDark())
	if got := screen.Reset(`[{"question":"Add a note"}]`); got != 1 {
		t.Fatalf("Reset()=%d, want 1", got)
	}
	host := newTestHost(t, screen)
	host.Focus(InputID)
	for _, r := range "note" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	result := host.Handle(tea.KeyPressMsg{Mod: tea.ModAlt, Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("Alt+Enter did not commit the write-in answer")
	}
	message, ok := result.Cmd().(ResultMsg)
	if !ok || message.Result.Answers["1"] != "note" {
		t.Fatalf("commit result=%#v", result.Cmd())
	}
}
