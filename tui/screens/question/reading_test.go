package question

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"github.com/charmbracelet/x/ansi"
)

func questionHost(t *testing.T, items []Item, width, height int) (*Screen, *ui.Host) {
	t.Helper()
	screen := New(theme.DefaultDark())
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := screen.ResetWithError(string(raw)); err != nil {
		t.Fatal(err)
	}
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(width, height)
	host.Layout()
	host.Focus(screen.InitialFocus())
	return screen, host
}

func paintQuestion(host *ui.Host, width, height int) *surface.CellBuffer {
	buf := surface.NewCellBuffer(width, height)
	host.Paint(buf)
	return buf
}

func clickQuestionText(t *testing.T, host *ui.Host, width, height int, text string) ui.EventResult {
	t.Helper()
	buf := paintQuestion(host, width, height)
	x, y, ok := bufferFind(buf, text)
	if !ok {
		t.Fatalf("missing target %q:\n%s", text, bufferText(buf))
	}
	result := host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if release := host.Handle(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}); release.Cmd != nil {
		t.Fatal("release repeated a question action")
	}
	return result
}

func TestQuestionLongContentIsFullyReachableAndBounded(t *testing.T) {
	for _, width := range []int{24, 48, 80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			// Source markers are independent of the wrapping implementation.
			question := "QUESTION_BEGIN\n" + strings.Repeat("Readable question body with deliberate paragraphs.\n", 250) + "QUESTION_END"
			option := "OPTION_BEGIN " + strings.Repeat("continuous-answer-word-", 150) + " OPTION_END"
			screen, host := questionHost(t, []Item{{Question: question, Options: []string{option, "Short choice"}}}, width, 12)
			seen := map[string]bool{}
			scroll := screen.options.Scroll()
			for offset := 0; offset <= scroll.MaxOffset(); offset++ {
				scroll.ScrollTo(offset)
				buf := paintQuestion(host, width, 12)
				text := bufferText(buf)
				for _, marker := range []string{"QUESTION_BEGIN", "QUESTION_END", "OPTION_BEGIN", "OPTION_END", "Short choice", "Write your response"} {
					seen[marker] = seen[marker] || strings.Contains(text, marker)
				}
				if !bufferContains(buf, "Review") || !bufferContains(buf, "Cancel") {
					t.Fatalf("scroll displaced pinned actions:\n%s", text)
				}
				// Every visible source row stays strictly above the input/hint/actions.
				for y := screen.actions.Bounds().Y; y < 12; y++ {
					for x := 0; x < width; x++ {
						if buf.Get(x, y).Char == 'Q' {
							t.Fatal("question painted into the action bar")
						}
					}
				}
			}
			for marker, found := range seen {
				if !found {
					t.Errorf("full content never reachable: %s", marker)
				}
			}
			// Repainting/layout must not snap a user's read position back to a choice.
			scroll.ScrollTo(7)
			host.MarkLayout()
			host.Layout()
			if scroll.Offset() != 7 {
				t.Fatal("unchanged layout reset explicit reading scroll")
			}
		})
	}
}

func TestQuestionWrappedChoiceContinuationHitsExactAnswer(t *testing.T) {
	answer := "Alpha option is deliberately long enough to span multiple lines, with a unique CONTINUATION marker."
	screen, host := questionHost(t, []Item{{Question: "Choose?", Options: []string{answer, "Beta"}}}, 48, 12)
	scroll := screen.options.Scroll()
	var x, y int
	found := false
	for offset := 0; offset <= scroll.MaxOffset(); offset++ {
		scroll.ScrollTo(offset)
		x, y, found = bufferFind(paintQuestion(host, 48, 12), "CONTINUATION")
		if found {
			break
		}
	}
	if !found {
		t.Fatal("wrapped choice continuation never painted")
	}
	result := host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if result.Cmd != nil || !screen.Reviewing() || screen.Answer(0) != answer {
		t.Fatal("continuation click did not retain exact answer for review")
	}
	if result := host.Handle(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}); result.Cmd != nil || !screen.Reviewing() {
		t.Fatal("release submitted or left review")
	}
}

