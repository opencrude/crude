package viewmodel

type FocusTarget int

const (
	FocusNone FocusTarget = iota
	FocusTree
	FocusEditor
	FocusTabBar
	FocusInput
	FocusOverlay
	FocusFloatingWindow
	FocusLauncher
)

func (m *App) focusTarget() FocusTarget {
	if m.windowManager != nil && m.windowManager.Focused() != nil {
		return FocusFloatingWindow
	}
	if m.agentMgr.Count() == 0 && m.editorState.ActiveView == "" {
		return FocusLauncher
	}
	top := m.focusStack.Top()
	if top == FocusTree && m.tree.Model != nil && m.tree.Visible {
		return FocusTree
	}
	if top == FocusTabBar {
		return FocusTabBar
	}
	if m.editorState.ActiveView != "" && m.editorState.Editor.IsOpen() {
		return FocusEditor
	}
	if m.overlayState.IsOverlay() {
		return FocusOverlay
	}
	return FocusInput
}

func (m *App) unfocus() {
	m.focusStack.Clear()
}

func (m *App) setFocusTree() {
	m.focusStack.Clear()
	m.focusStack.Push(FocusTree)
}

func (m *App) setFocusTabBar() {
	m.focusStack.Clear()
	m.focusStack.Push(FocusTabBar)
}

func (m *App) syncRenderFocus() {
	if m.renderCtx == nil {
		return
	}
	focused := m.focusTarget() == FocusEditor
	m.renderCtx.Focused = focused
}
