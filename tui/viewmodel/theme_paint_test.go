package viewmodel

import (
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/shellstatus"
	"crude/tui/screens/shelltree"
	settingsui "crude/tui/settings"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widget"
	"crude/tui/wm"
)

// TestApplyThemePreset_PaintedCellsLeaveOldPalette asserts on actual painted
// cell backgrounds — not theme pointers. A pointer-only check previously
// passed while live screenshots still showed dark voids / half-themed UI.
func TestApplyThemePreset_PaintedCellsLeaveOldPalette(t *testing.T) {
	app := newPaintableThemeApp(t)
	app.openWorkspaceSettings()

	dark := theme.DefaultDark()
	light := theme.DefaultLight()
	darkSig := exclusiveSignatureColors(dark, light)

	app.ApplyThemePreset("dark")
	darkBuf := paintThemeFrame(t, app)
	if n := countBGMatches(darkBuf, darkSig); n == 0 {
		t.Fatal("sanity: dark frame must paint dark-signature backgrounds")
	}

	app.ApplyThemePreset("light")
	lightBuf := paintThemeFrame(t, app)

	leftover := countBGMatches(lightBuf, darkSig)
	if leftover > 0 {
		t.Fatalf("after switch to light, %d cells still carry dark-only signature backgrounds %v — propagation/cache gap",
			leftover, darkSig)
	}
	lightBG := rendering.ThemeColorToCell(light.Colors.Bg)
	if n := countExactBG(lightBuf, lightBG); n < lightBuf.Width()*lightBuf.Height()/4 {
		t.Fatalf("light theme bg %v covers only %d cells — root/layer fill missing", lightBG, n)
	}

	// PROOF the assertion fails when propagation is broken: paint with the
	// old theme still installed on hosts after we only flip app.Theme.
	broken := newPaintableThemeApp(t)
	broken.openWorkspaceSettings()
	broken.ApplyThemePreset("dark")
	_ = paintThemeFrame(t, broken)
	broken.Theme = light // pretend switch without walking consumers / filling caches
	brokenBuf := paintThemeFrameSkippingApply(t, broken)
	if n := countBGMatches(brokenBuf, darkSig); n == 0 {
		t.Fatal("proof harness broken: expected dark signature leftovers when ApplyThemePreset is skipped")
	}
}

func TestSettingsCancel_RevertsLiveThemePreview(t *testing.T) {
	app := newPaintableThemeApp(t)
	app.ApplyThemePreset("dark")
	saved := app.LoadAppSettings()
	saved.Theme.Preset = "dark"
	_ = app.SaveAppSettings(saved)

	app.openWorkspaceSettings()
	app.ApplyThemePreset("light") // simulate ThemePreviewMsg live cycle
	if theme.PresetName(app.Theme) != "light" {
		t.Fatalf("preview theme=%q", theme.PresetName(app.Theme))
	}

	_, _ = app.Update(settingsui.CancelMsg{})
	if theme.PresetName(app.Theme) != "dark" {
		t.Fatalf("after Cancel theme=%q want dark (saved preset)", theme.PresetName(app.Theme))
	}
	if app.windowManager.Window(string(ui.OverlayWorkspaceSettings)) != nil {
		t.Fatal("settings overlay should close on Cancel")
	}
}

