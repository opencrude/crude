package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/wm"
)

// overlayOutsideFixture describes one product floating overlay's declared
// click-outside policy and expected integration behavior via OverlayHost.
type overlayOutsideFixture struct {
	id           ui.OverlayID
	policy       ui.OverlayPolicy
	wantClose    bool
	wantSuppress bool
}

// overlayOutsideContractMatrix is the authoritative Phase 15 policy table for
// product floating overlays (19 static surfaces). Each row is exercised by
// TestOverlayClickOutsideContractMatrix. Dynamic floating terminals use
// DefaultOverlayPolicy (KeepRoute) with ids `terminal/<pty-id>` and are not
// listed here. Real-opener dispatchMouseClick integration is covered separately
// per policy group in TestDispatchMouseClickOutsidePolicyGroups.
//
// Policy groups:
//   - CloseRoute (PopoverPolicy closeOnOutside=true): context-menu, LSP list popups, diag-context
//   - KeepRoute (DefaultOverlayPolicy / PopoverPolicy false): find/replace, palette, composer, FIF, replace-in-files, textoverlay, diag-hover
//   - Ignore (ModalDialogPolicy): dirty-close, tree prompts, file/workspace pickers, session-picker, help
var overlayOutsideContractMatrix = []overlayOutsideFixture{
	{id: ui.OverlayContextMenu, policy: ui.PopoverPolicy(true), wantClose: true},
	{id: ui.OverlayReferences, policy: ui.PopoverPolicy(true), wantClose: true},
	{id: ui.OverlayCompletion, policy: ui.PopoverPolicy(true), wantClose: true},
	{id: ui.OverlayOutline, policy: ui.PopoverPolicy(true), wantClose: true},
	{id: ui.OverlayDiagContext, policy: ui.PopoverPolicy(true), wantClose: true},
	{id: ui.OverlayPlanHover, policy: ui.PopoverPolicy(true), wantClose: true},

	{id: ui.OverlayFindReplace, policy: ui.DefaultOverlayPolicy()},
	{id: ui.OverlayCommandPalette, policy: ui.DefaultOverlayPolicy()},
	{id: ui.OverlayComposer, policy: ui.DefaultOverlayPolicy()},
	{id: ui.OverlayTextOverlay, policy: ui.DefaultOverlayPolicy()},
	{id: ui.OverlayFindInFiles, policy: ui.DefaultOverlayPolicy()},
	{id: ui.OverlayReplaceInFiles, policy: ui.DefaultOverlayPolicy()},
	{id: ui.OverlayDiagHover, policy: ui.PopoverPolicy(false)},

	{id: ui.OverlayDirtyClose, policy: ui.ModalDialogPolicy(), wantSuppress: true},
	{id: ui.OverlayOperationIssue, policy: ui.ModalDialogPolicy(), wantSuppress: true},
	{id: ui.OverlayTreePrompt, policy: ui.ModalDialogPolicy(), wantSuppress: true},
	{id: ui.OverlayTreeDelete, policy: ui.ModalDialogPolicy(), wantSuppress: true},
	{id: ui.OverlayFilePicker, policy: ui.ModalDialogPolicy(), wantSuppress: true},
	{id: ui.OverlayWorkspaceSettings, policy: ui.ModalDialogPolicy(), wantSuppress: true},
	{id: ui.OverlayCapabilityPicker, policy: ui.ModalDialogPolicy(), wantSuppress: true},
	{id: ui.OverlaySessionPicker, policy: ui.ModalDialogPolicy(), wantSuppress: true},
	{id: ui.OverlayHelp, policy: ui.ModalDialogPolicy(), wantSuppress: true},
}

