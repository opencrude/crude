package window

import (
	"testing"

	"crude/tui/theme"
)

func newTestWindow() *Window {
	return New(theme.DefaultDark())
}

func TestOpenTab(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	tab := w.OpenTab("Settings", "⚙", true)
	if tab == nil {
		t.Fatal("expected non-nil tab")
	}
	if tab.Title != "Settings" {
		t.Errorf("expected title Settings, got %s", tab.Title)
	}
	if !tab.Closable {
		t.Error("expected tab to be closable")
	}
	if !w.IsOpen() {
		t.Error("expected window to be open")
	}
	if w.TabCount() != 1 {
		t.Errorf("expected 1 tab, got %d", w.TabCount())
	}
	if w.ActiveIndex() != 0 {
		t.Errorf("expected active index 0, got %d", w.ActiveIndex())
	}
}

func TestOpenTabDedup(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Settings", "⚙", true)
	w.OpenTab("Settings", "⚙", true)

	if w.TabCount() != 1 {
		t.Errorf("expected 1 tab after dedup, got %d", w.TabCount())
	}
}

func TestOpenMultipleTabs(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Settings", "⚙", true)
	w.OpenTab("Confirm", "⚠", true)

	if w.TabCount() != 2 {
		t.Errorf("expected 2 tabs, got %d", w.TabCount())
	}
	if w.ActiveIndex() != 1 {
		t.Errorf("expected active index 1, got %d", w.ActiveIndex())
	}
}

func TestOpenTabWithLayout(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	tab := w.OpenTabWithLayout("Settings", "⚙", true, TabVertical)
	if tab.Layout != TabVertical {
		t.Errorf("expected TabVertical, got %d", tab.Layout)
	}
	if w.Layout() != TabVertical {
		t.Errorf("expected window layout TabVertical, got %d", w.Layout())
	}
}

func TestCloseTab(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Settings", "⚙", true)
	action := w.CloseTab(0)

	if action != CloseSwitchToChat {
		t.Errorf("expected CloseSwitchToChat for last tab, got %d", action)
	}
	if w.IsOpen() {
		t.Error("expected window to be closed after last tab removed")
	}
	if w.Layout() != TabHorizontal {
		t.Error("expected layout reset to horizontal after close all")
	}
}

func TestCloseTabNotClosable(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Main", "", false)
	action := w.CloseTab(0)

	if action != CloseNone {
		t.Errorf("expected CloseNone for non-closable tab, got %d", action)
	}
	if !w.IsOpen() {
		t.Error("expected window to remain open")
	}
}

func TestCloseTabByID(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	tab := w.OpenTab("Settings", "⚙", true)
	action := w.CloseTabByID(tab.ID)

	if action != CloseSwitchToChat {
		t.Errorf("expected CloseSwitchToChat for last tab, got %d", action)
	}
}

func TestCloseTabActiveIndexAdjustment(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Settings", "⚙", true)
	w.OpenTab("Confirm", "⚠", true)

	if w.ActiveIndex() != 1 {
		t.Errorf("expected active 1, got %d", w.ActiveIndex())
	}

	w.CloseTab(1)
	if w.ActiveIndex() != 0 {
		t.Errorf("expected active 0 after closing tab at 1, got %d", w.ActiveIndex())
	}
}

func TestCloseAll(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Settings", "⚙", true)
	w.OpenTab("Confirm", "⚠", true)
	w.CloseAll()

	if w.IsOpen() {
		t.Error("expected window to be closed")
	}
	if w.TabCount() != 0 {
		t.Errorf("expected 0 tabs, got %d", w.TabCount())
	}
	if w.Layout() != TabHorizontal {
		t.Error("expected layout reset to horizontal after close all")
	}
}

func TestSwitchTo(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Settings", "⚙", true)
	w.OpenTab("Confirm", "⚠", true)

	w.SwitchTo(0)
	if w.ActiveIndex() != 0 {
		t.Errorf("expected active 0, got %d", w.ActiveIndex())
	}
	if w.ActiveTab().Title != "Settings" {
		t.Errorf("expected active tab Settings, got %s", w.ActiveTab().Title)
	}
}

func TestSwitchToUpdatesLayout(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTabWithLayout("Settings", "⚙", true, TabVertical)
	w.OpenTab("Confirm", "⚠", true)

	if w.Layout() != TabHorizontal {
		t.Errorf("expected horizontal after opening Confirm, got %d", w.Layout())
	}

	w.SwitchTo(0)
	if w.Layout() != TabVertical {
		t.Errorf("expected vertical after switching to Settings, got %d", w.Layout())
	}
}

