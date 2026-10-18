package viewmodel

import (
	"reflect"
	"testing"

	"crude/tui/screens/activitybar"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

func TestApplyThemePreset_UpdatesAllRegisteredConsumers(t *testing.T) {
	app := newFullTestApp(t)
	app.renderCtx = wm.NewRenderContext(app.Theme)

	proj := app.subPaneProjections.Get("pane-1", workspace.PaneAgent)
	if proj == nil || proj.renderer == nil {
		t.Fatal("expected subpane projection with renderer")
	}
	oldRendererTheme := proj.renderer.Theme()

	app.openWorkspaceSettings()
	fw := app.windowManager.Window(string(ui.OverlayWorkspaceSettings))
	if fw == nil {
		t.Fatal("settings window not open")
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok || frame == nil || frame.Host() == nil {
		t.Fatalf("settings content type=%T", fw.Content)
	}
	oldOverlayTheme := frame.Host().Theme()
	if theme.PresetName(oldOverlayTheme) != "dark" {
		t.Fatalf("overlay started on %q", theme.PresetName(oldOverlayTheme))
	}

	app.ApplyThemePreset("midnight")

	if theme.PresetName(app.Theme) != "midnight" {
		t.Fatalf("app.Theme=%q", theme.PresetName(app.Theme))
	}
	if theme.PresetName(app.renderCtx.Theme) != "midnight" {
		t.Fatalf("renderCtx.Theme=%q — gap (a) regress", theme.PresetName(app.renderCtx.Theme))
	}
	if theme.PresetName(app.subPaneProjections.Theme()) != "midnight" {
		t.Fatalf("subPane registry theme=%q — gap (b) regress", theme.PresetName(app.subPaneProjections.Theme()))
	}
	if theme.PresetName(proj.renderer.Theme()) != "midnight" {
		t.Fatalf("existing renderer theme=%q (was %q) — gap (b) regress",
			theme.PresetName(proj.renderer.Theme()), theme.PresetName(oldRendererTheme))
	}
	if theme.PresetName(frame.Host().Theme()) != "midnight" {
		t.Fatalf("open overlay host theme=%q (was %q) — gap (c) regress",
			theme.PresetName(frame.Host().Theme()), theme.PresetName(oldOverlayTheme))
	}

	snapshots := app.themeConsumerSnapshots()
	for _, id := range requiredThemeConsumerIDs {
		got := snapshots[id]
		if got == nil {
			t.Errorf("required consumer %q missing from snapshot", id)
			continue
		}
		if theme.PresetName(got) != "midnight" {
			t.Errorf("consumer %q still holds %q", id, theme.PresetName(got))
		}
	}

	// Every non-nil Host on App must hold the new theme (direct field access;
	// reflection cannot Interface unexported fields).
	for _, h := range appHostHolders(app) {
		if h.host == nil {
			continue
		}
		if theme.PresetName(h.host.Theme()) != "midnight" {
			t.Errorf("host %s still holds %q", h.name, theme.PresetName(h.host.Theme()))
		}
	}

	// Anti-regression: App struct *ui.Host field names must match appHostHolders.
	// If someone adds a new *ui.Host field and forgets to wire it into
	// themeConsumers()/appHostHolders, this fails.
	structHosts := hostFieldNames(app)
	holderNames := map[string]bool{}
	for _, h := range appHostHolders(app) {
		holderNames[h.name] = true
	}
	for _, name := range structHosts {
		if !holderNames[name] {
			t.Errorf("App.%s is a *ui.Host field but is missing from appHostHolders/themeConsumers — wire it so theme switches cannot leave it stale", name)
		}
	}
	for name := range holderNames {
		found := false
		for _, n := range structHosts {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("appHostHolders lists %q but App has no such *ui.Host field — update the helper", name)
		}
	}
}

func TestApplyThemePreset_ConfigHotReloadPath(t *testing.T) {
	app := newFullTestApp(t)
	app.renderCtx = wm.NewRenderContext(app.Theme)
	app.applyThemePreset("warm")
	if theme.PresetName(app.Theme) != "warm" {
		t.Fatalf("hot-reload path left theme=%q", theme.PresetName(app.Theme))
	}
	if theme.PresetName(app.renderCtx.Theme) != "warm" {
		t.Fatalf("hot-reload did not update renderCtx: %q", theme.PresetName(app.renderCtx.Theme))
	}
}

func TestApplyThemePreset_PreservesExplicitNerdFontPreference(t *testing.T) {
	app := newFullTestApp(t)
	settings := app.ws.AppDefaults()
	settings.UI.UseNerdFont = true
	wsOf(app).SetAppDefaults(settings)

	app.ApplyThemePreset("dark")

	if app.Theme.Tree.Config.UseNerdFont == nil || !*app.Theme.Tree.Config.UseNerdFont {
		t.Fatal("explicit Nerd Font preference was lost while rebuilding the theme")
	}
	if got := app.rootHost.Theme().Tree.Config.UseNerdFont; got == nil || !*got {
		t.Fatal("root host did not receive the explicit Nerd Font preference")
	}

	const height = 40
	app.activityBar.Layout(ui.LayoutContext{}, ui.Rect{W: activitybar.Width, H: height})
	buf := surface.NewCellBuffer(activitybar.Width, height)
	app.activityBar.Paint(buf, 0, 0, ui.PaintContext{})
	if !bufferContainsPlain(buf, activitybar.ViewAgents.Icon(true)) {
		t.Fatal("Agents rail entry did not paint its Nerd Font icon")
	}
	if !bufferContainsPlain(buf, activitybar.SettingsIcon(true)) {
		t.Fatal("Settings rail entry did not paint its Nerd Font icon")
	}
}

type namedHost struct {
	name string
	host *ui.Host
}

// appHostHolders enumerates every App-owned *ui.Host that must receive themes.
// The root host owns the whole shell tree (Host.SetTheme propagates to every
// mounted screen via applyTokenSet); per-session deckHosts live on
// sessionProjections and are covered by the sessionProjections consumer.
// Keep in sync with App struct fields of type *ui.Host (enforced by test).
func appHostHolders(app *App) []namedHost {
	return []namedHost{
		{"rootHost", app.rootHost},
		// Borrowed routing alias; ApplyThemePreset clears it before updating
		// the owning root/window Host, never writes a second copy of theme state.
		{"wheelHost", app.wheelHost},
	}
}

func hostFieldNames(app *App) []string {
	var names []string
	v := reflect.ValueOf(app).Elem()
	ty := v.Type()
	hostType := reflect.TypeOf((*ui.Host)(nil))
	for i := 0; i < v.NumField(); i++ {
		if ty.Field(i).Type == hostType {
			names = append(names, ty.Field(i).Name)
		}
	}
	return names
}