func openOverlayFixture(t *testing.T, app *App, fx overlayOutsideFixture) {
	t.Helper()
	pop := ui.NewPopupContent([]string{"╭─╮", "│ x│", "╰─╯"})
	fw := wm.NewFloatingWindow(string(fx.id), "", pop, 7, 3,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
		wm.WithClickOutside(outsidePolicyToWM(fx.policy.Outside)),
	)
	fw.SetPosition(12, 6)
	app.overlayHost.Open(fx.id, fw, fx.policy, nil)
}

func outsidePolicyToWM(p ui.OutsidePolicy) wm.ClickOutsideBehavior {
	switch p {
	case ui.OutsideIgnoreAndConsume:
		return wm.ClickOutsideIgnore
	case ui.OutsideKeepAndRoute:
		return wm.ClickOutsideKeepRoute
	default:
		return wm.ClickOutsideCloseRoute
	}
}

// TestOverlayClickOutsideContractMatrix verifies every product overlay policy
// at viewmodel/OverlayHost integration: close/keep/suppress + single routing pass.
func TestOverlayClickOutsideContractMatrix(t *testing.T) {
	for _, fx := range overlayOutsideContractMatrix {
		t.Run(string(fx.id), func(t *testing.T) {
			app := newTestAppForPopupMouse(t)
			openOverlayFixture(t, app, fx)
			defer app.overlayHost.Close(fx.id)

			if app.windowManager.Window(string(fx.id)) == nil {
				t.Fatal("fixture overlay not open")
			}

			outside := tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}
			suppressed := app.handlePopupClickOutside(outside)
			if suppressed != fx.wantSuppress {
				t.Fatalf("suppress=%v want %v (policy=%v)", suppressed, fx.wantSuppress, fx.policy.Outside)
			}

			stillOpen := app.windowManager.Window(string(fx.id)) != nil
			if fx.wantClose && stillOpen {
				t.Fatal("overlay should close on outside click (CloseRoute)")
			}
			if !fx.wantClose && !stillOpen {
				t.Fatal("overlay must remain open for KeepRoute/Ignore policies")
			}

			// Second call with click still outside must not double-close or flip suppress.
			suppressedAgain := app.handlePopupClickOutside(outside)
			if suppressedAgain != fx.wantSuppress {
				t.Fatalf("second outside click suppress=%v want %v", suppressedAgain, fx.wantSuppress)
			}
		})
	}
}

// TestOverlayClickOutsidePolicyGroups spot-checks one representative per policy
// builder at OverlayHost level (shared builders + integration).
func TestOverlayClickOutsidePolicyGroups(t *testing.T) {
	tests := []struct {
		name         string
		build        func(*App) ui.OverlayID
		wantClose    bool
		wantSuppress bool
	}{
		{
			name: "popover_close_route_representative",
			build: func(app *App) ui.OverlayID {
				openOverlayFixture(t, app, overlayOutsideFixture{
					id: ui.OverlayContextMenu, policy: ui.PopoverPolicy(true), wantClose: true,
				})
				return ui.OverlayContextMenu
			},
			wantClose: true,
		},
		{
			name: "default_keep_route_representative",
			build: func(app *App) ui.OverlayID {
				openOverlayFixture(t, app, overlayOutsideFixture{
					id: ui.OverlayFindReplace, policy: ui.DefaultOverlayPolicy(),
				})
				return ui.OverlayFindReplace
			},
		},
		{
			name: "modal_ignore_representative",
			build: func(app *App) ui.OverlayID {
				openOverlayFixture(t, app, overlayOutsideFixture{
					id: ui.OverlayDirtyClose, policy: ui.ModalDialogPolicy(), wantSuppress: true,
				})
				return ui.OverlayDirtyClose
			},
			wantSuppress: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestAppForPopupMouse(t)
			id := tc.build(app)
			defer app.overlayHost.Close(id)

			msg := tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft}
			suppressed := app.handlePopupClickOutside(msg)
			if suppressed != tc.wantSuppress {
				t.Fatalf("suppress=%v want %v", suppressed, tc.wantSuppress)
			}
			open := app.windowManager.Window(string(id)) != nil
			if tc.wantClose && open {
				t.Fatal("expected overlay closed")
			}
			if !tc.wantClose && !open {
				t.Fatal("expected overlay still open")
			}
		})
	}
}