func TestNextPrev(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("A", "", true)
	w.OpenTab("B", "", true)
	w.OpenTab("C", "", true)

	w.SwitchTo(0)
	w.Next()
	if w.ActiveIndex() != 1 {
		t.Errorf("expected active 1 after Next, got %d", w.ActiveIndex())
	}
	w.Next()
	if w.ActiveIndex() != 2 {
		t.Errorf("expected active 2 after Next, got %d", w.ActiveIndex())
	}
	w.Next()
	if w.ActiveIndex() != 0 {
		t.Errorf("expected active 0 after wrapping Next, got %d", w.ActiveIndex())
	}

	w.Prev()
	if w.ActiveIndex() != 2 {
		t.Errorf("expected active 2 after wrapping Prev, got %d", w.ActiveIndex())
	}
	w.Prev()
	if w.ActiveIndex() != 1 {
		t.Errorf("expected active 1 after Prev, got %d", w.ActiveIndex())
	}
}

func TestTabByTitle(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Settings", "⚙", true)
	tab := w.TabByTitle("Settings")
	if tab == nil {
		t.Fatal("expected to find Settings tab")
	}
	if tab.Title != "Settings" {
		t.Errorf("expected title Settings, got %s", tab.Title)
	}

	missing := w.TabByTitle("NonExistent")
	if missing != nil {
		t.Error("expected nil for non-existent tab")
	}
}

func TestHasClosableTabs(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	if w.HasClosableTabs() {
		t.Error("expected no closable tabs on empty window")
	}

	w.OpenTab("Main", "", false)
	if w.HasClosableTabs() {
		t.Error("expected no closable tabs with only non-closable tab")
	}

	w.OpenTab("Settings", "⚙", true)
	if !w.HasClosableTabs() {
		t.Error("expected closable tabs after adding closable tab")
	}
}

func TestHitTestHorizontal(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Settings", "⚙", true)

	hit := w.HitTest(5, 0)
	if hit.TabIndex < 0 {
		t.Error("expected to hit a tab")
	}

	hit = w.HitTest(5, 1)
	if hit.TabIndex != -1 {
		t.Error("expected no hit on wrong Y line")
	}

	hit = w.HitTest(200, 0)
	if hit.TabIndex != -1 {
		t.Error("expected no hit beyond tab bar")
	}
}

func TestHitTestClose(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Settings", "⚙", true)

	tabWidths := w.computeTabWidths()
	if len(tabWidths) == 0 {
		t.Fatal("expected tab widths")
	}

	closeStart := tabBarPadLeft + tabWidths[0] - closeBtnWidth - tabPadRight
	hit := w.HitTest(closeStart, 0)
	if hit.TabIndex != 0 {
		t.Errorf("expected tab index 0, got %d", hit.TabIndex)
	}
	if !hit.IsClose {
		t.Error("expected close button hit")
	}
}

func TestHitTestNotClosable(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Main", "", false)

	tabWidths := w.computeTabWidths()
	closeStart := tabBarPadLeft + tabWidths[0] - closeBtnWidth - tabPadRight
	hit := w.HitTest(closeStart, 0)
	if hit.IsClose {
		t.Error("expected no close hit for non-closable tab")
	}
}

func TestHitTestVertical(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTabWithLayout("Settings", "⚙", true, TabVertical)
	w.OpenTabWithLayout("Confirm", "⚠", true, TabVertical)

	tabBarW := w.VTabBarWidth()
	contentWidth := w.width - tabBarW
	tabX := contentWidth + 2

	hit := w.HitTest(tabX, 0)
	if hit.TabIndex != 0 {
		t.Errorf("expected tab index 0, got %d", hit.TabIndex)
	}
	if hit.IsClose {
		t.Error("expected no close hit in middle of tab item")
	}

	hit = w.HitTest(tabX, 2)
	if hit.TabIndex != 1 {
		t.Errorf("expected tab index 1, got %d", hit.TabIndex)
	}

	hit = w.HitTest(tabX, 1)
	if hit.IsClose {
		t.Error("expected no close hit on separator line")
	}

	hit = w.HitTest(0, 0)
	if hit.TabIndex != -1 {
		t.Error("expected no hit in content area")
	}
}

func TestHitTestVerticalClose(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTabWithLayout("Settings", "⚙", true, TabVertical)
	w.OpenTabWithLayout("Confirm", "⚠", true, TabVertical)

	tabBarW := w.VTabBarWidth()
	contentWidth := w.width - tabBarW
	closeStart := contentWidth + tabBarW - vCloseBtnWidth

	hit := w.HitTest(closeStart, 0)
	if hit.TabIndex != 0 {
		t.Errorf("expected tab index 0, got %d", hit.TabIndex)
	}
	if !hit.IsClose {
		t.Error("expected close hit")
	}

	hit = w.HitTest(closeStart, 1)
	if hit.IsClose {
		t.Error("expected no close hit on separator line")
	}
}

