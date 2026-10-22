package workspace

import "testing"

// A human may leave any question unanswered, so one skipped question must not
// reject the whole response: the model receives the answered questions in
// order and an explicit "(no answer)" for the skipped one.
func TestFormatQuestionAnswersReportsSkippedQuestions(t *testing.T) {
	text, ok := formatQuestionAnswers(
		map[string]string{"1": " red ", "2": ""},
		map[string]string{"1": "Choose a color", "2": "Add a note"},
	)
	if !ok {
		t.Fatal("answers containing a deliberate skip were rejected")
	}
	want := "Q1: Choose a color\nA1: red\n\nQ2: Add a note\nA2: (no answer)"
	if text != want {
		t.Fatalf("question result=%q, want %q", text, want)
	}

	if text, ok := formatQuestionAnswers(nil, nil); ok || text != "" {
		t.Fatalf("an empty answer set produced %q (ok=%v)", text, ok)
	}
}
