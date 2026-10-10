package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// The scope toggle uses arrow keys to request a scope change without becoming
// a content tab strip.
func TestScopeStripArrowsSwitchScope(t *testing.T) {
	th := theme.DefaultDark()
	model := NewScoped(th, types.AppSettings{}, nil, types.SettingsScopeApplication, types.AppSettings{}, types.WorkspaceOverrides{})
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 22)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(80, 22)
	host.Layout()
	host.Focus(WorkspaceSettingsScopeID)

	// The host bubbles the toggle's action through the surface.
	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if res.Cmd == nil {
		t.Fatalf("Right on the scope strip produced no command (action=%+v)", res.Action)
	}
	scopeMsg, ok := res.Cmd().(ScopeMsg)
	if !ok || scopeMsg.Scope != types.SettingsScopeWorkspace {
		t.Fatalf("Right produced %#v want ScopeMsg{workspace}", res.Cmd())
	}

	// Swap in the workspace-scoped model exactly like the viewmodel does when
	// it handles the ScopeMsg.
	surfaceRoot.SetModel(NewScoped(th, types.AppSettings{}, nil, types.SettingsScopeWorkspace, types.AppSettings{}, types.WorkspaceOverrides{}))
	host.Layout()

	res = host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	if res.Cmd == nil {
		t.Fatalf("Left on the scope strip produced no command (action=%+v)", res.Action)
	}
	if scopeMsg, ok := res.Cmd().(ScopeMsg); !ok || scopeMsg.Scope != types.SettingsScopeApplication {
		t.Fatalf("Left produced %#v want ScopeMsg{application}", res.Cmd())
	}
}

// The scope toggle keeps the documented region exits: Tab to search,
// Shift+Tab to actions, and Up to categories.
func TestScopeStripKeepsFocusContract(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{}, nil)
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 22)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(80, 22)
	host.Layout()

	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want ui.ComponentID
	}{
		{"tab_goes_to_search", tea.KeyPressMsg{Code: tea.KeyTab}, WorkspaceSettingsSearchID},
		{"shift_tab_goes_to_actions", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, WorkspaceSettingsActionID},
		{"up_goes_to_categories", tea.KeyPressMsg{Code: tea.KeyUp}, WorkspaceSettingsListID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host.Focus(WorkspaceSettingsScopeID)
			host.Layout()
			res := host.Handle(tc.msg)
			if res.Action != nil {
				_ = surfaceRoot.HandleAction(ui.EventContext{Theme: th}, *res.Action)
			}
			if got := host.Focused(); got != tc.want {
				t.Fatalf("focus=%q want %q", got, tc.want)
			}
		})
	}
}

// The scope switch and command buttons occupy one row. Their separator is one
// continuous rule on the row above, not a tab-bar baseline between controls.
func TestSettingsFooterHasNoBlankRowBetweenScopeAndActions(t *testing.T) {
	th := theme.DefaultDark()
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		model := NewScoped(th, types.AppSettings{}, nil, scope, types.AppSettings{}, types.WorkspaceOverrides{})
		surfaceRoot := NewSurface(th, model)
		frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 104, 28)
		surfaceRoot.SetFocusHandler(frame.Focus)
		host := frame.Host()
		host.Resize(104, 28)
		host.Layout()
		buf := surface.NewCellBuffer(104, 28)
		host.Paint(buf)

		strip := surfaceRoot.scope.Bounds()
		if strip.H != 1 || strip.Y != surfaceRoot.actions.Bounds().Y {
			t.Fatalf("scope=%s toggle=%+v actions=%+v not on same footer row", scope, strip, surfaceRoot.actions.Bounds())
		}
		labelsRow := -1
		for y := 0; y < 28; y++ {
			if strings.Contains(settingsRow(buf, y, 104), "Application") {
				labelsRow = y
				break
			}
		}
		if labelsRow < 0 {
			t.Fatalf("scope=%s footer labels missing", scope)
		}
		labels := settingsRow(buf, labelsRow, 104)
		if !strings.Contains(labels, "Workspace") {
			t.Fatalf("scope=%s footer labels=%q", scope, labels)
		}
		if rule := settingsRow(buf, labelsRow-1, 104); strings.Trim(rule, " ─│┃") != "" {
			t.Fatalf("scope=%s footer rule row carries text: %q", scope, rule)
		}
		actions := settingsRow(buf, labelsRow, 104)
		closeLabel := "[ Close ]"
		if !strings.Contains(actions, "[ Save ]") || !strings.Contains(actions, closeLabel) {
			t.Fatalf("scope=%s command row=%q", scope, actions)
		}
		appX, workX := bufferIndexOnRow(buf, labelsRow, "Application"), bufferIndexOnRow(buf, labelsRow, "Workspace")
		if appX < 0 || workX < 0 || buf.Get(appX, labelsRow).BG.Equal(buf.Get(workX, labelsRow).BG) {
			t.Fatalf("scope=%s selected and unselected toggle backgrounds are indistinguishable", scope)
		}
	}
}

func settingsRow(buf *surface.CellBuffer, y, w int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		c := buf.Get(x, y)
		if c.Char != 0 && c.Width != 0 {
			b.WriteRune(c.Char)
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}