func TestMoveTab(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("A", "", true)
	w.OpenTab("B", "", true)
	w.OpenTab("C", "", true)

	w.MoveTab(0, 2)
	if w.tabs[0].Title != "B" {
		t.Errorf("expected B at 0, got %s", w.tabs[0].Title)
	}
	if w.tabs[1].Title != "C" {
		t.Errorf("expected C at 1, got %s", w.tabs[1].Title)
	}
	if w.tabs[2].Title != "A" {
		t.Errorf("expected A at 2, got %s", w.tabs[2].Title)
	}
}

func TestMoveTabActiveTracking(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("A", "", true)
	w.OpenTab("B", "", true)
	w.OpenTab("C", "", true)

	w.SwitchTo(0)
	w.MoveTab(0, 2)
	if w.ActiveIndex() != 2 {
		t.Errorf("expected active 2 after moving active tab, got %d", w.ActiveIndex())
	}
}

func TestRenderTabBar(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	if w.RenderTabBar() != "" {
		t.Error("expected empty tab bar for closed window")
	}

	w.OpenTab("Settings", "⚙", true)
	result := w.RenderTabBar()
	if result == "" {
		t.Error("expected non-empty tab bar")
	}
}

func TestRenderTabBarVerticalReturnsEmpty(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTabWithLayout("Settings", "⚙", true, TabVertical)
	result := w.RenderTabBar()
	if result != "" {
		t.Error("expected empty tab bar for vertical layout")
	}
}

func TestRenderSeparator(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	if w.RenderSeparator() != "" {
		t.Error("expected empty separator for closed window")
	}

	w.OpenTab("Settings", "⚙", true)
	result := w.RenderSeparator()
	if result == "" {
		t.Error("expected non-empty separator")
	}
}

func TestRenderVerticalSidebar(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	contentLines := []string{"line1", "line2", "line3"}

	result := w.RenderVerticalSidebar(contentLines)
	if result != nil {
		t.Error("expected nil for closed window")
	}

	w.OpenTab("Settings", "⚙", true)
	result = w.RenderVerticalSidebar(contentLines)
	if result != nil {
		t.Error("expected nil for horizontal layout window")
	}

	w.CloseAll()
	w.OpenTabWithLayout("Settings", "⚙", true, TabVertical)
	result = w.RenderVerticalSidebar(contentLines)
	if result != nil {
		t.Error("expected nil for single tab vertical layout")
	}

	w.CloseAll()
	w.OpenTabWithLayout("Settings", "⚙", true, TabVertical)
	w.OpenTabWithLayout("Confirm", "⚠", true, TabVertical)
	result = w.RenderVerticalSidebar(contentLines)
	if result == nil {
		t.Fatal("expected non-nil result for vertical layout with 2 tabs")
	}
	if len(result) < 3 {
		t.Errorf("expected at least 3 lines (2 tabs + 1 separator), got %d", len(result))
	}
	sepLine := result[1]
	if !contains(sepLine, "─") {
		t.Errorf("expected separator line at index 1, got %q", sepLine)
	}
}

func TestVerticalContentWidth(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	if w.VerticalContentWidth() != 80 {
		t.Errorf("expected 80 for horizontal layout, got %d", w.VerticalContentWidth())
	}

	w.OpenTabWithLayout("Settings", "⚙", true, TabVertical)
	if w.VerticalContentWidth() != 80 {
		t.Errorf("expected 80 for single tab vertical layout, got %d", w.VerticalContentWidth())
	}

	w.OpenTabWithLayout("Confirm", "⚠", true, TabVertical)
	expected := 80 - w.VTabBarWidth()
	if w.VerticalContentWidth() != expected {
		t.Errorf("expected %d for multi-tab vertical layout, got %d", expected, w.VerticalContentWidth())
	}
}

