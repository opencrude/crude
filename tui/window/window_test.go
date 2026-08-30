package window

import (
	"testing"

	"crude/tui/theme"
)

func newTestWindow() *Window {
	return New(theme.DefaultDark())
}

func TestOpenTabInactiveNeverStealsExistingActiveTab(t *testing.T) {
	w := newTestWindow()
	first := w.OpenTab("first", "", true)
	w.OpenTabInactive("background", "", true)
	if active := w.ActiveTab(); active == nil || active.ID != first.ID {
		t.Fatalf("background tab stole active selection: %#v", active)
	}
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

	tab := w.OpenTabWithLayout("Settings", "⚙", true, TabHorizontal)
	if tab.Layout != TabHorizontal {
		t.Errorf("expected TabHorizontal, got %d", tab.Layout)
	}
	if w.Layout() != TabHorizontal {
		t.Errorf("expected window layout TabHorizontal, got %d", w.Layout())
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

	w.OpenTabWithLayout("Settings", "⚙", true, TabHorizontal)
	w.OpenTab("Confirm", "⚠", true)

	if w.Layout() != TabHorizontal {
		t.Errorf("expected horizontal after opening Confirm, got %d", w.Layout())
	}

	w.SwitchTo(0)
	if w.Layout() != TabHorizontal {
		t.Errorf("expected horizontal after switching to Settings, got %d", w.Layout())
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
