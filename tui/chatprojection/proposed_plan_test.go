package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
)

func TestProposedPlanRendersAsTitledSectionWithoutTags(t *testing.T) {
	md := rendering.MakeMdRenderer(80, theme.DefaultDark())
	rendered, ok := renderMarkdown("Here is the plan.\n\n<proposed_plan>\n# Rename the cache\n\n- Move it\n</proposed_plan>\n", md)
	if !ok {
		t.Fatal("plan reply did not render as markdown")
	}
	plain := rendering.StripAnsi(rendered)
	if strings.Contains(plain, "proposed_plan") || !strings.Contains(plain, "Proposed plan") || !strings.Contains(plain, "Rename the cache") {
		t.Fatalf("proposed plan rendering:\n%s", plain)
	}
}
