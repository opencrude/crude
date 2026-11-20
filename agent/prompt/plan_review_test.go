package prompt

import (
	"strings"
	"testing"
)

func TestPlanReviewFeedbackCarriesQuotesEditsAndReadablePlan(t *testing.T) {
	plan := "# Cache\n\n- Keep <old> keys & values\n- Add a migration"
	edits := "--- a/plan.md\n+++ b/plan.md\n@@ -3 +3,2 @@\n+- Add a migration"
	text := PlanReviewFeedback(2, plan, edits, []PlanFeedbackComment{
		{FirstLine: 3, LastLine: 3, Quote: "Keep <old> keys", Note: "Drop old keys."},
		{FirstLine: 4, LastLine: 5, Quote: "migration", Note: "Explain the rollback.", Changed: true},
	})
	for _, want := range []string{
		"(revision 2)", "Address every comment, keep my edits",
		"<plan_review revision=\"2\">", "<comment lines=\"3\">", "<quoted_text>Keep &lt;old&gt; keys</quoted_text>",
		"<note>Drop old keys.</note>", "<comment lines=\"4-5\" text_changed=\"true\">",
		"<my_edits>\n--- a/plan.md\n+++ b/plan.md\n", "<plan>\n# Cache\n\n- Keep &lt;old&gt; keys &amp; values\n- Add a migration\n</plan>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("feedback lacks %q:\n%s", want, text)
		}
	}
	if only := PlanReviewFeedback(1, plan, edits, nil); !strings.Contains(only, "I edited the plan.") || strings.Contains(only, "<comments>") {
		t.Fatalf("edit-only feedback:\n%s", only)
	}
}

func TestPlanImplementationEditedNamesTheApprovedText(t *testing.T) {
	text := PlanImplementationEdited("# Cache\n- Step", "+- Step")
	if !strings.HasPrefix(text, "Implement the plan.") || !strings.Contains(text, "<approved_plan>\n# Cache\n- Step\n</approved_plan>") || !strings.Contains(text, "<my_edits>\n+- Step\n</my_edits>") {
		t.Fatalf("edited implementation:\n%s", text)
	}
}
