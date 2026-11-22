package conversation

import "testing"

func TestProposedPlanTakesTheLastStandaloneBlock(t *testing.T) {
	reply := "Draft:\n<proposed_plan>\n# Old\n</proposed_plan>\nRevised:\n  <proposed_plan>\n# Fix add\n\n- Change calc.py\n</proposed_plan>\nDone."
	plan, ok := ProposedPlan(reply)
	if !ok || plan != "# Fix add\n\n- Change calc.py" {
		t.Fatalf("plan=%q ok=%v", plan, ok)
	}
	for _, reply := range []string{"no plan", "inline <proposed_plan>x</proposed_plan>", "<proposed_plan>\n\n</proposed_plan>", "<proposed_plan>\nunclosed"} {
		if _, ok := ProposedPlan(reply); ok {
			t.Fatalf("accepted %q", reply)
		}
	}
}

func TestPlanTitleUsesTheFirstHeading(t *testing.T) {
	for plan, want := range map[string]string{
		"Intro line\n\n## Rename the **cache**\n- step": "Rename the **cache**",
		"# Fix add\n- Return a + b":                     "Fix add",
		"Just a list\n- one":                            "Just a list",
		"#hashtag line":                                 "#hashtag line",
		"\n\n":                                          "Plan",
	} {
		if got := PlanTitle(plan); got != want {
			t.Errorf("PlanTitle(%q) = %q, want %q", plan, got, want)
		}
	}
}

func TestPlanRevisionPendingComments(t *testing.T) {
	revision := PlanRevision{Comments: []PlanComment{{ID: "a"}, {ID: "b", Sent: true}, {ID: "c"}}}
	if got := revision.PendingComments(); got != 2 {
		t.Fatalf("pending = %d", got)
	}
	review := PlanReview{Revisions: []PlanRevision{{Number: 1, Path: "/p/a-v1.md"}, {Number: 2, Path: "/p/a-v2.md", Outcome: PlanOutcomeImplemented}}}
	if latest, ok := review.Latest(); !ok || latest.Number != 2 || latest.Open() {
		t.Fatalf("latest = %+v", latest)
	}
	if r, ok := review.RevisionAtPath("/p/a-v1.md"); !ok || r.Number != 1 {
		t.Fatalf("by path = %+v", r)
	}
	if _, ok := review.RevisionAtPath(""); ok {
		t.Fatal("empty path matched a revision")
	}
}
