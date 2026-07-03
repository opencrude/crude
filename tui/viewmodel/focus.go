package viewmodel

import "crude/tui/workspace"

type FocusTarget = workspace.FocusTarget

const (
	FocusNone           = workspace.FocusNone
	FocusTree           = workspace.FocusTree
	FocusEditor         = workspace.FocusEditor
	FocusTabBar         = workspace.FocusTabBar
	FocusInput          = workspace.FocusInput
	FocusOverlay        = workspace.FocusOverlay
	FocusFloatingWindow = workspace.FocusFloatingWindow
	FocusLauncher       = workspace.FocusLauncher
)

func (m *App) focusTarget() FocusTarget {
	if m.windowManager != nil && m.windowManager.Focused() != nil {
		return FocusFloatingWindow
	}
	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		return FocusLauncher
	}
	top := m.FocusStack.Top()
	if top == FocusTree && m.Tree.Model != nil && m.Tree.Visible {
		return FocusTree
	}
	if top == FocusTabBar {
		return FocusTabBar
	}
	if m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen() {
		return FocusEditor
	}
	if m.OverlayState.IsOverlay() {
		return FocusOverlay
	}
	return FocusInput
}

func (m *App) unfocus() {
	m.FocusStack.Clear()
}

func (m *App) setFocusTree() {
	m.FocusStack.Clear()
	m.FocusStack.Push(FocusTree)
}

func (m *App) setFocusTabBar() {
	m.FocusStack.Clear()
	m.FocusStack.Push(FocusTabBar)
}

func (m *App) syncRenderFocus() {
	if m.renderCtx == nil {
		return
	}
	focused := m.focusTarget() == FocusEditor
	m.renderCtx.Focused = focused
}
