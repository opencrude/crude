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
	if len(m.agents) == 0 && m.activeEditorView == "" {
		return FocusLauncher
	}
	if m.windowManager != nil && m.windowManager.Focused() != nil {
		return FocusFloatingWindow
	}
	if m.treeFocused && m.treeModel != nil && m.treeVisible {
		return FocusTree
	}
	if m.tabBarFocused {
		return FocusTabBar
	}
	if m.activeEditorView != "" && m.editor.IsOpen() {
		return FocusEditor
	}
	if m.Mode != ModeChat {
		return FocusOverlay
	}
	return FocusInput
}

func (m *App) unfocus() {
	m.treeFocused = false
	m.tabBarFocused = false
}

func (m *App) setFocusTree() {
	m.unfocus()
	m.treeFocused = true
}

func (m *App) setFocusTabBar() {
	m.unfocus()
	m.tabBarFocused = true
}

func (m *App) syncRenderFocus() {
	if m.renderCtx == nil {
		return
	}
	focused := m.focusTarget() == FocusEditor
	m.renderCtx.Focused = focused
}
