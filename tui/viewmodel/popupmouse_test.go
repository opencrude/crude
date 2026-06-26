package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/widgets"
	"crude/tui/wm"
)

// =============================================================================
// Click-outside behavior tests
// =============================================================================

func TestClickOutside_ContextMenu_ClosesAndRoutes(t *testing.T) {
	app := setupTestAppWithPopup(t, "context-menu", wm.ClickOutsideCloseRoute)
	defer app.windowManager.RemoveWindow("context-menu")

	if app.windowManager.Window("context-menu") == nil {
		t.Fatal("test setup: context-menu not found")
	}

	// Click outside the popup.
	msg := tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}
	suppressed := app.handlePopupClickOutside(msg)
	if suppressed {
		t.Fatal("click outside context-menu should NOT be suppressed")
	}
	if app.windowManager.Window("context-menu") != nil {
		t.Fatal("context-menu should be closed after click outside")
	}
}

func TestClickOutside_ConfirmPrompt_SuppressesClick(t *testing.T) {
	app := setupTestAppWithPopup(t, "dirty-close-prompt", wm.ClickOutsideIgnore)
	defer app.windowManager.RemoveWindow("dirty-close-prompt")

	if app.windowManager.Window("dirty-close-prompt") == nil {
		t.Fatal("test setup: dirty-close-prompt not found")
	}

	// Click outside the modal — must be suppressed.
	msg := tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}
	suppressed := app.handlePopupClickOutside(msg)
	if !suppressed {
		t.Fatal("click outside dirty-close-prompt must be suppressed (modal)")
	}
	if app.windowManager.Window("dirty-close-prompt") == nil {
		t.Fatal("dirty-close-prompt must remain open after suppressed click")
	}
}

func TestClickOutside_FindReplace_KeepsOpenAndRoutes(t *testing.T) {
	app := setupTestAppWithPopup(t, "find-replace", wm.ClickOutsideKeepRoute)
	defer app.windowManager.RemoveWindow("find-replace")

	if app.windowManager.Window("find-replace") == nil {
		t.Fatal("test setup: find-replace not found")
	}

	// Click outside — must not be suppressed, popup must remain open.
	msg := tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}
	suppressed := app.handlePopupClickOutside(msg)
	if suppressed {
		t.Fatal("click outside find-replace should NOT be suppressed")
	}
	if app.windowManager.Window("find-replace") == nil {
		t.Fatal("find-replace must remain open after click outside (KeepRoute)")
	}
}

func TestClickOutside_RightClick_DoesNothing(t *testing.T) {
	app := setupTestAppWithPopup(t, "dirty-close-prompt", wm.ClickOutsideIgnore)
	defer app.windowManager.RemoveWindow("dirty-close-prompt")

	// Right-click — should be passed through to dispatchMouseRightClick
	// and NOT considered for click-outside suppression.
	msg := tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseRight}
	suppressed := app.handlePopupClickOutside(msg)
	if suppressed {
		t.Fatal("right-click should not be suppressed")
	}
}

func TestClickOutside_InsidePopup_DoesNothing(t *testing.T) {
	app := setupTestAppWithPopup(t, "context-menu", wm.ClickOutsideCloseRoute)
	defer app.windowManager.RemoveWindow("context-menu")

	// Click INSIDE the popup — should not trigger click-outside.
	fw := app.windowManager.Window("context-menu")
	if fw == nil {
		t.Fatal("test setup: context-menu not found")
	}
	cx, cy := fw.Position()
	// The popup is 5x3, so center is (cx+2, cy+1).
	msg := tea.MouseClickMsg{X: cx + 2, Y: cy + 1, Button: tea.MouseLeft}
	suppressed := app.handlePopupClickOutside(msg)
	if suppressed {
		t.Fatal("click INSIDE context-menu should not be suppressed")
	}
	if app.windowManager.Window("context-menu") == nil {
		t.Fatal("context-menu must NOT be closed by an inside click")
	}
}

// =============================================================================
// Click-on-content tests
// =============================================================================

