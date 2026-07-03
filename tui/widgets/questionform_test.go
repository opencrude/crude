package widgets

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/widget"
)

const validQuestions = `[
	{"question": "What is your name?", "options": ["Alice", "Bob", "Charlie"]},
	{"question": "Favorite color?", "options": ["Red", "Blue", "Green"]},
	{"question": "Enter comment:", "header": "Comment"}
]`

func TestQuestionForm_New(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	if f.Count() != 0 {
		t.Errorf("expected count=0, got %d", f.Count())
	}
}

func TestQuestionForm_Reset_Empty(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	n := f.Reset("")
	if n != 0 {
		t.Errorf("expected 0 for empty JSON, got %d", n)
	}
}

func TestQuestionForm_Reset_Invalid(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	n := f.Reset("{bad json}")
	if n != 0 {
		t.Errorf("expected 0 for invalid JSON, got %d", n)
	}
}

func TestQuestionForm_Reset_Valid(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	n := f.Reset(validQuestions)
	if n != 3 {
		t.Errorf("expected 3 questions, got %d", n)
	}
	if f.Count() != 3 {
		t.Errorf("expected Count()=3, got %d", f.Count())
	}
}

func TestQuestionForm_Focused_SetFocused(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	if f.Focused() != 0 {
		t.Errorf("expected focused=0, got %d", f.Focused())
	}

	f.SetFocused(1)
	if f.Focused() != 1 {
		t.Errorf("expected focused=1, got %d", f.Focused())
	}

	f.SetFocused(999)
	if f.Focused() != 1 {
		t.Errorf("expected focused unchanged for out-of-range, got %d", f.Focused())
	}
}

func TestQuestionForm_FocusNext_FocusPrev(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	f.FocusNext()
	if f.Focused() != 1 {
		t.Errorf("expected focused=1 after FocusNext, got %d", f.Focused())
	}

	f.FocusNext()
	if f.Focused() != 2 {
		t.Errorf("expected focused=2 after FocusNext, got %d", f.Focused())
	}

	f.FocusNext()
	if f.Focused() != 0 {
		t.Errorf("expected focused=0 (wrap), got %d", f.Focused())
	}

	f.FocusPrev()
	if f.Focused() != 2 {
		t.Errorf("expected focused=2 after FocusPrev, got %d", f.Focused())
	}
}

func TestQuestionForm_FocusNext_FocusPrev_Empty(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)

	f.FocusNext()
	if f.Focused() != 0 {
		t.Errorf("expected focused=0 on empty form, got %d", f.Focused())
	}

	f.FocusPrev()
	if f.Focused() != 0 {
		t.Errorf("expected focused=0 on empty form, got %d", f.Focused())
	}
}

func TestQuestionForm_IsTextQuestion(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	if f.IsTextQuestion(0) {
		t.Error("expected question 0 to be option type")
	}
	if f.IsTextQuestion(1) {
		t.Error("expected question 1 to be option type")
	}
	if !f.IsTextQuestion(2) {
		t.Error("expected question 2 to be text type")
	}
	if f.IsTextQuestion(999) {
		t.Error("expected false for out-of-range index")
	}
}

func TestQuestionForm_SetAnswer_Answer(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	f.SetAnswer(0, "Alice")
	if f.Answer(0) != "Alice" {
		t.Errorf("expected answer Alice, got %q", f.Answer(0))
	}

	f.SetAnswer(999, "test")
	if f.Answer(999) != "" {
		t.Errorf("expected empty for out-of-range, got %q", f.Answer(999))
	}
}

func TestQuestionForm_Answered_IsAnswered(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	if f.Answered() != 0 {
		t.Errorf("expected 0 answered, got %d", f.Answered())
	}

	f.SetAnswer(0, "Alice")
	if f.Answered() != 1 {
		t.Errorf("expected 1 answered, got %d", f.Answered())
	}
	if !f.IsAnswered(0) {
		t.Error("expected question 0 to be answered")
	}
	if f.IsAnswered(1) {
		t.Error("expected question 1 to be unanswered")
	}
	if f.IsAnswered(999) {
		t.Error("expected false for out-of-range")
	}
}