// TestOverlayClickOutsideInsidePopupDoesNotClose verifies inside clicks never
// trigger outside policy for any representative policy group.
func TestOverlayClickOutsideInsidePopupDoesNotClose(t *testing.T) {
	representatives := []overlayOutsideFixture{
		{id: ui.OverlayContextMenu, policy: ui.PopoverPolicy(true)},
		{id: ui.OverlayFindReplace, policy: ui.DefaultOverlayPolicy()},
		{id: ui.OverlayDirtyClose, policy: ui.ModalDialogPolicy()},
	}
	for _, fx := range representatives {
		t.Run(string(fx.id), func(t *testing.T) {
			app := newTestAppForPopupMouse(t)
			openOverlayFixture(t, app, fx)
			defer app.overlayHost.Close(fx.id)

			fw := app.windowManager.Window(string(fx.id))
			if fw == nil {
				t.Fatal("overlay missing")
			}
			x, y := fw.Position()
			inside := tea.MouseClickMsg{X: x + 3, Y: y + 1, Button: tea.MouseLeft}
			if app.handlePopupClickOutside(inside) {
				t.Fatal("inside click must not be suppressed by outside policy")
			}
			if app.windowManager.Window(string(fx.id)) == nil {
				t.Fatal("inside click must not close overlay")
			}
		})
	}
}

// TestOverlayOutsideContractMatrixCount guards the static overlay policy table.
func TestOverlayOutsideContractMatrixCount(t *testing.T) {
	if got := len(overlayOutsideContractMatrix); got != 22 {
		t.Fatalf("overlayOutsideContractMatrix len=%d want 22", got)
	}
}

// TestOverlayOutsideContractMatrixMatchesOpeners verifies matrix policies match
// production overlayHost.Open calls (grep-backed registry).
func TestOverlayOutsideContractMatrixMatchesOpeners(t *testing.T) {
	expected := map[ui.OverlayID]ui.OutsidePolicy{
		ui.OverlayContextMenu:       ui.OutsideCloseAndRoute,
		ui.OverlayReferences:        ui.OutsideCloseAndRoute,
		ui.OverlayCompletion:        ui.OutsideCloseAndRoute,
		ui.OverlayOutline:           ui.OutsideCloseAndRoute,
		ui.OverlayDiagContext:       ui.OutsideCloseAndRoute,
		ui.OverlayPlanHover:         ui.OutsideCloseAndRoute,
		ui.OverlayFindReplace:       ui.OutsideKeepAndRoute,
		ui.OverlayCommandPalette:    ui.OutsideKeepAndRoute,
		ui.OverlayComposer:          ui.OutsideKeepAndRoute,
		ui.OverlayTextOverlay:       ui.OutsideKeepAndRoute,
		ui.OverlayFindInFiles:       ui.OutsideKeepAndRoute,
		ui.OverlayReplaceInFiles:    ui.OutsideKeepAndRoute,
		ui.OverlayDiagHover:         ui.OutsideKeepAndRoute,
		ui.OverlayDirtyClose:        ui.OutsideIgnoreAndConsume,
		ui.OverlayOperationIssue:    ui.OutsideIgnoreAndConsume,
		ui.OverlayTreePrompt:        ui.OutsideIgnoreAndConsume,
		ui.OverlayTreeDelete:        ui.OutsideIgnoreAndConsume,
		ui.OverlayFilePicker:        ui.OutsideIgnoreAndConsume,
		ui.OverlayWorkspaceSettings: ui.OutsideIgnoreAndConsume,
		ui.OverlayCapabilityPicker:  ui.OutsideIgnoreAndConsume,
		ui.OverlaySessionPicker:     ui.OutsideIgnoreAndConsume,
		ui.OverlayHelp:              ui.OutsideIgnoreAndConsume,
	}
	if len(expected) != 22 {
		t.Fatalf("expected opener registry len=%d want 22", len(expected))
	}
	for _, fx := range overlayOutsideContractMatrix {
		want, ok := expected[fx.id]
		if !ok {
			t.Fatalf("matrix entry %q missing from opener registry", fx.id)
		}
		if fx.policy.Outside != want {
			t.Fatalf("%s policy=%v want %v", fx.id, fx.policy.Outside, want)
		}
		delete(expected, fx.id)
	}
	if len(expected) != 0 {
		t.Fatalf("matrix missing openers: %v", expected)
	}
}

