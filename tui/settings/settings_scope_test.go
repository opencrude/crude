package settings

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
	"crude/tui/wm"
)

func TestWorkspaceScopeSaveEmitsOverridesWithoutAPIKeys(t *testing.T) {
	th := theme.DefaultDark()
	app := types.AppSettings{
		Theme:          types.ThemeSettings{Preset: "dark"},
		Editor:         types.EditorSettings{TabSize: 4},
		ActiveProvider: "openrouter",
		Providers:      map[string]types.ProviderConfig{"openrouter": {APIKey: "app-key", Model: "app-model"}},
	}
	working := app
	working.Editor.TabSize = 8
	// A shallow struct copy shares the providers map; mutating it in place
	// would edit the application baseline too and hide the diff.
	working.Providers = map[string]types.ProviderConfig{
		"openrouter": {APIKey: "ws-key", Model: "ws-model"},
	}
	model := NewScoped(th, working, nil, types.SettingsScopeWorkspace, app, types.WorkspaceOverrides{})
	got := model.Overrides()
	if !strings.Contains(string(got.Editor), `"tab_size":8`) {
		t.Fatalf("editor override missing: %s", got.Editor)
	}
	if strings.Contains(string(got.Providers), "ws-key") {
		t.Fatalf("workspace save leaked API key: %s", got.Providers)
	}
	if !strings.Contains(string(got.Providers), "ws-model") {
		t.Fatalf("workspace model overlay missing: %s", got.Providers)
	}
}

func TestWorkspaceScopeShowsResetSection(t *testing.T) {
	th := theme.DefaultDark()
	model := NewScoped(th, types.AppSettings{}, nil, types.SettingsScopeWorkspace, types.AppSettings{}, types.WorkspaceOverrides{})
	surfaceRoot := NewSurface(th, model)
	cw := ui.NewComponentWidget(th, surfaceRoot, 80, 22)
	surfaceRoot.SetFocusHandler(cw.Focus)
	surf := surface.NewSurface("settings-scope", 80, 22, cell.TransparentBlank())
	cw.Render(surf, &wm.RenderContext{Theme: th})
	if !bufferContains(surf.Buffer(), "Workspace") {
		t.Fatal("workspace scope bar missing")
	}
	if !bufferContains(surf.Buffer(), "Reset section") {
		t.Fatal("workspace scope should show Reset section")
	}
	if !bufferContains(surf.Buffer(), "Application") {
		t.Fatal("segmented scope switch must show Application")
	}
	resetX, saveX := bufferIndex(surf.Buffer(), "Reset section"), bufferIndex(surf.Buffer(), "Save")
	if resetX < 0 || saveX < 0 || resetX >= saveX {
		t.Fatalf("Reset should sit left of Save: reset=%d save=%d", resetX, saveX)
	}
}

func TestSettingsFramedWindowKeepsFooterActions(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{}, nil)
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Application Settings", surfaceRoot, nil, true, 104, 28)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(104, 28)
	host.Layout()
	buf := surface.NewCellBuffer(104, 28)
	host.Paint(buf)
	if !bufferContains(buf, "Save") || !bufferContains(buf, "Cancel") {
		t.Fatalf("framed settings lost footer actions:\n%s", bufferPlain(buf))
	}

	// Unhandled overlay messages used to rebuild the action bar without
	// layout, leaving Save/Cancel with empty bounds.
	surfaceRoot.SetModel(model)
	buf = surface.NewCellBuffer(104, 28)
	host.Paint(buf)
	if !bufferContains(buf, "Save") || !bufferContains(buf, "Cancel") {
		t.Fatalf("SetModel without relayout dropped Save/Cancel:\n%s", bufferPlain(buf))
	}

	scopeY := bufferRow(buf, "Workspace")
	appX := -1
	if scopeY >= 0 {
		appX = bufferIndexOnRow(buf, scopeY, "Application")
	}
	if appX < 0 || scopeY < 0 {
		t.Fatal("Application segment missing from footer")
	}
	if got, want := buf.Get(appX, scopeY).FG, ui.NewTokenSet(th).Color(ui.ColorAccent); !got.Equal(want) {
		t.Fatalf("active scope font color=%v want accent %v", got, want)
	}
}

