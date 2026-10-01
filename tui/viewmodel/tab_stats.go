package viewmodel

import (
	statusoverview "crude/tui/screens/statusoverview"
	"crude/tui/ui"
	"crude/tui/wm"
)

func (m *App) openTabStats(targetID string) {
	if m.overlayHost == nil {
		return
	}
	projection := statusoverview.Projection{
		Active: statusoverview.ViewTabs,
		Views:  map[statusoverview.ViewID][]statusoverview.Item{statusoverview.ViewTabs: m.openTabItems()},
	}
	screen := statusoverview.NewStats(m.Theme, projection)
	if targetID != "" {
		screen.Select(targetID)
	}
	w, h := fitFloatingSize(m.width, m.height, 112, 26, 60, 12)
	frame := ui.NewWindowFrameWidget(m.Theme, ui.NewID(ui.RootID, "tab-stats-frame"), ui.FrameWindow, "Open Tabs", screen, nil, true, w, h)
	screen.SetFocusHandler(frame.Focus)
	frame.Focus(statusoverview.SearchID)
	win := wm.NewFloatingWindow(string(ui.OverlayTabStats), "Open Tabs", frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true),
		wm.WithClosable(true), wm.WithFocusable(true), wm.WithModal(true),
		wm.WithMinSize(minInt(60, w), minInt(12, h)), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(ui.OverlayTabStats, win, ui.ModalDialogPolicy(), nil)
}

// openTabItems uses the visible tab strip as its inventory and ordering source,
// not the retained agent/editor/terminal stores used by Workspace.
func (m *App) openTabItems() []statusoverview.Item {
	if m.Win == nil {
		return nil
	}
	byID := make(map[string]statusoverview.Item)
	for _, item := range m.statusTabItems() {
		if !item.Header {
			byID[item.ID] = item
		}
	}
	items := make([]statusoverview.Item, 0, m.Win.TabCount())
	for _, tab := range m.Win.Tabs() {
		id := ""
		if agentID := m.agentIDFromTabTitle(tab.Title); agentID != "" {
			if node := m.AgentMgr.Get(agentID); node == nil || node.IsHidden() {
				continue
			}
			id = "agent:" + agentID
		} else if path := m.editorPathFromTabTitle(tab.Title); path != "" {
			id = "editor:" + path
		}
		if item, ok := byID[id]; ok {
			items = append(items, item)
		}
	}
	return items
}