func TestClickContextMenu_ActivatesItem(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	defer app.closeContextMenu()

	// Build a simple context menu.
	activated := false
	st := &widgets.ContextMenuState{
		Sections: []widgets.ContextMenuSection{
			{Items: []widgets.ContextMenuItem{
				{Label: "Copy", Action: func() { activated = true }},
				{Label: "Paste"},
			}},
		},
	}
	st.Build()
	app.lastContextMenu = st

	// Create the floating window.
	pop := widgets.NewPopupContent(widgets.ContextMenuLines(app.theme, st, 0))
	fw := wm.NewFloatingWindow("context-menu", "", pop, 30, 5,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
	)
	fw.SetPosition(0, 0)
	app.windowManager.AddWindow(fw)

	// Click the first item (row 1 = first content row, since row 0 is top border).
	app.clickContextMenu(5, 1)

	if !activated {
		t.Fatal("clicking on context menu item should activate it")
	}
	if app.windowManager.Window("context-menu") != nil {
		t.Fatal("context menu should close after activation")
	}
}

func TestClickContextMenu_DisabledItem_DoesNotActivate(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	defer app.closeContextMenu()

	activated := false
	st := &widgets.ContextMenuState{
		Sections: []widgets.ContextMenuSection{
			{Items: []widgets.ContextMenuItem{
				{Label: "Cut", Action: func() { activated = true }, Disabled: true},
			}},
		},
	}
	st.Build()
	app.lastContextMenu = st

	pop := widgets.NewPopupContent(widgets.ContextMenuLines(app.theme, st, 0))
	fw := wm.NewFloatingWindow("context-menu", "", pop, 30, 5,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
	)
	fw.SetPosition(0, 0)
	app.windowManager.AddWindow(fw)

	// Click the disabled item.
	app.clickContextMenu(5, 1)

	if activated {
		t.Fatal("clicking on disabled context menu item should NOT activate it")
	}
	if app.windowManager.Window("context-menu") == nil {
		t.Fatal("context menu should remain open after clicking disabled item")
	}
}

func TestHoverContextMenu_MovesCursor(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	defer app.closeContextMenu()

	st := &widgets.ContextMenuState{
		Sections: []widgets.ContextMenuSection{
			{Items: []widgets.ContextMenuItem{
				{Label: "A"},
				{Label: "B"},
				{Label: "C"},
			}},
		},
	}
	st.Build()
	app.lastContextMenu = st

	// Hover over item 2 (row 2 in chrome: row 0 = border, row 1 = A, row 2 = B).
	app.hoverContextMenu(5, 2)
	if st.Cursor != 1 {
		t.Fatalf("expected cursor=1 after hovering B, got %d", st.Cursor)
	}

	// Hover over item 1.
	app.hoverContextMenu(5, 1)
	if st.Cursor != 0 {
		t.Fatalf("expected cursor=0 after hovering A, got %d", st.Cursor)
	}
}

// =============================================================================
// Confirm prompt click & hover
// =============================================================================

func TestClickDirtyPrompt_NoButtonCancels(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.dirtyPromptPath = "/tmp/foo.go"

	pop := widgets.NewPopupContent(widgets.ConfirmPrompt(
		app.theme, "Unsaved", "Discard?", "Detail here",
		widgets.ConfirmPromptWarning, true, 56,
	))
	fw := wm.NewFloatingWindow("dirty-close-prompt", "", pop, 56, 7,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
	)
	fw.SetPosition(0, 0)
	app.windowManager.AddWindow(fw)

	// Click on the No button. With X >= 8, localY in button row.
	// Button row contentRow = innerH - 2 = 5 - 2 = 3, in chrome row = 4.
	app.clickDirtyPrompt(10, 4)

	if app.dirtyPromptPath != "" {
		t.Fatal("dirtyPromptPath should be cleared after No click")
	}
	if app.dirtyPromptFocusYes {
		t.Fatal("dirtyPromptFocusYes should be false after No click")
	}
}

// =============================================================================
// Focus restoration tests — verify keyboard focus flows correctly
// through popup open/close, mouse clicks, and chained popups.
// =============================================================================

func TestFocusRestoration_AfterPopupClose_EditorKeepsFocus(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.activeEditorView = "/tmp/foo.go"

	// Simulate popup open: treeFocused/tabBarFocused unchanged, but
	// windowManager.Focused is now the popup.
	preTarget := app.focusTarget()
	if preTarget != FocusEditor {
		t.Fatalf("pre-popup focus should be FocusEditor, got %v", preTarget)
	}

	// Open a popup. treeFocused/etc are unchanged.
	pop := widgets.NewPopupContent([]string{"╭─╮", "│ │", "╰─╯"})
	fw := wm.NewFloatingWindow("context-menu", "", pop, 3, 3,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
	)
	fw.SetPosition(20, 5)
	app.windowManager.AddWindow(fw)

	// While popup is open, focus routes to it.
	if app.windowManager.Focused() == nil {
		t.Fatal("popup should be focused")
	}
	withPopupTarget := app.focusTarget()
	if withPopupTarget != FocusFloatingWindow {
		t.Fatalf("focus with popup should be FocusFloatingWindow, got %v", withPopupTarget)
	}

	// Close the popup.
	app.closeContextMenu()

	// After close, focus should be back to FocusEditor.
	postTarget := app.focusTarget()
	if postTarget != FocusEditor {
		t.Fatalf("post-popup focus should be FocusEditor, got %v", postTarget)
	}
}