func TestSettingsFormAlignsWithSearch(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{}, nil)
	surfaceRoot := NewSurface(th, model)
	host := ui.NewHost(th, surfaceRoot)
	host.Resize(90, 24)
	host.Layout()
	buf := surface.NewCellBuffer(90, 24)
	host.Paint(buf)
	searchY, sectionY := bufferRow(buf, "Search settings"), bufferRow(buf, "Connection")
	if searchY < 0 || sectionY < 0 {
		t.Fatalf("searchY=%d sectionY=%d", searchY, sectionY)
	}
	if sectionY != searchY {
		t.Fatalf("right panel section y=%d want search y=%d", sectionY, searchY)
	}
}

func bufferRow(buf *surface.CellBuffer, want string) int {
	runes := []rune(want)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x <= buf.Width()-len(runes); x++ {
			matched := true
			for i, r := range runes {
				if buf.Get(x+i, y).Char != r {
					matched = false
					break
				}
			}
			if matched {
				return y
			}
		}
	}
	return -1
}

func bufferIndex(buf *surface.CellBuffer, want string) int {
	for y := 0; y < buf.Height(); y++ {
		if x := bufferIndexOnRow(buf, y, want); x >= 0 {
			return x
		}
	}
	return -1
}

func bufferIndexOnRow(buf *surface.CellBuffer, y int, want string) int {
	runes := []rune(want)
	if y < 0 || y >= buf.Height() {
		return -1
	}
	for x := 0; x <= buf.Width()-len(runes); x++ {
		matched := true
		for i, r := range runes {
			if buf.Get(x+i, y).Char != r {
				matched = false
				break
			}
		}
		if matched {
			return x
		}
	}
	return -1
}

func TestSettingsScrollbarsAppearOnlyOnOverflow(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{}, nil)
	model.SelectCategory(catLSP)
	surfaceRoot := NewSurface(th, model)
	surfaceRoot.refreshForm()
	host := ui.NewHost(th, surfaceRoot)
	host.Resize(90, 12)
	host.Layout()
	if !surfaceRoot.formScroll.Needed() {
		t.Fatal("LSP form should overflow a 12-row panel and show a scrollbar")
	}

	model.SelectCategory(catFont)
	surfaceRoot.refreshForm()
	host.Resize(90, 28)
	host.Layout()
	if surfaceRoot.formScroll.Needed() {
		t.Fatal("Font form should fit a 28-row panel without a scrollbar")
	}
}

func TestSettingsFormScrollDoesNotPaintFooter(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{}, nil)
	model.SelectCategory(catLSP)
	surfaceRoot := NewSurface(th, model)
	surfaceRoot.refreshForm()
	host := ui.NewHost(th, surfaceRoot)
	const w, h = 90, 24
	host.Resize(w, h)
	host.Layout()
	if !surfaceRoot.formScroll.Needed() {
		t.Fatal("LSP form should overflow so footer clipping can be tested")
	}

	paint := func() *surface.CellBuffer {
		buf := surface.NewCellBuffer(w, h)
		host.Paint(buf)
		return buf
	}
	assertFooterClear := func(t *testing.T, buf *surface.CellBuffer) {
		t.Helper()
		if !bufferContains(buf, "Save") || !bufferContains(buf, "Cancel") {
			t.Fatalf("footer actions missing:\n%s", bufferPlain(buf))
		}
		footerY := h - 1
		sepY := h - 2
		for _, y := range []int{sepY, footerY} {
			if bufferIndexOnRow(buf, y, "Start this server") >= 0 || bufferIndexOnRow(buf, y, "gopls") >= 0 {
				t.Fatalf("form content leaked onto footer row %d:\n%s", y, bufferPlain(buf))
			}
		}
	}

	assertFooterClear(t, paint())
	form := surfaceRoot.form
	form.Scroll().ScrollTo(form.Scroll().MaxOffset())
	form.Layout(ui.LayoutContext{}, form.Bounds())
	assertFooterClear(t, paint())
	form.Scroll().ScrollTo(1)
	form.Layout(ui.LayoutContext{}, form.Bounds())
	assertFooterClear(t, paint())
}