func TestQuestionReviewIsMandatoryEditableAndComplete(t *testing.T) {
	screen, host := questionHost(t, []Item{
		{Header: "First", Question: "FIRST_QUESTION", Options: []string{"Alpha", "Beta"}},
		{Header: "Second", Question: "SECOND_QUESTION"},
	}, 90, 16)
	if res := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.submit"}); res.Cmd != nil {
		t.Fatal("unanswered form submitted outside review")
	}
	clickQuestionText(t, host, 90, 16, "Alpha")
	answer := "ANSWER_BEGIN\n" + strings.Repeat("long answer body with preserved lines\n", 100) + "ANSWER_END"
	host.Handle(tea.PasteMsg{Content: answer})
	result := clickQuestionText(t, host, 90, 16, "Review answers")
	if result.Cmd != nil || !screen.Reviewing() {
		t.Fatal("last answer submitted instead of reviewing")
	}
	seen := map[string]bool{}
	for offset := 0; offset <= screen.options.Scroll().MaxOffset(); offset++ {
		screen.options.Scroll().ScrollTo(offset)
		text := bufferText(paintQuestion(host, 90, 16))
		for _, marker := range []string{"FIRST_QUESTION", "SECOND_QUESTION", "Alpha", "ANSWER_BEGIN", "ANSWER_END"} {
			seen[marker] = seen[marker] || strings.Contains(text, marker)
		}
	}
	for marker, ok := range seen {
		if !ok {
			t.Errorf("review lost %q", marker)
		}
	}
	screen.options.Scroll().ScrollTo(0)
	clickQuestionText(t, host, 90, 16, "1. First")
	if screen.Reviewing() || screen.Current() != 0 {
		t.Fatal("review did not open the chosen question for correction")
	}
	clickQuestionText(t, host, 90, 16, "Beta")
	if !screen.Reviewing() || screen.Answer(0) != "Beta" || screen.Answer(1) != answer {
		t.Fatal("correction did not return to review with other answers intact")
	}
	result = submitReview(t, host, screen)
	msg := result.Cmd().(ResultMsg)
	if msg.Result.Answers["1"] != "Beta" || msg.Result.Answers["2"] != answer || msg.Result.Questions["2"] != "SECOND_QUESTION" {
		t.Fatalf("reviewed result drifted: %+v", msg.Result)
	}
}

func TestQuestionReviewRejectsSkippedAndClearedAnswers(t *testing.T) {
	screen, host := questionHost(t, []Item{{Question: "One"}, {Question: "Two"}}, 80, 12)
	// Open review through the rail before answering; it remains usable but cannot send.
	clickQuestionText(t, host, 80, 12, "Review answers")
	if !screen.Reviewing() || screen.actions.ActionEnabled("question.submit") {
		t.Fatal("incomplete review enabled Submit")
	}
	if result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "question.submit"}); result.Cmd != nil {
		t.Fatal("incomplete review submitted via stale action")
	}
	clickQuestionText(t, host, 80, 12, "1.")
	host.Handle(tea.PasteMsg{Content: "draft"})
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if screen.Answer(0) != "" {
		t.Fatal("cleared answer remained submittable")
	}
}

