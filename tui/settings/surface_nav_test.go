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

func TestSettingsNavigation_UpDownFromSearchMovesCategories(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Theme: types.ThemeSettings{Preset: "dark"}}, nil)
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 22)
	surfaceRoot.SetFocusHandler(frame.Focus)
	surfaceRoot.FocusSearch()
	host := frame.Host()
	host.Resize(80, 22)
	host.Layout()

	start := model.SelectedCategory()
	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if !res.Handled {
		t.Fatal("Down from search should be handled via bubbling")
	}
	if model.SelectedCategory() <= start {
		t.Fatalf("selected=%d want > %d after Down", model.SelectedCategory(), start)
	}
	afterDown := model.SelectedCategory()
	res = host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if !res.Handled {
		t.Fatal("Up from search should be handled via bubbling")
	}
	if model.SelectedCategory() >= afterDown {
		t.Fatalf("selected=%d want < %d after Up", model.SelectedCategory(), afterDown)
	}
}

func TestSettingsNavigation_TabOrderFromEveryRegion(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Theme: types.ThemeSettings{Preset: "dark"}}, nil)
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 22)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(80, 22)
	host.Layout()

	order := []ui.ComponentID{
		WorkspaceSettingsSearchID,
		WorkspaceSettingsListID,
		WorkspaceSettingsFormID,
		WorkspaceSettingsActionID,
	}
	for startIdx, startID := range order {
		frame.Focus(startID)
		if startID == WorkspaceSettingsFormID {
			surfaceRoot.form.FocusFirst()
		}
		host.Layout()
		if host.Focused() != startID {
			t.Fatalf("start focus=%q want %q", host.Focused(), startID)
		}
		for step := 1; step < len(order); step++ {
			want := order[(startIdx+step)%len(order)]
			reached := false
			for attempt := 0; attempt < 24; attempt++ {
				res := host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
				if res.Action != nil {
					_ = surfaceRoot.HandleAction(ui.EventContext{Theme: th}, *res.Action)
				}
				if host.Focused() == want {
					reached = true
					break
				}
			}
			if !reached {
				t.Fatalf("from %q, Tab to reach %q failed; focus=%q", startID, want, host.Focused())
			}
		}
		wrapped := false
		for attempt := 0; attempt < 24; attempt++ {
			res := host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
			if res.Action != nil {
				_ = surfaceRoot.HandleAction(ui.EventContext{Theme: th}, *res.Action)
			}
			if host.Focused() == startID {
				wrapped = true
				break
			}
		}
		if !wrapped {
			t.Fatalf("from cycle starting %q, Tab wrap back failed; focus=%q", startID, host.Focused())
		}
	}

	frame.Focus(WorkspaceSettingsActionID)
	host.Layout()
	for i := 0; i < 6 && host.Focused() != WorkspaceSettingsFormID; i++ {
		res := host.Handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		if res.Action != nil {
			_ = surfaceRoot.HandleAction(ui.EventContext{Theme: th}, *res.Action)
		}
	}
	if host.Focused() != WorkspaceSettingsFormID {
		t.Fatalf("Shift+Tab from actions focus=%q want form", host.Focused())
	}
}

// TestSettingsPresetCycle_UserKeySequenceThroughHost drives the REAL key path
// users hit: Down to Appearance, Tab to categories, Tab to form, then Right.
// A unit test that FocusForm()+Right directly would miss the routing bug where
// a single Tab left focus on categories and Right entered the form without cycling.
func TestSettingsPresetCycle_UserKeySequenceThroughHost(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Theme: types.ThemeSettings{Preset: "dark"}}, nil)
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 24)
	surfaceRoot.SetFocusHandler(frame.Focus)
	surfaceRoot.FocusSearch()
	host := frame.Host()
	host.Resize(80, 24)
	host.Layout()

	if host.Focused() != WorkspaceSettingsSearchID {
		t.Fatalf("start focus=%q", host.Focused())
	}

	for i := 0; i < 20 && model.SelectedCategory() != catAppearance; i++ {
		_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if model.SelectedCategory() != catAppearance {
		t.Fatalf("selected category=%d want Appearance(%d)", model.SelectedCategory(), catAppearance)
	}

	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	if host.Focused() != WorkspaceSettingsListID {
		t.Fatalf("after first Tab focus=%q want categories (regression: one Tab must not skip to form)", host.Focused())
	}

	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	if host.Focused() != WorkspaceSettingsFormID {
		t.Fatalf("after second Tab focus=%q want form", host.Focused())
	}

	before := model.value(catAppearance, "preset")
	if before == "" {
		before = "dark"
	}

	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if !res.Handled {
		t.Fatal("Right on focused preset cycle must be handled")
	}
	after := model.value(catAppearance, "preset")
	if after == before {
		t.Fatalf("preset still %q after Right — key routing regression", after)
	}

	var preview ThemePreviewMsg
	if res.Cmd != nil {
		if msg, ok := res.Cmd().(ThemePreviewMsg); ok {
			preview = msg
		}
	}
	if preview.Preset == "" && res.Action != nil && res.Action.ID == "form.changed" {
		handled := surfaceRoot.HandleAction(ui.EventContext{Theme: th}, *res.Action)
		if handled.Cmd != nil {
			if msg, ok := handled.Cmd().(ThemePreviewMsg); ok {
				preview = msg
			}
		}
	}
	if preview.Preset == "" {
		t.Fatal("expected ThemePreviewMsg from live apply after Right")
	}
	if preview.Preset != after {
		t.Fatalf("preview=%q model=%q", preview.Preset, after)
	}

	mid := after
	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.value(catAppearance, "preset") == mid {
		_ = host.Handle(tea.KeyPressMsg{Code: tea.KeySpace})
	}
	if model.value(catAppearance, "preset") == mid {
		t.Fatal("Enter/Space should also cycle the preset field")
	}
}

