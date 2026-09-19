package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// A structured planning failure must read as a red contract dot plus a short
// reason — never as the canonical JSON payload that addresses the model.
func TestPlanToolFailureShowsRedDotAndReasonNotPayload(t *testing.T) {
	payload := `{"current_plan":{"id":"normalize-weird-filenames","revision":2,"objective":"Find weirdly-named files",` +
		`"status":"active","steps":[{"id":"audit","description":"Audit repo","status":"completed"}]},` +
		`"error":"revision_conflict","expected_revision":1,"received_revision":2}`
	for _, phase := range []string{types.ToolPreviewError, types.ToolPreviewDone} {
		msg := types.ChatMessage{
			InternalID: "p1", Role: types.RoleTool, ToolName: "update_plan",
			ToolStatus: types.ToolStatusError, ToolPreviewPhase: phase,
			ToolArgs: "normalize-weird-filenames", Content: payload, ToolDuration: time.Second,
		}
		ml := renderToolMessageLines(msg, theme.DefaultDark(), 100, 100, 100, 0, 20, "done", DisclosureCollapsed)
		if len(ml.native) == 0 || len(ml.native[0].Spans) < 2 {
			t.Fatalf("phase %s title row = %#v", phase, ml.native)
		}
		if role := ml.native[0].Spans[1].Role; role != ui.ColorError {
			t.Fatalf("phase %s failure dot role = %v, want error", phase, role)
		}
		plain := strings.Join(ml.lines, "\n")
		for _, want := range []string{"Update plan failed", "revision conflict (expected 1, received 2)"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("phase %s row missing %q:\n%s", phase, want, plain)
			}
		}
		for _, leaked := range []string{"current_plan", "{", "}"} {
			if strings.Contains(plain, leaked) {
				t.Fatalf("phase %s row dumped payload %q:\n%s", phase, leaked, plain)
			}
		}
		if len(ml.lines) != 1 {
			t.Fatalf("phase %s failure row must stay one line:\n%s", phase, plain)
		}
	}
}

// An unstructured planning failure keeps its own message: the reason is only
// replaced when a structured payload can actually explain it.
func TestPlanToolPlainFailureKeepsMessage(t *testing.T) {
	msg := types.ChatMessage{
		InternalID: "p1", Role: types.RoleTool, ToolName: "update_plan",
		ToolStatus: types.ToolStatusError, ToolPreviewPhase: types.ToolPreviewError,
		Content: "Error: update_plan: 'plan_id' is required",
	}
	ml := renderToolMessageLines(msg, theme.DefaultDark(), 100, 100, 100, 0, 20, "done", DisclosureCollapsed)
	plain := strings.Join(ml.lines, "\n")
	if !strings.Contains(plain, "'plan_id' is required") {
		t.Fatalf("plain failure message lost:\n%s", plain)
	}
	if role := ml.native[0].Spans[1].Role; role != ui.ColorError {
		t.Fatalf("plain failure dot role = %v, want error", role)
	}
}