// TestSettingsEsc_ForwardsThroughFloatingWindow ensures Esc reaches the
// settings surface (CancelMsg) instead of the default closePopupWindowByID
// path that previously dropped the live theme preview without reverting.
func TestSettingsEsc_ForwardsThroughFloatingWindow(t *testing.T) {
	app := newPaintableThemeApp(t)
	saved := app.LoadAppSettings()
	saved.Theme.Preset = "dark"
	_ = app.SaveAppSettings(saved)
	app.ApplyThemePreset("dark")
	app.openWorkspaceSettings()
	app.ApplyThemePreset("midnight")

	// Focus floating window path: Esc must not leave midnight without revert.
	app.FocusStack.Push(FocusFloatingWindow)
	_, cmd := app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		msg := cmd()
		if cancel, ok := msg.(settingsui.CancelMsg); ok {
			_, _ = app.Update(cancel)
		} else if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				if c == nil {
					continue
				}
				if cancel, ok := c().(settingsui.CancelMsg); ok {
					_, _ = app.Update(cancel)
				}
			}
		} else {
			// Host may return CancelMsg wrapped; try Update directly.
			_, _ = app.Update(msg)
		}
	}
	// Esc from search/form may first clear/back; keep Esc until closed.
	for i := 0; i < 4 && app.windowManager.Window(string(ui.OverlayWorkspaceSettings)) != nil; i++ {
		_, cmd = app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyEsc})
		if cmd != nil {
			msg := cmd()
			switch m := msg.(type) {
			case settingsui.CancelMsg:
				_, _ = app.Update(m)
			case tea.BatchMsg:
				for _, c := range m {
					if c == nil {
						continue
					}
					if cancel, ok := c().(settingsui.CancelMsg); ok {
						_, _ = app.Update(cancel)
					}
				}
			default:
				_, _ = app.Update(msg)
			}
		}
	}
	if theme.PresetName(app.Theme) != "dark" {
		t.Fatalf("after Esc close theme=%q want dark", theme.PresetName(app.Theme))
	}
}

func TestApplyThemePreset_SidebarTreeStillPainted(t *testing.T) {
	app := newPaintableThemeApp(t)
	app.ApplyThemePreset("dark")
	_ = paintThemeFrame(t, app)

	for _, preset := range []string{"light", "high-contrast", "warm", "midnight"} {
		app.ApplyThemePreset(preset)
		buf := paintThemeFrame(t, app)
		// Sidebar is the left Tree.Width+1 columns.
		sideW := app.Tree.Width + 1
		chars := 0
		for y := 2; y < buf.Height()-2; y++ {
			for x := 0; x < sideW && x < buf.Width(); x++ {
				c := buf.Get(x, y)
				if c.Char != 0 && c.Char != ' ' && c.Char != '│' {
					chars++
				}
			}
		}
		if chars < 5 {
			t.Fatalf("preset %q sidebar has only %d content cells — tree blanked", preset, chars)
		}
	}
}

func newPaintableThemeApp(t *testing.T) *App {
	t.Helper()
	th := theme.DefaultDark()
	w, h := 100, 28
	wmgr := wm.NewWindowManager()
	comp := compositor.NewCompositor(w, h)

	treeModel := filetree.NewFileTreeModel(t.TempDir())
	tree := NewTreeState(treeModel, 24)
	tree.Visible = true

	headerScreen := shellheader.New(th)
	headerHost := ui.NewHost(th, headerScreen)
	statusBar := shellstatus.New(th)
	statusHost := ui.NewHost(th, statusBar)
	sidebarScreen := shelltree.New(th, treeModel)
	sidebarHost := ui.NewHost(th, sidebarScreen)
	launcherScreen := launcher.New(th)
	launcherScreen.SetData(t.TempDir(), nil)
	launcherHost := ui.NewHost(th, launcherScreen)
	launcherScreen.SetFocusHandler(launcherHost.Focus)

	app := newFullTestApp(t)
	app.width = w
	app.height = h
	app.Theme = th
	app.compositor = comp
	app.windowManager = wmgr
	app.overlayHost = ui.NewOverlayHost(wmgr)
	app.renderCtx = widget.NewRenderContext(th)
	app.Tree = tree
	app.headerScreen = headerScreen
	app.headerHost = headerHost
	app.statusHost = statusHost
	app.StatusBar = statusBar
	app.sidebarScreen = sidebarScreen
	app.sidebarHost = sidebarHost
	app.launcherScreen = launcherScreen
	app.launcherHost = launcherHost
	app.mainHeaderSurf = surface.NewSurface("main-header", w, 2, cell.TransparentBlank())
	app.mainStatusbarSurf = surface.NewSurface("main-statusbar", w, 2, cell.TransparentBlank())
	app.mainSidebarSurf = surface.NewSurface("main-sidebar", tree.Width+1, h-4, cell.TransparentBlank())
	app.mainLauncherSurf = surface.NewSurface("main-launcher", w-tree.Width-1, h-4, cell.TransparentBlank())
	app.mainContentSurf = surface.NewSurface("main-content", w, h, cell.TransparentBlank())
	app.ChatAreaHeight = h - 6
	return app
}