func TestSettingsPresetCycle_EmitsLiveThemePreview(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Theme: types.ThemeSettings{Preset: "dark"}}, nil)
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 22)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(80, 22)
	host.Layout()

	model.SelectCategory(catAppearance)
	surfaceRoot.refreshForm()
	surfaceRoot.form.FocusFirst()
	surfaceRoot.FocusForm()
	host.Layout()

	var preview *ThemePreviewMsg
	for i := 0; i < len(theme.AllPresetNames)+1; i++ {
		res := host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
		if res.Action != nil && res.Action.ID == "form.changed" {
			handled := surfaceRoot.HandleAction(ui.EventContext{Theme: th}, *res.Action)
			if handled.Cmd != nil {
				if msg, ok := handled.Cmd().(ThemePreviewMsg); ok {
					preview = &msg
					break
				}
			}
		}
		if res.Cmd != nil {
			if msg, ok := res.Cmd().(ThemePreviewMsg); ok {
				preview = &msg
				break
			}
		}
	}
	if preview == nil {
		t.Fatal("expected ThemePreviewMsg after cycling preset")
	}
	if preview.Preset == "dark" {
		t.Fatalf("preview preset still dark after cycle")
	}
	if theme.FromPreset(preview.Preset).Name != preview.Preset && preview.Preset != "custom" {
		t.Fatalf("preview preset %q not a known theme", preview.Preset)
	}
}

func TestSettingsPresetCycle_LiveApplyThroughHost(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Theme: types.ThemeSettings{Preset: "dark"}}, nil)
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 22)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(80, 22)
	host.Layout()

	model.SelectCategory(catAppearance)
	surfaceRoot.syncModel()
	surfaceRoot.form.FocusFirst()
	surfaceRoot.FocusForm()
	host.Layout()

	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if !res.Handled {
		t.Fatal("cycle key should be handled")
	}
	preset := model.value(catAppearance, "preset")
	if preset == "dark" {
		t.Fatal("preset should have cycled away from dark")
	}
	if res.Cmd == nil {
		t.Fatal("expected ThemePreviewMsg cmd from live preset apply")
	}
	msg, ok := res.Cmd().(ThemePreviewMsg)
	if !ok {
		t.Fatalf("cmd msg type=%T want ThemePreviewMsg", res.Cmd())
	}
	if msg.Preset != preset {
		t.Fatalf("preview=%q model=%q", msg.Preset, preset)
	}
}

func TestSettingsAppearance_ColorFieldsShowResolvedHex(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Theme: types.ThemeSettings{Preset: "dark"}}, nil)
	bg := model.value(catAppearance, "background")
	if bg == "" || bg[0] != '#' {
		t.Fatalf("background=%q want resolved #hex", bg)
	}
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 24)
	surfaceRoot.SetFocusHandler(frame.Focus)
	model.SelectCategory(catAppearance)
	surfaceRoot.syncModel()
	surfaceRoot.FocusForm()
	host := frame.Host()
	host.Resize(80, 24)
	host.Layout()

	buf := surface.NewCellBuffer(80, 24)
	host.Paint(buf)
	plain := settingsBufferPlain(buf)
	if !strings.Contains(plain, bg) || !strings.Contains(plain, "foreground") || !strings.Contains(plain, "accent") {
		t.Fatalf("painted appearance form missing colour values:\n%s", plain)
	}

	surfaceRoot.form.FocusFirst()
	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	newBG := model.value(catAppearance, "background")
	if newBG == "" || newBG == bg {
		t.Fatalf("after preset cycle background=%q (was %q) — colour rows must refresh", newBG, bg)
	}
}

func settingsBufferPlain(buf *surface.CellBuffer) string {
	var rows []string
	for y := 0; y < buf.Height(); y++ {
		var b strings.Builder
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(rows, "\n")
}

func TestSettingsSave_PersistsPresetThroughHost(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Theme: types.ThemeSettings{Preset: "dark"}}, nil)
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 24)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(80, 24)
	host.Layout()

	model.SelectCategory(catAppearance)
	surfaceRoot.syncModel()
	surfaceRoot.form.FocusFirst()
	surfaceRoot.FocusForm()
	host.Layout()

	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyRight}) // dark -> light
	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyRight}) // light -> hc
	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyRight}) // hc -> warm
	preset := model.value(catAppearance, "preset")
	if preset != "warm" {
		t.Fatalf("preset=%q want warm after 3 Rights", preset)
	}

	// Tab through fields to action bar.
	for i := 0; i < 16 && host.Focused() != WorkspaceSettingsActionID; i++ {
		res := host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
		if res.Action != nil {
			_ = surfaceRoot.HandleAction(ui.EventContext{Theme: th}, *res.Action)
		}
	}
	if host.Focused() != WorkspaceSettingsActionID {
		t.Fatalf("focus=%q want actions", host.Focused())
	}

	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Action != nil && res.Action.ID == "settings.save" {
		res = surfaceRoot.HandleAction(ui.EventContext{Theme: th}, *res.Action)
	}
	if res.Cmd == nil {
		t.Fatalf("expected SaveMsg cmd; handled=%v action=%v focus=%q", res.Handled, res.Action, host.Focused())
	}
	msg := res.Cmd()
	save, ok := msg.(SaveMsg)
	if !ok {
		t.Fatalf("cmd type=%T want SaveMsg", msg)
	}
	if save.AppSettings.Theme.Preset != "warm" {
		t.Fatalf("SaveMsg preset=%q want warm", save.AppSettings.Theme.Preset)
	}
}
