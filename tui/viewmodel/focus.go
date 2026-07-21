package viewmodel

import "crude/workspace"

type FocusTarget = workspace.FocusTarget

const (
	FocusNone           = workspace.FocusNone
	FocusTree           = workspace.FocusTree
	FocusEditor         = workspace.FocusEditor
	FocusTabBar         = workspace.FocusTabBar
	FocusInput          = workspace.FocusInput
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
	return FocusInput
}

func (m *App) unfocus() {
	m.setSessionDeckFocused(false)
	m.FocusStack.Clear()
}

func (m *App) setFocusTree() {
	m.setSessionDeckFocused(false)
	m.FocusStack.Clear()
	m.FocusStack.Push(FocusTree)
}

func (m *App) setFocusTabBar() {
	m.setSessionDeckFocused(false)
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
