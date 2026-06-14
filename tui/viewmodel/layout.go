package viewmodel

type LayoutInfo struct {
	SidebarW    int
	ContentW    int
	InnerW      int
	ChatAreaTop int
	ChatH       int
	PadLR       int
}

func (m *App) computeLayout() LayoutInfo {
	sidebarW := 0
	if m.treeVisible && m.treeModel != nil && m.treeRenderer != nil {
		sidebarW = m.treeWidth + 1
	}

	contentW := m.width - sidebarW
	padLR := m.theme.Spacing.PadLR
	innerW := contentW - padLR*2
	if innerW < 20 {
		innerW = 20
	}

	chatAreaTop := 3
	if m.win.IsOpen() && m.win.TabCount() >= 2 {
		chatAreaTop += 2
	}

	return LayoutInfo{
		SidebarW:    sidebarW,
		ContentW:    contentW,
		InnerW:      innerW,
		ChatAreaTop: chatAreaTop,
		PadLR:       padLR,
	}
}
