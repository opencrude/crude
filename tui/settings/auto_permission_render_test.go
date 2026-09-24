package settings

import (
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestAutoPermissionControlsPaintInSafetySection(t *testing.T) {
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
		for _, text := range []string{"Auto permissions", "Denied review", "Output token cap", "Save", "Cancel"} {
			if !bufferContains(buf, text) {
				t.Errorf("width %d missing rendered %q:\n%s", width, text, bufferPlain(buf))
			}
		}
	}
}