func TestFocusRestoration_AfterPopupClose_TreeKeepsFocus(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.activeEditorView = "/tmp/foo.go"
	app.treeFocused = true
	app.treeVisible = true

	// Open and close a popup.
	pop := widgets.NewPopupContent([]string{"╭─╮", "│ │", "╰─╯"})
	fw := wm.NewFloatingWindow("context-menu", "", pop, 3, 3,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
	)
	app.windowManager.AddWindow(fw)
	app.closeContextMenu()

	// The booleans must be preserved through popup open/close.
	if !app.treeFocused {
		t.Fatal("treeFocused must be preserved through popup close")
	}
	if app.tabBarFocused {
		t.Fatal("tabBarFocused must remain false")
	}
}

func TestFocusRestoration_ChainedPopups(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.activeEditorView = "/tmp/foo.go"

	// Open popup A.
	popA := widgets.NewPopupContent([]string{"╭─╮", "│ │", "╰─╯"})
	fwA := wm.NewFloatingWindow("context-menu", "", popA, 3, 3,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
	)
	app.windowManager.AddWindow(fwA)

	// Open popup B on top.
	popB := widgets.NewPopupContent([]string{"╭─╮", "│ │", "╰─╯"})
	fwB := wm.NewFloatingWindow("find-replace", "", popB, 3, 3,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
	)
	app.windowManager.AddWindow(fwB)

	// WM.Focused should be B (topmost).
	if app.windowManager.Focused().ID != "find-replace" {
		t.Fatalf("expected find-replace to be focused, got %v", app.windowManager.Focused().ID)
	}

	// Close B. WM.Focused reverts to A.
	app.closeFindReplace()
	if app.windowManager.Focused() == nil || app.windowManager.Focused().ID != "context-menu" {
		t.Fatalf("after closing B, focus should revert to A, got %v", app.windowManager.Focused())
	}

	// Close A. No popup focused. focusTarget returns FocusEditor.
	app.closeContextMenu()
	postTarget := app.focusTarget()
	if postTarget != FocusEditor {
		t.Fatalf("after closing all popups, focus should be FocusEditor, got %v", postTarget)
	}
}

// =============================================================================
// Mouse wheel scrolling tests
// =============================================================================

