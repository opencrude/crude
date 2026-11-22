package popup

import (
	"encoding/json"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/types/setting"
)

// A section reset installs a replacement model on the surface. Later events
// must reach that replacement: re-installing the window's original model
// resurrected the reset values (Workspace › Editor showed "Word wrap On"
// again after the first pointer move) and made the next Save write them.
func TestEventsReachTheSurfacesCurrentModel(t *testing.T) {
	app := setting.App{}
	setting.ApplyAppDefaults(&app)
	stored := setting.WorkspaceOverrides{Editor: json.RawMessage(`{"word_wrap":true}`)}
	window := NewWorkspaceSettingsScoped(theme.DefaultDark(), setting.MergeAppWorkspace(app, stored), nil, setting.ScopeWorkspace, app, stored)
	original := window.ModelForTest()
	// The page only filters pending edits; this model has none.
	reset := original.SectionResetModel(app, app, setting.WorkspaceOverrides{}, -1)
	window.surface.SetModel(reset)

	window.HandleEvent(tea.MouseMotionMsg{X: 3, Y: 3})

	if window.ModelForTest() != reset || window.surface.Model() != reset {
		t.Fatal("an event re-installed the window's original settings model")
	}
	if reset.AppSettings().Editor.WordWrap {
		t.Fatal("the reset model must show the inherited value")
	}
}