func TestVTabBarWidthDynamic(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	// No tabs or single tab → 0
	if w.VTabBarWidth() != 0 {
		t.Errorf("expected 0 for no tabs, got %d", w.VTabBarWidth())
	}
	w.OpenTabWithLayout("A", "", true, TabVertical)
	if w.VTabBarWidth() != 0 {
		t.Errorf("expected 0 for single tab, got %d", w.VTabBarWidth())
	}

	// Two short tabs → should hit minimum (12)
	w.OpenTabWithLayout("B", "", true, TabVertical)
	got := w.VTabBarWidth()
	if got < vTabBarMinWidth {
		t.Errorf("expected >= %d for short tabs, got %d", vTabBarMinWidth, got)
	}

	// Tabs with long names → width should grow
	w.CloseAll()
	w.OpenTabWithLayout("1. Backend technology", "○", true, TabVertical)
	w.OpenTabWithLayout("2. Database choice", "○", true, TabVertical)
	wideGot := w.VTabBarWidth()
	if wideGot <= vTabBarMinWidth {
		t.Errorf("expected wider than %d for long titles, got %d", vTabBarMinWidth, wideGot)
	}

	// Content area must remain at least vMinContentWidth
	contentW := 80 - wideGot
	if contentW < vMinContentWidth {
		t.Errorf("content area %d is below minimum %d", contentW, vMinContentWidth)
	}

	// Narrow terminal → tab bar capped, content preserved
	w.SetWidth(50)
	narrowGot := w.VTabBarWidth()
	if 50-narrowGot < vMinContentWidth {
		t.Errorf("narrow terminal: content area %d below minimum %d", 50-narrowGot, vMinContentWidth)
	}

	// Wide terminal → tab bar grows to show more, but stays ≤ max
	w.SetWidth(200)
	widerGot := w.VTabBarWidth()
	if widerGot > vTabBarMaxAbsolute {
		t.Errorf("expected <= %d on wide terminal, got %d", vTabBarMaxAbsolute, widerGot)
	}
	if widerGot < wideGot {
		t.Errorf("wider terminal should not produce smaller tab bar: %d < %d", widerGot, wideGot)
	}
}

func TestHint(t *testing.T) {
	w := newTestWindow()

	if w.Hint() != "" {
		t.Error("expected empty hint for closed window")
	}

	w.OpenTab("Settings", "⚙", true)
	hint := w.Hint()
	if hint == "" {
		t.Error("expected non-empty hint for open window with closable tab")
	}
	if !contains(hint, "×") && !contains(hint, "esc") {
		t.Errorf("expected hint to mention close action, got %q", hint)
	}

	w.SetHint("custom hint")
	if w.Hint() != "[ custom hint ]" {
		t.Errorf("expected wrapped custom hint, got %q", w.Hint())
	}
}

func TestDisplayTitles(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("Tab1", "A", true)
	w.OpenTab("Tab2", "B", true)

	titles := w.DisplayTitles(20)
	if len(titles) != 2 {
		t.Fatalf("expected 2 titles, got %d", len(titles))
	}
	if titles[0] != "Tab1" {
		t.Errorf("expected Tab1, got %s", titles[0])
	}
}

func TestTabIDs(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	t1 := w.OpenTab("A", "", true)
	t2 := w.OpenTab("B", "", true)

	ids := w.TabIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %d", len(ids))
	}
	if ids[0] != t1.ID {
		t.Errorf("expected ID %d, got %d", t1.ID, ids[0])
	}
	if ids[1] != t2.ID {
		t.Errorf("expected ID %d, got %d", t2.ID, ids[1])
	}
}

func TestIndexOf(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	tab := w.OpenTab("A", "", true)
	if w.IndexOf(tab.ID) != 0 {
		t.Errorf("expected index 0, got %d", w.IndexOf(tab.ID))
	}
	if w.IndexOf(TabID(99999)) != -1 {
		t.Error("expected -1 for non-existent ID")
	}
}

func TestTogglePin(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("A", "", true)
	if w.tabs[0].Pinned {
		t.Error("expected tab to not be pinned initially")
	}
	w.TogglePin(0)
	if !w.tabs[0].Pinned {
		t.Error("expected tab to be pinned after toggle")
	}
	w.TogglePin(0)
	if w.tabs[0].Pinned {
		t.Error("expected tab to not be pinned after second toggle")
	}
}

func TestSetTabState(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("A", "", true)
	w.SetTabState(0, TabError)
	if w.tabs[0].State != TabError {
		t.Errorf("expected TabError, got %d", w.tabs[0].State)
	}
}

func TestCloseTabPinned(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("A", "", true)
	w.OpenTab("B", "", true)
	w.TogglePin(0)

	action := w.CloseTab(0)
	if action != CloseNone {
		t.Errorf("expected CloseNone for pinned tab, got %d", action)
	}
}

func TestCloseTabPinnedLastTab(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	w.OpenTab("A", "", true)
	w.TogglePin(0)

	action := w.CloseTab(0)
	if action != CloseSwitchToChat {
		t.Errorf("expected CloseSwitchToChat for pinned last tab, got %d", action)
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