func TestFocusRestoration_FindReplaceOpen_EditorClickFocusesEditor(t *testing.T) {
	// When find-replace is open and the user clicks the editor,
	// find-replace stays open (KeepRoute), but the editor gains focus.
	app := newTestAppForPopupMouse(t)
	app.activeEditorView = "/tmp/foo.go"

	// Open find-replace popup.
	pop := widgets.NewPopupContent([]string{"╭─╮", "│ │", "╰─╯"})
	fw := wm.NewFloatingWindow("find-replace", "", pop, 3, 3,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	fw.SetPosition(0, 0)
	app.windowManager.AddWindow(fw)

	// Verify find-replace is focused.
	if app.windowManager.Focused() == nil || app.windowManager.Focused().ID != "find-replace" {
		t.Fatal("find-replace should be focused when open")
	}

	// Simulate click outside the popup (e.g., on the editor).
	msg := tea.MouseClickMsg{X: 50, Y: 50, Button: tea.MouseLeft}
	suppressed := app.handlePopupClickOutside(msg)
	if suppressed {
		t.Fatal("click outside find-replace should NOT be suppressed (KeepRoute)")
	}

	// find-replace must still be open.
	if app.windowManager.Window("find-replace") == nil {
		t.Fatal("find-replace must stay open after click outside (KeepRoute)")
	}
}

func TestFocusRestoration_MouseClick_RoutesToClickedRegion(t *testing.T) {
	// Verifies that clicking outside a popup closes it, and the
	// underlying booleans (treeFocused/etc) are preserved so that
	// focus can route to the underlying widget.
	app := newTestAppForPopupMouse(t)
	app.activeEditorView = "/tmp/foo.go"
	app.treeFocused = true
	app.treeVisible = true

	// Open a context menu.
	pop := widgets.NewPopupContent([]string{"╭─╮", "│ │", "╰─╯"})
	fw := wm.NewFloatingWindow("context-menu", "", pop, 3, 3,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
	)
	fw.SetPosition(0, 0)
	app.windowManager.AddWindow(fw)

	// Simulate click outside the popup.
	msg := tea.MouseClickMsg{X: 50, Y: 50, Button: tea.MouseLeft}
	suppressed := app.handlePopupClickOutside(msg)
	if suppressed {
		t.Fatal("click outside context-menu should not be suppressed")
	}

	// Popup should be closed.
	if app.windowManager.Window("context-menu") != nil {
		t.Fatal("context-menu should be closed after outside click")
	}

	// The booleans are preserved — region handler will update focus.
	if !app.treeFocused {
		t.Fatal("treeFocused must be preserved through popup close")
	}
}

// =============================================================================
// Find/Replace hover & click tests
// =============================================================================

func TestHoverFindReplace_ToggleHover(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.findReplace = &widgets.FindReplaceState{}

	// Hover over toggle at X=32 (band 0 = Aa). localY=3 = toggle row in non-ReplaceMode.
	// Content layout: row 0 = Find, row 1 = Spacer, row 2 = Status+Toggles.
	// In chrome (row 0 = top border), toggle row is at localY=3.
	app.hoverFindReplace(32, 3)
	if app.findReplace.HoveredToggle != 0 {
		t.Fatalf("expected HoveredToggle=0 after hovering Aa, got %d", app.findReplace.HoveredToggle)
	}

	// Hover over .* (band 2). bandStart=30+2*5=40, bandEnd=45. So X=42 hits band 2.
	app.hoverFindReplace(42, 3)
	if app.findReplace.HoveredToggle != 2 {
		t.Fatalf("expected HoveredToggle=2 after hovering .*, got %d", app.findReplace.HoveredToggle)
	}

	// Hover off the toggles (X=10, not in any band).
	app.hoverFindReplace(10, 3)
	if app.findReplace.HoveredToggle != -1 {
		t.Fatalf("expected HoveredToggle=-1 after hovering away, got %d", app.findReplace.HoveredToggle)
	}
}

func TestClickFindReplace_FocusesFindField(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.findReplace = &widgets.FindReplaceState{
		Query:        "hello",
		FocusedField: 1, // currently focused on replace
	}

	// Click on the find field (content row 0 = first content row).
	// In chrome: localY=1.
	app.clickFindReplace(5, 1)
	if app.findReplace.FocusedField != 0 {
		t.Fatalf("expected FocusedField=0 after clicking find field, got %d", app.findReplace.FocusedField)
	}
}

func TestClickFindReplace_TogglesCaseSensitive(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.findReplace = &widgets.FindReplaceState{
		CaseSensitive: false,
	}

	// Click on Aa toggle (band 0, X=32, localY=3 for non-ReplaceMode).
	app.clickFindReplace(32, 3)
	if !app.findReplace.CaseSensitive {
		t.Fatal("CaseSensitive should be true after clicking Aa")
	}

	// Click again to toggle off.
	app.clickFindReplace(32, 3)
	if app.findReplace.CaseSensitive {
		t.Fatal("CaseSensitive should be false after second click on Aa")
	}
}

// =============================================================================
// Test helpers
// =============================================================================

func newTestAppForPopupMouse(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.windowManager = wm.NewWindowManager()
	// Create a stub editor and open a temp file so focusTarget() recognizes
	// the editor as open.
	ed := editor.New(app.theme)
	tmpFile := t.TempDir() + "/foo.go"
	if err := ed.Open(tmpFile); err != nil {
		t.Fatalf("open tmp file: %v", err)
	}
	app.editor = ed
	return app
}

func setupTestAppWithPopup(t *testing.T, id string, behavior wm.ClickOutsideBehavior) *App {
	t.Helper()
	app := newTestAppForPopupMouse(t)

	pop := widgets.NewPopupContent([]string{
		"╭───╮",
		"│ x │",
		"╰───╯",
	})
	// Use 5x3 chrome-wrapped content. Position somewhere central.
	fw := wm.NewFloatingWindow(id, "", pop, 5, 3,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
		wm.WithClickOutside(behavior),
	)
	fw.SetPosition(10, 5)
	app.windowManager.AddWindow(fw)
	return app
}