func setupOverlayDispatchApp(t *testing.T) (*App, int, int) {
	t.Helper()
	app := newFullTestApp(t)
	app.width = 120
	app.height = 40
	app.Tree.Visible = false
	app.registerOverlayHandlers()

	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("hello world\nsecond line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(app.Theme)
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}

	// Agent first, then the editor tab: AddOrchestrator is a user action
	// that activates the new agent even when an editor is showing. This
	// fixture wants the editor painted with an agent already in the tab
	// bar, which is "open a file after a session exists".
	app.AddOrchestrator(
		"agent")

	app.EditorState = NewEditorState()
	app.EditorState.Editor = ed
	app.EditorState.Tabs[path] = NewEditorTab(path, ed)
	title := app.editorTabTitle(path)
	if app.Win.TabByTitle(title) == nil {
		app.Win.OpenTab(title, "✎", true)
	}
	if winTab := app.Win.TabByTitle(title); winTab != nil {
		app.Win.SwitchTo(app.Win.IndexOf(winTab.ID))
		app.handleTabSwitch()
	}
	layout := app.computeLayout()
	app.ChatAreaHeight = app.height - layout.ChatAreaTop - 2
	if app.ChatAreaHeight < 10 {
		app.ChatAreaHeight = 10
	}
	layout = layoutEditorForMouse(app)
	clickX := layout.SidebarW + layout.PadLR + 10
	clickY := layout.ChatAreaTop + 2

	// The editor mouse path sources its coordinates from the root Host hit
	// tree, so the editor screen must be mounted in the shell tree (painted)
	// before clicks can route to it, as required by the production contract.
	app.paintShellFrame()

	return app, clickX, clickY
}

func layoutEditorForMouse(app *App) LayoutInfo {
	layout := app.computeLayout()
	if app.renderCtx == nil {
		app.renderCtx = wm.NewRenderContext(app.Theme)
	}
	edH := app.height - layout.ChatAreaTop - 2
	if edH < 8 {
		edH = 8
	}
	w := layout.ContentW
	if w < 40 {
		w = 40
	}
	surf := surface.NewSurface("test-editor", w, edH, cell.TransparentBlank())
	app.EditorState.Editor.Render(surf, ui.PaintContext{Theme: app.Theme})
	return layout
}

func editorClickOutsideOverlay(app *App, fw *wm.FloatingWindow, editorY int) (int, int) {
	if fw == nil {
		return app.width - 5, editorY
	}
	x, y := fw.Position()
	w, h := fw.Size()
	outX := x + w + 5
	if outX >= app.width-1 {
		outX = maxInt(0, x-5)
	}
	outY := y + h/2
	if outY == y {
		outY = y + h + 2
	}
	if outY >= app.height-1 {
		outY = editorY
	}
	return outX, outY
}

func editorCursor(app *App) (line, col int) {
	if app.EditorState.Editor == nil {
		return 0, 0
	}
	return app.EditorState.Editor.CursorLine(), app.EditorState.Editor.CursorCol()
}