func paintThemeFrame(t *testing.T, app *App) *surface.CellBuffer {
	t.Helper()
	_ = app.View() // full pipeline including root theme-bg fill + overlays
	// Re-paint into an inspectable buffer mirroring the same surfaces.
	return capturePaintedShell(t, app)
}

func paintThemeFrameSkippingApply(t *testing.T, app *App) *surface.CellBuffer {
	t.Helper()
	// Intentionally paint chrome using whatever theme each host still holds,
	// while filling the root from app.Theme (the broken half-theme pattern).
	return capturePaintedShell(t, app)
}

func capturePaintedShell(t *testing.T, app *App) *surface.CellBuffer {
	t.Helper()
	w, h := app.width, app.height
	buf := surface.NewCellBuffer(w, h)
	bg := cell.BlankCell(rendering.ThemeColorToCell(app.Theme.Colors.Bg))
	buf.Fill(bg)

	app.renderHeaderToSurface()
	if app.mainHeaderSurf != nil {
		blitSurface(buf, app.mainHeaderSurf)
	}
	if app.Tree.Visible {
		app.renderSidebarToSurface()
		if app.mainSidebarSurf != nil {
			blitSurface(buf, app.mainSidebarSurf)
		}
	}
	app.renderStatusbarToSurface()
	if app.mainStatusbarSurf != nil {
		blitSurface(buf, app.mainStatusbarSurf)
	}
	app.renderLauncherToSurface()
	if app.mainLauncherSurf != nil && app.mainLauncherSurf.Visible() {
		blitSurface(buf, app.mainLauncherSurf)
	}
	if app.windowManager != nil {
		app.windowManager.Render(app.compositor.Root(), app.renderCtx)
		for _, fw := range app.windowManager.Windows() {
			if fw == nil {
				continue
			}
			// Floating window paints into its own surfaces; sample via Content host.
			if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok && frame.Host() != nil {
				host := frame.Host()
				hw, hh := fw.Size()
				if hw < 1 || hh < 1 {
					continue
				}
				host.Resize(hw, hh)
				host.MarkLayout()
				host.Layout()
				tmp := surface.NewCellBuffer(hw, hh)
				host.Paint(tmp)
				x, y := fw.Position()
				for yy := 0; yy < hh; yy++ {
					for xx := 0; xx < hw; xx++ {
						c := tmp.Get(xx, yy)
						if c.Transparent {
							continue
						}
						buf.Set(x+xx, y+yy, c)
					}
				}
			}
		}
	}
	return buf
}

func blitSurface(dst *surface.CellBuffer, s *surface.Surface) {
	if s == nil || !s.Visible() {
		return
	}
	x, y := s.Position()
	surface.Blit(dst, x, y, s.Buffer(), nil)
}

func exclusiveSignatureColors(oldTh, newTh *theme.Theme) []cell.Color {
	oldSet := map[cell.Color]bool{}
	newSet := map[cell.Color]bool{}
	for _, c := range []color.Color{oldTh.Colors.Bg, oldTh.Colors.PopupBg, oldTh.Colors.SubPaneBg, oldTh.Colors.Code.Bg} {
		oldSet[rendering.ThemeColorToCell(c)] = true
	}
	for _, c := range []color.Color{newTh.Colors.Bg, newTh.Colors.PopupBg, newTh.Colors.SubPaneBg, newTh.Colors.Code.Bg, newTh.Colors.Fg, newTh.Colors.Selection, newTh.Colors.Hover} {
		newSet[rendering.ThemeColorToCell(c)] = true
	}
	var out []cell.Color
	for c := range oldSet {
		if !newSet[c] {
			out = append(out, c)
		}
	}
	return out
}

func countBGMatches(buf *surface.CellBuffer, colors []cell.Color) int {
	want := map[cell.Color]bool{}
	for _, c := range colors {
		want[c] = true
	}
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Transparent {
				continue
			}
			if want[c.BG] {
				n++
			}
		}
	}
	return n
}

func countExactBG(buf *surface.CellBuffer, bg cell.Color) int {
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if !c.Transparent && c.BG.Equal(bg) {
				n++
			}
		}
	}
	return n
}
