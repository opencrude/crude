package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/types"
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

func TestPlanTagsAbsentWithoutRoomForMarkdown(t *testing.T) {
	const text = "<proposed_plan>\n# Rename the cache\n</proposed_plan>"
	for _, live := range []bool{true, false} {
		r := NewRenderer(theme.DefaultDark())
		r.SetSize(23, 24)
		r.SetChatAreaBounds(0, 24)
		r.AppendMessage(types.ChatMessage{InternalID: "proposal", Role: types.RoleAssistant, Content: text})
		r.SetStreaming(live)
		rows, _, _ := r.ExportMessageRows(23)
		var lines []string
		for _, row := range rows {
			lines = append(lines, row.Lines...)
		}
		body := strings.Join(lines, "\n")
		if !strings.Contains(body, "Proposed plan") || strings.Contains(body, "proposed_plan") || strings.Contains(body, "</") {
			t.Errorf("live=%t: narrow proposal shows the wrong text:\n%s", live, body)
		}
	}
}
