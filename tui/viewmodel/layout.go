package viewmodel

type LayoutInfo struct {
	SidebarW    int
	ContentW    int
	InnerW      int
	EditorW     int // width of the editor pane (ContentW - PadLR*2)
	ChatAreaTop int
	ChatH       int
	PadLR       int
}

func (m *App) computeLayout() LayoutInfo {
	sidebarW := 0
	if m.tree.Visible && m.tree.Model != nil && m.tree.Renderer != nil {
		sidebarW = m.tree.Width + 1
	}

	contentW := m.width - sidebarW
	padLR := m.theme.Spacing.PadLR
	innerW := contentW - padLR*2
	if innerW < 20 {
		innerW = 20
	}
	editorW := contentW - padLR*2
	if editorW < 20 {
		editorW = 20
	}

	chatAreaTop := 3
	if m.win.IsOpen() && m.win.TabCount() >= 1 {
		chatAreaTop += 2
	}

	return LayoutInfo{
		SidebarW:    sidebarW,
		ContentW:    contentW,
		InnerW:      innerW,
		EditorW:     editorW,
		ChatAreaTop: chatAreaTop,
		PadLR:       padLR,
	}
}