func TestQuestionForm_AllAnswered(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	if f.AllAnswered() {
		t.Error("expected not all answered initially")
	}

	f.SetAnswer(0, "Alice")
	f.SetAnswer(1, "Red")
	f.SetAnswer(2, "hello")
	if !f.AllAnswered() {
		t.Error("expected all answered")
	}
}

func TestQuestionForm_AllAnswered_Empty(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	if f.AllAnswered() {
		t.Error("expected false for empty form")
	}
}

func TestQuestionForm_BuildResult(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)
	f.SetAnswer(0, "Alice")
	f.SetAnswer(1, "Red")

	r := f.BuildResult()
	if r.Cancelled {
		t.Error("expected cancelled=false")
	}
	if r.Answers["1"] != "Alice" {
		t.Errorf("expected answer 1=Alice, got %q", r.Answers["1"])
	}
	if r.Answers["2"] != "Red" {
		t.Errorf("expected answer 2=Red, got %q", r.Answers["2"])
	}
	if r.Questions["1"] != "What is your name?" {
		t.Errorf("expected question 1, got %q", r.Questions["1"])
	}
}

func TestQuestionForm_Update_Navigation(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	f, _, done := f.Update(keyMsg(tea.KeyDown))
	if done {
		t.Error("expected done=false on Down")
	}
	if f.optionCursor[0] != 1 {
		t.Errorf("expected cursor=1 after Down, got %d", f.optionCursor[0])
	}

	f, _, done = f.Update(keyMsg(tea.KeyUp))
	if done {
		t.Error("expected done=false on Up")
	}
	if f.optionCursor[0] != 0 {
		t.Errorf("expected cursor=0 after Up, got %d", f.optionCursor[0])
	}
}

func TestQuestionForm_Update_Tab(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	f, _, done := f.Update(keyMsg(tea.KeyTab))
	if done {
		t.Error("expected done=false on Tab")
	}
	if f.Focused() != 1 {
		t.Errorf("expected focused=1 after Tab, got %d", f.Focused())
	}

	f, _, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if f.Focused() != 0 {
		t.Errorf("expected focused=0 after Shift+Tab, got %d", f.Focused())
	}
}

func TestQuestionForm_Update_SelectOptionAndSubmit(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(`[{"question": "Pick one", "options": ["X", "Y"]}]`)

	f, result, done := f.Update(keyMsg(tea.KeyEnter))
	if !done {
		t.Error("expected done=true after selecting option (all answered)")
	}
	if f.Answer(0) != "X" {
		t.Errorf("expected answer X, got %q", f.Answer(0))
	}
	if result.Cancelled {
		t.Error("expected not cancelled")
	}
}

func TestQuestionForm_Update_EscCancels(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	_, result, done := f.Update(keyMsg(tea.KeyEsc))
	if !done {
		t.Error("expected done=true on Esc")
	}
	if !result.Cancelled {
		t.Error("expected Cancelled=true")
	}
}

func TestQuestionForm_Update_LeftRightOnSubmitCancel(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(`[{"question": "Pick", "options": ["A", "B"]}]`)

	// ownRow = 2 (len(options)), submitRow = 3, cancelRow = 4
	submitRow := len(f.items[0].Options) + 1
	cancelRow := len(f.items[0].Options) + 2
	f.optionCursor[0] = submitRow

	f, _, done := f.Update(keyMsg(tea.KeyRight))
	if done {
		t.Error("expected done=false")
	}
	if f.optionCursor[0] != cancelRow {
		t.Errorf("expected cursor=%d (cancel), got %d", cancelRow, f.optionCursor[0])
	}

	f, _, _ = f.Update(keyMsg(tea.KeyLeft))
	if f.optionCursor[0] != submitRow {
		t.Errorf("expected cursor=%d (submit), got %d", submitRow, f.optionCursor[0])
	}
}