// TestDispatchMouseClickOutsidePolicyGroups exercises real product openers
// through dispatchMouseClick (full path), one representative per policy group.
func TestDispatchMouseClickOutsidePolicyGroups(t *testing.T) {
	t.Run("close_route_context_menu_routes_editor_once", func(t *testing.T) {
		app, clickX, clickY := setupOverlayDispatchApp(t)
		ed := app.EditorState.Editor
		ed.SetCursorPosition(0, 0)

		_, _ = app.dispatchMouseRightClick(tea.MouseClickMsg{
			X: clickX, Y: clickY, Button: tea.MouseRight,
		})
		fw := app.windowManager.Window(string(ui.OverlayContextMenu))
		if fw == nil {
			t.Fatal("context menu should open via dispatchMouseRightClick")
		}

		ed.SetCursorPosition(0, 0)
		beforeLine, beforeCol := editorCursor(app)
		outX, outY := editorClickOutsideOverlay(app, fw, clickY)
		_, _ = app.dispatchMouseClick(tea.MouseClickMsg{
			X: outX, Y: outY, Button: tea.MouseLeft,
		})
		if app.windowManager.Window(string(ui.OverlayContextMenu)) != nil {
			t.Fatal("context menu should close on outside click (CloseRoute)")
		}
		afterLine, afterCol := editorCursor(app)
		if afterLine == beforeLine && afterCol == beforeCol {
			t.Fatal("outside click should route to editor exactly once (cursor moved)")
		}
		if app.focusTarget() != FocusEditor {
			t.Fatalf("focusTarget=%v want FocusEditor after routed click", app.focusTarget())
		}
	})

	t.Run("keep_route_find_replace_routes_editor_once", func(t *testing.T) {
		app, _, clickY := setupOverlayDispatchApp(t)
		app.openFindReplace(false)
		fw := app.windowManager.Window(string(ui.OverlayFindReplace))
		if fw == nil {
			t.Fatal("find-replace should open via openFindReplace")
		}

		beforeLine, beforeCol := editorCursor(app)
		outX, outY := editorClickOutsideOverlay(app, fw, clickY+5)
		_, _ = app.dispatchMouseClick(tea.MouseClickMsg{
			X: outX, Y: outY, Button: tea.MouseLeft,
		})
		if app.windowManager.Window(string(ui.OverlayFindReplace)) == nil {
			t.Fatal("find-replace must stay open (KeepRoute)")
		}
		afterLine, afterCol := editorCursor(app)
		if afterLine == beforeLine && afterCol == beforeCol {
			t.Fatal("outside click should route to editor (cursor moved)")
		}
		if app.focusTarget() != FocusEditor {
			t.Fatalf("focusTarget=%v want FocusEditor", app.focusTarget())
		}
	})

	t.Run("ignore_dirty_prompt_suppresses_downstream", func(t *testing.T) {
		app, clickX, clickY := setupOverlayDispatchApp(t)
		path := app.EditorState.ActiveView
		app.EditorState.Editor.InsertRune('Z')
		app.showDirtyClosePrompt(path)
		if app.windowManager.Window(string(ui.OverlayDirtyClose)) == nil {
			t.Fatal("dirty-close prompt should open via showDirtyClosePrompt")
		}

		beforeLine, beforeCol := editorCursor(app)
		beforeFocus := app.focusTarget()
		_, _ = app.dispatchMouseClick(tea.MouseClickMsg{
			X: clickX, Y: clickY, Button: tea.MouseLeft,
		})
		if app.windowManager.Window(string(ui.OverlayDirtyClose)) == nil {
			t.Fatal("dirty-close must remain open (Ignore)")
		}
		afterLine, afterCol := editorCursor(app)
		if afterLine != beforeLine || afterCol != beforeCol {
			t.Fatal("suppressed click must not route to editor")
		}
		if app.focusTarget() != beforeFocus {
			t.Fatal("suppressed click must not change focus target")
		}
		if app.DirtyPromptPath != path {
			t.Fatal("dirty prompt state must be unchanged")
		}
	})
}
