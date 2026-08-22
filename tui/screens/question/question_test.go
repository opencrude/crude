package question

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestScreenSelectsOptionsAndReturnsAllAnswers(t *testing.T) {
	screen := New(theme.DefaultDark())
	if got := screen.Reset(`[
		{"question":"Choose a color","header":"Color","options":["red","blue"]},
		{"question":"Add a note"}
	]`); got != 2 {
		t.Fatalf("Reset()=%d, want 2", got)
	}

	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "question", Title: "Questions", Content: screen, Policy: ui.PanelBlocking})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 12)
	host.Layout()
	host.Focus(FormID)

	// Enter opens the native select; Enter again commits the first option and
	// advances to the next question through typed form actions.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if screen.Current() != 1 || screen.Answer(0) != "red" {
		t.Fatalf("after option commit index=%d answer=%q", screen.Current(), screen.Answer(0))
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.previous"})
	if screen.Current() != 0 || screen.formModel.Field(answerFieldID).Value.String() != "option:0" {
		t.Fatalf("back navigation lost option selection: index=%d value=%q", screen.Current(), screen.formModel.Field(answerFieldID).Value.String())
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.next"})
	if screen.Current() != 1 {
		t.Fatal("next navigation did not restore the subsequent question")
	}

	for _, r := range "note" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
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

func TestScreenMultiSelectValidatesAndReturnsSelectedOptions(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[{"question":"Choose all","options":["alpha","beta"],"multiple":true}]`)

	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "question", Title: "Questions", Content: screen, Policy: ui.PanelBlocking})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 12)
	host.Layout()
	host.Focus(FormID)

	// Open the shared multi-select list, toggle the first two options, then
	// close it before submitting through the shared action model.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.next"})
	if result.Cmd == nil {
		t.Fatal("multi-select submit did not emit a result")
	}
	message := result.Cmd().(ResultMsg)
	if got, want := message.Result.Answers["1"], "alpha, beta"; got != want {
		t.Fatalf("multi-select answer=%q, want %q", got, want)
	}

	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	if !bufferContains(buf, "Choose all") {
		t.Fatal("native question form did not paint its question")
	}
}

func TestScreenCustomAnswerRequiresTextAndAcceptsPaste(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[{"question":"Choose or type","options":["known"]}]`)
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "question", Title: "Questions", Content: screen, Policy: ui.PanelBlocking})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 12)
	host.Layout()
	host.Focus(FormID)

	// The custom choice follows the suggested options in the retained List.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.next"}); result.Cmd != nil {
		t.Fatal("empty custom answer bypassed validation")
	}
	host.Handle(tea.PasteMsg{Content: "pasted answer"})
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "question", Title: "Questions", Content: screen, Policy: ui.PanelBlocking})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 12)
	host.Layout()
	host.Focus(FormID)

	// Open the shared option dropdown with the keyboard, then click the
	// painted option row wherever the two-pane (rail + form) layout placed
	// it — the option pick hit test must land inside the visible dropdown.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	x, y, ok := bufferFind(buf, "mouse choice")
	if !ok {
		t.Fatalf("dropdown option not painted:\n%s", bufferText(buf))
	}
	host.Handle(tea.MouseClickMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	if screen.Current() != 1 || screen.Answer(0) != "mouse choice" {
		t.Fatalf("mouse selection index=%d answer=%q", screen.Current(), screen.Answer(0))
	}
}

func TestScreenRailShowsAnswerStateAndSelectsQuestions(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.Reset(`[
		{"question":"Color","header":"Color","options":["red"]},
		{"question":"Note"}
	]`)
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "question", Title: "Questions", Content: screen, Policy: ui.PanelBlocking})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 12)
	host.Layout()
	host.Focus(FormID)

	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)
	painted := bufferText(buf)
	for _, want := range []string{"1. Color", "2. Note"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("rail missing %q:\n%s", want, painted)
		}
	}
	if strings.Contains(painted, "❯") {
		t.Fatalf("rail painted a marker glyph:\n%s", painted)
	}

	// Answer Q1 through the shared select; the rail icon flips to ✓ and the
	// screen advances to Q2 (rail selection follows).
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if screen.Current() != 1 || screen.Answer(0) != "red" {
		t.Fatalf("after option commit index=%d answer=%q", screen.Current(), screen.Answer(0))
	}
	railItems := screen.rail.Items()
	if len(railItems) != 3 || railItems[0].Icon != "✓" || railItems[2].Icon != "○" {
		t.Fatalf("rail icons after answering: %+v", railItems)
	}

	// Activating a rail row jumps to that question (entry, then separator).
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.rail.select", Payload: ui.ListItem{ID: "rail/0", Label: "1. Color", Payload: 0}})
	if screen.Current() != 0 {
		t.Fatalf("rail select stayed on index %d, want 0", screen.Current())
	}
	if screen.formModel.Field(answerFieldID).Value.String() != "option:0" {
		t.Fatalf("rail select lost the prior answer: %q", screen.formModel.Field(answerFieldID).Value.String())
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
		}
	}
}

func bufferContains(buf *surface.CellBuffer, want string) bool {
	_, _, ok := bufferFind(buf, want)
	return ok
}

// bufferFind locates the first pixel of a painted run of text, returning its
// cell coordinates. Used to compute mouse hit targets whose geometry depends
// on the live layout (rail width, form pane offset, dropdown position).
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