func TestQuestionForm_Update_EnterOnCancel(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(`[{"question": "Pick", "options": ["A", "B"]}]`)

	cancelRow := len(f.items[0].Options) + 2
	f.optionCursor[0] = cancelRow

	_, result, done := f.Update(keyMsg(tea.KeyEnter))
	if !done {
		t.Error("expected done=true on cancel")
	}
	if !result.Cancelled {
		t.Error("expected Cancelled=true")
	}
}

func TestQuestionForm_View_NotEmpty(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	v := f.View(0, 80)
	if v == "" {
		t.Fatal("expected non-empty view")
	}
}

func TestQuestionForm_View_Empty(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)

	v := f.View(0, 80)
	if v != "" {
		t.Errorf("expected empty view for empty form, got %q", v)
	}
}

func TestQuestionForm_Render(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	surf := surface.NewSurface("test", 80, 24, cell.BlankCell(rendering.ThemeColorToCell(th.Colors.Bg)))
	ctx := widget.NewRenderContext(th)
	f.Render(surf, ctx)

	buf := surf.Buffer()
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
}

func TestQuestionForm_Measure(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	w, h := f.Measure(100, 50)
	if w != 100 {
		t.Errorf("expected width=100, got %d", w)
	}
	if h != 50 {
		t.Errorf("expected height=50, got %d", h)
	}
}

func TestQuestionForm_Dismiss(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)
	f.SetAnswer(0, "Alice")
	f.Dismiss()

	if f.Count() != 0 {
		t.Errorf("expected count=0 after Dismiss, got %d", f.Count())
	}
	if f.Focused() != 0 {
		t.Errorf("expected focused=0 after Dismiss, got %d", f.Focused())
	}
}

func TestQuestionForm_Hint(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)

	if h := f.Hint(); h != "" {
		t.Errorf("expected empty hint for empty form, got %q", h)
	}

	f.Reset(validQuestions)
	h := f.Hint()
	if h == "" {
		t.Error("expected non-empty hint for valid form")
	}
	if !strings.Contains(h, "navigate") {
		t.Error("expected hint to mention navigation")
	}
}

func TestQuestionForm_ExitTextMode(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	f.Reset(validQuestions)

	f.ExitTextMode(2)
	if f.textMode[2] {
		t.Error("expected textMode[2]=false after ExitTextMode")
	}
}

// TestQuestionForm_TextMode_AcceptsKeystrokes verifies the fix for
// the "Type your own answer" bug. Previously, when textMode[i]=true
// the Update handler early-returned before doing anything, so
// keystrokes were silently dropped. Now the per-question TextInput
// receives the keys and the answer is captured on Enter.
func TestQuestionForm_TextMode_AcceptsKeystrokes(t *testing.T) {
	th := testTheme()
	f := NewQuestionForm(th)
	// Reset with a question that has no options (forces textMode[0]=true).
	f.Reset(`[{"question":"What's your name?","header":"Name"}]`)
	if !f.textMode[0] {
		t.Fatal("expected textMode[0]=true for option-less question")
	}
	// Type characters one at a time. The TextInput reads msg.Text
	// for character insertion (not msg.Code), so we put the rune
	// in the Text field.
	for _, r := range "Alice" {
		f, _, _ = f.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	// Sanity check: the per-question input should now contain "Alice".
	if got := f.textInputs[0].Value(); got != "Alice" {
		t.Fatalf("expected input value 'Alice', got %q", got)
	}
	// Enter the question to confirm.
	f, result, done := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done {
		t.Fatal("expected done=true after Enter on text-mode question")
	}
	if result.Answers["1"] != "Alice" {
		t.Errorf("expected answer 'Alice', got %q", result.Answers["1"])
	}
}