func TestQuestionReaderUnicodeResizeScrollAndNativeGutter(t *testing.T) {
	text := "你好 👩‍💻 é\n" + strings.Repeat("unicode body ", 80) + "TAIL"
	screen, host := questionHost(t, []Item{{Question: text, Options: []string{"OK"}}}, 80, 12)
	screen.options.Scroll().ScrollTo(3)
	host.Resize(36, 10)
	host.Layout()
	if screen.railVisible || screen.paneX != 0 {
		t.Fatal("narrow layout starved content with the question rail")
	}
	buf := paintQuestion(host, 36, 10)
	if !bufferContains(buf, "Review answers") || !bufferContains(buf, "Cancel") {
		t.Fatalf("narrow actions clipped:\n%s", bufferText(buf))
	}
	host.Focus(OptionsID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
	buf = paintQuestion(host, 36, 10)
	if !strings.Contains(bufferText(buf), "你") {
		t.Fatal("unicode question is not readable after resize/home")
	}
	before := screen.options.Scroll().Offset()
	r, _ := host.ScreenRect(OptionsID)
	x, y := r.X+r.W-1, r.Y+r.H-1
	host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if screen.options.Scroll().Offset() <= before || screen.Answer(0) != "" {
		t.Fatal("native gutter failed to scroll or activated an option")
	}
	host.Resize(100, 18)
	host.Layout()
	if !screen.railVisible || screen.options.Scroll().Offset() > screen.options.Scroll().MaxOffset() {
		t.Fatal("enlarging did not restore rail and bounded scroll")
	}
}

func TestQuestionTinyBoundsNeverPaintOutsideScreen(t *testing.T) {
	for width := 1; width < 70; width++ {
		for height := 0; height < 14; height++ {
			screen, _ := questionHost(t, []Item{{Question: "Long body", Options: []string{"Long answer", "No"}}}, width, height)
			screen.Layout(ui.LayoutContext{}, ui.Rect{X: 3, Y: 2, W: width, H: height})
			buf := surface.NewCellBuffer(width+6, height+4)
			sentinel := cell.NewCell('#', cell.NoColor, cell.NoColor, 0)
			buf.Fill(sentinel)
			ui.PaintTree(screen, buf, 0, 0, ui.PaintContext{})
			for y := 0; y < buf.Height(); y++ {
				for x := 0; x < buf.Width(); x++ {
					if (x < 3 || x >= width+3 || y < 2 || y >= height+2) && buf.Get(x, y).Char != '#' {
						t.Fatalf("paint escaped %dx%d at %d,%d", width, height, x, y)
					}
				}
			}
		}
	}
}

func TestQuestionMultipleCanDeselectCustomWithoutLosingDraft(t *testing.T) {
	s, host := questionHost(t, []Item{{Question: "Choose", Options: []string{"Known"}, Multiple: true}}, 80, 14)
	clickQuestionText(t, host, 80, 14, "Known")
	clickQuestionText(t, host, 80, 14, "Write your response")
	host.Handle(tea.PasteMsg{Content: "Retained write-in"})
	clickQuestionText(t, host, 80, 14, "Write your response")
	if s.Answer(0) != "Known" || s.custom[0] != "Retained write-in" || containsCustom(s.choices[0]) {
		t.Fatal("custom checkbox could not be deselected independently of its retained draft")
	}
	clickQuestionText(t, host, 80, 14, "Write your response")
	if s.Answer(0) != "Known, Retained write-in" {
		t.Fatal("reselecting custom did not restore its draft")
	}
}

func TestQuestionKeyboardMultiSelectCanVisitEveryOptionOnce(t *testing.T) {
	s, host := questionHost(t, []Item{{Question: "Pick several", Options: []string{"Alpha", "Beta", "Gamma"}, Multiple: true}}, 80, 14)
	for index := 0; index < 3; index++ {
		if index > 0 {
			host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		host.Handle(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		if s.options.SelectedIndex() != index+3 {
			t.Fatal("toggling an option reset keyboard selection to the first answer")
		}
	}
	if s.Answer(0) != "Alpha, Beta, Gamma" {
		t.Fatal("keyboard choices drifted", s.Answer(0))
	}
}

func TestQuestionWidthCalibrationReflowsWithoutChangingSource(t *testing.T) {
	defer ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{})
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{})
	s, host := questionHost(t, []Item{{Question: strings.Repeat("☰ ", 40), Options: []string{"OK"}}}, 48, 12)
	before := s.options.Scroll().Content()
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"☰": 4}})
	s.InvalidateWidthProfile()
	host.MarkLayout()
	host.Layout()
	if s.options.Scroll().Content() <= before || s.items[0].Question != strings.Repeat("☰ ", 40) {
		t.Fatal("calibration kept stale line breaks or changed the question")
	}
}

func TestQuestionMultiToggleKeepsLongChoiceReadingOffset(t *testing.T) {
	long := "Choice " + strings.Repeat("long option body ", 100) + "MARKER"
	s, host := questionHost(t, []Item{{Question: strings.Repeat("question body ", 100), Options: []string{long, "Other"}, Multiple: true}}, 48, 12)
	scroll := s.options.Scroll()
	var x, y int
	found := false
	for offset := 1; offset <= scroll.MaxOffset(); offset++ {
		scroll.ScrollTo(offset)
		x, y, found = bufferFind(paintQuestion(host, 48, 12), "MARKER")
		if found {
			break
		}
	}
	if !found {
		t.Fatal("choice tail not reachable")
	}
	before := scroll.Offset()
	host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	host.Layout()
	if scroll.Offset() != before || s.Answer(0) != long {
		t.Fatalf("toggle reset reading: before=%d after=%d", before, scroll.Offset())
	}
}

