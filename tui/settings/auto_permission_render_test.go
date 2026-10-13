package settings

import (
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestSecurityPagesSeparateCommandSafetyFromAutoReviewPolicy(t *testing.T) {
	for _, width := range []int{90, 140} {
		th := theme.DefaultDark()
		model := New(th, types.AppSettings{}, nil)
		model.SelectCategory(catSafety)
		root := NewSurface(th, model)
		host := ui.NewHost(th, root)
		host.Resize(width, 42)
		host.Layout()
		buf := surface.NewCellBuffer(width, 42)
		host.Paint(buf)
		for _, text := range []string{"Command policy", "Unclassified commands", "Save", "Close"} {
			if !bufferContains(buf, text) {
				t.Errorf("width %d missing rendered %q:\n%s", width, text, bufferPlain(buf))
			}
		}
		model.SelectCategory(catAutoReviewPolicy)
		root.refreshForm()
		host.MarkLayout()
		host.Layout()
		buf.Clear()
		host.Paint(buf)
		for _, text := range []string{"Review outcomes", "When unsure", "Thinking effort", "Technical recovery", "First attempt budget"} {
			if !bufferContains(buf, text) {
				t.Errorf("width %d missing Auto Review policy %q", width, text)
			}
		}
		if bufferContains(buf, "Protections") {
			t.Errorf("width %d still renders the non-interactive Protections page", width)
		}
	}
}