func TestQuestionResetAndDismissDoNotKeepPriorReviewOrAnswers(t *testing.T) {
	s, host := questionHost(t, []Item{{Question: "Original", Options: []string{"Yes"}}}, 80, 12)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !s.Reviewing() {
		t.Fatal("fixture did not enter review")
	}
	s.Reset(`[{"question":"Replacement","options":["No"]}]`)
	host.MarkLayout()
	text := bufferText(paintQuestion(host, 80, 12))
	if s.Reviewing() || s.Answer(0) != "" || strings.Contains(text, "Original") || !strings.Contains(text, "Replacement") {
		t.Fatal("replacement reused prior review or answers")
	}
	s.Dismiss()
	host.MarkLayout()
	if strings.Contains(bufferText(paintQuestion(host, 80, 12)), "Replacement") || len(s.Children()) != 0 {
		t.Fatal("dismissed request retained painted interactive content")
	}
}

func TestQuestionNarrowEscapeClimbsToActionsBeforeCancelling(t *testing.T) {
	for _, item := range []Item{{Question: "Text only"}, {Question: "Choice", Options: []string{"Yes"}}} {
		s, host := questionHost(t, []Item{item}, 36, 12)
		if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}); result.Cmd != nil || host.Focused() != ActionsID {
			t.Fatal("narrow answer escaped without first visiting pinned actions")
		}
		result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
		if result.Cmd == nil {
			t.Fatal("narrow actions trapped Escape without cancelling")
		}
		if message, ok := result.Cmd().(CancelMsg); !ok || message.Target != s {
			t.Fatal("narrow Escape cancelled the wrong request")
		}
	}
}

func TestQuestionManyQuestionsKeepRailAndReviewFullyReachable(t *testing.T) {
	items := make([]Item, 80)
	for index := range items {
		items[index] = Item{Header: fmt.Sprintf("Section %d", index+1), Question: fmt.Sprintf("COMPLETE_QUESTION_%d", index+1)}
	}
	s, host := questionHost(t, items, 120, 16)
	r, ok := host.ScreenRect(RailScrollID)
	if !ok || r.Empty() {
		t.Fatal("many questions have no native rail scrollbar")
	}
	host.Handle(tea.MouseClickMsg{X: r.X, Y: r.Y, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: r.X, Y: r.Y + r.H - 1, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: r.X, Y: r.Y + r.H - 1, Button: tea.MouseLeft})
	clickQuestionText(t, host, 120, 16, "80. Section")
	host.Handle(tea.PasteMsg{Content: "LAST_ANSWER"})
	clickQuestionText(t, host, 120, 16, "Review answers")
	if !s.Reviewing() || s.actions.ActionEnabled("question.submit") {
		t.Fatal("skipping many questions bypassed incomplete-review validation")
	}
	host.Focus(OptionsID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	text := bufferText(paintQuestion(host, 120, 16))
	if !strings.Contains(text, "COMPLETE_QUESTION_80") || !strings.Contains(text, "LAST_ANSWER") {
		t.Fatal("large review lost its final complete question/answer")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
	if !strings.Contains(bufferText(paintQuestion(host, 120, 16)), "COMPLETE_QUESTION_1") {
		t.Fatal("large review cannot return to its first question")
	}
}

func BenchmarkQuestionLongReflow(b *testing.B) {
	text := strings.Repeat("A very long question about deployment choices and safe defaults. ", 1024)
	raw, _ := json.Marshal([]Item{{Question: text, Options: []string{text, "Short"}}})
	s := New(theme.DefaultDark())
	s.Reset(string(raw))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Layout(ui.LayoutContext{}, ui.Rect{W: 48 + i%2, H: 16})
	}
}

func BenchmarkQuestionLongPaint(b *testing.B) {
	text := strings.Repeat("A very long question about deployment choices and safe defaults. ", 1024)
	raw, _ := json.Marshal([]Item{{Question: text, Options: []string{text, "Short"}}})
	s := New(theme.DefaultDark())
	s.Reset(string(raw))
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(80, 16)
	buf := surface.NewCellBuffer(80, 16)
	host.Paint(buf)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		host.MarkLayout()
		host.Paint(buf)
	}
}
